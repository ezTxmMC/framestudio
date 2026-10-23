package winchrome

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestStatus(t *testing.T) {
	both := env(map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"})
	cases := []struct {
		name     string
		cc       bool
		setting  string
		platform string
		can      bool
		getenv   func(string) string
		failed   error
		active   bool
		want     string
	}{
		{"off", false, "auto", "x11", true, both, nil, false, "Aus"},
		{"xwayland", true, "auto", "x11", true, both, nil, true, "XWayland"},
		{"x11", true, "auto", "x11", true, env(map[string]string{"DISPLAY": ":0"}), nil, true, "Aktiv (X11)"},
		{"native wayland pending restart", true, "auto", "wayland", false, both, nil, false, "Neustart"},
		{"no DISPLAY", true, "auto", "wayland", false, env(map[string]string{"WAYLAND_DISPLAY": "w"}), nil, false, "DISPLAY fehlt"},
		{"forced wayland", true, "wayland", "wayland", false, both, nil, false, "festgelegt"},
		{"user FYNE_PLATFORM", true, "auto", "wayland", false, env(map[string]string{"FYNE_PLATFORM": "wayland", "DISPLAY": ":0"}), nil, false, "FYNE_PLATFORM"},
		{"failed", true, "auto", "x11", true, both, errors.New("kaputt"), false, "kaputt"},
		{"unknown", true, "auto", "", false, both, nil, false, "kein Fenstersystem"},
	}
	for _, c := range cases {
		active, text := Status(c.cc, c.setting, c.platform, c.can, c.getenv, c.failed)
		if active != c.active || !strings.Contains(text, c.want) {
			t.Errorf("%s: active=%v %q", c.name, active, text)
		}
	}
}

func TestWantsXWayland(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	t.Setenv("DISPLAY", ":0")
	if !WantsXWayland(true, "auto", "") || WantsXWayland(false, "auto", "") || WantsXWayland(true, "wayland", "") || WantsXWayland(true, "auto", "wayland") {
		t.Fatal("policy")
	}
}

func TestWantedAndStyle(t *testing.T) {
	fk := UseFake(t)
	d := settings.Defaults().Display
	if !Wanted(d) || d.WindowButtons != "auto" || Style(d) != chrome.ResolveStyle("auto", runtime.GOOS) {
		t.Fatal("defaults: custom chrome with automatic buttons")
	}
	for goos, want := range map[string]string{"darwin": "mac", "windows": "win", "linux": "linux", "freebsd": "linux"} {
		if got := StyleFor(d, goos); got != want {
			t.Fatalf("%s: %s", goos, got)
		}
	}
	d.WindowButtons = "win"
	if Style(d) != "win" {
		t.Fatal("win")
	}
	d.WindowButtons = "bogus"
	if Style(d) != chrome.ResolveStyle("auto", runtime.GOOS) {
		t.Fatal("unknown styles are automatic")
	}
	fk.CanChrome = false
	if Wanted(settings.Defaults().Display) {
		t.Fatal("unsupported window systems keep the system title bar")
	}
	d.CustomChrome = false
	fk.CanChrome = true
	if Wanted(d) {
		t.Fatal("switched off")
	}
}

func TestHandlersRouteCloseAndFullscreen(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	closed, fs := 0, []bool{}
	OnClose(w, func() { closed++ })
	OnFullscreen(w, func(on bool) { fs = append(fs, on) })
	System{}.Close(w)
	System{}.SetFullscreen(w, true)
	if closed != 1 || len(fs) != 1 || !fs[0] {
		t.Fatalf("closed=%d fs=%v", closed, fs)
	}
	Forget(w)
	System{}.SetFullscreen(w, true)
	if len(fs) != 1 || !w.FullScreen() {
		t.Fatal("after Forget the plain window path is used")
	}
}

func TestUndecorateRemembersFailure(t *testing.T) {
	fk := UseFake(t)
	fk.DecorateErr = errors.New("nein")
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	if Undecorate(w) == nil || Failure() == nil {
		t.Fatal("failure must be kept")
	}
	fk.DecorateErr = nil
	if Undecorate(w) != nil || Failure() != nil {
		t.Fatal("success clears it")
	}
}
