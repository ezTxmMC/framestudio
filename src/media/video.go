package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"framestudio-proto/src/media/hwaccel"
	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

const (
	prefetchSeconds = 3                // how far a prefetcher decodes ahead of the last request
	maxPrefetchers  = 4                // default concurrent background decoders per cache
	prefetchIdle    = 2 * time.Second  // a decoder nobody asks for exits after this
	frameWait       = 20 * time.Second // longest a request waits for its frame
)

// fitSize returns the largest size with the aspect of sw x sh that fits into w x h.
func fitSize(sw, sh, w, h int) (int, int) {
	if sw <= 0 || sh <= 0 {
		return w, h
	}
	if w <= 0 || h <= 0 {
		return sw, sh
	}
	if sw*h > sh*w { // wider than the box
		return w, max(1, (sh*w+sw/2)/sw)
	}
	return max(1, (sw*h+sh/2)/sh), h
}

// clampFrame keeps frame n (at fps) inside the media duration.
func clampFrame(m model.MediaItem, n int, fps model.Rate) int {
	if n < 0 {
		n = 0
	}
	if m.Duration > 0 {
		last := fps.DurationToFrames(m.Duration+time.Microsecond) - 1
		if n > last {
			n = max(last, 0)
		}
	}
	return n
}

// seekLead is how far before the wanted frame decoding starts, so the frame rate filter
// sees the source frame that precedes the target time.
const seekLead = 500 * time.Millisecond

// decodeOpts tunes a frame decoder command.
type decodeOpts struct {
	seek    time.Duration // exact input seek (a keyframe time) when hasSeek
	hasSeek bool
	hw      *hwaccel.Args // hardware decode plan
	threads int           // decoder threads, 0 = ffmpeg default
}

// frameArgs builds an ffmpeg command line that writes frames n, n+1, ... of a video
// resampled to fps as raw RGBA of w x h to stdout; frames > 0 stops after that many.
//
// Timestamps stay absolute (-copyts) and the fps filter runs before the cut, so frame n
// is the same source frame whether decoding starts at 0 or seeks: random access, the
// prefetchers and sequential export all show identical frames for every rate pair.
func frameArgs(path string, n int, fps model.Rate, w, h, frames int) []string {
	return frameArgsOpt(path, n, fps, w, h, frames, decodeOpts{})
}

func frameArgsOpt(path string, n int, fps model.Rate, w, h, frames int, o decodeOpts) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	t := fps.FramesToDuration(n) - seekLead
	if o.hasSeek {
		t = o.seek // a keyframe at or before frame n: nothing earlier is needed
	}
	if o.hw != nil {
		args = append(args, o.hw.Input...)
	}
	if o.threads > 0 {
		args = append(args, "-threads", strconv.Itoa(o.threads))
	}
	if t > 0 {
		args = append(args, "-ss", model.DurationSeconds(t))
	}
	args = append(args, "-copyts", "-start_at_zero", "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn")
	if frames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(frames))
	}
	vf := fmt.Sprintf("fps=%s,trim=start_pts=%d,scale=%d:%d:flags=bicubic", fps.Fraction(), n, w, h)
	if o.hw != nil && o.hw.Filter != "" {
		vf = o.hw.Filter + "," + vf
	}
	return append(args, "-vf", vf, "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "rgba", "-")
}

// withThreads limits the decoder threads of a command line (inserted before the first -i).
func withThreads(args []string, n int) []string {
	for i, a := range args {
		if a == "-i" {
			out := append([]string(nil), args[:i]...)
			out = append(out, "-threads", strconv.Itoa(n))
			return append(out, args[i:]...)
		}
	}
	return args
}

// posterArgs builds an ffmpeg command line that writes the frame at time t as raw RGBA.
func posterArgs(path string, t float64, w, h int) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if t > 0 {
		args = append(args, "-ss", strconv.FormatFloat(t, 'f', 6, 64))
	}
	args = append(args, "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1")
	return append(args, "-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h), "-f", "rawvideo", "-pix_fmt", "rgba", "-")
}

