package timeline

import (
	"context"
	"image"
	"math"
	"sync"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/workgate"
)

// posterSource is the part of media.Cache that delivers filmstrip frames.
type posterSource interface {
	Poster(m model.MediaItem, sec float64, w, h int) (image.Image, error)
}

// Filmstrip loading limits.
const (
	posterWorkers  = 2   // concurrent Poster calls
	posterQueueMax = 48  // pending requests; the oldest are dropped (and asked again when visible)
	posterKeep     = 600 // loaded frames kept before the cache is cleared
)

type posterKey struct {
	id, path string
	frame    int        // source frame in project frames, quantised to the tile grid
	rate     model.Rate // project rate the frame counts in; a rate change must not reuse tiles
}

type posterEntry struct {
	done bool
	img  image.Image
}

type posterReq struct {
	key posterKey
	m   model.MediaItem
	sec float64
}

// posterLoader fetches filmstrip frames in the background through a small LIFO queue, so
// the tiles the user looks at right now load first. Results reach the UI thread through
// kit.Do and trigger one coalesced refresh.
type posterLoader struct {
	src     posterSource
	refresh func() // UI thread
	inline  bool   // tests: load synchronously

	mu      sync.Mutex
	items   map[posterKey]*posterEntry
	queue   []posterReq
	workers int
	closed  bool
	pending bool
	wg      sync.WaitGroup
	gate    workgate.Gate // nil = workgate.Default()
	ctx     context.Context
	cancel  context.CancelFunc
}

func newPosterLoader(src posterSource, refresh func()) *posterLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &posterLoader{src: src, refresh: refresh, items: map[posterKey]*posterEntry{}, ctx: ctx, cancel: cancel}
}

// get returns the frame of m at source frame (project frames, fps), or nil while it
// loads. The first call for a frame queues it.
func (l *posterLoader) get(m model.MediaItem, frame int, fps model.Rate) image.Image {
	if l == nil || l.src == nil {
		return nil
	}
	k := posterKey{m.ID, m.Path, frame, fps.Reduced()}
	l.mu.Lock()
	if e, ok := l.items[k]; ok {
		img := e.img
		l.mu.Unlock()
		return img
	}
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	if len(l.items) >= posterKeep {
		for key, e := range l.items {
			if e.done {
				delete(l.items, key)
			}
		}
	}
	e := &posterEntry{}
	l.items[k] = e
	req := posterReq{key: k, m: m, sec: fps.FramesToSeconds(frame)}
	if l.inline {
		l.mu.Unlock()
		img, _ := l.src.Poster(req.m, req.sec, thumbW, thumbH)
		l.mu.Lock()
		e.done, e.img = true, img
		l.mu.Unlock()
		return img
	}
	l.queue = append(l.queue, req)
	if len(l.queue) > posterQueueMax {
		drop := l.queue[0]
		l.queue = l.queue[1:]
		delete(l.items, drop.key)
	}
	if l.workers < posterWorkers {
		l.workers++
		l.wg.Add(1)
		go l.work()
	}
	l.mu.Unlock()
	return nil
}

// work serves the queue newest first and exits when it is empty or the loader closed.
func (l *posterLoader) work() {
	defer l.wg.Done()
	for {
		l.mu.Lock()
		if l.closed || len(l.queue) == 0 {
			l.workers--
			l.mu.Unlock()
			return
		}
		req := l.queue[len(l.queue)-1]
		l.queue = l.queue[:len(l.queue)-1]
		l.mu.Unlock()

		// filmstrip frames are visible work: they wait while playing or scrubbing
		var g workgate.Gate = workgate.Default()
		if l.gate != nil {
			g = l.gate
		}
		release, err := g.Acquire(l.ctx, workgate.PrioVisible)
		if err != nil {
			l.mu.Lock()
			l.workers--
			l.mu.Unlock()
			return
		}
		img, _ := l.src.Poster(req.m, req.sec, thumbW, thumbH)
		release()

		l.mu.Lock()
		if e, ok := l.items[req.key]; ok {
			e.done, e.img = true, img
		}
		queue := !l.closed && !l.pending
		if queue {
			l.pending = true
		}
		l.mu.Unlock()
		if queue {
			kit.Do(l.flush)
		}
	}
}

func (l *posterLoader) flush() {
	l.mu.Lock()
	l.pending = false
	closed := l.closed
	l.mu.Unlock()
	if !closed && l.refresh != nil {
		l.refresh()
	}
}

// forget drops the frames of a media item (relinked or replaced file).
func (l *posterLoader) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.items {
		if k.id == id {
			delete(l.items, k)
		}
	}
}

// close stops the workers after their current frame; it does not wait for them.
func (l *posterLoader) close() {
	l.mu.Lock()
	l.closed = true
	l.queue = nil
	l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
}

// posterStep is the source-frame grid for filmstrip tiles: the largest power of two not
// above the frames one tile spans, so neighbouring tiles always show different frames and
// small zoom changes reuse loaded frames.
func posterStep(framesPerTile float64) int {
	if framesPerTile <= 1 || math.IsNaN(framesPerTile) || math.IsInf(framesPerTile, 0) {
		return 1
	}
	return 1 << int(math.Floor(math.Log2(framesPerTile)))
}

// posterFrame returns the source frame shown by the tile that starts at clip-local frame
// offset local: the clip's first frame for the first tile (the head of a shot matters
// when editing), the quantised position for the others.
func posterFrame(sourceIn int, local float64, step, srcFrames int) int {
	f := int(math.Floor((float64(sourceIn)+local)/float64(step))) * step
	if local <= 0 {
		f = sourceIn
	}
	if srcFrames > 0 {
		f = min(f, (srcFrames-1)/step*step)
	}
	return max(f, 0)
}
