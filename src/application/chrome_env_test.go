package application

import (
	"testing"

	"framestudio-proto/src/settings"
)

func TestStartEnvCustomChromeRunsThroughXWayland(t *testing.T) {
	wl := map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"}
	s := settings.Defaults()
	if got := StartEnv(s, env(wl)); got["FYNE_PLATFORM"] != "x11" {
		t.Fatalf("custom chrome on Wayland with XWayland must force x11: %v", got)
	}
	s.Display.Platform = "auto"
	if got := StartEnv(s, env(wl)); got["FYNE_PLATFORM"] != "x11" {
		t.Fatalf("explicit auto: %v", got)
	}
	// Switched off: native Wayland stays.
	off := s
	off.Display.CustomChrome = false
	if got := StartEnv(off, env(wl)); got["FYNE_PLATFORM"] != "" {
		t.Fatalf("custom chrome off: %v", got)
	}
	// User-forced platform and existing FYNE_PLATFORM are respected.
	forced := s
	forced.Display.Platform = "wayland"
	if got := StartEnv(forced, env(wl)); got["FYNE_PLATFORM"] != "wayland" {
		t.Fatalf("forced wayland: %v", got)
	}
	user := map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0", "FYNE_PLATFORM": "wayland"}
	if got := StartEnv(s, env(user)); len(got) != 0 {
		t.Fatalf("FYNE_PLATFORM of the user: %v", got)
	}
	// Without DISPLAY there is no XWayland: stay on native Wayland.
	if got := StartEnv(s, env(map[string]string{"WAYLAND_DISPLAY": "wayland-1"})); got["FYNE_PLATFORM"] != "" {
		t.Fatalf("no DISPLAY: %v", got)
	}
	// A plain X11 session needs nothing.
	if got := StartEnv(s, env(map[string]string{"DISPLAY": ":0"})); got["FYNE_PLATFORM"] != "" {
		t.Fatalf("x11 session: %v", got)
	}
}
