package menubar

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	paletteListH = 380 // visible list height at most
	paletteRowH  = 28
	paletteSepH  = 9
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
	pop     *popup
	entry   *searchEntry
	list    *fyne.Container
	scroll  *container.Scroll
	entries []paletteEntry
	rows    []*paletteRow
	sel     int
}

// popup is the palette's overlay: the menu surface (same fill, edge, radius and shadow as
// the menus) at 12 % of the canvas height, with a transparent catcher around it that
// closes the palette on a click outside.
type popup struct {
	widget.BaseWidget
	cv      fyne.Canvas
	content fyne.CanvasObject
	shadow  []*canvas.Rectangle
	bg      *canvas.Rectangle
	pos     fyne.Position
	size    fyne.Size
	shown   bool
}

func newPopup(cv fyne.Canvas, content fyne.CanvasObject, size fyne.Size) *popup {
	st := menustyle
	s := kit.CanvasScale(cv)
	cs := cv.Size()
	if size.Width > cs.Width-4*st.ScreenMargin {
		size.Width = cs.Width - 4*st.ScreenMargin
	}
	pp := &popup{cv: cv, content: content, size: size}
	pp.pos = fyne.NewPos(kit.Snap((cs.Width-size.Width)/2, s), kit.Snap(cs.Height*0.12, s))
	if pp.pos.Y+size.Height > cs.Height-st.ScreenMargin {
		pp.pos.Y = st.ScreenMargin
	}
	for i := st.ShadowLayers; i >= 1; i-- {
		g := float32(i) * 2
		sh := canvas.NewRectangle(color.NRGBA{0, 0, 0, st.ShadowAlpha[i-1]})
		sh.CornerRadius = st.PaneRadius + g
		sh.Move(pp.pos.Add(fyne.NewPos(kit.Snap(-g, s), kit.Snap(-g+float32(i)*1.5, s))))
		sh.Resize(fyne.NewSize(kit.Snap(size.Width+2*g, s), kit.Snap(size.Height+2*g, s)))
		pp.shadow = append(pp.shadow, sh)
	}
	pp.bg = canvas.NewRectangle(studiotheme.MenuSurface)
	pp.bg.CornerRadius = st.PaneRadius
	pp.bg.StrokeColor = studiotheme.MenuEdge
	pp.bg.StrokeWidth = kit.Hair(s)
	pp.bg.Move(pp.pos)
	pp.bg.Resize(size)
	content.Move(pp.pos)
	content.Resize(size)
	pp.ExtendBaseWidget(pp)
	return pp
}

func (pp *popup) Show() {
	pp.shown = true
	pp.cv.Overlays().Add(pp)
	pp.Move(fyne.NewPos(0, 0))
	pp.Resize(pp.cv.Size())
}

// Hide removes the palette from the canvas.
func (pp *popup) Hide() {
	if !pp.shown {
		return
	}
	pp.shown = false
	pp.cv.Overlays().Remove(pp)
}

// Visible reports whether the palette is open.
func (pp *popup) Visible() bool { return pp.shown }

func (pp *popup) Tapped(ev *fyne.PointEvent) {
	p := ev.Position
	if p.X >= pp.pos.X && p.Y >= pp.pos.Y && p.X < pp.pos.X+pp.size.Width && p.Y < pp.pos.Y+pp.size.Height {
		return
	}
	pp.Hide()
}

func (pp *popup) CreateRenderer() fyne.WidgetRenderer { return &popupRenderer{pp: pp} }

type popupRenderer struct{ pp *popup }

func (r *popupRenderer) Layout(fyne.Size)   {}
func (r *popupRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 0) }
func (r *popupRenderer) Refresh()           { canvas.Refresh(r.pp) }
func (r *popupRenderer) Destroy()           {}
func (r *popupRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, 6)
	for _, s := range r.pp.shadow {
		objs = append(objs, s)
	}
	return append(objs, r.pp.bg, r.pp.content)
}

// searchTheme makes the search entry a plain text field inside the palette surface:
// no field fill, no border, 14 px text.
type searchTheme struct{ fyne.Theme }

