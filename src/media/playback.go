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
//   - Fast forward (2x..8x): with a fast hint the nearest cached frame within ±1 frame is
//     served when the exact one is not decoded yet, instead of waiting for it.

const (
	reverseSeconds = 2  // length of one backward chunk
	reverseStep    = 16 // a request at most this many frames before the previous one walks backwards
)

// reverse tracks the newest request per stream and the backward chunks being decoded.
type reverse struct {
	mu     sync.Mutex
	last   map[stream]int
	chunks map[stream]*chunk
}

// forget drops the request history of a media item.
func (r *reverse) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for s := range r.last {
		if s.id == id {
			delete(r.last, s)
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
func (c *Cache) PlaybackFrame(m model.MediaItem, n, fps, w, h int, rate float64) (image.Image, error) {
	return c.frame(m, n, fps, w, h, rate)
}

func (c *Cache) frame(m model.MediaItem, n, fps, w, h int, rate float64) (image.Image, error) {
	switch m.Kind {
	case model.KindImage:
		return c.Image(m, w, h)
	case model.KindAudio:
		return nil, fmt.Errorf("„%s“ hat kein Bild", m.Name)
	}
	if fps <= 0 {
		fps = 25
	}
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
		return v.(image.Image), nil
	}
	if rate >= 2 || rate <= -2 {
		if img, ok := c.near(s, n, rate); ok {
			c.hits.Add(1)
			return img, nil
		}
	}
	c.misses.Add(1)
	if _, err := os.Stat(m.Path); err != nil {
		c.noteMissing(m.Path)
		return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	backward := rate < 0 || (rate == 0 && had && n < prev && prev-n <= reverseStep)
	if backward {
		return c.backward(m, s, n)
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
					if p.s == s && p.covers(n, 2*s.fps) {
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
	n := reverseSeconds * s.fps
	if limit := int(c.frames.budget / 4 / int64(s.w*s.h*4)); n > limit {
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
	ctx, cancel := context.WithTimeout(c.ctx, frameWait)
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
