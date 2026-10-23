package menubar

import (
	"strings"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
)

// CaptionDown, when set, is asked for a primary mouse press outside the open menu: it
// returns true when the point is on the window's caption (the custom title bar) and has
// started a window drag. The menu closes then; every other outside press only closes it.
var CaptionDown func(c fyne.Canvas, pos fyne.Position) bool

// layer is the full-canvas overlay that hosts the open panes and owns input while open. It
// serves the bar menus (bar != nil: pane 0 hangs below the title) and context menus
// (bar == nil: pane 0 opens at the pointer). Both use the same panes, keyboard and mouse
// handling.
type layer struct {
	widget.BaseWidget
	bar    *MenuBar
	cv     fyne.Canvas
	reg    *command.Registry
	root   func() []row
	anchor fyne.Position // context menu: where the pointer was
	panes  []*pane
	box    *fyne.Container

	keyboard bool // opened or driven by keys: show mnemonics, keep a selection
	last     fyne.Position
	gen      int
	timer    *time.Timer
	closed   bool
	altSolo  bool
	openedAt time.Time
	prev     fyne.Focusable // context menu: focus to give back
}

func newLayer(bar *MenuBar, cv fyne.Canvas) *layer {
	l := &layer{bar: bar, cv: cv, box: container.NewWithoutLayout(), openedAt: time.Now()}
	if bar != nil {
		l.reg = bar.reg
	}
	l.ExtendBaseWidget(l)
	return l
}

func (l *layer) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(l.box) }

func (l *layer) scale() float32 { return kit.CanvasScale(l.cv) }

func (l *layer) canvasSize() fyne.Size {
	if s := l.Size(); s.Width > 0 && s.Height > 0 {
		return s
	}
	return l.cv.Size()
}

// maxPaneH is the height a pane may take before it scrolls.
func (l *layer) maxPaneH() float32 { return l.canvasSize().Height - 4*menustyle.ScreenMargin }

func (l *layer) maxPaneW() float32 { return l.canvasSize().Width - 4*menustyle.ScreenMargin }

func (l *layer) newPane(src func() []row) *pane {
	return newPane(src, l.scale(), l.maxPaneH(), l.maxPaneW())
}

func (l *layer) setRoot(keyboard bool) {
	l.cancel()
	l.panes = nil
	p := l.newPane(l.root)
	if keyboard {
		p.first()
	}
	l.panes = []*pane{p}
	l.place(0)
	l.sync()
	l.openedAt = time.Now()
}

func (l *layer) sync() {
	objs := make([]fyne.CanvasObject, len(l.panes))
	for i, p := range l.panes {
		objs[i] = p
	}
	l.box.Objects = objs
	l.box.Refresh()
}

