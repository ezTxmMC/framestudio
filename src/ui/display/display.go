// Package display enumerates monitors and places Fyne windows on them (fullscreen on a
// chosen monitor, or windowed at a position). It reaches GLFW through the Fyne window
// internals of the pinned Fyne version and runs every GLFW call on the main thread.
//
// Threading: Fyne ≥ 2.6 runs its event loop (glfw.PollEvents, window creation, every
// widget callback) on the main goroutine, which Fyne's GLFW driver locks to the OS main
// thread in its init (runtime.LockOSThread + async.SetMainGoroutine, see
// internal/driver/glfw/loop.go). fyne.DoAndWait queues onto that loop (runOnMainWithWait),
// but must not be called from the main goroutine itself (Fyne then logs an error and runs
// the function on a new goroutine). onMain therefore runs inline when already on the main
// goroutine (the UI thread: menus, buttons, key handlers) and uses fyne.DoAndWait from
// any other goroutine. GLFW callbacks (monitor hot-plug) also arrive on the main thread.
//
// When the window is not a GLFW window (test driver, other drivers) or GLFW is not
// initialised, everything degrades: Monitors returns nil, Fullscreen and Place use
// Fyne's SetFullScreen and Resize.
//
// OWNER: workspace agent. Exported signatures in this file are a contract.
package display

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2"
	"github.com/go-gl/glfw/v3.4/glfw"
)

type Monitor struct {
	ID      string // stable connector name, e.g. "DP-2", "HDMI-A-1"
	Name    string // human readable, e.g. "DP-2 · 2560×1440"
	X, Y    int    // position in the virtual desktop (pixels)
	W, H    int    // current video mode size
	Refresh int
	Scale   float32
	Primary bool
}

// ErrNoPositioning is returned (wrapped) by Place when the window system does not let
// applications position windows (Wayland). The size is still applied.
var ErrNoPositioning = errors.New("Unter Wayland bestimmt der Compositor, wo ein Fenster erscheint. " +
	"Für freie Platzierung in den Einstellungen „Fenstersystem: X11“ wählen; im Vollbild lässt sich jeder Monitor gezielt wählen.")

// ErrNoMonitor is returned when the requested monitor is not connected.
var ErrNoMonitor = errors.New("Der Monitor ist nicht angeschlossen.")

// Platform reports the window system in use: "wayland", "x11", "windows", "macos" or "".
func Platform() string {
	p := ""
	onMain(func() {
		if !glfwDriver() {
			return
		}
		safe(func() {
			switch glfw.GetPlatform() {
			case glfw.PlatformWayland:
				p = "wayland"
			case glfw.PlatformX11:
				p = "x11"
			case glfw.PlatformWin32:
				p = "windows"
			case glfw.PlatformCocoa:
				p = "macos"
			}
		})
	})
	return p
}

// CanPosition reports whether windowed placement on a chosen monitor is possible
// (false on Wayland, where only fullscreen-on-monitor works).
func CanPosition() bool {
	switch Platform() {
	case "x11", "windows", "macos":
		return true
	}
	return false
}

// Monitors lists connected monitors, primary first. Safe from the UI thread.
func Monitors() []Monitor {
	var out []Monitor
	onMain(func() {
		if !glfwDriver() {
			return
		}
		safe(func() {
			installCallback()
			out = monitorsLocked()
		})
	})
	return out
}

// monitorsLocked reads the monitors; main thread only.
func monitorsLocked() []Monitor {
	var out []Monitor
	primary := ""
	if p := glfw.GetPrimaryMonitor(); p != nil {
		primary = p.GetName()
	}
	for i, m := range glfw.GetMonitors() {
		mode := m.GetVideoMode()
		if mode == nil { // disconnected while we looked
			continue
		}
		id := m.GetName()
		if id == "" {
			id = fmt.Sprintf("Monitor %d", i+1)
		}
		x, y := m.GetPos()
		sx, _ := m.GetContentScale()
		out = append(out, Monitor{
			ID: id, Name: fmt.Sprintf("%s · %d×%d", id, mode.Width, mode.Height),
			X: x, Y: y, W: mode.Width, H: mode.Height, Refresh: mode.RefreshRate, Scale: sx,
			Primary: id == primary,
		})
	}
	if glfw.GetPlatform() == glfw.PlatformWayland {
		NormalizeLogical(out)
	}
	SortMonitors(out)
	return out
}

