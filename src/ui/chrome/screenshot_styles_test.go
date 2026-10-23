package chrome

import (
	"image"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// TestScreenshotStyles writes /tmp/fs-chrome-<style>-states.png (button crops, 4x) and
// /tmp/fs-chrome-<style>-bars.png (whole bars, 2x) for mac, win and linux when
// FS_SCREENSHOT is set. Test driver only, no real window.
func TestScreenshotStyles(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-chrome-<style>-*.png")
	}
	studiotheme.Configure("steel", false)
	fb := newFake()
	title := widget.NewLabelWithStyle("Interview Anna", fyne.TextAlignCenter, fyne.TextStyle{})
	render := func(style string, st State, center fyne.CanvasObject, prep func(*Controls)) image.Image {
		fb.st = st
		w := test.NewTempWindow(t, nil)
		w.SetPadded(false)
		tb := NewTitlebar(w, Options{Style: style, Backend: fb, CanMaximize: true, CanFullscreen: true}, nil, center, nil)
		defer tb.Close()
		w.SetContent(tb)
		w.Resize(fyne.NewSize(420, 28))
		if prep != nil {
			prep(tb.Controls())
		}
		return w.Canvas().Capture()
	}
	over := func(i int) func(*Controls) {
		return func(c *Controls) {
			p := c.centreOf(i)
			c.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: p}})
		}
	}
	press := func(i int) func(*Controls) {
		return func(c *Controls) {
			over(i)(c)
			c.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: c.centreOf(i)}, Button: desktop.MouseButtonPrimary})
		}
	}
	for _, style := range []string{StyleMac, StyleWin, StyleLinux} {
		var cx0, cx1 int // crop of the controls inside the 420 wide bar
		if style == StyleMac {
			cx0, cx1 = 0, 80
		} else {
			cx0, cx1 = 420-140, 420
		}
		crop := image.Rect(cx0, 0, cx1, 28)
		focused := State{Focused: true}
		var rows []image.Image
		add := func(st State, prep func(*Controls)) {
			rows = append(rows, enlarge(render(style, st, nil, prep), crop, 4))
		}
		add(focused, nil)
		add(focused, over(0))
		add(focused, over(1))
		add(focused, over(2))
		add(focused, press(1))
		add(focused, press(2))
		add(State{}, nil)
		add(State{Focused: true, Maximized: true}, nil)
		add(State{Focused: true, Maximized: true}, over(1))
		writePNG(t, "/tmp/fs-chrome-"+style+"-states.png", stack(rows))

		bars := []image.Image{
			enlarge(render(style, focused, title, nil), image.Rect(0, 0, 420, 28), 2),
			enlarge(render(style, State{}, title, nil), image.Rect(0, 0, 420, 28), 2),
			enlarge(render(style, State{Focused: true, Maximized: true}, title, over(2)), image.Rect(0, 0, 420, 28), 2),
		}
		writePNG(t, "/tmp/fs-chrome-"+style+"-bars.png", stack(bars))
	}
}

// centreOf is the centre of the i-th button (test helper for the screenshots).
func (c *Controls) centreOf(i int) fyne.Position {
	if c.spec().perButton {
		x0, x1 := c.slot(i)
		return fyne.NewPos(float32(x0+x1)/2, float32(c.height())/2)
	}
	x, y := c.macCentre(i)
	return fyne.NewPos(float32(x), float32(y))
}
