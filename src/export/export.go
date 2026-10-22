// Package export renders a project to a file through ffmpeg (video formats) or directly
// (PNG sequence), mixing audio tracks.
//
// OWNER: export agent. Exported signatures in this file are a contract; do not change them.
package export

import (
	"context"
	"errors"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
)

type Format string

const (
	FormatMP4  Format = "mp4"  // H.264 + AAC
	FormatWebM Format = "webm" // VP9 + Opus
	FormatGIF  Format = "gif"
	FormatPNG  Format = "png" // numbered PNG sequence in a folder
	FormatWAV  Format = "wav" // audio mixdown only
)

type Quality string

const (
	QualityHigh   Quality = "high"
	QualityMedium Quality = "medium"
	QualityLow    Quality = "low"
)

type Options struct {
	Format       Format
	Path         string // output file, or folder for FormatPNG
	Width        int
	Height       int
	Quality      Quality
	From, To     int // frame range, To exclusive; To <= From means whole timeline
	IncludeAudio bool
}

// Preset is a named starting point shown on the deliver page.
type Preset struct {
	Name    string
	Options Options
}

// Presets returns the built-in presets for a project (e.g. "YouTube 1080p", "GIF 480p").
func Presets(p *model.Project) []Preset { return nil }

type Progress struct {
	Frame, Total int
	Elapsed, ETA time.Duration
	Stage        string // e.g. "Bilder rendern", "Audio mischen"
}

// Validate checks options against the project and environment before Run.
func Validate(p *model.Project, o Options) error { return errors.New("TODO") }

// Run exports p. It blocks until done, ctx is cancelled (partial output is removed) or an
// error occurs. progress is called from the export goroutine, at most ~10 times per second.
func Run(ctx context.Context, p *model.Project, r *render.Renderer, o Options, progress func(Progress)) error {
	return errors.New("TODO")
}