// NormalizeLogical converts Wayland monitor positions to pixels. Wayland reports output
// positions in logical (scaled) coordinates but video modes in pixels: with KDE's 125 %
// three 2560-pixel outputs sit at x = 0, 2048, 4096. GLFW's integer content scale (2 for
// 125 %) cannot recover the fractional factor, so it is derived from adjacency: the gap
// to the next output on the right (or below) is that output's logical width (height).
// Monitors without a neighbour use the factor of another monitor of the same mode size,
// else the most common factor, else 1.
func NormalizeLogical(ms []Monitor) {
	ratio := make([]float64, len(ms))
	for i, m := range ms {
		best := 0
		for j, o := range ms {
			if i == j {
				continue
			}
			if o.Y < m.Y+m.H && m.Y < o.Y+o.H || o.Y == m.Y { // same row
				if d := o.X - m.X; d > 0 && d <= m.W && d*4 >= m.W && (best == 0 || d < best) {
					best = d
				}
			}
		}
		if best > 0 {
			ratio[i] = float64(m.W) / float64(best)
			continue
		}
		for j, o := range ms {
			if i != j && o.X < m.X+m.W && m.X < o.X+o.W {
				if d := o.Y - m.Y; d > 0 && d <= m.H && d*4 >= m.H && (best == 0 || d < best) {
					best = d
				}
			}
		}
		if best > 0 {
			ratio[i] = float64(m.H) / float64(best)
		}
	}
	common := 0.0
	counts := map[float64]int{}
	for _, r := range ratio {
		if r > 0 {
			counts[r]++
			if counts[r] > counts[common] {
				common = r
			}
		}
	}
	for i, m := range ms {
		if ratio[i] > 0 {
			continue
		}
		for j, o := range ms {
			if ratio[j] > 0 && o.W == m.W && o.H == m.H {
				ratio[i] = ratio[j]
				break
			}
		}
		if ratio[i] == 0 {
			ratio[i] = common
		}
	}
	// Positions are shared edges: scale them all with one factor per monitor's origin.
	for i := range ms {
		r := ratio[i]
		if r <= 0 {
			r = 1
		}
		ms[i].X = int(float64(ms[i].X)*r + 0.5)
		ms[i].Y = int(float64(ms[i].Y)*r + 0.5)
	}
}

// SortMonitors orders monitors primary first, then left to right, top to bottom.
func SortMonitors(ms []Monitor) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Primary != ms[j].Primary {
			return ms[i].Primary
		}
		if ms[i].X != ms[j].X {
			return ms[i].X < ms[j].X
		}
		return ms[i].Y < ms[j].Y
	})
}

// Fullscreen puts w fullscreen on monitor id ("" = current monitor) or back to windowed.
//
// Fyne's own fullscreen flag stays authoritative: the window first goes through
// w.SetFullScreen (Fyne records the state and switches on the monitor it picks), then,
// if Fyne picked another monitor, glfw's SetMonitor moves it to the requested one. This
// works on X11 and on Wayland (xdg_toplevel.set_fullscreen with the output). Leaving
// fullscreen uses w.SetFullScreen(false), which restores Fyne's remembered windowed
// size and position; on X11 the geometry remembered here is applied on top. Call it
// after w.Show().
func Fullscreen(w fyne.Window, monitorID string, on bool) error {
	if w == nil {
		return nil
	}
	var err error
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			w.SetFullScreen(on)
			return
		}
		safe(func() { err = fullscreenLocked(w, vp, monitorID, on) })
	})
	return err
}

func fullscreenLocked(w fyne.Window, vp *glfw.Window, monitorID string, on bool) error {
	if !on {
		if !w.FullScreen() {
			return nil
		}
		w.SetFullScreen(false)
		mu.Lock()
		g, ok := saved[w]
		delete(saved, w)
		mu.Unlock()
		if ok && glfw.GetPlatform() != glfw.PlatformWayland && g.monitor != "" {
			if mon := findMonitor(g.monitor); mon != nil {
				mx, my := mon.GetPos()
				vp.SetPos(mx+g.x, my+g.y)
				vp.SetSize(g.w, g.h)
			}
		}
		return nil
	}
	var target *glfw.Monitor
	if monitorID != "" {
		target = findMonitor(monitorID)
		if target == nil {
			return fmt.Errorf("%w (%s)", ErrNoMonitor, monitorID)
		}
	}
	if !w.FullScreen() {
		id, x, y, ww, hh, ok := geometryLocked(w, vp)
		if ok {
			mu.Lock()
			saved[w] = geom{id, x, y, ww, hh}
			mu.Unlock()
		}
		if target != nil && glfw.GetPlatform() != glfw.PlatformWayland {
			// Help Fyne pick the right monitor: it fullscreens on the monitor that holds
			// the window centre.
			mx, my := target.GetPos()
			if mode := target.GetVideoMode(); mode != nil {
				vp.SetPos(mx+(mode.Width-ww)/2, my+(mode.Height-hh)/2)
			}
		}
	}
	w.SetFullScreen(true) // on the main thread: Fyne switches synchronously
	if target == nil {
		return nil
	}
	if cur := vp.GetMonitor(); cur == nil || cur.GetName() != target.GetName() {
		mode := target.GetVideoMode()
		if mode == nil {
			return fmt.Errorf("%w (%s)", ErrNoMonitor, monitorID)
		}
		vp.SetMonitor(target, 0, 0, mode.Width, mode.Height, mode.RefreshRate)
	}
	mu.Lock()
	wish[w] = monitorID
	mu.Unlock()
	return nil
}

