package audio

import "math"

// viewport is the visible time window of the "Spuren" lanes. Zoom 1 shows the whole
// span; higher zoom shows span/zoom frames starting at start.
type viewport struct {
	start float64 // first visible frame
	zoom  float64 // 1 = fit
}

// minVisibleSeconds is how much time the lanes show at most zoom.
const minVisibleSeconds = 2

// maxZoom is the zoom at which the lanes show minVisibleSeconds.
func maxZoom(span, fps int) float64 {
	if fps <= 0 {
		fps = 25
	}
	return math.Max(1, float64(span)/float64(minVisibleSeconds*fps))
}

// visible returns the number of frames across the lanes.
func (v viewport) visible(span int) float64 {
	z := v.zoom
	if z < 1 {
		z = 1
	}
	return float64(max(span, 1)) / z
}

// clamp keeps zoom within 1..maxZoom and the window inside the span.
func (v *viewport) clamp(span, fps int) {
	v.zoom = math.Max(1, math.Min(v.zoom, maxZoom(span, fps)))
	v.start = math.Max(0, math.Min(v.start, float64(span)-v.visible(span)))
}

// xOf maps frame f to an x position on lanes w pixels wide.
func (v viewport) xOf(f float64, span int, w float32) float32 {
	return float32((f - v.start) / v.visible(span) * float64(w))
}

// frameAt maps x on lanes w pixels wide to a frame (never negative).
func (v viewport) frameAt(x, w float32, span int) int {
	if w <= 0 {
		return 0
	}
	f := v.start + float64(x)/float64(w)*v.visible(span)
	return max(0, int(f))
}

// zoomAround sets the zoom and keeps frame anchor at the same screen fraction frac (0..1).
func (v *viewport) zoomAround(zoom, anchor, frac float64, span, fps int) {
	v.zoom = zoom
	v.clamp(span, fps)
	v.start = anchor - frac*v.visible(span)
	v.clamp(span, fps)
}

// scrollBy moves the window by d frames.
func (v *viewport) scrollBy(d float64, span, fps int) {
	v.start += d
	v.clamp(span, fps)
}

// follow scrolls so that playhead f stays visible. While playing it pages ahead in the
// play direction (rate < 0 plays backwards); otherwise an off-screen playhead is centred.
// It reports whether the window moved.
func (v *viewport) follow(f int, playing bool, rate float64, span, fps int) bool {
	vis := v.visible(span)
	x := (float64(f) - v.start) / vis
	old := v.start
	switch {
	case x >= 0 && x <= 1 && !playing:
		return false
	case playing && rate >= 0 && x >= 0 && x < 0.92:
		return false
	case playing && rate < 0 && x > 0.08 && x <= 1:
		return false
	case !playing:
		v.start = float64(f) - vis/2
	case rate >= 0:
		v.start = float64(f) - 0.1*vis
	default:
		v.start = float64(f) - 0.9*vis
	}
	v.clamp(span, fps)
	return v.start != old
}

// zoomToSlider and sliderToZoom map zoom to a 0..1 slider position on a log scale.
func zoomToSlider(zoom, maxZ float64) float64 {
	if maxZ <= 1 || zoom <= 1 {
		return 0
	}
	return math.Min(1, math.Log(zoom)/math.Log(maxZ))
}

func sliderToZoom(pos, maxZ float64) float64 {
	if maxZ <= 1 {
		return 1
	}
	return math.Pow(maxZ, math.Max(0, math.Min(1, pos)))
}
