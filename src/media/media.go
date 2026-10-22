// Package media probes and decodes media files (stills via Go image codecs, video and
// audio via ffmpeg/ffprobe) and caches decoded frames, thumbnails and waveforms.
//
// OWNER: media agent. Exported signatures in this file are a contract; do not change them.
// Everything here must be safe for concurrent use.
package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"

	"framestudio-proto/src/model"
)

// FFmpegAvailable reports whether ffmpeg and ffprobe are on PATH.
func FFmpegAvailable() bool {
	lookTools()
	return tools.ffmpeg != "" && tools.ffprobe != ""
}

// KindForPath classifies a file by extension; ok is false for unsupported files.
func KindForPath(path string) (kind model.ClipKind, ok bool) {
	kind, ok = extKinds[strings.ToLower(filepath.Ext(path))]
	return kind, ok
}

// Extensions lists supported file extensions (lower case, with dot) for file dialogs.
func Extensions() []string { return append([]string(nil), extOrder...) }

// Probe inspects a file and returns a MediaItem with a fresh ID, name, kind, size, duration.
func Probe(path string) (model.MediaItem, error) { return probeFile(path) }

// Cache budgets in bytes.
const (
	stillBudget = 256 << 20
	frameBudget = 512 << 20
	thumbBudget = 64 << 20
	waveBudget  = 64 << 20
)

// Cache decodes and memoises media. The zero value is not usable; call NewCache.
type Cache struct {
	stills, frames, thumbs, waves *lru
	flight                        flight

	pmu         sync.Mutex
	prefetchers []*prefetcher
	closed      bool
	wg          sync.WaitGroup

	// Background jobs (thumbnails, waveforms) share a few ffmpeg slots, stop when the
	// cache closes and can be cancelled per media item.
	ctx  context.Context
	stop context.CancelFunc
	bg   chan struct{}
	jmu  sync.Mutex
	jobs map[string]map[*int]context.CancelFunc

	hits, misses atomic.Int64 // frame cache statistics
}

// Limits for background ffmpeg jobs.
const (
	maxBackground = 3                // concurrent thumbnail/waveform decoders per cache
	posterTimeout = 30 * time.Second // longest a single-frame decode may take
	peaksTimeout  = 10 * time.Minute // longest a full-file waveform decode may take
)

func NewCache() *Cache {
	ctx, stop := context.WithCancel(context.Background())
	return &Cache{
		stills: newLRU(stillBudget),
		frames: newLRU(frameBudget),
		thumbs: newLRU(thumbBudget),
		waves:  newLRU(waveBudget),
		ctx:    ctx,
		stop:   stop,
		bg:     make(chan struct{}, maxBackground),
		jobs:   map[string]map[*int]context.CancelFunc{},
	}
}

// background runs fn in one of the limited background slots with a context that ends
// after timeout, when the cache closes or when Cancel(mediaID) is called.
func (c *Cache) background(mediaID string, timeout time.Duration, fn func(ctx context.Context) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	key := new(int)
	c.jmu.Lock()
	if c.jobs[mediaID] == nil {
		c.jobs[mediaID] = map[*int]context.CancelFunc{}
	}
	c.jobs[mediaID][key] = cancel
	c.jmu.Unlock()
	defer func() {
		c.jmu.Lock()
		delete(c.jobs[mediaID], key)
		if len(c.jobs[mediaID]) == 0 {
			delete(c.jobs, mediaID)
		}
		c.jmu.Unlock()
	}()
	select {
	case c.bg <- struct{}{}:
		defer func() { <-c.bg }()
	case <-ctx.Done():
		return nil, fmt.Errorf("Vorschau abgebrochen: %w", ctx.Err())
	}
	return fn(ctx)
}

// Cancel stops running and queued thumbnail and waveform decodes of a media item, e.g.
// when its project closes. Callers waiting for them get an error; nothing is cached.
func (c *Cache) Cancel(mediaID string) {
	c.jmu.Lock()
	defer c.jmu.Unlock()
	for _, cancel := range c.jobs[mediaID] {
		cancel()
	}
}

