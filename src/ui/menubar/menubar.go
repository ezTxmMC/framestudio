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
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

type MenuBar struct {
	widget.BaseWidget
	reg   *command.Registry
	menus []command.Menu
	win   fyne.Window

	mnemonics []rune
	titleX    []float32
	titleW    []float32

	height  float32 // bar height the host gave (28, compact 24)
	hover   int     // title under the mouse while no menu is open
	pressed int     // title with the primary button down while closed
	active  int     // open menu or keyboard-focused title, -1 when closed
	altHeld bool
	altSolo bool // Alt went down and no other key followed yet

	layer *layer
	unsub func()

	// OnCombo, when set, receives the key combinations typed while a menu is open (the
	// menu closes first), so the window's aliases (Alt+1…5, Strg+Y) and keyboard rules
	// apply as without the menu. Without it the registry dispatches them.
	OnCombo func(key fyne.KeyName, mods fyne.KeyModifier)
}

// New builds the bar for window w. Leading/trailing objects (logo, tabs, status) are
// placed by the shell around it.
func New(w fyne.Window, reg *command.Registry, menus []command.Menu) *MenuBar {
	m := &MenuBar{reg: reg, menus: menus, win: w, hover: -1, active: -1, pressed: -1, height: 28}
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

// titleMetrics are the title text size and side padding for the current bar height.
func (m *MenuBar) titleMetrics() (size, padX float32) {
	if m.height <= compactBar {
		return menustyle.CompactTitle, menustyle.CompactPadX
	}
	return menustyle.TitleSize, menustyle.TitlePadX
}

func (m *MenuBar) measure() {
	m.titleX = make([]float32, len(m.menus))
	m.titleW = make([]float32, len(m.menus))
	size, padX := m.titleMetrics()
	x := float32(0)
	for i, mn := range m.menus {
		w := fyne.MeasureText(mn.Title, size, fyne.TextStyle{}).Width + 2*padX
		m.titleX[i], m.titleW[i] = x, w
		x += w
	}
}

// SetHeight sets the bar height (chrome.BarHeight: 28, or 24 compact). The titles, pills
// and MinSize follow it.
func (m *MenuBar) SetHeight(h float32) {
	if h <= 0 || h == m.height {
		return
	}
	m.height = h
	m.measure()
	m.Refresh()
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

// WindowFocusLost forgets a held Alt key (call when the window loses the keyboard, e.g.
// from Lifecycle OnExitedForeground). After a focus loss GLFW sends a synthetic release
// for every pressed key; the Alt of Alt+Tab would otherwise look like Alt pressed alone
// and leave the bar holding the keyboard when the user comes back.
func (m *MenuBar) WindowFocusLost() {
	if m.altHeld || m.altSolo {
		m.altHeld, m.altSolo = false, false
		m.Refresh()
	}
	if m.layer != nil {
		m.layer.altSolo = false
	}
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
	l := newLayer(m, c)
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
	items := m.menus[i].Items
	l.root = func() []row { return buildRows(m.reg, items) }
	l.setRoot(keyboard)
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
	l.shut(false)
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	m.active, m.hover, m.pressed = -1, -1, -1
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
	m.pressed = -1
	if i := m.indexAt(ev.Position.X); i >= 0 {
		m.openMenu(i, false)
	}
}

// MouseDown/MouseUp show the pressed look of a closed title.
func (m *MenuBar) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary || m.layer != nil {
		return
	}
	if i := m.indexAt(ev.Position.X); i >= 0 && i != m.pressed {
		m.pressed = i
		m.Refresh()
	}
}

func (m *MenuBar) MouseUp(*desktop.MouseEvent) {
	if m.pressed >= 0 {
		m.pressed = -1
		m.Refresh()
	}
}

func (m *MenuBar) MouseIn(ev *desktop.MouseEvent)    { m.setHover(m.indexAt(ev.Position.X)) }
func (m *MenuBar) MouseMoved(ev *desktop.MouseEvent) { m.setHover(m.indexAt(ev.Position.X)) }
func (m *MenuBar) MouseOut() {
	m.setHover(-1)
	if m.pressed >= 0 {
		m.pressed = -1
		m.Refresh()
	}
}

// MinSize is the width of all titles at the height the host set with SetHeight.
func (m *MenuBar) MinSize() fyne.Size {
	w := float32(0)
	if n := len(m.titleX); n > 0 {
		w = m.titleX[n-1] + m.titleW[n-1]
	}
	return fyne.NewSize(w, m.height)
}

func (m *MenuBar) CreateRenderer() fyne.WidgetRenderer {
	size, _ := m.titleMetrics()
	r := &barRenderer{m: m}
	for _, mn := range m.menus {
		hl := canvas.NewRectangle(color.Transparent)
		hl.CornerRadius = menustyle.PillRadius
		t := canvas.NewText(mn.Title, studiotheme.MenuText)
		t.TextSize = size
		u := canvas.NewRectangle(studiotheme.MenuText)
		u.Hide()
		r.hl, r.text, r.under = append(r.hl, hl), append(r.text, t), append(r.under, u)
	}
	r.focus = canvas.NewRectangle(color.Transparent)
	r.focus.CornerRadius = menustyle.PillRadius
	r.focus.StrokeWidth = 1
	r.focus.Hide()
	r.update()
	return r
}

type barRenderer struct {
	m     *MenuBar
	hl    []*canvas.Rectangle
	text  []*canvas.Text
	under []*canvas.Rectangle
	focus *canvas.Rectangle // keyboard focus outline of the bar
}

func (r *barRenderer) Layout(size fyne.Size) {
	m := r.m
	s := kit.ScaleOf(m)
	tsize, padX := m.titleMetrics()
	h := size.Height
	pillY := kit.Snap(menustyle.PillInsetY, s)
	pillH := kit.Snap(h-2*menustyle.PillInsetY, s)
	for i := range r.text {
		x, w := m.titleX[i], m.titleW[i]
		r.hl[i].Move(fyne.NewPos(kit.Snap(x+1, s), pillY))
		r.hl[i].Resize(fyne.NewSize(kit.Snap(w-2, s), pillH))
		r.text[i].TextSize = tsize
		ts := r.text[i].MinSize()
		ty := kit.CenterY(h, ts.Height, s)
		r.text[i].Move(fyne.NewPos(kit.Snap(x+padX, s), ty))
		r.text[i].Resize(ts)
		// mnemonic underline under its letter, one pixel below the baseline
		title := m.menus[i].Title
		pos := strings.IndexFunc(strings.ToLower(title), func(c rune) bool { return c == m.mnemonics[i] })
		if pos >= 0 {
			_, n := firstRune(title[pos:])
			pre := fyne.MeasureText(title[:pos], tsize, fyne.TextStyle{}).Width
			lw := fyne.MeasureText(title[pos:pos+n], tsize, fyne.TextStyle{}).Width
			_, base := fyne.CurrentApp().Driver().RenderedTextSize(title, tsize, fyne.TextStyle{}, nil)
			r.under[i].Move(fyne.NewPos(kit.Snap(x+padX+pre, s), kit.Snap(ty+base+1, s)))
			r.under[i].Resize(fyne.NewSize(kit.Snap(lw, s), kit.Hair(s)))
		}
		if i == m.active {
			r.focus.Move(r.hl[i].Position())
			r.focus.Resize(r.hl[i].Size())
			r.focus.StrokeWidth = kit.Hair(s)
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

// update sets the fills of the pills: hover, pressed and open use the white-alpha menu
// tokens; the text never changes colour. Keyboard focus without an open menu adds an
// accent outline.
func (r *barRenderer) update() {
	m := r.m
	open := m.layer != nil && len(m.layer.panes) > 0
	kbd := m.layer != nil && m.layer.keyboard
	showMnemonic := (m.altHeld || kbd) && runtimeGOOS != "darwin"
	r.focus.Hide()
	for i := range r.text {
		switch {
		case i == m.active && open:
			r.hl[i].FillColor = studiotheme.MenuOpen
		case i == m.active && m.layer != nil: // bar focused by keyboard
			r.hl[i].FillColor = studiotheme.MenuHoverBar
			r.focus.StrokeColor = studiotheme.Steel
			r.focus.Show()
		case i == m.pressed:
			r.hl[i].FillColor = studiotheme.MenuOpen
		case i == m.hover:
			r.hl[i].FillColor = studiotheme.MenuHoverBar
		default:
			r.hl[i].FillColor = color.Transparent
		}
		r.text[i].Color = studiotheme.MenuText
		if showMnemonic && m.mnemonics[i] != 0 {
			r.under[i].FillColor = studiotheme.MenuText
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
	var objs []fyne.CanvasObject
	for i := range r.text {
		objs = append(objs, r.hl[i], r.text[i], r.under[i])
	}
	return append(objs, r.focus)
}

func (r *barRenderer) Destroy() {}

// TitlePos returns the canvas position of the centre of the menu title (for UI
// automation and integration tests).
func (m *MenuBar) TitlePos(title string) (fyne.Position, bool) {
	for i, mn := range m.menus {
		if mn.Title == title {
			bp, bs := m.barRect()
			return fyne.NewPos(bp.X+m.titleX[i]+m.titleW[i]/2, bp.Y+bs.Height/2), true
		}
	}
	return fyne.Position{}, false
}

// ItemPos returns the canvas position of the open menu row with label (deepest open
// pane first; for UI automation and integration tests).
func (m *MenuBar) ItemPos(label string) (fyne.Position, bool) {
	if m.layer == nil {
		return fyne.Position{}, false
	}
	for d := len(m.layer.panes) - 1; d >= 0; d-- {
		p := m.layer.panes[d]
		for i, r := range p.rows {
			if !r.sep && r.label == label {
				return p.Position().Add(fyne.NewPos(60, p.tops[i]+menustyle.RowH/2-p.offset())), true
			}
		}
	}
	return fyne.Position{}, false
}
