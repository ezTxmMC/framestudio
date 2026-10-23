// Package render composites a project frame into an image: video tracks bottom to top,
// colour/title/image/video clips with grade, opacity, fades and fit.
//
// OWNER: render agent. Exported signatures in this file are a contract; do not change them.
// Renderer must be safe for concurrent use. Callers pass a project no other goroutine
// mutates (the UI passes a Clone()).
package render

import (
	"context"
	"image"
	"sync/atomic"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

// VideoSource supplies decoded video frames. *media.Cache implements it; export
// supplies a sequential decoder for speed.
type VideoSource interface {
	VideoFrame(m model.MediaItem, n int, fps model.Rate, w, h int) (image.Image, error)
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
	PlaybackFrame(m model.MediaItem, n int, fps model.Rate, w, h int, rate float64) (image.Image, error)
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
	r.compose(context.Background(), img, p, f, o)
	return img
}

// Info describes what RenderInto did.
type Info struct {
	Opaque bool  // every pixel of the result has alpha 255
	Layers int   // visual clips composited
	CompNS int64 // total time in RenderInto
}

// RenderInto renders timeline frame f of p into dst (premultiplied RGBA, size from
// dst.Rect) for the live preview. Scratch images come from a pool, so steady-state
// rendering allocates almost nothing. It returns ctx.Err() when cancelled between
// layers; dst then holds a partial picture and must not be shown.
func (r *Renderer) RenderInto(ctx context.Context, dst *image.RGBA, p *model.Project, f int, o Options) (Info, error) {
	t0 := time.Now()
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	var info Info
	if p == nil || w == 0 || h == 0 {
		return info, nil
	}
	out := getScratch(w, h)
	defer putLayer(out)
	info.Layers, _ = r.compose(ctx, out, p, f, o)
	if err := ctx.Err(); err != nil {
		return info, err
	}
	info.Opaque = nrgbaToRGBA(dst, out)
	info.CompNS = int64(time.Since(t0))
	return info, nil
}

// CopyNRGBA premultiplies src into dst and reports whether src is opaque. Both must
// have the same size; otherwise the overlapping top-left part is drawn.
func CopyNRGBA(dst *image.RGBA, src *image.NRGBA) bool {
	if dst.Rect.Size() != src.Rect.Size() {
		sub := image.Rect(0, 0, min(dst.Rect.Dx(), src.Rect.Dx()), min(dst.Rect.Dy(), src.Rect.Dy()))
		return nrgbaToRGBA(dst.SubImage(sub).(*image.RGBA), src.SubImage(sub.Add(src.Rect.Min)).(*image.NRGBA))
	}
	return nrgbaToRGBA(dst, src)
}

// nrgbaToRGBA premultiplies src into dst (same size) and reports whether it is opaque.
func nrgbaToRGBA(dst *image.RGBA, src *image.NRGBA) bool {
	var transparent atomic.Bool
	w := src.Rect.Dx() * 4
	parallelRows(0, src.Rect.Dy(), func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			s := src.Pix[src.PixOffset(src.Rect.Min.X, src.Rect.Min.Y+y):][:w]
			d := dst.Pix[dst.PixOffset(dst.Rect.Min.X, dst.Rect.Min.Y+y):][:w]
			solid := true
			for i := 0; i+3 < len(s); i += 4 {
				a := uint32(s[i+3])
				if a == 255 {
					copy(d[i:i+4:i+4], s[i:i+4:i+4])
					continue
				}
				solid = false
				d[i] = uint8((uint32(s[i])*a + 127) / 255)
				d[i+1] = uint8((uint32(s[i+1])*a + 127) / 255)
				d[i+2] = uint8((uint32(s[i+2])*a + 127) / 255)
				d[i+3] = uint8(a)
			}
			if !solid {
				transparent.Store(true)
			}
		}
	})
	return !transparent.Load()
}

// compose draws the project frame into img (w x h, any content) and returns the number
// of layers drawn.
func (r *Renderer) compose(ctx context.Context, img *image.NRGBA, p *model.Project, f int, o Options) (layers int, err error) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	fillRect(img, img.Rect, p.Settings.Background.NRGBA())
	src := o.Video
	if isNil(src) {
		src = r.Video
	}
	for i := range p.Tracks {
		if err = ctx.Err(); err != nil {
			return layers, err
		}
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
		layers++
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
	return layers, nil
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