// Image returns a still image scaled to fit within maxW x maxH (aspect preserved).
func (c *Cache) Image(m model.MediaItem, maxW, maxH int) (image.Image, error) {
	if m.Kind != model.KindImage {
		return nil, fmt.Errorf("„%s“ ist kein Standbild", m.Name)
	}
	full, err := c.fullImage(m)
	if err != nil {
		return nil, err
	}
	b := full.Bounds()
	w, h := fitSize(b.Dx(), b.Dy(), maxW, maxH)
	if w == b.Dx() && h == b.Dy() {
		return full, nil
	}
	key := fmt.Sprintf("img|%s|%s|%dx%d", m.ID, m.Path, w, h)
	if v, ok := c.stills.get(key); ok {
		return v.(image.Image), nil
	}
	v, err := c.flight.do(key, func() (any, error) {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), full, b, draw.Src, nil)
		c.stills.put(key, m.ID, dst, int64(w*h*4))
		return dst, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

// fullImage decodes a still once at full resolution.
func (c *Cache) fullImage(m model.MediaItem) (image.Image, error) {
	key := "full|" + m.ID + "|" + m.Path
	if v, ok := c.stills.get(key); ok {
		return v.(image.Image), nil
	}
	v, err := c.flight.do(key, func() (any, error) {
		f, err := os.Open(m.Path)
		if err != nil {
			return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
		}
		defer f.Close()
		img, _, err := image.Decode(bufio.NewReader(f))
		if err != nil {
			return nil, fmt.Errorf("„%s“ ist kein lesbares Bild: %v", m.Name, err)
		}
		b := img.Bounds()
		c.stills.put(key, m.ID, img, int64(b.Dx()*b.Dy()*4))
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

// VideoFrame returns source frame n (counted at the given project fps) of a video, scaled to fit w x h.
func (c *Cache) VideoFrame(m model.MediaItem, n, fps, w, h int) (image.Image, error) {
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
	key := s.frameKey(n)
	if v, ok := c.frames.get(key); ok {
		c.hits.Add(1)
		return v.(image.Image), nil
	}
	c.misses.Add(1)
	if _, err := os.Stat(m.Path); err != nil {
		return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	p, err := c.prefetcherFor(m, s, n)
	if err != nil {
		return nil, err
	}
	img, err := p.wait(n)
	if err != nil || img != nil {
		return img, err
	}
	ctx, cancel := context.WithTimeout(c.ctx, posterTimeout)
	defer cancel()
	one, err := decodeOne(ctx, m, frameArgs(m.Path, n, fps, w, h, 1), w, h)
	if err != nil {
		return nil, err
	}
	c.frames.put(key, m.ID, one, int64(w*h*4))
	return one, nil
}

// Thumbnail returns a poster image for the media pool: the still, the first video frame,
// or a rendered waveform for audio.
func (c *Cache) Thumbnail(m model.MediaItem, w, h int) (image.Image, error) {
	if w <= 0 || h <= 0 {
		return nil, errors.New("Vorschaugröße ist ungültig")
	}
	if m.Kind == model.KindImage {
		return c.Image(m, w, h)
	}
	key := fmt.Sprintf("th|%s|%s|%dx%d", m.ID, m.Path, w, h)
	if v, ok := c.thumbs.get(key); ok {
		return v.(image.Image), nil
	}
	v, err := c.flight.do(key, func() (any, error) {
		var img image.Image
		var err error
		if m.Kind == model.KindAudio {
			var peaks []float32
			if peaks, err = c.Waveform(m, w); err == nil {
				img = drawWaveform(peaks, w, h)
			}
		} else {
			var v any
			v, err = c.background(m.ID, posterTimeout, func(ctx context.Context) (any, error) {
				return c.posterFrame(ctx, m, w, h)
			})
			if err == nil {
				img = v.(image.Image)
			}
		}
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		c.thumbs.put(key, m.ID, img, int64(b.Dx()*b.Dy()*4))
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

func (c *Cache) posterFrame(ctx context.Context, m model.MediaItem, w, h int) (image.Image, error) {
	if _, err := os.Stat(m.Path); err != nil {
		return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	w, h = fitSize(m.Width, m.Height, w, h)
	t := m.Duration.Seconds() / 10
	img, err := decodeOne(ctx, m, posterArgs(m.Path, t, w, h), w, h)
	if err != nil && t > 0 && ctx.Err() == nil {
		img, err = decodeOne(ctx, m, posterArgs(m.Path, 0, w, h), w, h)
	}
	if err != nil {
		return nil, err
	}
	return img, nil
}

// Waveform returns peak amplitudes 0..1 across the whole file in the given number of buckets.
func (c *Cache) Waveform(m model.MediaItem, buckets int) ([]float32, error) {
	if buckets <= 0 {
		return nil, errors.New("Anzahl der Wellenform-Abschnitte muss größer als 0 sein")
	}
	if m.Kind == model.KindImage {
		return nil, fmt.Errorf("„%s“ hat keinen Ton", m.Name)
	}
	peaks, err := c.peaks(m)
	if err != nil {
		return nil, err
	}
	return resamplePeaks(peaks, buckets), nil
}

// peaks returns the cached full-resolution peak envelope of a file.
func (c *Cache) peaks(m model.MediaItem) ([]float32, error) {
	key := "wave|" + m.ID + "|" + m.Path
	if v, ok := c.waves.get(key); ok {
		return v.([]float32), nil
	}
	v, err := c.flight.do(key, func() (any, error) {
		if _, err := os.Stat(m.Path); err != nil {
			return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
		}
		bin, err := ffmpegPath()
		if err != nil {
			return nil, err
		}
		v, err := c.background(m.ID, peaksTimeout, func(ctx context.Context) (any, error) {
			return readPeaks(ctx, bin, m)
		})
		if err != nil {
			return nil, err
		}
		p := v.([]float32)
		c.waves.put(key, m.ID, p, int64(len(p)*4))
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]float32), nil
}

// Invalidate drops all cached data for a media ID (e.g. after relinking).
func (c *Cache) Invalidate(mediaID string) {
	c.stopPrefetch(mediaID)
	for _, l := range []*lru{c.stills, c.frames, c.thumbs, c.waves} {
		l.dropOwner(mediaID)
	}
}

// Close stops all background decoders and waits for their ffmpeg processes to exit.
// The cache keeps serving cached data but starts no new decoders.
func (c *Cache) Close() {
	c.pmu.Lock()
	c.closed = true
	c.pmu.Unlock()
	c.stop()
	c.stopPrefetch("")
	c.wg.Wait()
}

// FrameReader decodes consecutive video frames, used by export.
type FrameReader interface {
	Next() (image.Image, error) // io.EOF after the last frame
	Close() error
}

// OpenVideo starts sequential decoding at source frame n, resampled to fps and scaled to w x h.
func OpenVideo(m model.MediaItem, n, fps, w, h int) (FrameReader, error) {
	if m.Kind != model.KindVideo {
		return nil, fmt.Errorf("„%s“ ist kein Video", m.Name)
	}
	bin, err := ffmpegPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(m.Path); err != nil {
		return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
	}
	if fps <= 0 {
		fps = 25
	}
	if n < 0 {
		n = 0
	}
	w, h = fitSize(m.Width, m.Height, w, h)
	if w <= 0 || h <= 0 {
		return nil, errors.New("Ausgabegröße ist ungültig")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &tailBuffer{}
	cmd := exec.CommandContext(ctx, bin, frameArgs(m.Path, n, fps, w, h, 0)...)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		cancel()
		return nil, decodeError(m, "", err)
	}
	return &frameReader{cmd: cmd, cancel: cancel, r: bufio.NewReaderSize(stdout, 1<<20),
		stderr: stderr, m: m, w: w, h: h}, nil
}
