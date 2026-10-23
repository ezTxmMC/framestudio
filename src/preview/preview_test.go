package preview

import (
	"context"
	"image"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

// ---- fakes ----

type fakeRender struct {
	mu    sync.Mutex
	calls []call
	delay func(size int, frame int) time.Duration
	proxy bool // report the pictures as proxy-sourced
}

type call struct {
	frame, w, h int
	rate        float64
	acc         bool
}

func (f *fakeRender) RenderInto(ctx context.Context, dst *image.RGBA, p *model.Project, frame int, o render.Options) (render.Info, error) {
	f.mu.Lock()
	acc := o.Preview != nil && o.Preview.Accurate
	f.calls = append(f.calls, call{frame, dst.Rect.Dx(), dst.Rect.Dy(), o.Rate, acc})
	if f.proxy && o.Preview != nil && o.Preview.Report != nil && !acc {
		o.Preview.Report.Src = media.SrcProxy
	}
	d := time.Duration(0)
	if f.delay != nil {
		d = f.delay(dst.Rect.Dx(), frame)
	}
	f.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return render.Info{}, ctx.Err()
		}
	}
	dst.Pix[0] = uint8(frame)
	dst.Pix[3] = 255
	return render.Info{Opaque: true}, ctx.Err()
}

func (f *fakeRender) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRender) last() call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func testProject() *model.Project {
	p := &model.Project{}
	p.Settings.Width, p.Settings.Height = 1920, 1080
	p.Settings.FPS = model.Rate30
	p.Tracks = []model.Track{{ID: "v1", Kind: model.TrackVideo, Clips: []model.Clip{
		{ID: "a", Kind: model.KindColor, Start: 0, Length: 100, Opacity: 1},
		{ID: "b", Kind: model.KindColor, Start: 100, Length: 100, Opacity: 1},
	}}}
	return p
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func newSched(t *testing.T, fr *fakeRender, cfg Config) *Scheduler {
	t.Helper()
	before := runtime.NumGoroutine()
	s := New(cfg, fr)
	s.SetProject(testProject(), 1)
	s.SetTarget(Target{640, 360})
	t.Cleanup(func() {
		s.Close()
		time.Sleep(20 * time.Millisecond)
		if n := runtime.NumGoroutine(); n > before+1 {
			t.Errorf("goroutine leak: %d -> %d", before, n)
		}
	})
	return s
}

// ---- scheduler ----

func TestSchedulerLatestWins(t *testing.T) {
	fr := &fakeRender{delay: func(int, int) time.Duration { return 20 * time.Millisecond }}
	s := newSched(t, fr, Config{})
	s.Request(1, ReqPlay)
	waitFor(t, "first render start", func() bool { return fr.n() == 1 })
	for i := 2; i <= 9; i++ { // pile up while frame 1 renders
		s.Request(i, ReqPlay)
	}
	waitFor(t, "frame 9", func() bool { return fr.n() >= 2 && fr.last().frame == 9 })
	waitFor(t, "idle", func() bool { f := s.Ready().Load(); return f != nil && f.Pos == 9 })
	if n := fr.n(); n != 2 {
		t.Fatalf("expected 2 renders (1 and the newest), got %d: %+v", n, fr.calls)
	}
	_, _, dropped, _ := s.Stats().Counters()
	if dropped < 7 {
		t.Fatalf("dropped = %d, want >= 7", dropped)
	}
}

func TestSchedulerReadySlotHoldsOneFrame(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{})
	var notified atomic.Int32
	s.OnReady(func() { notified.Add(1) })
	for i := 0; i < 5; i++ {
		s.Request(i, ReqPlay)
		waitFor(t, "render", func() bool { return fr.n() == i+1 })
	}
	waitFor(t, "notify", func() bool { return notified.Load() == 5 })
	f := s.Take()
	if f == nil || f.Pos != 4 {
		t.Fatalf("ready frame = %+v, want pos 4", f)
	}
	if s.Take() != nil {
		t.Fatal("slot must be empty after Take")
	}
	f.Release()
	_, _, dropped, _ := s.Stats().Counters()
	if dropped != 4 {
		t.Fatalf("dropped = %d, want 4 unshown frames", dropped)
	}
}

