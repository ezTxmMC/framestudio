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
	stillBudget  = 256 << 20
	frameBudget  = 512 << 20
	thumbBudget  = 64 << 20
	waveBudget   = 64 << 20
	posterBudget = 96 << 20
)

// Cache decodes and memoises media. The zero value is not usable; call NewCache.
type Cache struct {
	stills, frames, thumbs, waves, posters *lru
	flight                                 flight

	// gens counts invalidations per media ID: decodes that started before an
	// Invalidate do not store their (stale) results.
	gmu  sync.Mutex
	gens map[string]uint64

	posterSlots chan struct{} // single-frame grabs for filmstrips
	rev         reverse       // backward chunk decoding
	off         offline       // missing-file map

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
	decoders     atomic.Int32 // limit of concurrent playback decoders (SetMaxDecoders)
}

// Limits for background ffmpeg jobs.
const (
	maxBackground = 3                // concurrent thumbnail/waveform decoders per cache
	posterTimeout = 30 * time.Second // longest a single-frame decode may take
	peaksTimeout  = 10 * time.Minute // longest a full-file waveform decode may take
)

func NewCache() *Cache {
	ctx, stop := context.WithCancel(context.Background())
	c := &Cache{
		stills:      newLRU(stillBudget),
		frames:      newLRU(frameBudget),
		thumbs:      newLRU(thumbBudget),
		waves:       newLRU(waveBudget),
		posters:     newLRU(posterBudget),
		gens:        map[string]uint64{},
		posterSlots: make(chan struct{}, maxPosters),
		ctx:         ctx,
		stop:        stop,
		bg:          make(chan struct{}, maxBackground),
		jobs:        map[string]map[*int]context.CancelFunc{},
	}
	c.decoders.Store(maxPrefetchers)
	register(c)
	return c
}

// gen returns the invalidation generation of a media ID.
func (c *Cache) gen(id string) uint64 {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	return c.gens[id]
}

// putIf stores val in l unless the media item was invalidated since generation g.
func (c *Cache) putIf(l *lru, g uint64, key, owner string, val any, size int64) {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	if c.gens[owner] == g {
		l.put(key, owner, val, size)
	}
}

// flightKey makes in-flight work of an older generation invisible to new callers.
func flightKey(key string, g uint64) string { return fmt.Sprintf("%s|g%d", key, g) }

// background runs fn in one of the limited background slots with a context that ends
// after timeout, when the cache closes or when Cancel(mediaID) is called.
func (c *Cache) background(mediaID string, timeout time.Duration, fn func(ctx context.Context) (any, error)) (any, error) {
	return c.inSlot(c.bg, mediaID, timeout, fn)
}

// inSlot runs fn while holding one of the slots of sem (see background). The timeout
// starts once the slot is acquired: time spent queued behind other jobs does not count,
// so a long queue cannot make healthy files time out. Queued jobs still end with the
// cache or Cancel(mediaID).
func (c *Cache) inSlot(sem chan struct{}, mediaID string, timeout time.Duration, fn func(ctx context.Context) (any, error)) (any, error) {
	wait, release := c.jobContext(mediaID)
	defer release()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-wait.Done():
		return nil, fmt.Errorf("Vorschau abgebrochen: %w", wait.Err())
	}
	ctx, cancel := context.WithTimeout(wait, timeout)
	defer cancel()
	return fn(ctx)
}

// jobContext returns a context that ends when the cache closes or Cancel(mediaID) is
// called; release unregisters it and must be called when the job is done.
func (c *Cache) jobContext(mediaID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(c.ctx)
	key := new(int)
	c.jmu.Lock()
	if c.jobs[mediaID] == nil {
		c.jobs[mediaID] = map[*int]context.CancelFunc{}
	}
	c.jobs[mediaID][key] = cancel
	c.jmu.Unlock()
	return ctx, func() {
		cancel()
		c.jmu.Lock()
		delete(c.jobs[mediaID], key)
		if len(c.jobs[mediaID]) == 0 {
			delete(c.jobs, mediaID)
		}
		c.jmu.Unlock()
	}
}