func (t searchTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameInputBackground, theme.ColorNameInputBorder, theme.ColorNameFocus:
		return color.Transparent
	}
	return t.Theme.Color(n, v)
}

func (t searchTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameInputBorder:
		return 0
	}
	return t.Theme.Size(n)
}

func showPalette(w fyne.Window, reg *command.Registry) *palette {
	st := menustyle
	p := &palette{w: w, reg: reg, sel: -1}
	p.entry = newSearchEntry(p)
	p.entry.SetPlaceHolder("Befehl suchen…")
	p.entry.OnChanged = func(string) { p.filter() }
	p.list = container.New(&listLayout{})
	p.scroll = container.NewVScroll(container.New(layout.NewCustomPaddedLayout(st.PanePad, st.PanePad, 0, 0), p.list))

	icon := canvas.NewImageFromResource(theme.NewColoredResource(theme.SearchIcon(), studiotheme.ColorNameMuted))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSquareSize(st.IconSize))
	strut := canvas.NewRectangle(color.Transparent)
	strut.SetMinSize(fyne.NewSize(1, st.PaletteSearchH))
	fieldTheme := searchTheme{theme.Current()}
	field := container.New(vCenterFill{}, container.NewThemeOverride(p.entry, fieldTheme))
	search := container.NewStack(strut, container.NewBorder(nil, nil,
		container.New(layout.NewCustomPaddedLayout(0, 0, 16, 4), container.NewCenter(icon)), nil, field))
	line := canvas.NewRectangle(studiotheme.MenuSep)
	line.SetMinSize(fyne.NewSize(1, 1))
	content := container.NewBorder(container.New(layout.NewCustomPaddedVBoxLayout(0), search, line),
		nil, nil, nil, p.scroll)

	p.filter()
	listH := float32(paletteListH)
	if max := w.Canvas().Size().Height * 0.6; listH > max {
		listH = max
	}
	p.scroll.SetMinSize(fyne.NewSize(st.PaletteW, listH))
	size := fyne.NewSize(st.PaletteW, st.PaletteSearchH+1+listH)
	p.pop = newPopup(w.Canvas(), content, size)
	p.pop.Show()
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

// listLayout stacks rows without gaps (group headers are higher than they look: the
// first one is a row, later ones carry the 9 px separator slot).
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
	return fyne.NewSize(menustyle.PaletteW, h)
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
		if r.i > 0 {
			return fyne.NewSize(100, paletteRowH+paletteSepH)
		}
		return fyne.NewSize(100, paletteRowH)
	}
	return fyne.NewSize(100, paletteRowH)
}

func (r *paletteRow) CreateRenderer() fyne.WidgetRenderer {
	st := menustyle
	pr := &paletteRowRenderer{r: r, bg: canvas.NewRectangle(color.Transparent)}
	pr.bg.CornerRadius = st.ItemRadius
	if r.e.kind == kindHeader {
		pr.label = canvas.NewText(r.e.header, studiotheme.Muted)
		pr.label.TextSize = st.ShortcutSize
		if r.i > 0 {
			pr.sep = canvas.NewRectangle(studiotheme.MenuSep)
		}
	} else {
		pr.label = canvas.NewText(r.e.cmd.Label, studiotheme.MenuText)
		pr.label.TextSize = st.LabelSize
		pr.cat = canvas.NewText(r.e.cmd.Category, studiotheme.Muted)
		pr.cat.TextSize = st.ShortcutSize
		if r.e.cmd.Toggle { // on/off state like the menus: a check mark when on
			if r.e.cmd.Checked { // off shows nothing, like the menus
				pr.check = newGlyph(10, 8, 1.5, [][2]float64{{0.8, 4.4}, {3.8, 7.2}, {9.2, 0.8}})
			}
		}
		if sc := ShortcutText(command.Display(r.e.cmd.Shortcut), runtimeGOOS); sc != "" {
			pr.sc = canvas.NewText(sc, studiotheme.MenuShortcut)
			pr.sc.TextSize = st.ShortcutSize
		}
	}
	pr.update()
	return pr
}

