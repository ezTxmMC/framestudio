package media

import (
	"image"

	"framestudio-proto/src/model"
)

// Poster returns a frame of m at sec seconds scaled to fit w x h, for filmstrips. It must
// not disturb playback decoders and is cached. (Stills return the image itself.)
func (c *Cache) Poster(m model.MediaItem, sec float64, w, h int) (image.Image, error) {
	return c.Thumbnail(m, w, h) // TODO(media agent): real frame at sec
}
