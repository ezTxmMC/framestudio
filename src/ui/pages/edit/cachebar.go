package edit

import (
	"slices"
	"testing"
	"time"

	"framestudio-proto/src/ui/timeline"
	"framestudio-proto/src/workgate"
)

// wireCacheBar draws the cached spans of the preview as a thin line under the ruler.
// The spans are computed off the UI thread once a second while the playback is stopped
// and the timeline is redrawn only when they changed.
func (p *Page) wireCacheBar() {
	if p.viewer == nil || p.tl == nil {
		return
	}
	p.tl.SetCacheRanges(func() []timeline.CacheRange {
		if r := p.cacheBar.Load(); r != nil {
			return *r
		}
		return nil
	})
	if testing.Testing() {
		return // the test driver runs UI callbacks on the calling goroutine
	}
	p.cacheStop = make(chan struct{})
	stop := p.cacheStop
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if p.cacheBarDue() && p.updateCacheBar() {
				p.post(p.tl.RefreshCacheBar)
			}
		}
	}()
}

// cacheBarDue is true only while nothing else matters more: not playing, and the work
// gate idle (no playhead drag, which would compete with the scrub renders).
func (p *Page) cacheBarDue() bool {
	return !p.viewer.Playing() && workgate.Default().State() == workgate.Idle
}

// updateCacheBar recomputes the cached spans and reports whether they changed.
func (p *Page) updateCacheBar() bool {
	var rs []timeline.CacheRange
	for _, r := range p.viewer.CacheRanges() {
		rs = append(rs, timeline.CacheRange{Start: r.From, End: r.To})
	}
	if old := p.cacheBar.Load(); (old == nil && len(rs) == 0) || (old != nil && slices.Equal(*old, rs)) {
		return false
	}
	p.cacheBar.Store(&rs)
	return true
}
