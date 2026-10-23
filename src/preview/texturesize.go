// Package preview is the live-preview pipeline: a frame scheduler with latest-wins and
// drop policy, an adaptive quality controller, pooled frame buffers, a rendered-frame
// cache with precise invalidation, playback statistics and a UI load governor.
//
// It has no UI dependency. The viewer feeds it requests and swaps the ready frame in on
// the UI thread; everything expensive happens on the scheduler goroutine.
package preview

import "math"

// TextureSize is the pixel size fyne's GL painter gives a widget of the given size:
// it rounds size*scale (painter.textureScale). A picture of exactly this size is
// uploaded 1:1.
func TextureSize(width, height, scale float32) (w, h int) {
	r := func(v float32) int {
		if scale == 1 {
			return int(math.Round(float64(v)))
		}
		return int(math.Round(float64(v * scale)))
	}
	return r(width), r(height)
}

// Target is the pixel size the preview should fill (the aspect-fit picture rectangle
// in physical pixels). The zero value means "no size known yet".
type Target struct{ W, H int }

// Empty reports whether the target has no area.
func (t Target) Empty() bool { return t.W <= 0 || t.H <= 0 }
