package viewer

import (
	"sync"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/preview"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/workgate"
)

// live viewers, for the commands "Vorschau-Cache leeren" and the memory watcher.
var live struct {
	mu sync.Mutex
	vs map[*Viewer]struct{}
	// share is the memory watcher's current share of the configured cache budgets.
	share float64
}

func register(v *Viewer) {
	live.mu.Lock()
	if live.vs == nil {
		live.vs = map[*Viewer]struct{}{}
	}
	live.vs[v] = struct{}{}
	live.mu.Unlock()
}

func unregister(v *Viewer) {
	live.mu.Lock()
	delete(live.vs, v)
	live.mu.Unlock()
}

// ClearCaches empties the rendered-frame cache of every viewer (command cache.clear).
func ClearCaches() {
	live.mu.Lock()
	vs := make([]*Viewer, 0, len(live.vs))
	for v := range live.vs {
		vs = append(vs, v)
	}
	live.mu.Unlock()
	for _, v := range vs {
		v.cache.Clear()
	}
}

// Budgets lets the memory watcher shrink the rendered-frame caches of all viewers.
type Budgets struct{}

// ScaleBudget implements application.Budgeter.
func (Budgets) ScaleBudget(share float64) {
	live.mu.Lock()
	live.share = share
	vs := make([]*Viewer, 0, len(live.vs))
	for v := range live.vs {
		vs = append(vs, v)
	}
	live.mu.Unlock()
	for _, v := range vs {
		v.applyCacheBudget(settings.Get())
	}
}

// cacheBytes is the budget of the rendered-frame cache for the setting Playback.CacheMB
// (0 = automatic, -1 = off): two thirds of the preview cache, the rest goes to decoded
// source frames.
func cacheBytes(s settings.Settings) int64 {
	var total int64
	switch mb := s.Playback.CacheMB; {
	case mb < 0:
		return 0
	case mb == 0:
		total = preview.DefaultCacheBytes()
	default:
		total = int64(mb) << 20
	}
	b := min(total*2/3, 1<<30)
	live.mu.Lock()
	share := live.share
	live.mu.Unlock()
	if share > 0 && share < 1 {
		b = int64(float64(b) * share)
	}
	return b
}

func (v *Viewer) applyCacheBudget(s settings.Settings) {
	if b := cacheBytes(s); b != v.cacheBudget.Swap(b) {
		v.cache.SetBudget(b)
	}
}

// prewarm tells the media layer where playback or a scrub is going to read next, so the
// decoders, keyframe indexes and ladders are ready (debounced; never blocks).
func (v *Viewer) prewarm() {
	if v.closed || v.r == nil || v.r.Media == nil {
		return
	}
	if v.s.Playing {
		return // playback has its decoders already
	}
	w, h := v.sched.PlanSize(preview.ReqStill, 1)
	v.warmProj.Store(v.snap)
	v.warmPos.Store(int64(v.s.Playhead))
	v.warmW.Store(int64(w))
	v.warmH.Store(int64(h))
	v.prewarmMu.Lock()
	defer v.prewarmMu.Unlock()
	if v.prewarmTimer == nil {
		v.prewarmTimer = time.AfterFunc(120*time.Millisecond, v.prewarmNow)
		return
	}
	v.prewarmTimer.Reset(120 * time.Millisecond)
}

func (v *Viewer) prewarmNow() {
	if v.playingA.Load() {
		return
	}
	v.warmMedia(v.warmProj.Load(), int(v.warmPos.Load()), int(v.warmW.Load()), int(v.warmH.Load()))
}

// warmMedia prepares the decoders of the video clips at frame f for pictures of tw x th.
func (v *Viewer) warmMedia(p *model.Project, f, tw, th int) {
	if p == nil || v.r == nil || v.r.Media == nil {
		return
	}
	rate := p.Rate()
	for i := range p.Tracks {
		t := &p.Tracks[i]
		if t.Kind != model.TrackVideo || t.Hidden {
			continue
		}
		if ci := t.ClipAt(f); ci >= 0 && t.Clips[ci].Kind == model.KindVideo {
			c := &t.Clips[ci]
			if m, ok := p.MediaByID(c.MediaID); ok && m.Kind == model.KindVideo {
				n := max(0, c.SourceIn+f-c.Start)
				v.r.Media.Prewarm(*m, rate.Reduced().FramesToDuration(n), rate, max(tw, 2), max(th, 2))
			}
		}
	}
}

// setBusy mirrors the governor's verdict to the rest of the UI (timeline paging, scope rate).
func (v *Viewer) setBusy() { workgate.SetUIBusy(v.gov.Busy()) }

// CacheRanges lists the timeline spans [From, To) whose rendered pictures are cached and
// still match the project (entries of edited clips do not count). Safe from any
// goroutine; it stats the media files, so keep it off the UI thread.
func (v *Viewer) CacheRanges() []preview.FrameRange {
	p := v.warmProj.Load()
	if p == nil {
		return nil
	}
	hs := preview.BuildHasher(p)
	return v.cache.Ranges(func(f int, h uint64) bool { return hs.FrameHash(f, false) == h })
}

// Playing reports whether the session plays (safe from any goroutine).
func (v *Viewer) Playing() bool { return v.playingA.Load() }