// Place moves a windowed w to rect (relative to the monitor's top-left) on monitor id.
// On Wayland only the size is applied and an error wrapping ErrNoPositioning explains
// why (unless monitorID is ""). A fullscreen or maximized window becomes windowed first.
func Place(w fyne.Window, monitorID string, x, y, width, height int) error {
	if w == nil {
		return nil
	}
	var err error
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			if w.FullScreen() {
				w.SetFullScreen(false)
			}
			if width > 0 && height > 0 {
				w.Resize(fyne.NewSize(float32(width), float32(height)))
			}
			return
		}
		safe(func() {
			if w.FullScreen() {
				_ = fullscreenLocked(w, vp, "", false)
			}
			if vp.GetAttrib(glfw.Maximized) == glfw.True {
				vp.Restore()
			}
			if glfw.GetPlatform() == glfw.PlatformWayland {
				if width > 0 && height > 0 {
					vp.SetSize(width, height)
				}
				mu.Lock()
				wish[w] = monitorID
				mu.Unlock()
				if monitorID != "" {
					err = ErrNoPositioning
				}
				return
			}
			ox, oy := 0, 0
			if monitorID != "" {
				mon := findMonitor(monitorID)
				if mon == nil {
					err = fmt.Errorf("%w (%s)", ErrNoMonitor, monitorID)
					return
				}
				ox, oy = mon.GetPos()
			}
			if width > 0 && height > 0 {
				vp.SetSize(width, height)
			}
			vp.SetPos(ox+x, oy+y)
			mu.Lock()
			wish[w] = monitorID
			mu.Unlock()
		})
	})
	return err
}

// Maximize maximizes w on monitor id ("" = where it is). On X11 the window is moved onto
// the monitor first; on Wayland the compositor chooses the output.
func Maximize(w fyne.Window, monitorID string) error {
	if w == nil {
		return nil
	}
	var err error
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			if w.FullScreen() {
				w.SetFullScreen(false)
			}
			return
		}
		safe(func() {
			if w.FullScreen() {
				_ = fullscreenLocked(w, vp, "", false)
			}
			if monitorID != "" {
				mon := findMonitor(monitorID)
				if mon == nil {
					err = fmt.Errorf("%w (%s)", ErrNoMonitor, monitorID)
				} else if glfw.GetPlatform() != glfw.PlatformWayland {
					if vp.GetAttrib(glfw.Maximized) == glfw.True {
						vp.Restore()
					}
					mx, my := mon.GetPos()
					vp.SetPos(mx+40, my+40)
				}
				mu.Lock()
				wish[w] = monitorID
				mu.Unlock()
			}
			vp.Maximize()
		})
	})
	return err
}

// IsMaximized reports whether w is maximized (false for non-GLFW windows).
func IsMaximized(w fyne.Window) bool {
	on := false
	onMain(func() {
		if vp := viewport(w); vp != nil {
			safe(func() { on = vp.GetAttrib(glfw.Maximized) == glfw.True })
		}
	})
	return on
}

// SetCursorHidden hides or shows the mouse pointer while it is over w (clean feed).
func SetCursorHidden(w fyne.Window, hidden bool) {
	onMain(func() {
		if vp := viewport(w); vp != nil {
			safe(func() {
				mode := glfw.CursorNormal
				if hidden {
					mode = glfw.CursorHidden
				}
				vp.SetInputMode(glfw.CursorMode, mode)
			})
		}
	})
}

// Geometry reports the monitor and monitor-relative rect of a window, for saving layouts.
func Geometry(w fyne.Window) (monitorID string, x, y, width, height int, ok bool) {
	onMain(func() {
		vp := viewport(w)
		if vp == nil {
			if w != nil && w.Canvas() != nil {
				s := w.Canvas().Size()
				width, height, ok = int(s.Width), int(s.Height), s.Width > 0
			}
			return
		}
		safe(func() { monitorID, x, y, width, height, ok = geometryLocked(w, vp) })
	})
	return
}

