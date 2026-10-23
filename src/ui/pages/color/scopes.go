package color

import (
	"image"
	"image/color"
	"math"
	"sync"
	"time"

	"framestudio-proto/src/render"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/workgate"
)

// scopeKind selects which scope the scopes panel shows.
type scopeKind int

const (
	scopeHistogram scopeKind = iota
	scopeWaveform
	scopeVectorscope
)

var scopeNames = []string{"Histogramm", "Waveform", "Vektorskop"}

// Output sizes of the scope images. The panel scales them to fit.
const (
	scopeW      = 512
	scopeH      = 280
	vectorSize  = 300
	maxSamples  = 160_000 // source pixels examined per scope, larger frames are subsampled
	skinToneDeg = 123.0   // angle of the skin tone line, counter-clockwise from +Cb
)

var (
	scopeBG       = color.NRGBA{0x0E, 0x0F, 0x11, 0xFF}
	scopeGrid     = color.NRGBA{0x2A, 0x2C, 0x32, 0xFF}
	scopeGridMain = color.NRGBA{0x3A, 0x3D, 0x45, 0xFF}
	scopeTrace    = [3]float64{0xC8, 0xE6, 0xD2} // pale green-white like a hardware scope
	skinLine      = color.NRGBA{0x9C, 0x7A, 0x5E, 0xFF}
)

// computeScope renders the scope of the given kind for frame f.
func computeScope(k scopeKind, f *image.NRGBA) *image.NRGBA {
	switch k {
	case scopeWaveform:
		return waveformImage(f, scopeW, scopeH)
	case scopeVectorscope:
		return vectorscopeImage(f, vectorSize)
	default:
		r, g, b, _ := render.Histogram(f)
		return histogramImage(r, g, b, scopeW, scopeH)
	}
}

func newScopeImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = scopeBG.R, scopeBG.G, scopeBG.B, 0xFF
	}
	return img
}

func hline(img *image.NRGBA, y int, c color.NRGBA) {
	for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
		img.SetNRGBA(x, y, c)
	}
}

func vline(img *image.NRGBA, x int, c color.NRGBA) {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		img.SetNRGBA(x, y, c)
	}
}

// sampleStep returns the pixel stride that keeps a w x h frame below maxSamples.
func sampleStep(w, h int) int {
	n := w * h
	if n <= maxSamples {
		return 1
	}
	return int(math.Ceil(math.Sqrt(float64(n) / maxSamples)))
}

// histogramImage draws the R, G and B histograms overlaid with additive colours:
// where channels overlap the trace turns white.
func histogramImage(r, g, b [256]int, w, h int) *image.NRGBA {
	img := newScopeImage(w, h)
	for i := 1; i < 4; i++ {
		vline(img, i*(w-1)/4, scopeGrid)
	}
	hline(img, h/2, scopeGrid)

	// Scale to the largest bin, ignoring the clipped extremes so a black border
	// does not flatten everything else.
	peak := 1
	for _, ch := range [][256]int{r, g, b} {
		for i := 1; i < 255; i++ {
			peak = max(peak, ch[i])
		}
	}
	// square-root scaling keeps small populations visible next to large flat areas
	sp := math.Sqrt(float64(peak))
	heights := func(ch [256]int) []float64 {
		out := make([]float64, w)
		for x := range out {
			v := float64(ch[x*255/max(1, w-1)])
			out[x] = math.Min(1, math.Sqrt(v)/sp) * float64(h-8)
		}
		return out
	}
	hr, hg, hb := heights(r), heights(g), heights(b)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			top := float64(h - 1 - y)
			var cr, cg, cb float64
			add := func(height float64, c [3]float64) {
				switch {
				case height <= 0:
				case top < height-1.5:
					cr, cg, cb = cr+c[0]*0.38, cg+c[1]*0.38, cb+c[2]*0.38
				case top < height+0.5: // brighter edge line
					cr, cg, cb = cr+c[0], cg+c[1], cb+c[2]
				}
			}
			add(hr[x], [3]float64{230, 70, 70})
			add(hg[x], [3]float64{70, 210, 90})
			add(hb[x], [3]float64{80, 120, 245})
			if cr+cg+cb == 0 {
				continue
			}
			o := img.PixOffset(x, y)
			img.Pix[o] = clampByte(float64(img.Pix[o]) + cr)
			img.Pix[o+1] = clampByte(float64(img.Pix[o+1]) + cg)
			img.Pix[o+2] = clampByte(float64(img.Pix[o+2]) + cb)
		}
	}
	return img
}

