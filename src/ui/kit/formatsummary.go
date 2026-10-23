package kit

import (
	"fmt"
	"strings"

	"framestudio-proto/src/model"
)

// ChangeSummary says what applying new project settings does.
type ChangeSummary struct {
	Lines   []string // what changes, one short sentence each
	Warning string   // clip boundaries that must be rounded; empty when none
	Changed bool     // anything differs from the current settings
	Clips   int      // clips that are re-timed to the new frame grid
	Rounded int      // clips with a boundary between two frames of the new rate
}

// Text joins the lines and the warning for display.
func (c ChangeSummary) Text() string {
	out := strings.Join(c.Lines, "\n")
	if c.Warning != "" {
		out += "\n" + c.Warning
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func sizeText(w, h int) string { return fmt.Sprintf("%d×%d", w, h) }

// exactFrame reports whether frame f of rate old is exactly a frame of rate to (they
// coincide in time), computed in integers.
func exactFrame(f int, old, to model.Rate) bool {
	old, to = old.Or(model.Rate25).Reduced(), to.Or(model.Rate25).Reduced()
	g := model.ConvertFrames(f, old, to)
	return int64(f)*int64(to.Num)*int64(old.Den) == int64(g)*int64(old.Num)*int64(to.Den)
}

// SummarizeChange describes the effect of switching p to settings to: the resolution only
// changes the settings (title sizes are relative, fit modes handle media); a frame rate
// change keeps every clip, marker and playhead at the same time in seconds and lays it on
// the new frame grid.
func SummarizeChange(p *model.Project, to model.Settings) ChangeSummary {
	var c ChangeSummary
	old := p.Settings
	to.FPS = to.FPS.Reduced()
	if !to.FPS.CanDropFrame() {
		to.DropFrame = false
	}
	if old.Width != to.Width || old.Height != to.Height {
		c.Changed = true
		c.Lines = append(c.Lines, fmt.Sprintf("Auflösung %s → %s: Clips bleiben unverändert, Titel skalieren mit und die Anpassung der Medien richtet sich nach dem neuen Bild.",
			sizeText(old.Width, old.Height), sizeText(to.Width, to.Height)))
	}
	oldRate := p.Rate()
	if !oldRate.Equal(to.FPS) {
		c.Changed = true
		for _, t := range p.Tracks {
			for _, cl := range t.Clips {
				c.Clips++
				if !exactFrame(cl.Start, oldRate, to.FPS) || !exactFrame(cl.End(), oldRate, to.FPS) || !exactFrame(cl.SourceIn, oldRate, to.FPS) {
					c.Rounded++
				}
			}
		}
		head := fmt.Sprintf("Bildrate %s → %s", strings.TrimSuffix(oldRate.Label(), " fps"), to.FPS.Label())
		if c.Clips == 0 && len(p.Markers) == 0 {
			c.Lines = append(c.Lines, head+": Das Projekt hat noch keine Clips.")
		} else {
			what := plural(c.Clips, "Clip wird", "Clips werden")
			if len(p.Markers) > 0 {
				what = plural(c.Clips, "Clip", "Clips") + " und " + plural(len(p.Markers), "Marker", "Marker") + " werden"
			}
			c.Lines = append(c.Lines, head+": "+what+" auf das Bildraster angepasst. Zeiten in Sekunden bleiben gleich.")
		}
		if c.Rounded > 0 {
			c.Warning = fmt.Sprintf("Achtung: Bei %s liegen Schnitte zwischen zwei Bildern. Sie werden auf das nächste Bild gerundet, die Länge kann um ein Bild abweichen.",
				plural(c.Rounded, "Clip", "Clips"))
		}
	}
	if old.DropFrame != to.DropFrame && (oldRate.CanDropFrame() || to.FPS.CanDropFrame()) {
		c.Changed = true
		if to.DropFrame {
			c.Lines = append(c.Lines, "Drop-Frame: Der Timecode zeigt „;“ vor den Frames. Nur die Anzeige ändert sich, die Bilder bleiben.")
		} else {
			c.Lines = append(c.Lines, "Drop-Frame aus: Der Timecode zählt ohne ausgelassene Nummern. Nur die Anzeige ändert sich.")
		}
	}
	if old.Background != to.Background {
		c.Changed = true
		c.Lines = append(c.Lines, "Hintergrundfarbe wird geändert.")
	}
	if !c.Changed {
		c.Lines = []string{"Keine Änderung gegenüber dem Projekt."}
	}
	return c
}
