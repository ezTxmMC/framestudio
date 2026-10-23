package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

// Playback at other rates than 1x forward:
//
//   - Reverse (J): decoding single frames backwards would seek for every frame. Instead,
//     when requests walk backwards, the ~2 s before the requested frame are decoded in one
//     ffmpeg run (seek to the chunk start, decode forward into the frame cache), so the
//     following backward requests are cache hits.
//     While the playhead is in the lower half of the newest chunk, the next older chunk is
//     decoded in the background (one per stream), so reverse play does not stall at
//     chunk boundaries.
//   - Fast forward (2x..8x): with a fast hint the nearest cached frame within ±1 frame is
//     served when the exact one is not decoded yet, instead of waiting for it.
//   - Shuttle at 4x and 8x either way: whether decoding keeps up is measured (frames per
//     second the decoders of the stream really deliver). When it does, shuttle is exact:
//     forward from the prefetch ring, reverse from backward chunks, and a request never
//     waits for a frame that is not decoded yet (the newest nearby picture stands in).
//     When it does not (4K camera files on a weak CPU), shuttle is keyframe-only: each
//     request returns the newest picture at once and, when none is running, starts one
//     keyframe grab (seek, decode only the keyframe) at the playhead in the background.
//     The work is proportional to what is shown; requests while a grab runs are dropped.
//     Results flagged KFOnly tell the UI ("Nur Keyframes").

const (
	reverseSeconds = 2  // length of one backward chunk
	reverseStep    = 16 // a request at most this many frames before the previous one walks backwards
	// shuttleHeadroom is how much faster than the shuttle needs decoding must be to count
	// as keeping up.
	shuttleHeadroom = 1.4
)

// reverse tracks the newest request per stream, the backward chunks being decoded and
// the shuttle pictures.
type reverse struct {
	mu     sync.Mutex
	last   map[stream]int
	chunks map[stream]*chunk
	fast   map[stream]*shuttle
	speed  map[stream]float64 // decoded frames per second, smoothed
}

// shuttle is the newest picture of a stream at 4x/8x and whether a grab is running.
type shuttle struct {
	img  image.Image
	n    int
	busy bool
}

// frameRes is a frame with the reference its reader holds on the pooled buffer.
type frameRes struct {
	img     image.Image
	src     SourceKind
	approx  bool // a neighbour or a keyframe stands in for the wanted frame
	kfOnly  bool // keyframe-only shuttle
	release func()
}

func (r frameRes) done() {
	if r.release != nil {
		r.release()
	}
}

// cached returns frame n of s from the frame cache with a reference.
func (c *Cache) cached(s stream, n int) (frameRes, bool) {
	v, src, rel, ok := c.frames.getRef(s.frameKey(n))
	if !ok {
		return frameRes{}, false
	}
	return frameRes{img: v.(image.Image), src: src, release: rel}, true
}

// noteSpeed folds the time one frame took to decode into the stream's speed estimate.
func (c *Cache) noteSpeed(s stream, perFrame time.Duration) {
	if perFrame <= 0 {
		return
	}
	fps := float64(time.Second) / float64(perFrame)
	c.rev.mu.Lock()
	defer c.rev.mu.Unlock()
	if c.rev.speed == nil {
		c.rev.speed = map[stream]float64{}
	}
	if old := c.rev.speed[s]; old > 0 {
		fps = old*0.8 + fps*0.2
	}
	c.rev.speed[s] = fps
}

// DecodeSpeed reports how many frames per second the decoders of a rendition delivered
// lately (0 = unknown).
func (c *Cache) DecodeSpeed(m model.MediaItem, fps model.Rate, w, h int) float64 {
	fps = fps.Or(model.Rate25).Reduced()
	w, h = fitSize(m.Width, m.Height, w, h)
	c.rev.mu.Lock()
	defer c.rev.mu.Unlock()
	return c.rev.speed[stream{id: m.ID, path: m.Path, fps: fps, w: w, h: h}]
}

// keyframeOnly reports whether shuttling at |rate| >= 4 must be keyframe-only for s.
func (c *Cache) keyframeOnly(s stream, rate float64) bool {
	if c.shuttleKF.Load() {
		return true
	}
	c.rev.mu.Lock()
	sp := c.rev.speed[s]
	c.rev.mu.Unlock()
	if sp == 0 {
		return false // unknown: try, the first decodes measure it
	}
	return sp < shuttleHeadroom*math.Abs(rate)*s.fps.Float()
}