// waveformImage draws a luma waveform: x follows the image column, y the Rec.709 luma,
// brightness the number of pixels with that luma in the column.
func waveformImage(src *image.NRGBA, w, h int) *image.NRGBA {
	img := newScopeImage(w, h)
	for i := 0; i <= 4; i++ {
		c := scopeGrid
		if i == 0 || i == 4 {
			c = scopeGridMain
		}
		hline(img, waveY(float64(i)/4, h), c)
	}
	if src == nil || src.Rect.Empty() {
		return img
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	// One sampled source column per output column (or fewer for narrow frames), rows
	// subsampled to stay within maxSamples, so every output column gets equal weight.
	cols := min(sw, w)
	ystep := max(1, sh*cols/maxSamples)
	perCol := float64((sh + ystep - 1) / ystep)
	norm := math.Log1p(perCol * 0.08)
	counts := make([]int, h)
	for i := 0; i < cols; i++ {
		clear(counts)
		sx := src.Rect.Min.X + i*sw/cols
		for sy := 0; sy < sh; sy += ystep {
			o := src.PixOffset(sx, src.Rect.Min.Y+sy)
			counts[waveY(luma(src.Pix[o], src.Pix[o+1], src.Pix[o+2]), h)]++
		}
		for y, c := range counts {
			if c == 0 {
				continue
			}
			k := math.Min(1, 0.2+math.Log1p(float64(c))/norm)
			for x := i * w / cols; x < (i+1)*w/cols; x++ {
				plot(img, x, y, k)
			}
		}
	}
	return img
}

// waveY maps luma 0..1 to an image row, leaving a small margin top and bottom.
func waveY(l float64, h int) int {
	m := float64(h) * 0.04
	y := int(math.Round(float64(h-1) - m - l*(float64(h-1)-2*m)))
	return min(max(y, 0), h-1)
}

// vectorscopeImage plots Cb (x) against Cr (y, up) as a density scatter with a graticule:
// outer circle, cross, 75 % colour targets and the skin tone line.
func vectorscopeImage(src *image.NRGBA, size int) *image.NRGBA {
	img := newScopeImage(size, size)
	c := float64(size-1) / 2
	scale := float64(size) * 0.88 // Cb/Cr of ±0.5 reach close to the edge
	toXY := func(cb, cr float64) (float64, float64) { return c + cb*scale, c - cr*scale }

	circle(img, c, c, scale*0.5, scopeGridMain)
	circle(img, c, c, scale*0.5*0.75, scopeGrid)
	for i := 0; i < size; i++ {
		img.SetNRGBA(i, int(c), scopeGrid)
		img.SetNRGBA(int(c), i, scopeGrid)
	}
	a := skinToneDeg * math.Pi / 180
	line(img, c, c, c+math.Cos(a)*scale*0.5, c-math.Sin(a)*scale*0.5, skinLine)
	for _, t := range []struct {
		r, g, b float64
		c       color.NRGBA
	}{
		{0.75, 0, 0, color.NRGBA{0xC0, 0x50, 0x50, 0xFF}},
		{0.75, 0.75, 0, color.NRGBA{0xB0, 0xA0, 0x40, 0xFF}},
		{0, 0.75, 0, color.NRGBA{0x50, 0xB0, 0x60, 0xFF}},
		{0, 0.75, 0.75, color.NRGBA{0x40, 0xA0, 0xB0, 0xFF}},
		{0, 0, 0.75, color.NRGBA{0x60, 0x70, 0xD0, 0xFF}},
		{0.75, 0, 0.75, color.NRGBA{0xB0, 0x50, 0xB0, 0xFF}},
	} {
		cb, cr := chroma(t.r, t.g, t.b)
		x, y := toXY(cb, cr)
		box(img, int(math.Round(x)), int(math.Round(y)), 5, t.c)
	}
	if src == nil || src.Rect.Empty() {
		return img
	}

	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	step := sampleStep(sw, sh)
	counts := make([]int, size*size)
	n := 0
	for sy := 0; sy < sh; sy += step {
		for sx := 0; sx < sw; sx += step {
			o := src.PixOffset(src.Rect.Min.X+sx, src.Rect.Min.Y+sy)
			cb, cr := chroma(float64(src.Pix[o])/255, float64(src.Pix[o+1])/255, float64(src.Pix[o+2])/255)
			x, y := toXY(cb, cr)
			xi, yi := int(math.Round(x)), int(math.Round(y))
			if xi < 0 || yi < 0 || xi >= size || yi >= size {
				continue
			}
			counts[yi*size+xi]++
			n++
		}
	}
	norm := math.Log1p(float64(n) * 0.02)
	for i, k := range counts {
		if k > 0 {
			plot(img, i%size, i/size, math.Min(1, 0.25+math.Log1p(float64(k))/norm))
		}
	}
	return img
}

// luma is Rec.709 luma of 8-bit RGB in 0..1.
func luma(r, g, b uint8) float64 {
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
}

// chroma returns Rec.709 Cb and Cr (each -0.5..0.5) of RGB in 0..1.
func chroma(r, g, b float64) (cb, cr float64) {
	y := 0.2126*r + 0.7152*g + 0.0722*b
	return (b - y) / 1.8556, (r - y) / 1.5748
}

// plot blends the scope trace colour over the pixel with intensity k (0..1).
func plot(img *image.NRGBA, x, y int, k float64) {
	o := img.PixOffset(x, y)
	for i := 0; i < 3; i++ {
		img.Pix[o+i] = clampByte(float64(img.Pix[o+i])*(1-k) + scopeTrace[i]*k)
	}
}

func circle(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	n := int(r*2*math.Pi) + 8
	for i := 0; i < n; i++ {
		a := float64(i) / float64(n) * 2 * math.Pi
		img.SetNRGBA(int(math.Round(cx+math.Cos(a)*r)), int(math.Round(cy+math.Sin(a)*r)), c)
	}
}

func line(img *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		img.SetNRGBA(int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), c)
	}
}

