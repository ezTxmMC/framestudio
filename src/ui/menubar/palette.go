package menubar

import (
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	paletteW     = 620
	paletteListH = 380
	paletteRowH  = 28
	mruMax       = 8
)

// mru holds the IDs of recently run palette commands, most recent first (in memory only).
var mru []string

func remember(id string) {
	out := []string{id}
	for _, x := range mru {
		if x != id && len(out) < mruMax {
			out = append(out, x)
		}
	}
	mru = out
}

// ShowPalette opens the command palette (fuzzy search over available commands).
func ShowPalette(w fyne.Window, reg *command.Registry) { showPalette(w, reg) }

type entryKind int

const (
	kindCommand entryKind = iota
	kindHeader
)

type paletteEntry struct {
	kind   entryKind
	header string
	cmd    command.Command
	recent bool
}

type palette struct {
	w       fyne.Window
	reg     *command.Registry
	pop     *widget.PopUp
	entry   *searchEntry
	list    *fyne.Container
	scroll  *container.Scroll
	count   *canvas.Text
	entries []paletteEntry
	rows    []*paletteRow
	sel     int
}

func showPalette(w fyne.Window, reg *command.Registry) *palette {
	p := &palette{w: w, reg: reg, sel: -1}
	p.entry = newSearchEntry(p)
	p.entry.SetPlaceHolder("Befehl suchen…")
	p.entry.OnChanged = func(string) { p.filter() }
	p.list = container.New(&listLayout{})
	p.scroll = container.NewVScroll(p.list)
	p.scroll.SetMinSize(fyne.NewSize(paletteW, paletteListH))
	p.count = canvas.NewText("", studiotheme.Faint)
	p.count.TextSize = 11
	hint := canvas.NewText("Pfeiltasten wählen  ·  Eingabe führt aus  ·  Esc schließt", studiotheme.Faint)
	hint.TextSize = 11
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	line2 := canvas.NewRectangle(studiotheme.Border)
	line2.SetMinSize(fyne.NewSize(1, 1))
	footerBg := canvas.NewRectangle(studiotheme.Panel)
	footer := container.NewStack(footerBg, container.New(layout.NewCustomPaddedLayout(5, 5, 12, 12),
		container.NewHBox(hint, layout.NewSpacer(), p.count)))
	top := container.New(layout.NewCustomPaddedLayout(8, 8, 8, 8), p.entry)
	content := container.NewBorder(
		container.New(layout.NewCustomPaddedVBoxLayout(0), top, line),
		container.New(layout.NewCustomPaddedVBoxLayout(0), line2, footer), nil, nil, p.scroll)
	p.pop = widget.NewPopUp(content, w.Canvas())
	p.filter()
	cs := w.Canvas().Size()
	size := fyne.NewSize(paletteW, content.MinSize().Height)
	if size.Width > cs.Width-20 {
		size.Width = cs.Width - 20
	}
	p.pop.Resize(size)
	y := cs.Height * 0.14
	if y+size.Height > cs.Height {
		y = 0
	}
	p.pop.ShowAtPosition(fyne.NewPos((cs.Width-size.Width)/2, y))
	w.Canvas().Focus(p.entry)
	return p
}