func TestSchedulerScrubCancelsInFlight(t *testing.T) {
	started := make(chan struct{}, 4)
	fr := &fakeRender{}
	fr.delay = func(_, frame int) time.Duration {
		if frame == 10 {
			started <- struct{}{}
			return 5 * time.Second
		}
		return 0
	}
	s := newSched(t, fr, Config{})
	s.Request(10, ReqScrub)
	<-started
	t0 := time.Now()
	s.Request(20, ReqScrub)
	waitFor(t, "frame 20", func() bool { f := s.Ready().Load(); return f != nil && f.Pos == 20 })
	if time.Since(t0) > time.Second {
		t.Fatal("scrub did not cancel the in-flight render")
	}
}

func TestSchedulerPlayNotCancelledByNewerPlay(t *testing.T) {
	fr := &fakeRender{delay: func(_, frame int) time.Duration {
		if frame == 1 {
			return 60 * time.Millisecond
		}
		return 0
	}}
	s := newSched(t, fr, Config{})
	s.Request(1, ReqPlay)
	waitFor(t, "start", func() bool { return fr.n() == 1 })
	s.Request(2, ReqPlay)
	waitFor(t, "2", func() bool { f := s.Ready().Load(); return f != nil && f.Pos == 2 })
	// frame 1 must have completed (it was not cancelled) and been replaced by 2
	if fr.n() != 2 {
		t.Fatalf("renders = %d", fr.n())
	}
}

func TestSchedulerIdleRefineToFull(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{RefineDelay: 30 * time.Millisecond})
	s.Request(5, ReqPlay)
	s.Request(6, ReqPlay)
	waitFor(t, "play frame", func() bool { return fr.n() >= 1 })
	// force the ladder down by hand: pin none, so use the controller via late samples
	s.mu.Lock()
	s.qc.level = QHalf
	s.mu.Unlock()
	s.Request(6, ReqScrub)
	s.Request(7, ReqScrub) // part of a drag: half size, then the idle refine
	waitFor(t, "half frame", func() bool { c := fr.last(); return c.frame == 7 && c.w == 320 })
	waitFor(t, "full still", func() bool { c := fr.last(); return c.frame == 7 && c.w == 640 })
	_, _, _, _ = s.Stats().Counters()
	if f := s.Ready().Load(); f == nil || f.Q != QFull {
		t.Fatalf("expected full quality still, got %+v", f)
	}
}

func TestSchedulerPinnedQualityRenderSizes(t *testing.T) {
	for _, tc := range []struct {
		pin  string
		w, h int
	}{{"full", 640, 360}, {"half", 320, 180}, {"quarter", 160, 90}} {
		fr := &fakeRender{}
		s := New(Config{}, fr)
		s.SetProject(testProject(), 1)
		s.SetTarget(Target{640, 360})
		s.SetQualityPin(tc.pin)
		s.Request(3, ReqPlay)
		waitFor(t, tc.pin, func() bool { return fr.n() > 0 })
		if c := fr.last(); c.w != tc.w || c.h != tc.h {
			t.Errorf("%s: rendered %dx%d, want %dx%d", tc.pin, c.w, c.h, tc.w, tc.h)
		}
		s.Close()
	}
}

func TestSchedulerCacheHitSkipsRender(t *testing.T) {
	fr := &fakeRender{}
	cache := NewFrameCache(64 << 20)
	s := newSched(t, fr, Config{Cache: cache})
	s.Request(12, ReqPlay)
	waitFor(t, "render", func() bool { return fr.n() == 1 })
	waitFor(t, "ready", func() bool { return s.Ready().Load() != nil })
	s.Take().Release()
	s.Request(12, ReqPlay)
	waitFor(t, "hit", func() bool { return s.Ready().Load() != nil })
	if fr.n() != 1 {
		t.Fatalf("cache hit rendered again (%d renders)", fr.n())
	}
	f := s.Take()
	if f.Img.Pix[0] != 12 {
		t.Fatal("wrong cached pixels")
	}
	f.Release()
	if st := cache.Stats(); st.Hits != 1 {
		t.Fatalf("hits = %d", st.Hits)
	}
}

func TestSchedulerNeverRendersLargerThanTarget(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{})
	s.SetTarget(Target{301, 169})
	s.Request(1, ReqStill)
	waitFor(t, "render", func() bool { return fr.n() > 0 })
	if c := fr.last(); c.w != 301 || c.h != 169 {
		t.Fatalf("still must match the target exactly, got %dx%d", c.w, c.h)
	}
}