// box draws a small square outline centred on x, y.
func box(img *image.NRGBA, x, y, s int, c color.NRGBA) {
	for i := -s; i <= s; i++ {
		img.SetNRGBA(x+i, y-s, c)
		img.SetNRGBA(x+i, y+s, c)
		img.SetNRGBA(x-s, y+i, c)
		img.SetNRGBA(x+s, y+i, c)
	}
}

func clampByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}

// scopeRunner computes scopes off the UI thread. Only the newest frame is kept: frames
// that arrive while a computation runs replace each other and the stale ones are dropped.
type scopeRunner struct {
	async func(func())                        // starts work in the background
	show  func(k scopeKind, img *image.NRGBA) // called on the UI thread

	// pace overrides scopeInterval (tests); sleep overrides time.Sleep.
	pace  func() time.Duration
	sleep func(time.Duration)

	mu      sync.Mutex
	frame   *image.NRGBA
	kind    scopeKind
	gen     int
	busy    bool
	closed  bool
	lastRun time.Time
}

// Scope rates: scopes never compete with the preview. While playing or scrubbing they
// run at most 10 times a second; when the UI thread is saturated 2 times; at rest at
// full speed.
const (
	scopePlayInterval = 100 * time.Millisecond
	scopeBusyInterval = 500 * time.Millisecond
)

// scopeInterval is the minimum time between two scope computations right now.
func scopeInterval(st workgate.State, uiBusy bool) time.Duration {
	switch {
	case uiBusy && st != workgate.Idle:
		return scopeBusyInterval
	case st != workgate.Idle:
		return scopePlayInterval
	}
	return 0
}

func (s *scopeRunner) interval() time.Duration {
	if s.pace != nil {
		return s.pace()
	}
	return scopeInterval(workgate.Default().State(), workgate.UIBusy())
}

// wait holds back the next computation until the interval since the last one passed.
// The newest frame is read after the wait, so frames in between are dropped.
func (s *scopeRunner) wait() {
	s.mu.Lock()
	left := s.interval() - time.Since(s.lastRun)
	s.mu.Unlock()
	if left <= 0 {
		return
	}
	if s.sleep != nil {
		s.sleep(left)
	} else {
		time.Sleep(left)
	}
}

// submit queues frame f for scope kind k. Call on the UI thread.
func (s *scopeRunner) submit(f *image.NRGBA, k scopeKind) {
	s.mu.Lock()
	if s.closed || f == nil {
		s.mu.Unlock()
		return
	}
	s.frame, s.kind = f, k
	s.gen++
	if s.busy {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	s.async(s.loop)
}

func (s *scopeRunner) loop() {
	for {
		s.wait()
		s.mu.Lock()
		if s.closed {
			s.busy = false
			s.mu.Unlock()
			return
		}
		f, k, gen := s.frame, s.kind, s.gen
		s.lastRun = time.Now()
		s.mu.Unlock()

		out := computeScope(k, f)
		kit.Do(func() {
			s.mu.Lock()
			ok := !s.closed && s.kind == k
			s.mu.Unlock()
			if ok {
				s.show(k, out)
			}
		})

		s.mu.Lock()
		if s.gen == gen || s.closed {
			s.busy = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// latest returns the most recent frame, for recomputing when the scope kind changes.
func (s *scopeRunner) latest() *image.NRGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

func (s *scopeRunner) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
