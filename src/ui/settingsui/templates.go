package settingsui

import (
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// templateLabel is the select text of a template.
func templateLabel(t settings.Template) string {
	l := fmt.Sprintf("%s · %d×%d · %s", t.Name, t.Width, t.Height, t.FPS.Label())
	if t.User {
		l += " (eigene)"
	}
	return l
}

// templateChoice is "Vorlage für neue Projekte": built-in and user templates, plus the
// last used format when "Neues Projekt" remembered one.
func (w *window) templateChoice(c *category) *field {
	sel := widget.NewSelect(nil, nil)
	sel.PlaceHolder = "Eigener Wert"
	get := func(s *settings.Settings) *string { return &s.General.DefaultTemplate }
	var ids []string
	f := &field{label: "Vorlage für neue Projekte", help: "Auflösung und Bildrate, die „Neues Projekt“ vorschlägt. Nach dem Anlegen merkt sich Frame Studio das zuletzt verwendete Format.",
		control: sel, input: sel, reset: resetter(get)}
	f.load = func(s settings.Settings) {
		var labels []string
		ids = ids[:0]
		for _, t := range s.AllTemplates() {
			labels = append(labels, templateLabel(t))
			ids = append(ids, t.ID)
		}
		cur := s.General.DefaultTemplate
		if !slices.Contains(ids, cur) {
			if t, ok := s.ResolveTemplate(cur); ok {
				labels = append(labels, fmt.Sprintf("Zuletzt verwendet · %d×%d · %s", t.Width, t.Height, t.FPS.Label()))
				ids = append(ids, cur)
			}
		}
		on := sel.OnChanged
		sel.OnChanged = nil
		sel.Options = labels
		if i := slices.Index(ids, cur); i >= 0 {
			sel.SetSelectedIndex(i)
		} else {
			sel.ClearSelected()
		}
		sel.OnChanged = on
	}
	sel.OnChanged = func(string) {
		if i := sel.SelectedIndex(); i >= 0 && i < len(ids) {
			id := ids[i]
			w.set(func(s *settings.Settings) { *get(s) = id })
		}
	}
	return w.add(c, f)
}

// userTemplates is "Eigene Vorlagen": the user's saved formats with rename and delete.
func (w *window) userTemplates(c *category) *field {
	list := container.NewVBox()
	f := &field{label: "Eigene Vorlagen", help: "Im Dialog „Neues Projekt“ lässt sich das gewählte Format als Vorlage speichern. Hier lassen sich Vorlagen umbenennen und löschen.",
		control: list, input: list,
		reset: func(*settings.Settings, settings.Settings) {}} // the user's own templates survive a reset
	f.load = func(s settings.Settings) {
		list.Objects = nil
		if len(s.General.Templates) == 0 {
			empty := canvas.NewText("Noch keine eigenen Vorlagen", studiotheme.Muted)
			empty.TextSize = 12
			list.Add(empty)
		}
		for _, t := range s.General.Templates {
			name := widget.NewLabel(t.Name)
			name.Truncation = fyne.TextTruncateEllipsis
			spec := kit.MonoText(fmt.Sprintf("%d×%d · %s", t.Width, t.Height, strings.TrimSuffix(t.FPS.Label(), " fps")), studiotheme.Muted, 10.5)
			rename := kit.ToolButton(theme.DocumentCreateIcon(), func() { w.renameTemplate(t) })
			del := kit.ToolButton(theme.DeleteIcon(), func() {
				w.confirm("Vorlage löschen?", "Die Vorlage „"+t.Name+"“ wird gelöscht. Projekte bleiben unverändert.", "Löschen", func() {
					w.set(func(s *settings.Settings) { _ = settings.DeleteTemplate(s, t.ID) })
				})
			})
			list.Add(container.NewBorder(nil, nil, nil, container.NewHBox(spec, rename, del), name))
		}
		list.Refresh()
	}
	return w.add(c, f)
}

func (w *window) renameTemplate(t settings.Template) {
	entry := widget.NewEntry()
	entry.SetText(t.Name)
	errText := widget.NewLabel("")
	errText.Importance = widget.WarningImportance
	errText.Wrapping = fyne.TextWrapWord
	errText.Hide()
	var d *dialog.CustomDialog
	submit := func() {
		var failed error
		if err := settings.Update(func(s *settings.Settings) { failed = settings.RenameTemplate(s, t.ID, entry.Text) }); err != nil && failed == nil {
			failed = err
		}
		if failed != nil {
			errText.SetText(failed.Error())
			errText.Show()
			return
		}
		d.Hide()
	}
	entry.OnSubmitted = func(string) { submit() }
	ok := widget.NewButton("Umbenennen", submit)
	ok.Importance = widget.HighImportance
	d = dialog.NewCustomWithoutButtons("Vorlage umbenennen", container.NewGridWrap(fyne.NewSize(340, 0), container.NewVBox(entry, errText)), w.win)
	d.SetButtons([]fyne.CanvasObject{widget.NewButton("Abbrechen", func() { d.Hide() }), ok})
	d.Show()
	w.win.Canvas().Focus(entry)
}
