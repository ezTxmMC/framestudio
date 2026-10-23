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
	AllowOffline bool // render even if media in the range is missing (placeholders, no sound)
	// FPS overrides the output frame rate (zero = project rate). Frames are always rendered at
	// the project rate and then converted with ffmpeg's fps filter (frames are dropped or
	// repeated, never blended). Ignored for PNG sequences and WAV. GIF defaults to
	// min(project rate, 15 fps) when FPS is zero.
	FPS model.Rate

	fpsFilter string // set by runVideo: ffmpeg fps filter converting to FPS
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
	// Note is a hint for the user that stays for the whole run, e.g. that an odd size was
	// rounded to even. Empty when there is nothing to say.
	Note string
}

// OutputRate is the frame rate of the file an export of o writes for a project at rate
// project: o.FPS if set, else the project rate (GIF: at most 15 fps).
func OutputRate(o Options, project model.Rate) model.Rate {
	project = project.Or(model.Rate25).Reduced()
	if !o.FPS.IsZero() {
		return o.FPS.Reduced()
	}
	if o.Format == FormatGIF && project.Float() > gifMaxFPS {
		return model.IntRate(gifMaxFPS)
	}
	return project
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

// EvenNote says, in German, that Run rounds the size of o to even numbers; empty when it
// does not (the format needs no even size or the size is even already).
func EvenNote(o Options) string {
	w, h := EvenSize(o)
	if w == o.Width && h == o.Height {
		return ""
	}
	return fmt.Sprintf("%s braucht gerade Seitenlängen: %d × %d wird mit %d × %d exportiert.", Label(o.Format), o.Width, o.Height, w, h)
}

// EvenSize is the size Run really encodes: for MP4 and WebM both sides are rounded down to
// even numbers (yuv420p needs them), other formats keep the size.
func EvenSize(o Options) (w, h int) {
	if o.Format == FormatMP4 || o.Format == FormatWebM {
		return model.EvenSize(o.Width, o.Height)
	}
	return o.Width, o.Height
}

// Validate checks options against the project and environment before Run.
func Validate(p *model.Project, o Options) error {
	if p == nil {
		return errors.New("Kein Projekt geöffnet.")
	}
	if !p.Settings.FPS.Valid() {
		return errors.New("Die Bildrate des Projekts ist ungültig. Prüfe die Projekteinstellungen.")
	}
	if !o.FPS.IsZero() && !o.FPS.Valid() {
		return fmt.Errorf("Die Export-Bildrate muss zwischen %d und %d fps liegen. Wert anpassen oder leer lassen für die Projekt-Bildrate.", model.MinFPS, model.MaxFPS)
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
		if o.Width > model.MaxSize || o.Height > model.MaxSize {
			return fmt.Errorf("Die Auflösung %d × %d ist zu groß. Höchstens %d Pixel pro Seite.", o.Width, o.Height, model.MaxSize)
		}
		if w, h := EvenSize(o); w < 2 || h < 2 {
			return fmt.Errorf("Die Auflösung %d × %d ist zu klein. Mindestens 2 Pixel pro Seite.", o.Width, o.Height)
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
	if err := checkOutput(o); err != nil {
		return err
	}
	if off := OfflineMedia(p, o); len(off) > 0 && !o.AllowOffline {
		return &OfflineError{Media: off}
	}
	return nil
}

// OfflineError reports media that the export range uses but that cannot be read. Set
// Options.AllowOffline to render anyway.
type OfflineError struct {
	Media []model.MediaItem
}

func (e *OfflineError) Error() string {
	names := ""
	for i, m := range e.Media {
		if i == 3 {
			names += fmt.Sprintf(" und %d weitere", len(e.Media)-3)
			break
		}
		if i > 0 {
			names += ", "
		}
		names += m.Name
	}
	what := "1 Medium im Bereich ist offline"
	if len(e.Media) > 1 {
		what = fmt.Sprintf("%d Medien im Bereich sind offline", len(e.Media))
	}
	return what + " (" + names + "). Verknüpfe sie unter Medien › „Neu verknüpfen“ oder exportiere trotzdem."
}

// OfflineMedia lists the media items whose files are missing but that the export of o
// would show or play: clips in the range on visible video tracks and, if sound is
// exported, audible clips on audio tracks. Each item appears once, in project order.
func OfflineMedia(p *model.Project, o Options) []model.MediaItem {
	if p == nil {
		return nil
	}
	from, to := Range(p, o)
	used := map[string]bool{}
	if o.Format != FormatWAV {
		for _, t := range p.Tracks {
			if t.Kind != model.TrackVideo || t.Hidden {
				continue
			}
			for _, c := range t.Clips {
				if c.MediaID != "" && c.End() > from && c.Start < to {
					used[c.MediaID] = true
				}
			}
		}
	}
	if o.Format == FormatWAV || (o.IncludeAudio && o.Format != FormatGIF && o.Format != FormatPNG) {
		for _, part := range audioParts(p, from, to, nil) {
			used[part.media.ID] = true
		}
	}
	var out []model.MediaItem
	for _, m := range p.Media {
		if used[m.ID] && !fileExists(m) {
			out = append(out, m)
		}
	}
	return out
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
	rep.note = EvenNote(o)
	o.Width, o.Height = EvenSize(o)
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