func geometryLocked(w fyne.Window, vp *glfw.Window) (string, int, int, int, int, bool) {
	ww, hh := vp.GetSize()
	if m := vp.GetMonitor(); m != nil { // fullscreen
		return m.GetName(), 0, 0, ww, hh, true
	}
	if glfw.GetPlatform() == glfw.PlatformWayland {
		// Wayland hides window positions: report the monitor we last put it on.
		mu.Lock()
		id := wish[w]
		mu.Unlock()
		return id, 0, 0, ww, hh, true
	}
	px, py := vp.GetPos()
	cx, cy := px+ww/2, py+hh/2
	for _, m := range glfw.GetMonitors() {
		mode := m.GetVideoMode()
		if mode == nil {
			continue
		}
		mx, my := m.GetPos()
		if cx >= mx && cx < mx+mode.Width && cy >= my && cy < my+mode.Height {
			return m.GetName(), px - mx, py - my, ww, hh, true
		}
	}
	return "", px, py, ww, hh, true
}

// Forget drops what the package remembers about w (call when the window closes).
func Forget(w fyne.Window) {
	mu.Lock()
	delete(saved, w)
	delete(wish, w)
	mu.Unlock()
}

// OnChange registers fn for monitor hot-plug events. fn runs on the UI (main) thread.
func OnChange(fn func()) {
	mu.Lock()
	listeners = append(listeners, fn)
	mu.Unlock()
	onMain(func() {
		if glfwDriver() {
			safe(installCallback)
		}
	})
}

type geom struct {
	monitor    string
	x, y, w, h int
}

var (
	mu        sync.Mutex
	saved     = map[fyne.Window]geom{}   // windowed geometry before fullscreen
	wish      = map[fyne.Window]string{} // monitor a window was last put on
	listeners []func()
	installed bool
)

// installCallback hooks glfw's monitor callback once, chaining any previous one.
// Main thread only; GLFW must be initialised (it is once Fyne created a window).
func installCallback() {
	if installed {
		return
	}
	installed = true
	var prev glfw.MonitorCallback
	prev = glfw.SetMonitorCallback(func(m *glfw.Monitor, e glfw.PeripheralEvent) {
		if prev != nil {
			prev(m, e)
		}
		mu.Lock()
		fns := append([]func(){}, listeners...)
		mu.Unlock()
		for _, fn := range fns {
			fn()
		}
	})
}

func findMonitor(id string) *glfw.Monitor {
	for _, m := range glfw.GetMonitors() {
		if m.GetName() == id {
			return m
		}
	}
	return nil
}

// safe runs fn and swallows panics from the GLFW binding (it panics on GLFW errors, e.g.
// when GLFW is not initialised yet).
func safe(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

const fynePkg = "fyne.io/fyne/v2/internal/driver/glfw"

// glfwDriver reports whether the running app uses Fyne's GLFW driver.
func glfwDriver() bool {
	a := fyne.CurrentApp()
	if a == nil {
		return false
	}
	t := reflect.TypeOf(a.Driver())
	return t != nil && t.Kind() == reflect.Pointer && t.Elem().PkgPath() == fynePkg && t.Elem().Name() == "gLDriver"
}

var glfwWindowType = reflect.TypeOf((*glfw.Window)(nil))

// viewport returns the *glfw.Window behind a Fyne window, or nil when w is not a live
// window of Fyne's GLFW driver. The concrete type is *glfw.window (package
// fyne.io/fyne/v2/internal/driver/glfw) whose first field is "viewport *glfw.Window";
// both are verified with reflect before the field is read. Main thread only.
func viewport(w fyne.Window) *glfw.Window {
	if w == nil {
		return nil
	}
	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	t := v.Type().Elem()
	if t.Kind() != reflect.Struct || t.PkgPath() != fynePkg || t.Name() != "window" || t.NumField() == 0 {
		return nil
	}
	f := t.Field(0)
	if f.Name != "viewport" || f.Type != glfwWindowType || f.Offset != 0 {
		return nil
	}
	base := unsafe.Pointer(v.Pointer())
	if c, ok := t.FieldByName("closing"); ok && c.Type.Kind() == reflect.Bool {
		if *(*bool)(unsafe.Add(base, c.Offset)) {
			return nil // closed: Fyne destroys the viewport
		}
	}
	return *(**glfw.Window)(base)
}

// mainGID is the id of the main goroutine: package initialisation runs on it.
var mainGID = goid()

// onMain runs fn on the main thread and waits for it (see the package comment).
func onMain(fn func()) {
	if goid() == mainGID || !glfwDriver() {
		fn()
		return
	}
	fyne.DoAndWait(fn)
}