func rgbaFrame(buf []byte, w, h int) *image.RGBA {
	return &image.RGBA{Pix: buf, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
}

// tailBuffer keeps the first bytes of a process's stderr for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 2048 - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// decodeOne runs ffmpeg with args and returns the first raw w x h frame it writes.
func decodeOne(ctx context.Context, m model.MediaItem, args []string, w, h int) (*image.RGBA, error) {
	return decodeOneG(ctx, workgate.NopGate{}, workgate.PrioPlayback, m, args, w, h)
}

// decodeOneG is decodeOne through a work gate: the process gets the priority of the job
// and is suspended with it.
func decodeOneG(ctx context.Context, g workgate.Gate, prio workgate.Prio, m model.MediaItem, args []string, w, h int) (*image.RGBA, error) {
	bin, err := ffmpegPath()
	if err != nil {
		return nil, err
	}
	size := w * h * 4
	stdout := bytes.NewBuffer(make([]byte, 0, size+512))
	var stderr tailBuffer
	cmd := g.Command(ctx, prio, bin, args...)
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	done, runErr := workgate.StartCmd(g, cmd, prio)
	if runErr == nil {
		runErr = cmd.Wait()
		done()
	}
	if stdout.Len() < size {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Dekodieren von „%s“ abgebrochen: %w", m.Name, ctx.Err())
		}
		return nil, decodeError(m, stderr.String(), runErr)
	}
	return rgbaFrame(stdout.Bytes()[:size], w, h), nil
}

func decodeError(m model.MediaItem, stderr string, err error) error {
	if stderr == "" && err == nil {
		return fmt.Errorf("„%s“ enthält an dieser Stelle kein Bild", m.Name)
	}
	return fmt.Errorf("„%s“ kann nicht dekodiert werden. Datei prüfen oder neu verknüpfen (%s)", m.Name, firstLine(stderr, err))
}

// stream identifies one decoded rendition of a video: same file, rate and output size.
type stream struct {
	id, path string
	fps      model.Rate
	w, h     int
	proxy    string // decode this proxy file instead of path (preview only)
}

func (s stream) frameKey(n int) string {
	if s.proxy != "" {
		return fmt.Sprintf("vf|%s|%s|%s|%dx%d|px|%d", s.id, s.path, s.fps.Fraction(), s.w, s.h, n)
	}
	return fmt.Sprintf("vf|%s|%s|%s|%dx%d|%d", s.id, s.path, s.fps.Fraction(), s.w, s.h, n)
}

// file is what ffmpeg opens for the stream.
func (s stream) file() string {
	if s.proxy != "" {
		return s.proxy
	}
	return s.path
}

// kind is how frames of the stream are produced when no hardware is involved.
func (s stream) kind() SourceKind {
	if s.proxy != "" {
		return SrcProxy
	}
	return SrcSW
}

// prefetcher runs one ffmpeg process that decodes a stream sequentially into the
// frame cache, staying at most "ahead" frames in front of the newest request.
type prefetcher struct {
	c      *Cache
	m      model.MediaItem
	s      stream
	start  int
	ahead  int
	gen    uint64 // invalidation generation of the media item at start
	cancel context.CancelFunc

	mu      sync.Mutex
	next    int // next frame the decoder will produce
	want    int // newest requested frame
	used    time.Time
	done    bool
	stopped bool // ended by stop, not by the end of the file
	err     error
	tail    image.Image // last decoded frame, held when the file ends early
	tailPF  *pframe     // reference held on tail's buffer
	tailSrc SourceKind
	changed chan struct{}
}

func (p *prefetcher) signal() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *prefetcher) stop() { p.cancel() }

// demand raises the newest requested frame to n without waiting for it.
func (p *prefetcher) demand(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.used = time.Now()
	if n > p.want {
		p.want = n
		p.signal()
	}
}

// covers reports whether frame n will soon be produced by this prefetcher.
func (p *prefetcher) covers(n, jump int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.done && n >= p.next && n <= p.next+jump
}

func (p *prefetcher) isDone() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *prefetcher) lastUsed() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.used
}

