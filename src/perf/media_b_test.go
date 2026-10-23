package perf

import (
	"image"
	"testing"
	"time"

	"framestudio-proto/src/media"
)

// shuttleDistinct drives a shuttle like the viewer's render worker (playhead = wall clock,
// the worker asks for the frame that is due and skips the ones it was too slow for) and
// counts how many different pictures came back and how long the picture stood still.
// Uses only the plain Cache API, so it runs against older trees for before/after numbers.
type shuttleResult struct {
	calls, distinct int
	firstMs         float64
	callMax         float64 // slowest single request
	staleMax        float64 // longest time without a new picture
}

func shuttleDistinct(t *testing.T, c *media.Cache, name string, start int, rate float64, w, h int, dur time.Duration) shuttleResult {
	t.Helper()
	m := probe(t, name)
	fps := fpsOf(m)
	total := fps.DurationToFrames(m.Duration) - 1
	var res shuttleResult
	var lastImg image.Image
	t0 := time.Now()
	lastChange := t0
	lastN := -1 << 30
	for time.Since(t0) < dur {
		n := start + int(rate*time.Since(t0).Seconds()*fps.Float())
		if n < 0 || n >= total {
			break
		}
		if n == lastN {
			time.Sleep(time.Millisecond)
			continue
		}
		a := time.Now()
		img, err := c.PlaybackFrame(m, n, fps, w, h, rate)
		if err != nil {
			t.Fatalf("frame %d: %v", n, err)
		}
		d := time.Since(a)
		if res.calls == 0 {
			res.firstMs = float64(d) / 1e6
			lastChange = time.Now()
		}
		res.callMax = max(res.callMax, float64(d)/1e6)
		res.calls++
		lastN = n
		if img != lastImg {
			res.distinct++
			res.staleMax = max(res.staleMax, float64(time.Since(lastChange))/1e6)
			lastChange = time.Now()
			lastImg = img
		}
	}
	return res
}

// TestPerfShuttlePictures: pictures per second and stale time of fast and reverse play on
// the original files (no proxy).
func TestPerfShuttlePictures(t *testing.T) {
	need(t)
	for _, tc := range []mcase{mediaCases[0], mediaCases[1], mediaCases[6]} {
		m := probe(t, tc.file)
		fps := fpsOf(m)
		total := fps.DurationToFrames(m.Duration) - 2
		for _, rate := range []float64{4, 8, -1, -4, -8} {
			c := media.NewCache()
			start := 0
			if rate < 0 {
				start = total - 3
			}
			dur := 1500 * time.Millisecond
			r := shuttleDistinct(t, c, tc.file, start, rate, 854, 480, dur)
			t.Logf("PICTURES %-22s %+gx first=%4.0fms calls=%3d distinct=%3d (%.1f/s) slowestCall=%4.0fms staleMax=%4.0fms",
				tc.name, rate, r.firstMs, r.calls, r.distinct, float64(r.distinct)/dur.Seconds(), r.callMax, r.staleMax)
			c.Close()
		}
	}
}
