package display

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestNormalizeLogicalKDE125(t *testing.T) {
	// KDE Plasma Wayland at 125 %: three 2560x1440 outputs reported at logical 0/2048/4096.
	ms := []Monitor{
		{ID: "DP-2", X: 2048, W: 2560, H: 1440, Primary: true, Scale: 2},
		{ID: "DP-3", X: 0, W: 2560, H: 1440, Scale: 2},
		{ID: "HDMI-A-1", X: 4096, W: 2560, H: 1440, Scale: 2},
	}
	NormalizeLogical(ms)
	SortMonitors(ms)
	want := []struct {
		id string
		x  int
	}{{"DP-2", 2560}, {"DP-3", 0}, {"HDMI-A-1", 5120}}
	for i, w := range want {
		if ms[i].ID != w.id || ms[i].X != w.x {
			t.Fatalf("monitor %d = %s x=%d, want %s x=%d", i, ms[i].ID, ms[i].X, w.id, w.x)
		}
		// GLFW's integer buffer scale (2) becomes the real factor, 125 %.
		if ms[i].Scale != 1.25 {
			t.Fatalf("monitor %s scale %v, want 1.25", ms[i].ID, ms[i].Scale)
		}
	}
}

func TestNormalizeLogicalUnscaledAndStacked(t *testing.T) {
	ms := []Monitor{{ID: "A", W: 1920, H: 1080}, {ID: "B", X: 1920, W: 1920, H: 1080}}
	NormalizeLogical(ms)
	if ms[1].X != 1920 {
		t.Fatalf("unscaled monitor moved: %+v", ms[1])
	}
	// Stacked at 150 %: second output logically 1440 below a 3840x2160 output.
	st := []Monitor{{ID: "T", W: 3840, H: 2160}, {ID: "U", Y: 1440, W: 3840, H: 2160}}
	NormalizeLogical(st)
	if st[1].Y != 2160 {
		t.Fatalf("stacked monitor y = %d, want 2160", st[1].Y)
	}
	// A single monitor stays where it is.
	one := []Monitor{{ID: "S", X: 0, W: 2560, H: 1440}}
	NormalizeLogical(one)
	if one[0].X != 0 {
		t.Fatal("single monitor moved")
	}
}

func TestSortPrimaryFirstThenLeftToRight(t *testing.T) {
	ms := []Monitor{{ID: "R", X: 5120}, {ID: "L", X: 0}, {ID: "P", X: 2560, Primary: true}}
	SortMonitors(ms)
	if ms[0].ID != "P" || ms[1].ID != "L" || ms[2].ID != "R" {
		t.Fatalf("order = %v", ms)
	}
}

// With the test driver there is no GLFW: everything degrades to Fyne's own calls.
func TestFallbackWithoutGLFW(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	w.Resize(fyne.NewSize(400, 300))
	if viewport(w) != nil {
		t.Fatal("test window must not expose a GLFW viewport")
	}
	if glfwDriver() {
		t.Fatal("test driver detected as GLFW")
	}
	if Platform() != "" || CanPosition() {
		t.Fatalf("platform %q canPosition %v", Platform(), CanPosition())
	}
	if ms := Monitors(); len(ms) != 0 {
		t.Fatalf("monitors = %v", ms)
	}
	if err := Fullscreen(w, "DP-3", true); err != nil || !w.FullScreen() {
		t.Fatalf("fullscreen fallback: err=%v full=%v", err, w.FullScreen())
	}
	if err := Place(w, "DP-3", 10, 10, 640, 360); err != nil || w.FullScreen() {
		t.Fatalf("place fallback: err=%v full=%v", err, w.FullScreen())
	}
	if s := w.Canvas().Size(); s.Width != 640 || s.Height != 360 {
		t.Fatalf("size = %v", s)
	}
	if _, _, _, ww, hh, ok := Geometry(w); !ok || ww != 640 || hh != 360 {
		t.Fatalf("geometry = %d %d %v", ww, hh, ok)
	}
	if err := Maximize(w, ""); err != nil || IsMaximized(w) {
		t.Fatal("maximize fallback")
	}
	SetCursorHidden(w, true) // no-op, must not panic
	OnChange(func() {})
	Forget(w)
	if viewport(nil) != nil {
		t.Fatal("nil window")
	}
}

func TestErrorsAreGerman(t *testing.T) {
	if !errors.Is(errors.Join(ErrNoPositioning), ErrNoPositioning) {
		t.Fatal("wrap")
	}
	for _, e := range []error{ErrNoPositioning, ErrNoMonitor} {
		if len(e.Error()) < 10 {
			t.Fatal("error text too short")
		}
	}
}

func TestOnMainRunsInlineOffDriver(t *testing.T) {
	test.NewTempApp(t)
	ran := false
	done := make(chan struct{})
	go func() { onMain(func() { ran = true }); close(done) }()
	<-done
	if !ran {
		t.Fatal("onMain did not run")
	}
}
