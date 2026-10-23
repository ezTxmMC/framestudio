package display

import (
	"os"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"github.com/go-gl/glfw/v3.4/glfw"
)

// Interactive move and resize of borderless windows.
//
// GLFW 3.4 has no API for this. Two methods exist:
//
//   - native (X11 and XWayland, chrome_x11.go): a _NET_WM_MOVERESIZE client message to the
//     window manager, which then runs the drag itself. The pointer is grabbed by the
//     window manager, so the application sees no release; a poll notices the end and
//     replays the missing button release into Fyne.
//   - emulated (everything else, or FS_CHROME_DRAG=emulate): while the left button is
//     held, every cursor event computes the pointer's screen position (window position +
//     window-relative cursor) and sets the window position or size from the pointer
//     delta since the press. The delta is taken against the position at the press, never
//     accumulated, so there is no drift when the window lags behind a request.
//
// Native Wayland has no method (the xdg_toplevel move/resize request needs the serial of
// the press, which GLFW keeps private); CanCustomChrome is false there.

type session struct {
	w          fyne.Window
	vp         *glfw.Window
	edge       Edge // EdgeNone: move
	start      Rect
	sx, sy     int // pointer screen position at the press
	minW, minH int
	monitors   []Rect
	prevCursor glfw.CursorPosCallback
	prevButton glfw.MouseButtonCallback
}

var active = map[*glfw.Window]*session{}

// nativeMoveResize asks the window manager to run the drag (chrome_x11.go sets it up);
// nil when unavailable. Main thread only.
var nativeMoveResize func(vp *glfw.Window, e Edge) bool

// nativeButtonDown reports whether the primary button is still down according to the
// window system, independent of GLFW's event state. Main thread only.
var nativeButtonDown func(vp *glfw.Window) bool

// BeginMove starts moving w with the pointer. Call it on mouse down in the title bar; the
// move follows the pointer until the button is released (native request where available,
// otherwise emulated from the cursor position). Ignored for fullscreen windows and when
// the primary button is not down.
func BeginMove(w fyne.Window) { begin(w, EdgeNone) }

// BeginResize starts resizing w from edge until the button is released. Ignored for
// fullscreen and maximized windows.
func BeginResize(w fyne.Window, e Edge) {
	if e != EdgeNone {
		begin(w, e)
	}
}

func begin(w fyne.Window, e Edge) {
	if w == nil {
		return
	}
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			return
		}
		safe(func() {
			if active[vp] != nil || w.FullScreen() ||
				vp.GetMouseButton(glfw.MouseButtonLeft) != glfw.Press {
				return
			}
			if e != EdgeNone && vp.GetAttrib(glfw.Maximized) == glfw.True {
				return
			}
			if nativeMoveResize != nil && os.Getenv("FS_CHROME_DRAG") != "emulate" &&
				nativeMoveResize(vp, e) {
				watchNative(w, vp)
				return
			}
			startEmulated(w, vp, e)
		})
	})
}

// watchNative polls until the window manager's drag is over, then replays the missing
// button release into Fyne so no widget stays "pressed" and hover works again.
func watchNative(w fyne.Window, vp *glfw.Window) {
	active[vp] = &session{w: w, vp: vp}
	go func() {
		deadline := time.Now().Add(10 * time.Minute)
		time.Sleep(60 * time.Millisecond) // the window manager takes the grab asynchronously
		for time.Now().Before(deadline) {
			done := false
			onMain(func() {
				v := viewport(w)
				if v != vp {
					done = true
					delete(active, vp)
					return
				}
				safe(func() {
					if nativeButtonDown != nil && nativeButtonDown(vp) {
						return
					}
					done = true
					delete(active, vp)
					replayRelease(vp)
				})
			})
			if done {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		onMain(func() { delete(active, vp) })
	}()
}

// replayRelease delivers a primary button release to the callback registered before us
// (Fyne's), which never saw it because the window manager owned the pointer.
func replayRelease(vp *glfw.Window) {
	cb := vp.SetMouseButtonCallback(nil)
	vp.SetMouseButtonCallback(cb)
	if cb != nil {
		cb(vp, glfw.MouseButtonLeft, glfw.Release, 0)
	}
}

func startEmulated(w fyne.Window, vp *glfw.Window, e Edge) {
	cx, cy := vp.GetCursorPos()
	px, py := vp.GetPos()
	ww, hh := vp.GetSize()
	s := &session{
		w: w, vp: vp, edge: e,
		start: Rect{px, py, ww, hh},
		sx:    px + int(cx), sy: py + int(cy),
	}
	s.minW, s.minH = minWindowSize(w)
	if ms := Monitors(); len(ms) > 0 {
		s.monitors = MonitorRects(ms)
	}
	active[vp] = s
	s.prevCursor = vp.SetCursorPosCallback(s.onCursor)
	s.prevButton = vp.SetMouseButtonCallback(s.onButton)
}

// minWindowSize is the smallest size Fyne allows for w in screen coordinates.
func minWindowSize(w fyne.Window) (int, int) {
	if w.Content() == nil {
		return 1, 1
	}
	m := w.Content().MinSize()
	sc := float32(1)
	if runtime.GOOS != "darwin" && w.Canvas() != nil {
		sc = w.Canvas().Scale()
	}
	return int(m.Width*sc + 0.999), int(m.Height*sc + 0.999)
}

func (s *session) onCursor(vp *glfw.Window, x, y float64) {
	if vp.GetMouseButton(glfw.MouseButtonLeft) != glfw.Press {
		s.end(true) // release was lost (focus change, grab by another client)
		return
	}
	px, py := vp.GetPos()
	sx, sy := px+int(x), py+int(y)
	dx, dy := sx-s.sx, sy-s.sy
	if s.edge == EdgeNone {
		r := MoveRect(s.start, dx, dy, sx, sy, 80, s.monitors)
		if r.X != px || r.Y != py {
			vp.SetPos(r.X, r.Y)
		}
		return
	}
	r := ResizeRect(s.start, s.edge, dx, dy, s.minW, s.minH, Union(s.monitors))
	ww, hh := vp.GetSize()
	if r.X != px || r.Y != py {
		vp.SetPos(r.X, r.Y)
	}
	if r.W != ww || r.H != hh {
		vp.SetSize(r.W, r.H)
	}
}

func (s *session) onButton(vp *glfw.Window, b glfw.MouseButton, a glfw.Action, m glfw.ModifierKey) {
	if b == glfw.MouseButtonLeft && a == glfw.Release {
		s.end(false)
	}
	if s.prevButton != nil {
		s.prevButton(vp, b, a, m)
	}
}

// end removes the hooks. lost: the release was never seen, so replay it.
func (s *session) end(lost bool) {
	if active[s.vp] != s {
		return
	}
	delete(active, s.vp)
	s.vp.SetCursorPosCallback(s.prevCursor)
	s.vp.SetMouseButtonCallback(s.prevButton)
	if lost && s.prevButton != nil {
		s.prevButton(s.vp, glfw.MouseButtonLeft, glfw.Release, 0)
	}
}
