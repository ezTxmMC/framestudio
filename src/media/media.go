// Package media probes and decodes media files (stills via Go image codecs, video and
// audio via ffmpeg/ffprobe) and caches decoded frames, thumbnails and waveforms.
//
// OWNER: media agent. Exported signatures in this file are a contract; do not change them.
// Everything here must be safe for concurrent use.
package media

import (
	"errors"
	"image"

	"framestudio-proto/src/model"
)

// FFmpegAvailable reports whether ffmpeg and ffprobe are on PATH.
func FFmpegAvailable() bool { return false }

// KindForPath classifies a file by extension; ok is false for unsupported files.
func KindForPath(path string) (kind model.ClipKind, ok bool) { return "", false }

// Extensions lists supported file extensions (lower case, with dot) for file dialogs.
func Extensions() []string { return nil }

// Probe inspects a file and returns a MediaItem with a fresh ID, name, kind, size, duration.
func Probe(path string) (model.MediaItem, error) { return model.MediaItem{}, errors.New("TODO") }

// Cache decodes and memoises media. The zero value is not usable; call NewCache.
type Cache struct{}

func NewCache() *Cache { return &Cache{} }

// Image returns a still image scaled to fit within maxW x maxH (aspect preserved).
func (c *Cache) Image(m model.MediaItem, maxW, maxH int) (image.Image, error) {
	return nil, errors.New("TODO")
}

// VideoFrame returns source frame n (counted at the given project fps) of a video, scaled to fit w x h.
func (c *Cache) VideoFrame(m model.MediaItem, n, fps, w, h int) (image.Image, error) {
	return nil, errors.New("TODO")
}

// Thumbnail returns a poster image for the media pool: the still, the first video frame,
// or a rendered waveform for audio.
func (c *Cache) Thumbnail(m model.MediaItem, w, h int) (image.Image, error) {
	return nil, errors.New("TODO")
}

// Waveform returns peak amplitudes 0..1 across the whole file in the given number of buckets.
func (c *Cache) Waveform(m model.MediaItem, buckets int) ([]float32, error) {
	return nil, errors.New("TODO")
}

// Invalidate drops all cached data for a media ID (e.g. after relinking).
func (c *Cache) Invalidate(mediaID string) {}

// FrameReader decodes consecutive video frames, used by export.
type FrameReader interface {
	Next() (image.Image, error) // io.EOF after the last frame
	Close() error
}

// OpenVideo starts sequential decoding at source frame n, resampled to fps and scaled to w x h.
func OpenVideo(m model.MediaItem, n, fps, w, h int) (FrameReader, error) {
	return nil, errors.New("TODO")
}
