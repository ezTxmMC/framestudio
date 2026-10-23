package shell

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/winchrome"
)

func menuShotDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("FS_SCREENSHOT")
	if d == "" {
		t.Skip("set FS_SCREENSHOT=1")
	}
	if d == "1" {
		d = "/tmp/fs-menus"
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeCrop(t *testing.T, path string, c fyne.Canvas, r image.Rectangle, zoom int) {
	t.Helper()
	img := c.Capture()
	s := float64(c.Scale())
	px := image.Rect(int(float64(r.Min.X)*s), int(float64(r.Min.Y)*s), int(float64(r.Max.X)*s+0.5), int(float64(r.Max.Y)*s+0.5)).
		Intersect(img.Bounds())
	out := image.NewNRGBA(image.Rect(0, 0, px.Dx()*zoom, px.Dy()*zoom))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.Set(x, y, img.At(px.Min.X+x/zoom, px.Min.Y+y/zoom))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
}

// TestMenuTitleBarShots renders the custom title bar with its menus for the three button
// styles at scales 1, 1.25, 1.3 and 2 (FS_SCREENSHOT=1; test driver, no real window).
func TestMenuTitleBarShots(t *testing.T) {
	dir := menuShotDir(t)
	for _, style := range []string{"mac", "win", "linux"} {
		for _, s := range []float32{1, 1.25, 1.3, 2} {
			style, s := style, s
			t.Run(fmt.Sprintf("%s-%.2f", style, s), func(t *testing.T) {
				fk := winchrome.UseFake(t)
				fk.CanChrome = true
				a, q := newR3App(t)
				_ = settings.Update(func(st *settings.Settings) { st.Display.WindowButtons = style })
				sh, w := buildShell(t, a, q)
				sh.decorate()
				w.Canvas().(test.WindowlessCanvas).SetScale(s)
				w.Resize(fyne.NewSize(1440, 800))
				name := func(n string) string {
					return filepath.Join(dir, fmt.Sprintf("shell-%s-%.2f-%s.png", style, s, n))
				}
				mb := sh.bars.menu

				// no project: the menus are mostly disabled
				writeCrop(t, name("noproject-bar"), w.Canvas(), image.Rect(0, 0, 1440, 40), 2)
				pos, _ := mb.TitlePos("Bearbeiten")
				test.TapCanvas(w.Canvas(), pos)
				writeCrop(t, name("noproject-edit"), w.Canvas(), image.Rect(0, 0, 700, 340), 2)
				mb.Close()

				sh.Start([]string{saveSample(t, "Schnittprojekt mit einem langen Namen", 1920, 1080, 25, 100)})
				writeCrop(t, name("bar"), w.Canvas(), image.Rect(0, 0, 1440, 40), 2)
				test.MoveMouse(w.Canvas(), mustTitle(t, mb, "Timeline"))
				writeCrop(t, name("bar-hover"), w.Canvas(), image.Rect(0, 0, 700, 40), 3)

				pos, _ = mb.TitlePos("Datei")
				test.TapCanvas(w.Canvas(), pos)
				if p, ok := mb.ItemPos("Zuletzt geöffnet"); ok {
					test.MoveMouse(w.Canvas(), p)
				}
				writeCrop(t, name("file"), w.Canvas(), image.Rect(0, 0, 700, 340), 2)
				mb.Close()

				pos, _ = mb.TitlePos("Clip")
				test.TapCanvas(w.Canvas(), pos)
				if p, ok := mb.ItemPos("Clipfarbe"); ok {
					test.MoveMouse(w.Canvas(), p)
				}
				writeCrop(t, name("clip-sub"), w.Canvas(), image.Rect(0, 0, 900, 340), 2)
				mb.Close()
			})
		}
	}
}

func mustTitle(t *testing.T, mb interface {
	TitlePos(string) (fyne.Position, bool)
}, title string) fyne.Position {
	p, ok := mb.TitlePos(title)
	if !ok {
		t.Fatalf("no menu %s", title)
	}
	return p
}
