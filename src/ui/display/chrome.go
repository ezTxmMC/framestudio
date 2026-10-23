package display

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/go-gl/glfw/v3.4/glfw"
)

// Window chrome primitives for borderless windows with a custom title bar. Every call must
// be safe from the UI thread and degrade to a no-op/error on drivers without GLFW.
//
// OWNER: probe agent (window system layer). Signatures are a contract; chrome and shell
// code against them.
//
// Method per window system (verified on KDE Plasma, see the notes in chrome_drag.go):
//   - X11 and XWayland: window moves and resizes are handed to the window manager with a
//     _NET_WM_MOVERESIZE message (snapping, edge tiling, multi-monitor behaviour of the
//     desktop). Without a usable libX11, or with FS_CHROME_DRAG=emulate, the move is
//     emulated from the pointer position (pure Go, same on Windows and macOS).
//   - Native Wayland: GLFW 3.4 exposes no way to call xdg_toplevel.move/resize and the
//     serial of the button press stays private, so CanCustomChrome is false there. The app
//     runs through XWayland instead (see ChromePlatform).

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

// ErrNoChrome is returned when the window cannot lose its system title bar.
var ErrNoChrome = errors.New("Dieses Fenstersystem unterstützt kein eigenes Fensterbild.")

// CanCustomChrome reports whether a borderless window can be moved and resized by the app
// on the current window system (true on X11/XWayland, Windows, macOS; false on native
// Wayland, where GLFW cannot start an interactive move). When false the shell keeps the
// system title bar. Before GLFW starts (the driver initialises it in Run) the window
// system is predicted from the environment as Fyne would choose it.
func CanCustomChrome() bool {
	if !glfwDriver() {
		return false
	}
	p := Platform()
	if p == "" {
		p = GuessPlatform(runtime.GOOS, os.Getenv)
	}
	return SupportsChrome(p)
}

// SetDecorated shows or hides the system title bar and border of w. The client area keeps
// its size; Fyne's content is laid out again afterwards.
func SetDecorated(w fyne.Window, on bool) error {
	var err error
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			err = ErrNoChrome
			return
		}
		safe(func() {
			v := glfw.False
			if on {
				v = glfw.True
			}
			if (vp.GetAttrib(glfw.Decorated) == glfw.True) == on {
				return
			}
			if !on && !SupportsChrome(Platform()) {
				err = ErrNoChrome
				return
			}
			vp.SetAttrib(glfw.Decorated, v)
		})
		if err == nil && w.Content() != nil {
			w.Content().Refresh()
		}
	})
	return err
}

// Minimize iconifies w.
func Minimize(w fyne.Window) {
	onMain(func() {
		if vp := viewport(w); vp != nil {
			safe(vp.Iconify)
		}
	})
}

// Restore brings a minimized or maximized w back to its normal state.
func Restore(w fyne.Window) {
	onMain(func() {
		if vp := viewport(w); vp != nil {
			safe(vp.Restore)
		}
	})
}

// ToggleMaximize maximizes a normal window and restores a maximized one (a minimized one
// is restored). A fullscreen window leaves fullscreen first.
func ToggleMaximize(w fyne.Window) {
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			return
		}
		safe(func() {
			if w.FullScreen() {
				_ = fullscreenLocked(w, vp, "", false)
				return
			}
			if vp.GetAttrib(glfw.Iconified) == glfw.True || vp.GetAttrib(glfw.Maximized) == glfw.True {
				vp.Restore()
				return
			}
			vp.Maximize()
		})
	})
}

// Current returns the window state. Windows that do not belong to the GLFW driver report
// a focused, normal window (plus Fyne's fullscreen flag).
func Current(w fyne.Window) State {
	st := State{Focused: true}
	if w == nil {
		return st
	}
	onMain(func() {
		st.Fullscreen = w.FullScreen()
		if vp := viewport(w); vp != nil {
			safe(func() { st = stateLocked(w, vp) })
		}
	})
	return st
}

func stateLocked(w fyne.Window, vp *glfw.Window) State {
	return State{
		Focused:    vp.GetAttrib(glfw.Focused) == glfw.True,
		Maximized:  vp.GetAttrib(glfw.Maximized) == glfw.True,
		Fullscreen: w.FullScreen(),
		Minimized:  vp.GetAttrib(glfw.Iconified) == glfw.True,
	}
}

