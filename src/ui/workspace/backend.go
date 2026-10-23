package workspace

import (
	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/display"
)

// Display is the window-system backend of the manager. The default forwards to package
// display (GLFW); tests inject a fake so placement logic runs without a real screen.
// All methods are called on the UI thread.
type Display interface {
	Platform() string
	CanPosition() bool
	Monitors() []display.Monitor
	Fullscreen(w fyne.Window, monitorID string, on bool) error
	Maximize(w fyne.Window, monitorID string) error
	Place(w fyne.Window, monitorID string, x, y, width, height int) error
	Geometry(w fyne.Window) (monitorID string, x, y, width, height int, ok bool)
	IsMaximized(w fyne.Window) bool
	SetCursorHidden(w fyne.Window, hidden bool)
	Forget(w fyne.Window)
	OnChange(fn func())
}

// System is the Display backed by package display.
type System struct{}

func (System) Platform() string            { return display.Platform() }
func (System) CanPosition() bool           { return display.CanPosition() }
func (System) Monitors() []display.Monitor { return display.Monitors() }
func (System) Fullscreen(w fyne.Window, id string, on bool) error {
	return display.Fullscreen(w, id, on)
}
func (System) Maximize(w fyne.Window, id string) error { return display.Maximize(w, id) }
func (System) Place(w fyne.Window, id string, x, y, width, height int) error {
	return display.Place(w, id, x, y, width, height)
}
func (System) Geometry(w fyne.Window) (string, int, int, int, int, bool) {
	return display.Geometry(w)
}
func (System) IsMaximized(w fyne.Window) bool             { return display.IsMaximized(w) }
func (System) SetCursorHidden(w fyne.Window, hidden bool) { display.SetCursorHidden(w, hidden) }
func (System) Forget(w fyne.Window)                       { display.Forget(w) }
func (System) OnChange(fn func())                         { display.OnChange(fn) }
