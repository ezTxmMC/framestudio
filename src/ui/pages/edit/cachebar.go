package edit

import (
	"slices"
	"time"

	"framestudio-proto/src/ui/timeline"
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
	p.cacheStop = make(chan struct{})
	stop := p.cacheStop
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var last []timeline.CacheRange
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if p.viewer.Playing() {
				continue
			}
			var rs []timeline.CacheRange
			for _, r := range p.viewer.CacheRanges() {
				rs = append(rs, timeline.CacheRange{Start: r.From, End: r.To})
			}
			if slices.Equal(rs, last) {
				continue
			}
			last = rs
			p.cacheBar.Store(&rs)
			p.post(p.tl.RefreshCacheBar)
		}
	}()
}