// watcher hooks GLFW's focus, maximize, iconify and size callbacks of one window once
// and fans out to the registered functions. The previous callbacks (Fyne's) are always
// chained. A hook stays installed (and passes everything through) after the last
// watcher is gone: GLFW callbacks cannot be unchained safely when someone else
// registered on top of ours.
type watcher struct {
	w    fyne.Window
	vp   *glfw.Window
	last State
	fns  map[int]func(State)
	next int
}

var (
	watchMu  sync.Mutex
	watchers = map[*glfw.Window]*watcher{}
)

// Watch calls fn (UI thread) whenever focus, maximize, fullscreen or minimize state of w
// changes. The returned func stops watching; it is safe to call more than once. Fyne
// creates the native window when it is first shown: called earlier, Watch attaches as
// soon as the window exists (within a few seconds, else it gives up).
func Watch(w fyne.Window, fn func(State)) (stop func()) {
	if w == nil || fn == nil || !glfwDriver() {
		return func() {}
	}
	var (
		mu      sync.Mutex
		stopped bool
		wt0     *watcher
		id      int
	)
	attach := func() bool {
		attached := false
		onMain(func() {
			vp := viewport(w)
			if vp == nil {
				return
			}
			safe(func() {
				watchMu.Lock()
				wt := watchers[vp]
				fresh := wt == nil
				if fresh {
					wt = &watcher{w: w, vp: vp, fns: map[int]func(State){}, last: stateLocked(w, vp)}
					watchers[vp] = wt
				}
				watchMu.Unlock()
				if fresh {
					wt.install()
				}
				watchMu.Lock()
				wt.next++
				nid := wt.next
				wt.fns[nid] = fn
				watchMu.Unlock()
				mu.Lock()
				wt0, id = wt, nid
				if stopped { // stop ran meanwhile
					watchMu.Lock()
					delete(wt.fns, nid)
					watchMu.Unlock()
				}
				mu.Unlock()
				attached = true
			})
		})
		return attached
	}
	done := make(chan struct{})
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(done)
			mu.Lock()
			stopped = true
			if wt0 != nil {
				watchMu.Lock()
				delete(wt0.fns, id)
				watchMu.Unlock()
			}
			mu.Unlock()
		})
	}
	attachLocked := func() bool {
		mu.Lock()
		s := stopped
		mu.Unlock()
		return s || attach()
	}
	if attachLocked() {
		return stop
	}
	go func() {
		for i := 0; i < 100; i++ {
			select {
			case <-done:
				return
			case <-time.After(50 * time.Millisecond):
			}
			if attachLocked() {
				return
			}
		}
	}()
	return stop
}

func (t *watcher) install() {
	prevFocus := t.vp.SetFocusCallback(nil)
	t.vp.SetFocusCallback(func(v *glfw.Window, f bool) {
		if prevFocus != nil {
			prevFocus(v, f)
		}
		t.check()
	})
	prevSize := t.vp.SetSizeCallback(nil)
	t.vp.SetSizeCallback(func(v *glfw.Window, ww, hh int) {
		if prevSize != nil {
			prevSize(v, ww, hh)
		}
		t.check()
	})
	prevMax := t.vp.SetMaximizeCallback(nil)
	t.vp.SetMaximizeCallback(func(v *glfw.Window, m bool) {
		if prevMax != nil {
			prevMax(v, m)
		}
		t.check()
	})
	prevIcon := t.vp.SetIconifyCallback(nil)
	t.vp.SetIconifyCallback(func(v *glfw.Window, i bool) {
		if prevIcon != nil {
			prevIcon(v, i)
		}
		t.check()
	})
}

// check runs on the main thread inside a GLFW callback.
func (t *watcher) check() {
	var st State
	ok := false
	safe(func() { st, ok = stateLocked(t.w, t.vp), true })
	if !ok {
		return
	}
	watchMu.Lock()
	if st == t.last {
		watchMu.Unlock()
		return
	}
	t.last = st
	fns := make([]func(State), 0, len(t.fns))
	for _, f := range t.fns {
		fns = append(fns, f)
	}
	watchMu.Unlock()
	for _, f := range fns {
		f(st)
	}
}
