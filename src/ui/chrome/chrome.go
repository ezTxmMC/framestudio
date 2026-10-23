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
)

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
	Style         string // StyleMac or StyleWin
	Compact       bool   // thinner bar for secondary windows
	Backend       Backend
	CanMaximize   bool // false hides/disables the zoom button (dialog-like windows)
	CanFullscreen bool
}
