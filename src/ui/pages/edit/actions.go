package edit

import (
	"errors"
	"fmt"

	"framestudio-proto/src/model"
)

// Default length of stills and generators in seconds (settings Timeline.StillSeconds
// and GeneratorSecs override it).
const defaultSeconds = 5

// secondsToFrames converts a length from the settings to frames (at least one);
// zero or negative seconds mean defaultSeconds.
func secondsToFrames(sec float64, fps model.Rate) int {
	if sec <= 0 {
		sec = defaultSeconds
	}
	return max(1, fps.SecondsToFrames(sec))
}

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

// generatorClip builds the clip a generator inserts at frame start, sec seconds long.
func generatorClip(id string, fps model.Rate, start int, sec float64) model.Clip {
	length := secondsToFrames(sec, fps)
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

// insertMedia overwrites media item m at frame start on the selected track or the next
// fitting unlocked one; a video's sound goes to the first unlocked audio track. Stills
// get stillSec seconds. The routine is shared with the media page (model.PlaceMedia).
// Returns the main clip's ID.
func insertMedia(p *model.Project, selectedTrack string, m model.MediaItem, start int, stillSec float64) (string, error) {
	return p.PlaceMedia(m, start, secondsToFrames(stillSec, p.Rate()), selectedTrack)
}

// placeGenerator places a generator clip. Titles overlay the picture: they go to the
// lowest unlocked video track above the topmost clip under their start that is free for
// their whole length, and a new video track is added when there is none. Colour mattes
// use the selected track like media.
func placeGenerator(p *model.Project, selectedTrack string, c model.Clip) error {
	if c.Kind != model.KindTitle {
		t, err := p.TargetTrack(c.Kind, selectedTrack)
		if err != nil {
			return err
		}
		return p.PlaceClip(t.ID, c)
	}
	video := p.TrackIndices(model.TrackVideo)
	base := -1 // position in video of the topmost track with a clip at c.Start
	for k, i := range video {
		if p.Tracks[i].ClipAt(c.Start) >= 0 {
			base = k
		}
	}
	for _, i := range video[base+1:] {
		t := &p.Tracks[i]
		if !t.Locked && free(t, c.Start, c.End()) {
			return p.PlaceClip(t.ID, c)
		}
	}
	return p.PlaceClip(p.AddTrack(model.TrackVideo).ID, c)
}

// free reports whether no clip of t overlaps [from,to).
func free(t *model.Track, from, to int) bool {
	for _, c := range t.Clips {
		if c.Start < to && c.End() > from {
			return false
		}
	}
	return true
}

// explain turns model errors into messages that say how to fix them.
func explain(err error) error {
	var all *model.LockedTracksError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &all):
		return err
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
