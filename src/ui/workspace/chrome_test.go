package workspace

import (
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/winchrome"
)

func isFrame(w fyne.Window) bool {
	_, ok := w.Content().(*chrome.Frame)
	return ok
}

func TestDetachedWindowGetsCustomChrome(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	if d == nil || !d.custom || !isFrame(d.win) || d.chrome == nil {
		t.Fatal("detached window without custom chrome")
	}
	if fk.IsDecorated(d.win) {
		t.Fatal("system title bar must be taken off")
	}
	if d.header != nil {
		t.Fatal("one header row only: the old header must be gone")
	}
}

func TestDetachedWindowKeepsSystemChromeWhenUnsupported(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.CanChrome = false
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	if d.custom || isFrame(d.win) || d.header == nil || !fk.IsDecorated(d.win) {
		t.Fatal("system title bar and the thin header expected")
	}
}

func TestDetachedDecorateFailureKeepsHeader(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.DecorateErr = errTest
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	if d.custom || isFrame(d.win) || d.header == nil {
		t.Fatal("must fall back to the system title bar")
	}
}

var errTest = errString("kein Fensterbild")

type errString string

func (e errString) Error() string { return string(e) }

func TestCleanFeedChrome(t *testing.T) {
	winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelCleanFeed, "DP-3", true)
	d := h.m.find(page.PanelCleanFeed)
	if !d.win.FullScreen() || isFrame(d.win) || d.chrome != nil {
		t.Fatal("a fullscreen clean feed has no bar, no frame and no grips")
	}
	d.setFullscreen(false)
	if d.win.FullScreen() || !isFrame(d.win) {
		t.Fatal("a windowed clean feed gets the thin bar")
	}
	d.setFullscreen(true)
	if isFrame(d.win) {
		t.Fatal("chrome must go again in fullscreen")
	}
}

func TestPanelFullscreenKeepsBar(t *testing.T) {
	winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", true)
	d := h.m.find(page.PanelScopes)
	if !d.win.FullScreen() || !isFrame(d.win) {
		t.Fatal("fullscreen keeps the bar of ordinary panels")
	}
}

func TestDetachedCloseButtonUsesWorkspaceClose(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	fk.Close(d.win) // the red button
	if h.m.find(page.PanelScopes) != nil || !d.done {
		t.Fatal("the close button must put the panel back (workspace close)")
	}
}

func TestDetachedGreenButtonUsesWorkspaceFullscreen(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	fk.SetFullscreen(d.win, true)
	if s := h.state(page.PanelScopes); s.mode != layout.ModeFullscreen {
		t.Fatalf("state %+v", s)
	}
}

func TestDetachedChromeFollowsSettingsLive(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	panel := lastPanel(page.PanelScopes)
	if err := settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = false }); err != nil {
		t.Fatal(err)
	}
	if d.custom || isFrame(d.win) || d.header == nil || !fk.IsDecorated(d.win) {
		t.Fatal("system title bar expected after switching off")
	}
	if lastPanel(page.PanelScopes) != panel || panel.closed {
		t.Fatal("the panel must survive the switch")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = true })
	if !d.custom || !isFrame(d.win) || fk.IsDecorated(d.win) {
		t.Fatal("custom chrome expected after switching on")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = "win" })
	if !isFrame(d.win) {
		t.Fatal("button style change keeps the bar")
	}
}

func TestMaximizeWithBarKeepsLayoutCapture(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	fk.SetState(d.win, chrome.State{Focused: true, Maximized: true})
	if d.mode != layout.ModeMaximized {
		t.Fatalf("mode %q", d.mode)
	}
	fk.SetState(d.win, chrome.State{Focused: true})
	if d.mode != layout.ModeWindow {
		t.Fatalf("mode %q after restore", d.mode)
	}
	l := h.m.Current()
	found := false
	for _, w := range l.Windows {
		found = found || w.Panel == page.PanelScopes
	}
	if !found {
		t.Fatal("window missing in the captured layout")
	}
}

func TestCloseFocusedClosesTheFocusedDetachedWindow(t *testing.T) {
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	fk.SetState(d.win, chrome.State{})
	if h.m.CloseFocused() {
		t.Fatal("an unfocused window must stay")
	}
	fk.SetState(d.win, chrome.State{Focused: true})
	if !h.m.CloseFocused() || h.m.find(page.PanelScopes) != nil {
		t.Fatal("the focused window must close")
	}
}
