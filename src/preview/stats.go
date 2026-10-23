package preview

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const statRing = 64

// Stats collects playback counters with plain atomics (no allocation on the hot path).
// Snapshot turns them into rates over a sliding window of about one second.
type Stats struct {
	rendered, shown, dropped, late atomic.Uint64
	cacheHit, cacheMiss            atomic.Uint64
	quality                        atomic.Int32
	pinned                         atomic.Bool
	src                            atomic.Int32
	underrun                       atomic.Bool
	kfOnly                         atomic.Bool
	uiLatUS                        atomic.Int64 // p95 UI queue latency in microseconds
	renderW, renderH               atomic.Int32
	targetW, targetH               atomic.Int32
	projFPS                        atomic.Int64 // fps*1000
	lastShown                      atomic.Int64 // unix nanos

	ring [statRing]atomic.Int64
	head atomic.Uint32

	mu   sync.Mutex
	hist []statPoint
	base statPoint // counters at the start of the current play run
}

type statPoint struct {
	t                              time.Time
	rendered, shown, dropped, late uint64
	hit, miss                      uint64
}

// StatsSnapshot is a consistent reading for the overlay.
type StatsSnapshot struct {
	RenderedFPS, ShownFPS float64
	DroppedPerSec         float64
	DroppedTotal          uint64 // since the last ResetRun
	LateTotal             uint64
	Quality               Quality
	Pinned                bool
	CachePct              float64 // hit rate over the window; -1 without lookups
	Decoder               string  // sw | hw | proxy | cache
	RenderP50, RenderP90  float64 // ms
	UILatencyMS           float64 // p95 kit.Do latency
	TargetW, TargetH      int
	RenderW, RenderH      int
	ProjectFPS            float64
	UnderRun              bool
	KFOnly                bool
	StaleMS               float64 // since the last shown frame
}

func (s *Stats) addRender(ns int64) {
	i := s.head.Add(1) % statRing
	s.ring[i].Store(ns)
}

// SetUILatency publishes the p95 queue latency of the UI thread.
func (s *Stats) SetUILatency(d time.Duration) { s.uiLatUS.Store(int64(d / time.Microsecond)) }

// SetProjectFPS publishes the project frame rate for the overlay.
func (s *Stats) SetProjectFPS(fps float64) { s.projFPS.Store(int64(fps * 1000)) }

// SetKFOnly flags keyframe-only shuttle.
func (s *Stats) SetKFOnly(on bool) { s.kfOnly.Store(on) }

// CacheLookup records a cache hit or miss.
func (s *Stats) CacheLookup(hit bool) {
	if hit {
		s.cacheHit.Add(1)
	} else {
		s.cacheMiss.Add(1)
	}
}

// Shown records that the UI took a frame.
func (s *Stats) Shown(at time.Time) {
	s.shown.Add(1)
	s.lastShown.Store(at.UnixNano())
}

// Dropped counts n frames that were requested or rendered but never shown.
func (s *Stats) Dropped(n uint64) { s.dropped.Add(n) }

// Counters returns the raw totals (rendered, shown, dropped, late).
func (s *Stats) Counters() (rendered, shown, dropped, late uint64) {
	return s.rendered.Load(), s.shown.Load(), s.dropped.Load(), s.late.Load()
}

// ResetRun starts a new play run: DroppedTotal counts from here.
func (s *Stats) ResetRun(now time.Time) {
	s.mu.Lock()
	s.base = s.point(now)
	s.mu.Unlock()
}

func (s *Stats) point(now time.Time) statPoint {
	return statPoint{now, s.rendered.Load(), s.shown.Load(), s.dropped.Load(), s.late.Load(),
		s.cacheHit.Load(), s.cacheMiss.Load()}
}

// Snapshot computes rates over the last ~1 s. Call it at most a few times per second.
func (s *Stats) Snapshot(now time.Time) StatsSnapshot {
	cur := s.point(now)
	s.mu.Lock()
	// keep points of the last 2 s, compare to the oldest one that is >= 1 s old (or the oldest)
	s.hist = append(s.hist, cur)
	for len(s.hist) > 2 && now.Sub(s.hist[1].t) >= 2*time.Second {
		s.hist = s.hist[1:]
	}
	ref := s.hist[0]
	for _, p := range s.hist {
		if now.Sub(p.t) >= time.Second {
			ref = p
		}
	}
	base := s.base
	s.mu.Unlock()

	dt := cur.t.Sub(ref.t).Seconds()
	rate := func(a, b uint64) float64 {
		if dt <= 0 {
			return 0
		}
		return float64(a-b) / dt
	}
	snap := StatsSnapshot{
		RenderedFPS:   rate(cur.rendered, ref.rendered),
		ShownFPS:      rate(cur.shown, ref.shown),
		DroppedPerSec: rate(cur.dropped, ref.dropped),
		DroppedTotal:  cur.dropped - base.dropped,
		LateTotal:     cur.late - base.late,
		Quality:       Quality(s.quality.Load()),
		Pinned:        s.pinned.Load(),
		CachePct:      -1,
		Decoder:       Source(s.src.Load()).String(),
		UILatencyMS:   float64(s.uiLatUS.Load()) / 1000,
		TargetW:       int(s.targetW.Load()), TargetH: int(s.targetH.Load()),
		RenderW: int(s.renderW.Load()), RenderH: int(s.renderH.Load()),
		ProjectFPS: float64(s.projFPS.Load()) / 1000,
		UnderRun:   s.underrun.Load(),
		KFOnly:     s.kfOnly.Load(),
	}
	if h, m := cur.hit-ref.hit, cur.miss-ref.miss; h+m > 0 {
		snap.CachePct = 100 * float64(h) / float64(h+m)
	}
	if ls := s.lastShown.Load(); ls != 0 {
		snap.StaleMS = float64(now.UnixNano()-ls) / 1e6
	}
	var v [statRing]int64
	n := 0
	for i := range s.ring {
		if x := s.ring[i].Load(); x > 0 {
			v[n] = x
			n++
		}
	}
	if n > 0 {
		sl := v[:n]
		slices.Sort(sl)
		snap.RenderP50 = float64(sl[n/2]) / 1e6
		snap.RenderP90 = float64(sl[min(n-1, n*9/10)]) / 1e6
	}
	return snap
}

// Lines formats the three overlay lines (German UI text).
func (sn StatsSnapshot) Lines() [3]string {
	mode := "auto"
	if sn.Pinned {
		mode = "fest"
	}
	cache := "--"
	if sn.CachePct >= 0 {
		cache = fmt.Sprintf("%.0f%%", sn.CachePct)
	}
	return [3]string{
		fmt.Sprintf("Bild %.1f/%.0f fps  verworfen %d (%.1f/s)", sn.ShownFPS, sn.ProjectFPS, sn.DroppedTotal, sn.DroppedPerSec),
		fmt.Sprintf("Qualität: %s (%s)  %dx%d  Render %.1f ms", sn.Quality, mode, sn.RenderW, sn.RenderH, sn.RenderP50),
		fmt.Sprintf("Cache %s  Decoder: %s  UI %.1f ms", cache, sn.Decoder, sn.UILatencyMS),
	}
}
