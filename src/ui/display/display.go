// Package display enumerates monitors and places Fyne windows on them (fullscreen on a
// chosen monitor, or windowed at a position). It reaches GLFW through the Fyne window
// internals of the pinned Fyne version and runs every GLFW call on the main thread.
//
// OWNER: workspace agent. Exported signatures in this file are a contract.
package display

import "fyne.io/fyne/v2"

type Monitor struct {
	ID      string // stable connector name, e.g. "DP-2", "HDMI-A-1"
	Name    string // human readable, e.g. "DP-2 · 2560×1440"
	X, Y    int    // position in the virtual desktop (pixels)
	W, H    int    // current video mode size
	Refresh int
	Scale   float32
	Primary bool
}

// Platform reports the window system in use: "wayland", "x11", "windows", "macos" or "".
func Platform() string { return "" }

// CanPosition reports whether windowed placement on a chosen monitor is possible
// (false on Wayland, where only fullscreen-on-monitor works).
func CanPosition() bool { return false }

// Monitors lists connected monitors, primary first. Safe from the UI thread.
func Monitors() []Monitor { return nil }

// Fullscreen puts w fullscreen on monitor id ("" = current monitor) or back to windowed.
func Fullscreen(w fyne.Window, monitorID string, on bool) error { return nil }

// Place moves a windowed w to rect (relative to the monitor's top-left) on monitor id.
func Place(w fyne.Window, monitorID string, x, y, width, height int) error { return nil }

// Geometry reports the monitor and monitor-relative rect of a window, for saving layouts.
func Geometry(w fyne.Window) (monitorID string, x, y, width, height int, ok bool) { return }

// OnChange registers fn for monitor hot-plug events.
func OnChange(fn func()) {}
