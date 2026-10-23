package media

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fileDialog is the in-app file browser used when the desktop file chooser is not
// available: folder navigation, a list with check boxes (clicking a row toggles it),
// "Alle auswählen", a type filter and, for folder imports, "Unterordner einbeziehen".
// The choice persists across folders.
type fileDialog struct {
	p    *Page
	req  pickRequest
	done func(paths []string)

	dir     string
	all     []browserEntry // current folder
	entries []browserEntry // after the type filter
	kind    model.ClipKind
	chosen  map[string]bool
	order   []string // chosen paths in click order

	list    *widget.List
	where   *canvas.Text
	up      *widget.Button
	kindSel *widget.Select
	allBtn  *widget.Button
	count   *canvas.Text
	okBtn   *widget.Button
	dlg     dialog.Dialog
}

// showFileBrowser opens the in-app chooser; done gets the chosen paths on confirm.
func (p *Page) showFileBrowser(req pickRequest, done func([]string)) *fileDialog {
	d := &fileDialog{p: p, req: req, done: done, chosen: map[string]bool{}}
	d.build()
	dir := req.dir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	d.open(dir)
	d.dlg.Resize(dialogSize(p.ctx.Window))
	d.dlg.Show()
	p.fileDlg = d
	return d
}

func (d *fileDialog) build() {
	d.where = kit.MonoText("", studiotheme.Text, 12)
	d.up = kit.ToolButton(theme.MoveUpIcon(), func() { d.open(filepath.Dir(d.dir)) })
	home := kit.ToolButton(theme.HomeIcon(), func() {
		if h, err := os.UserHomeDir(); err == nil {
			d.open(h)
		}
	})
	labels := make([]string, len(kindChoices))
	for i, c := range kindChoices {
		labels[i] = c.label
	}
	d.kindSel = widget.NewSelect(labels, func(s string) {
		for _, c := range kindChoices {
			if c.label == s {
				d.kind = c.kind
			}
		}
		d.filter()
	})
	d.kindSel.Selected = labels[0]
	d.allBtn = widget.NewButton("Alle auswählen", d.toggleAll)
	if !d.req.multi {
		d.allBtn.Hide()
	}

	d.list = widget.NewList(
		func() int { return len(d.entries) },
		func() fyne.CanvasObject { return newFileRow() },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(d.entries) {
				return
			}
			e := d.entries[i]
			r := o.(*fileRow)
			r.onTap = func(x float32) { d.tap(e, x) }
			r.set(e, d.chosen[e.path], d.checkable(e))
		})

	head := func(s string, align fyne.TextAlign) *canvas.Text {
		t := canvas.NewText(s, studiotheme.Faint)
		t.TextSize = 11
		t.Alignment = align
		return t
	}
	header := container.New(&fileRowLayout{}, spacer(1), spacer(1), head("Name", fyne.TextAlignLeading), head("Größe", fyne.TextAlignTrailing))
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	listBg := canvas.NewRectangle(studiotheme.Lane)
	listBg.CornerRadius = 3
	listBox := container.NewStack(listBg, container.NewBorder(container.NewVBox(header, line), nil, nil, nil, d.list))

	d.count = canvas.NewText("", studiotheme.Muted)
	d.count.TextSize = 12
	cancel := widget.NewButton("Abbrechen", func() { d.dlg.Hide() })
	d.okBtn = widget.NewButtonWithIcon(d.req.accept, theme.ConfirmIcon(), d.confirm)
	d.okBtn.Importance = widget.HighImportance

	left := []fyne.CanvasObject{container.NewCenter(d.count)}
	if d.req.folders {
		check := widget.NewCheck("Unterordner einbeziehen", func(on bool) { d.p.recursive.SetChecked(on) })
		check.Checked = d.p.recursive.Checked
		left = append(left, spacer(1), check)
	}
	footer := container.NewBorder(nil, nil, container.NewHBox(left...),
		container.NewHBox(cancel, fixedWidth(170, d.okBtn)))

	top := container.NewBorder(nil, nil, container.NewHBox(d.up, home),
		container.NewHBox(fixedWidth(130, d.kindSel), d.allBtn),
		container.NewPadded(d.where))
	content := container.NewBorder(top, container.NewVBox(spacer(2), footer), nil, nil, listBox)
	d.dlg = dialog.NewCustomWithoutButtons(d.req.title, content, d.p.ctx.Window)
}

