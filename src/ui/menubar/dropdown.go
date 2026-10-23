package menubar

import (
	"image/color"
	"strings"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Timing of hover actions; tests set them to 0 to act synchronously.
var (
	openDelay = 180 * time.Millisecond // hover on a submenu row before it opens / closes
	safeDelay = 320 * time.Millisecond // grace while the pointer heads for an open submenu
)

const (
	rowHeight  = 24
	sepHeight  = 9
	panePadY   = 4
	labelSize  = 12.5
	scSize     = 11
	minPaneW   = 190
	iconColX   = 10
	labelX     = 32
	arrowColW  = 22
	paneRadius = 5
)

// row is a resolved menu entry.
type row struct {
	sep      bool
	label    string
	shortcut string // display text
	enabled  bool
	toggle   bool
	checked  bool
	icon     fyne.Resource
	sub      []command.Item // submenu items (nil when not a submenu)
	run      func()
}

func (r row) selectable() bool { return !r.sep && r.enabled }

// buildRows resolves menu items against the registry. Dynamic generators are expanded
// in place, separators are tidied and an empty result becomes "Keine Einträge".
func buildRows(reg *command.Registry, items []command.Item) []row {
	var rows []row
	var add func(items []command.Item, depth int)
	add = func(items []command.Item, depth int) {
		for _, it := range items {
			switch {
			case it.Separator:
				rows = append(rows, row{sep: true})
			case it.Dynamic != "":
				if depth < 4 {
					add(reg.DynamicItems(it.Dynamic), depth+1)
				}
			case it.Submenu != nil:
				label := it.Label
				if label == "" {
					label = it.Submenu.Title
				}
				rows = append(rows, row{label: label, enabled: true, sub: it.Submenu.Items})
			case it.Run != nil:
				r := row{label: it.Label, enabled: it.Enabled == nil || it.Enabled(), run: it.Run}
				if it.Command != "" {
					if c, ok := reg.Get(it.Command); ok {
						if r.label == "" {
							r.label = c.Label
						}
						r.shortcut = command.Display(c.Shortcut)
						r.icon = c.Icon
					}
				}
				r.toggle, r.checked = it.Checked != 0, it.Checked == 2
				rows = append(rows, r)
			case it.Command != "":
				id := it.Command
				r := row{label: it.Label}
				if c, ok := reg.Get(id); ok {
					if r.label == "" {
						r.label = c.MenuLabel
					}
					if r.label == "" {
						r.label = c.Label
					}
					r.shortcut = command.Display(c.Shortcut)
					r.enabled = c.Enabled
					r.toggle, r.checked = c.Toggle, c.Checked
					r.icon = c.Icon
				}
				if r.label == "" {
					r.label = id
				}
				r.run = func() { reg.Run(id) }
				rows = append(rows, r)
			default: // a plain label (e.g. a heading from a generator)
				rows = append(rows, row{label: it.Label, toggle: it.Checked != 0, checked: it.Checked == 2})
			}
		}
	}
	add(items, 0)
	// tidy separators: none leading, trailing or doubled
	out := rows[:0]
	for _, r := range rows {
		if r.sep && (len(out) == 0 || out[len(out)-1].sep) {
			continue
		}
		out = append(out, r)
	}
	for len(out) > 0 && out[len(out)-1].sep {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		out = append(out, row{label: "Keine Einträge"})
	}
	return out
}

// pane is one dropdown or submenu box.
type pane struct {
	widget.BaseWidget
	reg   *command.Registry
	items []command.Item
	rows  []row
	tops  []float32
	hover int  // highlighted row, -1 none
	sub   int  // row whose submenu is open, -1 none
	dim   bool // the highlight is the path to an active submenu (tinted, not Steel)
	size  fyne.Size

	bg, sh1, sh2 *canvas.Rectangle
	hl           *canvas.Rectangle
	objs         []fyne.CanvasObject // per-row objects
	labels       []*canvas.Text
	scs          []*canvas.Text
}

func newPane(reg *command.Registry, items []command.Item) *pane {
	p := &pane{reg: reg, items: items, hover: -1, sub: -1}
	p.ExtendBaseWidget(p)
	p.reload()
	return p
}

// reload resolves the rows again (labels, enabled, checked, dynamic lists).
func (p *pane) reload() {
	p.rows = buildRows(p.reg, p.items)
	if p.hover >= len(p.rows) || (p.hover >= 0 && !p.rows[p.hover].selectable()) {
		p.hover = -1
	}
	if p.sub >= len(p.rows) || (p.sub >= 0 && p.rows[p.sub].sub == nil) {
		p.sub = -1
	}
	p.measure()
	p.build()
}

func (p *pane) measure() {
	var lw, sw float32
	hasSub := false
	p.tops = make([]float32, len(p.rows))
	y := float32(panePadY)
	for i, r := range p.rows {
		p.tops[i] = y
		if r.sep {
			y += sepHeight
			continue
		}
		y += rowHeight
		if w := fyne.MeasureText(r.label, labelSize, fyne.TextStyle{}).Width; w > lw {
			lw = w
		}
		if r.shortcut != "" {
			if w := fyne.MeasureText(r.shortcut, scSize, fyne.TextStyle{Monospace: true}).Width; w > sw {
				sw = w
			}
		}
		if r.sub != nil {
			hasSub = true
		}
	}
	w := labelX + lw + 18
	if sw > 0 {
		w += 28 + sw
	}
	if hasSub {
		w += arrowColW
	} else {
		w += 4
	}
	if w < minPaneW {
		w = minPaneW
	}
	p.size = fyne.NewSize(float32(int(w+0.5)), y+panePadY)
}

func (p *pane) rowHeightAt(i int) float32 {
	if p.rows[i].sep {
		return sepHeight
	}
	return rowHeight
}

// rowAt returns the row index at y (pane coordinates), -1 outside.
func (p *pane) rowAt(y float32) int {
	for i := range p.rows {
		if y >= p.tops[i] && y < p.tops[i]+p.rowHeightAt(i) {
			return i
		}
	}
	return -1
}

func (p *pane) contains(pos fyne.Position) bool {
	o := p.Position()
	return pos.X >= o.X && pos.Y >= o.Y && pos.X < o.X+p.size.Width && pos.Y < o.Y+p.size.Height
}

// build creates the canvas objects for the current rows.
func (p *pane) build() {
	w, h := p.size.Width, p.size.Height
	p.sh2 = canvas.NewRectangle(color.NRGBA{0, 0, 0, 0x26})
	p.sh2.CornerRadius = paneRadius + 3
	p.sh2.Move(fyne.NewPos(-3, -1))
	p.sh2.Resize(fyne.NewSize(w+6, h+7))
	p.sh1 = canvas.NewRectangle(color.NRGBA{0, 0, 0, 0x50})
	p.sh1.CornerRadius = paneRadius + 1
	p.sh1.Move(fyne.NewPos(-1, 1))
	p.sh1.Resize(fyne.NewSize(w+2, h+3))
	p.bg = canvas.NewRectangle(studiotheme.Raised)
	p.bg.CornerRadius = paneRadius
	p.bg.StrokeColor = studiotheme.Border
	p.bg.StrokeWidth = 1
	p.bg.Resize(p.size)
	p.hl = canvas.NewRectangle(studiotheme.Steel)
	p.hl.CornerRadius = 3
	p.hl.Hide()
	p.objs = nil
	p.labels = make([]*canvas.Text, len(p.rows))
	p.scs = make([]*canvas.Text, len(p.rows))
	for i, r := range p.rows {
		y := p.tops[i]
		if r.sep {
			line := canvas.NewRectangle(studiotheme.Border)
			line.Move(fyne.NewPos(8, y+sepHeight/2))
			line.Resize(fyne.NewSize(w-16, 1))
			p.objs = append(p.objs, line)
			continue
		}
		var icon fyne.Resource
		switch {
		case r.toggle && r.checked:
			icon = theme.ConfirmIcon()
		case !r.toggle && r.icon != nil:
			icon = r.icon
		}
		if icon != nil {
			if !r.enabled {
				icon = theme.NewDisabledResource(icon)
			}
			img := canvas.NewImageFromResource(icon)
			img.FillMode = canvas.ImageFillContain
			img.Move(fyne.NewPos(iconColX, y+(rowHeight-15)/2))
			img.Resize(fyne.NewSize(15, 15))
			p.objs = append(p.objs, img)
		}
		lbl := canvas.NewText(r.label, studiotheme.Text)
		lbl.TextSize = labelSize
		ls := lbl.MinSize()
		lbl.Move(fyne.NewPos(labelX, y+(rowHeight-ls.Height)/2))
		lbl.Resize(ls)
		p.labels[i] = lbl
		p.objs = append(p.objs, lbl)
		right := w - 12
		if r.sub != nil {
			arrow := canvas.NewImageFromResource(theme.MenuExpandIcon())
			if !r.enabled {
				arrow.Resource = theme.NewDisabledResource(theme.MenuExpandIcon())
			}
			arrow.Move(fyne.NewPos(w-arrowColW, y+(rowHeight-18)/2))
			arrow.Resize(fyne.NewSize(18, 18))
			p.objs = append(p.objs, arrow)
		}
		if p.hasSubs() {
			right = w - arrowColW - 2
		}
		if r.shortcut != "" {
			sc := canvas.NewText(r.shortcut, studiotheme.Muted)
			sc.TextSize = scSize
			sc.TextStyle = fyne.TextStyle{Monospace: true}
			ss := sc.MinSize()
			sc.Move(fyne.NewPos(right-ss.Width, y+(rowHeight-ss.Height)/2))
			sc.Resize(ss)
			p.scs[i] = sc
			p.objs = append(p.objs, sc)
		}
	}
	p.paintHover()
	p.Refresh()
}

func (p *pane) hasSubs() bool {
	for _, r := range p.rows {
		if r.sub != nil {
			return true
		}
	}
	return false
}

func (p *pane) setHover(i int) {
	if i >= 0 && (i >= len(p.rows) || !p.rows[i].selectable()) {
		i = -1
	}
	if i == p.hover {
		return
	}
	p.hover = i
	p.paintHover()
	p.Refresh()
}

// paintHover moves the Steel highlight and recolours the texts.
func (p *pane) paintHover() {
	for i, r := range p.rows {
		if r.sep {
			continue
		}
		lc, sc := studiotheme.Text, studiotheme.Muted
		if !r.enabled {
			lc, sc = studiotheme.Faint, studiotheme.Faint
		}
		if i == p.hover && !p.dim {
			lc, sc = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}, color.NRGBA{0xFF, 0xFF, 0xFF, 0xC0}
		}
		if t := p.labels[i]; t != nil {
			t.Color = lc
		}
		if t := p.scs[i]; t != nil {
			t.Color = sc
		}
	}
	if p.hover < 0 {
		p.hl.Hide()
		return
	}
	p.hl.FillColor = studiotheme.Steel
	if p.dim {
		p.hl.FillColor = openTint()
	}
	p.hl.Move(fyne.NewPos(4, p.tops[p.hover]))
	p.hl.Resize(fyne.NewSize(p.size.Width-8, rowHeight))
	p.hl.Show()
}