// newest returns the most recently decoded frame as a stand-in (false before the first).
func (p *prefetcher) newest() (frameRes, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tail == nil || p.stopped {
		return frameRes{}, false
	}
	res := frameRes{img: p.tail, src: p.tailSrc, approx: true, release: func() {}}
	if pf := p.tailPF; pf != nil {
		pf.refs.Add(1)
		var once sync.Once
		res.release = func() { once.Do(pf.release) }
	}
	return res, true
}

// hwFirstFrame is how long a hardware decoder may take for its first picture before the
// stream restarts in software.
const hwFirstFrame = 2 * time.Second

func (p *prefetcher) run(ctx context.Context, bin string) {
	hw, info, hwOK := p.c.hwPlan(p.m, p.s)
	if hwOK {
		if !p.runOnce(ctx, bin, &hw) {
			return
		}
		p.c.hwPlanner().Fail(p.m.Path, info) // no picture in time or an error: software from now on
	}
	p.runOnce(ctx, bin, nil)
}

// runOnce runs one ffmpeg process. It reports true when a hardware attempt produced
// nothing and the caller should retry in software; the prefetcher is not finished then.
func (p *prefetcher) runOnce(ctx context.Context, bin string, hw *hwaccel.Args) (retrySoftware bool) {
	c := p.c
	actx, acancel := context.WithCancel(ctx)
	defer acancel()
	var stderr tailBuffer
	o := decodeOpts{hw: hw, threads: c.decoderThreads()}
	// a restart (hardware died mid-stream) continues where the decoder stopped
	p.mu.Lock()
	from := max(p.next, p.start)
	p.mu.Unlock()
	o.seek, o.hasSeek = c.keyframeSeek(p.m, p.s.fps, from)
	cmd := c.gateOf().Command(actx, workgate.PrioPlayback, bin, frameArgsOpt(p.s.file(), from, p.s.fps, p.s.w, p.s.h, 0, o)...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	var done func()
	if err == nil {
		done, err = workgate.StartCmd(c.gateOf(), cmd, workgate.PrioPlayback)
	}
	if err != nil {
		if hw != nil {
			return true
		}
		p.finish(decodeError(p.m, "", err))
		return false
	}
	var produced atomic.Int64
	eof := false
	if hw != nil {
		wd := time.AfterFunc(hwFirstFrame, func() {
			if produced.Load() == 0 {
				acancel()
			}
		})
		defer wd.Stop()
	}
	src := p.s.kind()
	if hw != nil {
		src = SrcHW
	}
	defer func() {
		eof = eof && actx.Err() == nil
		var werr error
		if eof {
			// The pipe closed by itself: let the process report how it ended instead of
			// killing it first (a kill would look like a crash).
			t := time.AfterFunc(2*time.Second, acancel)
			werr = cmd.Wait()
			t.Stop()
			acancel()
		} else {
			acancel()
			werr = cmd.Wait()
		}
		done()
		var xe *exec.ExitError
		died := eof && errors.As(werr, &xe) && ctx.Err() == nil // crashed or killed mid-stream
		if hw != nil && ctx.Err() == nil && (produced.Load() == 0 || died) {
			retrySoftware = true
			return
		}
		if died {
			p.mu.Lock()
			if p.tailPF != nil {
				p.tailPF.release()
				p.tailPF, p.tail = nil, nil
			}
			p.mu.Unlock()
			p.finish(decodeError(p.m, stderr.String(), werr)) // not the frozen last picture
			return
		}
		if produced.Load() == 0 && eof {
			p.finish(decodeError(p.m, stderr.String(), werr))
			return
		}
		p.mu.Lock()
		p.stopped = !eof
		if p.stopped && p.tailPF != nil {
			p.tailPF.release()
			p.tailPF, p.tail = nil, nil
		}
		p.mu.Unlock()
		p.finish(nil)
	}()

	r := bufio.NewReaderSize(stdout, 1<<20)
	size := p.s.w * p.s.h * 4
	var lastRead time.Time
	for {
		waited, ok := p.awaitDemand(actx)
		if !ok {
			return false
		}
		if waited {
			lastRead = time.Time{}
		}
		pf := c.pool.newFrame(size)
		if _, err := io.ReadFull(r, pf.buf); err != nil {
			pf.release()
			eof = true
			return false
		}
		now := time.Now()
		if !lastRead.IsZero() {
			c.noteSpeed(p.s, now.Sub(lastRead))
		}
		lastRead = now
		img := rgbaFrame(pf.buf, p.s.w, p.s.h)
		p.mu.Lock()
		n := p.next
		c.putIfPF(c.frames, p.gen, p.s.frameKey(n), p.s.id, img, int64(size), pf, src)
		p.next++
		old := p.tailPF
		p.tail, p.tailPF, p.tailSrc = img, pf, src
		p.signal()
		p.mu.Unlock()
		if old != nil {
			old.release()
		}
		produced.Add(1)
	}
}

// awaitDemand blocks while the decoder is far enough ahead; ok false means stop, waited
// whether it had to block.
func (p *prefetcher) awaitDemand(ctx context.Context) (waited, ok bool) {
	idle := time.NewTimer(prefetchIdle)
	defer idle.Stop()
	p.mu.Lock()
	for p.next > p.want+p.ahead {
		waited = true
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return waited, false
		case <-idle.C:
			if time.Since(p.lastUsed()) >= prefetchIdle {
				return waited, false
			}
			idle.Reset(prefetchIdle)
		}
		p.mu.Lock()
	}
	p.mu.Unlock()
	return waited, ctx.Err() == nil
}

