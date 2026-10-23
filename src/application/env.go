package application

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"framestudio-proto/src/settings"
)

// Environment variables Fyne reads at start-up.
const (
	envPlatform = "FYNE_PLATFORM"
	envScale    = "FYNE_SCALE"
)

// StartEnv returns the environment variables to set before the Fyne app is created so
// that Display.Platform ("x11" / "wayland"; "auto" leaves the choice to Fyne) and
// Display.Scale (> 0) take effect. Variables the user already set (getenv returns
// non-empty) win and are not touched.
//
// A window system is only forced when its display exists ("wayland" needs
// WAYLAND_DISPLAY, "x11" needs DISPLAY): forcing a missing one would make GLFW fail and
// every start crash. StartWarning explains the fallback.
func StartEnv(s settings.Settings, getenv func(string) string) map[string]string {
	env := map[string]string{}
	if getenv(envPlatform) == "" {
		if p := wantedPlatform(s); p != "" && platformAvailable(p, getenv) {
			env[envPlatform] = p
		}
	}
	if getenv(envScale) == "" && s.Display.Scale > 0 {
		env[envScale] = strconv.FormatFloat(min(s.Display.Scale, 4), 'f', -1, 64)
	}
	return env
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
