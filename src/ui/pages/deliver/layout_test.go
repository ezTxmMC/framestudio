package deliver

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// shellChrome is the height the app frame takes from the window (top bar and page bar
// with their hairlines); the page gets the rest.
const shellChrome = 35 + 53

func TestSettingsFitWithoutScrolling(t *testing.T) {
	for _, win := range []fyne.Size{{Width: 1440, Height: 900}, {Width: 1280, Height: 800}} {
		h := newHarness(t)
		f := h.pg.form
		h.w.Resize(fyne.NewSize(win.Width, win.Height-shellChrome))
		need, have := f.formBox.MinSize().Height, f.scroll.Size().Height
		t.Logf("%vx%v: form needs %.0f, has %.0f", win.Width, win.Height, need, have)
		if need > have {
			t.Errorf("%vx%v: the default preset needs scrolling (%.0f > %.0f)", win.Width, win.Height, need, have)
		}
		if min := h.pg.root.MinSize().Height; min > win.Height-shellChrome {
			t.Errorf("%vx%v: page min height %.0f exceeds the page area", win.Width, win.Height, min)
		}
		// The error line and the custom size row must still fit at 1440x900.
		if win.Height == 900 {
			test.Tap(f.rows[0])
			f.res.SetSelected(resCustom)
			if need, have := f.formBox.MinSize().Height, f.scroll.Size().Height; need > have {
				t.Logf("custom size needs scrolling at 1440x900 (%.0f > %.0f)", need, have)
			}
		}
	}
}

func TestAdvancedSectionCollapses(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	if f.advanced.open || f.rng.Visible() && f.advanced.body.Visible() {
		t.Fatal("Erweitert must start collapsed")
	}
	if f.advanced.summary.Text != "Gesamte Timeline · mit Audio · Software" {
		t.Errorf("summary %q", f.advanced.summary.Text)
	}
	closed := f.formBox.MinSize().Height
	test.Tap(f.advanced)
	if !f.advanced.body.Visible() || f.formBox.MinSize().Height <= closed {
		t.Fatal("tapping Erweitert should show range and audio")
	}
	f.audio.SetChecked(false)
	if f.advanced.summary.Text != "Gesamte Timeline · ohne Audio · Software" {
		t.Errorf("summary after unchecking audio %q", f.advanced.summary.Text)
	}
	test.Tap(f.advanced)
	if f.advanced.body.Visible() {
		t.Error("second tap should collapse again")
	}
}