// step moves the highlight to the next selectable row in direction dir (wrapping).
func (p *pane) step(dir int) {
	n := len(p.rows)
	i := p.hover
	for k := 0; k < n; k++ {
		if i < 0 {
			if dir > 0 {
				i = 0
			} else {
				i = n - 1
			}
		} else {
			i = (i + dir + n) % n
		}
		if p.rows[i].selectable() {
			p.setHover(i)
			return
		}
	}
}

func (p *pane) first() { p.hover = -1; p.step(1) }

func (p *pane) MinSize() fyne.Size { return p.size }

func (p *pane) CreateRenderer() fyne.WidgetRenderer { return &paneRenderer{p: p} }

type paneRenderer struct{ p *pane }

func (r *paneRenderer) Layout(fyne.Size)   {}
func (r *paneRenderer) MinSize() fyne.Size { return r.p.size }
func (r *paneRenderer) Refresh()           { canvas.Refresh(r.p) }
func (r *paneRenderer) Destroy()           {}
func (r *paneRenderer) Objects() []fyne.CanvasObject {
	p := r.p
	objs := []fyne.CanvasObject{p.sh2, p.sh1, p.bg, p.hl}
	return append(objs, p.objs...)
}

// layer is the full-canvas overlay that hosts the open panes and owns input while open.
type layer struct {
	widget.BaseWidget
	bar      *MenuBar
	panes    []*pane
	box      *fyne.Container
	keyboard bool // opened or driven by keys: show mnemonics, keep a selection
	last     fyne.Position
	gen      int
	timer    *time.Timer
	closed   bool
	altSolo  bool
}