// filter rebuilds the list for the current query: recently used first, then the rest.
func (p *palette) filter() {
	q := strings.TrimSpace(p.entry.Text)
	all := p.reg.Available()
	byID := map[string]command.Command{}
	for _, c := range all {
		byID[c.ID] = c
	}
	var recent, prefix, rest []command.Command
	seen := map[string]bool{}
	for _, id := range mru {
		if c, ok := byID[id]; ok && command.Matches(c, q) {
			recent = append(recent, c)
			seen[id] = true
		}
	}
	lq := strings.ToLower(q)
	for _, c := range all {
		if seen[c.ID] || !(command.Matches(c, q) || matchesShortcut(c, lq)) {
			continue
		}
		if lq != "" && strings.HasPrefix(strings.ToLower(c.Label), lq) {
			prefix = append(prefix, c)
		} else {
			rest = append(rest, c)
		}
	}
	p.entries = p.entries[:0]
	if len(recent) > 0 {
		p.entries = append(p.entries, paletteEntry{kind: kindHeader, header: "Zuletzt verwendet"})
		for _, c := range recent {
			p.entries = append(p.entries, paletteEntry{cmd: c, recent: true})
		}
		if len(prefix)+len(rest) > 0 {
			p.entries = append(p.entries, paletteEntry{kind: kindHeader, header: "Alle Befehle"})
		}
	}
	for _, c := range append(prefix, rest...) {
		p.entries = append(p.entries, paletteEntry{cmd: c})
	}
	if len(p.entries) == 0 {
		p.entries = append(p.entries, paletteEntry{kind: kindHeader, header: "Kein Befehl gefunden"})
	}
	n := len(recent) + len(prefix) + len(rest)
	switch n {
	case 1:
		p.count.Text = "1 Befehl"
	default:
		p.count.Text = strconv.Itoa(n) + " Befehle"
	}
	p.count.Refresh()
	p.sel = -1 // before the rows exist: new renderers read it
	p.rows = make([]*paletteRow, len(p.entries))
	objs := make([]fyne.CanvasObject, len(p.entries))
	for i, e := range p.entries {
		p.rows[i] = newPaletteRow(p, i, e)
		objs[i] = p.rows[i]
	}
	p.list.Objects = objs
	p.list.Refresh()
	p.move(1)
	p.scroll.ScrollToTop()
	p.scroll.Refresh()
}

func matchesShortcut(c command.Command, lq string) bool {
	if lq == "" || c.Shortcut == "" {
		return false
	}
	return strings.ToLower(command.Display(c.Shortcut)) == lq || strings.ToLower(c.Shortcut) == lq
}

// move selects the next command row in direction dir (headers are skipped).
func (p *palette) move(dir int) {
	i := p.sel
	for {
		i += dir
		if i < 0 || i >= len(p.entries) {
			return
		}
		if p.entries[i].kind == kindCommand {
			p.select_(i)
			return
		}
	}
}

func (p *palette) select_(i int) {
	if i == p.sel {
		return
	}
	old := p.sel
	p.sel = i
	if old >= 0 && old < len(p.rows) {
		p.rows[old].Refresh()
	}
	if i >= 0 && i < len(p.rows) {
		p.rows[i].Refresh()
		p.reveal(i)
	}
}

func (p *palette) reveal(i int) {
	top := p.list.Objects[i].Position().Y
	bottom := top + p.list.Objects[i].Size().Height
	h := p.scroll.Size().Height
	if h <= 0 {
		return
	}
	off := p.scroll.Offset.Y
	if top < off {
		p.scroll.Offset.Y = top
	} else if bottom > off+h {
		p.scroll.Offset.Y = bottom - h
	}
	if p.scroll.Offset.Y != off {
		p.scroll.Refresh()
	}
}

// run executes the selected command; disabled commands keep the palette open.
func (p *palette) run(i int) {
	if i < 0 || i >= len(p.entries) || p.entries[i].kind != kindCommand {
		return
	}
	c := p.entries[i].cmd
	if cur, ok := p.reg.Get(c.ID); !ok || !cur.Enabled {
		return
	}
	p.close()
	remember(c.ID)
	p.reg.Run(c.ID)
}

func (p *palette) close() { p.pop.Hide() }

// searchEntry routes the navigation keys to the palette.
type searchEntry struct {
	widget.Entry
	p *palette
}

func newSearchEntry(p *palette) *searchEntry {
	e := &searchEntry{p: p}
	e.ExtendBaseWidget(e)
	return e
}

