package kit

import (
	"math"

	"fyne.io/fyne/v2"
)

// ScaleOf returns the canvas scale (device pixels per unit) that paints o, 1 when o is not
// on a canvas (tests without a window, objects not yet shown).
func ScaleOf(o fyne.CanvasObject) float32 {
	if app := fyne.CurrentApp(); app != nil && app.Driver() != nil {
		if c := app.Driver().CanvasForObject(o); c != nil {
			return CanvasScale(c)
		}
	}
	return 1
}

// CanvasScale is c.Scale() with a safe fallback of 1.
func CanvasScale(c fyne.Canvas) float32 {
	if c == nil || c.Scale() <= 0 {
		return 1
	}
	return c.Scale()
}

// Snap rounds v (units) to a whole device pixel at scale s: round(v*s)/s.
func Snap(v, s float32) float32 {
	if s <= 0 {
		s = 1
	}
	return float32(math.Round(float64(v*s))) / s
}

// Hair is the thickness of a one-pixel line in units at scale s (one device pixel, never
// thinner than one unit below scale 1).
func Hair(s float32) float32 {
	if s < 1 {
		return 1
	}
	return 1 / s
}

// CenterY places a child of height h in a band of height band: floor((band-h)/2) in
// whole device pixels. Wordmark, menu titles and project name all use it, so their text
// boxes share one centre line.
func CenterY(band, h, s float32) float32 {
	if s <= 0 {
		s = 1
	}
	return float32(math.Floor(float64((band-h)/2*s)+1e-3)) / s
}
