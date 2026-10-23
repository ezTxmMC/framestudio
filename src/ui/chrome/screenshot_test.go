package chrome

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// enlarge crops r out of img and scales it by k with nearest neighbour.
func enlarge(img image.Image, r image.Rectangle, k int) image.Image {
	r = r.Intersect(img.Bounds())
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx()*k, r.Dy()*k))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.Set(x, y, img.At(r.Min.X+x/k, r.Min.Y+y/k))
		}
	}
	return out
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// stack puts the images below each other with a small gap.
func stack(imgs []image.Image) image.Image {
	w, h := 0, 0
	for _, im := range imgs {
		w = max(w, im.Bounds().Dx())
		h += im.Bounds().Dy() + 6
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(image.Black), image.Point{}, draw.Src)
	y := 0
	for _, im := range imgs {
		draw.Draw(out, image.Rect(0, y, im.Bounds().Dx(), y+im.Bounds().Dy()), im, im.Bounds().Min, draw.Src)
		y += im.Bounds().Dy() + 6
	}
	return out
}

// TestScreenshotChrome writes /tmp/fs-chrome-*.png when FS_SCREENSHOT is set (test driver
// only, no real window): the mac lights in every state and the win style, enlarged 4x.
func TestScreenshotChrome(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-chrome-*.png")
	}
	studiotheme.Configure("steel", false)
	fb := newFake()
	render := func(o Options, st State, lead, center fyne.CanvasObject, width float32, prep func(*Titlebar)) image.Image {
		fb.st = st
		w := test.NewTempWindow(t, nil)
		w.SetPadded(false)
		o.Backend = fb
		tb := NewTitlebar(w, o, lead, center, nil)
		defer tb.Close()
		w.SetContent(tb)
		h := float32(28)
		if o.Compact {
			h = 24
		}
		w.Resize(fyne.NewSize(width, h))
		if prep != nil {
			prep(tb)
		}
		return w.Canvas().Capture()
	}
	hover := func(p fyne.Position, mod fyne.KeyModifier) func(*Titlebar) {
		return func(tb *Titlebar) {
			tb.Controls().MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: p}, Modifier: mod})
		}
	}
	title := widget.NewLabelWithStyle("Interview Anna", fyne.TextAlignCenter, fyne.TextStyle{})
	mac := Options{Style: StyleMac, CanMaximize: true, CanFullscreen: true}
	win := Options{Style: StyleWin, CanMaximize: true, CanFullscreen: true}
	focused := State{Focused: true}
	crop := image.Rect(0, 0, 90, 28)

	// the lights, one row per state, 8x enlarged
	rows := []image.Image{
		enlarge(render(mac, focused, nil, nil, 200, nil), crop, 8),
		enlarge(render(mac, focused, nil, nil, 200, hover(posMin, 0)), crop, 8),
		enlarge(render(mac, focused, nil, nil, 200, hover(posZoom, fyne.KeyModifierAlt)), crop, 8),
		enlarge(render(mac, State{}, nil, nil, 200, nil), crop, 8),
		enlarge(render(mac, State{Focused: true, Fullscreen: true}, nil, nil, 200, hover(posZoom, 0)), crop, 8),
		enlarge(render(Options{Style: StyleMac}, focused, nil, nil, 200, hover(posZoom, 0)), crop, 8),
		enlarge(render(mac, focused, nil, nil, 200, func(tb *Titlebar) {
			hover(posMin, 0)(tb)
			tb.Controls().MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: posMin}, Button: desktop.MouseButtonPrimary})
		}), crop, 8),
	}
	writePNG(t, "/tmp/fs-chrome-lights.png", stack(rows))

	// whole bars at 3x
	bar := func(img image.Image, w int) image.Image { return enlarge(img, image.Rect(0, 0, w, 28), 3) }
	menuLike := container.NewHBox(
		widget.NewLabel("Datei"), widget.NewLabel("Bearbeiten"), widget.NewLabel("Ansicht"))
	bars := []image.Image{
		bar(render(mac, focused, nil, title, 420, nil), 420),
		bar(render(mac, focused, menuLike, title, 420, hover(posClose, 0)), 420),
		bar(render(mac, State{Focused: false}, menuLike, title, 420, nil), 420),
		bar(render(win, focused, menuLike, title, 420, nil), 420),
		bar(render(win, State{Focused: true, Maximized: true}, menuLike, title, 420, nil), 420),
		bar(render(win, focused, menuLike, title, 420, hover(pt(115, 14), 0)), 420),
		bar(render(win, State{}, menuLike, title, 420, nil), 420),
		enlarge(render(Options{Style: StyleMac, Compact: true, CanMaximize: true, CanFullscreen: true}, focused, nil, title, 420, hover(posMin, 0)), image.Rect(0, 0, 420, 24), 3),
	}
	writePNG(t, "/tmp/fs-chrome-bars.png", stack(bars))

	// frame with outline
	w := test.NewTempWindow(t, nil)
	w.SetPadded(false)
	fr := NewFrame(w, fb, canvas.NewRectangle(studiotheme.Gap))
	defer fr.Close()
	w.SetContent(fr)
	w.Resize(fyne.NewSize(120, 80))
	writePNG(t, "/tmp/fs-chrome-frame.png", enlarge(w.Canvas().Capture(), image.Rect(0, 0, 120, 80), 4))
}