func TestSchedulerMaxHCapsPlayback(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{MaxH: 180})
	s.Request(1, ReqPlay)
	waitFor(t, "render", func() bool { return fr.n() > 0 })
	if c := fr.last(); c.h != 180 {
		t.Fatalf("cap ignored: %dx%d", c.w, c.h)
	}
}

func TestSchedulerCloseIsIdempotentAndStopsWork(t *testing.T) {
	fr := &fakeRender{}
	s := New(Config{}, fr)
	s.SetProject(testProject(), 1)
	s.SetTarget(Target{320, 180})
	s.Close()
	s.Close()
	s.Request(1, ReqPlay)
	time.Sleep(10 * time.Millisecond)
	if fr.n() != 0 {
		t.Fatal("rendered after Close")
	}
}

// ---- frames and pools ----

func TestSizeFor(t *testing.T) {
	for _, tc := range []struct {
		t    Target
		q    Quality
		w, h int
	}{
		{Target{1920, 1080}, QFull, 1920, 1080},
		{Target{1921, 1081}, QFull, 1921, 1081},
		{Target{1920, 1080}, QHalf, 960, 540},
		{Target{1920, 1080}, QQuarter, 480, 270},
		{Target{400, 225}, QQuarter, 160, 90},
		{Target{100, 56}, QFull, 100, 56},
	} {
		if w, h := SizeFor(tc.t, tc.q); w != tc.w || h != tc.h {
			t.Errorf("SizeFor(%v,%v) = %dx%d, want %dx%d", tc.t, tc.q, w, h, tc.w, tc.h)
		}
	}
}

func TestTextureSizeRounding(t *testing.T) {
	if w, h := TextureSize(800.4, 450.5, 1); w != 800 || h != 451 {
		t.Fatalf("scale 1: %dx%d", w, h)
	}
	if w, h := TextureSize(801, 451, 1.25); w != 1001 || h != 564 {
		t.Fatalf("scale 1.25: %dx%d", w, h)
	}
}

func TestFrameRefcountAndPool(t *testing.T) {
	p := NewBufPool(64, 36, 2)
	f := p.Get()
	f.Retain()
	f.Release()
	if len(p.free) != 0 {
		t.Fatal("recycled while referenced")
	}
	f.Release()
	if len(p.free) != 1 {
		t.Fatal("not recycled")
	}
	g := p.Get()
	if p.Allocs() != 1 {
		t.Fatalf("allocs = %d, want 1 (buffer reuse)", p.Allocs())
	}
	g.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("double release must panic")
		}
	}()
	g.Release()
}

func TestFrameNRGBAUnpremultiplies(t *testing.T) {
	p := NewBufPool(2, 1, 1)
	f := p.Get()
	copy(f.Img.Pix, []uint8{50, 0, 0, 100, 10, 20, 30, 255})
	n := f.NRGBA()
	if n.Pix[0] != 128 || n.Pix[3] != 100 || n.Pix[4] != 10 {
		t.Fatalf("%v", n.Pix)
	}
}

// ---- quality controller (virtual clock) ----

type qcDriver struct {
	q   *QualityController
	now time.Time
}

func newQC() *qcDriver {
	return &qcDriver{q: NewQualityController(), now: time.Unix(1000, 0)}
}

// runLoad feeds n frames of a workload costing fullCost at full quality and a quarter
// of that per step down (area scaling); a frame is late when it costs more than a period.
func (d *qcDriver) runLoad(n int, fullCost time.Duration) Quality {
	var lvl Quality
	for range n {
		c := fullCost >> (2 * uint(d.q.Level()))
		lvl = d.run(1, c, c > period)
	}
	return lvl
}

const period = 33333 * time.Microsecond

// run feeds n frames costing cost each, one per period, and returns the final level.
func (d *qcDriver) run(n int, cost time.Duration, late bool) Quality {
	var lvl Quality
	for range n {
		d.now = d.now.Add(period)
		lvl = d.q.Observe(Sample{At: d.now, Period: period, RenderNS: int64(cost), Late: late, Playing: true, Speed: 1})
	}
	return lvl
}

