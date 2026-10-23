// Package menubar is the custom in-window menu bar (replacing Fyne's native main menu):
// graphite bar with dropdowns, submenus, check marks, shortcuts and keyboard navigation,
// built from command.MenuBar and the registry.
//
// OWNER: menubar agent. Exported signatures in this file are a contract.
//
// The bar is a plain widget in the window content. Opening a menu adds a full-canvas
// overlay (the menu layer) that owns the mouse and keyboard until it closes: it draws the
// dropdown panes, switches menus when the pointer moves over another title, opens
// submenus on hover and closes on a click outside, Esc or when the window loses focus.
//
// The shell forwards window keys so the bar can react without focus:
//
//	desktop canvas OnKeyDown  -> HandleKeyDown (tracks Alt for mnemonics)
//	desktop canvas OnKeyUp    -> HandleKeyUp   (Alt pressed and released alone focuses the bar)
//	canvas OnTypedKey         -> HandleKey     (F10 focuses the bar)
//	canvas shortcut handler   -> HandleShortcut (Alt+letter opens a menu)
//
// UI thread only.
package menubar

import (
	"image/color"
	"strings"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	barHeight = 27
	titleSize = 12.5
	titlePadX = 9
)

type MenuBar struct {
	widget.BaseWidget
	reg   *command.Registry
	menus []command.Menu
	win   fyne.Window

	mnemonics []rune
	titleX    []float32
	titleW    []float32

	hover   int // title under the mouse while no menu is open
	active  int // open menu or keyboard-focused title, -1 when closed
	altHeld bool
	altSolo bool // Alt went down and no other key followed yet

	layer *layer
	unsub func()
}

// New builds the bar for window w. Leading/trailing objects (logo, tabs, status) are
// placed by the shell around it.
func New(w fyne.Window, reg *command.Registry, menus []command.Menu) *MenuBar {
	m := &MenuBar{reg: reg, menus: menus, win: w, hover: -1, active: -1}
	m.mnemonics = mnemonics(menus)
	m.measure()
	m.ExtendBaseWidget(m)
	return m
}

// mnemonics picks one letter per title: the first letter not yet taken, scanning the
// title from the start (Arbeitsbereich gets R because Ansicht took A).
func mnemonics(menus []command.Menu) []rune {
	used := map[rune]bool{}
	out := make([]rune, len(menus))
	for i, mn := range menus {
		for _, r := range strings.ToLower(mn.Title) {
			if unicode.IsLetter(r) && !used[r] {
				used[r] = true
				out[i] = r
				break
			}
		}
	}
	return out
}

func (m *MenuBar) measure() {
	m.titleX = make([]float32, len(m.menus))
	m.titleW = make([]float32, len(m.menus))
	x := float32(0)
	for i, mn := range m.menus {
		w := fyne.MeasureText(mn.Title, titleSize, fyne.TextStyle{}).Width + 2*titlePadX
		m.titleX[i], m.titleW[i] = x, w
		x += w
	}
}

func (m *MenuBar) indexAt(x float32) int {
	for i := range m.menus {
		if x >= m.titleX[i] && x < m.titleX[i]+m.titleW[i] {
			return i
		}
	}
	return -1
}

func (m *MenuBar) indexForRune(r rune) int {
	r = unicode.ToLower(r)
	for i, mn := range m.mnemonics {
		if mn == r && mn != 0 {
			return i
		}
	}
	return -1
}

// Open opens the menu at index i (Alt+letter / F10 navigation) with its first entry selected.
func (m *MenuBar) Open(i int) { m.openMenu(i, true) }

// Close closes any open menu and stops pending timers.
func (m *MenuBar) Close() { m.closeAll() }

// IsOpen reports whether a menu is open or the bar has keyboard focus.
func (m *MenuBar) IsOpen() bool { return m.layer != nil }

// HandleAlt toggles keyboard focus on the bar (Alt pressed alone, F10).
func (m *MenuBar) HandleAlt() {
	if m.layer != nil {
		m.closeAll()
		return
	}
	if len(m.menus) > 0 {
		m.focusBar(0)
	}
}

