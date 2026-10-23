package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"sync"

	"framestudio-proto/src/model"
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
//   - Shuttle at 4x and 8x either way: no decoder can produce 100-200 frames a second of
//     real footage, so the viewer is never blocked on sequential decoding. Each request
//     returns the newest picture at once and, when none is running, starts one keyframe
//     grab (seek, decode only the keyframe) at the playhead in the background. The work
//     is proportional to what is shown; requests while a grab runs are dropped.

const (
	reverseSeconds = 2  // length of one backward chunk
	reverseStep    = 16 // a request at most this many frames before the previous one walks backwards
)

// reverse tracks the newest request per stream, the backward chunks being decoded and
// the shuttle pictures.
type reverse struct {
	mu     sync.Mutex
	last   map[stream]int
	chunks map[stream]*chunk
	fast   map[stream]*shuttle
}

// shuttle is the newest picture of a stream at 4x/8x and whether a grab is running.
type shuttle struct {
	img  image.Image
	n    int
	busy bool
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

func (c *Cache) frame(m model.MediaItem, n int, fps model.Rate, w, h int, rate float64) (image.Image, error) {
	switch m.Kind {
	case model.KindImage:
		return c.Image(m, w, h)
	case model.KindAudio:
		return nil, fmt.Errorf("„%s“ hat kein Bild", m.Name)
	}
	fps = fps.Or(model.Rate25).Reduced()
	w, h = fitSize(m.Width, m.Height, w, h)
	if w <= 0 || h <= 0 {
		return nil, errors.New("Bildgröße für die Vorschau ist ungültig")
	}
	n = clampFrame(m, n, fps)
	s := stream{id: m.ID, path: m.Path, fps: fps, w: w, h: h}
	prev, had := c.rev.note(s, n)
	key := s.frameKey(n)
	if v, ok := c.frames.get(key); ok {
		c.hits.Add(1)
		if rate < 0 && rate > -4 {
			c.readBack(m, s, n)
		}
		return v.(image.Image), nil
	}
	if rate >= 2 || rate <= -2 {
		if img, ok := c.near(s, n, rate); ok {
			c.hits.Add(1)
			return img, nil
		}
	}
	if rate >= 4 || rate <= -4 {
		if img, ok := c.shuttleFrame(m, s, n); ok {
			c.hits.Add(1)
			return img, nil
		}
	}
	c.misses.Add(1)
	if _, err := os.Stat(m.Path); err != nil {
		c.noteMissing(m.Path)
		return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	if rate >= 4 || rate <= -4 {
		return c.firstShuttle(m, s, n)
	}
	backward := rate < 0 || (rate == 0 && had && n < prev && prev-n <= reverseStep)
	if backward {
		img, err := c.backward(m, s, n)
		if err == nil && rate < 0 {
			c.readBack(m, s, n)
		}
		return img, err
	}
	p, err := c.prefetcherFor(m, s, n)
	if err != nil {
		return nil, err
	}
	img, err := p.wait(n)
	if err != nil || img != nil {
		return img, err
	}
	return c.single(m, s, n)
}

// near returns a cached frame next to n (the one in playing direction first) and keeps
// the decoder for s moving.
func (c *Cache) near(s stream, n int, rate float64) (image.Image, bool) {
	order := []int{n - 1, n + 1}
	if rate < 0 {
		order = []int{n + 1, n - 1}
	}
	for _, k := range order {
		if k < 0 {
			continue
		}
		if v, ok := c.frames.get(s.frameKey(k)); ok {
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
			return v.(image.Image), true
		}
	}
	return nil, false
}

// single decodes frame n on its own and caches it.
func (c *Cache) single(m model.MediaItem, s stream, n int) (image.Image, error) {
	g := c.gen(m.ID)
	ctx, cancel := context.WithTimeout(c.ctx, posterTimeout)
	defer cancel()
	one, err := decodeOne(ctx, m, frameArgs(m.Path, n, s.fps, s.w, s.h, 1), s.w, s.h)
	if err != nil {
		return nil, err
	}
	c.putIf(c.frames, g, s.frameKey(n), m.ID, one, int64(s.w*s.h*4))
	return one, nil
}

// chunkFrames is the number of frames of one backward chunk of s, limited so a chunk
// fills at most a quarter of the frame cache.
func (c *Cache) chunkFrames(s stream) int {
	n := reverseSeconds * s.fps.Nominal()
	if limit := int(c.frames.getBudget() / 4 / int64(s.w*s.h*4)); n > limit {
		n = max(limit, 1)
	}
	return n
}

// backward returns frame n, decoding the chunk that ends at n when it is not cached.
// Requests inside a chunk that is being decoded wait for it instead of starting another.
func (c *Cache) backward(m model.MediaItem, s stream, n int) (image.Image, error) {
	c.rev.mu.Lock()
	if c.rev.chunks == nil {
		c.rev.chunks = map[stream]*chunk{}
	}
	if ch := c.rev.chunks[s]; ch != nil && n >= ch.start && n <= ch.end {
		c.rev.mu.Unlock()
		<-ch.done
		if v, ok := c.frames.get(s.frameKey(n)); ok {
			return v.(image.Image), nil
		}
		if ch.err != nil {
			return nil, ch.err
		}
		return c.single(m, s, n)
	}
	ch := &chunk{start: max(0, n-c.chunkFrames(s)+1), end: n, done: make(chan struct{})}
	c.rev.chunks[s] = ch
	c.rev.mu.Unlock()

	ch.err = c.decodeChunk(m, s, ch.start, ch.end)
	c.rev.mu.Lock()
	if c.rev.chunks[s] == ch {
		delete(c.rev.chunks, s)
	}
	c.rev.mu.Unlock()
	close(ch.done)

	if v, ok := c.frames.get(s.frameKey(n)); ok {
		return v.(image.Image), nil
	}
	if ch.err != nil {
		return nil, ch.err
	}
	return c.single(m, s, n) // evicted already or past the end of the file
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
	cmd := exec.CommandContext(ctx, bin, frameArgs(m.Path, start, s.fps, s.w, s.h, end-start+1)...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return decodeError(m, "", err)
	}
	r := bufio.NewReaderSize(stdout, 1<<20)
	size := s.w * s.h * 4
	got := 0
	for n := start; n <= end; n++ {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		c.putIf(c.frames, g, s.frameKey(n), m.ID, rgbaFrame(buf, s.w, s.h), int64(size))
		got++
	}
	cancel()
	werr := cmd.Wait()
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
func (c *Cache) readBack(m model.MediaItem, s stream, n int) {
	size := c.chunkFrames(s)
	end := -1
	for k := n - 1; k >= max(0, n-size/2); k-- {
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

// shuttleFrame serves 4x/8x shuttle: the newest picture of s at once, starting a
// keyframe grab at n in the background unless one is running. ok is false before the
// stream has any picture.
func (c *Cache) shuttleFrame(m model.MediaItem, s stream, n int) (image.Image, bool) {
	c.rev.mu.Lock()
	st := c.rev.fast[s]
	if st == nil || st.img == nil {
		c.rev.mu.Unlock()
		return nil, false
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
	return img, true
}

// firstShuttle grabs the first shuttle picture of s, blocking once.
func (c *Cache) firstShuttle(m model.MediaItem, s stream, n int) (image.Image, error) {
	ctx, release := c.jobContext(s.id)
	defer release()
	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()
	img, err := decodeOne(ctx, m, keyframeArgs(m.Path, n, s.fps, s.w, s.h), s.w, s.h)
	if err != nil {
		return c.single(m, s, n) // e.g. no keyframe index: decode exactly
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
	return img, nil
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
		img, err := decodeOne(ctx, m, keyframeArgs(m.Path, n, s.fps, s.w, s.h), s.w, s.h)
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