func TestQCStepsDownWhenOverBudget(t *testing.T) {
	d := newQC()
	// 1.6 periods per frame at full: emergency step down within a few frames
	if lvl := d.runLoad(10, period*16/10); lvl != QHalf {
		t.Fatalf("level = %v, want half after emergency", lvl)
	}
	// at half the cost is 0.4 period: stays
	if lvl := d.run(60, period*4/10, false); lvl != QHalf {
		t.Fatalf("level = %v, want half to stay", lvl)
	}
}

func TestQCStepsDownOnP90(t *testing.T) {
	d := newQC()
	// 0.9 period is under the emergency limit but over 0.8 p90
	lvl := d.runLoad(40, period*9/10)
	if lvl != QHalf {
		t.Fatalf("level = %v, want half", lvl)
	}
}

func TestQCRecoversWithHysteresis(t *testing.T) {
	d := newQC()
	d.runLoad(10, period*2) // -> half
	if d.q.Level() != QHalf {
		t.Fatal("setup")
	}
	// cheap: 0.1 period -> predicted 0.32 < 0.7; needs >= 1 s dwell and a full window
	d.run(20, period/10, false)
	if d.q.Level() != QHalf {
		t.Fatal("stepped up before the dwell time")
	}
	d.run(40, period/10, false)
	if d.q.Level() != QFull {
		t.Fatalf("did not recover, level %v", d.q.Level())
	}
}

func TestQCNoStepUpWhenPredictionTooHigh(t *testing.T) {
	d := newQC()
	d.runLoad(10, period*2) // -> half
	// 0.3 period at half would predict 0.96 at full: stay
	d.run(200, period*3/10, false)
	if d.q.Level() != QHalf {
		t.Fatalf("flapped up to %v", d.q.Level())
	}
}

func TestQCFlappingDoublesDwell(t *testing.T) {
	d := newQC()
	d.runLoad(10, period*2)
	d.run(60, period/10, false) // up
	if d.q.Level() != QFull {
		t.Fatal("setup: no step up")
	}
	d.run(3, period*2, true) // undone within 5 s
	if d.q.Level() != QHalf || d.q.Dwell() != 2*time.Second {
		t.Fatalf("level %v dwell %v, want half and 2s", d.q.Level(), d.q.Dwell())
	}
}

func TestQCSyntheticLoadCycle(t *testing.T) {
	// load of 1.6 periods at full, linear in area: half = 0.4, quarter = 0.1
	d := newQC()
	cost := func(l Quality) time.Duration { return period * 16 / 10 >> (2 * uint(l)) }
	t0 := d.now
	reversals := 0
	dir := 0
	prev := QFull
	var downAt time.Duration
	for i := 0; i < 400; i++ {
		l := d.q.Level()
		lvl := d.run(1, cost(l), cost(l) > period)
		if lvl != prev {
			nd := 1
			if lvl < prev {
				nd = -1
			}
			if dir != 0 && nd != dir {
				reversals++
			}
			dir = nd
			if downAt == 0 && nd == 1 {
				downAt = d.now.Sub(t0)
			}
			prev = lvl
		}
	}
	if downAt > time.Second {
		t.Fatalf("stepped down after %v, want within 1 s", downAt)
	}
	if reversals > 1 {
		t.Fatalf("%d reversals, want <= 1", reversals)
	}
	if d.q.Level() == QFull {
		t.Fatal("must not return to full while the load persists")
	}
	// load removed: back to full within 3 s
	start := d.now
	for d.q.Level() != QFull && d.now.Sub(start) < 6*time.Second {
		d.run(1, period/20, false)
	}
	if got := d.now.Sub(start); got > 3*time.Second || d.q.Level() != QFull {
		t.Fatalf("recovered in %v to %v, want full within 3 s", got, d.q.Level())
	}
}

func TestQCShuttleNeverFull(t *testing.T) {
	d := newQC()
	d.now = d.now.Add(period)
	if l := d.q.Observe(Sample{At: d.now, Period: period, RenderNS: 1, Playing: true, Speed: 4}); l != QHalf {
		t.Fatalf("4x shuttle level %v", l)
	}
}

func TestQCUIBusyForcesStep(t *testing.T) {
	d := newQC()
	var lvl Quality
	for range 40 {
		d.now = d.now.Add(period)
		lvl = d.q.Observe(Sample{At: d.now, Period: period, RenderNS: int64(period / 10), Playing: true, Speed: 1, UIBusy: true})
	}
	if lvl == QFull {
		t.Fatal("UI busy must lower the quality")
	}
}