func (e *searchEntry) TypedKey(ev *fyne.KeyEvent) {
	switch ev.Name {
	case fyne.KeyDown:
		e.p.move(1)
	case fyne.KeyUp:
		e.p.move(-1)
	case fyne.KeyPageDown:
		for k := 0; k < 10; k++ {
			e.p.move(1)
		}
	case fyne.KeyPageUp:
		for k := 0; k < 10; k++ {
			e.p.move(-1)
		}
	case fyne.KeyReturn, fyne.KeyEnter:
		e.p.run(e.p.sel)
	case fyne.KeyEscape:
		e.p.close()
	default:
		e.Entry.TypedKey(ev)
	}
}

// listLayout stacks rows without gaps (headers are shorter than command rows).
type listLayout struct{}

func (listLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (listLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		h += o.MinSize().Height
	}
	return fyne.NewSize(paletteW-20, h)
}

type paletteRow struct {
	widget.BaseWidget
	p *palette
	i int
	e paletteEntry
}

func newPaletteRow(p *palette, i int, e paletteEntry) *paletteRow {
	r := &paletteRow{p: p, i: i, e: e}
	r.ExtendBaseWidget(r)
	return r
}

func (r *paletteRow) Tapped(*fyne.PointEvent) {
	if r.e.kind == kindCommand {
		r.p.select_(r.i)
		r.p.run(r.i)
	}
}

func (r *paletteRow) MouseIn(*desktop.MouseEvent) {
	if r.e.kind == kindCommand {
		r.p.select_(r.i)
	}
}
func (r *paletteRow) MouseMoved(*desktop.MouseEvent) {}
func (r *paletteRow) MouseOut()                      {}

func (r *paletteRow) MinSize() fyne.Size {
	if r.e.kind == kindHeader {
		return fyne.NewSize(100, 24)
	}
	return fyne.NewSize(100, paletteRowH)
}

func (r *paletteRow) CreateRenderer() fyne.WidgetRenderer {
	pr := &paletteRowRenderer{r: r, bg: canvas.NewRectangle(color.Transparent)}
	pr.bg.CornerRadius = 3
	if r.e.kind == kindHeader {
		pr.label = canvas.NewText(strings.ToUpper(r.e.header), studiotheme.Muted)
		pr.label.TextSize = 10.5
		pr.label.TextStyle = fyne.TextStyle{Bold: true}
	} else {
		pr.label = canvas.NewText(r.e.cmd.Label, studiotheme.Text)
		pr.label.TextSize = labelSize
		pr.cat = canvas.NewText(r.e.cmd.Category, studiotheme.Muted)
		pr.cat.TextSize = 11
		if r.e.cmd.Toggle { // on/off state like the menus: a check mark, or an empty box
			if r.e.cmd.Checked {
				pr.check = canvas.NewImageFromResource(theme.ConfirmIcon())
				pr.check.FillMode = canvas.ImageFillContain
			} else {
				pr.box = canvas.NewRectangle(color.Transparent)
				pr.box.StrokeWidth = 1
				pr.box.CornerRadius = 2
			}
		}
		if sc := command.Display(r.e.cmd.Shortcut); sc != "" {
			pr.sc = canvas.NewText(sc, studiotheme.Muted)
			pr.sc.TextSize = scSize
			pr.sc.TextStyle = fyne.TextStyle{Monospace: true}
			pr.chip = canvas.NewRectangle(studiotheme.Lane)
			pr.chip.CornerRadius = 3
			pr.chip.StrokeColor = studiotheme.Border
			pr.chip.StrokeWidth = 1
		}
	}
	pr.update()
	return pr
}

type paletteRowRenderer struct {
	r          *paletteRow
	bg         *canvas.Rectangle
	label, cat *canvas.Text
	sc         *canvas.Text
	chip       *canvas.Rectangle
	check      *canvas.Image     // toggle that is on
	box        *canvas.Rectangle // toggle that is off
}

const (
	catColW   = 120
	checkColW = 22 // state column of toggles, kept for every row so labels align
)

