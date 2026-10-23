// Package chrome draws window decoration for borderless windows: a title bar with
// macOS-style window buttons (or Windows/Linux-style buttons), drag-to-move, double-click
// to maximize, a window menu, and invisible resize borders with a thin outline.
//
// Files: chrome.go (contract), backend.go (display backend, close registry), controls.go
// (window buttons), titlebar.go, frame.go (resize borders), paint.go (button rasteriser).
package chrome

import "fyne.io/fyne/v2"

// Button styles for the window controls.
const (
	StyleMac = "mac" // red/yellow/green circles on the left, glyphs on hover
	StyleWin = "win" // minimize / maximize / close icons on the right, red close on hover
	// StyleLinux has symbolic icons in round hover discs on the right (Adwaita / Breeze).
	StyleLinux = "linux"
	// StyleAuto is only a setting value: it resolves by operating system (ResolveStyle).
	StyleAuto = "auto"
)

// ResolveStyle maps the Display.WindowButtons setting to a style for the operating system
// goos (runtime.GOOS): "auto" and unknown values follow the system (darwin: mac, windows:
// win, anything else: linux); explicit "mac", "win" and "linux" are honoured everywhere.
func ResolveStyle(setting, goos string) string {
	switch setting {
	case StyleMac, StyleWin, StyleLinux:
		return setting
	}
	switch goos {
	case "darwin":
		return StyleMac
	case "windows":
		return StyleWin
	}
	return StyleLinux
}

// Backend is the window system as the chrome needs it (display implements it; tests fake it).
type Backend interface {
	BeginMove(w fyne.Window)
	BeginResize(w fyne.Window, edge int) // display.Edge values
	Minimize(w fyne.Window)
	ToggleMaximize(w fyne.Window)
	SetFullscreen(w fyne.Window, on bool)
	Close(w fyne.Window) // same path as the system close button (close intercept)
	State(w fyne.Window) State
	Watch(w fyne.Window, fn func(State)) (stop func())
}

// State mirrors display.State.
type State struct{ Focused, Maximized, Fullscreen, Minimized bool }

// Options configure a title bar.
type Options struct {
	Style         string // StyleMac, StyleWin or StyleLinux
	Compact       bool   // thinner bar for secondary windows
	Backend       Backend
	CanMaximize   bool // false hides/disables the zoom button (dialog-like windows)
	CanFullscreen bool
}