// forget drops the request history and shuttle pictures of a media item.
func (r *reverse) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for s := range r.last {
		if s.id == id {
			delete(r.last, s)
		}
	}
	for s := range r.fast {
		if s.id == id {
			delete(r.fast, s)
		}
	}
	for s := range r.speed {
		if s.id == id {
			delete(r.speed, s)
		}
	}
}

// chunk is one running backward decode of frames start..end.
type chunk struct {
	start, end int
	done       chan struct{}
	err        error
}

// note records n as the newest request of s and returns the previous one.
func (r *reverse) note(s stream, n int) (prev int, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[stream]int{}
	}
	prev, ok = r.last[s]
	r.last[s] = n
	return prev, ok
}

// PlaybackFrame is VideoFrame for a playing viewer. rate is the session's playback rate
// (1 normal, 2/4/8 fast forward, negative = reverse, 0 = unknown). Negative rates decode
// backwards in chunks; |rate| >= 2 lets a frame within ±1 of n stand in when n itself is
// not decoded yet. With rate 0 or 1 it behaves exactly like VideoFrame.
func (c *Cache) PlaybackFrame(m model.MediaItem, n int, fps model.Rate, w, h int, rate float64) (image.Image, error) {
	return c.frame(m, n, fps, w, h, rate)
}

// frame is the plain API: originals only, images are kept by the caller for as long as
// they like, so the reference taken on a pooled buffer is never dropped (the buffer then
// ends up with the garbage collector instead of the pool).
func (c *Cache) frame(m model.MediaItem, n int, fps model.Rate, w, h int, rate float64) (image.Image, error) {
	res, err := c.frameX(context.Background(), m, n, fps, w, h, rate, frameOpts{})
	if err != nil {
		return nil, err
	}
	return res.img, nil
}

// frameOpts are the per-request choices of VideoFrameInto.
type frameOpts struct {
	proxy  string // proxy file to decode instead of the original
	ladder bool   // keyframe-only shuttle may answer with keyframe ladder pictures (smaller than asked)
}

