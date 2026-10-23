// Package keymapui is the shortcut editor: every command by category with its shortcut,
// search, record-a-shortcut, conflict warnings, reset per command and reset all.
//
// OWNER: menubar agent. Exported signatures in this file are a contract.
//
// Every change goes to the registry (SetShortcut / SetKeymap) and is persisted at once in
// settings.Keymap. UI thread only.
package keymapui

import (
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	rowH     = 28
	headerH  = 30
	scColW   = 190
	actColW  = 196
	textSize = 12.5
)

// warn marks conflicts (Ocker; Tally red stays reserved for the playhead).
var warn = studiotheme.ClipColors[4].Color

// Editor returns the editor content; changes go to reg and are persisted in settings.Keymap.
func Editor(w fyne.Window, reg *command.Registry) fyne.CanvasObject {
	return newEditor(w, reg).content
}

// Show opens the editor in a dialog.
func Show(w fyne.Window, reg *command.Registry) { show(w, reg) }

func show(w fyne.Window, reg *command.Registry) *editor {
	e := newEditor(w, reg)
	d := dialog.NewCustom("Tastenkürzel", "Schließen", e.content, w)
	d.SetOnClosed(e.cancelRecording)
	d.Resize(fyne.NewSize(760, 560))
	d.Show()
	w.Canvas().Focus(e.search)
	return e
}

type conflict struct {
	id, sc string
	others []string
}

type editor struct {
	w   fyne.Window
	reg *command.Registry

	content     fyne.CanvasObject
	search      *widget.Entry
	onlyChanged *widget.Check
	resetAllBtn *widget.Button
	body        *fyne.Container
	scroll      *container.Scroll
	summary     *canvas.Text
	status      *canvas.Text

	recording string    // command being recorded
	rec       *recorder // its capture widget
	pending   *conflict // a captured shortcut waiting for Ersetzen / Abbrechen

	rows map[string]*kmRow // visible rows by ID (tests)
}

func newEditor(w fyne.Window, reg *command.Registry) *editor {
	e := &editor{w: w, reg: reg}
	e.search = widget.NewEntry()
	e.search.SetPlaceHolder("Befehl oder Kürzel suchen…")
	e.search.OnChanged = func(string) { e.rebuild() }
	e.onlyChanged = widget.NewCheck("Nur geänderte", func(bool) { e.rebuild() })
	e.resetAllBtn = widget.NewButtonWithIcon("Alle zurücksetzen", theme.HistoryIcon(), e.confirmResetAll)
	e.resetAllBtn.Importance = widget.LowImportance
	e.body = container.New(stackLayout{})
	e.scroll = container.NewVScroll(e.body)
	e.summary = canvas.NewText("", studiotheme.Muted)
	e.summary.TextSize = 11
	e.status = canvas.NewText("", warn)
	e.status.TextSize = 11

	toolbar := container.NewBorder(nil, nil, nil,
		container.NewHBox(e.onlyChanged, e.resetAllBtn), e.search)

	colTitle := func(s string) *canvas.Text {
		t := canvas.NewText(s, studiotheme.Muted)
		t.TextSize = 11
		t.TextStyle = fyne.TextStyle{Bold: true}
		return t
	}
	head := container.New(&rowLayout{}, canvas.NewRectangle(studiotheme.Raised),
		colTitle("Befehl"), container.NewHBox(container.New(&fixedLayout{fyne.NewSize(6, 6)}), colTitle("Kürzel")), container.NewHBox())
	tableBg := canvas.NewRectangle(studiotheme.Lane)
	tableBg.StrokeColor = studiotheme.Border
	tableBg.StrokeWidth = 1
	tableBg.CornerRadius = 4
	table := container.NewStack(tableBg, container.New(layout.NewCustomPaddedLayout(1, 1, 1, 1),
		container.NewBorder(head, nil, nil, nil, e.scroll)))
	footer := container.NewHBox(e.summary, layout.NewSpacer(), e.status)
	e.content = container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(0, 6, 0, 0), toolbar),
		container.New(layout.NewCustomPaddedLayout(6, 0, 2, 2), footer), nil, nil, table)
	e.rebuild()
	return e
}

func (e *editor) modified() map[string]string { return e.reg.Keymap() }

func (e *editor) label(id string) string {
	if c, ok := e.reg.Get(id); ok && c.Label != "" {
		return strings.TrimSuffix(c.Label, "…")
	}
	return id
}

func (e *editor) matches(c command.Command, q string) bool {
	if q == "" || command.Matches(c, q) {
		return true
	}
	lq := strings.ToLower(q)
	sc := strings.ToLower(command.Display(c.Shortcut))
	return sc != "" && (strings.Contains(sc, lq) || strings.ToLower(c.Shortcut) == lq)
}

