package workspace

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/layout"
	studiotheme "framestudio-proto/src/ui/theme"
)

// showSaveLayout asks for a name and saves the current layout (workspace.saveLayout).
func (m *Manager) showSaveLayout() {
	w := m.host.MainWindow()
	if w == nil || m.closed {
		return
	}
	entry := widget.NewEntry()
	entry.SetText(m.name)
	entry.SetPlaceHolder("z. B. Schnitt mit drei Monitoren")
	hint := canvas.NewText("Gespeichert werden Fenster, Monitore, Vollbild und die Aufteilung jeder Seite.", studiotheme.Muted)
	hint.TextSize = 11
	content := container.NewVBox(widget.NewLabel("Name des Layouts"), entry, hint)
	var d dialog.Dialog
	save := func() {
		name := strings.TrimSpace(entry.Text)
		doSave := func() {
			if err := m.SaveLayout(name); err != nil {
				m.showErr(err)
				return
			}
			d.Hide()
		}
		if name != m.name && slices.Contains(layout.List(), name) {
			dialog.ShowConfirm("Layout ersetzen?", "Es gibt schon ein Layout „"+name+"“. Ersetzen?", func(ok bool) {
				if ok {
					doSave()
				}
			}, w)
			return
		}
		doSave()
	}
	entry.OnSubmitted = func(string) { save() }
	saveBtn := widget.NewButtonWithIcon("Speichern", theme.DocumentSaveIcon(), save)
	saveBtn.Importance = widget.HighImportance
	cancel := widget.NewButton("Abbrechen", func() { d.Hide() })
	d = dialog.NewCustomWithoutButtons("Layout speichern", container.NewBorder(nil,
		container.NewVBox(spacer(4), container.NewHBox(fynelayout.NewSpacer(), cancel, saveBtn)), nil, nil, content), w)
	d.Resize(fyne.NewSize(440, 200))
	d.Show()
	w.Canvas().Focus(entry)
}

// layoutManager is the "Layouts verwalten" dialog.
type layoutManager struct {
	m      *Manager
	names  []string
	sel    int
	list   *widget.List
	btns   []*widget.Button
	dialog dialog.Dialog
}

// showManageLayouts lists saved layouts: laden, umbenennen, duplizieren, löschen, als Standard.
func (m *Manager) showManageLayouts() {
	w := m.host.MainWindow()
	if w == nil || m.closed {
		return
	}
	lm := m.newLayoutManager()
	lm.dialog = dialog.NewCustomWithoutButtons("Layouts verwalten", lm.build(), w)
	lm.dialog.Resize(fyne.NewSize(620, 420))
	lm.dialog.Show()
}

func (m *Manager) newLayoutManager() *layoutManager {
	lm := &layoutManager{m: m, sel: -1}
	lm.reload()
	return lm
}

func (lm *layoutManager) reload() {
	lm.names = layout.List()
	if lm.sel >= len(lm.names) {
		lm.sel = len(lm.names) - 1
	}
	if lm.list != nil {
		lm.list.Refresh()
		if lm.sel >= 0 {
			lm.list.Select(lm.sel)
		} else {
			lm.list.UnselectAll()
		}
	}
	lm.updateButtons()
}

func (lm *layoutManager) selected() string {
	if lm.sel < 0 || lm.sel >= len(lm.names) {
		return ""
	}
	return lm.names[lm.sel]
}

func (lm *layoutManager) build() fyne.CanvasObject {
	lm.list = widget.NewList(func() int { return len(lm.names) },
		func() fyne.CanvasObject {
			name := canvas.NewText("Layout", studiotheme.Text)
			name.TextSize = 13
			badge := canvas.NewText("", studiotheme.Steel)
			badge.TextSize = 11
			return container.NewBorder(nil, nil, nil, centerV(badge), centerV(name))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container)
			name := c.Objects[0].(*fyne.Container).Objects[1].(*canvas.Text)
			badge := c.Objects[1].(*fyne.Container).Objects[1].(*canvas.Text)
			n := lm.names[i]
			name.Text = n
			var tags []string
			if n == lm.m.name {
				tags = append(tags, "Aktuell")
			}
			if n == settings.Get().Display.Layout {
				tags = append(tags, "Standard")
			}
			badge.Text = strings.Join(tags, " · ")
			name.Refresh()
			badge.Refresh()
		})
	lm.list.OnSelected = func(i widget.ListItemID) { lm.sel = i; lm.updateButtons() }
	lm.list.OnUnselected = func(widget.ListItemID) { lm.sel = -1; lm.updateButtons() }

	mk := func(label string, icon fyne.Resource, fn func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, fn)
		b.Alignment = widget.ButtonAlignLeading
		lm.btns = append(lm.btns, b)
		return b
	}
	load := mk("Laden", theme.MediaPlayIcon(), func() { lm.load() })
	load.Importance = widget.HighImportance
	actions := container.NewVBox(
		load,
		mk("Umbenennen…", theme.DocumentCreateIcon(), lm.rename),
		mk("Duplizieren", theme.ContentCopyIcon(), lm.duplicate),
		mk("Als Standard", theme.HomeIcon(), lm.makeDefault),
		mk("Löschen", theme.DeleteIcon(), lm.remove),
	)
	empty := canvas.NewText("Noch keine Layouts gespeichert. Speichere eines über Arbeitsbereich ▸ Layout speichern.", studiotheme.Muted)
	empty.TextSize = 12
	listArea := container.NewStack(frame(lm.list, studiotheme.Lane))
	if len(lm.names) == 0 {
		listArea.Add(container.NewCenter(empty))
	}
	note := canvas.NewText("Das Standard-Layout wird beim Start geladen, wenn „Layout beim Beenden speichern“ aus ist.", studiotheme.Muted)
	note.TextSize = 11
	closeBtn := widget.NewButton("Schließen", func() { lm.dialog.Hide() })
	body := container.NewBorder(nil, container.NewVBox(spacer(4), container.NewBorder(nil, nil, centerV(note), closeBtn)),
		nil, container.NewPadded(container.NewStack(spacer(170), actions)), listArea)
	lm.updateButtons()
	return body
}