func TestQCUnderRunAndHalfRate(t *testing.T) {
	d := newQC()
	d.run(100, period*3, true)
	if d.q.Level() != QQuarter || !d.q.UnderRun() || !d.q.HalfRate() {
		t.Fatalf("level %v under %v half %v", d.q.Level(), d.q.UnderRun(), d.q.HalfRate())
	}
	d.run(120, period/10, false)
	if d.q.UnderRun() || d.q.HalfRate() {
		t.Fatal("underrun must lift after 2 s of headroom")
	}
}

// ---- governor ----

func TestGovernorBusyAndRecovery(t *testing.T) {
	g := NewLoadGovernor()
	now := time.Unix(0, 0)
	feed := func(sec float64, lat time.Duration) {
		for range int(sec * 10) {
			now = now.Add(100 * time.Millisecond)
			g.Record(now, lat)
		}
	}
	feed(1.5, 20*time.Millisecond)
	if g.Busy() {
		t.Fatal("busy too early")
	}
	feed(1, 20*time.Millisecond)
	if !g.Busy() {
		t.Fatal("not busy after 2.5 s of slow probes")
	}
	feed(2, time.Millisecond)
	if !g.Busy() {
		t.Fatal("recovered too early")
	}
	feed(3, time.Millisecond)
	if g.Busy() {
		t.Fatal("did not recover")
	}
}

// ---- cache and hasher ----

func key(f int, h uint64) CacheKey { return CacheKey{ProjHash: h, Frame: f, W: 16, H: 9} }

func frameOf(p *BufPool) *Frame { return p.Get() }

func TestCacheLRUByBytes(t *testing.T) {
	pool := NewBufPool(16, 9, 8) // 576 bytes
	c := NewFrameCache(576 * 3)
	for i := range 5 {
		f := frameOf(pool)
		c.Put(key(i, 1), f)
		f.Release()
	}
	st := c.Stats()
	if st.Entries != 3 || st.Bytes > st.Budget {
		t.Fatalf("%+v", st)
	}
	if _, ok := c.Get(key(0, 1)); ok {
		t.Fatal("oldest must be evicted")
	}
	f, ok := c.Get(key(4, 1))
	if !ok {
		t.Fatal("newest missing")
	}
	f.Release()
}

func TestCachePinProtectsPlayheadWindow(t *testing.T) {
	pool := NewBufPool(16, 9, 8)
	c := NewFrameCache(576 * 3)
	c.Pin(0, 1)
	for i := range 6 {
		f := pool.Get()
		c.Put(key(i, 1), f)
		f.Release()
	}
	for _, fr := range []int{0, 1} {
		if !c.Has(key(fr, 1)) {
			t.Fatalf("pinned frame %d evicted", fr)
		}
	}
}

