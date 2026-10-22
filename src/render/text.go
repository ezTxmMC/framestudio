package render

import (
	"image"
	"image/color"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"framestudio-proto/src/assets"
	"framestudio-proto/src/model"
)

const maxCached = 64 // entries per text cache before it is reset

var (
	fontsOnce           sync.Once
	fontRegular, fontBd *sfnt.Font

	faceMu sync.Mutex
	faces  = map[faceKey]*lockedFace{}

	maskMu sync.Mutex
	masks  = map[maskKey]*textMask{}
)

type faceKey struct {
	bold bool
	px   int
}

// lockedFace serialises use of a font.Face, which is not safe for concurrent use.
type lockedFace struct {
	sync.Mutex
	font.Face
}

func loadFonts() {
	fontsOnce.Do(func() {
		fontRegular, _ = opentype.Parse(assets.FontRegular)
		fontBd, _ = opentype.Parse(assets.FontBold)
	})
}

// faceFor returns the cached face for the given weight and pixel size, or nil if the
// embedded fonts cannot be parsed.
func faceFor(bold bool, px int) *lockedFace {
	loadFonts()
	k := faceKey{bold, px}
	faceMu.Lock()
	defer faceMu.Unlock()
	if f, ok := faces[k]; ok {
		return f
	}
	ft := fontRegular
	if bold {
		ft = fontBd
	}
	if ft == nil {
		return nil
	}
	face, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	if len(faces) >= maxCached {
		clear(faces)
	}
	f := &lockedFace{Face: face}
	faces[k] = f
	return f
}

type maskKey struct {
	text  string
	bold  bool
	px    int
	align model.Align
	w     int
}

// textMask is the rendered coverage of a text block. Mask x is in frame coordinates,
// y is relative to the top of the block. Immutable once built.
type textMask struct {
	mask       *image.Alpha
	height     int
	minX, maxX int // horizontal extent of the advance boxes
	line       int // line advance
}

// textMaskFor renders (or reuses) the coverage of a multi-line text block laid out in a
// frame of width w: left/right aligned text keeps a 5 % margin.
func textMaskFor(text string, bold bool, px int, align model.Align, w int) *textMask {
	k := maskKey{text, bold, px, align, w}
	maskMu.Lock()
	tm, ok := masks[k]
	maskMu.Unlock()
	if ok {
		return tm
	}
	face := faceFor(bold, px)
	if face == nil {
		return nil
	}
	face.Lock()
	defer face.Unlock()

	lines := strings.Split(text, "\n")
	m := face.Metrics()
	ascent, line := m.Ascent.Ceil(), m.Height.Ceil()
	height := ascent + m.Descent.Ceil() + line*(len(lines)-1)
	margin := int(math.Round(0.05 * float64(w)))
	xs := make([]int, len(lines))
	tm = &textMask{height: height, minX: math.MaxInt, maxX: math.MinInt, line: line}
	for i, s := range lines {
		lw := font.MeasureString(face, s).Ceil()
		switch align {
		case model.AlignLeft:
			xs[i] = margin
		case model.AlignRight:
			xs[i] = w - margin - lw
		default:
			xs[i] = (w - lw) / 2
		}
		if lw > 0 {
			tm.minX, tm.maxX = min(tm.minX, xs[i]), max(tm.maxX, xs[i]+lw)
		}
	}
	if tm.minX > tm.maxX {
		tm.minX, tm.maxX = w/2, w/2
	}
	// Glyphs may overhang their advance box; leave room on both sides.
	pad := px/4 + 1
	r := image.Rect(tm.minX-pad, 0, tm.maxX+pad, height)
	dst := image.NewRGBA(r)
	d := font.Drawer{Dst: dst, Src: image.White, Face: face}
	for i, s := range lines {
		d.Dot = fixed.P(xs[i], ascent+i*line)
		d.DrawString(s)
	}
	tm.mask = image.NewAlpha(r)
	for i := range tm.mask.Pix {
		tm.mask.Pix[i] = dst.Pix[i*4+3]
	}

	maskMu.Lock()
	if len(masks) >= maxCached {
		clear(masks)
	}
	masks[k] = tm
	maskMu.Unlock()
	return tm
}

