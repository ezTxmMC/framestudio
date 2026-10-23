package viewer

import (
	"image"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
)

func TestRateLabel(t *testing.T) {
	cases := []struct {
		playing bool
		rate    float64
		want    string
	}{
		{false, 2, ""}, {true, 1, ""}, {true, 2, "2×"}, {true, 8, "8×"}, {true, -1, "−1×"}, {true, -4, "−4×"},
	}
	for _, c := range cases {
		if got := RateLabel(c.playing, c.rate); got != c.want {
			t.Errorf("RateLabel(%v, %v) = %q, want %q", c.playing, c.rate, got, c.want)
		}
	}
}

func TestTransportShowsShuttleRate(t *testing.T) {
	test.NewTempApp(t)
	s := newTestSession()
	b := NewTransportBar(s, false)
	defer b.Close()
	s.Seek(50)
	s.Shuttle(1)
	s.Shuttle(1)
	if b.Rate() != "2×" || b.t.play.Tip != tipPause {
		t.Fatalf("rate %q, play tip %q", b.Rate(), b.t.play.Tip)
	}
	s.Shuttle(-1)
	if b.Rate() != "−1×" {
		t.Fatalf("reverse rate %q", b.Rate())
	}
	test.Tap(b.t.play) // the play button stops shuttling
	if s.Playing || b.Rate() != "" || b.t.play.Tip != tipPlay {
		t.Fatal("play button should stop the shuttle")
	}
	b.Close()
	s.Shuttle(1)
	if b.Rate() != "" {
		t.Fatal("closed bar must not follow the session")
	}
}

func TestMeterPollsOnlyWhilePlaying(t *testing.T) {
	test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	s := newTestSession()
	before := runtime.NumGoroutine()
	m := NewMeter(s)
	var polls atomic.Int32
	m.levels = func(*session.Session) liveaudio.Level {
		polls.Add(1)
		return liveaudio.Level{Peak: [2]float32{0.5, 1}, Clipped: true}
	}
	if m.Running() {
		t.Fatal("meter must not poll while stopped")
	}
	s.SetPlaying(true)
	if !m.Running() {
		t.Fatal("meter should poll while playing")
	}
	if !q.Until(func() bool { return polls.Load() >= 3 }, 2*time.Second) {
		t.Fatal("no polls at 30 Hz")
	}
	if h := m.Hold(); h[1] != 1 || h[0] <= 0 || !m.clipped {
		t.Fatalf("hold %v clipped %v", h, m.clipped)
	}
	s.SetPlaying(false)
	if m.Running() {
		t.Fatal("ticker must stop on pause")
	}
	if m.Hold() != [2]float32{} || m.level.Peak != [2]float32{} {
		t.Fatal("meter should drop to silence when stopped")
	}
	n := polls.Load()
	time.Sleep(100 * time.Millisecond)
	q.Drain()
	if polls.Load() != n {
		t.Fatal("polled after pause")
	}
	s.SetPlaying(true)
	m.Close()
	if m.Running() {
		t.Fatal("Close must stop the ticker")
	}
	s.SetPlaying(false)
	s.SetPlaying(true)
	if m.Running() {
		t.Fatal("closed meter restarted")
	}
	q.Drain()
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > before {
		t.Fatalf("goroutines leaked: %d > %d", g, before)
	}
}

func TestMeterPeakHoldFalls(t *testing.T) {
	test.NewTempApp(t)
	s := newTestSession()
	m := NewMeter(s)
	defer m.Close()
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.SetLevel(liveaudio.Level{Peak: [2]float32{1, 1}})
	now = now.Add(500 * time.Millisecond)
	m.SetLevel(liveaudio.Level{})
	if m.Hold()[0] != 1 {
		t.Fatal("peak must hold for a second")
	}
	now = now.Add(time.Second)
	m.SetLevel(liveaudio.Level{})
	if h := m.Hold()[0]; h >= 1 || h <= 0 {
		t.Fatalf("held peak should fall after the hold time, got %v", h)
	}
	now = now.Add(5 * time.Second)
	m.SetLevel(liveaudio.Level{})
	if m.Hold()[0] != 0 {
		t.Fatal("held peak should reach the floor")
	}
}

// slowSource blocks each render until released, to check frames do not pile up.
type slowSource struct {
	release chan struct{}
	started atomic.Int32
}

func (s *slowSource) frame(p *model.Project, f, w, h int, o render.Options) *image.NRGBA {
	s.started.Add(1)
	<-s.release
	return gradient(w, h)
}

func TestViewerDropsStaleFramesWhileShuttling(t *testing.T) {
	app := test.NewTempApp(t)
	_ = app
	s := newTestSession()
	v := New(s, nil)
	t.Cleanup(v.Close)
	src := &slowSource{release: make(chan struct{})}
	q := &queue{}
	v.Source = src.frame
	v.SetDeliver(q.post)
	w := test.NewTempWindow(t, v)
	w.Resize(fyne.NewSize(1600, 1000))
	deadline := time.Now().Add(2 * time.Second)
	for src.started.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.Shuttle(1)
	s.Shuttle(1)
	for f := 1; f <= 20; f++ { // playhead runs while the first frame still renders
		s.Advance(s.Project.FrameDuration() / 2)
	}
	close(src.release)
	q.wait(t)
	time.Sleep(50 * time.Millisecond)
	q.mu.Lock()
	pending := len(q.fns)
	q.mu.Unlock()
	if pending > 1 {
		t.Fatalf("%d frames queued for the UI thread; want at most one", pending)
	}
	if n := src.started.Load(); n > 3 {
		t.Fatalf("rendered %d frames for 20 playhead moves; stale requests must be dropped", n)
	}
	// the newest request was made while shuttling at 2x: it renders small
	small := func() bool {
		img := v.Image()
		return img != nil && img.Bounds().Dx() <= shuttleW && img.Bounds().Dy() <= shuttleH
	}
	for end := time.Now().Add(2 * time.Second); !small() && time.Now().Before(end); {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		for _, f := range fns {
			f()
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !small() {
		t.Fatalf("shuttle frame %v larger than %dx%d", v.Image().Bounds(), shuttleW, shuttleH)
	}
}
