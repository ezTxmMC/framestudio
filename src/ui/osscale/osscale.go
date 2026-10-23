// Package osscale finds the display scale the desktop is set to (125 % -> 1.25) so the
// app can follow it where Fyne's own detection does not (X11 / XWayland).
//
// The chain, for a Linux X11 or XWayland window:
//
//	a) Xft.dpi from the X root RESOURCE_MANAGER (KWin writes int(96*scale)); read with
//	   "xrdb -query" or "xprop -root RESOURCE_MANAGER".
//	b) kscreen-doctor -j on a Wayland session: the largest scale of the enabled outputs
//	   (XWayland uses the maximum). Skipped when kdeglobals [KScreen]
//	   XwaylandClientsScale=false (the system upscales X11 programs, scale 1).
//	c) GDK_SCALE * GDK_DPI_SCALE, then QT_SCALE_FACTOR.
//	d) 1.0.
//
// XSETTINGS, the monitor's physical size (EDID) and kwinrc / kwinoutputconfig.json on disk
// are deliberately never used. Everything is read-only; no window is opened. The parsing
// functions are pure; the OS access sits behind Env so tests need no X server.
package osscale

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Source says where a detected scale came from.
type Source string

const (
	SourceNone    Source = ""
	SourceXft     Source = "Xft.dpi"
	SourceKScreen Source = "kscreen-doctor"
	SourceGDK     Source = "GDK_SCALE"
	SourceQt      Source = "QT_SCALE_FACTOR"
)

// Label is the German name shown in the diagnostics.
func (s Source) Label() string {
	switch s {
	case SourceXft:
		return "Xft.dpi (X-Ressourcen)"
	case SourceKScreen:
		return "kscreen-doctor"
	case SourceGDK:
		return "GDK_SCALE"
	case SourceQt:
		return "QT_SCALE_FACTOR"
	}
	return "nicht erkannt"
}

// Result is the outcome of a detection.
type Result struct {
	Scale    float64 // always valid: 1 when nothing was found
	Source   Source
	DPI      float64 // Xft.dpi when Source is SourceXft
	Detected bool
	// XwaylandUnscaled: the desktop scales X11 programs itself (XwaylandClientsScale=false).
	XwaylandUnscaled bool
}

// Env is everything the detection reads from the machine.
type Env struct {
	Getenv   func(string) string
	Run      func(name string, args ...string) ([]byte, error) // bounded by the implementation
	ReadFile func(path string) ([]byte, error)
}

// Timeout bounds every external command.
const Timeout = 300 * time.Millisecond

// SystemEnv is the real machine.
func SystemEnv() Env {
	return Env{
		Getenv:   os.Getenv,
		ReadFile: os.ReadFile,
		Run: func(name string, args ...string) ([]byte, error) {
			path, err := exec.LookPath(name)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			return exec.CommandContext(ctx, path, args...).Output()
		},
	}
}

// Clamp limits a scale to the sane range; values up to 1.02 count as 1. Non-finite
// values give 1.
func Clamp(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 1.02 {
		return 1
	}
	return min(v, 4)
}

// ParseXftDPI extracts "Xft.dpi:\t120" from X resource text and returns dpi and scale
// (dpi/96). ok is false when missing or not in 48..768.
func ParseXftDPI(text string) (dpi, scale float64, ok bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		rest, found := strings.CutPrefix(line, "Xft.dpi:")
		if !found {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 48 || v > 768 {
			return 0, 0, false
		}
		return v, Clamp(v / 96), true
	}
	return 0, 0, false
}