func (p *prefetcher) finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done, p.err = true, err
	p.signal()
}

// wait raises the demand to n and blocks until frame n is decoded or ctx ends. It returns
// the frame from the cache, or the last frame when the file ended before n. ok is false
// when the frame is decoded but already evicted; the caller decodes it directly.
func (p *prefetcher) wait(ctx context.Context, n int) (res frameRes, ok bool, err error) {
	timeout := time.NewTimer(frameWait)
	defer timeout.Stop()
	p.mu.Lock()
	p.used = time.Now()
	if n > p.want {
		p.want = n
		p.signal()
	}
	for p.next <= n && !p.done {
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return frameRes{}, false, ctx.Err()
		case <-timeout.C:
			return frameRes{}, false, errors.New("Zeitüberschreitung beim Dekodieren des Videos. Die Datei ist zu groß für die Vorschau oder beschädigt")
		}
		p.mu.Lock()
	}
	next, perr, tail, stopped := p.next, p.err, p.tail, p.stopped
	p.mu.Unlock()
	if r, found := p.c.cached(p.s, n); found {
		return r, true, nil
	}
	if next <= n { // decoder ended before n
		if perr != nil {
			return frameRes{}, false, perr
		}
		if tail != nil && !stopped {
			if r, found := p.newest(); found {
				r.approx = false
				return r, true, nil
			}
		}
	}
	return frameRes{}, false, nil // decoded but already evicted; caller decodes it directly
}

// prefetcherFor returns a running prefetcher that will soon produce frame n of s,
// or starts a new one at n, stopping the least recently used beyond the limit.
func (c *Cache) prefetcherFor(m model.MediaItem, s stream, n int) (*prefetcher, error) {
	return c.prefetcherForJump(m, s, n, 2*s.fps.Nominal())
}

