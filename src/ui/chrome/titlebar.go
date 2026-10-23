package chrome

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// Titlebar is the draggable bar. leading and trailing are placed next to the window
// buttons (e.g. wordmark + menu bar); center is the title area (e.g. project name) which
// also drags the window. The buttons sit left (mac) or right (win) of everything.
//
// Layout: mac = [controls][leading][center][trailing], win = [leading][center][trailing]
// [controls]. Every pixel not covered by an interactive child (center, padding, the
// empty margins of the controls) is the drag region: a primary MouseDown calls
// Backend.BeginMove synchronously, a double click zooms, a secondary tap opens the
// window menu.
type Titlebar struct {
	widget.BaseWidget

	win     fyne.Window
	opt     Options
	back    Backend
	style   string
	ctl     *Controls
	lead    fyne.CanvasObject
	center  fyne.CanvasObject
	trail   fyne.CanvasObject
	bg      *canvas.Rectangle
	line    *canvas.Rectangle
	state   State
	stop    func()
	closed  bool
	menuPop *widget.PopUpMenu
}

// NewTitlebar builds the bar for window w. leading, center and trailing may be nil.
func NewTitlebar(w fyne.Window, o Options, leading, center, trailing fyne.CanvasObject) *Titlebar {
	t := &Titlebar{win: w, opt: o, back: o.Backend, lead: leading, center: center, trail: trailing}
	t.style = normStyle(o.Style)
	t.state = State{Focused: true}
	if t.back != nil {
		t.state = t.back.State(w)
	}
	t.bg = canvas.NewRectangle(studiotheme.Panel)
	t.line = canvas.NewRectangle(studiotheme.Border)
	t.ctl = newControls(t.style, o.Compact, actions{
		close:       func() { t.do(func(b Backend) { b.Close(t.win) }) },
		minimize:    func() { t.do(func(b Backend) { b.Minimize(t.win) }) },
		fullscreen:  func() { t.do(func(b Backend) { b.SetFullscreen(t.win, !t.state.Fullscreen) }) },
		maximize:    func() { t.do(func(b Backend) { b.ToggleMaximize(t.win) }) },
		drag:        t.beginMove,
		doubleTap:   t.zoom,
		secondary:   t.showMenu,
		canMaximize: o.CanMaximize,
		canFullscr:  o.CanFullscreen,
	})
	t.ctl.state = t.state
	t.ExtendBaseWidget(t)
	if t.back != nil {
		t.stop = t.back.Watch(w, t.onState)
	}
	return t
}

func (t *Titlebar) do(f func(Backend)) {
	if t.back != nil {
		f(t.back)
	}
}

func (t *Titlebar) onState(s State) {
	if t.closed || s == t.state {
		return
	}
	t.state = s
	t.ctl.setState(s)
	t.Refresh()
}

// State returns the window state the bar currently shows.
func (t *Titlebar) State() State { return t.state }

// Style returns the active button style.
func (t *Titlebar) Style() string { return t.style }

// Controls returns the window button group (for tests and hosts that need its geometry).
func (t *Titlebar) Controls() *Controls { return t.ctl }

// SetStyle switches the button style live.
func (t *Titlebar) SetStyle(style string) {
	style = normStyle(style)
	if style == t.style {
		return
	}
	t.style = style
	t.opt.Style = style
	t.ctl.setStyle(style)
	t.Refresh()
}

// SetCenter replaces the title area.
func (t *Titlebar) SetCenter(o fyne.CanvasObject) {
	t.center = o
	t.Refresh()
}

// Close releases the state watcher. The bar stays usable (without live state).
func (t *Titlebar) Close() {
	if t.closed {
		return
	}
	t.closed = true
	if t.stop != nil {
		t.stop()
		t.stop = nil
	}
	if t.menuPop != nil {
		t.menuPop.Hide()
		t.menuPop = nil
	}
}

func (t *Titlebar) beginMove() {
	if t.back != nil {
		t.back.BeginMove(t.win)
	}
}

func (t *Titlebar) zoom() {
	if t.back != nil && t.opt.CanMaximize {
		t.back.ToggleMaximize(t.win)
	}
}

// desktop.Mouseable

func (t *Titlebar) MouseDown(e *desktop.MouseEvent) {
	if e.Button == desktop.MouseButtonPrimary {
		t.beginMove()
	}
}
func (t *Titlebar) MouseUp(*desktop.MouseEvent) {}

// fyne.DoubleTappable / Tappable / SecondaryTappable

func (t *Titlebar) Tapped(*fyne.PointEvent)            {}
func (t *Titlebar) DoubleTapped(*fyne.PointEvent)      { t.zoom() }
func (t *Titlebar) TappedSecondary(e *fyne.PointEvent) { t.showMenu(e) }