// Cancel stops running and queued thumbnail, poster and waveform decodes of a media
// item, its playback prefetchers, backward chunks and shuttle grabs, e.g. when its
// project closes. Callers waiting for them get an error; nothing is cached.
func (c *Cache) Cancel(mediaID string) {
	c.jmu.Lock()
	for _, cancel := range c.jobs[mediaID] {
		cancel()
	}
	c.jmu.Unlock()
	c.stopPrefetch(mediaID)
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
	g := c.gen(m.ID)
	v, err := c.flight.do(flightKey(key, g), func() (any, error) {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), full, b, draw.Src, nil)
		c.putIf(c.stills, g, key, m.ID, dst, int64(w*h*4))
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
	g := c.gen(m.ID)
	v, err := c.flight.do(flightKey(key, g), func() (any, error) {
		f, err := os.Open(m.Path)
		if err != nil {
			c.noteMissing(m.Path)
			return nil, fmt.Errorf("„%s“ fehlt unter %s. Datei wiederherstellen oder neu verknüpfen", m.Name, m.Path)
		}
		defer f.Close()
		img, _, err := image.Decode(bufio.NewReader(f))
		if err != nil {
			return nil, fmt.Errorf("„%s“ ist kein lesbares Bild: %v", m.Name, err)
		}
		b := img.Bounds()
		c.putIf(c.stills, g, key, m.ID, img, int64(b.Dx()*b.Dy()*4))
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

// VideoFrame returns source frame n (counted at the given project fps) of a video, scaled to fit w x h.
// Requests that walk backwards in small steps are detected and served by chunked
// backward decoding (see PlaybackFrame).
func (c *Cache) VideoFrame(m model.MediaItem, n int, fps model.Rate, w, h int) (image.Image, error) {
	return c.frame(m, n, fps, w, h, 0)
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
	g := c.gen(m.ID)
	v, err := c.flight.do(flightKey(key, g), func() (any, error) {
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
		c.putIf(c.thumbs, g, key, m.ID, img, int64(b.Dx()*b.Dy()*4))
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

func (c *Cache) posterFrame(ctx context.Context, m model.MediaItem, w, h int) (image.Image, error) {
	if _, err := os.Stat(m.Path); err != nil {
		c.noteMissing(m.Path)
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
	g := c.gen(m.ID)
	v, err := c.flight.do(flightKey(key, g), func() (any, error) {
		if _, err := os.Stat(m.Path); err != nil {
			c.noteMissing(m.Path)
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
		c.putIf(c.waves, g, key, m.ID, p, int64(len(p)*4))
		return p, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]float32), nil
}

// Invalidate drops all cached data for a media ID (e.g. after relinking): stills, video
// frames, thumbnails, posters and waveforms. Decodes still running for it are cancelled
// and their results discarded, so every page loads the new file. Subscribers registered
// with OnInvalidate are told afterwards (on the calling goroutine).
func (c *Cache) Invalidate(mediaID string) {
	c.Cancel(mediaID)
	c.gmu.Lock()
	c.gens[mediaID]++
	for _, l := range []*lru{c.stills, c.frames, c.thumbs, c.waves, c.posters} {
		l.dropOwner(mediaID)
	}
	c.gmu.Unlock()
	c.rev.forget(mediaID)
	c.off.notifyInvalidate(mediaID)
}

// OnInvalidate registers fn to be called with the media ID after every Invalidate, so
// pages can drop their own copies of thumbnails or filmstrips. fn runs on the goroutine
// that called Invalidate. The returned func unregisters it.
func (c *Cache) OnInvalidate(fn func(mediaID string)) (cancel func()) {
	return c.off.subscribeInvalidate(fn)
}

// Close stops all background decoders and waits for their ffmpeg processes to exit.
// The cache keeps serving cached data but starts no new decoders.
func (c *Cache) Close() {
	c.pmu.Lock()
	c.closed = true
	c.pmu.Unlock()
	unregister(c)
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
func OpenVideo(m model.MediaItem, n int, fps model.Rate, w, h int) (FrameReader, error) {
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
	fps = fps.Or(model.Rate25).Reduced()
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
