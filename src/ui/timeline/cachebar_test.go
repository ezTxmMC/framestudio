package timeline

import (
	"testing"

	"fyne.io/fyne/v2/canvas"
)

func cacheBars(f *fixture) []*canvas.Rectangle {
	var out []*canvas.Rectangle
	for _, o := range f.tl.r.ruler.Objects {
		if r, ok := o.(*canvas.Rectangle); ok && r.FillColor == cacheBar {
			out = append(out, r)
		}
	}
	return out
}

func TestCacheBarShowsCachedRanges(t *testing.T) {
	f := setup(t, 1000, 300)
	if len(cacheBars(f)) != 0 {
		t.Fatal("no cache line without a source")
	}
	f.tl.SetCacheRanges(func() []CacheRange { return []CacheRange{{0, 50}, {100, 150}} })
	bars := cacheBars(f)
	if len(bars) != 2 {
		t.Fatalf("%d cache segments", len(bars))
	}
	if bars[0].Size().Height != 2 || bars[1].Position().X <= bars[0].Position().X+bars[0].Size().Width {
		t.Fatalf("segments: %v %v", bars[0], bars[1])
	}
	f.tl.SetCacheRanges(nil)
	if len(cacheBars(f)) != 0 {
		t.Fatal("line must go away")
	}
}
