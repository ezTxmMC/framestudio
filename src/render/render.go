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
	// Rate is the playback rate the frame is rendered for (0 = paused or scrubbing).
	// When it is not 0 or 1 and the source implements PlaybackSource, frames are fetched
	// with PlaybackFrame so reverse and fast playback can decode ahead and skip frames.
	Rate float64
}

// PlaybackSource is an optional VideoSource extension for shuttle playback.
// *media.Cache implements it.
type PlaybackSource interface {
	PlaybackFrame(m model.MediaItem, n, fps, w, h int, rate float64) (image.Image, error)
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
	w, h = max(w, 0), max(h, 0)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if p == nil || w == 0 || h == 0 {
		return img
	}
	fillRect(img, img.Rect, p.Settings.Background.NRGBA())
	src := o.Video
	if isNil(src) {
		src = r.Video
	}
	for i := range p.Tracks {
		t := &p.Tracks[i]
		if t.Kind != model.TrackVideo || t.Hidden {
			continue
		}
		ci := t.ClipAt(f)
		if ci < 0 || !t.Clips[ci].Kind.IsVisual() {
			continue
		}
		c := &t.Clips[ci]
		alpha := clamp01(c.Opacity) * c.FadeFactor(f)
		if alpha <= 0 {
			continue
		}
		grade := !o.SkipGrade && !c.Grade.IsNeutral()
		if c.Kind == model.KindColor && !grade {
			blendSolid(img, img.Rect, c.Color.NRGBA(), alpha)
			continue
		}
		l := getLayer(w, h)
		bounds, offline := r.drawClip(l, p, c, f, src, o.Rate)
		if grade && !offline {
			gradeRect(l, bounds, c.Grade)
		}
		overLayer(img, l, bounds, alpha)
		putLayer(l)
	}
	return img
}

// ApplyGrade colour-corrects img in place (exported for the colour page's scopes/preview).
func ApplyGrade(img *image.NRGBA, g model.Grade) {
	if img == nil || g.IsNeutral() {
		return
	}
	gradeRect(img, img.Rect, g)
}

// Histogram returns 256-bin R, G, B and luma histograms of img.
func Histogram(img *image.NRGBA) (r, g, b, l [256]int) {
	if img == nil {
		return
	}
	bd := img.Rect
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		row := img.Pix[img.PixOffset(bd.Min.X, y):img.PixOffset(bd.Max.X, y)]
		for i := 0; i+3 < len(row); i += 4 {
			pr, pg, pb := int(row[i]), int(row[i+1]), int(row[i+2])
			r[pr]++
			g[pg]++
			b[pb]++
			// Rec.709 luma weights in fixed point (sum 65536).
			l[(13933*pr+46871*pg+4732*pb+32768)>>16]++
		}
	}
	return
}
