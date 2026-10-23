package workspace

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// WaylandHint explains the placement limit of Wayland in the monitor dialog.
const WaylandHint = "Fenster können unter Wayland nur im Vollbild gezielt auf einen Monitor gelegt werden. " +
	"Für freie Platzierung in den Einstellungen Fenstersystem X11 wählen."

// monitorDialog is "Monitore und Fenster": arrangement, per-monitor assignment, presets.
type monitorDialog struct {
	m        *Manager
	dlg      dialog.Dialog
	ms       []display.Monitor
	plan     plan
	selected string
	page     string // page a preset asks for
	dirty    bool

	arr       *arrangement
	heading   *canvas.Text
	use       *widget.Check
	modes     *segmented
	content   *checkList
	options   []string // content option labels, index 0 = main window
	optionIDs []string // "" = main window, else panel ID
	status    *canvas.Text
	apply     *widget.Button
	presetBox *fyne.Container
	updating  bool
}

// ShowMonitorDialog opens the monitor dialog (workspace.monitors).
func (m *Manager) ShowMonitorDialog() {
	if m.closed {
		return
	}
	if m.dlg != nil {
		m.dlg.dlg.Show()
		return
	}
	w := m.host.MainWindow()
	if w == nil {
		return
	}
	md := m.newMonitorDialog()
	md.dlg = dialog.NewCustomWithoutButtons("Monitore und Fenster", md.build(), w)
	md.dlg.SetOnClosed(func() {
		if m.dlg == md {
			m.dlg = nil
		}
	})
	md.dlg.Resize(fyne.NewSize(900, 600))
	m.dlg = md
	md.dlg.Show()
}

func (m *Manager) newMonitorDialog() *monitorDialog {
	md := &monitorDialog{m: m}
	md.load()
	return md
}

// load reads monitors and the current layout into the dialog's plan.
func (md *monitorDialog) load() {
	md.ms = md.m.monitors()
	md.plan = planFrom(md.m.Current(), md.ms, md.m.unused)
	if _, ok := md.plan[md.selected]; !ok {
		md.selected = ""
		if len(md.ms) > 0 {
			md.selected = md.ms[0].ID
			for _, mon := range md.ms {
				if md.plan[mon.ID].Main {
					md.selected = mon.ID
				}
			}
		}
	}
	md.dirty = false
	md.page = ""
}

