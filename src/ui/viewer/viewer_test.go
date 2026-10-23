package viewer

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// queue collects UI callbacks from the render goroutine so the test runs them itself.
type queue struct {
	mu  sync.Mutex
	fns []func()
}

func (q *queue) post(f func()) {
	q.mu.Lock()
	q.fns = append(q.fns, f)
	q.mu.Unlock()
}

// wait runs queued callbacks until at least one arrived or the timeout passed.
func (q *queue) wait(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		for _, f := range fns {
			f()
		}
		if len(fns) > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no frame delivered")
}

// until runs queued callbacks until cond holds (the scheduler may deliver intermediate
// frames, e.g. the layout render or a refine, before the one a test waits for).
func (q *queue) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		for _, f := range fns {
			f()
		}
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

type call struct {
	f, w, h int
	skip    bool
}

// fakeSource draws a gradient and records each request.
type fakeSource struct {
	mu    sync.Mutex
	calls []call
}

func (s *fakeSource) frame(p *model.Project, f, w, h int, o render.Options) *image.NRGBA {
	s.mu.Lock()
	s.calls = append(s.calls, call{f, w, h, o.SkipGrade})
	s.mu.Unlock()
	return gradient(w, h)
}

func (s *fakeSource) last() call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return call{w: 1 << 20, h: 1 << 20}
	}
	return s.calls[len(s.calls)-1]
}

func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := float64(y) / float64(h)
			img.SetNRGBA(x, y, color.NRGBA{uint8(40 + 120*t), uint8(70 + 60*t), uint8(120 - 40*t), 255})
		}
	}
	return img
}

func newTestSession() *session.Session {
	p := model.NewProject("Test")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, 100))
	return session.New(p)
}

func setup(t *testing.T) (*session.Session, *Viewer, *fakeSource, *queue, fyne.Window) {
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	s := newTestSession()
	v := New(s, nil)
	src := &fakeSource{}
	q := &queue{}
	v.Source = src.frame
	v.SetDeliver(q.post)
	t.Cleanup(v.Close)
	w := test.NewTempWindow(t, container.NewBorder(nil, Transport(s), nil, nil, v))
	w.Resize(fyne.NewSize(800, 500))
	return s, v, src, q, w
}

func TestViewerRendersPlayheadFrame(t *testing.T) {
	s, v, src, q, _ := setup(t)
	q.wait(t)
	if v.Image() == nil {
		t.Fatal("no image shown")
	}
	first := src.last()
	if first.w <= 0 || first.h <= 0 || abs(float64(first.w)/float64(first.h)-16.0/9) > 0.02 {
		t.Fatalf("render size %dx%d not 16:9", first.w, first.h)
	}

	var got *image.NRGBA
	v.OnFrame = func(img *image.NRGBA) { got = img }
	s.Seek(42)
	q.wait(t)
	if src.last().f != 42 || got == nil {
		t.Fatalf("expected frame 42 delivered, got %+v", src.last())
	}

	v.SetCompare(true)
	q.wait(t)
	if !src.last().skip {
		t.Fatal("compare must skip the grade")
	}
}

func TestViewerAspectFollowsProject(t *testing.T) {
	s, v, src, q, _ := setup(t)
	q.wait(t)
	_ = s.Edit("Hochkant", func(p *model.Project) error {
		p.Settings.Width, p.Settings.Height = 1080, 1920
		return nil
	})
	q.wait(t)
	c := src.last()
	if c.h <= c.w {
		t.Fatalf("portrait project rendered at %dx%d", c.w, c.h)
	}
	if v.ratio != float32(1080)/1920 {
		t.Fatal("aspect not updated")
	}
}

func TestViewerCapsWhilePlaying(t *testing.T) {
	_, v, src, q, w := setup(t)
	s := v.s
	_ = s.Edit("4K", func(p *model.Project) error {
		p.Settings.Width, p.Settings.Height = 3840, 2160
		return nil
	})
	w.Resize(fyne.NewSize(2400, 1500))
	q.until(t, "full size frame", func() bool { return src.last().w > 1000 })
	if c := src.last(); c.w <= 1000 {
		t.Fatalf("paused frame should use the full widget size, got %dx%d", c.w, c.h)
	}
	set := settings.Get()
	set.Playback.MaxPreviewH = 720
	v.applySettings(set)
	s.SetPlaying(true)
	q.until(t, "capped frame", func() bool { return src.last().h <= 720 })
	if c := src.last(); c.h > 720 {
		t.Fatalf("playing frame not capped by MaxPreviewH: %dx%d", c.w, c.h)
	}
}

func TestViewerCloseStopsRendering(t *testing.T) {
	s, v, src, q, _ := setup(t)
	q.wait(t)
	v.Close()
	src.mu.Lock()
	n := len(src.calls)
	src.mu.Unlock()
	s.Seek(10)
	time.Sleep(20 * time.Millisecond)
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.calls) != n {
		t.Fatal("rendered after Close")
	}
}

func TestTransport(t *testing.T) {
	test.NewTempApp(t)
	s := newTestSession()
	tr := newTransport(s, false)
	s.Seek(30)
	if tr.tc.Text != "00:00:01:05" {
		t.Fatalf("timecode %q", tr.tc.Text)
	}
	if tr.dur.Text != "00:00:04:00" {
		t.Fatalf("duration %q", tr.dur.Text)
	}
	test.Tap(tr.play)
	if !s.Playing || tr.play.Tip != tipPause {
		t.Fatal("play button did not start playback")
	}
	test.Tap(tr.play)
	test.Tap(tr.loop)
	if !s.Loop {
		t.Fatal("loop not toggled")
	}
	s.SetIn(10)
	s.SetOut(20)
	if tr.inOut.Text == "" {
		t.Fatal("in/out not shown")
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestScreenshot(t *testing.T) {
	path := os.Getenv("FS_SCREENSHOT")
	if path == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-viewer.png")
	}
	s, v, _, q, w := setup(t)
	w.SetContent(kit.Panel("Programm", nil, w.Content()))
	w.Resize(fyne.NewSize(1440, 860))
	s.SetIn(25)
	s.SetOut(75)
	v.SetCompare(true)
	q.wait(t)
	f, err := os.Create("/tmp/fs-viewer.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