// HandleKey handles a typed key of the window (F10). It returns true when it used the key.
func (m *MenuBar) HandleKey(ev *fyne.KeyEvent) bool {
	if m.layer != nil {
		m.layer.TypedKey(ev)
		return true
	}
	if ev.Name == fyne.KeyF10 {
		m.HandleAlt()
		return true
	}
	return false
}

// HandleKeyDown tracks the Alt key (call from the desktop canvas OnKeyDown). Never consumes.
func (m *MenuBar) HandleKeyDown(ev *fyne.KeyEvent) bool {
	if isAlt(ev.Name) {
		if !m.altHeld {
			m.altHeld, m.altSolo = true, true
			m.Refresh()
		}
		return false
	}
	m.altSolo = false
	return false
}

// HandleKeyUp finishes an Alt press (call from the desktop canvas OnKeyUp): Alt pressed and
// released without another key focuses the bar. Returns true when it did.
func (m *MenuBar) HandleKeyUp(ev *fyne.KeyEvent) bool {
	if !isAlt(ev.Name) {
		return false
	}
	solo := m.altHeld && m.altSolo
	m.altHeld, m.altSolo = false, false
	m.Refresh()
	if solo {
		m.HandleAlt()
		return true
	}
	return false
}

// HandleShortcut opens the menu of an Alt+letter mnemonic. Returns true when it did.
func (m *MenuBar) HandleShortcut(sc fyne.Shortcut) bool {
	cs, ok := sc.(*desktop.CustomShortcut)
	if !ok || cs.Modifier != fyne.KeyModifierAlt {
		return false
	}
	name := []rune(string(cs.KeyName))
	if len(name) != 1 {
		return false
	}
	i := m.indexForRune(name[0])
	if i < 0 {
		return false
	}
	m.altSolo = false
	m.Open(i)
	return true
}

func isAlt(k fyne.KeyName) bool { return k == desktop.KeyAltLeft || k == desktop.KeyAltRight }

func (m *MenuBar) canvas() fyne.Canvas {
	if c := fyne.CurrentApp().Driver().CanvasForObject(m); c != nil {
		return c
	}
	return m.win.Canvas()
}

func (m *MenuBar) ensureLayer() *layer {
	if m.layer != nil {
		return m.layer
	}
	c := m.canvas()
	l := newLayer(m)
	m.layer = l
	c.Overlays().Add(l)
	l.Move(fyne.NewPos(0, 0))
	l.Resize(c.Size())
	c.Focus(l)
	m.unsub = m.reg.Subscribe(func() {
		if m.layer == l {
			l.reload()
		}
	})
	return l
}

func (m *MenuBar) openMenu(i int, keyboard bool) {
	if i < 0 || i >= len(m.menus) {
		return
	}
	l := m.ensureLayer()
	m.active = i
	m.hover = -1
	l.keyboard = keyboard
	l.setRoot(m.menus[i].Items, keyboard)
	m.Refresh()
}

func (m *MenuBar) focusBar(i int) {
	l := m.ensureLayer()
	l.keyboard = true
	l.truncate(0)
	m.active = i
	m.Refresh()
}

func (m *MenuBar) closeAll() {
	l := m.layer
	if l == nil {
		return
	}
	m.layer = nil
	l.closed = true
	l.cancel()
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	m.canvas().Overlays().Remove(l)
	m.active, m.hover = -1, -1
	m.Refresh()
}

// barRect returns the absolute position and size of the bar.
func (m *MenuBar) barRect() (fyne.Position, fyne.Size) {
	return fyne.CurrentApp().Driver().AbsolutePositionForObject(m), m.Size()
}

func (m *MenuBar) setHover(i int) {
	if m.hover != i {
		m.hover = i
		m.Refresh()
	}
}

func (m *MenuBar) Tapped(ev *fyne.PointEvent) {
	if i := m.indexAt(ev.Position.X); i >= 0 {
		m.openMenu(i, false)
	}
}

func (m *MenuBar) MouseIn(ev *desktop.MouseEvent)    { m.setHover(m.indexAt(ev.Position.X)) }
func (m *MenuBar) MouseMoved(ev *desktop.MouseEvent) { m.setHover(m.indexAt(ev.Position.X)) }
func (m *MenuBar) MouseOut()                         { m.setHover(-1) }