func (md *monitorDialog) build() fyne.CanvasObject {
	md.arr = newArrangement(md)
	arrFrame := frame(md.arr, studiotheme.Lane)

	platform := md.m.disp.Platform()
	if platform == "" {
		platform = "unbekannt"
	}
	info := canvas.NewText(fmt.Sprintf("Fenstersystem: %s · %d %s", platformName(platform), len(md.ms), plural(len(md.ms), "Monitor", "Monitore")), studiotheme.Muted)
	info.TextSize = 11
	top := container.NewBorder(nil, nil, sectionTitle("Anordnung"), info)

	// Selected monitor.
	md.heading = canvas.NewText("", studiotheme.Text)
	md.heading.TextSize = 14
	md.heading.TextStyle = fyne.TextStyle{Bold: true}
	md.use = widget.NewCheck("Verwenden", func(on bool) {
		if md.updating || md.selected == "" {
			return
		}
		mp := md.plan[md.selected]
		mp.Use = on
		if !on {
			mp.Panels = nil
			if mp.Main {
				mp.Main = false
				for _, mon := range md.ms {
					if q := md.plan[mon.ID]; q.Use {
						q.Main = true
						break
					}
				}
			}
		}
		md.changed()
	})
	md.modes = newSegmented([]string{"Fenster", "Maximiert", "Vollbild"}, func(i int) {
		if md.updating || md.selected == "" {
			return
		}
		md.plan[md.selected].Mode = []string{layout.ModeWindow, layout.ModeMaximized, layout.ModeFullscreen}[i]
		md.changed()
	})
	md.options = []string{"Hauptfenster"}
	md.optionIDs = []string{""}
	for _, p := range page.Panels() {
		md.options = append(md.options, p.Title)
		md.optionIDs = append(md.optionIDs, p.ID)
	}
	md.content = newCheckList(md.options, 2, func(sel []string) {
		if md.updating || md.selected == "" {
			return
		}
		md.setContent(sel)
	})
	form := container.NewBorder(
		container.NewVBox(
			container.NewBorder(nil, nil, md.heading, md.use),
			hline(),
			row("Modus", md.modes.Container),
			fieldLabel("Inhalt"),
		), nil, nil, nil, container.NewVScroll(container.NewVBox(md.content.Container)))
	left := frame(container.NewPadded(form), studiotheme.Panel)

	// Presets and hints.
	md.presetBox = container.NewVBox()
	md.buildPresets()
	right := container.NewVBox(sectionTitle("Vorlagen"), md.presetBox)
	if !md.m.disp.CanPosition() {
		right.Add(spacer(1))
		right.Add(hintBox(WaylandHint))
	}
	rightFrame := frame(container.NewPadded(right), studiotheme.Panel)

	md.status = canvas.NewText("", studiotheme.Muted)
	md.status.TextSize = 11
	md.apply = widget.NewButtonWithIcon("Anwenden", theme.ConfirmIcon(), md.applyPlan)
	md.apply.Importance = widget.HighImportance
	closeBtn := widget.NewButton("Schließen", func() { md.hide() })
	buttons := container.NewBorder(nil, nil, centerV(md.status), container.NewHBox(closeBtn, md.apply))

	middle := container.New(&splitCols{ratio: 0.58, gap: 8}, left, rightFrame)
	body := container.NewBorder(
		container.NewVBox(top, container.New(&fixedHeight{h: 200}, arrFrame), spacer(2)),
		container.NewVBox(spacer(2), buttons), nil, nil, middle)
	md.refreshForm()
	return body
}

func (md *monitorDialog) buildPresets() {
	md.presetBox.Objects = nil
	ps := md.m.presets(md.ms)
	for _, p := range ps {
		p := p
		b := widget.NewButtonWithIcon(p.Label, theme.GridIcon(), func() {
			md.plan = p.Build()
			md.page = p.Page
			md.changed()
			md.status.Text = "Vorlage „" + p.Label + "“ übernommen – mit Anwenden umsetzen."
			md.status.Refresh()
		})
		b.Alignment = widget.ButtonAlignLeading
		md.presetBox.Add(b)
	}
	if len(ps) == 0 {
		t := canvas.NewText("Keine Monitore erkannt.", studiotheme.Muted)
		t.TextSize = 12
		md.presetBox.Add(t)
	}
	md.presetBox.Refresh()
}

// setContent applies the content check list of the selected monitor.
func (md *monitorDialog) setContent(sel []string) {
	on := map[string]bool{}
	for _, s := range sel {
		on[s] = true
	}
	mp := md.plan[md.selected]
	if on[md.options[0]] != mp.Main {
		if on[md.options[0]] {
			md.plan.setMain(md.selected)
		} else {
			mp.Main = false
		}
	}
	for i := 1; i < len(md.options); i++ {
		id := md.optionIDs[i]
		has := false
		for _, x := range mp.Panels {
			has = has || x == id
		}
		if on[md.options[i]] != has {
			md.plan.setPanel(md.selected, id, on[md.options[i]])
		}
	}
	if (mp.Main || len(mp.Panels) > 0) && !mp.Use {
		mp.Use = true
	}
	md.changed()
}

func (md *monitorDialog) changed() {
	md.dirty = true
	md.status.Text = "Nicht angewendete Änderungen."
	md.status.Refresh()
	md.refreshForm()
}

func (md *monitorDialog) selectMonitor(id string) {
	md.selected = id
	md.refreshForm()
}

