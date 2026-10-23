package color

import (
	"image"
	"testing"
	"time"

	"framestudio-proto/src/workgate"
)

func TestScopeInterval(t *testing.T) {
	cases := []struct {
		st   workgate.State
		busy bool
		want time.Duration
	}{
		{workgate.Idle, false, 0},
		{workgate.Idle, true, 0},
		{workgate.Playing, false, 100 * time.Millisecond},
		{workgate.Scrubbing, false, 100 * time.Millisecond},
		{workgate.Playing, true, 500 * time.Millisecond},
	}
	for _, c := range cases {
		if got := scopeInterval(c.st, c.busy); got != c.want {
			t.Errorf("%v busy=%v: %v, want %v", c.st, c.busy, got, c.want)
		}
	}
}

func TestScopeRunnerPacesWhilePlaying(t *testing.T) {
	var slept []time.Duration
	var shown int
	r := &scopeRunner{
		async: func(f func()) { f() },
		show:  func(scopeKind, *image.NRGBA) { shown++ },
		pace:  func() time.Duration { return 100 * time.Millisecond },
		sleep: func(d time.Duration) { slept = append(slept, d) },
	}
	r.submit(testFrame(8, 8), scopeHistogram) // first run is immediate
	r.submit(testFrame(8, 8), scopeHistogram) // right after: must wait for the interval
	if shown != 2 || len(slept) != 1 || slept[0] <= 0 || slept[0] > 100*time.Millisecond {
		t.Fatalf("shown %d, sleeps %v", shown, slept)
	}
}
