package workspace

import (
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/winchrome"
)

// TestDetachedHeaderScreenshot writes the system-title-bar header of a detached window
// (FS_SCREENSHOT=1) from the test driver.
func TestDetachedHeaderScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1")
	}
	fk := winchrome.UseFake(t)
	fk.CanChrome = false
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	d.win.Resize(fyne.NewSize(900, 300))
	f, err := os.Create("/tmp/fs-r4-shell-detached.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, d.win.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
	_ = chrome.StyleMac
}