// ParseKScreenMax returns the largest scale among the enabled outputs of
// "kscreen-doctor -j" output.
func ParseKScreenMax(data []byte) (float64, bool) {
	var doc struct {
		Outputs []struct {
			Enabled bool    `json:"enabled"`
			Scale   float64 `json:"scale"`
		} `json:"outputs"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return 0, false
	}
	best, ok := 0.0, false
	for _, o := range doc.Outputs {
		if !o.Enabled || o.Scale <= 0 || math.IsNaN(o.Scale) || math.IsInf(o.Scale, 0) {
			continue
		}
		if o.Scale > best {
			best, ok = o.Scale, true
		}
	}
	if !ok {
		return 0, false
	}
	return Clamp(best), true
}

// XwaylandClientsScaleOff reports whether kdeglobals text has [KScreen]
// XwaylandClientsScale=false.
func XwaylandClientsScaleOff(kdeglobals string) bool {
	in := false
	for _, line := range strings.Split(kdeglobals, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "[KScreen]"
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && in && strings.TrimSpace(k) == "XwaylandClientsScale" {
			return strings.EqualFold(strings.TrimSpace(v), "false")
		}
	}
	return false
}

func envFloat(getenv func(string) string, key string) (float64, bool) {
	s := strings.TrimSpace(getenv(key))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, false
	}
	return v, true
}

// WaylandSession reports whether the login session is Wayland.
func WaylandSession(getenv func(string) string) bool {
	return strings.EqualFold(getenv("XDG_SESSION_TYPE"), "wayland") || getenv("WAYLAND_DISPLAY") != ""
}

// Detect runs the chain for an X11 / XWayland window on Linux.
func Detect(e Env) Result {
	res := Result{Scale: 1}
	if e.Getenv == nil {
		e.Getenv = func(string) string { return "" }
	}
	wayland := WaylandSession(e.Getenv)

	// a) Xft.dpi
	if e.Run != nil && e.Getenv("DISPLAY") != "" {
		if out, err := e.Run("xrdb", "-query"); err == nil {
			if dpi, sc, ok := ParseXftDPI(string(out)); ok {
				return Result{Scale: sc, Source: SourceXft, DPI: dpi, Detected: true}
			}
		}
		if out, err := e.Run("xprop", "-root", "RESOURCE_MANAGER"); err == nil {
			if dpi, sc, ok := ParseXftDPI(unquoteXprop(string(out))); ok {
				return Result{Scale: sc, Source: SourceXft, DPI: dpi, Detected: true}
			}
		}
	}

	// b) kscreen-doctor on a Wayland session
	if wayland {
		if e.ReadFile != nil {
			dir := e.Getenv("XDG_CONFIG_HOME")
			if dir == "" {
				dir = filepath.Join(e.Getenv("HOME"), ".config")
			}
			if b, err := e.ReadFile(filepath.Join(dir, "kdeglobals")); err == nil && XwaylandClientsScaleOff(string(b)) {
				res.XwaylandUnscaled = true
				return res
			}
		}
		if e.Run != nil {
			if out, err := e.Run("kscreen-doctor", "-j"); err == nil {
				if sc, ok := ParseKScreenMax(out); ok {
					return Result{Scale: sc, Source: SourceKScreen, Detected: true}
				}
			}
		}
	}

	// c) toolkit variables
	if g, ok := envFloat(e.Getenv, "GDK_SCALE"); ok {
		if d, ok := envFloat(e.Getenv, "GDK_DPI_SCALE"); ok {
			g *= d
		}
		return Result{Scale: Clamp(g), Source: SourceGDK, Detected: true}
	}
	if q, ok := envFloat(e.Getenv, "QT_SCALE_FACTOR"); ok {
		return Result{Scale: Clamp(q), Source: SourceQt, Detected: true}
	}
	return res
}

// unquoteXprop turns xprop's  RESOURCE_MANAGER(STRING) = "a\nb\n"  into plain lines.
func unquoteXprop(s string) string {
	_, v, ok := strings.Cut(s, "=")
	if !ok {
		return s
	}
	v = strings.Trim(strings.TrimSpace(v), "\"")
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t").Replace(v)
}

// Format writes a scale the way FYNE_SCALE wants it ("1.25").
func Format(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// FyneRound is the scale Fyne ends up with: it rounds to tenths (1.25 -> 1.3).
func FyneRound(v float64) float64 { return math.Round(v*10) / 10 }
