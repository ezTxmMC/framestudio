package color

import (
	"image"
	"math"
	"testing"
)

// testFrame is a sky-to-ground gradient with a saturated red block and a skin patch.
func testFrame(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			v := uint8(x * 255 / w)
			r, g, b := v/2+40, uint8(y*200/h)+20, 255-v
			switch {
			case x > w*3/4 && y < h/3:
				r, g, b = 220, 30, 30
			case x < w/4 && y > h*2/3:
				r, g, b = 224, 172, 140
			}
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = r, g, b, 255
		}
	}
	return img
}

// lit counts pixels clearly brighter than the scope background.
func lit(img *image.NRGBA) int {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if int(img.Pix[i])+int(img.Pix[i+1])+int(img.Pix[i+2]) > 3*120 {
			n++
		}
	}
	return n
}

func TestHistogramImage(t *testing.T) {
	var r, g, b [256]int
	for i := range 256 {
		r[i] = int(1000 * math.Exp(-math.Pow(float64(i-80)/30, 2)))
		g[i] = int(800 * math.Exp(-math.Pow(float64(i-128)/40, 2)))
		b[i] = 300
	}
	img := histogramImage(r, g, b, scopeW, scopeH)
	if img.Rect.Dx() != scopeW || img.Rect.Dy() != scopeH {
		t.Fatalf("size %v", img.Rect)
	}
	if lit(img) == 0 {
		t.Fatal("histogram is empty")
	}
	// where red and green overlap near the bottom all of R and G are bright
	o := img.PixOffset(100*scopeW/256, scopeH-4)
	if img.Pix[o] < 150 || img.Pix[o+1] < 150 {
		t.Errorf("overlap pixel %v should be additive", img.Pix[o:o+3])
	}
	empty := histogramImage([256]int{}, [256]int{}, [256]int{}, 64, 32)
	if lit(empty) != 0 {
		t.Error("empty histogram should have no trace")
	}
}

func TestWaveformImage(t *testing.T) {
	img := waveformImage(testFrame(1920, 1080), scopeW, scopeH)
	if img.Rect.Dx() != scopeW || img.Rect.Dy() != scopeH {
		t.Fatalf("size %v", img.Rect)
	}
	if lit(img) < 500 {
		t.Fatalf("waveform has only %d lit pixels", lit(img))
	}
	// a pure white frame puts all trace on the 100 % line
	white := image.NewNRGBA(image.Rect(0, 0, 64, 36))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	w := waveformImage(white, 128, 100)
	y := waveY(1, 100)
	if o := w.PixOffset(60, y); w.Pix[o] < 180 {
		t.Errorf("white frame trace missing at row %d: %v", y, w.Pix[o:o+3])
	}
	if o := w.PixOffset(60, waveY(0.5, 100)); w.Pix[o] > 100 {
		t.Error("white frame should not reach mid grey")
	}
	if lit(waveformImage(nil, 32, 32)) != 0 {
		t.Error("nil frame should give an empty graticule")
	}
}

func TestVectorscopeImage(t *testing.T) {
	img := vectorscopeImage(testFrame(800, 450), vectorSize)
	if img.Rect.Dx() != vectorSize || img.Rect.Dy() != vectorSize {
		t.Fatalf("size %v", img.Rect)
	}
	if lit(img) < 100 {
		t.Fatalf("vectorscope has only %d lit pixels", lit(img))
	}
	// neutral grey sits in the centre
	grey := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for i := range grey.Pix {
		grey.Pix[i] = 128
	}
	g := vectorscopeImage(grey, 101)
	if o := g.PixOffset(50, 50); g.Pix[o] < 180 {
		t.Errorf("grey should plot in the centre, got %v", g.Pix[o:o+3])
	}
	if cb, cr := chroma(1, 0, 0); cb >= 0 || cr < 0.49 {
		t.Errorf("red chroma = %v, %v", cb, cr)
	}
}

func TestComputeScopeDimensions(t *testing.T) {
	f := testFrame(160, 90)
	for k, want := range map[scopeKind][2]int{
		scopeHistogram:   {scopeW, scopeH},
		scopeWaveform:    {scopeW, scopeH},
		scopeVectorscope: {vectorSize, vectorSize},
	} {
		img := computeScope(k, f)
		if img.Rect.Dx() != want[0] || img.Rect.Dy() != want[1] {
			t.Errorf("%s: %v", scopeNames[k], img.Rect)
		}
	}
}

func TestScopeRunnerDropsStaleFrames(t *testing.T) {
	var queued []func()
	var shown []*image.NRGBA
	r := &scopeRunner{
		async: func(f func()) { queued = append(queued, f) },
		show:  func(_ scopeKind, img *image.NRGBA) { shown = append(shown, img) },
	}
	frames := []*image.NRGBA{testFrame(8, 8), testFrame(16, 16), testFrame(32, 32)}
	for _, f := range frames {
		r.submit(f, scopeWaveform)
	}
	if len(queued) != 1 {
		t.Fatalf("%d workers started, want 1", len(queued))
	}
	queued[0]()
	// the worker computes the newest frame and stops; the middle one is never seen
	if len(shown) != 1 || r.busy {
		t.Fatalf("shown %d scopes, busy %v", len(shown), r.busy)
	}
	r.close()
	r.submit(frames[0], scopeWaveform)
	if len(queued) != 1 {
		t.Error("closed runner must not start work")
	}
}
