package perf

// Regression harness for "the preview must never lag" (budgets of the preview design).
//
// The guarantee is about BEHAVIOUR under load, not raw speed: the UI thread is never
// blocked by preview work, the playhead clock is the master, late pictures are dropped
// instead of queued, background work does not steal CPU from playback. These tests
// run the real viewer, render worker, media cache and the 60 Hz playback clock headless
// (fyne test driver, the test goroutine is the UI thread through kit.UIQueue) and fail
// when a budget is broken. They need no ffmpeg: the projects consist of colour and
// title clips, which load the compositor and the UI hand-off exactly like video.
//
// Run (polite, one core less than the box, each test < 60 s):
//
//	FS_PERF=1 GOMAXPROCS=4 nice -n 19 taskset -c 0-7 go test ./src/perf -run TestBudget -v -count=1
//
// Environment:
//
//	FS_PERF=1               enable (without it every test here is skipped)
//	FS_PERF_SLACK=2.5       multiply every absolute millisecond limit (loaded boxes)
//	FS_PERF_REPORT_ONLY=1   print the numbers and "OVER BUDGET" lines but do not fail
//	FS_PERF_DUR=6           seconds per playback run (default 3)
//	FS_PERF_RUNS=3          runs per scenario; the MEDIAN run decides
//
// Budgets (reference machine, 8 logical CPUs, GOMAXPROCS=4; relative limits scale with
// a CPU calibration measured in the same process, see calibrate):
//
//	UI busy (1x 30 fps, 2560x1440 window, painter emulated)      < 10 %
//	kit.Do latency during playback                                p95 < 8 ms, p99 < 16 ms, max < 40 ms
//	shown pictures at 30 fps                                      >= 29 fps
//	playhead vs wall clock                                        within 3 frames after the run
//	GC pauses per run                                             max <= 4 ms, total <= 10 ms
//	steady-state allocation per rendered 1080p 3-layer frame      <= 2 MB
//	heavy 4K project at auto quality                              >= 24 fps shown, UI latency p95 < 8 ms
//
// Not measurable headless (documented limits): the OpenGL texture upload and the redraw
// of the whole canvas per picture. The painter emulation here replicates fyne's CPU work
// (software scale + NRGBA->RGBA) but not glTexImage2D. A one-off real-GL timing run is
// for the user to do on the real machine, never for an agent: it needs a window.
// The LoadGovernor of the preview (kit.Do latency probe -> workgate.SetUIBusy) is the
// safety net that lowers quality, UI rate and timeline following when the real painter
// turns out heavier than emulated.
//
// Where the numbers stand today (before the preview pipeline rewrite) is documented in
// docs/perf.md and in the commit message; the same tests are the acceptance gate for it.

import (
	"context"
	"fmt"
	"image"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/ui/viewer"
)

func needBudget(t testing.TB) {
	t.Helper()
	if os.Getenv("FS_PERF") == "" {
		t.Skip("set FS_PERF=1 to run the preview budget harness")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

func envFloat(name string, def float64) float64 {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			return v
		}
	}
	return def
}

// slack is the CI factor for absolute millisecond limits: FS_PERF_SLACK times the CPU
// calibration of this machine (a box twice as slow as the reference gets twice the
// time, but never less than the reference limits).
func slack() float64 { return envFloat("FS_PERF_SLACK", 1) * calibrate() }

var calibrated float64

// calibrate times a fixed CPU spin of 20 ms (reference machine: 20 ms) and returns
// max(1, measured/20ms * 0.5): half of the slowdown is forgiven as noise, the rest is
// passed on to the limits.
func calibrate() float64 {
	if calibrated != 0 {
		return calibrated
	}
	best := time.Hour
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		x := 0
		for j := 0; j < 20_000_000; j++ { // ~20 ms on the reference machine
			x += j ^ (x >> 3)
		}
		if x == 42 {
			fmt.Print()
		}
		best = min(best, time.Since(t0))
	}
	calibrated = max(1, float64(best)/float64(20*time.Millisecond)*0.5)
	return calibrated
}

// over reports a broken budget: a test failure, or only a log line in report-only mode.
func over(t *testing.T, name string, got, limit float64, unit string) bool {
	t.Helper()
	if got <= limit {
		t.Logf("ok    %-44s %8.2f %s (limit %.2f)", name, got, unit, limit)
		return false
	}
	if os.Getenv("FS_PERF_REPORT_ONLY") != "" {
		t.Logf("OVER BUDGET  %-36s %8.2f %s (limit %.2f)", name, got, unit, limit)
		return true
	}
	t.Errorf("%s: %.2f %s exceeds the budget of %.2f", name, got, unit, limit)
	return true
}

