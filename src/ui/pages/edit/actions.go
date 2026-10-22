package edit

import (
	"errors"
	"fmt"
	"math"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
)

// Default length of stills and generators in seconds.
const defaultSeconds = 5

// Generator is an entry of the "Generatoren" tab.
type Generator struct {
	ID, Name, Hint string
}

var generators = []Generator{
	{"color", "Farbfläche", "Vollbild in einer Farbe"},
	{"title", "Titel", "Zentrierter Text"},
	{"lower", "Untertitel-Bauchbinde", "Name im unteren Drittel"},
}

var audioClipColor = model.RGBA{R: 0x3D, G: 0x9A, B: 0x5B, A: 0xFF}

// generatorClip builds the clip a generator inserts at frame start.
func generatorClip(id string, fps, start int) model.Clip {
	length := defaultSeconds * fps
	switch id {
	case "title":
		return model.NewClip(model.KindTitle, "Titel", start, length)
	case "lower":
		c := model.NewClip(model.KindTitle, "Bauchbinde", start, length)
		c.Title.Text = "Vorname Nachname"
		c.Title.Size = 0.055
		c.Title.Y = 0.82
		c.Title.Align = model.AlignLeft
		c.Title.Background = model.RGBA{R: 0, G: 0, B: 0, A: 0xB4}
		return c
	default:
		c := model.NewClip(model.KindColor, "Farbfläche", start, length)
		c.Color = model.RGBA{R: 0x3F, G: 0x73, B: 0xB5, A: 0xFF}
		return c
	}
}

// targetTrack picks the track for a new clip of kind k: the selected track if it fits,
// otherwise the topmost video track (visual clips) or the first audio track.
func targetTrack(p *model.Project, selected string, k model.ClipKind) (*model.Track, error) {
	if t, _ := p.Track(selected); t != nil && t.Kind.Accepts(k) {
		return t, nil
	}
	if k.IsVisual() {
		if v := p.TrackIndices(model.TrackVideo); len(v) > 0 {
			return &p.Tracks[v[len(v)-1]], nil
		}
		return nil, errors.New("Das Projekt hat keine Videospur. Mit „Videospur hinzufügen“ eine anlegen")
	}
	if a := p.TrackIndices(model.TrackAudio); len(a) > 0 {
		return &p.Tracks[a[0]], nil
	}
	return nil, errors.New("Das Projekt hat keine Audiospur. Mit „Audiospur hinzufügen“ eine anlegen")
}

// insertMedia overwrites media item m at frame start. Video with sound also gets its
// audio on the first audio track. Returns the ID of the main clip.
func insertMedia(p *model.Project, selectedTrack string, m model.MediaItem, start int) (string, error) {
	t, err := targetTrack(p, selectedTrack, m.Kind)
	if err != nil {
		return "", err
	}
	c := p.ClipFromMedia(m, start, defaultSeconds*p.Settings.FPS)
	if err := p.PlaceClip(t.ID, c); err != nil {
		return "", err
	}
	if m.Kind == model.KindVideo && m.HasAudio {
		if a := p.TrackIndices(model.TrackAudio); len(a) > 0 && !p.Tracks[a[0]].Locked {
			snd := c
			snd.ID = model.NewID()
			snd.Kind = model.KindAudio
			snd.Color = audioClipColor
			if err := p.PlaceClip(p.Tracks[a[0]].ID, snd); err != nil {
				return "", err
			}
		}
	}
	return c.ID, nil
}

// rescaleFPS changes the frame rate and converts every frame position so clips keep
// their timing in seconds.
func rescaleFPS(p *model.Project, fps int) error {
	old := p.Settings.FPS
	if fps <= 0 || old <= 0 {
		return errors.New("Bildrate muss größer als 0 sein")
	}
	if fps == old {
		return nil
	}
	conv := func(f int) int { return scaleFrames(f, old, fps) }
	p.Settings.FPS = fps
	for ti := range p.Tracks {
		prevEnd := 0
		for ci := range p.Tracks[ti].Clips {
			c := &p.Tracks[ti].Clips[ci]
			start := max(conv(c.Start), prevEnd)
			end := max(conv(c.End()), start+1)
			c.Start, c.Length = start, end-start
			c.SourceIn = conv(c.SourceIn)
			if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
				if limit := p.MediaFrames(*m) - c.SourceIn; c.Length > limit && limit >= 1 {
					c.Length = limit
				}
			}
			c.FadeIn = min(conv(c.FadeIn), c.Length)
			c.FadeOut = min(conv(c.FadeOut), c.Length)
			prevEnd = c.End()
		}
	}
	for i := range p.Markers {
		p.Markers[i].Frame = conv(p.Markers[i].Frame)
	}
	return p.Validate()
}

func scaleFrames(f, from, to int) int {
	return int(math.Round(float64(f) * float64(to) / float64(from)))
}

// rescaleSession moves playhead and In/Out after a frame rate change.
func rescaleSession(s *session.Session, from, to int) {
	s.In, s.Out = scaleFrames(s.In, from, to), scaleFrames(s.Out, from, to)
	s.Playhead = scaleFrames(s.Playhead, from, to)
	s.Notify(session.ChangePlayhead)
}

// explain turns model errors into messages that say how to fix them.
func explain(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, model.ErrLocked):
		return errors.New("Die Spur ist gesperrt. Spur in der Timeline entsperren und erneut versuchen")
	case errors.Is(err, model.ErrWrongTrack):
		return errors.New("Der Clip passt nicht auf diese Spur. Bild, Video und Titel auf Videospuren, Ton auf Audiospuren legen")
	case errors.Is(err, model.ErrNotFound):
		return errors.New("Der Clip oder die Spur existiert nicht mehr. Auswahl in der Timeline erneuern")
	case errors.Is(err, model.ErrInvalidEdit):
		return errors.New("Diese Änderung ist nicht möglich. Clips brauchen mindestens 1 Frame und dürfen nicht über das Quellmaterial hinausgehen")
	}
	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
