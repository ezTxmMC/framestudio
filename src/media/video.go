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
	"time"

	"framestudio-proto/src/model"
)

const (
	prefetchSeconds = 3                // how far a prefetcher decodes ahead of the last request
	maxPrefetchers  = 4                // concurrent background decoders per cache
	prefetchIdle    = 10 * time.Second // a decoder nobody asks for exits after this
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
func clampFrame(m model.MediaItem, n, fps int) int {
	if n < 0 {
		n = 0
	}
	if m.Duration > 0 {
		last := int(m.Duration.Seconds()*float64(fps)) - 1
		if n > last {
			n = max(last, 0)
		}
	}
	return n
}

func seconds(n, fps int) float64 { return float64(n) / float64(fps) }

// videoArgs builds an ffmpeg command line that writes raw RGBA frames of w x h to stdout.
// fps > 0 resamples to that rate; frames > 0 stops after that many frames.
func videoArgs(path string, t float64, fps, w, h, frames int) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if t > 0 {
		args = append(args, "-ss", strconv.FormatFloat(t, 'f', 6, 64))
	}
	args = append(args, "-i", path, "-map", "0:v:0", "-an", "-sn", "-dn")
	vf := fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h)
	if fps > 0 {
		vf = fmt.Sprintf("fps=%d,%s", fps, vf)
	}
	if frames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(frames))
	}
	return append(args, "-vf", vf, "-f", "rawvideo", "-pix_fmt", "rgba", "-")
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

// decodeOne decodes the single frame at time t, scaled to exactly w x h.
func decodeOne(m model.MediaItem, t float64, w, h int) (*image.RGBA, error) {
	bin, err := ffmpegPath()
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr tailBuffer
	cmd := exec.Command(bin, videoArgs(m.Path, t, 0, w, h, 1)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if stdout.Len() < w*h*4 {
		return nil, decodeError(m, stderr.String(), runErr)
	}
	return rgbaFrame(stdout.Bytes()[:w*h*4], w, h), nil
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
	fps      int
	w, h     int
}

func (s stream) frameKey(n int) string {
	return fmt.Sprintf("vf|%s|%s|%d|%dx%d|%d", s.id, s.path, s.fps, s.w, s.h, n)
}

// prefetcher runs one ffmpeg process that decodes a stream sequentially into the
// frame cache, staying at most "ahead" frames in front of the newest request.
type prefetcher struct {
	c      *Cache
	s      stream
	start  int
	ahead  int
	cancel context.CancelFunc

	mu      sync.Mutex
	next    int // next frame the decoder will produce
	want    int // newest requested frame
	used    time.Time
	done    bool
	stopped bool // ended by stop, not by the end of the file
	err     error
	tail    image.Image // last decoded frame, held when the file ends early
	changed chan struct{}
}

func (p *prefetcher) signal() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *prefetcher) stop() { p.cancel() }

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

func (p *prefetcher) run(ctx context.Context, bin string, m model.MediaItem) {
	var stderr tailBuffer
	cmd := exec.CommandContext(ctx, bin, videoArgs(m.Path, seconds(p.start, p.s.fps), p.s.fps, p.s.w, p.s.h, 0)...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		p.finish(decodeError(m, "", err))
		return
	}
	produced, eof := 0, false
	defer func() {
		eof = eof && ctx.Err() == nil
		p.cancel()
		werr := cmd.Wait()
		if produced == 0 && eof {
			p.finish(decodeError(m, stderr.String(), werr))
			return
		}
		p.mu.Lock()
		p.stopped = !eof
		p.mu.Unlock()
		p.finish(nil)
	}()

	r := bufio.NewReaderSize(stdout, 1<<20)
	size := p.s.w * p.s.h * 4
	for {
		if !p.awaitDemand(ctx) {
			return
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			eof = true
			return
		}
		img := rgbaFrame(buf, p.s.w, p.s.h)
		p.mu.Lock()
		n := p.next
		p.c.frames.put(p.s.frameKey(n), p.s.id, img, int64(size))
		p.next++
		p.tail = img
		p.signal()
		p.mu.Unlock()
		produced++
	}
}

// awaitDemand blocks while the decoder is far enough ahead; false means stop.
func (p *prefetcher) awaitDemand(ctx context.Context) bool {
	idle := time.NewTimer(prefetchIdle)
	defer idle.Stop()
	p.mu.Lock()
	for p.next > p.want+p.ahead {
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		case <-idle.C:
			if time.Since(p.lastUsed()) >= prefetchIdle {
				return false
			}
			idle.Reset(prefetchIdle)
		}
		p.mu.Lock()
	}
	p.mu.Unlock()
	return ctx.Err() == nil
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

// wait raises the demand to n and blocks until frame n is decoded. It returns the frame
// from the cache, or the last frame when the file ended before n.
func (p *prefetcher) wait(n int) (image.Image, error) {
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
		case <-timeout.C:
			return nil, errors.New("Zeitüberschreitung beim Dekodieren des Videos. Die Datei ist zu groß für die Vorschau oder beschädigt")
		}
		p.mu.Lock()
	}
	next, err, tail, stopped := p.next, p.err, p.tail, p.stopped
	p.mu.Unlock()
	if v, ok := p.c.frames.get(p.s.frameKey(n)); ok {
		return v.(image.Image), nil
	}
	if next <= n { // decoder ended before n
		if err != nil {
			return nil, err
		}
		if tail != nil && !stopped {
			return tail, nil
		}
	}
	return nil, nil // decoded but already evicted; caller decodes it directly
}

// prefetcherFor returns a running prefetcher that will soon produce frame n of s,
// or starts a new one at n, stopping the least recently used beyond the limit.
func (c *Cache) prefetcherFor(m model.MediaItem, s stream, n int) (*prefetcher, error) {
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
		if found == nil && p.s == s && p.covers(n, 2*s.fps) {
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
	all := make([]int, len(c.prefetchers))
	for i := range all {
		all[i] = i
	}
	if len(all) >= maxPrefetchers {
		c.evict(all)
	}
	ahead := prefetchSeconds * s.fps
	if limit := int(c.frames.budget / 4 / int64(s.w*s.h*4)); ahead > limit {
		ahead = max(limit, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &prefetcher{c: c, s: s, start: n, ahead: ahead, cancel: cancel,
		next: n, want: n, used: time.Now(), changed: make(chan struct{})}
	c.prefetchers = append(c.prefetchers, p)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		p.run(ctx, bin, m)
	}()
	return p, nil
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
