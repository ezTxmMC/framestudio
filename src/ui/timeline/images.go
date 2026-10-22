package timeline

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	xdraw "golang.org/x/image/draw"

	"framestudio-proto/src/model"
)

// mix blends a towards b by t (0..1).
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), l(a.A, b.A)}
}

func alpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// hatchImage draws diagonal stripes of colour c on transparent, spaced every n pixels.
func hatchImage(w, h int, c color.NRGBA, n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x+y)%n < 2 {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return img
}

// waveSpec describes the visible part of an audio clip to draw as a waveform.
type waveSpec struct {
	peaks     []float32
	srcFrames int     // length of the whole media in project frames
	srcStart  float64 // source frame at the left edge of the image
	fpp       float64 // frames per image pixel
	gain      float64
	col       color.NRGBA
}

// waveImage renders mirrored peaks around the centre line into a w x h image.
func waveImage(w, h int, s waveSpec) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	mid := float64(h) / 2
	centre := alpha(s.col, 0x90)
	for x := 0; x < w; x++ {
		img.SetNRGBA(x, int(mid), centre)
	}
	if len(s.peaks) == 0 || s.srcFrames <= 0 {
		return img
	}
	n := float64(len(s.peaks))
	scale := n / float64(s.srcFrames)
	gain := math.Min(math.Max(s.gain, 0), 1.6)
	for x := 0; x < w; x++ {
		f0 := s.srcStart + float64(x)*s.fpp
		b0 := int(f0 * scale)
		b1 := max(b0+1, int((f0+s.fpp)*scale))
		if b0 >= len(s.peaks) || b1 <= 0 {
			continue
		}
		var pk float32
		for b := max(b0, 0); b < min(b1, len(s.peaks)); b++ {
			pk = max(pk, s.peaks[b])
		}
		a := math.Min(float64(pk)*gain, 1) * (mid - 1)
		for y := int(math.Round(mid - a)); y <= int(math.Round(mid+a)); y++ {
			if y >= 0 && y < h {
				img.SetNRGBA(x, y, s.col)
			}
		}
	}
	return img
}

// scaleToHeight resizes src to height h keeping its aspect.
func scaleToHeight(src image.Image, h int) *image.NRGBA {
	b := src.Bounds()
	if b.Dy() == 0 || h <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	w := max(1, int(math.Round(float64(b.Dx())*float64(h)/float64(b.Dy()))))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

// filmstripImage tiles tile horizontally, anchored at clip-local pixel -from so tiles stay
// put while the view scrolls. A one-pixel gap separates frames.
func filmstripImage(w, h int, tile *image.NRGBA, from int, bg color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	xdraw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, xdraw.Src)
	tw := tile.Bounds().Dx() + 1
	if tw <= 1 {
		return img
	}
	for x := -(from % tw); x < w; x += tw {
		r := image.Rect(x, 0, x+tw-1, h)
		xdraw.Draw(img, r, tile, image.Point{}, xdraw.Src)
	}
	return img
}

// iconSVG holds the toolbar glyphs drawn in track headers. %s is the fill colour.
var iconSVG = map[string]string{
	"eye":    `<path d="M12 5C6.5 5 2.5 9.5 1.5 12c1 2.5 5 7 10.5 7s9.5-4.5 10.5-7C21.5 9.5 17.5 5 12 5z" fill="none" stroke="%[1]s" stroke-width="2"/><circle cx="12" cy="12" r="3.2" fill="%[1]s"/>`,
	"eyeoff": `<path d="M12 5C6.5 5 2.5 9.5 1.5 12c1 2.5 5 7 10.5 7s9.5-4.5 10.5-7C21.5 9.5 17.5 5 12 5z" fill="none" stroke="%[1]s" stroke-width="2"/><path d="M3 3l18 18" stroke="%[1]s" stroke-width="2.4"/>`,
	"lock":   `<rect x="4.5" y="10.5" width="15" height="11" rx="2" fill="%[1]s"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3" fill="none" stroke="%[1]s" stroke-width="2.2"/>`,
	"unlock": `<rect x="4.5" y="10.5" width="15" height="11" rx="2" fill="%[1]s"/><path d="M8 10.5V7.5a4 4 0 0 1 7.6-1.7" fill="none" stroke="%[1]s" stroke-width="2.2"/>`,
	"film":   `<rect x="2.5" y="5" width="19" height="14" rx="2.5" fill="none" stroke="%[1]s" stroke-width="2"/><path d="M10 9l5 3-5 3z" fill="%[1]s"/>`,
	"wave":   `<path d="M3 11v2M7 8v8M11 4v16M15 7v10M19 10v4" stroke="%[1]s" stroke-width="2.2" stroke-linecap="round"/>`,
	"swatch": `<rect x="3" y="3" width="18" height="18" rx="3" fill="%[1]s"/>`,
}

var iconCache = map[string]fyne.Resource{}

// icon returns a named glyph in colour c. Only used on the UI thread.
func icon(name string, c color.NRGBA) fyne.Resource {
	hex := fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	key := name + hex
	if r, ok := iconCache[key]; ok {
		return r
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` + fmt.Sprintf(iconSVG[name], hex) + `</svg>`
	r := fyne.NewStaticResource(key+".svg", []byte(svg))
	iconCache[key] = r
	return r
}

// clipLabel is the duration text of a clip, short form without leading zero hours.
func clipLabel(length, fps int) string {
	tc := model.Timecode(length, fps)
	if len(tc) == 11 && tc[:3] == "00:" {
		return tc[3:]
	}
	return tc
}
