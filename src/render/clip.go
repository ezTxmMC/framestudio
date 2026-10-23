package render

import (
	"context"
	"errors"
	"fmt"
	"image"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
)

var errNoSource = errors.New("keine Medienquelle")

// drawClip renders clip c at timeline frame f into the transparent layer l and returns
// the region it touched. offline is true when a placeholder was drawn instead.
func (r *Renderer) drawClip(ctx context.Context, l *image.NRGBA, p *model.Project, c *model.Clip, f int, vs VideoSource, o Options) (bounds image.Rectangle, offline bool) {
	w, h := l.Rect.Dx(), l.Rect.Dy()
	switch c.Kind {
	case model.KindColor:
		fillRect(l, l.Rect, c.Color.NRGBA())
		return l.Rect, false
	case model.KindTitle:
		return drawTitle(l, c.Title), false
	}
	m, ok := p.MediaByID(c.MediaID)
	if !ok {
		return drawOffline(l, c.Name), true
	}
	fps := p.Rate().Reduced()
	rw, rh := requestSize(c.Fit, *m, w, h)
	var src image.Image
	var err error
	rate := o.Rate
	var release func()
	if is, ok := vs.(IntoSource); ok && o.Preview != nil && m.Kind == model.KindVideo && !isNil(is) {
		n := max(0, c.SourceIn+f-c.Start)
		src, release, err = r.fetchInto(ctx, is, *m, n, fps, rw, rh, o)
	} else if m.Kind == model.KindImage {
		src, err = r.still(*m, rw, rh, vs)
	} else {
		n := max(0, c.SourceIn+f-c.Start)
		src, err = safeFetch(func() (image.Image, error) {
			if isNil(vs) {
				return nil, errNoSource
			}
			if rate != 0 && rate != 1 {
				if ps, ok := vs.(PlaybackSource); ok {
					return ps.PlaybackFrame(*m, n, fps, rw, rh, rate)
				}
			}
			return vs.VideoFrame(*m, n, fps, rw, rh)
		})
	}
	if err != nil || src == nil || src.Bounds().Empty() {
		if release != nil {
			release()
		}
		return drawOffline(l, c.Name), true
	}
	s, pooled := toNRGBAPooled(src, true)
	d := fitRect(c.Fit, s.Rect.Dx(), s.Rect.Dy(), w, h)
	drawScaled(l, s, d)
	if pooled {
		putLayer(s)
	}
	if release != nil {
		release()
	}
	return d.Intersect(l.Rect), false
}

// still fetches an image from r.Media, or from vs when it can supply stills.
func (r *Renderer) still(m model.MediaItem, w, h int, vs VideoSource) (image.Image, error) {
	var ss StillSource
	if r.Media != nil {
		ss = r.Media
	} else if s, ok := vs.(StillSource); ok && !isNil(s) {
		ss = s
	}
	if ss == nil {
		return nil, errNoSource
	}
	return safeFetch(func() (image.Image, error) { return ss.Image(m, w, h) })
}

// safeFetch runs a media call and turns a decoder panic into an error, so a broken
// file shows a placeholder instead of taking down the application.
func safeFetch(fn func() (image.Image, error)) (img image.Image, err error) {
	defer func() {
		if v := recover(); v != nil {
			img, err = nil, fmt.Errorf("Dekodierfehler: %v", v)
		}
	}()
	return fn()
}

// fetchInto gets a pooled frame through VideoFrameInto; release returns its buffer.
func (r *Renderer) fetchInto(ctx context.Context, is IntoSource, m model.MediaItem, n int, fps model.Rate, w, h int, o Options) (img image.Image, release func(), err error) {
	defer func() {
		if v := recover(); v != nil {
			img, release, err = nil, nil, fmt.Errorf("Dekodierfehler: %v", v)
		}
	}()
	ph := o.Preview
	hint := media.MediaHint{Speed: o.Rate, Scrub: ph.Scrub, Accurate: ph.Accurate, RenderH: ph.RenderH, Q: ph.Q, Deadline: ph.Deadline}
	d, err := is.VideoFrameInto(ctx, m, fps.FramesToDuration(n), fps, w, h, hint)
	if err != nil {
		return nil, nil, err
	}
	ph.Report.add(d)
	rgba := &image.RGBA{Pix: d.Pix, Stride: d.Stride, Rect: image.Rect(0, 0, d.W, d.H)}
	return rgba, d.Release, nil
}
