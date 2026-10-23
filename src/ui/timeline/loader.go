package timeline

import (
	"image"
	"os"
	"sync"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

// mediaSource is the part of media.Cache the timeline draws from.
type mediaSource interface {
	Thumbnail(m model.MediaItem, w, h int) (image.Image, error)
	Waveform(m model.MediaItem, buckets int) ([]float32, error)
}

// Thumbnail request size; the filmstrip scales it to the lane height.
const thumbW, thumbH = 160, 90

type assetKey struct {
	id, path string
	wave     bool
}

// asset is the loaded state of one media item for one purpose (filmstrip or waveform).
type asset struct {
	done    bool
	offline bool      // file missing on disk
	checked time.Time // when the file was found offline
	thumb   image.Image
	peaks   []float32
}

// offlineRecheck is how long an offline result is trusted before the file is looked up
// again, so remounted drives come back without reopening the project.
const offlineRecheck = 2 * time.Second

// loader fetches thumbnails and waveforms in the background so ffmpeg never blocks the
// UI thread. Results are published on the UI thread and trigger one coalesced refresh.
type loader struct {
	src     mediaSource
	refresh func() // runs on the UI thread
	inline  bool   // tests: load synchronously

	mu      sync.Mutex
	items   map[assetKey]*asset
	closed  bool
	pending bool // a refresh is already queued
	sem     chan struct{}
	quit    chan struct{} // closed by close: queued loads give up
}

func newLoader(src mediaSource, refresh func()) *loader {
	return &loader{src: src, refresh: refresh, items: map[assetKey]*asset{}, sem: make(chan struct{}, 3),
		quit: make(chan struct{})}
}

// isClosed reports whether close was called.
func (l *loader) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// get returns the asset for m, starting a load if needed. The result may not be done yet.
func (l *loader) get(m model.MediaItem, wave bool, buckets int) *asset {
	k := assetKey{m.ID, m.Path, wave}
	l.mu.Lock()
	a, ok := l.items[k]
	if ok && a.done && a.offline && time.Since(a.checked) >= offlineRecheck {
		ok = false // look again; the old asset keeps drawing as offline meanwhile
	}
	if !ok {
		a = &asset{}
		if old := l.items[k]; old != nil && old.done {
			*a = *old
			a.done = true
		}
		l.items[k] = a
	}
	closed := l.closed
	l.mu.Unlock()
	if ok || closed {
		return a
	}
	if l.inline {
		res := l.load(m, wave, buckets)
		l.mu.Lock()
		*a = res
		l.mu.Unlock()
		return a
	}
	go func() {
		select {
		case l.sem <- struct{}{}:
		case <-l.quit:
			return // closed while queued: start no decode for a closed timeline
		}
		if l.isClosed() {
			<-l.sem
			return
		}
		res := l.load(m, wave, buckets)
		<-l.sem
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return
		}
		*a = res
		queue := !l.pending
		l.pending = true
		l.mu.Unlock()
		if queue {
			kit.Do(l.flush)
		}
	}()
	return a
}

// snapshot copies an asset under the lock for drawing.
func (l *loader) snapshot(a *asset) asset {
	l.mu.Lock()
	defer l.mu.Unlock()
	return *a
}

func (l *loader) flush() {
	l.mu.Lock()
	l.pending = false
	closed := l.closed
	l.mu.Unlock()
	if !closed && l.refresh != nil {
		l.refresh()
	}
}

func (l *loader) load(m model.MediaItem, wave bool, buckets int) asset {
	res := asset{done: true}
	if _, err := os.Stat(m.Path); err != nil {
		res.offline, res.checked = true, time.Now()
		return res
	}
	if l.src == nil {
		return res
	}
	if wave {
		res.peaks, _ = l.src.Waveform(m, buckets)
	} else {
		res.thumb, _ = l.src.Thumbnail(m, thumbW, thumbH)
	}
	return res
}

// forget drops cached assets of a media item so they reload (e.g. after relinking).
func (l *loader) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.items {
		if k.id == id {
			delete(l.items, k)
		}
	}
}

// recheckOffline drops finished assets whose offline state disagrees with the media
// cache's offline map, so they load again. It reports whether anything was dropped.
func (l *loader) recheckOffline(offline func(path string) bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	dropped := false
	for k, a := range l.items {
		if a.done && a.offline != offline(k.path) {
			delete(l.items, k)
			dropped = true
		}
	}
	return dropped
}

func (l *loader) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.quit)
	}
}

// waveBuckets picks a waveform resolution of about four peaks per frame.
func waveBuckets(p *model.Project, m model.MediaItem) int {
	return max(512, min(p.MediaFrames(m)*4, 1<<15))
}
