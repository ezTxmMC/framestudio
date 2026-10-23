// Package winchrome connects the windows of the app to the custom window chrome: it
// decides whether a window gets the app's own title bar, adapts package display to the
// chrome.Backend, remembers the close and fullscreen paths of each window (so the custom
// close button honours the window's close intercept) and builds the framed content.
//
// Shell, workspace and settings window share it; tests replace the variables below.
package winchrome

import (
	"os"
	"runtime"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/display"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Bar heights.
const (
	Height        float32 = 28 // main window
	CompactHeight float32 = 24 // secondary windows
)

// Seams for tests.
var (
	// Can reports whether the window system lets borderless windows be moved and resized.
	Can = display.CanCustomChrome
	// SetDecorated shows or hides the system title bar.
	SetDecorated = display.SetDecorated
	// Sys is the window system as the chrome uses it.
	Sys chrome.Backend = System{}
)

var (
	mu       sync.Mutex
	failure  error
	handlers = map[fyne.Window]*handler{}
)

type handler struct {
	close      func()
	fullscreen func(on bool)
}

// Wanted reports whether windows get the custom title bar now: the setting is on and the
// window system supports it.
func Wanted(d settings.Display) bool { return d.CustomChrome && Can() }

// Style is the button style of d for the running system ("mac", "win" or "linux").
func Style(d settings.Display) string { return StyleFor(d, runtime.GOOS) }

// StyleFor is Style for any operating system name (runtime.GOOS values).
func StyleFor(d settings.Display, goos string) string {
	return chrome.ResolveStyle(d.WindowButtons, goos)
}

// Undecorate removes the system title bar of w. On failure the error is remembered for
// the diagnostics and returned; the caller keeps the system title bar.
func Undecorate(w fyne.Window) error {
	err := SetDecorated(w, false)
	SetFailure(err)
	return err
}

// Redecorate gives w its system title bar back.
func Redecorate(w fyne.Window) error { return SetDecorated(w, true) }

// SetFailure remembers why a window could not lose its system title bar (nil clears it).
func SetFailure(err error) {
	mu.Lock()
	failure = err
	mu.Unlock()
}

// Failure is the error SetFailure remembered.
func Failure() error {
	mu.Lock()
	defer mu.Unlock()
	return failure
}

func entry(w fyne.Window) *handler {
	h := handlers[w]
	if h == nil {
		h = &handler{}
		handlers[w] = h
	}
	return h
}

// OnClose sets what the custom close button does for w (its close intercept).
func OnClose(w fyne.Window, fn func()) {
	mu.Lock()
	entry(w).close = fn
	mu.Unlock()
	chrome.OnClose(w, fn)
}

// OnFullscreen sets how the green button puts w fullscreen and back.
func OnFullscreen(w fyne.Window, fn func(on bool)) {
	mu.Lock()
	entry(w).fullscreen = fn
	mu.Unlock()
}

// Forget drops what is remembered for w (after it closed).
func Forget(w fyne.Window) {
	mu.Lock()
	delete(handlers, w)
	mu.Unlock()
	chrome.OnClose(w, nil)
}

func handlerOf(w fyne.Window) handler {
	mu.Lock()
	defer mu.Unlock()
	if h := handlers[w]; h != nil {
		return *h
	}
	return handler{}
}

// System is the chrome.Backend backed by package display.
type System struct{}

func (System) BeginMove(w fyne.Window)             { display.BeginMove(w) }
func (System) BeginResize(w fyne.Window, edge int) { display.BeginResize(w, display.Edge(edge)) }
func (System) Minimize(w fyne.Window)              { display.Minimize(w) }
func (System) ToggleMaximize(w fyne.Window)        { display.ToggleMaximize(w) }

func (System) SetFullscreen(w fyne.Window, on bool) {
	if h := handlerOf(w); h.fullscreen != nil {
		h.fullscreen(on)
		return
	}
	if err := display.Fullscreen(w, "", on); err != nil || w.FullScreen() != on {
		w.SetFullScreen(on)
	}
}

func (System) Close(w fyne.Window) {
	chrome.RequestClose(w)
}

func (System) State(w fyne.Window) chrome.State { return chrome.State(display.Current(w)) }

func (System) Watch(w fyne.Window, fn func(chrome.State)) (stop func()) {
	return display.Watch(w, func(s display.State) { fn(chrome.State(s)) })
}

// Options returns the title bar options of a window.
func Options(d settings.Display, compact, canMax, canFull bool) chrome.Options {
	return chrome.Options{Style: Style(d), Compact: compact, Backend: Sys, CanMaximize: canMax, CanFullscreen: canFull}
}

// Parts are the areas of a title bar next to the window buttons.
type Parts struct{ Leading, Center, Trailing fyne.CanvasObject }

// Chrome is the title bar and frame of one window.
type Chrome struct {
	Bar   *chrome.Titlebar
	Frame *chrome.Frame
}

// Build returns the content of w with the custom title bar above body and the resize
// frame around both, plus the Chrome to release with Close.
func Build(w fyne.Window, o chrome.Options, p Parts, body fyne.CanvasObject) (fyne.CanvasObject, *Chrome) {
	if o.Backend == nil {
		o.Backend = Sys
	}
	c := &Chrome{Bar: chrome.NewTitlebar(w, o, p.Leading, p.Center, p.Trailing)}
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	bar := container.NewBorder(nil, line, nil, nil, c.Bar)
	c.Frame = chrome.NewFrame(w, o.Backend, container.NewBorder(bar, nil, nil, nil, body))
	return c.Frame, c
}

// Close releases the watchers of the title bar and frame.
func (c *Chrome) Close() {
	if c == nil {
		return
	}
	c.Bar.Close()
	c.Frame.Close()
}

// CurrentPlatform is the window system in use: the running one, else the predicted one.
func CurrentPlatform() string {
	if p := display.Platform(); p != "" {
		return p
	}
	return display.GuessPlatform(runtime.GOOS, os.Getenv)
}

// WantsXWayland reports whether the program must run through XWayland for the custom
// title bar with these settings. userPlatform is the FYNE_PLATFORM the user set ("" when
// the program chose it itself).
func WantsXWayland(customChrome bool, platformSetting, userPlatform string) bool {
	return display.ChromePlatform(customChrome, platformSetting, func(k string) string {
		if k == "FYNE_PLATFORM" {
			return userPlatform
		}
		return os.Getenv(k)
	}) == "x11"
}

// Status says whether the custom title bar is active and, when not, why; when active the
// text names the window system. getenv answers for the user's environment.
func Status(customChrome bool, platformSetting, platform string, can bool, getenv func(string) string, failed error) (active bool, text string) {
	name := platformName(platform)
	switch {
	case !customChrome:
		return false, "Aus (Einstellung „Eigene Titelleiste“ ist ausgeschaltet). Fenster zeigen die Titelleiste des Systems."
	case failed != nil:
		return false, "Nicht verfügbar: " + failed.Error() + " Fenster zeigen die Titelleiste des Systems."
	case can:
		return true, "Aktiv (" + PlatformLabel(platform, getenv) + ")"
	case platform == "":
		return false, "Nicht verfügbar: Es wurde kein Fenstersystem erkannt."
	case platform == "wayland" && getenv("DISPLAY") == "":
		return false, "Nicht verfügbar: Natives Wayland ohne XWayland (DISPLAY fehlt). Dort kann die Anwendung Fenster nicht selbst verschieben."
	case platform == "wayland" && platformSetting == "wayland":
		return false, "Nicht verfügbar: Das Fenstersystem ist auf Wayland festgelegt. Dort kann die Anwendung Fenster nicht selbst verschieben."
	case platform == "wayland" && getenv("FYNE_PLATFORM") == "wayland":
		return false, "Nicht verfügbar: FYNE_PLATFORM=wayland erzwingt natives Wayland. Dort kann die Anwendung Fenster nicht selbst verschieben."
	case platform == "wayland":
		return false, "Nicht verfügbar unter nativem Wayland. Nach einem Neustart läuft Frame Studio über XWayland und zeigt die eigene Titelleiste."
	}
	return false, "Nicht verfügbar: " + name + " unterstützt keine eigene Titelleiste."
}

// PlatformLabel names the window system for the user: "X11 über XWayland" when an x11
// program runs in a Wayland session.
func PlatformLabel(platform string, getenv func(string) string) string {
	if platform == "x11" && getenv("WAYLAND_DISPLAY") != "" {
		return "X11 über XWayland"
	}
	if platform == "" {
		return "unbekannt"
	}
	return platformName(platform)
}

func platformName(p string) string {
	switch p {
	case "x11":
		return "X11"
	case "wayland":
		return "Wayland"
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	}
	return p
}

// Describe is Status for the running program and settings.
func Describe(d settings.Display) (active bool, text string) {
	return Status(d.CustomChrome, d.Platform, CurrentPlatform(), Can(), os.Getenv, Failure())
}
