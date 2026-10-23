package application

import (
	"os"
	"testing"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/osscale"
)

// TestMain keeps the tests away from the machine: no xrdb, no kscreen-doctor, and a
// settings directory that is not the user's.
func TestMain(m *testing.M) {
	detectOS = func() osscale.Result { return osscale.Result{Scale: 1} }
	dir, err := os.MkdirTemp("", "fs-app-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func fakeOS(t *testing.T, r osscale.Result) *int {
	t.Helper()
	calls := 0
	old, oldGOOS := detectOS, goos
	detectOS = func() osscale.Result { calls++; return r }
	goos = "linux"
	t.Cleanup(func() { detectOS, goos = old, oldGOOS })
	return &calls
}

var xft120 = osscale.Result{Scale: 1.25, Source: osscale.SourceXft, DPI: 120, Detected: true}

func TestScaleX11FollowsOS(t *testing.T) {
	fakeOS(t, xft120)
	// Custom chrome on a Wayland session: XWayland.
	got := StartEnv(settings.Defaults(), env(map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}))
	if got["FYNE_SCALE"] != "1.25" || got["FYNE_DISABLE_DPI_DETECTION"] != "1" || got["FYNE_PLATFORM"] != "x11" {
		t.Fatalf("env: %v", got)
	}
	// Plain X11 session.
	got = StartEnv(settings.Defaults(), env(map[string]string{"DISPLAY": ":0"}))
	if got["FYNE_SCALE"] != "1.25" || got["FYNE_DISABLE_DPI_DETECTION"] != "1" {
		t.Fatalf("x11: %v", got)
	}
}

func TestScaleOneStillDisablesGuess(t *testing.T) {
	fakeOS(t, osscale.Result{Scale: 1})
	got := StartEnv(settings.Defaults(), env(map[string]string{"DISPLAY": ":0"}))
	if got["FYNE_SCALE"] != "1" || got["FYNE_DISABLE_DPI_DETECTION"] != "1" {
		t.Fatalf("env: %v", got)
	}
}

func TestScalePrecedence(t *testing.T) {
	calls := fakeOS(t, xft120)
	x11 := map[string]string{"DISPLAY": ":0"}

	s := settings.Defaults()
	s.Display.Scale = 1.5
	got := StartEnv(s, env(x11))
	if got["FYNE_SCALE"] != "1.5" || got["FYNE_DISABLE_DPI_DETECTION"] != "1" {
		t.Errorf("setting must beat OS: %v", got)
	}

	// The user's FYNE_SCALE wins; nothing is set, not even the DPI switch.
	user := map[string]string{"DISPLAY": ":0", "FYNE_SCALE": "2"}
	*calls = 0
	got = StartEnv(s, env(user))
	if _, ok := got["FYNE_SCALE"]; ok {
		t.Errorf("user FYNE_SCALE overwritten: %v", got)
	}
	if _, ok := got["FYNE_DISABLE_DPI_DETECTION"]; ok {
		t.Errorf("DPI switch set against user FYNE_SCALE: %v", got)
	}
	if *calls != 0 {
		t.Error("OS detection ran although the user decided")
	}
	if rep := (func() osscale.Report { _, r := planStart(s, env(user)); return r })(); rep.UserEnv != "2" {
		t.Errorf("report: %+v", rep)
	}
}

func TestScaleNativeWaylandInjectsNothingFromOS(t *testing.T) {
	calls := fakeOS(t, xft120)
	s := settings.Defaults()
	s.Display.CustomChrome = false
	s.Display.Platform = "wayland"
	wl := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}
	got := StartEnv(s, env(wl))
	if got["FYNE_PLATFORM"] != "wayland" {
		t.Fatalf("platform: %v", got)
	}
	if _, ok := got["FYNE_SCALE"]; ok {
		t.Errorf("scale injected on Wayland: %v", got)
	}
	if _, ok := got["FYNE_DISABLE_DPI_DETECTION"]; ok {
		t.Errorf("DPI switch on Wayland: %v", got)
	}
	if *calls != 0 {
		t.Error("OS detection ran on native Wayland")
	}
	// The setting is an extra factor there.
	s.Display.Scale = 1.5
	if got := StartEnv(s, env(wl)); got["FYNE_SCALE"] != "1.5" || got["FYNE_DISABLE_DPI_DETECTION"] != "" {
		t.Errorf("extra factor: %v", got)
	}
}

func TestScaleOtherSystemsUnchanged(t *testing.T) {
	for _, os := range []string{"windows", "darwin"} {
		calls := fakeOS(t, xft120)
		goos = os
		got := StartEnv(settings.Defaults(), env(map[string]string{"DISPLAY": ":0"}))
		if got["FYNE_SCALE"] != "" || got["FYNE_DISABLE_DPI_DETECTION"] != "" || *calls != 0 {
			t.Errorf("%s: %v", os, got)
		}
	}
}

func TestScaleReportRows(t *testing.T) {
	fakeOS(t, xft120)
	_, rep := planStart(settings.Defaults(), env(map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "w"}))
	rows := osscale.Text(osscale.Rows(rep, osscale.Live{Platform: "x11", CanvasScale: 1.3, TexScale: 1}))
	for _, want := range []string{"Fenstersystem: X11 (XWayland)", "System-Skalierung: 125 % (Xft.dpi 120)", "Quelle: Xft.dpi (X-Ressourcen)",
		"Einstellung: Automatisch", "Wirksam: 1,3 (Fyne rundet auf 0,1)", "Gewünscht: 1,25", "FYNE_DISABLE_DPI_DETECTION: gesetzt"} {
		if !contains(rows, want) {
			t.Errorf("missing %q in\n%s", want, rows)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
