package shell

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/winchrome"
)

func chromeShell(t *testing.T) (*Shell, fyne.Window, *winchrome.Fake) {
	t.Helper()
	fk := winchrome.UseFake(t)
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.decorate() // Run does this once the window is shown
	return sh, w, fk
}

func isFrame(w fyne.Window) bool {
	_, ok := w.Content().(*chrome.Frame)
	return ok
}

func TestMainWindowGetsCustomChrome(t *testing.T) {
	sh, w, fk := chromeShell(t)
	if !sh.CustomChrome() || !isFrame(w) {
		t.Fatalf("custom chrome expected: on=%v frame=%v", sh.CustomChrome(), isFrame(w))
	}
	if fk.IsDecorated(w) {
		t.Fatal("the system title bar must be taken off")
	}
	if sh.chrome == nil || sh.chrome.Bar == nil {
		t.Fatal("no title bar")
	}
}

func TestSystemChromeWhereUnsupported(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.CanChrome = false
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.decorate()
	if sh.CustomChrome() || isFrame(w) || sh.chrome != nil {
		t.Fatal("native Wayland keeps the system title bar and the menu row")
	}
	if !fk.IsDecorated(w) {
		t.Fatal("must not be undecorated")
	}
}

func TestDecorateFailureFallsBack(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.DecorateErr = display.ErrNoChrome
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.decorate()
	if sh.CustomChrome() || isFrame(w) {
		t.Fatal("falls back to the system title bar")
	}
	if winchrome.Failure() == nil {
		t.Fatal("the reason must be kept for the diagnostics")
	}
	found := false
	for _, r := range sh.diagnostics() {
		if r[0] == "Eigene Titelleiste" && r[1] != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("diagnostics lack the title bar row")
	}
}

func TestDecorateRetriedAfterStart(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.DecorateErr = display.ErrNoChrome // the native window does not exist yet
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.decorate()
	if sh.CustomChrome() {
		t.Fatal("first attempt must fall back")
	}
	fk.DecorateErr = nil
	sh.decorateAgain()
	if !sh.CustomChrome() || !isFrame(w) || fk.IsDecorated(w) || winchrome.Failure() != nil {
		t.Fatal("second attempt after the start must give the custom title bar")
	}
}

func TestLiveSwitchKeepsState(t *testing.T) {
	sh, w, fk := chromeShell(t)
	sh.Start([]string{saveSample(t, "Schalter", 1920, 1080, 25, 50)})
	p := sh.active
	if p == nil {
		t.Fatal("no project")
	}
	sh.showPageID("color")
	if err := settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = false }); err != nil {
		t.Fatal(err)
	}
	if sh.CustomChrome() || isFrame(w) || !fk.IsDecorated(w) {
		t.Fatalf("system chrome expected: on=%v decorated=%v", sh.CustomChrome(), fk.IsDecorated(w))
	}
	if sh.active != p || p.current != "color" || len(sh.projects) != 1 {
		t.Fatal("state lost when switching off")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = true })
	if !sh.CustomChrome() || !isFrame(w) || fk.IsDecorated(w) {
		t.Fatal("custom chrome expected again")
	}
	if sh.active != p || p.current != "color" {
		t.Fatal("state lost when switching on")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = "win" })
	if !sh.CustomChrome() || !isFrame(w) {
		t.Fatal("button style change must keep the title bar")
	}
}

func TestWindowCommandsBound(t *testing.T) {
	sh, w, fk := chromeShell(t)
	for _, id := range []string{"window.minimize", "window.zoom", "window.close", "window.customChrome", "window.buttons.mac", "window.buttons.win"} {
		found := false
		for _, c := range sh.reg.Available() {
			found = found || c.ID == id
		}
		if !found {
			t.Errorf("%s is not bound", id)
		}
	}
	sh.reg.Run("window.minimize")
	sh.reg.Run("window.zoom")
	if !fk.Called("minimize") || !fk.Called("zoom") {
		t.Fatalf("calls %v", fk.Calls)
	}
	checked := func(id string) bool {
		for _, c := range sh.reg.Available() {
			if c.ID == id {
				return c.Checked
			}
		}
		t.Fatalf("%s missing", id)
		return false
	}
	if !checked("window.customChrome") || !checked("window.buttons.mac") || checked("window.buttons.win") {
		t.Fatal("initial check marks")
	}
	sh.reg.Run("window.buttons.win")
	if checked("window.buttons.mac") || !checked("window.buttons.win") {
		t.Fatal("radio did not switch")
	}
	sh.reg.Run("window.customChrome")
	if checked("window.customChrome") || sh.CustomChrome() {
		t.Fatal("toggle did not switch off")
	}
	_ = w
}