// place positions pane d: below its bar title (bar menu) or at the pointer (context
// menu), or beside its parent row; flipped to the other side and moved when it would
// leave the canvas.
func (l *layer) place(d int) {
	st := menustyle
	p := l.panes[d]
	p.setMaxH(l.maxPaneH())
	p.Resize(p.size)
	cs := l.canvasSize()
	m := st.ScreenMargin
	var x, y float32
	switch {
	case d == 0 && l.bar != nil:
		bp, bs := l.bar.barRect()
		x = bp.X + l.bar.titleX[l.bar.active]
		y = bp.Y + bs.Height - st.BarOverlap
	case d == 0:
		x, y = l.anchor.X, l.anchor.Y
		if x+p.size.Width > cs.Width-m {
			x = l.anchor.X - p.size.Width
		}
		if y+p.size.Height > cs.Height-m {
			y = l.anchor.Y - p.size.Height
		}
	default:
		par := l.panes[d-1]
		pp := par.Position()
		y = pp.Y + par.tops[par.sub] - st.SubDrop
		x = pp.X + par.size.Width - st.SubOverlap
		if x+p.size.Width > cs.Width-m {
			x = pp.X - p.size.Width + st.SubOverlap
		}
	}
	if x+p.size.Width > cs.Width-m {
		x = cs.Width - m - p.size.Width
	}
	if y+p.size.Height > cs.Height-m {
		y = cs.Height - m - p.size.Height
	}
	if x < m {
		x = m
	}
	if y < m {
		y = m
	}
	p.Move(fyne.NewPos(p.snap(x), p.snap(y)))
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
	if d+1 >= 2 { // the bar nests one submenu level only
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
	p := l.newPane(par.rows[ri].sub)
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
	l.closeAll()
	if r.run != nil {
		r.run()
	}
}

// closeAll closes the whole menu.
func (l *layer) closeAll() {
	if l.bar != nil {
		l.bar.closeAll()
		return
	}
	l.shut(true)
}

// shut removes the overlay of a context menu (bar menus: MenuBar.closeAll).
func (l *layer) shut(restoreFocus bool) {
	if l.closed {
		return
	}
	l.closed = true
	l.cancel()
	l.cv.Overlays().Remove(l)
	if ctxLayer == l {
		ctxLayer = nil
	}
	if restoreFocus && l.prev != nil && l.cv.Focused() == fyne.Focusable(l) {
		l.cv.Focus(l.prev)
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
	if l.bar == nil {
		return -1, false
	}
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
	prev := l.last
	l.last = pos
	if i, ok := l.inBar(pos); ok {
		l.bar.setHover(i)
		if i >= 0 && i != l.bar.active && len(l.panes) > 0 {
			l.bar.openMenu(i, false)
		}
		return
	}
	if l.bar != nil {
		l.bar.setHover(-1)
	}
	if len(l.panes) == 0 {
		return
	}
	d, ri := l.hit(pos)
	if d < 0 {
		l.cancel()
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

// hoverRow highlights row ri of pane d and opens or closes submenus after the delays.
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
		// a plain row (or the gap between rows) closes the old submenu at once, the
		// pointer leaving towards it is handled by the safe triangle
		l.cancel()
		l.truncate(d + 1)
		p.setHover(ri)
		return
	}
	l.cancel()
}

func (l *layer) MouseIn(ev *desktop.MouseEvent)    { l.mouseAt(ev.Position) }
func (l *layer) MouseMoved(ev *desktop.MouseEvent) { l.mouseAt(ev.Position) }
func (l *layer) MouseOut()                         {}

// MouseDown starts a window drag when the press lands on the window caption; the menu
// closes and the press does not reach anything else.
func (l *layer) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	if d, _ := l.hit(ev.Position); d >= 0 {
		return
	}
	if _, ok := l.inBar(ev.Position); ok {
		return
	}
	if CaptionDown != nil && CaptionDown(l.cv, ev.AbsolutePosition) {
		l.closeAll()
	}
}

func (l *layer) MouseUp(*desktop.MouseEvent) {}

func (l *layer) Tapped(ev *fyne.PointEvent) {
	pos := ev.Position
	if d, ri := l.hit(pos); d >= 0 {
		if l.bar == nil && time.Since(l.openedAt) < ignoreUpWithin {
			return // the release of the press that opened the context menu
		}
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
	l.closeAll()
}

func (l *layer) TappedSecondary(*fyne.PointEvent) { l.closeAll() }

// Scrolled forwards the wheel to the scrolling pane under the pointer.
func (l *layer) Scrolled(ev *fyne.ScrollEvent) {
	for d := len(l.panes) - 1; d >= 0; d-- {
		p := l.panes[d]
		if p.scroll != nil && p.contains(ev.Position) {
			p.scroll.Scrolled(ev)
			return
		}
	}
}

// Keyboard: the layer holds focus in its overlay while open.

func (l *layer) FocusGained() {}
func (l *layer) FocusLost() {
	if l.closed {
		return
	}
	if l.bar != nil {
		if l.bar.layer == l {
			l.bar.closeAll()
		}
		return
	}
	l.shut(false)
}

func (l *layer) TypedRune(r rune) {
	if len(l.panes) == 0 {
		if l.bar != nil {
			if i := l.bar.indexForRune(r); i >= 0 {
				l.bar.Open(i)
			}
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
	l.keyboard = true
	l.cancel()
	if len(l.panes) == 0 { // bar focused
		if m == nil || len(m.menus) == 0 {
			l.closeAll()
			return
		}
		switch ev.Name {
		case fyne.KeyLeft:
			m.focusBar((m.active - 1 + len(m.menus)) % len(m.menus))
		case fyne.KeyRight:
			m.focusBar((m.active + 1) % len(m.menus))
		case fyne.KeyHome:
			m.focusBar(0)
		case fyne.KeyEnd:
			m.focusBar(len(m.menus) - 1)
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
		p.last()
	case fyne.KeyPageDown:
		p.page(menustyle.PageRows)
	case fyne.KeyPageUp:
		p.page(-menustyle.PageRows)
	case fyne.KeyRight:
		if p.hover >= 0 && p.rows[p.hover].sub != nil && p.rows[p.hover].selectable() {
			l.openSub(d, p.hover, true)
			return
		}
		if m != nil {
			m.Open((m.active + 1) % len(m.menus))
		}
	case fyne.KeyLeft:
		if d > 0 {
			l.truncate(d)
			return
		}
		if m != nil {
			m.Open((m.active - 1 + len(m.menus)) % len(m.menus))
		}
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
		l.activate(d, p.hover, true)
	case fyne.KeyEscape:
		switch {
		case d > 0:
			l.truncate(d)
		case m != nil:
			m.focusBar(m.active) // menu -> keyboard mode on the bar
		default:
			l.closeAll()
		}
		return
	case fyne.KeyF10:
		l.closeAll()
	}
	if !l.closed && m != nil {
		m.Refresh()
	}
}

// KeyDown/KeyUp track Alt while the menu is open: Alt alone closes it again, and the
// mnemonics stay visible while it is held.
func (l *layer) KeyDown(ev *fyne.KeyEvent) {
	if l.bar == nil {
		return
	}
	if isAlt(ev.Name) {
		l.altSolo = true
		l.bar.altHeld = true
		l.bar.Refresh()
		return
	}
	l.altSolo = false
}

func (l *layer) KeyUp(ev *fyne.KeyEvent) {
	if l.bar == nil || !isAlt(ev.Name) {
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
	if l.bar == nil {
		// context menu: close it and hand the combo to the window
		if key, mods, ok := command.ComboOf(sc); ok {
			l.shut(true)
			if ContextCombo != nil {
				ContextCombo(key, mods)
			}
		}
		return
	}
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

// ContextCombo, when set, receives the key combinations typed while a context menu is
// open (the menu closes first), so app shortcuts keep working.
var ContextCombo func(key fyne.KeyName, mods fyne.KeyModifier)

// ctxLayer is the open context menu (one at a time).
var ctxLayer *layer

// ShowContextMenu opens a context menu with items at pos (canvas coordinates, top-left of
// the first pane; the pane flips left/up when it would leave the canvas). It is the one
// menu component of the app: the same panes, metrics, shadow and keyboard handling as the
// bar menus. Esc closes everything. Empty menus open nothing.
func ShowContextMenu(c fyne.Canvas, pos fyne.Position, items []Item) {
	rows := rowsOf(items)
	if c == nil || len(rows) == 0 {
		return
	}
	if ctxLayer != nil {
		ctxLayer.shut(false)
	}
	l := newLayer(nil, c)
	l.prev = c.Focused()
	l.anchor = pos
	l.root = func() []row { return rows }
	ctxLayer = l
	c.Overlays().Add(l)
	l.Move(fyne.NewPos(0, 0))
	l.Resize(c.Size())
	c.Focus(l)
	l.setRoot(false)
}

// CloseContextMenu closes the open context menu, if any.
func CloseContextMenu() {
	if ctxLayer != nil {
		ctxLayer.shut(false)
	}
}
