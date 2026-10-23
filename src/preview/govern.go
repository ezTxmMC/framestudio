package preview

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// LoadGovernor watches how long the UI thread takes to pick up a posted closure. A UI
// that is congested by a slow painter (weak GPU, huge window) shows up as queue
// latency, which is exactly what the user feels. When the latency stays high the
// governor reports Busy so quality steps down and UI-heavy features back off.
type LoadGovernor struct {
	HighP95 time.Duration // busy above this (default 8 ms)
	LowP95  time.Duration // recovered below this (default 4 ms)
	HighFor time.Duration // how long it must stay high (default 2 s)
	LowFor  time.Duration // how long it must stay low (default 3 s)

	busy atomic.Bool

	mu        sync.Mutex
	lat       []latSample
	highSince time.Time
	lowSince  time.Time
	lastP95   time.Duration
}

type latSample struct {
	at time.Time
	d  time.Duration
}

// NewLoadGovernor returns a governor with the default thresholds.
func NewLoadGovernor() *LoadGovernor {
	return &LoadGovernor{HighP95: 8 * time.Millisecond, LowP95: 4 * time.Millisecond,
		HighFor: 2 * time.Second, LowFor: 3 * time.Second}
}

// Busy reports whether the UI thread is congested.
func (g *LoadGovernor) Busy() bool { return g.busy.Load() }

// P95 is the latest 1 s p95 of the probe latency.
func (g *LoadGovernor) P95() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastP95
}

// Record adds one probe latency measured at time at and re-evaluates.
func (g *LoadGovernor) Record(at time.Time, latency time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lat = append(g.lat, latSample{at, latency})
	i := 0
	for i < len(g.lat) && at.Sub(g.lat[i].at) > time.Second {
		i++
	}
	g.lat = append(g.lat[:0], g.lat[i:]...)
	if len(g.lat) < 5 {
		return
	}
	var buf [64]time.Duration
	v := buf[:0]
	for _, s := range g.lat {
		v = append(v, s.d)
	}
	slices.Sort(v)
	p95 := v[min(len(v)-1, len(v)*95/100)]
	g.lastP95 = p95

	if !g.busy.Load() {
		if p95 > g.HighP95 {
			if g.highSince.IsZero() {
				g.highSince = at
			}
			if at.Sub(g.highSince) >= g.HighFor {
				g.busy.Store(true)
				g.lowSince = time.Time{}
			}
		} else {
			g.highSince = time.Time{}
		}
		return
	}
	if p95 < g.LowP95 {
		if g.lowSince.IsZero() {
			g.lowSince = at
		}
		if at.Sub(g.lowSince) >= g.LowFor {
			g.busy.Store(false)
			g.highSince = time.Time{}
		}
	} else {
		g.lowSince = time.Time{}
	}
}

// Probe posts one stamped closure through post (kit.Do) and records its latency.
// Call it every 100 ms while playing; it never blocks.
func (g *LoadGovernor) Probe(post func(func())) {
	t0 := time.Now()
	post(func() { g.Record(time.Now(), time.Since(t0)) })
}

// Reset clears the history and the busy flag (playback stopped).
func (g *LoadGovernor) Reset() {
	g.mu.Lock()
	g.lat, g.highSince, g.lowSince = g.lat[:0], time.Time{}, time.Time{}
	g.mu.Unlock()
	g.busy.Store(false)
}
