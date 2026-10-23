package timeline

import (
	"testing"
	"time"
)

// TestSmoothFollowTickAllocations: the per-frame cost of the timeline while playing
// zoomed in with smooth follow. It was a full rebuild per frame (about 3,400
// allocations, 330 KB); now a tick only moves objects.
func TestSmoothFollowTickAllocations(t *testing.T) {
	f := smoothFixture(t)
	n := testing.AllocsPerRun(50, func() {
		f.s.Advance(10 * time.Millisecond)
	})
	t.Logf("allocations per smooth-follow tick: %.0f", n)
	if n > 150 {
		t.Fatalf("%.0f allocations per tick: the timeline is rebuilt again", n)
	}
}
