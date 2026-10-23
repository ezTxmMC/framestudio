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
func StartEnv(s settings.Settings, getenv func(string) string) map[string]string {
	env := map[string]string{}
	if getenv(envPlatform) == "" {
		switch p := strings.ToLower(strings.TrimSpace(s.Display.Platform)); p {
		case "x11", "wayland":
			env[envPlatform] = p
		}
	}
	if getenv(envScale) == "" && s.Display.Scale > 0 {
		env[envScale] = strconv.FormatFloat(min(s.Display.Scale, 4), 'f', -1, 64)
	}
	return env
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