func TestCacheNearestRangesInvalidate(t *testing.T) {
	pool := NewBufPool(16, 9, 8)
	c := NewFrameCache(1 << 20)
	for _, fr := range []int{10, 11, 12, 20} {
		f := pool.Get()
		c.Put(key(fr, 7), f)
		f.Release()
	}
	hashAt := func(int) uint64 { return 7 }
	f, d, ok := c.GetNearest(hashAt, 14, 6, 16, 9, QFull)
	if !ok || d != 2 {
		t.Fatalf("nearest d=%d ok=%v", d, ok)
	}
	f.Release()
	if _, _, ok := c.GetNearest(hashAt, 15, 2, 16, 9, QFull); ok {
		t.Fatal("nothing within 2 frames of 15")
	}
	r := c.Ranges(nil)
	if len(r) != 2 || r[0] != (FrameRange{10, 13}) || r[1] != (FrameRange{20, 21}) {
		t.Fatalf("ranges %v", r)
	}
	c.Invalidate(11, 12)
	if r := c.Ranges(nil); len(r) != 2 || r[0] != (FrameRange{10, 11}) {
		t.Fatalf("ranges after invalidate %v", r)
	}
	c.Clear()
	if st := c.Stats(); st.Entries != 0 || st.Bytes != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestCacheReleasesFramesOnEviction(t *testing.T) {
	pool := NewBufPool(16, 9, 8)
	c := NewFrameCache(576)
	f := pool.Get()
	c.Put(key(1, 1), f)
	f.Release()
	g := pool.Get()
	c.Put(key(2, 1), g) // evicts 1, which returns its buffer
	g.Release()
	if len(pool.free) != 1 {
		t.Fatalf("pool free = %d", len(pool.free))
	}
}

func TestHasherInvalidatesOnlyTouchedRange(t *testing.T) {
	p := testProject()
	h1 := BuildHasher(p)
	q := *p
	q.Tracks = []model.Track{p.Tracks[0]}
	q.Tracks[0].Clips = append([]model.Clip(nil), p.Tracks[0].Clips...)
	q.Tracks[0].Clips[1].Grade.Exposure = 0.5 // edit clip b (frames 100..199)
	h2 := BuildHasher(&q)
	for _, f := range []int{0, 50, 99} {
		if h1.FrameHash(f, false) != h2.FrameHash(f, false) {
			t.Errorf("frame %d changed by an edit elsewhere", f)
		}
	}
	for _, f := range []int{100, 150, 199} {
		if h1.FrameHash(f, false) == h2.FrameHash(f, false) {
			t.Errorf("frame %d must change", f)
		}
	}
	// names, selection-like and audio data do not matter
	r := q
	r.Tracks = []model.Track{q.Tracks[0]}
	r.Tracks[0].Clips = append([]model.Clip(nil), q.Tracks[0].Clips...)
	r.Tracks[0].Clips[0].Name, r.Tracks[0].Clips[0].Volume = "x", 0.3
	if BuildHasher(&r).FrameHash(10, false) != h2.FrameHash(10, false) {
		t.Fatal("name/volume must not invalidate pictures")
	}
	if h2.FrameHash(10, false) == h2.FrameHash(10, true) {
		t.Fatal("compare picture must have its own hash")
	}
	// canvas change affects everything
	s := q
	s.Settings.Width = 1280
	if BuildHasher(&s).FrameHash(10, false) == h2.FrameHash(10, false) {
		t.Fatal("canvas change must change every frame")
	}
}

func TestSecondPassHitsCache(t *testing.T) {
	fr := &fakeRender{}
	cache := NewFrameCache(256 << 20)
	s := newSched(t, fr, Config{Cache: cache})
	pass := func() {
		for f := 0; f < 60; f++ {
			s.Request(f, ReqPlay)
			waitFor(t, "ready", func() bool { x := s.Ready().Load(); return x != nil && x.Pos == f })
			s.Take().Release()
		}
	}
	pass()
	n := fr.n()
	pass()
	if fr.n() != n {
		t.Fatalf("second pass rendered %d frames", fr.n()-n)
	}
	// edit clip a: only its frames miss
	q := *testProject()
	q.Tracks[0].Clips[0].Opacity = 0.5
	s.SetProject(&q, 2)
	for f := 90; f < 110; f++ {
		s.Request(f, ReqPlay)
		waitFor(t, "ready", func() bool { x := s.Ready().Load(); return x != nil && x.Pos == f })
		s.Take().Release()
	}
	if fr.n() <= n {
		t.Fatal("edited frames must re-render")
	}
}

func TestStatsSnapshotRatesAndLines(t *testing.T) {
	st := &Stats{}
	st.SetProjectFPS(30)
	now := time.Unix(0, 0)
	st.Snapshot(now)
	for range 30 {
		st.rendered.Add(1)
		st.Shown(now)
	}
	st.Dropped(2)
	st.CacheLookup(true)
	st.CacheLookup(false)
	sn := st.Snapshot(now.Add(time.Second))
	if sn.ShownFPS < 29.9 || sn.ShownFPS > 30.1 || sn.DroppedPerSec != 2 || sn.CachePct != 50 {
		t.Fatalf("%+v", sn)
	}
	l := sn.Lines()
	if l[0] != "Bild 30.0/30 fps  verworfen 2 (2.0/s)" {
		t.Fatalf("line 1 %q", l[0])
	}
}

func TestStatsHotPathAllocsFree(t *testing.T) {
	st := &Stats{}
	if n := testing.AllocsPerRun(100, func() {
		st.addRender(1000)
		st.rendered.Add(1)
		st.Dropped(1)
		st.CacheLookup(true)
	}); n != 0 {
		t.Fatalf("%v allocs", n)
	}
}

// A press on play must not wait behind a slow scrub or refine render.
func TestSchedulerPlayCancelsInFlightStill(t *testing.T) {
	for _, kind := range []ReqKind{ReqScrub, ReqStill} {
		fr := &fakeRender{delay: func(_, frame int) time.Duration {
			if frame == 10 {
				return 5 * time.Second
			}
			return 0
		}}
		s := newSched(t, fr, Config{})
		s.Request(10, kind)
		waitFor(t, "start", func() bool { return fr.n() == 1 })
		t0 := time.Now()
		s.Request(11, ReqPlay)
		waitFor(t, "play frame", func() bool { f := s.Ready().Load(); return f != nil && f.Pos == 11 })
		if time.Since(t0) > time.Second {
			t.Fatalf("kind %v: first play frame waited %v", kind, time.Since(t0))
		}
	}
}

// Half-rate mode decides on the clock, not on the position parity.
func TestSchedulerHalfRateIndependentOfParity(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{})
	s.mu.Lock()
	s.qc.halfRate, s.qc.under = true, true
	s.mu.Unlock()
	s.Request(11, ReqPlay) // odd position, 2x run
	for i := 0; i < 8; i++ {
		time.Sleep(34 * time.Millisecond)
		s.Request(13+2*i, ReqPlay)
	}
	time.Sleep(50 * time.Millisecond)
	if n := fr.n(); n < 3 || n > 7 {
		t.Fatalf("half rate rendered %d of 9 odd-position requests", n)
	}
}