// under is over for lower bounds (frame rates).
func under(t *testing.T, name string, got, limit float64, unit string) bool {
	t.Helper()
	if got >= limit {
		t.Logf("ok    %-44s %8.2f %s (min %.2f)", name, got, unit, limit)
		return false
	}
	if os.Getenv("FS_PERF_REPORT_ONLY") != "" {
		t.Logf("UNDER BUDGET %-36s %8.2f %s (min %.2f)", name, got, unit, limit)
		return true
	}
	t.Errorf("%s: %.2f %s is below the budget of %.2f", name, got, unit, limit)
	return true
}

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func budgetDur() time.Duration {
	if s := os.Getenv("FS_PERF_DUR"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 3 * time.Second
}

func budgetRuns() int {
	if s := os.Getenv("FS_PERF_RUNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

// layersProject is a project of n full-frame colour layers with a grade each (the
// compositor load of stacked video layers) and a title on top, 30 fps.
func layersProject(w, h, n int, grade bool) *model.Project {
	p := newProject(w, h, model.Rate30)
	ensureVideoTracks(p, n+1)
	vt := p.TrackIndices(model.TrackVideo)
	for i := 0; i < n; i++ {
		c := model.NewClip(model.KindColor, fmt.Sprintf("Ebene %d", i+1), 0, 30*20)
		c.Opacity = 1
		if i > 0 {
			c.Opacity = 0.7
		}
		if grade {
			c.Grade = model.Grade{Exposure: 0.1, Contrast: 0.15, Saturation: 0.2, Temperature: 0.1, Vignette: 0.3}
		}
		if err := p.PlaceClip(p.Tracks[vt[i]].ID, c); err != nil {
			panic(err)
		}
	}
	t := model.NewClip(model.KindTitle, "Titel", 0, 30*20)
	t.Title.Text = "Frame Studio Vorschau"
	if err := p.PlaceClip(p.Tracks[vt[n]].ID, t); err != nil {
		panic(err)
	}
	return p
}

type metric struct {
	name string
	get  func(r vres) float64
}

func runsMedian(t *testing.T, build func() *model.Project, win [2]float32, quality string, emu bool, ms ...metric) map[string]float64 {
	t.Helper()
	vals := map[string][]float64{}
	for i := 0; i < budgetRuns(); i++ {
		r := runViewer(t, fmt.Sprintf("%s run %d", quality, i+1), build(), win[0], win[1], quality, emu, budgetDur())
		r.log(t)
		for _, m := range ms {
			vals[m.name] = append(vals[m.name], m.get(r))
		}
	}
	out := map[string]float64{}
	for k, v := range vals {
		out[k] = median(v)
		t.Logf("median %-28s %.2f  (min %.2f max %.2f over %d runs)", k, out[k], minOf(v), maxOf(v), len(v))
	}
	return out
}

func minOf(v []float64) float64 {
	m := v[0]
	for _, x := range v {
		m = min(m, x)
	}
	return m
}

func maxOf(v []float64) float64 {
	m := v[0]
	for _, x := range v {
		m = max(m, x)
	}
	return m
}

var (
	mUIBusy = metric{"ui_busy_pct", func(r vres) float64 { return 100 * float64(r.uiBusy) / float64(r.dur) }}
	mQ95    = metric{"ui_latency_p95_ms", func(r vres) float64 { return r.qlat.pct(.95) }}
	mQ99    = metric{"ui_latency_p99_ms", func(r vres) float64 { return r.qlat.pct(.99) }}
	mQmax   = metric{"ui_latency_max_ms", func(r vres) float64 { return r.qlat.max() }}
	mFPS    = metric{"shown_fps", func(r vres) float64 { return float64(r.shown) / r.dur.Seconds() }}
	mDrift  = metric{"playhead_drift_frames", func(r vres) float64 {
		return absf(float64(r.endFrame) - 30*r.dur.Seconds())
	}}
	mGCmax = metric{"gc_pause_max_ms", func(r vres) float64 { return float64(r.gc.pauseMax) / 1e6 }}
	mGCtot = metric{"gc_pause_total_ms", func(r vres) float64 { return float64(r.gc.pauseTotal) / 1e6 }}
)

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TestBudgetUIThread: a normal 3-layer 1080p project plays at 30 fps in a big
// (2560x1440) window with the fyne painter hand-off emulated on the UI thread. The UI
// thread must stay almost idle and answer kit.Do within a few milliseconds; the picture
// reaches 29 fps and the playhead stays on the wall clock.
func TestBudgetUIThread(t *testing.T) {
	needBudget(t)
	k := slack()
	for _, q := range []string{viewer.QualityAuto, viewer.QualityFull} {
		t.Run(q, func(t *testing.T) {
			m := runsMedian(t, func() *model.Project { return layersProject(1920, 1080, 3, true) },
				[2]float32{2560, 1440}, q, true, mUIBusy, mQ95, mQ99, mQmax, mFPS, mDrift, mGCmax, mGCtot)
			over(t, "UI busy (painter emulated)", m["ui_busy_pct"], 10, "%")
			over(t, "kit.Do latency p95", m["ui_latency_p95_ms"], 8*k, "ms")
			over(t, "kit.Do latency p99", m["ui_latency_p99_ms"], 16*k, "ms")
			over(t, "kit.Do latency max", m["ui_latency_max_ms"], 40*k, "ms")
			under(t, "shown fps", m["shown_fps"], 29/k, "fps")
			over(t, "playhead drift", m["playhead_drift_frames"], 3, "frames")
			over(t, "GC pause max", m["gc_pause_max_ms"], 4*k, "ms")
			over(t, "GC pause total", m["gc_pause_total_ms"], 10*k, "ms")
		})
	}
}

// TestBudgetHeavyLoad: a 4K project with stacked graded layers saturates the render
// worker on purpose (it cannot reach real time on few cores). Behaviour under load is
// what counts: the UI thread stays responsive, the playhead keeps the wall clock, the
// viewer shows the newest frames (>= 24 fps at auto quality after the quality ladder
// settled) instead of a backlog or a frozen picture.
func TestBudgetHeavyLoad(t *testing.T) {
	needBudget(t)
	k := slack()
	heavy := func() *model.Project { return layersProject(3840, 2160, 6, true) }
	win := [2]float32{2560, 1440}
	stale := metric{"stale_p95_ms", func(r vres) float64 { return r.stale.pct(.95) }}
	t.Run("auto", func(t *testing.T) {
		m := runsMedian(t, heavy, win, viewer.QualityAuto, true, mUIBusy, mQ95, mQ99, mQmax, mFPS, mDrift, stale)
		over(t, "kit.Do latency p95 (heavy, auto)", m["ui_latency_p95_ms"], 8*k, "ms")
		over(t, "kit.Do latency max (heavy, auto)", m["ui_latency_max_ms"], 40*k, "ms")
		over(t, "playhead drift (picture never delays clock)", m["playhead_drift_frames"], 3, "frames")
		under(t, "shown fps (heavy, auto quality)", m["shown_fps"], 24/k, "fps")
		over(t, "stale p95", m["stale_p95_ms"], 150*k, "ms")
	})
	// A pinned full quality may run at its own rate (it can be too slow for the project
	// rate), but it must never take the UI thread with it.
	t.Run("full-pinned", func(t *testing.T) {
		m := runsMedian(t, heavy, win, viewer.QualityFull, true, mUIBusy, mQ95, mQ99, mQmax, mFPS, mDrift)
		over(t, "UI busy (heavy, full pinned)", m["ui_busy_pct"], 25, "%")
		over(t, "kit.Do latency p95 (heavy, full pinned)", m["ui_latency_p95_ms"], 8*k, "ms")
		over(t, "playhead drift (heavy, full pinned)", m["playhead_drift_frames"], 3, "frames")
	})
}

// TestBudgetAllocPerFrame: steady-state allocation of one rendered 1080p 3-layer frame
// (was 26 MB/frame, 93 % image.NewNRGBA).
func TestBudgetAllocPerFrame(t *testing.T) {
	needBudget(t)
	p := layersProject(1920, 1080, 3, true)
	cache := media.NewCache()
	defer cache.Close()
	r := render.New(cache)
	dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080)) // the preview's pooled frame buffer
	ctx := context.Background()
	for i := 0; i < 5; i++ { // warm the pools
		_, _ = r.RenderInto(ctx, dst, p, i, render.Options{})
	}
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	const n = 30
	for i := 0; i < n; i++ {
		_, _ = r.RenderInto(ctx, dst, p, 5+i, render.Options{})
	}
	runtime.ReadMemStats(&b)
	perFrame := float64(b.TotalAlloc-a.TotalAlloc) / n / (1 << 20)
	over(t, "allocation per 1080p 3-layer frame", perFrame, 2, "MB")
}