type paletteRowRenderer struct {
	r          *paletteRow
	bg         *canvas.Rectangle
	sep        *canvas.Rectangle // group separator above a later header
	label, cat *canvas.Text
	sc         *canvas.Text
	check      *glyph // toggle that is on
}

const (
	catColW   = 120
	checkColW = 24 // state column of toggles, kept for every row so labels align
)

func (pr *paletteRowRenderer) Layout(size fyne.Size) {
	st := menustyle
	s := kit.ScaleOf(pr.r)
	top := float32(0)
	if pr.sep != nil {
		top = paletteSepH
		pr.sep.Move(fyne.NewPos(st.SepInset, kit.Snap(4, s)))
		pr.sep.Resize(fyne.NewSize(size.Width-2*st.SepInset, kit.Hair(s)))
	}
	band := size.Height - top
	pr.bg.Move(fyne.NewPos(st.PanePad, top))
	pr.bg.Resize(fyne.NewSize(size.Width-2*st.PanePad, band))
	ls := pr.label.MinSize()
	if pr.r.e.kind == kindHeader {
		pr.label.Move(fyne.NewPos(st.PanePad+st.PadSide, top+kit.CenterY(band, ls.Height, s)))
		pr.label.Resize(ls)
		return
	}
	left := st.PanePad + st.PadSide
	cs := pr.cat.MinSize()
	pr.cat.Move(fyne.NewPos(left, top+kit.CenterY(band, cs.Height, s)))
	pr.cat.Resize(cs)
	pr.label.Move(fyne.NewPos(left+catColW+checkColW, top+kit.CenterY(band, ls.Height, s)))
	pr.label.Resize(ls)
	if pr.check != nil {
		pr.check.obj.Resize(fyne.NewSize(10, 8))
		pr.check.obj.Move(fyne.NewPos(kit.Snap(left+catColW, s), top+kit.CenterY(band, 8, s)))
	}
	if pr.sc != nil {
		ss := pr.sc.MinSize()
		pr.sc.Move(fyne.NewPos(kit.Snap(size.Width-st.PanePad-st.PadSide-ss.Width, s), top+kit.CenterY(band, ss.Height, s)))
		pr.sc.Resize(ss)
	}
}

func (pr *paletteRowRenderer) update() {
	r := pr.r
	if r.e.kind == kindHeader {
		return
	}
	sel := r.p.sel == r.i
	enabled := r.e.cmd.Enabled
	pr.label.Color, pr.cat.Color = studiotheme.MenuText, studiotheme.Muted
	if !enabled {
		pr.label.Color, pr.cat.Color = studiotheme.MenuDisabled, studiotheme.MenuDisabled
	}
	pr.bg.FillColor = color.Transparent
	if sel {
		pr.bg.FillColor = studiotheme.MenuAccentRow()
		if !enabled {
			pr.bg.FillColor = studiotheme.MenuHover
		}
	}
	if pr.check != nil {
		pr.check.setColor(pr.label.Color)
	}
	if pr.sc != nil {
		pr.sc.Color = studiotheme.MenuShortcut
		if !enabled {
			pr.sc.Color = studiotheme.MenuDisabled
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
	if pr.sep != nil {
		objs = append(objs, pr.sep)
	}
	if pr.sc != nil {
		objs = append(objs, pr.sc)
	}
	objs = append(objs, pr.label)
	if pr.cat != nil {
		objs = append(objs, pr.cat)
	}
	if pr.check != nil {
		objs = append(objs, pr.check.obj)
	}
	return objs
}

// vCenterFill gives its child the full width and its own minimum height, centred
// vertically, so the text sits on the centre line of the search icon.
type vCenterFill struct{}

func (vCenterFill) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		h := o.MinSize().Height
		o.Resize(fyne.NewSize(size.Width, h))
		o.Move(fyne.NewPos(0, kit.CenterY(size.Height, h, 1)))
	}
}

func (vCenterFill) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m
}
