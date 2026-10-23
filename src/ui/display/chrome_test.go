package display

import "testing"

func TestResizeRectEdges(t *testing.T) {
	s := Rect{100, 100, 400, 300}
	none := Rect{}
	cases := []struct {
		e      Edge
		dx, dy int
		want   Rect
	}{
		{EdgeRight, 50, 99, Rect{100, 100, 450, 300}},
		{EdgeBottom, 99, 40, Rect{100, 100, 400, 340}},
		{EdgeLeft, -30, 0, Rect{70, 100, 430, 300}},
		{EdgeTop, 0, -20, Rect{100, 80, 400, 320}},
		{EdgeTopLeft, 10, 10, Rect{110, 110, 390, 290}},
		{EdgeTopRight, 10, -10, Rect{100, 90, 410, 310}},
		{EdgeBottomLeft, -10, 10, Rect{90, 100, 410, 310}},
		{EdgeBottomRight, 10, 10, Rect{100, 100, 410, 310}},
	}
	for _, c := range cases {
		if got := ResizeRect(s, c.e, c.dx, c.dy, 100, 80, none); got != c.want {
			t.Errorf("edge %d: got %+v want %+v", c.e, got, c.want)
		}
	}
}

func TestResizeRectMinSizeKeepsOppositeEdge(t *testing.T) {
	s := Rect{100, 100, 400, 300}
	got := ResizeRect(s, EdgeLeft, 900, 0, 150, 80, Rect{})
	if got.X != 350 || got.W != 150 {
		t.Errorf("left past min: %+v", got)
	}
	got = ResizeRect(s, EdgeBottomRight, -900, -900, 150, 80, Rect{})
	if got.X != 100 || got.Y != 100 || got.W != 150 || got.H != 80 {
		t.Errorf("br past min: %+v", got)
	}
	got = ResizeRect(s, EdgeTop, 0, 900, 150, 80, Rect{})
	if got.Y != 320 || got.H != 80 {
		t.Errorf("top past min: %+v", got)
	}
}

func TestResizeRectBounds(t *testing.T) {
	b := Rect{0, 0, 1000, 800}
	s := Rect{100, 100, 400, 300}
	got := ResizeRect(s, EdgeTopLeft, -500, -500, 100, 80, b)
	if got.X != 0 || got.Y != 0 || got.W != 500 || got.H != 400 {
		t.Errorf("clamp top-left: %+v", got)
	}
	got = ResizeRect(s, EdgeBottomRight, 5000, 5000, 100, 80, b)
	if got.W != 900 || got.H != 700 {
		t.Errorf("clamp bottom-right: %+v", got)
	}
	// An edge that already lies outside the bounds stays, but goes no further out.
	out := Rect{-50, 100, 400, 300}
	got = ResizeRect(out, EdgeLeft, -10, 0, 100, 80, b)
	if got.X != -50 { // may not go further out
		t.Errorf("outside edge: %+v", got)
	}
}

func TestMoveRect(t *testing.T) {
	mons := []Rect{{0, 0, 2560, 1440}, {2560, 0, 2560, 1440}}
	s := Rect{100, 100, 600, 400}
	if got := MoveRect(s, 50, 30, 0, 0, 80, mons); got != (Rect{150, 130, 600, 400}) {
		t.Errorf("plain: %+v", got)
	}
	if got := MoveRect(s, 0, -500, 300, 20, 80, mons); got.Y != 0 {
		t.Errorf("top clamp: %+v", got)
	}
	if got := MoveRect(s, 99999, 0, 5000, 20, 80, mons); got.X != 5120-80 {
		t.Errorf("right clamp: %+v", got)
	}
	if got := MoveRect(s, -99999, 0, 20, 20, 80, mons); got.X != 80-600 {
		t.Errorf("left clamp: %+v", got)
	}
	if got := MoveRect(s, 0, 99999, 20, 20, 80, mons); got.Y != 1440-80 {
		t.Errorf("bottom clamp: %+v", got)
	}
	if got := MoveRect(s, 5, 5, 0, 0, 80, nil); got.X != 105 {
		t.Errorf("no monitors: %+v", got)
	}
}

func TestMoveRectIsDeltaNotAccumulated(t *testing.T) {
	s := Rect{100, 100, 600, 400}
	a := MoveRect(s, 40, 40, 0, 0, 80, nil)
	b := MoveRect(s, 40, 40, 0, 0, 80, nil)
	if a != b {
		t.Error("same delta must give the same rect")
	}
}

func TestChromePlatform(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	wl := map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}
	cases := []struct {
		name   string
		chrome bool
		plat   string
		env    map[string]string
		want   string
	}{
		{"wayland auto", true, "auto", wl, "x11"},
		{"wayland empty setting", true, "", wl, "x11"},
		{"chrome off", false, "auto", wl, ""},
		{"user forces wayland", true, "wayland", wl, ""},
		{"user forces x11", true, "x11", wl, ""},
		{"FYNE_PLATFORM wins", true, "auto", map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0", "FYNE_PLATFORM": "wayland"}, ""},
		{"no xwayland", true, "auto", map[string]string{"WAYLAND_DISPLAY": "w"}, ""},
		{"plain x11", true, "auto", map[string]string{"DISPLAY": ":0"}, ""},
	}
	for _, c := range cases {
		if got := ChromePlatform(c.chrome, c.plat, env(c.env)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestGuessAndSupport(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if GuessPlatform("linux", env(map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"})) != "wayland" {
		t.Error("wayland session")
	}
	if GuessPlatform("linux", env(map[string]string{"WAYLAND_DISPLAY": "w", "FYNE_PLATFORM": "x11"})) != "x11" {
		t.Error("forced x11")
	}
	if GuessPlatform("darwin", env(nil)) != "macos" || GuessPlatform("windows", env(nil)) != "windows" {
		t.Error("other os")
	}
	for p, want := range map[string]bool{"x11": true, "windows": true, "macos": true, "wayland": false, "": false} {
		if SupportsChrome(p) != want {
			t.Errorf("SupportsChrome(%q)", p)
		}
	}
}

func TestChromeFallbackWithoutGLFW(t *testing.T) {
	if CanCustomChrome() {
		t.Error("no GLFW driver: no custom chrome")
	}
	if SetDecorated(nil, false) == nil {
		t.Error("SetDecorated must report an error without GLFW")
	}
	BeginMove(nil)
	BeginResize(nil, EdgeLeft)
	Minimize(nil)
	Restore(nil)
	ToggleMaximize(nil)
	if !Current(nil).Focused {
		t.Error("default state is focused")
	}
	Watch(nil, func(State) {})()
}