func TestFullscreenCheckFollowsGreenButton(t *testing.T) {
	sh, w, fk := chromeShell(t)
	checked := func() bool {
		for _, c := range sh.reg.Available() {
			if c.ID == "view.fullscreen" {
				return c.Checked
			}
		}
		return false
	}
	if checked() {
		t.Fatal("not fullscreen yet")
	}
	fk.SetFullscreen(w, true) // the green button
	if !fk.Called("fullscreen:on") || !w.FullScreen() || !checked() {
		t.Fatal("green button must use the same path as view.fullscreen")
	}
	fk.SetFullscreen(w, false)
	if w.FullScreen() || checked() {
		t.Fatal("leaving fullscreen")
	}
}

func TestCustomCloseRunsQuit(t *testing.T) {
	sh, w, fk := chromeShell(t)
	closed := false
	sh.closeWindow = func() { closed = true }
	fk.Close(w) // the red button
	if !closed || !sh.shutdownDone {
		t.Fatal("the custom close button must run the normal quit flow")
	}
}

func TestCustomCloseAsksAboutUnsavedProjects(t *testing.T) {
	sh, w, fk := chromeShell(t)
	sh.Start([]string{saveSample(t, "Offen", 1920, 1080, 25, 50)})
	sh.active.sess.Dirty = true
	closed := false
	sh.closeWindow = func() { closed = true }
	fk.Close(w)
	if closed {
		t.Fatal("window closed without asking")
	}
	if sh.pendingAnswer == nil {
		t.Fatal("no save prompt")
	}
}

func TestNoNativeMenu(t *testing.T) {
	_, w, _ := chromeShell(t)
	if m := w.MainMenu(); m != nil {
		t.Fatalf("native main menu set: %v", m)
	}
}

func TestWindowMenuHasWindowEntries(t *testing.T) {
	var ids []string
	for _, m := range command.MenuBar {
		if m.Title != "Fenster" {
			continue
		}
		var walk func(items []command.Item)
		walk = func(items []command.Item) {
			for _, it := range items {
				if it.Command != "" {
					ids = append(ids, it.Command)
				}
				if it.Submenu != nil {
					walk(it.Submenu.Items)
				}
			}
		}
		walk(m.Items)
	}
	for _, want := range []string{"window.minimize", "window.zoom", "view.fullscreen", "window.customChrome", "window.buttons.mac", "window.buttons.win"} {
		found := false
		for _, id := range ids {
			found = found || id == want
		}
		if !found {
			t.Errorf("Fenster menu lacks %s", want)
		}
	}
}

// TestTopAreaScreenshots writes the top of the window for both modes and both button
// styles (FS_SCREENSHOT=1), from the test driver: no real window is opened.
func TestTopAreaScreenshots(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1")
	}
	for _, c := range []struct {
		name   string
		custom bool
		style  string
	}{{"custom-mac", true, "mac"}, {"custom-win", true, "win"}, {"system", false, "mac"}} {
		t.Run(c.name, func(t *testing.T) {
			fk := winchrome.UseFake(t)
			fk.CanChrome = c.custom
			a, q := newR3App(t)
			_ = settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = c.style })
			sh, w := buildShell(t, a, q)
			sh.decorate()
			sh.Start([]string{saveSample(t, "Schnittprojekt", 1920, 1080, 25, 100)})
			w.Resize(fyne.NewSize(1440, 900))
			f, err := os.Create("/tmp/fs-r4-shell-" + c.name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, w.Canvas().Capture()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestChromeScreenshotsInt writes /tmp/fs-int-chrome-{mac,win,system,fullscreen,inactive}.png
// (FS_SCREENSHOT=1, test driver only).
func TestChromeScreenshotsInt(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1")
	}
	for _, c := range []struct {
		name   string
		custom bool
		style  string
		state  *chrome.State
	}{
		{"mac", true, "mac", nil}, {"win", true, "win", nil}, {"system", false, "mac", nil},
		{"fullscreen", true, "mac", &chrome.State{Focused: true, Fullscreen: true}},
		{"inactive", true, "mac", &chrome.State{Focused: false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fk := winchrome.UseFake(t)
			fk.CanChrome = c.custom
			a, q := newR3App(t)
			_ = settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = c.style })
			sh, w := buildShell(t, a, q)
			sh.decorate()
			sh.Start([]string{saveSample(t, "Schnittprojekt", 1920, 1080, 25, 100)})
			w.Resize(fyne.NewSize(1440, 900))
			if c.state != nil {
				fk.SetState(w, *c.state)
			}
			f, err := os.Create("/tmp/fs-int-chrome-" + c.name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, w.Canvas().Capture()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
