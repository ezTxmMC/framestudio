package render

import (
	"errors"
	"fmt"
	"image"

	"framestudio-proto/src/model"
)

var errNoSource = errors.New("keine Medienquelle")

// drawClip renders clip c at timeline frame f into the transparent layer l and returns
// the region it touched. offline is true when a placeholder was drawn instead.
func (r *Renderer) drawClip(l *image.NRGBA, p *model.Project, c *model.Clip, f int, vs VideoSource, rate float64) (bounds image.Rectangle, offline bool) {
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
	if m.Kind == model.KindImage {
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
		return drawOffline(l, c.Name), true
	}
	s, pooled := toNRGBAPooled(src, true)
	d := fitRect(c.Fit, s.Rect.Dx(), s.Rect.Dy(), w, h)
	drawScaled(l, s, d)
	if pooled {
		putLayer(s)
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