// rebuild lays the table out again for the current filter and state.
func (e *editor) rebuild() {
	q := strings.TrimSpace(e.search.Text)
	mod := e.modified()
	var objs []fyne.CanvasObject
	e.rows = map[string]*kmRow{}
	if e.rec != nil {
		e.rec.done = true // replaced: its focus loss must not cancel the new state
		e.rec = nil
	}
	var cat string
	n, shown := 0, 0
	for _, c := range e.reg.All() {
		n++
		_, changed := mod[c.ID]
		if (e.onlyChanged.Checked && !changed) || !e.matches(c, q) {
			continue
		}
		if c.Category != cat {
			cat = c.Category
			objs = append(objs, categoryHeader(cat))
		}
		r := e.newRow(c, changed, shown)
		e.rows[c.ID] = r
		objs = append(objs, r.obj)
		shown++
	}
	if shown == 0 {
		t := canvas.NewText("Keine passenden Befehle", studiotheme.Faint)
		t.TextSize = textSize
		objs = append(objs, container.New(layout.NewCustomPaddedLayout(14, 14, 14, 14), t))
	}
	e.body.Objects = objs
	e.body.Refresh()
	e.scroll.Refresh()
	sum := strconv.Itoa(n) + " Befehle"
	if len(mod) == 1 {
		sum += " · 1 geändert"
	} else if len(mod) > 1 {
		sum += " · " + strconv.Itoa(len(mod)) + " geändert"
	}
	if shown != n {
		sum += " · " + strconv.Itoa(shown) + " angezeigt"
	}
	e.summary.Text = sum
	e.summary.Refresh()
	if len(mod) == 0 {
		e.resetAllBtn.Disable()
	} else {
		e.resetAllBtn.Enable()
	}
	if e.rec != nil {
		e.w.Canvas().Focus(e.rec)
	}
}

func categoryHeader(cat string) fyne.CanvasObject {
	t := canvas.NewText(strings.ToUpper(cat), studiotheme.Muted)
	t.TextSize = 10.5
	t.TextStyle = fyne.TextStyle{Bold: true}
	line := canvas.NewRectangle(studiotheme.Border)
	return container.New(&headerLayout{}, t, line)
}

type kmRow struct {
	id       string
	obj      fyne.CanvasObject
	record   *flatButton
	reset    *flatButton
	replace  *flatButton
	abort    *flatButton
	conflict *canvas.Text
}

func (e *editor) newRow(c command.Command, changed bool, idx int) *kmRow {
	id := c.ID
	r := &kmRow{id: id}
	bg := canvas.NewRectangle(color.Transparent)
	if idx%2 == 1 {
		bg.FillColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x05}
	}
	label := canvas.NewText(c.Label, studiotheme.Text)
	label.TextSize = textSize
	if !c.Available {
		label.Color = studiotheme.Muted
	}

	var cell fyne.CanvasObject
	switch {
	case e.recording == id:
		e.rec = newRecorder(func(sc string) { e.captured(id, sc) }, func() { e.cancelRecording() }, func() { e.set(id, "") })
		cell = e.rec
		bg.FillColor = withAlpha(studiotheme.Steel, 0x1C)
	case e.pending != nil && e.pending.id == id:
		cell = chip(command.Display(e.pending.sc), true)
		bg.FillColor = withAlpha(warn, 0x1C)
	default:
		cell = chip(command.Display(c.Shortcut), false)
	}
	// a fixed slot left of the chip marks changed shortcuts, so chips stay aligned
	dot := canvas.NewCircle(color.Transparent)
	if changed {
		dot.FillColor = studiotheme.Steel
	}
	cellBox := []fyne.CanvasObject{container.New(&fixedLayout{fyne.NewSize(6, 6)}, dot), cell}

	r.record = newFlatButton("Aufnehmen", false, func() { e.startRecording(id) })
	r.reset = newFlatButton("Zurücksetzen", false, func() { e.resetOne(id) })
	// the rightmost slot is always Aufnehmen (or Abbrechen while recording)
	actions := container.NewHBox(r.record)
	if changed {
		actions = container.NewHBox(r.reset, r.record)
	}
	if e.recording == id {
		actions = container.NewHBox(newFlatButton("Abbrechen", false, e.cancelRecording))
	}
	main := container.New(&rowLayout{}, bg, label, container.NewHBox(cellBox...), actions)
	if e.pending == nil || e.pending.id != id {
		r.obj = main
		return r
	}
	// conflict line: "Belegt durch: Speichern" with Ersetzen / Abbrechen
	names := make([]string, len(e.pending.others))
	for i, o := range e.pending.others {
		names[i] = e.label(o)
	}
	r.conflict = canvas.NewText("Belegt durch: "+strings.Join(names, ", "), warn)
	r.conflict.TextSize = 11.5
	icon := canvas.NewImageFromResource(theme.NewColoredResource(theme.WarningIcon(), theme.ColorNameWarning))
	icon.SetMinSize(fyne.NewSize(14, 14))
	r.replace = newFlatButton("Ersetzen", true, e.replaceConflict)
	r.abort = newFlatButton("Abbrechen", false, e.cancelConflict)
	cbg := canvas.NewRectangle(withAlpha(warn, 0x1C))
	line := container.New(&rowLayout{}, cbg, container.NewHBox(icon, r.conflict), container.NewHBox(), container.NewHBox(r.replace, r.abort))
	r.obj = container.New(layout.NewCustomPaddedVBoxLayout(0), main, line)
	return r
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// chip renders a shortcut as a key cap ("—" when none).
func chip(text string, warning bool) fyne.CanvasObject {
	if text == "" {
		t := canvas.NewText("—", studiotheme.Faint)
		t.TextSize = textSize
		return t
	}
	t := canvas.NewText(text, studiotheme.Text)
	t.TextSize = 11.5
	t.TextStyle = fyne.TextStyle{Monospace: true}
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 3
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	if warning {
		bg.StrokeColor = warn
		t.Color = warn
	}
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(2, 2, 7, 7), t))
}