func newLayer(m *MenuBar) *layer {
	l := &layer{bar: m, box: container.NewWithoutLayout()}
	l.ExtendBaseWidget(l)
	return l
}

func (l *layer) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(l.box) }

func (l *layer) reg() *command.Registry { return l.bar.reg }

func (l *layer) setRoot(items []command.Item, keyboard bool) {
	l.cancel()
	l.panes = nil
	p := newPane(l.reg(), items)
	if keyboard {
		p.first()
	}
	l.panes = []*pane{p}
	l.place(0)
	l.sync()
}

// chain tints the highlight of rows whose submenu holds the active selection.
func (l *layer) chain() {
	for d, p := range l.panes {
		dim := p.sub >= 0 && p.hover == p.sub && d+1 < len(l.panes) && l.panes[d+1].hover >= 0
		if dim != p.dim {
			p.dim = dim
			p.paintHover()
			p.Refresh()
		}
	}
}

func (l *layer) sync() {
	l.chain()
	objs := make([]fyne.CanvasObject, len(l.panes))
	for i, p := range l.panes {
		objs[i] = p
	}
	l.box.Objects = objs
	l.box.Refresh()
}

// place positions pane d: below its bar title, or beside its parent row (flipped to the
// left when it would leave the window).
func (l *layer) place(d int) {
	p := l.panes[d]
	p.Resize(p.size)
	cs := l.Size()
	var x, y float32
	if d == 0 {
		bp, bs := l.bar.barRect()
		x = bp.X + l.bar.titleX[l.bar.active]
		y = bp.Y + bs.Height
		if x+p.size.Width > cs.Width {
			x = cs.Width - p.size.Width
		}
	} else {
		par := l.panes[d-1]
		pp := par.Position()
		y = pp.Y + par.tops[par.sub] - panePadY
		x = pp.X + par.size.Width - 3
		if x+p.size.Width > cs.Width {
			x = pp.X - p.size.Width + 3
		}
		if y+p.size.Height > cs.Height {
			y = cs.Height - p.size.Height
		}
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	p.Move(fyne.NewPos(x, y))
}

// reload re-resolves all open panes after a registry change.
func (l *layer) reload() {
	for d, p := range l.panes {
		p.reload()
		if d+1 < len(l.panes) && p.sub < 0 {
			l.truncate(d + 1)
			break
		}
	}
	for d := range l.panes {
		l.place(d)
	}
	l.sync()
}

// truncate closes the panes from depth n on.
func (l *layer) truncate(n int) {
	if n >= len(l.panes) {
		return
	}
	l.panes = l.panes[:n]
	if n > 0 {
		l.panes[n-1].sub = -1
	}
	l.sync()
}

func (l *layer) openSub(d, ri int, keyboard bool) {
	par := l.panes[d]
	if ri < 0 || ri >= len(par.rows) || par.rows[ri].sub == nil {
		return
	}
	if par.sub == ri && len(l.panes) > d+1 {
		if keyboard {
			l.panes[d+1].first()
		}
		return
	}
	l.truncate(d + 1)
	par.sub = ri
	par.setHover(ri)
	p := newPane(l.reg(), par.rows[ri].sub)
	if keyboard {
		p.first()
	}
	l.panes = append(l.panes, p)
	l.place(d + 1)
	l.sync()
}

// activate runs a row (or opens its submenu). Disabled rows and separators do nothing.
func (l *layer) activate(d, ri int, keyboard bool) {
	p := l.panes[d]
	if ri < 0 || ri >= len(p.rows) || !p.rows[ri].selectable() {
		return
	}
	r := p.rows[ri]
	if r.sub != nil {
		l.cancel()
		l.openSub(d, ri, keyboard)
		return
	}
	l.bar.closeAll()
	if r.run != nil {
		r.run()
	}
}

// schedule runs fn after delay on the UI thread unless something else is scheduled or the
// menu closes first. A zero delay runs fn now.
func (l *layer) schedule(delay time.Duration, fn func()) {
	l.cancel()
	if delay <= 0 {
		fn()
		return
	}
	g := l.gen
	l.timer = time.AfterFunc(delay, func() {
		kit.Do(func() {
			if l.gen == g && !l.closed {
				l.timer = nil
				fn()
				l.chain()
			}
		})
	})
}

func (l *layer) cancel() {
	l.gen++
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
}

// hit returns the deepest pane and row under pos (-1, -1 when none).
func (l *layer) hit(pos fyne.Position) (int, int) {
	for d := len(l.panes) - 1; d >= 0; d-- {
		p := l.panes[d]
		if p.contains(pos) {
			return d, p.rowAt(pos.Y - p.Position().Y)
		}
	}
	return -1, -1
}

func (l *layer) inBar(pos fyne.Position) (int, bool) {
	bp, bs := l.bar.barRect()
	if pos.X < bp.X || pos.Y < bp.Y || pos.X >= bp.X+bs.Width || pos.Y >= bp.Y+bs.Height {
		return -1, false
	}
	return l.bar.indexAt(pos.X - bp.X), true
}

// towardsSub reports whether the pointer, moving from prev to pos inside pane d, heads for
// the open submenu: pos lies in the triangle between prev and the submenu's near edge.
func (l *layer) towardsSub(d int, prev, pos fyne.Position) bool {
	if d+1 >= len(l.panes) || prev == pos {
		return false
	}
	par, sub := l.panes[d], l.panes[d+1]
	sp := sub.Position()
	x := sp.X
	if sp.X < par.Position().X { // flipped to the left
		x = sp.X + sub.size.Width
	}
	return inTriangle(pos, prev, fyne.NewPos(x, sp.Y-4), fyne.NewPos(x, sp.Y+sub.size.Height+4))
}

func inTriangle(p, a, b, c fyne.Position) bool {
	cross := func(p1, p2, p3 fyne.Position) float32 {
		return (p1.X-p3.X)*(p2.Y-p3.Y) - (p2.X-p3.X)*(p1.Y-p3.Y)
	}
	d1, d2, d3 := cross(p, a, b), cross(p, b, c), cross(p, c, a)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func (l *layer) mouseAt(pos fyne.Position) {
	defer l.chain()
	prev := l.last
	l.last = pos
	if i, ok := l.inBar(pos); ok {
		l.bar.setHover(i)
		if i >= 0 && i != l.bar.active && len(l.panes) > 0 {
			l.bar.openMenu(i, false)
		}
		return
	}
	l.bar.setHover(-1)
	if len(l.panes) == 0 {
		return
	}
	d, ri := l.hit(pos)
	if d < 0 {
		deep := l.panes[len(l.panes)-1]
		if deep.sub < 0 {
			deep.setHover(-1)
		}
		return
	}
	l.keyboard = false
	p := l.panes[d]
	if d+1 < len(l.panes) && ri != p.sub && l.towardsSub(d, prev, pos) {
		l.schedule(safeDelay, func() {
			if d2, r2 := l.hit(l.last); d2 == d {
				l.hoverRow(d, r2)
			}
		})
		return
	}
	l.hoverRow(d, ri)
}

// hoverRow highlights row ri of pane d and opens or closes submenus after the open delay.
func (l *layer) hoverRow(d, ri int) {
	p := l.panes[d]
	if d > 0 {
		par := l.panes[d-1]
		par.setHover(par.sub)
	}
	if ri >= 0 && ri < len(p.rows) && p.rows[ri].selectable() {
		p.setHover(ri)
		if p.rows[ri].sub != nil {
			if p.sub == ri && len(l.panes) > d+1 {
				l.cancel()
				return
			}
			l.schedule(openDelay, func() { l.openSub(d, ri, false) })
			return
		}
	} else if p.sub < 0 {
		p.setHover(-1)
	}
	if len(l.panes) > d+1 {
		l.schedule(openDelay, func() {
			l.truncate(d + 1)
			p.setHover(ri)
		})
		return
	}
	l.cancel()
}

func (l *layer) MouseIn(ev *desktop.MouseEvent)    { l.mouseAt(ev.Position) }
func (l *layer) MouseMoved(ev *desktop.MouseEvent) { l.mouseAt(ev.Position) }
func (l *layer) MouseOut()                         {}

func (l *layer) Tapped(ev *fyne.PointEvent) {
	pos := ev.Position
	if d, ri := l.hit(pos); d >= 0 {
		l.activate(d, ri, false)
		return
	}
	if i, ok := l.inBar(pos); ok && i >= 0 {
		if i == l.bar.active && len(l.panes) > 0 {
			l.bar.closeAll()
			return
		}
		l.bar.openMenu(i, false)
		return
	}
	l.bar.closeAll()
}

func (l *layer) TappedSecondary(*fyne.PointEvent) { l.bar.closeAll() }

// Keyboard: the layer holds focus in its overlay while open.

func (l *layer) FocusGained() {}
func (l *layer) FocusLost() {
	if !l.closed && l.bar.layer == l {
		l.bar.closeAll()
	}
}

func (l *layer) TypedRune(r rune) {
	defer l.chain()
	if len(l.panes) == 0 {
		if i := l.bar.indexForRune(r); i >= 0 {
			l.bar.Open(i)
		}
		return
	}
	// jump to the next entry starting with that letter
	p := l.panes[len(l.panes)-1]
	r = unicode.ToLower(r)
	n := len(p.rows)
	for k := 1; k <= n; k++ {
		i := (p.hover + k + n) % n
		if p.hover < 0 {
			i = k - 1
		}
		row := p.rows[i]
		if row.selectable() && strings.HasPrefix(strings.ToLower(row.label), string(r)) {
			l.keyboard = true
			p.setHover(i)
			return
		}
	}
}

func (l *layer) TypedKey(ev *fyne.KeyEvent) {
	m := l.bar
	defer l.chain()
	l.keyboard = true
	l.cancel()
	if len(l.panes) == 0 { // bar focused
		switch ev.Name {
		case fyne.KeyLeft:
			m.focusBar((m.active - 1 + len(m.menus)) % len(m.menus))
		case fyne.KeyRight:
			m.focusBar((m.active + 1) % len(m.menus))
		case fyne.KeyDown, fyne.KeyUp, fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
			m.Open(m.active)
		case fyne.KeyEscape, fyne.KeyF10:
			m.closeAll()
		}
		return
	}
	d := len(l.panes) - 1
	p := l.panes[d]
	switch ev.Name {
	case fyne.KeyDown:
		p.step(1)
	case fyne.KeyUp:
		p.step(-1)
	case fyne.KeyHome:
		p.first()
	case fyne.KeyEnd:
		p.hover = -1
		p.step(-1)
	case fyne.KeyRight:
		if p.hover >= 0 && p.rows[p.hover].sub != nil {
			l.openSub(d, p.hover, true)
			return
		}
		m.Open((m.active + 1) % len(m.menus))
	case fyne.KeyLeft:
		if d > 0 {
			l.truncate(d)
			return
		}
		m.Open((m.active - 1 + len(m.menus)) % len(m.menus))
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		l.activate(d, p.hover, true)
	case fyne.KeyEscape:
		if d > 0 {
			l.truncate(d)
			return
		}
		m.closeAll()
	case fyne.KeyF10:
		m.closeAll()
	}
	if !l.closed {
		m.Refresh()
	}
}

// KeyDown/KeyUp track Alt while the menu is open: Alt alone closes it again, and the
// mnemonics stay visible while it is held.
func (l *layer) KeyDown(ev *fyne.KeyEvent) {
	if isAlt(ev.Name) {
		l.altSolo = true
		l.bar.altHeld = true
		l.bar.Refresh()
		return
	}
	l.altSolo = false
}

func (l *layer) KeyUp(ev *fyne.KeyEvent) {
	if !isAlt(ev.Name) {
		return
	}
	solo := l.altSolo
	l.altSolo = false
	l.bar.altHeld = false
	if solo {
		l.bar.closeAll()
		return
	}
	l.bar.Refresh()
}

// TypedShortcut: Alt+letter switches menus; any other shortcut (also the driver's own
// types such as Strg+Z) closes the menu and goes to the window's key handling
// (MenuBar.OnCombo), or to the registry.
func (l *layer) TypedShortcut(sc fyne.Shortcut) {
	l.altSolo = false
	if l.bar.HandleShortcut(sc) {
		return
	}
	key, mods, ok := command.ComboOf(sc)
	if !ok {
		return
	}
	l.bar.closeAll()
	if l.bar.OnCombo != nil {
		l.bar.OnCombo(key, mods)
		return
	}
	l.bar.reg.Dispatch(key, mods)
}