func (pr *paletteRowRenderer) Layout(size fyne.Size) {
	pr.bg.Move(fyne.NewPos(6, 1))
	pr.bg.Resize(fyne.NewSize(size.Width-12, size.Height-2))
	ls := pr.label.MinSize()
	if pr.r.e.kind == kindHeader {
		pr.label.Move(fyne.NewPos(14, size.Height-ls.Height-3))
		pr.label.Resize(ls)
		return
	}
	cs := pr.cat.MinSize()
	pr.cat.Move(fyne.NewPos(16, (size.Height-cs.Height)/2))
	pr.cat.Resize(cs)
	pr.label.Move(fyne.NewPos(16+catColW+checkColW, (size.Height-ls.Height)/2))
	pr.label.Resize(ls)
	if pr.check != nil {
		pr.check.Move(fyne.NewPos(16+catColW, (size.Height-14)/2))
		pr.check.Resize(fyne.NewSquareSize(14))
	}
	if pr.box != nil {
		pr.box.Move(fyne.NewPos(16+catColW+2, (size.Height-11)/2))
		pr.box.Resize(fyne.NewSquareSize(11))
	}
	if pr.sc != nil {
		ss := pr.sc.MinSize()
		x := size.Width - 16 - ss.Width
		pr.sc.Move(fyne.NewPos(x, (size.Height-ss.Height)/2))
		pr.sc.Resize(ss)
		pr.chip.Move(fyne.NewPos(x-6, (size.Height-ss.Height)/2-2))
		pr.chip.Resize(fyne.NewSize(ss.Width+12, ss.Height+4))
	}
}

func (pr *paletteRowRenderer) update() {
	r := pr.r
	if r.e.kind == kindHeader {
		return
	}
	sel := r.p.sel == r.i
	enabled := r.e.cmd.Enabled
	pr.label.Color, pr.cat.Color = studiotheme.Text, studiotheme.Muted
	if !enabled {
		pr.label.Color, pr.cat.Color = studiotheme.Faint, studiotheme.Faint
	}
	pr.bg.FillColor = color.Transparent
	if sel {
		if enabled {
			pr.bg.FillColor = studiotheme.Steel
			pr.label.Color = color.White
			pr.cat.Color = color.NRGBA{0xFF, 0xFF, 0xFF, 0xB0}
		} else {
			pr.bg.FillColor = studiotheme.Raised
		}
	}
	if pr.box != nil {
		pr.box.StrokeColor = studiotheme.Faint
		if sel && enabled {
			pr.box.StrokeColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xB0}
		}
	}
	if pr.check != nil {
		res := theme.ConfirmIcon()
		if !enabled {
			res = theme.NewDisabledResource(res)
		}
		pr.check.Resource = res
	}
	if pr.sc != nil {
		pr.sc.Color = studiotheme.Muted
		pr.chip.FillColor, pr.chip.StrokeColor = studiotheme.Lane, studiotheme.Border
		if !enabled {
			pr.sc.Color = studiotheme.Faint
		}
		if sel && enabled {
			pr.sc.Color = color.White
			pr.chip.FillColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x22}
			pr.chip.StrokeColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x40}
		}
	}
}

func (pr *paletteRowRenderer) MinSize() fyne.Size { return pr.r.MinSize() }
func (pr *paletteRowRenderer) Refresh() {
	pr.update()
	canvas.Refresh(pr.r)
}
func (pr *paletteRowRenderer) Destroy() {}
func (pr *paletteRowRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{pr.bg}
	if pr.chip != nil {
		objs = append(objs, pr.chip, pr.sc)
	}
	objs = append(objs, pr.label)
	if pr.cat != nil {
		objs = append(objs, pr.cat)
	}
	if pr.check != nil {
		objs = append(objs, pr.check)
	}
	if pr.box != nil {
		objs = append(objs, pr.box)
	}
	return objs
}
