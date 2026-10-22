// Package export renders a project to a file through ffmpeg (video formats) or directly
// (PNG sequence), mixing audio tracks.
//
// OWNER: export agent. Exported signatures in this file are a contract; do not change them.
package export

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"framestudio-proto/src/media"
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
func Presets(p *model.Project) []Preset {
	pw, ph := 1920, 1080
	if p != nil && p.Settings.Width > 0 && p.Settings.Height > 0 {
		pw, ph = p.Settings.Width, p.Settings.Height
	}
	size := func(h int) (int, int) { return SizeForHeight(pw, ph, h) }
	opt := func(f Format, w, h int, q Quality, audio bool) Options {
		return Options{Format: f, Width: w, Height: h, Quality: q, IncludeAudio: audio}
	}
	w1080, h1080 := size(1080)
	w720, h720 := size(720)
	w480, h480 := size(480)
	return []Preset{
		{"YouTube 1080p (MP4)", opt(FormatMP4, w1080, h1080, QualityHigh, true)},
		{"Web 720p (MP4)", opt(FormatMP4, w720, h720, QualityMedium, true)},
		{"Projekt-Auflösung (MP4)", opt(FormatMP4, even(pw), even(ph), QualityHigh, true)},
		{"WebM 1080p", opt(FormatWebM, w1080, h1080, QualityMedium, true)},
		{"GIF 480p", opt(FormatGIF, w480, h480, QualityMedium, false)},
		{"PNG-Sequenz", opt(FormatPNG, pw, ph, QualityHigh, false)},
		{"Nur Audio (WAV)", opt(FormatWAV, 0, 0, QualityHigh, true)},
	}
}

// SizeForHeight scales a w x h frame to height h2, keeping the aspect ratio with even sides.
func SizeForHeight(w, h, h2 int) (int, int) {
	if w <= 0 || h <= 0 {
		return even(h2 * 16 / 9), even(h2)
	}
	return even(int(float64(h2)*float64(w)/float64(h) + 0.5)), even(h2)
}

func even(n int) int {
	if n%2 != 0 {
		n++
	}
	if n < 2 {
		n = 2
	}
	return n
}

// Extension returns the file extension (with dot) of a format; empty for PNG sequences.
func Extension(f Format) string {
	switch f {
	case FormatMP4, FormatWebM, FormatGIF, FormatWAV:
		return "." + string(f)
	}
	return ""
}

// Label is the short display name of a format.
func Label(f Format) string {
	switch f {
	case FormatMP4:
		return "MP4 (H.264)"
	case FormatWebM:
		return "WebM (VP9)"
	case FormatGIF:
		return "GIF"
	case FormatPNG:
		return "PNG-Sequenz"
	case FormatWAV:
		return "WAV (Audio)"
	}
	return string(f)
}

// Formats lists all formats in display order.
func Formats() []Format {
	return []Format{FormatMP4, FormatWebM, FormatGIF, FormatPNG, FormatWAV}
}

type Progress struct {
	Frame, Total int
	Elapsed, ETA time.Duration
	Stage        string // e.g. "Bilder rendern", "Audio mischen"
}

// Range resolves the frame range of o against p: [From,To), or the whole timeline.
func Range(p *model.Project, o Options) (from, to int) {
	if o.To <= o.From {
		return 0, p.Duration()
	}
	return o.From, o.To
}

// lookFFmpeg is replaced in tests to simulate a missing ffmpeg.
var lookFFmpeg = func() bool {
	_, err := execLookPath("ffmpeg")
	return err == nil
}

const maxSide = 8192