// refreshForm mirrors the plan of the selected monitor into the controls.
func (md *monitorDialog) refreshForm() {
	md.updating = true
	defer func() { md.updating = false }()
	md.arr.Refresh()
	mp := md.plan[md.selected]
	if mp == nil {
		md.heading.Text = "Kein Monitor ausgewählt"
		md.heading.Refresh()
		md.use.Disable()
		md.modes.disable(true)
		md.content.Disable()
		md.apply.Disable()
		return
	}
	mon, _ := md.monitorByID(md.selected)
	md.heading.Text = monitorLabel(mon)
	md.heading.Refresh()
	md.use.Enable()
	md.use.SetChecked(mp.Use)
	md.modes.disable(!mp.Use)
	md.modes.set(map[string]int{layout.ModeWindow: 0, layout.ModeMaximized: 1, layout.ModeFullscreen: 2}[mp.Mode])
	var sel []string
	if mp.Main {
		sel = append(sel, md.options[0])
	}
	for i := 1; i < len(md.options); i++ {
		for _, x := range mp.Panels {
			if x == md.optionIDs[i] {
				sel = append(sel, md.options[i])
			}
		}
	}
	md.content.SetSelected(sel)
	if mp.Use {
		md.content.Enable()
	} else {
		md.content.Disable()
	}
	md.apply.Enable()
}

func (md *monitorDialog) monitorByID(id string) (display.Monitor, bool) {
	for _, mon := range md.ms {
		if mon.ID == id {
			return mon, true
		}
	}
	return display.Monitor{}, false
}

// applyPlan builds a layout from the plan and restores it.
func (md *monitorDialog) applyPlan() {
	base := md.m.Current()
	if md.page != "" {
		base.ActivePage = md.page
	} else {
		base.ActivePage = ""
	}
	l := md.m.planLayout(md.plan, md.ms, base)
	keepWaiting(&l, base, md.ms)
	md.m.Apply(l)
	md.dirty = false
	md.page = ""
	md.status.Text = "Angewendet."
	md.status.Refresh()
}

// refresh reloads monitors (hot-plug, windows changed elsewhere) unless the user is
// editing.
func (md *monitorDialog) refresh() {
	if md.dirty {
		md.ms = md.m.monitors()
		md.arr.Refresh()
		return
	}
	md.load()
	if md.presetBox != nil {
		md.buildPresets()
		md.refreshForm()
	}
}

func (md *monitorDialog) hide() {
	if md.dlg != nil {
		md.dlg.Hide()
	}
	if md.m.dlg == md {
		md.m.dlg = nil
	}
}

// arrangement draws the monitors at their relative positions; a tap selects one.
type arrangement struct {
	widget.BaseWidget
	md    *monitorDialog
	boxes map[string]fyne.Position // top-left of each drawn monitor
	sizes map[string]fyne.Size
}

func newArrangement(md *monitorDialog) *arrangement {
	a := &arrangement{md: md}
	a.ExtendBaseWidget(a)
	return a
}

func (a *arrangement) CreateRenderer() fyne.WidgetRenderer {
	r := &arrRenderer{a: a}
	r.Refresh()
	return r
}

func (a *arrangement) MinSize() fyne.Size { return fyne.NewSize(400, 160) }

func (a *arrangement) Tapped(e *fyne.PointEvent) {
	for id, p := range a.boxes {
		s := a.sizes[id]
		if e.Position.X >= p.X && e.Position.X < p.X+s.Width && e.Position.Y >= p.Y && e.Position.Y < p.Y+s.Height {
			a.md.selectMonitor(id)
			return
		}
	}
}

type arrRenderer struct {
	a    *arrangement
	objs []fyne.CanvasObject
	size fyne.Size
}

func (r *arrRenderer) Destroy()                     {}
func (r *arrRenderer) MinSize() fyne.Size           { return r.a.MinSize() }
func (r *arrRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *arrRenderer) Layout(s fyne.Size) {
	if s != r.size {
		r.size = s
		r.Refresh()
	}
}

