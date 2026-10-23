package settingsui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/winchrome"
)

func isFrame(w fyne.Window) bool {
	_, ok := w.Content().(*chrome.Frame)
	return ok
}

func TestSettingsWindowGetsCustomChrome(t *testing.T) {
	fk := winchrome.UseFake(t)
	f := newFixture(t, "display")
	if !f.w.custom || !isFrame(f.w.win) || fk.IsDecorated(f.w.win) {
		t.Fatal("custom chrome expected")
	}
	// Live switch off and on again, the page stays.
	if err := settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = false }); err != nil {
		t.Fatal(err)
	}
	if f.w.custom || isFrame(f.w.win) || !fk.IsDecorated(f.w.win) {
		t.Fatal("system title bar expected")
	}
	if f.w.cur == nil || f.w.cur.id != catDisplay {
		t.Fatal("the shown category changed")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.CustomChrome = true })
	if !f.w.custom || !isFrame(f.w.win) {
		t.Fatal("custom chrome expected again")
	}
}

func TestSettingsWindowSystemChromeWhereUnsupported(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.CanChrome = false
	f := newFixture(t, "display")
	if f.w.custom || isFrame(f.w.win) || !fk.IsDecorated(f.w.win) {
		t.Fatal("system title bar expected")
	}
}

func TestSettingsCloseButtonCloses(t *testing.T) {
	fk := winchrome.UseFake(t)
	f := newFixture(t, "display")
	fk.Close(f.w.win)
	if !f.w.closed {
		t.Fatal("the close button must close the settings window")
	}
}

func TestCloseFocused(t *testing.T) {
	fk := winchrome.UseFake(t)
	f := newFixture(t, "display")
	fk.SetState(f.w.win, chrome.State{})
	if CloseFocused() {
		t.Fatal("not focused")
	}
	fk.SetState(f.w.win, chrome.State{Focused: true})
	if !CloseFocused() || !f.w.closed {
		t.Fatal("focused window must close")
	}
}

func TestDisplayCategoryHasChromeRows(t *testing.T) {
	winchrome.UseFake(t)
	f := newFixture(t, "display")
	f.field("Eigene Titelleiste")
	f.field("Fensterknöpfe")
	if err := settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = "win" }); err != nil {
		t.Fatal(err)
	}
	if f.saved().Display.WindowButtons != "win" {
		t.Fatal("style not saved")
	}
}

func TestChromeRestartPendingOnWayland(t *testing.T) {
	winchrome.UseFake(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	t.Setenv("DISPLAY", ":0")
	f := newFixture(t, "display")
	s := settings.Get()
	if len(pendingRestart(s)) != 0 {
		t.Fatalf("nothing changed yet: %v", pendingRestart(s))
	}
	s.Display.CustomChrome = false // started with it on: native Wayland needs a restart
	if got := pendingRestart(s); len(got) != 1 || got[0] != "Eigene Titelleiste" {
		t.Fatalf("got %v", got)
	}
	_ = f
}

func TestWindowInfoExplainsWhy(t *testing.T) {
	fk := winchrome.UseFake(t)
	fk.CanChrome = false
	f := newFixture(t, "display")
	if f.w.infoRefresh == nil {
		t.Fatal("no info block")
	}
	active, text := winchrome.Describe(settings.Get().Display)
	if active || !strings.HasPrefix(text, "Nicht verfügbar") {
		t.Fatalf("%v %q", active, text)
	}
}