// prefetcherForJump is prefetcherFor where a running decoder at most jump frames behind n
// still counts as covering it.
func (c *Cache) prefetcherForJump(m model.MediaItem, s stream, n, jump int) (*prefetcher, error) {
	bin, err := ffmpegPath()
	if err != nil {
		return nil, err
	}
	c.pmu.Lock()
	defer c.pmu.Unlock()
	if c.closed {
		return nil, errors.New("Medien-Cache ist geschlossen")
	}
	live := c.prefetchers[:0]
	var found *prefetcher
	for _, p := range c.prefetchers {
		if p.isDone() {
			continue
		}
		live = append(live, p)
		if found == nil && p.s == s && p.covers(n, jump) {
			found = p
		}
	}
	clear(c.prefetchers[len(live):])
	c.prefetchers = live
	if found != nil {
		return found, nil
	}
	// Keep at most two decoders per stream: enough for two clips of one file playing at
	// different offsets, while scrubbing replaces the older decoder on every jump.
	var same []int
	for i, p := range c.prefetchers {
		if p.s == s {
			same = append(same, i)
		}
	}
	if len(same) >= 2 {
		c.evict(same)
	}
	for limit := c.maxDecoders(); len(c.prefetchers) > 0 && len(c.prefetchers) >= limit; {
		all := make([]int, len(c.prefetchers))
		for i := range all {
			all[i] = i
		}
		c.evict(all)
	}
	ahead := prefetchSeconds * s.fps.Nominal()
	if limit := int(c.frames.getBudget() / 4 / int64(s.w*s.h*4)); ahead > limit {
		ahead = max(limit, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &prefetcher{c: c, m: m, s: s, start: n, ahead: ahead, gen: c.gen(s.id), cancel: cancel,
		next: n, want: n, used: time.Now(), changed: make(chan struct{})}
	c.prefetchers = append(c.prefetchers, p)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		p.run(ctx, bin)
	}()
	c.retireOtherSizes(p)
	return p, nil
}

// retireOtherSizes stops decoders of the same source at another size or proxy once they
// have not been asked for a moment: after a quality step (or a window resize) the old
// decoder would otherwise keep decoding up to its ring and compete with the new one for
// the CPU. A second clip of the same file at another size keeps being asked and stays.
func (c *Cache) retireOtherSizes(np *prefetcher) {
	for _, d := range []time.Duration{300 * time.Millisecond, 3 * time.Second} {
		time.AfterFunc(d, func() {
			c.pmu.Lock()
			defer c.pmu.Unlock()
			for _, p := range c.prefetchers {
				if p != np && !p.isDone() && p.s.id == np.s.id && p.s != np.s && time.Since(p.lastUsed()) > 250*time.Millisecond {
					p.stop()
				}
			}
		})
	}
}

// evict stops the least recently used of the prefetchers at the given indices.
func (c *Cache) evict(idx []int) {
	oldest := idx[0]
	for _, i := range idx {
		if c.prefetchers[i].lastUsed().Before(c.prefetchers[oldest].lastUsed()) {
			oldest = i
		}
	}
	c.prefetchers[oldest].stop()
	c.prefetchers = append(c.prefetchers[:oldest], c.prefetchers[oldest+1:]...)
}

// stopPrefetch stops background decoders, all of them or those of one media ID.
func (c *Cache) stopPrefetch(mediaID string) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	keep := c.prefetchers[:0]
	for _, p := range c.prefetchers {
		if mediaID == "" || p.s.id == mediaID {
			p.stop()
			continue
		}
		keep = append(keep, p)
	}
	clear(c.prefetchers[len(keep):])
	c.prefetchers = keep
}

// frameReader is the sequential decoder behind OpenVideo.
type frameReader struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	r      *bufio.Reader
	stderr *tailBuffer
	m      model.MediaItem
	w, h   int
	count  int
	err    error
	once   sync.Once
}

func (f *frameReader) Next() (image.Image, error) {
	if f.err != nil {
		return nil, f.err
	}
	buf := make([]byte, f.w*f.h*4)
	if _, err := io.ReadFull(f.r, buf); err != nil {
		f.err = io.EOF
		if f.count == 0 {
			if werr := f.wait(); werr != nil || f.stderr.String() != "" {
				f.err = decodeError(f.m, f.stderr.String(), werr)
			}
		}
		return nil, f.err
	}
	f.count++
	return rgbaFrame(buf, f.w, f.h), nil
}

func (f *frameReader) wait() error {
	var err error
	f.once.Do(func() { err = f.cmd.Wait() })
	return err
}

func (f *frameReader) Close() error {
	f.cancel()
	_ = f.wait()
	if f.err == nil {
		f.err = errors.New("Video-Leser ist geschlossen")
	}
	return nil
}