func (m *MenuBar) MinSize() fyne.Size {
	w := float32(0)
	if n := len(m.titleX); n > 0 {
		w = m.titleX[n-1] + m.titleW[n-1]
	}
	return fyne.NewSize(w, barHeight)
}

func (m *MenuBar) CreateRenderer() fyne.WidgetRenderer {
	r := &barRenderer{m: m, bg: canvas.NewRectangle(studiotheme.Gap)}
	for _, mn := range m.menus {
		hl := canvas.NewRectangle(color.Transparent)
		hl.CornerRadius = 3
		t := canvas.NewText(mn.Title, studiotheme.Text)
		t.TextSize = titleSize
		u := canvas.NewRectangle(studiotheme.Text)
		u.Hide()
		r.hl, r.text, r.under = append(r.hl, hl), append(r.text, t), append(r.under, u)
	}
	r.update()
	return r
}

// openTint is Raised tinted towards Steel: the title whose menu is open.
var openTint = mix(studiotheme.Raised, studiotheme.Steel, 0.3)

func mix(a, b color.NRGBA, t float32) color.NRGBA {
	f := func(x, y uint8) uint8 { return uint8(float32(x)*(1-t) + float32(y)*t + 0.5) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 0xFF}
}

type barRenderer struct {
	m     *MenuBar
	bg    *canvas.Rectangle
	hl    []*canvas.Rectangle
	text  []*canvas.Text
	under []*canvas.Rectangle
}

func (r *barRenderer) Layout(size fyne.Size) {
	m := r.m
	r.bg.Resize(size)
	h := size.Height
	for i := range r.text {
		x, w := m.titleX[i], m.titleW[i]
		r.hl[i].Move(fyne.NewPos(x+1, 3))
		r.hl[i].Resize(fyne.NewSize(w-2, h-6))
		ts := r.text[i].MinSize()
		ty := (h - ts.Height) / 2
		r.text[i].Move(fyne.NewPos(x+titlePadX, ty))
		r.text[i].Resize(ts)
		// mnemonic underline under its letter
		title := m.menus[i].Title
		pos := strings.IndexFunc(strings.ToLower(title), func(c rune) bool { return c == m.mnemonics[i] })
		if pos >= 0 {
			_, n := firstRune(title[pos:])
			pre := fyne.MeasureText(title[:pos], titleSize, fyne.TextStyle{}).Width
			lw := fyne.MeasureText(title[pos:pos+n], titleSize, fyne.TextStyle{}).Width
			r.under[i].Move(fyne.NewPos(x+titlePadX+pre, ty+ts.Height-3))
			r.under[i].Resize(fyne.NewSize(lw, 1))
		}
	}
}

func firstRune(s string) (rune, int) {
	for i, c := range s {
		if i == 0 {
			n := len(string(c))
			return c, n
		}
	}
	return 0, 0
}

func (r *barRenderer) MinSize() fyne.Size { return r.m.MinSize() }

func (r *barRenderer) update() {
	m := r.m
	showMnemonic := m.altHeld || (m.layer != nil && m.layer.keyboard)
	for i := range r.text {
		switch {
		case i == m.active:
			r.hl[i].FillColor = openTint
		case i == m.hover:
			r.hl[i].FillColor = studiotheme.Raised
		default:
			r.hl[i].FillColor = color.Transparent
		}
		r.text[i].Color = studiotheme.Text
		if i == m.active {
			r.text[i].Color = color.White
		}
		if showMnemonic && m.mnemonics[i] != 0 {
			r.under[i].FillColor = r.text[i].Color
			r.under[i].Show()
		} else {
			r.under[i].Hide()
		}
	}
}

func (r *barRenderer) Refresh() {
	r.update()
	r.Layout(r.m.Size())
	canvas.Refresh(r.m)
}

func (r *barRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.bg}
	for i := range r.text {
		objs = append(objs, r.hl[i], r.text[i], r.under[i])
	}
	return objs
}

func (r *barRenderer) Destroy() {}
