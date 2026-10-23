package osscale

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Report records what the start-up decided about the scale; the settings window and the
// diagnostics show it.
type Report struct {
	GOOS           string // runtime.GOOS
	WaylandSession bool
	// X11Path: the window runs through X11 / XWayland (the path with the OS detection).
	X11Path bool
	OS      Result
	// Setting is Display.Scale (0 = automatic).
	Setting float64
	// UserEnv is the user's own FYNE_SCALE ("" = none).
	UserEnv string
	// Wanted is the final scale asked for ("FYNE_SCALE" injected); 0 when none.
	Wanted float64
	// DisableDPI: FYNE_DISABLE_DPI_DETECTION was injected.
	DisableDPI bool
}

var (
	repMu  sync.Mutex
	report Report
)

// SetReport stores the start-up decision.
func SetReport(r Report) {
	repMu.Lock()
	report = r
	repMu.Unlock()
}

// Current returns the stored start-up decision.
func Current() Report {
	repMu.Lock()
	defer repMu.Unlock()
	return report
}

// Percent formats a scale as "125 %".
func Percent(v float64) string {
	return strconv.FormatFloat(v*100, 'f', -1, 64) + " %"
}

// Comma formats a number with a decimal comma ("1,25").
func Comma(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// WindowSystemLabel names the window system for platform ("x11", "wayland", "windows",
// "macos"; "" = unknown).
func WindowSystemLabel(platform string, waylandSession bool) string {
	switch platform {
	case "x11":
		if waylandSession {
			return "X11 (XWayland)"
		}
		return "X11"
	case "wayland":
		return "Wayland"
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	}
	return "unbekannt"
}

// Live is what only the running app knows.
type Live struct {
	Platform     string  // display.Platform()
	CanvasScale  float64 // canvas.Scale()
	TexScale     float64 // texture scale of the canvas (1 when unknown)
	ContentScale float64 // largest monitor content scale from GLFW (Wayland), 0 = unknown
}

// Rows builds the diagnostics block (label, value), in the order of the design spec.
func Rows(r Report, l Live) [][2]string {
	var rows [][2]string
	add := func(k, v string) { rows = append(rows, [2]string{k, v}) }

	wl := r.WaylandSession
	add("Fenstersystem", WindowSystemLabel(l.Platform, wl))

	native := l.Platform == "wayland" || l.Platform == "windows" || l.Platform == "macos"
	if r.X11Path {
		v := Percent(r.OS.Scale)
		if r.OS.Source == SourceXft {
			v += fmt.Sprintf(" (Xft.dpi %s)", strconv.FormatFloat(r.OS.DPI, 'f', -1, 64))
		}
		add("System-Skalierung", v)
		add("Quelle", r.OS.Source.Label())
	} else if l.Platform == "wayland" && l.ContentScale > 0 {
		add("System-Skalierung", Percent(l.ContentScale))
		add("Quelle", "Wayland-Inhaltsskalierung")
	}

	switch {
	case r.UserEnv != "":
		add("Umgebung", "FYNE_SCALE="+r.UserEnv+" (hat Vorrang)")
	case r.Setting > 0:
		add("Einstellung", Percent(r.Setting))
	default:
		add("Einstellung", "Automatisch")
	}

	if l.CanvasScale > 0 {
		eff := fmt.Sprintf("%s (Fyne rundet auf 0,1)", Comma(roundTo(l.CanvasScale, 4)))
		add("Wirksam", eff)
		if r.Wanted > 0 && math.Abs(r.Wanted-l.CanvasScale) > 0.005 {
			add("Gewünscht", Comma(r.Wanted))
		}
		px := fmt.Sprintf("Pixel pro Einheit: %s; Texturskalierung: %s", Comma(roundTo(l.CanvasScale, 4)), Comma(roundTo(max(l.TexScale, 1), 4)))
		add("Pixel", px)
	}
	if l.Platform == "wayland" && l.ContentScale > 0 {
		add("Wayland-Inhaltsskalierung", Comma(l.ContentScale))
	}
	if r.DisableDPI {
		add("FYNE_DISABLE_DPI_DETECTION", "gesetzt")
	}

	switch {
	case r.OS.XwaylandUnscaled && r.X11Path:
		add("Hinweis", "System skaliert X11-Programme, Frame Studio skaliert nicht zusätzlich.")
	case native && r.Setting > 0 && r.UserEnv == "":
		add("Hinweis", "Zusätzlicher Faktor zur Systemskalierung.")
	case r.X11Path && !r.OS.Detected && r.Setting == 0 && r.UserEnv == "":
		add("Hinweis", "Systemskalierung nicht erkannt, 100 % verwendet. Skalierung in den Einstellungen setzen.")
	}
	return rows
}

// Text renders rows as plain text for the clipboard.
func Text(rows [][2]string) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r[0] + ": " + r[1] + "\n")
	}
	return b.String()
}

func roundTo(v float64, digits int) float64 {
	p := 1.0
	for range digits {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}

// SettingHelp is the help text of the Display.Scale row, by platform.
func SettingHelp(x11Path bool) string {
	if x11Path {
		return "Skalierung: Automatisch oder fest. „Automatisch (wie das System)“ übernimmt die Skalierung des Desktops. Änderungen gelten nach einem Neustart (Neustart nötig)."
	}
	return "Zusätzlicher Faktor zur Systemskalierung. 100 % ändert nichts. Änderungen gelten nach einem Neustart (Neustart nötig)."
}
