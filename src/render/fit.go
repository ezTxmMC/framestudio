package render

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"framestudio-proto/src/model"
)

// StillSource supplies decoded still images. *media.Cache implements it. When
// Renderer.Media is nil, a VideoSource that also implements StillSource is used for stills.
type StillSource interface {
	Image(m model.MediaItem, maxW, maxH int) (image.Image, error)
}

// fitRect returns where a sw x sh source lands in a w x h frame for the given fit mode.
// For cover the rectangle extends beyond the frame and is cropped when drawn.
func fitRect(fit model.Fit, sw, sh, w, h int) image.Rectangle {
	if fit == model.FitStretch || sw <= 0 || sh <= 0 {
		return image.Rect(0, 0, w, h)
	}
	sx, sy := float64(w)/float64(sw), float64(h)/float64(sh)
	s := math.Min(sx, sy)
	if fit == model.FitCover {
		s = math.Max(sx, sy)
	}
	dw := max(1, int(math.Round(float64(sw)*s)))
	dh := max(1, int(math.Round(float64(sh)*s)))
	x0, y0 := (w-dw)/2, (h-dh)/2
	return image.Rect(x0, y0, x0+dw, y0+dh)
}

// requestSize is the size to ask a media cache for so that the fitted image needs no
// upscaling. Without known media dimensions the frame size is used.
func requestSize(fit model.Fit, m model.MediaItem, w, h int) (int, int) {
	if fit == model.FitContain || m.Width <= 0 || m.Height <= 0 {
		return w, h
	}
	s := math.Max(float64(w)/float64(m.Width), float64(h)/float64(m.Height))
	return max(1, int(math.Ceil(float64(m.Width)*s))), max(1, int(math.Ceil(float64(m.Height)*s)))
}

// toNRGBA converts src to a non-premultiplied image with fast paths for the common types.
func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	switch s := src.(type) {
	case *image.NRGBA:
		return s
	case *image.RGBA:
		dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		parallelRows(0, b.Dy(), func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				si := s.Pix[s.PixOffset(b.Min.X, y+b.Min.Y):s.PixOffset(b.Max.X, y+b.Min.Y)]
				di := dst.Pix[dst.PixOffset(0, y):dst.PixOffset(b.Dx(), y)]
				for i := 0; i < len(si); i += 4 {
					a := uint32(si[i+3])
					switch a {
					case 255:
						copy(di[i:i+4], si[i:i+4])
					case 0:
					default:
						di[i] = uint8((uint32(si[i])*255 + a/2) / a)
						di[i+1] = uint8((uint32(si[i+1])*255 + a/2) / a)
						di[i+2] = uint8((uint32(si[i+2])*255 + a/2) / a)
						di[i+3] = uint8(a)
					}
				}
			}
		})
		return dst
	case *image.YCbCr:
		dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		parallelRows(0, b.Dy(), func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				di := dst.Pix[dst.PixOffset(0, y):dst.PixOffset(b.Dx(), y)]
				for x := 0; x < b.Dx(); x++ {
					yi := s.YOffset(x+b.Min.X, y+b.Min.Y)
					ci := s.COffset(x+b.Min.X, y+b.Min.Y)
					r, g, bb := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
					di[x*4], di[x*4+1], di[x*4+2], di[x*4+3] = r, g, bb, 255
				}
			}
		})
		return dst
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Rect, src, b.Min, draw.Src)
	return dst
}

// drawScaled bilinearly scales src into rectangle d of dst, clipped to dst's bounds.
func drawScaled(dst, src *image.NRGBA, d image.Rectangle) {
	clip := d.Intersect(dst.Rect)
	sb := src.Rect
	sw, sh := sb.Dx(), sb.Dy()
	if clip.Empty() || sw == 0 || sh == 0 {
		return
	}
	if d.Dx() == sw && d.Dy() == sh {
		for y := clip.Min.Y; y < clip.Max.Y; y++ {
			sy := y - d.Min.Y + sb.Min.Y
			sx := clip.Min.X - d.Min.X + sb.Min.X
			copy(dst.Pix[dst.PixOffset(clip.Min.X, y):dst.PixOffset(clip.Max.X, y)],
				src.Pix[src.PixOffset(sx, sy):src.PixOffset(sx+clip.Dx(), sy)])
		}
		return
	}
	// Per-column source positions in 16.16 fixed point, precomputed once.
	const one = 1 << 16
	cols := make([]tap, clip.Dx())
	kx := float64(sw) / float64(d.Dx())
	for i := range cols {
		cols[i] = sample(float64(clip.Min.X+i-d.Min.X), kx, sw)
	}
	ky := float64(sh) / float64(d.Dy())
	parallelRows(clip.Min.Y, clip.Max.Y, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			t := sample(float64(y-d.Min.Y), ky, sh)
			r0 := src.Pix[src.PixOffset(sb.Min.X, sb.Min.Y+t.x0):]
			r1 := src.Pix[src.PixOffset(sb.Min.X, sb.Min.Y+t.x1):]
			fy := t.fx
			out := dst.Pix[dst.PixOffset(clip.Min.X, y):dst.PixOffset(clip.Max.X, y)]
			for i, c := range cols {
				a, b := c.x0*4, c.x1*4
				o := out[i*4 : i*4+4 : i*4+4]
				for ch := range 4 {
					top := int(r0[a+ch])*(one-c.fx) + int(r0[b+ch])*c.fx
					bot := int(r1[a+ch])*(one-c.fx) + int(r1[b+ch])*c.fx
					o[ch] = uint8(((top>>8)*(one-fy) + (bot>>8)*fy + (1 << 23)) >> 24)
				}
			}
		}
	})
}

// tap is a bilinear sample: two source neighbours and the 16.16 weight of the second.
type tap struct{ x0, x1, fx int }

// sample maps destination pixel i to its source tap for scale factor k.
func sample(i, k float64, n int) tap {
	s := (i+0.5)*k - 0.5
	if s < 0 {
		s = 0
	}
	x0 := int(s)
	if x0 >= n-1 {
		return tap{n - 1, n - 1, 0}
	}
	return tap{x0, x0 + 1, int((s - float64(x0)) * (1 << 16))}
}