// paintMask composites colour c through mask, shifted down by dy, onto img.
func paintMask(img *image.NRGBA, mask *image.Alpha, dy int, c color.NRGBA) image.Rectangle {
	r := mask.Rect.Add(image.Pt(0, dy)).Intersect(img.Rect)
	if r.Empty() || c.A == 0 {
		return image.Rectangle{}
	}
	ca := uint32(c.A)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		mrow := mask.Pix[mask.PixOffset(r.Min.X, y-dy):mask.PixOffset(r.Max.X, y-dy)]
		row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		for x, m := range mrow {
			if m == 0 {
				continue
			}
			a := uint32(m)
			if ca != 255 {
				a = (a*ca + 127) / 255
			}
			over(row[x*4:x*4+4:x*4+4], c.R, c.G, c.B, a)
		}
	}
	return r
}

// drawTitle renders a title into the layer and returns the region it touched.
func drawTitle(l *image.NRGBA, t model.Title) image.Rectangle {
	text := strings.TrimRight(strings.ReplaceAll(t.Text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return image.Rectangle{}
	}
	w, h := l.Rect.Dx(), l.Rect.Dy()
	size := t.Size
	if size <= 0 || size != size {
		size = 0.1
	}
	px := max(1, min(2*h, int(math.Round(size*float64(h)))))
	tm := textMaskFor(text, t.Bold, px, t.Align, w)
	if tm == nil {
		return image.Rectangle{}
	}
	top := int(math.Round(t.Y*float64(h) - float64(tm.height)/2))
	var bounds image.Rectangle
	if t.Background.A > 0 {
		padX, padY := int(0.4*float64(px)), int(0.18*float64(px))
		box := image.Rect(tm.minX-padX, top-padY, tm.maxX+padX, top+tm.height+padY).Intersect(l.Rect)
		fillRect(l, box, t.Background.NRGBA())
		bounds = box
	}
	return bounds.Union(paintMask(l, tm.mask, top, t.Color.NRGBA()))
}

var (
	offlineBase   = color.NRGBA{0x1A, 0x1B, 0x1F, 0xFF}
	offlineStripe = color.NRGBA{0x24, 0x25, 0x2A, 0xFF}
	offlineBox    = color.NRGBA{0x10, 0x11, 0x13, 0xE6}
	offlineText   = color.NRGBA{0xDA, 0xDB, 0xD7, 0xFF}
	offlineMuted  = color.NRGBA{0x8B, 0x8E, 0x96, 0xFF}
)

// drawOffline fills the layer with the "Medien offline" placeholder: dark diagonal
// stripes and a label with the clip name.
func drawOffline(l *image.NRGBA, name string) image.Rectangle {
	w, h := l.Rect.Dx(), l.Rect.Dy()
	period := max(4, h/14)
	parallelRows(0, h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := l.Pix[l.PixOffset(0, y):l.PixOffset(w, y)]
			for x := range w {
				c := offlineBase
				if (x+y)%(2*period) < period {
					c = offlineStripe
				}
				i := x * 4
				row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, c.A
			}
		}
	})
	if h < 24 {
		return l.Rect
	}
	head := textMaskFor("Medien offline", true, max(1, h*7/100), model.AlignCenter, w)
	var sub *textMask
	if name = strings.TrimSpace(strings.ReplaceAll(name, "\n", " ")); name != "" {
		sub = textMaskFor(name, false, max(1, h*45/1000), model.AlignCenter, w)
	}
	if head == nil {
		return l.Rect
	}
	gap := head.line / 4
	total := head.height
	minX, maxX := head.minX, head.maxX
	if sub != nil {
		total += gap + sub.height
		minX, maxX = min(minX, sub.minX), max(maxX, sub.maxX)
	}
	top := (h - total) / 2
	pad := head.line / 2
	blendSolid(l, image.Rect(minX-pad, top-pad/2, maxX+pad, top+total+pad/2), offlineBox, 1)
	paintMask(l, head.mask, top, offlineText)
	if sub != nil {
		paintMask(l, sub.mask, top+head.height+gap, offlineMuted)
	}
	return l.Rect
}
