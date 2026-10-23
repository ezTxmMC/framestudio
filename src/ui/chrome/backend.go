package chrome

import (
	"sync"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/display"
)

var (
	closeMu  sync.Mutex
	closeFns = map[fyne.Window]func(){}
)

// OnClose registers fn as the close action of w (the shell routes it to its close
// intercept: ask to save, quit, ...). Fyne's w.Close() does not run SetCloseIntercept
// handlers, so the red button goes through this registry. fn == nil removes the entry.
func OnClose(w fyne.Window, fn func()) {
	if w == nil {
		return
	}
	closeMu.Lock()
	defer closeMu.Unlock()
	if fn == nil {
		delete(closeFns, w)
		return
	}
	closeFns[w] = fn
}

// RequestClose closes w the way the system close button would: the registered OnClose
// function, else w.Close().
func RequestClose(w fyne.Window) {
	if w == nil {
		return
	}
	closeMu.Lock()
	fn := closeFns[w]
	closeMu.Unlock()
	if fn != nil {
		fn()
		return
	}
	w.Close()
}

// DisplayBackend implements Backend with the window system layer of package display.
type DisplayBackend struct{}

var _ Backend = DisplayBackend{}

func (DisplayBackend) BeginMove(w fyne.Window) { display.BeginMove(w) }
func (DisplayBackend) BeginResize(w fyne.Window, edge int) {
	display.BeginResize(w, display.Edge(edge))
}
func (DisplayBackend) Minimize(w fyne.Window)       { display.Minimize(w) }
func (DisplayBackend) ToggleMaximize(w fyne.Window) { display.ToggleMaximize(w) }
func (DisplayBackend) SetFullscreen(w fyne.Window, on bool) {
	_ = display.Fullscreen(w, "", on)
}
func (DisplayBackend) Close(w fyne.Window) { RequestClose(w) }
func (DisplayBackend) State(w fyne.Window) State {
	return State(display.Current(w))
}
func (DisplayBackend) Watch(w fyne.Window, fn func(State)) func() {
	return display.Watch(w, func(s display.State) { fn(State(s)) })
}
