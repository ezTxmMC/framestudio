package winchrome

import (
	"sync"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/chrome"
)

// Fake is a window system for tests: it records the calls, answers State from fields and
// lets Can and SetDecorated be chosen. Close and fullscreen go through the handlers the
// windows registered, as System does.
type Fake struct {
	mu sync.Mutex

	CanChrome   bool
	DecorateErr error
	Decorated   map[fyne.Window]bool // last SetDecorated per window
	Calls       []string             // "minimize", "zoom", "fullscreen:on", "close", ...
	States      map[fyne.Window]chrome.State
	watchers    map[fyne.Window][]func(chrome.State)
}

// Cleaner is the part of *testing.T UseFake needs.
type Cleaner interface{ Cleanup(func()) }

// UseFake installs a Fake that supports custom chrome; the real seams return when the
// test ends.
func UseFake(t Cleaner) *Fake {
	f := &Fake{CanChrome: true, Decorated: map[fyne.Window]bool{}, States: map[fyne.Window]chrome.State{},
		watchers: map[fyne.Window][]func(chrome.State){}}
	oldCan, oldSet, oldSys := Can, SetDecorated, Sys
	Can = func() bool { return f.CanChrome }
	SetDecorated = func(w fyne.Window, on bool) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.DecorateErr != nil && !on {
			return f.DecorateErr
		}
		f.Decorated[w] = on
		return nil
	}
	Sys = f
	SetFailure(nil)
	t.Cleanup(func() {
		Can, SetDecorated, Sys = oldCan, oldSet, oldSys
		SetFailure(nil)
	})
	return f
}

func (f *Fake) record(s string) {
	f.mu.Lock()
	f.Calls = append(f.Calls, s)
	f.mu.Unlock()
}

// Called reports whether call was recorded.
func (f *Fake) Called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if c == call {
			return true
		}
	}
	return false
}

// IsDecorated reports the last SetDecorated of w (true when never called: system bar).
func (f *Fake) IsDecorated(w fyne.Window) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	on, ok := f.Decorated[w]
	return !ok || on
}

// SetState changes the state of w and tells the watchers.
func (f *Fake) SetState(w fyne.Window, s chrome.State) {
	f.mu.Lock()
	f.States[w] = s
	fns := append([]func(chrome.State){}, f.watchers[w]...)
	f.mu.Unlock()
	for _, fn := range fns {
		fn(s)
	}
}

func (f *Fake) BeginMove(fyne.Window)        { f.record("move") }
func (f *Fake) BeginResize(fyne.Window, int) { f.record("resize") }
func (f *Fake) Minimize(fyne.Window)         { f.record("minimize") }
func (f *Fake) ToggleMaximize(fyne.Window)   { f.record("zoom") }
func (f *Fake) SetFullscreen(w fyne.Window, on bool) {
	if on {
		f.record("fullscreen:on")
	} else {
		f.record("fullscreen:off")
	}
	System{}.SetFullscreen(w, on)
}

func (f *Fake) Close(w fyne.Window) {
	f.record("close")
	System{}.Close(w)
}

func (f *Fake) State(w fyne.Window) chrome.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.States[w]; ok {
		return s
	}
	return chrome.State{Focused: true}
}

func (f *Fake) Watch(w fyne.Window, fn func(chrome.State)) (stop func()) {
	f.mu.Lock()
	f.watchers[w] = append(f.watchers[w], fn)
	n := len(f.watchers[w])
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if ws := f.watchers[w]; len(ws) >= n {
			ws[n-1] = func(chrome.State) {}
		}
	}
}