// Refresh rebuilds the drawing: one card per monitor, scaled into the widget.
func (r *arrRenderer) Refresh() {
	a, md := r.a, r.a.md
	a.boxes, a.sizes = map[string]fyne.Position{}, map[string]fyne.Size{}
	r.objs = nil
	size := r.size
	if size.Width <= 0 {
		size = a.MinSize()
	}
	if len(md.ms) == 0 {
		t := canvas.NewText("Keine Monitore erkannt – Fyne läuft ohne GLFW oder der Treiber meldet keine Ausgänge.", studiotheme.Muted)
		t.TextSize = 12
		t.Move(fyne.NewPos(16, size.Height/2-8))
		t.Resize(t.MinSize())
		r.objs = append(r.objs, t)
		return
	}
	minX, minY, maxX, maxY := md.ms[0].X, md.ms[0].Y, md.ms[0].X+md.ms[0].W, md.ms[0].Y+md.ms[0].H
	for _, mon := range md.ms {
		minX, minY = min(minX, mon.X), min(minY, mon.Y)
		maxX, maxY = max(maxX, mon.X+mon.W), max(maxY, mon.Y+mon.H)
	}
	const pad = 18
	sc := min((size.Width-2*pad)/float32(maxX-minX), (size.Height-2*pad)/float32(maxY-minY))
	offX := (size.Width - float32(maxX-minX)*sc) / 2
	offY := (size.Height - float32(maxY-minY)*sc) / 2
	for _, mon := range md.ms {
		mp := md.plan[mon.ID]
		pos := fyne.NewPos(offX+float32(mon.X-minX)*sc+4, offY+float32(mon.Y-minY)*sc+4)
		sz := fyne.NewSize(float32(mon.W)*sc-8, float32(mon.H)*sc-8)
		a.boxes[mon.ID], a.sizes[mon.ID] = pos, sz
		r.objs = append(r.objs, monitorCard(mon, mp, mon.ID == md.selected, pos, sz)...)
	}
}

