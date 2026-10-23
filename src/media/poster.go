package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"framestudio-proto/src/model"
)

const (
	maxPosters    = 2  // concurrent single-frame grabs for filmstrips per cache
	posterQuantum = 10 // poster times are rounded to 1/posterQuantum s
)

// Poster returns a frame of m at sec seconds scaled to fit w x h, for filmstrips. It must
// not disturb playback decoders and is cached. (Stills return the image itself.)
//
// Video frames come from a separate single-frame grab (ffmpeg -ss sec -frames:v 1) with at
// most two processes per cache; results are cached by media, time rounded to 0.1 s and
// size. Audio returns its waveform thumbnail.
func (c *Cache) Poster(m model.MediaItem, sec float64, w, h int) (image.Image, error) {
	if w <= 0 || h <= 0 {
		return nil, errors.New("Vorschaugröße ist ungültig")
	}
	switch m.Kind {
	case model.KindImage:
		return c.Image(m, w, h)
	case model.KindAudio:
		return c.Thumbnail(m, w, h)
	}
	w, h = fitSize(m.Width, m.Height, w, h)
	q := posterSlot(m, sec)
	key := fmt.Sprintf("po|%s|%s|%d|%dx%d", m.ID, m.Path, q, w, h)
	if v, ok := c.posters.get(key); ok {
		return v.(image.Image), nil
	}
	g := c.gen(m.ID)
	v, err := c.flight.do(flightKey(key, g), func() (any, error) {
		if v, ok := c.posters.get(key); ok {
			return v, nil
		}
		v, err := c.inSlot(c.posterSlots, m.ID, posterTimeout, func(ctx context.Context) (any, error) {
			return c.grab(ctx, m, float64(q)/posterQuantum, w, h)
		})
		if err != nil {
			return nil, err
		}
		img := v.(image.Image)
		c.putIf(c.posters, g, key, m.ID, img, int64(w*h*4))
		return img, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(image.Image), nil
}

// posterSlot rounds sec to the poster grid and keeps it inside the video.
func posterSlot(m model.MediaItem, sec float64) int {
	if math.IsNaN(sec) || sec < 0 {
		sec = 0
	}
	q := int(math.Round(sec * posterQuantum))
	if d := m.Duration.Seconds(); d > 0 {
		step := 1 / m.FPS.Or(model.Rate25).Float()
		last := int(math.Floor((d - step) * posterQuantum))
		q = min(q, max(last, 0))
	}
	return q
}

// grab decodes the single frame at t; near the end of a file, where ffmpeg may find no
// frame at t, it steps back.
func (c *Cache) grab(ctx context.Context, m model.MediaItem, t float64, w, h int) (image.Image, error) {
	img, err := decodeOne(ctx, m, posterArgs(m.Path, t, w, h), w, h)
	for back := 0.5; err != nil && t > 0 && ctx.Err() == nil && back <= 2; back *= 2 {
		img, err = decodeOne(ctx, m, posterArgs(m.Path, max(t-back, 0), w, h), w, h)
	}
	if err != nil {
		if ctx.Err() == nil {
			c.CheckOffline([]model.MediaItem{m})
		}
		return nil, err
	}
	return img, nil
}
