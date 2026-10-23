package edit

// KeyGroup is one titled group of shortcuts for the help dialog.
type KeyGroup struct {
	Title string
	Keys  [][2]string // key, action
}

// KeyMap documents the edit page shortcuts (Page.TypedKey plus Ctrl+A) in the format
// of the shell's help dialog, so the dialog can list exactly what the page handles.
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
