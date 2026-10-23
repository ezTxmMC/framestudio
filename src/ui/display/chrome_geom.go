package display

import (
	"os"
	"runtime"
	"strings"
)

// Pure geometry and policy helpers of the window chrome layer (no GLFW, fully testable).

// Rect is a window rectangle in screen pixels.
type Rect struct{ X, Y, W, H int }

func (r Rect) right() int  { return r.X + r.W }
func (r Rect) bottom() int { return r.Y + r.H }

// Contains reports whether the point lies inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.right() && y >= r.Y && y < r.bottom()
}

// Union returns the bounding box of all rectangles (zero Rect for none).
func Union(rs []Rect) Rect {
	if len(rs) == 0 {
		return Rect{}
	}
	minX, minY, maxX, maxY := rs[0].X, rs[0].Y, rs[0].right(), rs[0].bottom()
	for _, r := range rs[1:] {
		minX, minY = min(minX, r.X), min(minY, r.Y)
		maxX, maxY = max(maxX, r.right()), max(maxY, r.bottom())
	}
	return Rect{minX, minY, maxX - minX, maxY - minY}
}

// MonitorRects returns the pixel rectangles of the given monitors.
func MonitorRects(ms []Monitor) []Rect {
	out := make([]Rect, 0, len(ms))
	for _, m := range ms {
		out = append(out, Rect{m.X, m.Y, m.W, m.H})
	}
	return out
}

// ResizeRect computes the window rectangle while edge e is dragged by (dx, dy) screen
// pixels from the rectangle start. The size never drops below minW x minH (the opposite
// edge stays put then, not the dragged one: a window never slides away when it hits its
// minimum). bounds (zero = unlimited) limits how far the dragged edges may go; the
// rectangle stays inside it.
func ResizeRect(start Rect, e Edge, dx, dy, minW, minH int, bounds Rect) Rect {
	left, top, right, bottom := start.X, start.Y, start.right(), start.bottom()
	switch e {
	case EdgeLeft, EdgeTopLeft, EdgeBottomLeft:
		left += dx
	case EdgeRight, EdgeTopRight, EdgeBottomRight:
		right += dx
	}
	switch e {
	case EdgeTop, EdgeTopLeft, EdgeTopRight:
		top += dy
	case EdgeBottom, EdgeBottomLeft, EdgeBottomRight:
		bottom += dy
	}
	if bounds.W > 0 && bounds.H > 0 {
		// Only edges that move are clamped; an edge that already lies outside stays.
		if moves(e, EdgeLeft, EdgeTopLeft, EdgeBottomLeft) {
			left = max(left, min(bounds.X, start.X))
		}
		if moves(e, EdgeRight, EdgeTopRight, EdgeBottomRight) {
			right = min(right, max(bounds.right(), start.right()))
		}
		if moves(e, EdgeTop, EdgeTopLeft, EdgeTopRight) {
			top = max(top, min(bounds.Y, start.Y))
		}
		if moves(e, EdgeBottom, EdgeBottomLeft, EdgeBottomRight) {
			bottom = min(bottom, max(bounds.bottom(), start.bottom()))
		}
	}
	minW, minH = max(minW, 1), max(minH, 1)
	if right-left < minW {
		if moves(e, EdgeLeft, EdgeTopLeft, EdgeBottomLeft) {
			left = right - minW
		} else {
			right = left + minW
		}
	}
	if bottom-top < minH {
		if moves(e, EdgeTop, EdgeTopLeft, EdgeTopRight) {
			top = bottom - minH
		} else {
			bottom = top + minH
		}
	}
	return Rect{left, top, right - left, bottom - top}
}

func moves(e Edge, set ...Edge) bool {
	for _, s := range set {
		if e == s {
			return true
		}
	}
	return false
}

// MoveRect computes the window rectangle while it is moved by (dx, dy). The title bar
// stays reachable: the top never leaves the monitor that contains the pointer, and at
// least keep pixels of the window stay visible horizontally and vertically on the
// desktop. monitors may be empty (no clamping).
func MoveRect(start Rect, dx, dy, px, py, keep int, monitors []Rect) Rect {
	r := Rect{start.X + dx, start.Y + dy, start.W, start.H}
	if len(monitors) == 0 {
		return r
	}
	cur := monitors[0]
	best := -1
	for _, m := range monitors {
		d := distSq(m, px, py)
		if best < 0 || d < best {
			best, cur = d, m
		}
	}
	all := Union(monitors)
	keep = max(1, min(keep, start.W))
	r.Y = max(r.Y, cur.Y)
	r.Y = min(r.Y, all.bottom()-keep)
	r.X = max(r.X, all.X+keep-r.W)
	r.X = min(r.X, all.right()-keep)
	return r
}

func distSq(m Rect, px, py int) int {
	cx := max(m.X, min(px, m.right()-1))
	cy := max(m.Y, min(py, m.bottom()-1))
	return (px-cx)*(px-cx) + (py-cy)*(py-cy)
}

// ChromePlatform decides which GLFW platform the app should force at start so that custom
// window chrome works. It returns "x11" when custom chrome is wanted, the user left the
// window system on "auto" (or empty), the session is Wayland, XWayland is reachable and no
// FYNE_PLATFORM override exists; "" means: change nothing. Native Wayland offers no way
// for an application to start a window move or resize (see CanCustomChrome).
func ChromePlatform(customChrome bool, platformSetting string, getenv func(string) string) string {
	if !customChrome || runtime.GOOS != "linux" {
		return ""
	}
	platformSetting = strings.ToLower(strings.TrimSpace(platformSetting))
	if platformSetting != "" && platformSetting != "auto" {
		return ""
	}
	if getenv("FYNE_PLATFORM") != "" {
		return ""
	}
	if getenv("WAYLAND_DISPLAY") == "" || getenv("DISPLAY") == "" {
		return ""
	}
	return "x11"
}

// NeedsXWaylandForChrome reports whether the app must run through XWayland for custom
// chrome (ChromePlatform == "x11"), using the process environment.
func NeedsXWaylandForChrome(customChrome bool, platformSetting string) bool {
	return ChromePlatform(customChrome, platformSetting, os.Getenv) == "x11"
}

// SupportsChrome reports whether a window system ("x11", "wayland", "windows", "macos")
// lets an application move and resize its own borderless windows.
func SupportsChrome(platform string) bool {
	switch platform {
	case "x11", "windows", "macos":
		return true
	}
	return false
}

// GuessPlatform predicts the window system before GLFW is initialised (the driver starts
// GLFW in Run). It follows Fyne's own selection: FYNE_PLATFORM, else Wayland when a
// Wayland session exists.
func GuessPlatform(goos string, getenv func(string) string) string {
	switch goos {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	}
	switch getenv("FYNE_PLATFORM") {
	case "x11":
		return "x11"
	case "wayland":
		return "wayland"
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if getenv("DISPLAY") != "" {
		return "x11"
	}
	return ""
}
