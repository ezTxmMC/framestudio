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

// TestDetachedChromeScreenshot writes /tmp/fs-int-chrome-detached.png (FS_SCREENSHOT=1,
// test driver only).
func TestDetachedChromeScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1")
	}
	fk := winchrome.UseFake(t)
	h := newHarness(t, true)
	h.host.ctx = &page.Context{}
	h.m.Detach(page.PanelScopes, "DP-3", false)
	d := h.m.find(page.PanelScopes)
	d.win.Resize(fyne.NewSize(900, 300))
	shot := func(name string) {
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, d.win.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
	shot("/tmp/fs-int-chrome-detached.png")
	fk.SetState(d.win, chrome.State{Focused: false})
	shot("/tmp/fs-int-chrome-detached-inactive.png")
}