// checkable reports whether a row can be chosen (files; folders in folder mode).
func (d *fileDialog) checkable(e browserEntry) bool { return !e.dir || d.req.folders }

// tap opens folders (in folder mode only outside the check box) and toggles files.
func (d *fileDialog) tap(e browserEntry, x float32) {
	if e.dir && (!d.req.folders || x > rowCheckW) {
		d.open(e.path)
		return
	}
	d.toggle(e.path)
}

func (d *fileDialog) toggle(path string) {
	if !d.req.multi && !d.chosen[path] {
		d.chosen, d.order = map[string]bool{}, nil
	}
	d.toggleQuiet(path)
	d.update()
}

// toggleAll chooses every choosable row of the folder, or clears them when all are chosen.
func (d *fileDialog) toggleAll() {
	var rows []string
	for _, e := range d.entries {
		if d.checkable(e) {
			rows = append(rows, e.path)
		}
	}
	all := len(rows) > 0
	for _, p := range rows {
		all = all && d.chosen[p]
	}
	if all { // "Auswahl aufheben" clears the whole choice, in other folders too
		d.chosen, d.order = map[string]bool{}, nil
		d.update()
		return
	}
	for _, p := range rows {
		if !d.chosen[p] {
			d.toggleQuiet(p)
		}
	}
	d.update()
}

func (d *fileDialog) toggleQuiet(path string) {
	if d.chosen[path] {
		delete(d.chosen, path)
		for i, p := range d.order {
			if p == path {
				d.order = append(d.order[:i], d.order[i+1:]...)
				break
			}
		}
		return
	}
	d.chosen[path] = true
	d.order = append(d.order, path)
}

// open lists dir; unreadable folders keep the current view and report why.
func (d *fileDialog) open(dir string) {
	entries, err := readDir(dir)
	if err != nil {
		if d.dir == "" { // first folder: show it empty rather than nothing
			d.dir = dir
			d.update()
		}
		d.p.showErr(fmt.Errorf("%w. Einen anderen Ordner wählen", err))
		return
	}
	d.dir, d.all = dir, entries
	d.filter()
	if d.list.Size().Height > 0 {
		d.list.ScrollToTop()
	}
}

// filter applies the type filter to the folder. Chosen files the filter hides are
// dropped from the choice, so the count, the button and the import match what is shown.
func (d *fileDialog) filter() {
	if !d.req.folders && d.kind != "" {
		for _, path := range append([]string(nil), d.order...) {
			if k, ok := media.KindForPath(path); !ok || k != d.kind {
				d.toggleQuiet(path)
			}
		}
	}
	d.entries = d.entries[:0]
	for _, e := range d.all {
		if e.dir || d.kind == "" || e.kind == d.kind {
			d.entries = append(d.entries, e)
		}
	}
	d.update()
}

func (d *fileDialog) update() {
	d.where.Text = d.dir
	if home, err := os.UserHomeDir(); err == nil && d.dir == home {
		d.where.Text = d.dir + "  (Persönlicher Ordner)"
	}
	d.where.Refresh()
	if filepath.Dir(d.dir) == d.dir {
		d.up.Disable()
	} else {
		d.up.Enable()
	}
	n := len(d.order)
	noun := plural(n, "Datei", "Dateien")
	if d.req.folders {
		noun = plural(n, "Ordner", "Ordner")
	}
	switch {
	case n > 0:
		d.count.Text = fmt.Sprintf("%d %s ausgewählt", n, noun)
	case d.req.folders:
		d.count.Text = "Kein Ordner markiert – übernimmt den geöffneten Ordner"
	default:
		d.count.Text = "Keine Datei ausgewählt"
	}
	d.count.Refresh()
	switch {
	case d.req.folders || !d.req.multi || n == 0:
		d.okBtn.SetText(d.req.accept)
	default:
		d.okBtn.SetText(fmt.Sprintf("%s (%d)", d.req.accept, n))
	}
	if n == 0 && !d.req.folders {
		d.okBtn.Disable()
	} else {
		d.okBtn.Enable()
	}
	all, any := true, false
	for _, e := range d.entries {
		if d.checkable(e) {
			any = true
			all = all && d.chosen[e.path]
		}
	}
	if any && all {
		d.allBtn.SetText("Auswahl aufheben")
	} else {
		d.allBtn.SetText("Alle auswählen")
	}
	if any {
		d.allBtn.Enable()
	} else {
		d.allBtn.Disable()
	}
	d.list.Refresh()
}

