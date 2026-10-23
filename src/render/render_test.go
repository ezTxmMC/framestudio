package render

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// fakeSource serves stills from PNG files and synthetic video frames whose red channel
// encodes the requested frame number.
type fakeSource struct {
	mu    sync.Mutex
	last  int
	fail  bool
	video func(n, w, h int) image.Image
}

func (s *fakeSource) VideoFrame(m model.MediaItem, n int, fps model.Rate, w, h int) (image.Image, error) {
	s.mu.Lock()
	s.last = n
	s.mu.Unlock()
	if s.fail {
		return nil, errors.New("kaputt")
	}
	if s.video != nil {
		return s.video(n, w, h), nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fillRect(img, img.Rect, color.NRGBA{uint8(n), 0, 0, 255})
	return img, nil
}

func (s *fakeSource) Image(m model.MediaItem, maxW, maxH int) (image.Image, error) {
	if s.fail {
		return nil, errors.New("kaputt")
	}
	f, err := os.Open(m.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

var (
	red   = rgba(255, 0, 0, 255)
	green = rgba(0, 255, 0, 255)
	blue  = rgba(0, 0, 255, 255)
	white = rgba(255, 255, 255, 255)
)

// newProject returns a 1920x1080 project with a black background and video tracks V1, V2.
func newProject(t *testing.T) *model.Project {
	t.Helper()
	return model.NewProject("Test")
}

func place(t *testing.T, p *model.Project, track int, c model.Clip) {
	t.Helper()
	if err := p.PlaceClip(p.Tracks[track].ID, c); err != nil {
		t.Fatal(err)
	}
}

func colorClip(c model.RGBA, start, length int) model.Clip {
	cl := model.NewClip(model.KindColor, "Farbe", start, length)
	cl.Color = c
	return cl
}

func px(img *image.NRGBA, x, y int) color.NRGBA { return img.NRGBAAt(x, y) }

func near(a, b uint8, tol int) bool { return math.Abs(float64(a)-float64(b)) <= float64(tol) }

func assertPx(t *testing.T, img *image.NRGBA, x, y int, want model.RGBA, tol int) {
	t.Helper()
	got := px(img, x, y)
	if !near(got.R, want.R, tol) || !near(got.G, want.G, tol) || !near(got.B, want.B, tol) || !near(got.A, want.A, tol) {
		t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
	}
}

func TestEmptyAndDegenerate(t *testing.T) {
	r := &Renderer{}
	if img := r.Frame(nil, 0, 10, 10); img == nil || img.Rect.Dx() != 10 {
		t.Fatal("nil project must give a blank frame")
	}
	if img := r.Frame(newProject(t), 0, 0, -3); img == nil {
		t.Fatal("zero size must not return nil")
	}
	p := newProject(t)
	p.Settings.Background = rgba(10, 20, 30, 255)
	img := r.Frame(p, 0, 64, 36)
	for _, pt := range []image.Point{{0, 0}, {63, 35}, {32, 18}} {
		assertPx(t, img, pt.X, pt.Y, p.Settings.Background, 0)
	}
}

func TestColorClipAndTrackOrder(t *testing.T) {
	p := newProject(t)
	place(t, p, 0, colorClip(red, 0, 50))
	place(t, p, 1, colorClip(blue, 10, 20))
	r := &Renderer{}
	assertPx(t, r.Frame(p, 5, 96, 54), 48, 27, red, 0)
	assertPx(t, r.Frame(p, 15, 96, 54), 48, 27, blue, 0) // top track wins
	assertPx(t, r.Frame(p, 60, 96, 54), 48, 27, rgba(0, 0, 0, 255), 0)

	p.Tracks[1].Hidden = true
	assertPx(t, r.Frame(p, 15, 96, 54), 48, 27, red, 0)

	// Audio tracks never composite.
	a := model.NewClip(model.KindAudio, "Ton", 0, 50)
	place(t, p, 2, a)
	assertPx(t, r.Frame(p, 15, 96, 54), 48, 27, red, 0)
}

func TestOpacityAndFades(t *testing.T) {
	p := newProject(t)
	c := colorClip(white, 0, 100)
	c.Opacity = 0.5
	place(t, p, 0, c)
	r := &Renderer{}
	assertPx(t, r.Frame(p, 50, 32, 18), 0, 0, rgba(128, 128, 128, 255), 1)

	c.Opacity, c.FadeIn, c.FadeOut = 1, 10, 10
	if err := p.UpdateClip(c); err != nil {
		t.Fatal(err)
	}
	assertPx(t, r.Frame(p, 0, 32, 18), 0, 0, rgba(0, 0, 0, 255), 0)
	assertPx(t, r.Frame(p, 5, 32, 18), 0, 0, rgba(128, 128, 128, 255), 1)
	assertPx(t, r.Frame(p, 50, 32, 18), 0, 0, white, 0)
	assertPx(t, r.Frame(p, 99, 32, 18), 0, 0, rgba(0, 0, 0, 255), 0)
}

func TestTransparentBackgroundCompositing(t *testing.T) {
	p := newProject(t)
	p.Settings.Background = model.RGBA{}
	c := colorClip(rgba(200, 100, 50, 128), 0, 10)
	place(t, p, 0, c)
	img := (&Renderer{}).Frame(p, 0, 8, 8)
	assertPx(t, img, 4, 4, rgba(200, 100, 50, 128), 0)

	// Two half-transparent layers: alpha = 0.5 + 0.5*0.5.
	place(t, p, 1, colorClip(rgba(0, 0, 255, 128), 0, 10))
	img = (&Renderer{}).Frame(p, 0, 8, 8)
	got := px(img, 4, 4)
	if !near(got.A, 192, 1) || !near(got.B, 187, 2) || !near(got.R, 67, 2) {
		t.Errorf("over result %v", got)
	}
}

// writeTestPNG writes a 200x100 image: a red stripe in the leftmost 10 %, then green,
// and blue in the right half.
func writeTestPNG(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	fillRect(img, image.Rect(0, 0, 20, 100), red.NRGBA())
	fillRect(img, image.Rect(20, 0, 100, 100), green.NRGBA())
	fillRect(img, image.Rect(100, 0, 200, 100), blue.NRGBA())
	path := filepath.Join(t.TempDir(), "still.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageFitModes(t *testing.T) {
	path := writeTestPNG(t)
	src := &fakeSource{}
	r := &Renderer{Video: src}
	black := rgba(0, 0, 0, 255)
	for _, tc := range []struct {
		fit    model.Fit
		checks map[image.Point]model.RGBA
	}{
		// 200x100 in 160x90: contain gives 160x80 with 5 px bars.
		{model.FitContain, map[image.Point]model.RGBA{{80, 1}: black, {80, 88}: black, {2, 45}: red, {60, 45}: green, {150, 45}: blue}},
		// cover gives 180x90 shifted 10 px left: the red stripe shrinks to 8 px.
		{model.FitCover, map[image.Point]model.RGBA{{50, 1}: green, {2, 45}: red, {12, 45}: green, {159, 1}: blue}},
		// stretch keeps the stripe at 16 px.
		{model.FitStretch, map[image.Point]model.RGBA{{50, 1}: green, {12, 45}: red, {159, 89}: blue}},
	} {
		p := newProject(t)
		id := p.AddMedia(model.MediaItem{Name: "still.png", Path: path, Kind: model.KindImage, Width: 200, Height: 100})
		m, _ := p.MediaByID(id)
		c := p.ClipFromMedia(*m, 0, 50)
		c.Fit = tc.fit
		place(t, p, 0, c)
		img := r.Frame(p, 10, 160, 90)
		for pt, want := range tc.checks {
			got := px(img, pt.X, pt.Y)
			if !near(got.R, want.R, 2) || !near(got.G, want.G, 2) || !near(got.B, want.B, 2) {
				t.Errorf("%s: pixel %v = %v, want %v", tc.fit, pt, got, want)
			}
		}
	}
}

func TestVideoSourceFrame(t *testing.T) {
	p := newProject(t)
	id := p.AddMedia(model.MediaItem{Name: "clip.mp4", Path: "/nirgends/clip.mp4", Kind: model.KindVideo, Width: 1920, Height: 1080})
	c := model.NewClip(model.KindVideo, "clip", 5, 40)
	c.MediaID, c.SourceIn = id, 10
	place(t, p, 0, c)

	def, over := &fakeSource{}, &fakeSource{}
	r := &Renderer{Video: def}
	img := r.Frame(p, 8, 64, 36)
	if def.last != 13 {
		t.Fatalf("source frame %d, want 13", def.last)
	}
	assertPx(t, img, 32, 18, rgba(13, 0, 0, 255), 0)

	r.FrameWith(p, 20, 64, 36, Options{Video: over})
	if over.last != 25 || def.last != 13 {
		t.Fatalf("Options.Video must override: over=%d def=%d", over.last, def.last)
	}
}

func TestOfflinePlaceholder(t *testing.T) {
	isPlaceholder := func(img *image.NRGBA) bool {
		seen := map[color.NRGBA]bool{}
		for y := 0; y < 20; y++ {
			for x := 0; x < 20; x++ {
				seen[px(img, x, y)] = true
			}
		}
		return seen[offlineBase] && seen[offlineStripe]
	}
	p := newProject(t)
	missing := model.NewClip(model.KindVideo, "fehlt.mp4", 0, 10)
	missing.MediaID = "gibt-es-nicht"
	place(t, p, 0, missing)

	id := p.AddMedia(model.MediaItem{Name: "a.mp4", Path: "/x/a.mp4", Kind: model.KindVideo})
	broken := model.NewClip(model.KindVideo, "a.mp4", 10, 10)
	broken.MediaID = id
	place(t, p, 0, broken)

	still := p.AddMedia(model.MediaItem{Name: "b.png", Path: "/x/b.png", Kind: model.KindImage})
	img := model.NewClip(model.KindImage, "b.png", 20, 10)
	img.MediaID = still
	img.Grade.Exposure = 1 // placeholder stays readable: no grade
	place(t, p, 0, img)

	for name, r := range map[string]*Renderer{
		"zero":    {},
		"nilNew":  New(nil),
		"stub":    New(media.NewCache()),
		"failing": {Video: &fakeSource{fail: true}},
		"panics":  {Video: &fakeSource{video: func(int, int, int) image.Image { panic("decoder") }}},
	} {
		for _, f := range []int{0, 15, 25} {
			if out := r.Frame(p, f, 320, 180); !isPlaceholder(out) {
				t.Errorf("%s: frame %d shows no placeholder", name, f)
			}
		}
	}
}

// brightRect returns the bounding box of pixels brighter than the threshold.
func brightRect(img *image.NRGBA, thr uint8) image.Rectangle {
	var r image.Rectangle
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if px(img, x, y).G > thr {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

func TestTitle(t *testing.T) {
	p := newProject(t)
	c := model.NewClip(model.KindTitle, "Titel", 0, 50)
	c.Title.Text = "Frame Studio\nzweite Zeile"
	c.Title.Y = 0.3
	place(t, p, 0, c)
	r := &Renderer{}
	img := r.Frame(p, 0, 400, 225)
	b := brightRect(img, 128)
	if b.Empty() {
		t.Fatal("title drew nothing")
	}
	cy := float64(b.Min.Y+b.Max.Y) / 2
	if math.Abs(cy-0.3*225) > 8 {
		t.Errorf("title centred at y=%.1f, want ~%.1f (%v)", cy, 0.3*225, b)
	}
	if lines := float64(b.Dy()) / (0.1 * 225); lines < 1.5 {
		t.Errorf("expected two lines, block height %d", b.Dy())
	}
	if math.Abs(float64(b.Min.X+b.Max.X)/2-200) > 6 {
		t.Errorf("centred title spans %v", b)
	}

	c.Title.Align = model.AlignLeft
	c.Title.Background = rgba(0, 0, 200, 255)
	_ = p.UpdateClip(c)
	img = r.Frame(p, 0, 400, 225)
	b = brightRect(img, 128)
	if b.Min.X < 18 || b.Min.X > 26 {
		t.Errorf("left aligned title starts at x=%d, want ~20 (5%% margin)", b.Min.X)
	}
	if got := px(img, b.Min.X-2, 67); got.B != 200 {
		t.Errorf("background box missing left of the text: %v", got)
	}

	c.Title.Align = model.AlignRight
	_ = p.UpdateClip(c)
	if b = brightRect(r.Frame(p, 0, 400, 225), 128); b.Max.X < 372 || b.Max.X > 382 {
		t.Errorf("right aligned title ends at x=%d, want ~380", b.Max.X)
	}
}

// testCard returns a colourful gradient image.
func testCard(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8(255 - x*255/w), 255})
		}
	}
	return img
}

func mean(img *image.NRGBA) (r, g, b float64) {
	n := float64(img.Rect.Dx() * img.Rect.Dy())
	for i := 0; i < len(img.Pix); i += 4 {
		r += float64(img.Pix[i])
		g += float64(img.Pix[i+1])
		b += float64(img.Pix[i+2])
	}
	return r / n, g / n, b / n
}

func graded(g model.Grade) *image.NRGBA {
	img := testCard(64, 36)
	ApplyGrade(img, g)
	return img
}

func TestGrade(t *testing.T) {
	base := graded(model.Grade{})
	if !equalImg(base, testCard(64, 36)) {
		t.Error("neutral grade changed the image")
	}
	br, bg, bb := mean(base)
	lum := func(img *image.NRGBA) float64 { r, g, b := mean(img); return r + g + b }

	prev := lum(graded(model.Grade{Exposure: -1}))
	for _, e := range []float64{-0.5, 0, 0.25, 0.5} {
		l := lum(graded(model.Grade{Exposure: e}))
		if l <= prev {
			t.Errorf("exposure %.2f: brightness %.1f not above %.1f", e, l, prev)
		}
		prev = l
	}

	mono := graded(model.Grade{Saturation: -1})
	for i := 0; i < len(mono.Pix); i += 4 {
		if mono.Pix[i] != mono.Pix[i+1] || mono.Pix[i+1] != mono.Pix[i+2] {
			t.Fatalf("saturation -1 left colour at %d: %v", i/4, mono.Pix[i:i+3])
		}
	}

	warm := graded(model.Grade{Temperature: 0.8})
	if r, _, b := mean(warm); r <= br || b >= bb {
		t.Errorf("warm grade: R %.1f->%.1f, B %.1f->%.1f", br, r, bb, b)
	}
	if _, g, _ := mean(graded(model.Grade{Tint: 0.8})); g >= bg {
		t.Errorf("magenta tint should lower green: %.1f -> %.1f", bg, g)
	}

	spread := func(img *image.NRGBA) int {
		_, _, _, l := Histogram(img)
		lo, hi := 255, 0
		for i, n := range l {
			if n > 0 {
				lo, hi = min(lo, i), max(hi, i)
			}
		}
		return hi - lo
	}
	if spread(graded(model.Grade{Contrast: -0.8})) >= spread(base) {
		t.Error("negative contrast should narrow the luma range")
	}

	flat := image.NewNRGBA(image.Rect(0, 0, 64, 36))
	fillRect(flat, flat.Rect, color.NRGBA{200, 200, 200, 255})
	ApplyGrade(flat, model.Grade{Vignette: 1})
	if c, e := px(flat, 32, 18), px(flat, 0, 0); c.R != 200 || e.R > 60 {
		t.Errorf("vignette centre %v corner %v", c, e)
	}
	ApplyGrade(nil, model.Grade{Exposure: 1})
}

func equalImg(a, b *image.NRGBA) bool {
	if a.Rect != b.Rect {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

func TestGradeInFrameAndSkip(t *testing.T) {
	p := newProject(t)
	c := colorClip(rgba(100, 100, 100, 255), 0, 10)
	c.Grade.Exposure = 0.5
	place(t, p, 0, c)
	r := &Renderer{}
	if got := px(r.Frame(p, 0, 16, 9), 8, 4); got.R <= 100 {
		t.Errorf("graded clip not brighter: %v", got)
	}
	assertPx(t, r.FrameWith(p, 0, 16, 9, Options{SkipGrade: true}), 8, 4, c.Color, 0)
}

func TestHistogram(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	fillRect(img, image.Rect(0, 0, 10, 5), color.NRGBA{255, 0, 0, 255})
	fillRect(img, image.Rect(0, 5, 10, 10), color.NRGBA{255, 255, 255, 255})
	r, g, b, l := Histogram(img)
	if r[255] != 100 || g[0] != 50 || g[255] != 50 || b[0] != 50 {
		t.Errorf("channel bins r255=%d g0=%d g255=%d b0=%d", r[255], g[0], g[255], b[0])
	}
	if l[255] != 50 || l[54] != 50 { // Rec.709: 0.2126*255 = 54.2
		t.Errorf("luma bins l255=%d l54=%d", l[255], l[54])
	}
	Histogram(nil)
}

func TestConcurrentFrames(t *testing.T) {
	p := richProject(t)
	r := &Renderer{Video: &fakeSource{video: func(n, w, h int) image.Image { return testCard(w, h) }}}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Frame(p, 10+i, 320+i*16, 180+i*9)
		}()
	}
	wg.Wait()
}

// richProject has a graded video on V1, a translucent colour wash on V2 and a lower
// third title on V3.
func richProject(t testing.TB) *model.Project {
	p := model.NewProject("Demo")
	p.AddTrack(model.TrackVideo)
	id := p.AddMedia(model.MediaItem{Name: "a001.mov", Path: "/x/a001.mov", Kind: model.KindVideo, Width: 1920, Height: 1080})
	v := model.NewClip(model.KindVideo, "a001.mov", 0, 100)
	v.MediaID = id
	v.Grade = model.Grade{Exposure: 0.1, Contrast: 0.2, Saturation: 0.15, Temperature: 0.2, Vignette: 0.6}
	wash := colorClip(rgba(0x20, 0x40, 0x80, 255), 0, 100)
	wash.Opacity = 0.2
	title := model.NewClip(model.KindTitle, "Titel", 0, 100)
	title.Title = model.Title{Text: "Frame Studio\nRendering-Test", Size: 0.08, Color: white, Bold: true,
		Align: model.AlignLeft, Y: 0.78, Background: rgba(0x10, 0x11, 0x14, 0xC0)}
	for i, c := range []model.Clip{v, wash, title} {
		if err := p.PlaceClip(p.Tracks[i].ID, c); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestWriteRichFrame(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-render.png")
	}
	p := richProject(t)
	// A second, offline clip on the right half shows the placeholder.
	r := &Renderer{Video: &fakeSource{video: func(n, w, h int) image.Image { return testCard(w, h) }}}
	img := r.Frame(p, 10, 960, 540)
	q := p.Clone()
	q.Media = nil
	off := r.Frame(q, 10, 480, 270)
	out := image.NewNRGBA(image.Rect(0, 0, 1440, 540))
	drawScaled(out, img, image.Rect(0, 0, 960, 540))
	drawScaled(out, off, image.Rect(960, 0, 1440, 270))
	tc := testCard(480, 270)
	ApplyGrade(tc, model.Grade{Saturation: -1, Vignette: 1})
	drawScaled(out, tc, image.Rect(960, 270, 1440, 540))
	f, err := os.Create("/tmp/fs-render.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkFrame960(b *testing.B) {
	p := richProject(b)
	frame := testCard(960, 540)
	r := &Renderer{Video: &fakeSource{video: func(int, int, int) image.Image { return frame }}}
	r.Frame(p, 10, 960, 540)
	b.ReportAllocs()
	for b.Loop() {
		r.Frame(p, 10, 960, 540)
	}
}

func rgba(r, g, b, a uint8) model.RGBA { return model.RGBA{R: r, G: g, B: b, A: a} }
