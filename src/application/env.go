package application

import (
	"math"
	"os"
	"runtime"
	"slices"
	"strings"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/osscale"
)

// Environment variables Fyne reads at start-up.
const (
	envPlatform = "FYNE_PLATFORM"
	envScale    = "FYNE_SCALE"
	envNoDPI    = "FYNE_DISABLE_DPI_DETECTION"
)

// goos and detectOS are replaced by tests: the real ones read the machine.
var (
	goos     = runtime.GOOS
	detectOS = func() osscale.Result { return osscale.Detect(osscale.SystemEnv()) }
)

// StartEnv returns the environment variables to set before the Fyne app is created so
// that Display.Platform ("x11" / "wayland"; "auto" leaves the choice to Fyne) and
// Display.Scale (> 0) take effect. Variables the user already set (getenv returns
// non-empty) win and are not touched.
//
// With Display.CustomChrome on and the window system left on "auto", a Wayland session
// with XWayland available runs through XWayland (display.ChromePlatform).
//
// A window system is only forced when its display exists ("wayland" needs
// WAYLAND_DISPLAY, "x11" needs DISPLAY): forcing a missing one would make GLFW fail and
// every start crash. StartWarning explains the fallback.
func StartEnv(s settings.Settings, getenv func(string) string) map[string]string {
	env, _ := planStart(s, getenv)
	return env
}

// planStart is StartEnv plus the scale decision for the diagnostics.
func planStart(s settings.Settings, getenv func(string) string) (map[string]string, osscale.Report) {
	env := map[string]string{}
	if getenv(envPlatform) == "" {
		if p := wantedPlatform(s); p != "" && platformAvailable(p, getenv) {
			env[envPlatform] = p
		} else if display.ChromePlatform(s.Display.CustomChrome, s.Display.Platform, getenv) == "x11" {
			// The custom title bar needs a window system where the app can move and
			// resize its own windows: on a Wayland session that is XWayland.
			env[envPlatform] = "x11"
		}
	}
	rep := planScale(s, getenv, env)
	return env, rep
}

// effectiveWindowSystem is the window system GLFW will pick on Linux: "x11" (incl.
// XWayland), "wayland" or "" when neither display exists.
func effectiveWindowSystem(getenv func(string) string, env map[string]string) string {
	p := getenv(envPlatform)
	if p == "" {
		p = env[envPlatform]
	}
	switch strings.ToLower(p) {
	case "x11":
		return "x11"
	case "wayland":
		return "wayland"
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if getenv("DISPLAY") != "" {
		return "x11"
	}
	return ""
}

// planScale adds FYNE_SCALE (and FYNE_DISABLE_DPI_DETECTION) to env.
//
// Precedence: the user's FYNE_SCALE (nothing is touched) > Display.Scale > the scale of
// the desktop > 1. On the X11 / XWayland path Fyne cannot see the desktop scale (it
// guesses from the monitor's physical size), so the detected scale is passed as
// FYNE_SCALE and Fyne's own guess is switched off, always together. On native Wayland,
// Windows and macOS Fyne already follows the system; Display.Scale is an extra factor.
func planScale(s settings.Settings, getenv func(string) string, env map[string]string) osscale.Report {
	s.Display.Scale = clampSetting(s.Display.Scale)
	userEnv := getenv(envScale)
	rep := osscale.Report{GOOS: goos, Setting: s.Display.Scale, UserEnv: userEnv,
		WaylandSession: osscale.WaylandSession(getenv), OS: osscale.Result{Scale: 1}}
	x11 := goos == "linux" && effectiveWindowSystem(getenv, env) == "x11"
	rep.X11Path = x11
	if x11 && strings.EqualFold(strings.TrimSpace(userEnv), "auto") {
		// "auto" is Fyne's own monitor-size guess, which is wrong on X11/XWayland:
		// treat it like unset so the desktop scale is used
		rep.UserEnv = ""
		userEnv = ""
	}
	if userEnv != "" {
		return rep
	}
	if x11 {
		rep.OS = detectOS()
		final := rep.OS.Scale
		if s.Display.Scale > 0 {
			final = min(s.Display.Scale, 4)
		}
		env[envScale] = osscale.Format(final)
		env[envNoDPI] = "1"
		rep.Wanted = final
		rep.DisableDPI = true
		return rep
	}
	if s.Display.Scale > 0 {
		rep.Wanted = min(s.Display.Scale, 4)
		env[envScale] = osscale.Format(rep.Wanted)
	}
	return rep
}

// clampSetting makes a hand-edited Display.Scale usable: not a number or <= 0 is
// automatic (0), below 0.5 is raised to 0.5, above 4 lowered to 4.
func clampSetting(v float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	return min(max(v, 0.5), 4)
}

// wantedPlatform is the window system the settings force ("" = automatic).
func wantedPlatform(s settings.Settings) string {
	switch p := strings.ToLower(strings.TrimSpace(s.Display.Platform)); p {
	case "x11", "wayland":
		return p
	}
	return ""
}

// platformAvailable reports whether the display of window system p is reachable.
func platformAvailable(p string, getenv func(string) string) bool {
	switch p {
	case "wayland":
		return getenv("WAYLAND_DISPLAY") != ""
	case "x11":
		return getenv("DISPLAY") != ""
	}
	return true
}

// StartWarning explains why the window system chosen in the settings is not used ("" when
// it is, or when the choice is automatic or the user's FYNE_PLATFORM decides).
func StartWarning(s settings.Settings, getenv func(string) string) string {
	p := wantedPlatform(s)
	if p == "" || getenv(envPlatform) != "" || platformAvailable(p, getenv) {
		return ""
	}
	name, missing := "Wayland", "WAYLAND_DISPLAY"
	if p == "x11" {
		name, missing = "X11 (XWayland)", "DISPLAY"
	}
	return "Das Fenstersystem „" + name + "“ ist in dieser Sitzung nicht verfügbar (" + missing +
		" fehlt). Frame Studio wählt es automatisch. Ändern unter Einstellungen › Monitore & Fenster › Fenstersystem."
}

// EnvOverrides lists the start-up settings that the user's own environment decides:
// setting label → "FYNE_SCALE=1,5". The settings window shows them instead of promising
// that a change applies after a restart.
func EnvOverrides(getenv func(string) string) map[string]string {
	out := map[string]string{}
	if v := getenv(envPlatform); v != "" {
		out["Fenstersystem"] = envPlatform + "=" + v
	}
	if v := getenv(envScale); v != "" {
		out["Skalierung"] = envScale + "=" + v
	}
	return out
}

// applied lists the variables Run set from the settings, so a restart can drop them and
// let the new settings decide.
var applied []string

// applyEnv sets env in the process environment and remembers the names.
func applyEnv(env map[string]string) {
	for k, v := range env {
		if err := os.Setenv(k, v); err == nil {
			applied = append(applied, k)
		}
	}
	slices.Sort(applied)
}

// RestartEnv is environ without the variables Run derived from the settings.
func RestartEnv(environ []string, derived []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(derived, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
