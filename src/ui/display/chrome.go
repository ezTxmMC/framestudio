package display

import "fyne.io/fyne/v2"

// Window chrome primitives for borderless windows with a custom title bar. Every call must
// be safe from the UI thread and degrade to a no-op/error on drivers without GLFW.
//
// OWNER: probe agent (window system layer). Signatures are a contract; chrome and shell
// code against them.

// Edge names a resize border or corner.
type Edge int

const (
	EdgeNone Edge = iota
	EdgeTop
	EdgeBottom
	EdgeLeft
	EdgeRight
	EdgeTopLeft
	EdgeTopRight
	EdgeBottomLeft
	EdgeBottomRight
)

// State is what a title bar needs to know about its window.
type State struct {
	Focused    bool
	Maximized  bool
	Fullscreen bool
	Minimized  bool
}

// CanCustomChrome reports whether a borderless window can be moved and resized by the app
// on the current window system (true on X11/XWayland, Windows, macOS; native Wayland only
// if a working method exists). When false the shell keeps the system title bar.
func CanCustomChrome() bool { return false }

// SetDecorated shows or hides the system title bar and border of w.
func SetDecorated(w fyne.Window, on bool) error { return nil }

// BeginMove starts moving w with the pointer. Call it on mouse down in the title bar; the
// move follows the pointer until the button is released (native request where available,
// otherwise emulated from the cursor position).
func BeginMove(w fyne.Window) {}

// BeginResize starts resizing w from edge until the button is released.
func BeginResize(w fyne.Window, e Edge) {}

func Minimize(w fyne.Window)       {}
func Restore(w fyne.Window)        {}
func ToggleMaximize(w fyne.Window) {}

// Current returns the window state.
func Current(w fyne.Window) State { return State{Focused: true} }

// Watch calls fn (UI thread) whenever focus, maximize, fullscreen or minimize state of w
// changes. The returned func stops watching.
func Watch(w fyne.Window, fn func(State)) (stop func()) { return func() {} }
