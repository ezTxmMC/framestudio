// Package chrome draws window decoration for borderless windows: a title bar with
// macOS-style window buttons (or Windows/Linux-style buttons), drag-to-move, double-click
// to maximize, a window menu, and invisible resize borders with a thin outline.
//
// OWNER: chrome agent. Signatures are a contract; the shell and workspace code against them.
package chrome

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

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

// Titlebar is the draggable bar. leading and trailing are placed next to the window
// buttons (e.g. wordmark + menu bar); center is the title area (e.g. project name) which
// also drags the window. The buttons sit left (mac) or right (win) of everything.
type Titlebar struct {
	widget.BaseWidget
}

// NewTitlebar builds the bar for window w.
func NewTitlebar(w fyne.Window, o Options, leading, center, trailing fyne.CanvasObject) *Titlebar {
	t := &Titlebar{}
	t.ExtendBaseWidget(t)
	return t
}

func (t *Titlebar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewLabel("Titelleiste"))
}

// SetStyle switches the button style live.
func (t *Titlebar) SetStyle(style string) {}

// Close releases watchers.
func (t *Titlebar) Close() {}

// Frame wraps content with resize borders (6 px, invisible, resize cursors) and a 1 px
// outline. When the window is maximized or fullscreen the borders are inactive and the
// outline is hidden. Call Close to stop watching.
type Frame struct {
	widget.BaseWidget
}

func NewFrame(w fyne.Window, b Backend, content fyne.CanvasObject) *Frame {
	f := &Frame{}
	f.ExtendBaseWidget(f)
	return f
}

func (f *Frame) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewLabel("Rahmen"))
}

func (f *Frame) Close() {}