func (e *editor) startRecording(id string) {
	e.pending = nil
	e.status.Text = ""
	e.status.Refresh()
	e.recording = id
	e.rebuild()
}

func (e *editor) cancelRecording() {
	if e.recording == "" {
		return
	}
	e.recording = ""
	e.rebuild()
}

// captured receives a recorded shortcut: applied at once, or held as a conflict.
func (e *editor) captured(id, sc string) {
	e.recording = ""
	if others := e.reg.Conflicts(sc, id); len(others) > 0 {
		e.pending = &conflict{id: id, sc: sc, others: others}
		e.rebuild()
		return
	}
	e.set(id, sc)
}

func (e *editor) replaceConflict() {
	p := e.pending
	if p == nil {
		return
	}
	e.pending = nil
	for _, o := range p.others {
		e.reg.SetShortcut(o, "")
	}
	e.set(p.id, p.sc)
}

func (e *editor) cancelConflict() {
	e.pending = nil
	e.rebuild()
}

// set changes one shortcut ("" removes it) and persists the keymap.
func (e *editor) set(id, sc string) {
	e.recording = ""
	e.reg.SetShortcut(id, sc)
	e.persist()
	e.rebuild()
}

func (e *editor) resetOne(id string) {
	km := e.reg.Keymap()
	delete(km, id)
	e.reg.SetKeymap(km)
	e.pending = nil
	e.persist()
	e.rebuild()
}

func (e *editor) confirmResetAll() {
	d := dialog.NewConfirm("Alle Tastenkürzel zurücksetzen?",
		"Alle eigenen Belegungen werden gelöscht und die Standardkürzel wiederhergestellt.",
		func(ok bool) {
			if ok {
				e.resetAll()
			}
		}, e.w)
	d.SetConfirmText("Zurücksetzen")
	d.SetDismissText("Abbrechen")
	d.Show()
}

func (e *editor) resetAll() {
	e.reg.SetKeymap(nil)
	e.pending, e.recording = nil, ""
	e.persist()
	e.rebuild()
}

func (e *editor) persist() {
	km := e.reg.Keymap()
	err := settings.Update(func(s *settings.Settings) { s.Keymap = km })
	if err != nil {
		e.status.Text = "Tastenkürzel konnten nicht gespeichert werden: " + err.Error()
	} else {
		e.status.Text = ""
	}
	e.status.Refresh()
}

// rowLayout places [background, label, shortcut cell, actions] in fixed columns.
type rowLayout struct{}

func (rowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	center := func(o fyne.CanvasObject, x, w float32) {
		ms := o.MinSize()
		if w <= 0 {
			w = ms.Width
		}
		o.Move(fyne.NewPos(x, (size.Height-ms.Height)/2))
		o.Resize(fyne.NewSize(w, ms.Height))
	}
	scX := size.Width - actColW - scColW
	center(objs[1], 14, scX-20)
	center(objs[2], scX, scColW)
	act := objs[3].MinSize()
	center(objs[3], size.Width-act.Width-10, act.Width)
}

func (rowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(rowH)
	for _, o := range objs[1:] {
		if mh := o.MinSize().Height + 4; mh > h {
			h = mh
		}
	}
	return fyne.NewSize(scColW+actColW+200, h)
}

type headerLayout struct{}

func (headerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	t, line := objs[0], objs[1]
	ts := t.MinSize()
	t.Move(fyne.NewPos(14, size.Height-ts.Height-5))
	t.Resize(ts)
	line.Move(fyne.NewPos(0, size.Height-1))
	line.Resize(fyne.NewSize(size.Width, 1))
}

func (headerLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(100, headerH) }

// stackLayout stacks rows vertically without gaps.
type stackLayout struct{}

func (stackLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (stackLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		ms := o.MinSize()
		h += ms.Height
		if ms.Width > w {
			w = ms.Width
		}
	}
	return fyne.NewSize(w, h)
}

// fixedLayout centres its object in a box of fixed size (plus a little right margin).
type fixedLayout struct{ size fyne.Size }

func (f *fixedLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(f.size)
		o.Move(fyne.NewPos(0, (size.Height-f.size.Height)/2))
	}
}

func (f *fixedLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(f.size.Width, f.size.Height)
}
