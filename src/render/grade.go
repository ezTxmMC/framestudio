package render

import (
	"image"
	"math"

	"framestudio-proto/src/model"
)

const (
	gamma     = 2.2
	midGrey   = 0.46 // display value of 18 % linear grey
	vigInner  = 0.3  // normalised radius where the vignette starts
	vigAmount = 0.85 // corner darkening at Vignette = 1
)

// gradeLUT holds the per-channel part of a grade (exposure, white balance, contrast)
// as display values 0..1 indexed by the 8-bit input.
type gradeLUT struct{ r, g, b [256]float32 }

func newGradeLUT(g model.Grade) *gradeLUT {
	exp := math.Exp2(2 * clampSigned(g.Exposure))
	t, tint := clampSigned(g.Temperature), clampSigned(g.Tint)
	// Warm raises red and lowers blue; positive tint pushes towards magenta.
	mr := exp * (1 + 0.3*t) * (1 + 0.1*tint)
	mg := exp * (1 - 0.25*tint)
	mb := exp * (1 - 0.3*t) * (1 + 0.1*tint)
	k := math.Exp2(1.25 * clampSigned(g.Contrast))
	l := &gradeLUT{}
	for i := range 256 {
		lin := math.Pow(float64(i)/255, gamma)
		l.r[i] = channelOut(lin*mr, k)
		l.g[i] = channelOut(lin*mg, k)
		l.b[i] = channelOut(lin*mb, k)
	}
	return l
}

// channelOut converts a linear value back to display space and applies contrast.
func channelOut(lin, k float64) float32 {
	v := math.Pow(clamp01(lin), 1/gamma)
	return float32(clamp01((v-midGrey)*k + midGrey))
}

// gradeRect applies g to the r region of img. The vignette is relative to img's bounds.
func gradeRect(img *image.NRGBA, r image.Rectangle, g model.Grade) {
	r = r.Intersect(img.Rect)
	if r.Empty() {
		return
	}
	lut := newGradeLUT(g)
	sat := float32(1 + clampSigned(g.Saturation))
	vig := float32(clamp01(g.Vignette) * vigAmount)

	// Squared normalised horizontal distance from the centre per column.
	b := img.Rect
	var nx2 []float32
	if vig > 0 {
		nx2 = make([]float32, r.Dx())
		for x := range nx2 {
			n := (float32(r.Min.X+x-b.Min.X)+0.5)/float32(b.Dx())*2 - 1
			nx2[x] = n * n
		}
	}
	parallelRows(r.Min.Y, r.Max.Y, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
			var ny2 float32
			if vig > 0 {
				n := (float32(y-b.Min.Y)+0.5)/float32(b.Dy())*2 - 1
				ny2 = n * n
			}
			for i, x := 0, 0; i < len(row); i, x = i+4, x+1 {
				if row[i+3] == 0 {
					continue
				}
				rv, gv, bv := lut.r[row[i]], lut.g[row[i+1]], lut.b[row[i+2]]
				if sat != 1 {
					yl := 0.2126*rv + 0.7152*gv + 0.0722*bv
					rv = yl + (rv-yl)*sat
					gv = yl + (gv-yl)*sat
					bv = yl + (bv-yl)*sat
				}
				if vig > 0 {
					f := 1 - vig*smoothstep(vigInner, 1, float32(math.Sqrt(float64((nx2[x]+ny2)/2))))
					rv, gv, bv = rv*f, gv*f, bv*f
				}
				row[i], row[i+1], row[i+2] = unit8(rv), unit8(gv), unit8(bv)
			}
		}
	})
}

func smoothstep(e0, e1, x float32) float32 {
	t := (x - e0) / (e1 - e0)
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// unit8 maps 0..1 to 0..255 with clamping and rounding.
func unit8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

func clampSigned(v float64) float64 {
	if v != v {
		return 0
	}
	return math.Max(-1, math.Min(1, v))
}