// Validate checks options against the project and environment before Run.
func Validate(p *model.Project, o Options) error {
	if p == nil {
		return errors.New("Kein Projekt geöffnet.")
	}
	if p.Settings.FPS <= 0 {
		return errors.New("Die Bildrate des Projekts ist ungültig. Prüfe die Projekteinstellungen.")
	}
	switch o.Format {
	case FormatMP4, FormatWebM, FormatGIF, FormatPNG, FormatWAV:
	default:
		return fmt.Errorf("Unbekanntes Format %q. Wähle MP4, WebM, GIF, PNG-Sequenz oder WAV.", o.Format)
	}
	switch o.Quality {
	case "", QualityHigh, QualityMedium, QualityLow:
	default:
		return fmt.Errorf("Unbekannte Qualität %q. Wähle Hoch, Mittel oder Niedrig.", o.Quality)
	}
	if o.Format != FormatPNG && !lookFFmpeg() {
		return errors.New("ffmpeg wurde nicht gefunden. Installiere ffmpeg (z. B. über die Paketverwaltung) oder wähle PNG-Sequenz.")
	}
	if o.Format != FormatWAV {
		if o.Width <= 0 || o.Height <= 0 {
			return errors.New("Die Auflösung fehlt. Gib Breite und Höhe in Pixeln an.")
		}
		if o.Width > maxSide || o.Height > maxSide {
			return fmt.Errorf("Die Auflösung %d × %d ist zu groß. Höchstens %d Pixel pro Seite.", o.Width, o.Height, maxSide)
		}
		if (o.Format == FormatMP4 || o.Format == FormatWebM) && (o.Width%2 != 0 || o.Height%2 != 0) {
			return fmt.Errorf("%s braucht eine gerade Breite und Höhe (%d × %d). Runde auf die nächste gerade Zahl.", Label(o.Format), o.Width, o.Height)
		}
	}
	if o.From < 0 {
		return errors.New("Der Bereich beginnt vor dem Timeline-Anfang. Setze den In-Punkt neu.")
	}
	if from, to := Range(p, o); to <= from {
		return errors.New("Die Timeline ist leer. Lege Clips in die Timeline, bevor du renderst.")
	}
	if o.Format == FormatWAV {
		from, to := Range(p, o)
		if len(audioParts(p, from, to, nil)) == 0 {
			return errors.New("Im Bereich gibt es keine hörbaren Audio-Clips. Prüfe Stummschaltung und Solo der Audiospuren.")
		}
	}
	return checkOutput(o)
}

// checkOutput verifies that the output folder exists and is writable.
func checkOutput(o Options) error {
	if o.Path == "" {
		return errors.New("Der Dateiname fehlt. Gib einen Namen für die Ausgabe an.")
	}
	dir := filepath.Dir(o.Path)
	if o.Format == FormatPNG {
		if st, err := os.Stat(o.Path); err == nil {
			if !st.IsDir() {
				return fmt.Errorf("%s ist eine Datei. Für eine PNG-Sequenz einen Ordnernamen angeben.", o.Path)
			}
			dir = o.Path
		}
	} else if st, err := os.Stat(o.Path); err == nil && st.IsDir() {
		return fmt.Errorf("%s ist ein Ordner. Gib einen Dateinamen an.", o.Path)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("Der Ordner %s existiert nicht. Wähle einen vorhandenen Ordner.", dir)
	}
	f, err := os.CreateTemp(dir, ".framestudio-check-*")
	if err != nil {
		return fmt.Errorf("Keine Schreibrechte im Ordner %s. Wähle einen anderen Ordner.", dir)
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}

// Run exports p. It blocks until done, ctx is cancelled (partial output is removed) or an
// error occurs. progress is called from the export goroutine, at most ~10 times per second.
func Run(ctx context.Context, p *model.Project, r *render.Renderer, o Options, progress func(Progress)) error {
	if err := Validate(p, o); err != nil {
		return err
	}
	if o.Quality == "" {
		o.Quality = QualityMedium
	}
	if r == nil {
		r = render.New(media.NewCache())
	}
	rep := newReporter(progress)
	var err error
	switch o.Format {
	case FormatPNG:
		err = runPNG(ctx, p, r, o, rep)
	case FormatWAV:
		err = runWAV(ctx, p, o, rep)
	default:
		err = runVideo(ctx, p, r, o, rep)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
