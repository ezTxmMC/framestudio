// Package statsui shows what the preview does: a small playback statistics overlay
// (rendered fps, dropped frames, quality, cache hit rate, decoder, UI load) and the
// warning chips ("Wiedergabe überlastet", "Nur Keyframes"). The numbers come from a
// Source, implemented by the preview pipeline; this package only presents them.
package statsui

import (
	"fmt"
	"time"
)

// Snapshot is one reading of the playback statistics, computed over a sliding window of
// about one second by the preview pipeline.
type Snapshot struct {
	Playing        bool
	FPS            float64 // pictures rendered per second
	ProjectFPS     float64
	Dropped        uint64  // frames dropped since play start
	DroppedPerSec  float64 // over the window
	Quality        string  // "Voll", "Halb", "Viertel"
	QualityPinned  bool    // false = automatic
	W, H           int     // size of the rendered picture
	RenderMS       float64 // p50 of the render time
	CachePct       int     // cache hit rate, -1 = no data yet
	Decoder        string  // "sw", "hw", "proxy" or "cache"
	UIBusyPct      int
	UnderRun       bool // playback cannot keep up even at the lowest quality
	KeyframesOnly  bool // shuttle shows keyframes only
	HasData        bool
	HalfRateActive bool // last resort: every 2nd frame rendered
}

// Source delivers snapshots. Snapshot must be cheap and safe to call from any goroutine.
type Source interface {
	Snapshot() Snapshot
}

// SourceFunc adapts a function to a Source.
type SourceFunc func() Snapshot

// Snapshot implements Source.
func (f SourceFunc) Snapshot() Snapshot { return f() }

// Lines formats the three lines of the overlay:
//
//	Bild 29.8/30 fps  verworfen 12 (0.4/s)
//	Qualität: Halb (auto)  640x360  Render 8.2 ms
//	Cache 87%  Decoder: hw  UI 4%
func Lines(s Snapshot) [3]string {
	if !s.HasData {
		return [3]string{"Bild – / – fps", "Qualität: –", "Cache –  Decoder: –  UI –"}
	}
	mode := "auto"
	if s.QualityPinned {
		mode = "fest"
	}
	q := s.Quality
	if q == "" {
		q = "Voll"
	}
	cache := "–"
	if s.CachePct >= 0 {
		cache = fmt.Sprintf("%d%%", s.CachePct)
	}
	dec := s.Decoder
	if dec == "" {
		dec = "sw"
	}
	l1 := fmt.Sprintf("Bild %.1f/%s fps  verworfen %d (%.1f/s)", s.FPS, trimFloat(s.ProjectFPS), s.Dropped, s.DroppedPerSec)
	l2 := fmt.Sprintf("Qualität: %s (%s)  %dx%d  Render %.1f ms", q, mode, s.W, s.H, s.RenderMS)
	l3 := fmt.Sprintf("Cache %s  Decoder: %s  UI %d%%", cache, dec, s.UIBusyPct)
	if s.HalfRateActive {
		l2 += "  15 fps"
	}
	return [3]string{l1, l2, l3}
}

// trimFloat prints 30 as "30" and 29.97 as "29.97".
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// Chip texts.
const (
	ChipOverload  = "Wiedergabe überlastet"
	ChipKeyframes = "Nur Keyframes"
)

// recovery is how long the overload chip stays after the playback caught up again.
const recovery = 3 * time.Second

// chips is the show/hide state machine of the warning chips.
type chips struct {
	overload      bool
	lastUnderrun  time.Time
	keyframesOnly bool
}

// update feeds a snapshot and returns which chips are visible at now. The overload chip
// appears at once and disappears 3 s after the last under-run.
func (c *chips) update(now time.Time, s Snapshot) (overload, keyframes bool) {
	if s.UnderRun {
		c.lastUnderrun = now
		c.overload = true
	} else if c.overload && now.Sub(c.lastUnderrun) >= recovery {
		c.overload = false
	}
	c.keyframesOnly = s.KeyframesOnly && s.Playing
	return c.overload, c.keyframesOnly
}
