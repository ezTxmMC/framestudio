package application

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/media"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/liveaudio"
	studiotheme "framestudio-proto/src/ui/theme"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestStartEnv(t *testing.T) {
	s := settings.Defaults()
	if got := StartEnv(s, env(nil)); len(got) != 0 {
		t.Fatalf("auto platform and system scale set nothing: %v", got)
	}
	s.Display.Platform = "X11"
	s.Display.Scale = 1.25
	got := StartEnv(s, env(map[string]string{"DISPLAY": ":0"}))
	if got["FYNE_PLATFORM"] != "x11" || got["FYNE_SCALE"] != "1.25" {
		t.Fatalf("got %v", got)
	}
	s.Display.Platform = "wayland"
	if StartEnv(s, env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}))["FYNE_PLATFORM"] != "wayland" {
		t.Fatal("wayland")
	}
	// The user's own environment wins.
	got = StartEnv(s, env(map[string]string{"FYNE_PLATFORM": "x11", "FYNE_SCALE": "2"}))
	if len(got) != 0 {
		t.Fatalf("user variables must not be overridden: %v", got)
	}
	s.Display.Platform = "bogus"
	s.Display.Scale = 9
	got = StartEnv(s, env(nil))
	if _, ok := got["FYNE_PLATFORM"]; ok || got["FYNE_SCALE"] != "4" {
		t.Fatalf("unknown platforms are ignored and the scale is clamped: %v", got)
	}
}

func TestRestartEnvDropsDerived(t *testing.T) {
	environ := []string{"HOME=/home/x", "FYNE_PLATFORM=x11", "FYNE_SCALE=1.5", "PATH=/bin"}
	got := RestartEnv(environ, []string{"FYNE_PLATFORM"})
	want := []string{"HOME=/home/x", "FYNE_SCALE=1.5", "PATH=/bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestApplyEnvRemembers(t *testing.T) {
	t.Setenv("FYNE_SCALE", "")
	saved := applied
	defer func() { applied = saved }()
	applied = nil
	applyEnv(map[string]string{"FYNE_SCALE": "1.5"})
	if !slices.Equal(applied, []string{"FYNE_SCALE"}) {
		t.Fatalf("applied %v", applied)
	}
}

func TestFollowAppliesSettingsLive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Reset(); studiotheme.Configure("steel", false) })
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	stop := Follow()
	defer stop()
	c := media.NewCache()
	defer c.Close()

	_ = settings.Update(func(s *settings.Settings) {
		s.Display.Accent = "violet"
		s.Display.Compact = true
		s.Media.FrameCacheMB = 1024
		s.Media.MaxDecoders = 2
		s.Audio.MasterGain = 0.5
	})
	if got := a.Settings().Theme().Color(theme.ColorNamePrimary, theme.VariantDark); got != studiotheme.AccentColor("violet") {
		t.Fatalf("theme primary %v, want violet", got)
	}
	if studiotheme.Steel != studiotheme.AccentColor("violet") || !studiotheme.Compact() {
		t.Fatal("Steel follows the accent; compact density on")
	}
	if c.FrameCacheBytes() != 1024<<20 || c.MaxDecoders() != 2 {
		t.Fatal("media cache follows the settings")
	}
	if liveaudio.CurrentConfig().MasterGain != 0.5 {
		t.Fatal("live audio follows the settings")
	}
	stop()
	_ = settings.Update(func(s *settings.Settings) { s.Display.Accent = "teal" })
	if studiotheme.Steel == studiotheme.AccentColor("teal") {
		t.Fatal("stop ends following")
	}
}

func TestMediaConfig(t *testing.T) {
	s := settings.Defaults()
	if got := MediaConfig(s); got != (media.Config{FrameCacheMB: 512, StillCacheMB: 256, MaxDecoders: 4}) {
		t.Fatalf("got %+v", got)
	}
}

// A window system without a display is not forced: the start would crash in GLFW.
func TestStartEnvNeedsTheDisplay(t *testing.T) {
	s := settings.Defaults()
	s.Display.Platform = "wayland"
	x11 := env(map[string]string{"DISPLAY": ":0"})
	if got := StartEnv(s, x11); got["FYNE_PLATFORM"] != "" {
		t.Fatalf("Wayland forced without WAYLAND_DISPLAY: %v", got)
	}
	if w := StartWarning(s, x11); !strings.Contains(w, "WAYLAND_DISPLAY fehlt") {
		t.Fatalf("warning %q", w)
	}
	s.Display.Platform = "x11"
	wl := env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	if got := StartEnv(s, wl); got["FYNE_PLATFORM"] != "" || !strings.Contains(StartWarning(s, wl), "DISPLAY fehlt") {
		t.Fatalf("X11 forced without DISPLAY: %v", got)
	}
	both := env(map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	if StartEnv(s, both)["FYNE_PLATFORM"] != "x11" || StartWarning(s, both) != "" {
		t.Fatal("XWayland available but not used")
	}
	// The user's own variable decides; no warning then.
	if StartWarning(s, env(map[string]string{"FYNE_PLATFORM": "wayland"})) != "" {
		t.Fatal("warning although FYNE_PLATFORM decides")
	}
}

func TestEnvOverrides(t *testing.T) {
	got := EnvOverrides(env(map[string]string{"FYNE_SCALE": "1.5"}))
	if got["Skalierung"] != "FYNE_SCALE=1.5" || got["Fenstersystem"] != "" {
		t.Fatalf("overrides %v", got)
	}
}