func (d *fileDialog) confirm() {
	paths := append([]string(nil), d.order...)
	if len(paths) == 0 {
		if !d.req.folders {
			return
		}
		paths = []string{d.dir}
	}
	d.dlg.Hide()
	if d.p.fileDlg == d {
		d.p.fileDlg = nil
	}
	if d.done != nil {
		d.done(paths)
	}
}

// rowCheckW is the width of the check box column of a file row.
const rowCheckW = 30

// fileRow is one line of the file dialog: check box, kind icon, name and size.
type fileRow struct {
	widget.BaseWidget
	onTap func(x float32)

	bg          *canvas.Rectangle
	check, icon *widget.Icon
	name        *canvas.Text
	size        *canvas.Text
	chosen      bool
}

func newFileRow() *fileRow {
	r := &fileRow{
		bg:    canvas.NewRectangle(color.Transparent),
		check: widget.NewIcon(theme.CheckButtonIcon()),
		icon:  widget.NewIcon(theme.FolderIcon()),
		name:  canvas.NewText("", studiotheme.Text),
		size:  kit.MonoText("", studiotheme.Muted, 11),
	}
	r.bg.CornerRadius = 3
	r.name.TextSize = 13
	r.size.Alignment = fyne.TextAlignTrailing
	r.ExtendBaseWidget(r)
	return r
}

func (r *fileRow) set(e browserEntry, chosen, checkable bool) {
	r.chosen = chosen
	r.name.Text = e.name
	r.size.Text = ""
	if e.dir {
		r.icon.SetResource(theme.NewColoredResource(theme.FolderIcon(), theme.ColorNamePrimary))
	} else {
		r.icon.SetResource(theme.NewColoredResource(kindIcon(e.kind), theme.ColorNameForeground))
		r.size.Text = humanSize(e.size)
	}
	switch {
	case !checkable:
		r.check.Hide()
	case chosen:
		r.check.SetResource(theme.NewColoredResource(theme.CheckButtonCheckedIcon(), theme.ColorNamePrimary))
		r.check.Show()
	default:
		r.check.SetResource(theme.NewColoredResource(theme.CheckButtonIcon(), theme.ColorNameDisabled))
		r.check.Show()
	}
	if chosen {
		r.bg.FillColor = color.NRGBA{0x5A, 0x8C, 0xEB, 0x30}
		r.name.Color = color.White
	} else {
		r.bg.FillColor = color.Transparent
		r.name.Color = studiotheme.Text
	}
	r.Refresh()
}

func (r *fileRow) Tapped(e *fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap(e.Position.X)
	}
}

func (r *fileRow) MinSize() fyne.Size { return fyne.NewSize(200, 28) }

func (r *fileRow) CreateRenderer() fyne.WidgetRenderer {
	box := container.New(&fileRowLayout{}, r.check, r.icon, r.name, r.size)
	return widget.NewSimpleRenderer(container.NewStack(r.bg, box))
}

// fileRowLayout places check box, icon, name and size column of a file row (and of the
// header above the list, so both align).
type fileRowLayout struct{}

func (fileRowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	const icon, sizeW = 18, 90
	mid := size.Height / 2
	objs[0].Resize(fyne.NewSize(icon, icon))
	objs[0].Move(fyne.NewPos((rowCheckW-icon)/2+2, mid-icon/2))
	objs[1].Resize(fyne.NewSize(icon, icon))
	objs[1].Move(fyne.NewPos(rowCheckW+2, mid-icon/2))
	nameX := float32(rowCheckW + icon + 10)
	for i, o := range objs[2:] {
		h := o.MinSize().Height
		if i == 0 {
			o.Resize(fyne.NewSize(max(size.Width-nameX-sizeW-12, 0), h))
			o.Move(fyne.NewPos(nameX, mid-h/2))
		} else {
			o.Resize(fyne.NewSize(sizeW, h))
			o.Move(fyne.NewPos(size.Width-sizeW-10, mid-h/2))
		}
	}
}

func (fileRowLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(200, 24) }

// humanSize formats a byte count like a file manager ("4,2 MB").
func humanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v, u := float64(n), 0
	for v >= 1000 && u < len(units)-1 {
		v /= 1000
		u++
	}
	if u == 0 {
		return fmt.Sprintf("%d B", n)
	}
	s := fmt.Sprintf("%.1f %s", v, units[u])
	for i := range s {
		if s[i] == '.' {
			s = s[:i] + "," + s[i+1:]
			break
		}
	}
	return s
}
