package shell

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// shortcut is one line of the help dialog. Keys lists alternatives separated by " | ";
// each alternative is a chord of caps joined by "+" (the plus key itself is "Plus").
type shortcut struct {
	Keys, Action string
}

// shortcutGroup is a titled block of the help dialog.
type shortcutGroup struct {
	Title string
	Keys  []shortcut
}

// shortcutColumns is the final key map of the app, grouped by area in two columns.
var shortcutColumns = [2][]shortcutGroup{
	{
		{"Projekt", []shortcut{
			{"Strg+N", "Neues Projekt"},
			{"Strg+O", "Projekt öffnen"},
			{"Strg+S", "Speichern"},
			{"Strg+Umschalt+S", "Speichern unter"},
			{"Strg+W", "Projekt schließen"},
			{"Strg+Z", "Rückgängig"},
			{"Strg+Umschalt+Z", "Wiederholen"},
			{"Strg+Y", "Wiederholen (alternativ)"},
		}},
		{"Seiten", []shortcut{
			{"Umschalt+1", "Medien"},
			{"Umschalt+2", "Schnitt"},
			{"Umschalt+3", "Farbe"},
			{"Umschalt+4", "Ton"},
			{"Umschalt+5", "Export"},
			{"Alt+1…5 | Strg+1…5", "Seite wechseln (alternativ)"},
		}},
		{"Auswahl", []shortcut{
			{"Strg+A", "Alle Clips auswählen (Medien: alle Medien)"},
			{"Strg+Klick", "Clip zur Auswahl hinzufügen oder entfernen"},
			{"Umschalt+Klick", "Auswahl bis zum Clip erweitern"},
			{"Esc", "Auswahl aufheben"},
		}},
		{"Farbe", []shortcut{
			{"↑ | ↓", "Vorheriger / nächster Clip"},
		}},
	},
	{
		{"Wiedergabe (Schnitt, Farbe, Ton, Export)", []shortcut{
			{"Leertaste", "Abspielen / Pause"},
			{"L", "Vorwärts; erneut drücken: 2×, 4×, 8×"},
			{"J", "Rückwärts; erneut drücken: 2×, 4×, 8×"},
			{"K", "Stopp"},
			{"← | →", "Ein Frame zurück / vor"},
			{"↑ | ↓", "Vorheriger / nächster Schnittpunkt"},
			{"Pos1 | Ende", "Zum Anfang / Ende"},
			{"I | O", "In- / Out-Punkt setzen"},
		}},
		{"Schnitt", []shortcut{
			{"A", "Auswahlwerkzeug"},
			{"B", "Klingenwerkzeug"},
			{"R", "Ripple-Trim: Schnitt verschieben, Rest rückt nach"},
			{"N", "Roll-Trim: Schnittpunkt zwischen zwei Clips"},
			{"S", "Clip am Playhead teilen"},
			{"M", "Marker setzen"},
			{"Entf | Rücktaste", "Ausgewählte Clips löschen"},
			{"Eingabe", "Ausgewähltes Medium am Playhead einfügen"},
		}},
		{"Timeline", []shortcut{
			{"Strg+Mausrad", "Zoomen"},
			{"Plus | −", "Vergrößern / verkleinern"},
			{"Ziehen", "Medien aus der Bibliothek (Schnitt) in die Timeline ziehen"},
		}},
	},
}

// keyCapW is the width of the key column of the help dialog.
const keyCapW = 200

func (sh *Shell) showShortcuts() {
	d := dialog.NewCustom("Tastenkürzel", "Schließen", container.NewVScroll(shortcutTable()), sh.win)
	d.Resize(fyne.NewSize(940, 700))
	d.Show()
}

// shortcutTable lays the key map out in two columns of groups.
func shortcutTable() fyne.CanvasObject {
	cols := container.New(layout.NewGridLayoutWithColumns(2))
	for _, groups := range shortcutColumns {
		col := container.New(layout.NewCustomPaddedVBoxLayout(2))
		for gi, g := range groups {
			if gi > 0 {
				col.Add(strut(0, 10))
			}
			title := text(strings.ToUpper(g.Title), studiotheme.Muted, 11, true)
			line := canvas.NewRectangle(studiotheme.Border)
			line.SetMinSize(fyne.NewSize(1, 1))
			col.Add(container.NewVBox(title, line, strut(0, 2)))
			for _, k := range g.Keys {
				keys := container.NewStack(strut(keyCapW, 0), container.NewHBox(keyCaps(k.Keys)...))
				col.Add(container.NewBorder(nil, nil, keys, nil,
					container.NewVBox(layout.NewSpacer(), text(k.Action, studiotheme.Text, 12, false), layout.NewSpacer())))
			}
		}
		cols.Add(container.New(layout.NewCustomPaddedLayout(4, 4, 6, 14), col))
	}
	return cols
}

// keyCaps renders "Strg+Umschalt+Z | Strg+Y" as key caps with separators.
func keyCaps(spec string) []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for ai, alt := range strings.Split(spec, " | ") {
		if ai > 0 {
			out = append(out, centered(text("/", studiotheme.Faint, 11, false)))
		}
		for ki, k := range strings.Split(alt, "+") {
			if ki > 0 {
				out = append(out, centered(text("+", studiotheme.Faint, 10, false)))
			}
			if k == "Plus" {
				k = "+"
			}
			out = append(out, keyCap(k))
		}
	}
	return out
}

// keyCap is one key in a raised mono cap.
func keyCap(label string) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 3
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	t := kit.MonoText(label, studiotheme.Text, 11)
	minW := strut(18, 0)
	box := container.NewStack(bg, minW, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), container.NewCenter(t)))
	return centered(box)
}

func centered(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}
