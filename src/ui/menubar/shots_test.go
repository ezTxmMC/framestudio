package menubar

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	studiotheme "framestudio-proto/src/ui/theme"
)

// shotDir is where FS_SCREENSHOT screenshot tests write their PNGs.
func shotDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("FS_SCREENSHOT")
	if d == "" {
		t.Skip("set FS_SCREENSHOT=1 (or a directory) to write screenshots")
	}
	if d == "1" {
		d = "/tmp/fs-menus"
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func savePNG(t *testing.T, path string, img image.Image) {
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

// crop writes the part r (canvas units) of the canvas capture, enlarged by zoom.
func crop(t *testing.T, path string, c fyne.Canvas, r image.Rectangle, zoom int) {
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
	savePNG(t, path, out)
}

func setScale(h *harness, s float32) {
	h.c().(test.WindowlessCanvas).SetScale(s)
	h.w.Resize(fyne.NewSize(900, 560))
}

// The bar with menus open at several scales: full shot and enlarged crops.
func TestShotsMenus(t *testing.T) {
	dir := shotDir(t)
	for _, s := range []float32{1, 1.25, 1.3, 2} {
		s := s
		t.Run(fmt.Sprint(s), func(t *testing.T) {
			h := newHarness(t)
			setScale(h, s)
			name := func(n string) string { return filepath.Join(dir, fmt.Sprintf("menu-%.2f-%s.png", s, n)) }
			area := image.Rect(0, 0, 520, 330)

			savePNG(t, name("bar-idle"), h.c().Capture())
			crop(t, name("bar-idle-zoom"), h.c(), image.Rect(0, 0, 300, 40), 3)

			test.MoveMouse(h.c(), h.titlePos(2))
			crop(t, name("bar-hover-zoom"), h.c(), image.Rect(0, 0, 300, 40), 3)

			h.openTitle("Datei")
			test.MoveMouse(h.c(), h.rowPos(0, "Speichern"))
			savePNG(t, name("file"), h.c().Capture())
			crop(t, name("file-zoom"), h.c(), area, 2)

			test.MoveMouse(h.c(), h.rowPos(0, "Zuletzt geöffnet"))
			crop(t, name("file-sub-zoom"), h.c(), image.Rect(0, 0, 640, 330), 2)
			h.bar.Close()

			h.openTitle("Clip")
			test.MoveMouse(h.c(), h.rowPos(0, "Clipfarbe"))
			test.MoveMouse(h.c(), h.rowPos(1, "Rosé"))
			crop(t, name("clip-sub-zoom"), h.c(), image.Rect(0, 0, 640, 330), 2)
			h.bar.Close()

			h.openTitle("Bearbeiten")
			crop(t, name("edit-zoom"), h.c(), area, 2)
			h.bar.Close()

			h.openTitle("Timeline")
			h.key(fyne.KeyDown)
			h.key(fyne.KeyDown)
			crop(t, name("timeline-zoom"), h.c(), area, 2)
			h.bar.Close()

			// keyboard mode: underlines and focus outline
			h.bar.HandleKeyDown(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
			h.bar.HandleKeyUp(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
			crop(t, name("bar-kbd-zoom"), h.c(), image.Rect(0, 0, 300, 40), 3)
			h.bar.Close()

			// context menu
			ShowContextMenu(h.c(), fyne.NewPos(300, 200), []Item{
				{Label: "Ausschneiden", Shortcut: "Strg+X", Icon: theme.ContentCutIcon()},
				{Label: "Kopieren", Shortcut: "Strg+C", Icon: theme.ContentCopyIcon()},
				{Label: "Einfügen", Shortcut: "Strg+V", Disabled: true, Icon: theme.ContentPasteIcon()},
				Sep(),
				{Label: "Clipfarbe", Children: []Item{
					{Label: "Teal", Swatch: studiotheme.ClipColors[0].Color},
					{Label: "Violett", Swatch: studiotheme.ClipColors[1].Color, Checked: true, Radio: true},
				}},
				{Label: "Magnetisch einrasten", Checked: true, Shortcut: "N"},
				Sep(),
				{Label: "Löschen", Shortcut: "Entf"},
			})
			crop(t, name("context-zoom"), h.c(), image.Rect(280, 180, 640, 440), 2)
			CloseContextMenu()

			// the palette
			p := showPalette(h.w, h.reg)
			remember("clip.split")
			remember("file.save")
			p.filter()
			crop(t, name("palette-zoom"), h.c(), image.Rect(120, 40, 780, 500), 1)
			p.entry.SetText("ab")
			crop(t, name("palette-search-zoom"), h.c(), image.Rect(120, 40, 780, 500), 1)
			p.close()
		})
	}
}
