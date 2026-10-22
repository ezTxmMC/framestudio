// Package render composites a project frame into an image: video tracks bottom to top,
// colour/title/image/video clips with grade, opacity, fades and fit.
//
// OWNER: render agent. Exported signatures in this file are a contract; do not change them.
// Renderer must be safe for concurrent use. Callers pass a project no other goroutine
// mutates (the UI passes a Clone()).
package render

import (
	"image"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// VideoSource supplies decoded video frames. *media.Cache implements it; export
// supplies a sequential decoder for speed.
type VideoSource interface {
	VideoFrame(m model.MediaItem, n, fps, w, h int) (image.Image, error)
}

type Options struct {
	SkipGrade bool        // render without colour correction (before/after compare)
	Video     VideoSource // overrides Renderer.Video for this call
}

type Renderer struct {
	Media *media.Cache
	Video VideoSource
}

func New(c *media.Cache) *Renderer { return &Renderer{Media: c, Video: c} }

// Frame renders timeline frame f of p at w x h. It never returns nil: missing media is
// drawn as a placeholder.
func (r *Renderer) Frame(p *model.Project, f, w, h int) *image.NRGBA {
	return r.FrameWith(p, f, w, h, Options{})
}

func (r *Renderer) FrameWith(p *model.Project, f, w, h int, o Options) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	return img
}

// ApplyGrade colour-corrects img in place (exported for the colour page's scopes/preview).
func ApplyGrade(img *image.NRGBA, g model.Grade) {}

// Histogram returns 256-bin R, G, B and luma histograms of img.
func Histogram(img *image.NRGBA) (r, g, b, l [256]int) { return }
