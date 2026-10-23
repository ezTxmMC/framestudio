package edit

import "framestudio-proto/src/ui/command"

// KeyGroup is one titled group of shortcuts for the help dialog.
type KeyGroup struct {
	Title string
	Keys  [][2]string // key, action
}

// keyGroups lists the commands the help shows on the edit page, by group. Playback
// keys are the shell's global commands; the rest are bound by the page.
var keyGroups = []struct {
	title string
	ids   []string
}{
	{"Wiedergabe", []string{"play.toggle", "play.reverse", "play.stop", "play.forward", "play.frameBack",
		"play.frameForward", "play.prevEdit", "play.nextEdit", "play.start", "play.end",
		"timeline.setIn", "timeline.setOut", "marker.next", "marker.prev"}},
	{"Schnitt", []string{"tool.select", "tool.blade", "tool.ripple", "tool.roll", "clip.split", "clip.splitAll",
		"marker.add", "edit.cut", "edit.copy", "edit.paste", "edit.duplicate", "edit.selectAll", "edit.deselect",
		"edit.delete", "edit.rippleDelete", "clip.rename", "timeline.snapping", "timeline.zoomIn", "timeline.zoomOut",
		"timeline.zoomFit", "view.viewerFullscreen"}},
}

// KeyMap documents the default edit page shortcuts in the format of the shell's help
// dialog (static, kept for the round 2 help). KeyMapFor lists the effective keys of a
// registry, including the user's key map.
var KeyMap = []KeyGroup{
	{"Wiedergabe", [][2]string{
		{"Leertaste", "Abspielen / Pause"},
		{"J / K / L", "Rückwärts / Stopp / Vorwärts (mehrfach: 2×, 4×, 8×)"},
		{"← / →", "Ein Frame zurück / vor"},
		{"↑ / ↓", "Vorheriger / nächster Schnittpunkt"},
		{"Pos1 / Ende", "Zum Anfang / zum Ende"},
		{"I / O", "In- / Out-Punkt setzen"},
	}},
	{"Schnitt", [][2]string{
		{"A", "Auswahlwerkzeug"},
		{"B", "Klingenwerkzeug"},
		{"R", "Ripple-Trim-Werkzeug"},
		{"N", "Rollen-Werkzeug"},
		{"S", "Clip am Playhead teilen"},
		{"M", "Marker setzen"},
		{"Strg+A", "Alle Clips auswählen"},
		{"Esc", "Auswahl aufheben"},
		{"Entf / Rücktaste", "Ausgewählte Clips löschen"},
		{"Eingabe", "Ausgewähltes Medium am Playhead einfügen"},
		{"+ / −", "Timeline vergrößern / verkleinern"},
	}},
}

// KeyMapFor returns the edit page shortcuts with the effective keys of r. Commands
// without a key are left out; Enter (insert the selected medium) is always listed.
func KeyMapFor(r *command.Registry) []KeyGroup {
	var out []KeyGroup
	for _, g := range keyGroups {
		kg := KeyGroup{Title: g.title}
		for _, id := range g.ids {
			c, ok := r.Get(id)
			if !ok {
				continue
			}
			if sc := command.Display(r.Shortcut(id)); sc != "" {
				kg.Keys = append(kg.Keys, [2]string{sc, c.Label})
			}
		}
		if g.title == "Schnitt" {
			kg.Keys = append(kg.Keys, [2]string{"Eingabe", "Ausgewähltes Medium am Playhead einfügen"})
		}
		out = append(out, kg)
	}
	return out
}