// frameX returns frame n of m. The result holds a reference on its buffer; call done.
func (c *Cache) frameX(ctx context.Context, m model.MediaItem, n int, fps model.Rate, w, h int, rate float64, o frameOpts) (frameRes, error) {
	switch m.Kind {
	case model.KindImage:
		img, err := c.Image(m, w, h)
		return frameRes{img: img, release: func() {}}, err
	case model.KindAudio:
		return frameRes{}, fmt.Errorf("„%s“ hat kein Bild", m.Name)
	}
	if err := ctx.Err(); err != nil {
		return frameRes{}, err
	}
	fps = fps.Or(model.Rate25).Reduced()
	w, h = fitSize(m.Width, m.Height, w, h)
	if w <= 0 || h <= 0 {
		return frameRes{}, errors.New("Bildgröße für die Vorschau ist ungültig")
	}
	n = clampFrame(m, n, fps)
	s := stream{id: m.ID, path: m.Path, fps: fps, w: w, h: h, proxy: o.proxy}
	prev, had := c.rev.note(s, n)
	if r, ok := c.cached(s, n); ok {
		c.hits.Add(1)
		if rate < 0 {
			c.readBack(m, s, n, rate)
		}
		return r, nil
	}
	if rate >= 2 || rate <= -2 {
		if r, ok := c.near(s, n, rate); ok {
			c.hits.Add(1)
			return r, nil
		}
	}
	shuttleKF := (rate >= 4 || rate <= -4) && c.keyframeOnly(s, rate)
	if shuttleKF && o.ladder {
		// Keyframe-only shuttle straight from the ladder: every keyframe picture is in RAM
		// already, so nothing is decoded while the playhead runs.
		if r, ok := c.ladderRes(m, s, n, o); ok {
			c.hits.Add(1)
			return r, nil
		}
	}
	if shuttleKF {
		if r, ok := c.shuttleFrame(m, s, n); ok {
			c.hits.Add(1)
			return r, nil
		}
	}
	c.misses.Add(1)
	if _, err := os.Stat(m.Path); err != nil {
		c.noteMissing(m.Path)
		return frameRes{}, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	if shuttleKF {
		return c.firstShuttle(ctx, m, s, n)
	}
	backward := rate < 0 || (rate == 0 && had && n < prev && prev-n <= reverseStep)
	if backward {
		r, err := c.backward(ctx, m, s, n, rate, o)
		if err == nil && rate < 0 {
			c.readBack(m, s, n, rate)
		}
		return r, err
	}
	jump := 2 * s.fps.Nominal()
	if rate >= 4 {
		jump = max(jump, int(rate)*s.fps.Nominal()) // a second of playhead movement behind is not worth a restart
	}
	p, err := c.prefetcherForJump(m, s, n, jump)
	if err != nil {
		return frameRes{}, err
	}
	if rate >= 4 {
		// Exact fast forward never waits for a picture once there is one to show.
		p.demand(n)
		if r, ok := p.newest(); ok {
			return r, nil
		}
		if r, ok := c.ladderRes(m, s, n, o); ok {
			return r, nil
		}
	}
	r, ok, err := p.wait(ctx, n)
	if err != nil || ok {
		return r, err
	}
	return c.single(ctx, m, s, n)
}

// ladderRes answers with the keyframe ladder picture at n, a small picture flagged
// keyframe-only, when the caller accepts those (VideoFrameInto) and the ladder is built.
func (c *Cache) ladderRes(m model.MediaItem, s stream, n int, o frameOpts) (frameRes, bool) {
	if !o.ladder {
		return frameRes{}, false
	}
	img := c.ladderFor(m).at(s.fps.FramesToDuration(n))
	if img == nil {
		return frameRes{}, false
	}
	return frameRes{img: img, src: SrcCache, approx: true, kfOnly: true, release: func() {}}, true
}

// near returns a cached frame next to n (the one in playing direction first) and keeps
// the decoder for s moving.
func (c *Cache) near(s stream, n int, rate float64) (frameRes, bool) {
	order := []int{n - 1, n + 1}
	if rate < 0 {
		order = []int{n + 1, n - 1}
	}
	for _, k := range order {
		if k < 0 {
			continue
		}
		if r, ok := c.cached(s, k); ok {
			if rate > 0 {
				c.pmu.Lock()
				for _, p := range c.prefetchers {
					if p.s == s && p.covers(n, 2*s.fps.Nominal()) {
						p.demand(n)
						break
					}
				}
				c.pmu.Unlock()
			}
			r.approx = true
			return r, true
		}
	}
	return frameRes{}, false
}

// standIn finds a cached picture close to n for reverse shuttle while the chunk that
// holds n is still being decoded: the frames just passed (above n) first.
func (c *Cache) standIn(s stream, n int) (frameRes, bool) {
	for d := 1; d <= 2*reverseStep; d++ {
		for _, k := range []int{n + d, n - d} {
			if k < 0 {
				continue
			}
			if r, ok := c.cached(s, k); ok {
				r.approx = true
				return r, true
			}
		}
	}
	c.rev.mu.Lock()
	defer c.rev.mu.Unlock()
	if st := c.rev.fast[s]; st != nil && st.img != nil {
		return frameRes{img: st.img, approx: true, release: func() {}}, true
	}
	return frameRes{}, false
}

// single decodes frame n on its own and caches it.
func (c *Cache) single(ctx context.Context, m model.MediaItem, s stream, n int) (frameRes, error) {
	g := c.gen(m.ID)
	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()
	o := decodeOpts{}
	o.seek, o.hasSeek = c.keyframeSeek(m, s.fps, n)
	one, err := decodeOneG(ctx, c.gateOf(), workgate.PrioPlayback, m, frameArgsOpt(s.file(), n, s.fps, s.w, s.h, 1, o), s.w, s.h)
	if err != nil {
		return frameRes{}, err
	}
	c.putIf(c.frames, g, s.frameKey(n), m.ID, one, int64(s.w*s.h*4))
	return frameRes{img: one, src: s.kind(), release: func() {}}, nil
}

// chunkFrames is the number of frames of one backward chunk of s, limited so a chunk
// fills at most a quarter of the frame cache. Faster reverse needs longer chunks.
func (c *Cache) chunkFrames(s stream, rate float64) int {
	secs := float64(reverseSeconds)
	if r := math.Abs(rate); r > 2*reverseSeconds {
		secs = r / 2
	}
	n := int(secs * float64(s.fps.Nominal()))
	if limit := int(c.frames.getBudget() / 4 / int64(s.w*s.h*4)); n > limit {
		n = max(limit, 1)
	}
	return n
}

// backward returns frame n, decoding the chunk that ends at n when it is not cached.
// Requests inside a chunk that is being decoded wait for it instead of starting another,
// except fast reverse (rate <= -2), which shows a nearby picture when it has one.
func (c *Cache) backward(ctx context.Context, m model.MediaItem, s stream, n int, rate float64, o frameOpts) (frameRes, error) {
	c.rev.mu.Lock()
	if c.rev.chunks == nil {
		c.rev.chunks = map[stream]*chunk{}
	}
	ch := c.rev.chunks[s]
	if ch == nil || n < ch.start || n > ch.end {
		ch = &chunk{start: max(0, n-c.chunkFrames(s, rate)+1), end: n, done: make(chan struct{})}
		c.rev.chunks[s] = ch
		c.rev.mu.Unlock()
		c.goChunk(m, s, ch)
	} else {
		c.rev.mu.Unlock()
	}
	if rate <= -2 {
		if r, ok := c.standIn(s, n); ok {
			return r, nil
		}
		if r, ok := c.ladderRes(m, s, n, o); ok {
			return r, nil
		}
	}
	select {
	case <-ch.done:
	case <-ctx.Done():
		return frameRes{}, ctx.Err()
	}
	if r, ok := c.cached(s, n); ok {
		return r, nil
	}
	if ch.err != nil {
		return frameRes{}, ch.err
	}
	return c.single(ctx, m, s, n) // evicted already or past the end of the file
}

// goChunk decodes ch in the background, as the single decoder of the stream's chunks.
func (c *Cache) goChunk(m model.MediaItem, s stream, ch *chunk) {
	go func() {
		ch.err = c.decodeChunk(m, s, ch.start, ch.end)
		c.rev.mu.Lock()
		if c.rev.chunks[s] == ch {
			delete(c.rev.chunks, s)
		}
		c.rev.mu.Unlock()
		close(ch.done)
	}()
}

// decodeChunk decodes frames start..end of s in one ffmpeg run into the frame cache.
func (c *Cache) decodeChunk(m model.MediaItem, s stream, start, end int) error {
	bin, err := ffmpegPath()
	if err != nil {
		return err
	}
	c.pmu.Lock()
	closed := c.closed
	if !closed {
		c.wg.Add(1)
	}
	c.pmu.Unlock()
	if closed {
		return errors.New("Medien-Cache ist geschlossen")
	}
	defer c.wg.Done()
	g := c.gen(m.ID)
	job, release := c.jobContext(m.ID) // ends with Cancel(mediaID) or Invalidate too
	defer release()
	ctx, cancel := context.WithTimeout(job, frameWait)
	defer cancel()
	var stderr tailBuffer
	o := decodeOpts{threads: c.decoderThreads()}
	o.seek, o.hasSeek = c.keyframeSeek(m, s.fps, start)
	cmd := c.gateOf().Command(ctx, workgate.PrioPlayback, bin, frameArgsOpt(s.file(), start, s.fps, s.w, s.h, end-start+1, o)...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	var done func()
	if err == nil {
		done, err = workgate.StartCmd(c.gateOf(), cmd, workgate.PrioPlayback)
	}
	if err != nil {
		return decodeError(m, "", err)
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	size := s.w * s.h * 4
	got := 0
	began := time.Now()
	for n := start; n <= end; n++ {
		pf := c.pool.newFrame(size)
		if _, err := io.ReadFull(r, pf.buf); err != nil {
			pf.release()
			break
		}
		c.putIfPF(c.frames, g, s.frameKey(n), m.ID, rgbaFrame(pf.buf, s.w, s.h), int64(size), pf, s.kind())
		pf.release() // the cache holds its own reference now
		got++
	}
	cancel()
	werr := cmd.Wait()
	done()
	if got > 1 {
		c.noteSpeed(s, time.Since(began)/time.Duration(got))
	}
	if got == 0 {
		if ctx.Err() != nil && c.ctx.Err() != nil {
			return fmt.Errorf("Dekodieren von „%s“ abgebrochen: %w", m.Name, c.ctx.Err())
		}
		return decodeError(m, stderr.String(), werr)
	}
	return nil
}

// readBack starts decoding the chunk below the playhead in the background once reverse
// play is in the lower half of the frames decoded so far, at most one chunk per stream.
func (c *Cache) readBack(m model.MediaItem, s stream, n int, rate float64) {
	size := c.chunkFrames(s, rate)
	// Start the next chunk early enough that it is decoded before the playhead gets there:
	// at least half a chunk ahead, more when decoding is slow compared to the shuttle.
	lead := size / 2
	c.rev.mu.Lock()
	sp := c.rev.speed[s]
	c.rev.mu.Unlock()
	if sp > 0 {
		decodeSecs := float64(size)/sp + 0.25 // process start and the walk from the keyframe
		lead = max(lead, int(math.Abs(rate)*s.fps.Float()*decodeSecs*1.3))
	}
	lead = min(lead, size)
	end := -1
	for k := n - 1; k >= max(0, n-lead); k-- {
		if _, ok := c.frames.peek(s.frameKey(k)); !ok {
			end = k
			break
		}
	}
	if end < 0 {
		return // the next half chunk is decoded already
	}
	c.rev.mu.Lock()
	if c.rev.chunks == nil {
		c.rev.chunks = map[stream]*chunk{}
	}
	if c.rev.chunks[s] != nil {
		c.rev.mu.Unlock()
		return
	}
	ch := &chunk{start: max(0, end-size+1), end: end, done: make(chan struct{})}
	c.rev.chunks[s] = ch
	c.rev.mu.Unlock()
	c.goChunk(m, s, ch)
}

// shuttleFrame serves keyframe-only shuttle: the newest picture of s at once, starting a
// keyframe grab at n in the background unless one is running. ok is false before the
// stream has any picture.
func (c *Cache) shuttleFrame(m model.MediaItem, s stream, n int) (frameRes, bool) {
	c.rev.mu.Lock()
	st := c.rev.fast[s]
	if st == nil || st.img == nil {
		c.rev.mu.Unlock()
		return frameRes{}, false
	}
	img := st.img
	start := !st.busy && st.n != n
	if start {
		st.busy = true
	}
	c.rev.mu.Unlock()
	if start && !c.goGrab(m, s, n, st) {
		c.rev.mu.Lock()
		st.busy = false
		c.rev.mu.Unlock()
	}
	return frameRes{img: img, src: s.kind(), approx: true, kfOnly: true, release: func() {}}, true
}

// firstShuttle grabs the first shuttle picture of s, blocking once.
func (c *Cache) firstShuttle(ctx context.Context, m model.MediaItem, s stream, n int) (frameRes, error) {
	jctx, release := c.jobContext(s.id)
	defer release()
	jctx, cancel := context.WithTimeout(jctx, posterTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	img, err := decodeOneG(jctx, c.gateOf(), workgate.PrioPlayback, m, keyframeArgs(s.file(), n, s.fps, s.w, s.h), s.w, s.h)
	if err != nil {
		if ctx.Err() != nil {
			return frameRes{}, ctx.Err()
		}
		return c.single(ctx, m, s, n) // e.g. no keyframe index: decode exactly
	}
	c.rev.mu.Lock()
	if c.rev.fast == nil {
		c.rev.fast = map[stream]*shuttle{}
	}
	if c.rev.fast[s] == nil {
		c.rev.fast[s] = &shuttle{}
	}
	st := c.rev.fast[s]
	if !st.busy {
		st.img, st.n = img, n
	}
	c.rev.mu.Unlock()
	return frameRes{img: img, src: s.kind(), approx: true, kfOnly: true, release: func() {}}, nil
}

// goGrab runs one keyframe grab at n for st in the background; false when the cache is
// closed.
func (c *Cache) goGrab(m model.MediaItem, s stream, n int, st *shuttle) bool {
	c.pmu.Lock()
	if c.closed {
		c.pmu.Unlock()
		return false
	}
	c.wg.Add(1)
	c.pmu.Unlock()
	g := c.gen(s.id)
	go func() {
		defer c.wg.Done()
		ctx, release := c.jobContext(s.id)
		defer release()
		ctx, cancel := context.WithTimeout(ctx, posterTimeout)
		defer cancel()
		img, err := decodeOneG(ctx, c.gateOf(), workgate.PrioPlayback, m, keyframeArgs(s.file(), n, s.fps, s.w, s.h), s.w, s.h)
		c.rev.mu.Lock()
		defer c.rev.mu.Unlock()
		st.busy = false
		if err == nil && c.gen(s.id) == g {
			st.img, st.n = img, n
		}
	}()
	return true
}

// keyframeArgs builds an ffmpeg command line that writes the keyframe at or before frame
// n as raw RGBA: only keyframes are decoded, so a grab costs one frame even in long GOPs.
func keyframeArgs(path string, n int, fps model.Rate, w, h int) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-skip_frame", "nokey"}
	if t := fps.FramesToDuration(n); t > 0 {
		args = append(args, "-ss", model.DurationSeconds(t))
	}
	args = append(args, "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1")
	return append(args, "-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h), "-f", "rawvideo", "-pix_fmt", "rgba", "-")
}