// monitorCard draws one monitor: frame, connector, mode line, primary star, content.
func monitorCard(mon display.Monitor, mp *monPlan, selected bool, pos fyne.Position, sz fyne.Size) []fyne.CanvasObject {
	used := mp == nil || mp.Use
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 4
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	if !used {
		bg.FillColor = studiotheme.Panel
	}
	if selected {
		bg.StrokeColor = studiotheme.Steel
		bg.StrokeWidth = 2
		bg.FillColor = blend(bg.FillColor.(color.NRGBA), studiotheme.Steel, 0x22)
	}
	bg.Move(pos)
	bg.Resize(sz)
	objs := []fyne.CanvasObject{bg}
	// Screen surface: a darker inset like a bezel.
	screen := canvas.NewRectangle(studiotheme.Lane)
	screen.CornerRadius = 2
	inset := float32(6)
	screen.Move(pos.Add(fyne.NewPos(inset, inset+38)))
	screen.Resize(fyne.NewSize(sz.Width-2*inset, max(sz.Height-2*inset-38, 4)))
	objs = append(objs, screen)

	nameCol := studiotheme.Text
	if !used {
		nameCol = studiotheme.Faint
	}
	name := canvas.NewText(mon.ID, nameCol)
	name.TextSize = 13
	name.TextStyle = fyne.TextStyle{Bold: true}
	name.Move(pos.Add(fyne.NewPos(10, 6)))
	name.Resize(name.MinSize())
	objs = append(objs, name)
	res := fmt.Sprintf("%d×%d", mon.W, mon.H)
	if mon.Refresh > 0 {
		res += fmt.Sprintf(" · %d Hz", mon.Refresh)
	}
	sub := canvas.NewText(res, studiotheme.Muted)
	sub.TextSize = 11
	sub.TextStyle = fyne.TextStyle{Monospace: true}
	sub.Move(pos.Add(fyne.NewPos(10, 23)))
	sub.Resize(sub.MinSize())
	objs = append(objs, sub)
	if mon.Primary {
		star := canvas.NewText("★ Hauptmonitor", studiotheme.Steel)
		star.TextSize = 11
		star.Alignment = fyne.TextAlignTrailing
		star.Move(pos.Add(fyne.NewPos(10, 7)))
		star.Resize(fyne.NewSize(sz.Width-20, star.MinSize().Height))
		objs = append(objs, star)
	}

	type entry struct{ title, mode string }
	var entries []entry
	col := studiotheme.Text
	switch {
	case !used:
		entries, col = []entry{{"Nicht verwendet", ""}}, studiotheme.Faint
	case mp != nil && (mp.Main || len(mp.Panels) > 0):
		var titles []string
		if mp.Main {
			titles = append(titles, "Hauptfenster")
		}
		for _, id := range mp.Panels {
			title := id
			if p, ok := page.PanelByID(id); ok {
				title = p.Title
			}
			titles = append(titles, title)
		}
		for i, t := range titles {
			// Only the first window can fill the monitor; the others open as windows.
			mode := mp.Mode
			if i > 0 && mode != layout.ModeWindow {
				mode = layout.ModeWindow
			}
			entries = append(entries, entry{t, modeLabel(mode)})
		}
	default:
		entries, col = []entry{{"Frei", ""}}, studiotheme.Faint
	}
	y := float32(38 + inset + 7)
	for _, e := range entries {
		if y+16 > sz.Height-inset {
			break
		}
		chip := canvas.NewRectangle(studiotheme.Steel)
		if col == studiotheme.Faint {
			chip.FillColor = studiotheme.Faint
		}
		chip.CornerRadius = 1
		chip.Move(pos.Add(fyne.NewPos(inset+8, y+4)))
		chip.Resize(fyne.NewSize(3, 10))
		t := canvas.NewText(e.title, col)
		t.TextSize = 12
		t.Move(pos.Add(fyne.NewPos(inset+16, y)))
		t.Resize(t.MinSize())
		objs = append(objs, chip, t)
		if e.mode != "" {
			mt := canvas.NewText(e.mode, studiotheme.Muted)
			mt.TextSize = 11
			mt.Alignment = fyne.TextAlignTrailing
			mt.Move(pos.Add(fyne.NewPos(inset+16, y+1)))
			mt.Resize(fyne.NewSize(sz.Width-2*inset-26, mt.MinSize().Height))
			objs = append(objs, mt)
		}
		y += 18
	}
	return objs
}

func blend(a, b color.NRGBA, t uint8) color.NRGBA {
	mix := func(x, y uint8) uint8 { return uint8((int(x)*(255-int(t)) + int(y)*int(t)) / 255) }
	return color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 0xFF}
}

// checkList is a multi-select list of checks laid out in columns.
type checkList struct {
	*fyne.Container
	checks  []*widget.Check
	labels  []string
	on      func([]string)
	setting bool
}

func newCheckList(labels []string, cols int, on func([]string)) *checkList {
	l := &checkList{labels: labels, on: on}
	var objs []fyne.CanvasObject
	for _, label := range labels {
		c := widget.NewCheck(label, func(bool) {
			if !l.setting {
				l.on(l.Selected())
			}
		})
		l.checks = append(l.checks, c)
		objs = append(objs, c)
	}
	l.Container = container.New(fynelayout.NewGridLayoutWithColumns(cols), objs...)
	return l
}

// Selected returns the checked labels in list order.
func (l *checkList) Selected() []string {
	var out []string
	for i, c := range l.checks {
		if c.Checked {
			out = append(out, l.labels[i])
		}
	}
	return out
}

// SetSelected checks exactly the given labels and reports the change once.
func (l *checkList) SetSelected(sel []string) {
	l.setting = true
	for i, c := range l.checks {
		on := false
		for _, s := range sel {
			on = on || s == l.labels[i]
		}
		c.SetChecked(on)
	}
	l.setting = false
	l.on(l.Selected())
}

func (l *checkList) Enable() {
	for _, c := range l.checks {
		c.Enable()
	}
}

func (l *checkList) Disable() {
	for _, c := range l.checks {
		c.Disable()
	}
}

