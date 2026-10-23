package chrome

import (
	"image"
	"image/color"
	"math"
)

// painter is a tiny anti-aliased vector rasteriser used for the window buttons. Everything
// is specified in device independent points; k is the number of pixels per point, so the
// result is crisp at 1x, 2x and fractional scales. Shapes are described by signed distance
// functions (negative inside), coverage comes from the distance to the pixel centre.
type painter struct {
	img *image.NRGBA
	k   float64
}

func newPainter(pw, ph int, w float32) *painter {
	if pw < 1 {
		pw = 1
	}
	if ph < 1 {
		ph = 1
	}
	k := 1.0
	if w > 0 {
		k = float64(pw) / float64(w)
	}
	return &painter{img: image.NewNRGBA(image.Rect(0, 0, pw, ph)), k: k}
}

type sdf func(x, y float64) float64

// fill paints the shape inside the box (x0,y0,x1,y1 in points) with colour c.
func (p *painter) fill(box [4]float64, d sdf, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	b := p.img.Bounds()
	px0 := int(math.Floor(box[0]*p.k)) - 1
	py0 := int(math.Floor(box[1]*p.k)) - 1
	px1 := int(math.Ceil(box[2]*p.k)) + 1
	py1 := int(math.Ceil(box[3]*p.k)) + 1
	px0, py0 = max(px0, b.Min.X), max(py0, b.Min.Y)
	px1, py1 = min(px1, b.Max.X), min(py1, b.Max.Y)
	for y := py0; y < py1; y++ {
		for x := px0; x < px1; x++ {
			dist := d((float64(x)+0.5)/p.k, (float64(y)+0.5)/p.k) * p.k
			cov := 0.5 - dist
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			p.blend(x, y, c, cov)
		}
	}
}

// blend composites c with extra coverage cov over the pixel (straight alpha).
func (p *painter) blend(x, y int, c color.NRGBA, cov float64) {
	sa := float64(c.A) / 255 * cov
	i := p.img.PixOffset(x, y)
	pix := p.img.Pix[i : i+4]
	da := float64(pix[3]) / 255
	oa := sa + da*(1-sa)
	if oa <= 0 {
		return
	}
	mix := func(s, d uint8) uint8 {
		v := (float64(s)*sa + float64(d)*da*(1-sa)) / oa
		return uint8(math.Round(math.Min(255, math.Max(0, v))))
	}
	pix[0], pix[1], pix[2] = mix(c.R, pix[0]), mix(c.G, pix[1]), mix(c.B, pix[2])
	pix[3] = uint8(math.Round(oa * 255))
}

func (p *painter) disc(cx, cy, r float64, c color.NRGBA) {
	p.fill([4]float64{cx - r, cy - r, cx + r, cy + r}, func(x, y float64) float64 {
		return math.Hypot(x-cx, y-cy) - r
	}, c)
}

// ring paints a circle outline of width w whose outer edge is at radius r.
func (p *painter) ring(cx, cy, r, w float64, c color.NRGBA) {
	mid := r - w/2
	p.fill([4]float64{cx - r, cy - r, cx + r, cy + r}, func(x, y float64) float64 {
		return math.Abs(math.Hypot(x-cx, y-cy)-mid) - w/2
	}, c)
}

// rect paints the axis aligned rectangle.
func (p *painter) rect(x0, y0, x1, y1 float64, c color.NRGBA) {
	hx, hy := (x1-x0)/2, (y1-y0)/2
	mx, my := (x0+x1)/2, (y0+y1)/2
	p.fill([4]float64{x0, y0, x1, y1}, func(x, y float64) float64 {
		dx, dy := math.Abs(x-mx)-hx, math.Abs(y-my)-hy
		return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0)
	}, c)
}

// frame paints a rounded rectangle outline whose stroke is centred on the given box.
func (p *painter) frame(x0, y0, x1, y1, w, rad float64, c color.NRGBA) {
	hx, hy := (x1-x0)/2, (y1-y0)/2
	mx, my := (x0+x1)/2, (y0+y1)/2
	e := w / 2
	p.fill([4]float64{x0 - e, y0 - e, x1 + e, y1 + e}, func(x, y float64) float64 {
		dx, dy := math.Abs(x-mx)-(hx-rad), math.Abs(y-my)-(hy-rad)
		d := math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - rad
		return math.Abs(d) - e
	}, c)
}

// line paints a segment of width w with round caps.
func (p *painter) line(x0, y0, x1, y1, w float64, c color.NRGBA) {
	r := w / 2
	p.fill([4]float64{math.Min(x0, x1) - r, math.Min(y0, y1) - r, math.Max(x0, x1) + r, math.Max(y0, y1) + r},
		func(x, y float64) float64 {
			vx, vy := x1-x0, y1-y0
			l2 := vx*vx + vy*vy
			t := 0.0
			if l2 > 0 {
				t = math.Max(0, math.Min(1, ((x-x0)*vx+(y-y0)*vy)/l2))
			}
			return math.Hypot(x-(x0+t*vx), y-(y0+t*vy)) - r
		}, c)
}

// tri paints a filled triangle (any winding).
func (p *painter) tri(ax, ay, bx, by, cx, cy float64, c color.NRGBA) {
	pts := [3][2]float64{{ax, ay}, {bx, by}, {cx, cy}}
	if (bx-ax)*(cy-ay)-(by-ay)*(cx-ax) < 0 {
		pts[1], pts[2] = pts[2], pts[1]
	}
	p.fill([4]float64{
		math.Min(ax, math.Min(bx, cx)) - 1, math.Min(ay, math.Min(by, cy)) - 1,
		math.Max(ax, math.Max(bx, cx)) + 1, math.Max(ay, math.Max(by, cy)) + 1,
	}, func(x, y float64) float64 {
		d := math.Inf(-1)
		for i := 0; i < 3; i++ {
			a, b := pts[i], pts[(i+1)%3]
			ex, ey := b[0]-a[0], b[1]-a[1]
			l := math.Hypot(ex, ey)
			if l == 0 {
				continue
			}
			s := ((x-a[0])*ey - (y-a[1])*ex) / l
			if s > d {
				d = s
			}
		}
		return d
	}, c)
}

func withAlpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(math.Round(float64(c.A) * a))
	return c
}

func darken(c color.NRGBA, f float64) color.NRGBA {
	c.R = uint8(float64(c.R) * f)
	c.G = uint8(float64(c.G) * f)
	c.B = uint8(float64(c.B) * f)
	return c
}