func (lm *layoutManager) updateButtons() {
	on := lm.selected() != ""
	for _, b := range lm.btns {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
}

func (lm *layoutManager) win() fyne.Window { return lm.m.host.MainWindow() }

func (lm *layoutManager) load() {
	if n := lm.selected(); n != "" {
		lm.m.LoadLayout(n)
		lm.reload()
	}
}

func (lm *layoutManager) makeDefault() {
	n := lm.selected()
	if n == "" {
		return
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = n })
	lm.reload()
}

func (lm *layoutManager) duplicate() {
	n := lm.selected()
	if n == "" {
		return
	}
	if err := lm.m.copyLayout(n, uniqueName(n+" Kopie", layout.List()), false); err != nil {
		lm.m.showErr(err)
	}
	lm.reload()
}

func (lm *layoutManager) rename() {
	old := lm.selected()
	if old == "" {
		return
	}
	entry := widget.NewEntry()
	entry.SetText(old)
	dialog.ShowCustomConfirm("Layout umbenennen", "Umbenennen", "Abbrechen", entry, func(ok bool) {
		if !ok {
			return
		}
		if err := lm.m.RenameLayout(old, entry.Text); err != nil {
			lm.m.showErr(err)
		}
		lm.reload()
	}, lm.win())
}

func (lm *layoutManager) remove() {
	n := lm.selected()
	if n == "" {
		return
	}
	dialog.ShowConfirm("Layout löschen?", "„"+n+"“ wird gelöscht. Fenster und Monitore bleiben, wie sie gerade sind.", func(ok bool) {
		if !ok {
			return
		}
		if err := lm.m.DeleteLayout(n); err != nil {
			lm.m.showErr(err)
		}
		lm.reload()
	}, lm.win())
}

// RenameLayout renames a saved layout (keeps it current and default when it was).
func (m *Manager) RenameLayout(old, name string) error {
	name = strings.TrimSpace(name)
	if name == old {
		return nil
	}
	if name == "" || strings.HasPrefix(name, "_") {
		return errors.New("Bitte einen Namen eingeben, der nicht mit „_“ beginnt.")
	}
	if slices.Contains(layout.List(), name) {
		return errors.New("Es gibt schon ein Layout „" + name + "“.")
	}
	if err := m.copyLayout(old, name, true); err != nil {
		return err
	}
	if m.name == old {
		m.name = name
	}
	if settings.Get().Display.Layout == old {
		_ = settings.Update(func(s *settings.Settings) { s.Display.Layout = name })
	}
	m.refresh()
	return nil
}

// DeleteLayout removes a saved layout.
func (m *Manager) DeleteLayout(name string) error {
	if err := layout.Delete(name); err != nil {
		return errors.New("Das Layout ließ sich nicht löschen: " + plainErr(err))
	}
	m.refresh()
	return nil
}

func (m *Manager) copyLayout(from, to string, move bool) error {
	l, err := layout.Load(from, false)
	if err != nil {
		return errors.New("Das Layout „" + from + "“ ließ sich nicht lesen: " + plainErr(err))
	}
	l.Name = to
	if err := layout.Save(l, false); err != nil {
		return errors.New("Das Layout ließ sich nicht speichern: " + plainErr(err))
	}
	if move {
		if err := layout.Delete(from); err != nil {
			return errors.New("Das alte Layout ließ sich nicht entfernen: " + plainErr(err))
		}
	}
	return nil
}

// uniqueName appends a number until name is not taken.
func uniqueName(name string, taken []string) string {
	if !slices.Contains(taken, name) {
		return name
	}
	for i := 2; ; i++ {
		n := name + " " + strconv.Itoa(i)
		if !slices.Contains(taken, n) {
			return n
		}
	}
}