// WindowMenu builds the context menu of the bar for the current state.
func (t *Titlebar) WindowMenu() *fyne.Menu {
	st := t.state
	restore := fyne.NewMenuItem("Wiederherstellen", func() {
		switch {
		case st.Fullscreen:
			t.do(func(b Backend) { b.SetFullscreen(t.win, false) })
		case st.Maximized:
			t.do(func(b Backend) { b.ToggleMaximize(t.win) })
		}
	})
	restore.Disabled = !(st.Fullscreen || st.Maximized)
	minimize := fyne.NewMenuItem("Minimieren", func() { t.do(func(b Backend) { b.Minimize(t.win) }) })
	maxLabel := "Maximieren"
	if st.Maximized {
		maxLabel = "Wiederherstellen"
	}
	maximize := fyne.NewMenuItem(maxLabel, func() { t.do(func(b Backend) { b.ToggleMaximize(t.win) }) })
	maximize.Disabled = !t.opt.CanMaximize || st.Fullscreen
	fsLabel := "Vollbild"
	if st.Fullscreen {
		fsLabel = "Vollbild beenden"
	}
	full := fyne.NewMenuItem(fsLabel, func() { t.do(func(b Backend) { b.SetFullscreen(t.win, !st.Fullscreen) }) })
	full.Disabled = !t.opt.CanFullscreen
	closeItem := fyne.NewMenuItem("Schließen", func() { t.do(func(b Backend) { b.Close(t.win) }) })
	return fyne.NewMenu("", restore, minimize, maximize, full, fyne.NewMenuItemSeparator(), closeItem)
}

func (t *Titlebar) showMenu(e *fyne.PointEvent) {
	if t.win == nil || t.closed {
		return
	}
	if t.menuPop != nil {
		t.menuPop.Hide()
	}
	t.menuPop = widget.NewPopUpMenu(t.WindowMenu(), t.win.Canvas())
	t.menuPop.ShowAtPosition(e.AbsolutePosition)
}

// MinSize is the sum of the children widths at the bar height.
func (t *Titlebar) MinSize() fyne.Size {
	h := barHeight(t.opt.Compact)
	w := t.ctl.MinSize().Width
	for _, o := range []fyne.CanvasObject{t.lead, t.center, t.trail} {
		if o != nil {
			m := o.MinSize()
			w += m.Width
			if m.Height > h {
				h = m.Height
			}
		}
	}
	return fyne.NewSize(w, h)
}

func (t *Titlebar) CreateRenderer() fyne.WidgetRenderer {
	return &titlebarRenderer{t: t}
}

type titlebarRenderer struct{ t *Titlebar }

func (r *titlebarRenderer) MinSize() fyne.Size { return r.t.MinSize() }

func (r *titlebarRenderer) Objects() []fyne.CanvasObject {
	t := r.t
	objs := []fyne.CanvasObject{t.bg, t.line}
	for _, o := range []fyne.CanvasObject{t.lead, t.center, t.trail} {
		if o != nil {
			objs = append(objs, o)
		}
	}
	return append(objs, t.ctl)
}

func (r *titlebarRenderer) Layout(size fyne.Size) {
	t := r.t
	t.bg.Move(fyne.NewPos(0, 0))
	t.bg.Resize(size)
	t.line.Move(fyne.NewPos(0, size.Height-1))
	t.line.Resize(fyne.NewSize(size.Width, 1))

	cw := t.ctl.MinSize().Width
	left, right := float32(0), size.Width
	if t.style == StyleWin {
		right -= cw
		t.ctl.Move(fyne.NewPos(right, 0))
	} else {
		left = cw
		t.ctl.Move(fyne.NewPos(0, 0))
	}
	t.ctl.Resize(fyne.NewSize(cw, size.Height))

	place := func(o fyne.CanvasObject, x, w float32) {
		m := o.MinSize()
		hh := m.Height
		if hh > size.Height {
			hh = size.Height
		}
		o.Resize(fyne.NewSize(w, hh))
		o.Move(fyne.NewPos(x, (size.Height-hh)/2))
	}
	if t.lead != nil {
		w := t.lead.MinSize().Width
		place(t.lead, left, w)
		left += w
	}
	if t.trail != nil {
		w := t.trail.MinSize().Width
		right -= w
		place(t.trail, right, w)
	}
	if t.center != nil {
		free := right - left
		if free < 0 {
			free = 0
		}
		x, w := left, free
		if t.style == StyleMac && isText(t.center) {
			if m := t.center.MinSize().Width; m < free {
				x, w = left+(free-m)/2, m
			}
		}
		place(t.center, x, w)
	}
}

// isText reports whether o is a plain title (label/text) that is centred, not stretched.
func isText(o fyne.CanvasObject) bool {
	switch o.(type) {
	case *widget.Label, *canvas.Text:
		return true
	}
	return false
}

func (r *titlebarRenderer) Refresh() {
	t := r.t
	t.bg.FillColor = studiotheme.Panel
	t.line.FillColor = studiotheme.Border
	t.bg.Refresh()
	t.line.Refresh()
	r.Layout(t.Size())
	t.ctl.Refresh()
}

func (r *titlebarRenderer) Destroy() {}