func (l *checkList) Disabled() bool { return len(l.checks) > 0 && l.checks[0].Disabled() }

// segmented is a compact button group with one active segment (Steel).
type segmented struct {
	*fyne.Container
	buttons []*widget.Button
	cur     int
	on      func(int)
}

func newSegmented(labels []string, on func(int)) *segmented {
	s := &segmented{on: on, cur: -1}
	var objs []fyne.CanvasObject
	for i, l := range labels {
		i := i
		b := widget.NewButton(l, func() {
			s.set(i)
			s.on(i)
		})
		s.buttons = append(s.buttons, b)
		objs = append(objs, b)
	}
	s.Container = container.New(fynelayout.NewGridLayoutWithColumns(len(labels)), objs...)
	return s
}

func (s *segmented) set(i int) {
	s.cur = i
	for j, b := range s.buttons {
		if j == i {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.MediumImportance
		}
		b.Refresh()
	}
}

func (s *segmented) disable(off bool) {
	for _, b := range s.buttons {
		if off {
			b.Disable()
		} else {
			b.Enable()
		}
	}
}

// --- small layout helpers -------------------------------------------------------

func sectionTitle(s string) fyne.CanvasObject {
	t := canvas.NewText(strings.ToUpper(s), studiotheme.Muted)
	t.TextSize = 11
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

func fieldLabel(s string) fyne.CanvasObject {
	t := canvas.NewText(s, studiotheme.Muted)
	t.TextSize = 12
	return container.New(fynelayout.NewCustomPaddedLayout(6, 2, 0, 0), t)
}

func row(label string, o fyne.CanvasObject) fyne.CanvasObject {
	t := canvas.NewText(label, studiotheme.Muted)
	t.TextSize = 12
	return container.NewBorder(nil, nil, container.NewStack(spacer(64), centerV(t)), nil, o)
}

func hline() fyne.CanvasObject {
	l := canvas.NewRectangle(studiotheme.Border)
	l.SetMinSize(fyne.NewSize(1, 1))
	return l
}

func frame(o fyne.CanvasObject, fill color.Color) fyne.CanvasObject {
	bg := canvas.NewRectangle(fill)
	bg.CornerRadius = 4
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	return container.NewStack(bg, o)
}

func hintBox(text string) fyne.CanvasObject {
	bar := canvas.NewRectangle(studiotheme.Steel)
	bar.SetMinSize(fyne.NewSize(3, 1))
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.SizeName = theme.SizeNameCaptionText
	bg := canvas.NewRectangle(blend(studiotheme.Panel, studiotheme.Steel, 0x18))
	bg.CornerRadius = 3
	icon := widget.NewIcon(theme.InfoIcon())
	return container.NewStack(bg, container.NewBorder(nil, nil, container.NewHBox(bar, container.NewVBox(icon)), nil, l))
}

// PlatformName is the display name of a window system ID ("wayland" → "Wayland").
func PlatformName(p string) string { return platformName(p) }

func platformName(p string) string {
	switch p {
	case "wayland":
		return "Wayland"
	case "x11":
		return "X11"
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	}
	return p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// splitCols places two objects side by side, the first taking ratio of the width.
type splitCols struct {
	ratio float32
	gap   float32
}

func (s *splitCols) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) < 2 {
		return
	}
	w := (size.Width - s.gap) * s.ratio
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(w, size.Height))
	objs[1].Move(fyne.NewPos(w+s.gap, 0))
	objs[1].Resize(fyne.NewSize(size.Width-w-s.gap, size.Height))
}

func (s *splitCols) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		m := o.MinSize()
		w += m.Width
		h = max(h, m.Height)
	}
	return fyne.NewSize(w+s.gap, h)
}

// fixedHeight gives its children a fixed height.
type fixedHeight struct{ h float32 }

func (f *fixedHeight) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(size.Width, f.h))
	}
}

func (f *fixedHeight) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w float32
	for _, o := range objs {
		w = max(w, o.MinSize().Width)
	}
	return fyne.NewSize(w, f.h)
}