func TestSchedulerRunResetClearsUnderRun(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{})
	s.Request(1, ReqPlay)
	waitFor(t, "frame", func() bool { return fr.n() == 1 })
	s.mu.Lock()
	s.qc.halfRate, s.qc.under = true, true
	s.mu.Unlock()
	s.Request(1, ReqScrub) // playback stopped
	s.Request(2, ReqPlay)  // and started again
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.qc.HalfRate() || s.qc.UnderRun() {
		t.Fatal("under-run state survived stop and start")
	}
}

// A paused picture shown from a proxy is refined from the original and cached apart.
func TestSchedulerPausedStillNeverProxyOnly(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{RefineDelay: 20 * time.Millisecond})
	s.Request(10, ReqScrub) // isolated
	waitFor(t, "frame", func() bool { return fr.n() >= 1 })
	if c := fr.calls[0]; !c.acc {
		t.Fatal("isolated paused scrub must be an accurate render")
	}
	// a drag-scrub frame through a proxy gets an accurate refine
	fr2 := &fakeRender{proxy: true}
	s2 := newSched(t, fr2, Config{RefineDelay: 20 * time.Millisecond})
	s2.Request(1, ReqScrub)
	s2.Request(2, ReqScrub)
	waitFor(t, "refine", func() bool {
		fr2.mu.Lock()
		defer fr2.mu.Unlock()
		for _, c := range fr2.calls {
			if c.frame == 2 && c.acc {
				return true
			}
		}
		return false
	})
}

// No full-size exact render starts while play requests keep coming, however slowly.
func TestSchedulerNoRefineDuringPlay(t *testing.T) {
	fr := &fakeRender{}
	s := newSched(t, fr, Config{RefineDelay: 20 * time.Millisecond, MaxH: 180})
	for i := 0; i < 5; i++ {
		s.Request(i, ReqPlay)
		time.Sleep(80 * time.Millisecond)
	}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, c := range fr.calls {
		if c.acc {
			t.Fatalf("accurate render during play: %+v", c)
		}
	}
}

func TestSchedulerAudioEditKeepsRenderPictureEditCancels(t *testing.T) {
	for _, pic := range []bool{false, true} {
		fr := &fakeRender{delay: func(_, frame int) time.Duration { return 250 * time.Millisecond }}
		s := newSched(t, fr, Config{})
		s.Request(10, ReqPlay)
		waitFor(t, "start", func() bool { return fr.n() == 1 })
		p2 := s.proj.Clone()
		if pic {
			p2.Tracks[0].Clips[0].Opacity = 0.5
		} else {
			p2.Tracks[0].Clips[0].Volume = 0.5
		}
		s.SetProject(p2, 2)
		time.Sleep(500 * time.Millisecond)
		got := s.Ready().Load() != nil
		if got == pic {
			t.Fatalf("pictureEdit=%v: render survived=%v", pic, got)
		}
	}
}
