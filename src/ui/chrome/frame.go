package chrome

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/display"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Grip sizes (points): edges are 6 wide, corners 8 x 8.
const (
	GripEdge   = 6
	GripCorner = 8
	// Inset is the margin between the window edge and the content. The grips lie below the
	// content in z-order, so only this margin resizes and controls at the edge keep clicks.
	Inset = 3
)

// GripRect is the area of a grip in frame coordinates.
type GripRect struct {
	Pos  fyne.Position
	Size fyne.Size
}

// Frame wraps content with resize borders (6 px, invisible, resize cursors) and a 1 px
// outline. When the window is maximized or fullscreen the borders are inactive and the
// outline is hidden. Call Close to stop watching.
type Frame struct {
	widget.BaseWidget

	win     fyne.Window
	back    Backend
	content fyne.CanvasObject
	grips   []*grip
	outline [4]*canvas.Rectangle
	state   State
	stop    func()
	closed  bool
}

// NewFrame wraps content of window w.
func NewFrame(w fyne.Window, b Backend, content fyne.CanvasObject) *Frame {
	f := &Frame{win: w, back: b, content: content}
	if b != nil {
		f.state = b.State(w)
	}
	for i := range f.outline {
		f.outline[i] = canvas.NewRectangle(studiotheme.Border)
	}
	for _, e := range []display.Edge{
		display.EdgeTop, display.EdgeBottom, display.EdgeLeft, display.EdgeRight,
		display.EdgeTopLeft, display.EdgeTopRight, display.EdgeBottomLeft, display.EdgeBottomRight,
	} {
		g := &grip{f: f, edge: e}
		g.ExtendBaseWidget(g)
		f.grips = append(f.grips, g)
	}
	f.ExtendBaseWidget(f)
	f.applyActive()
	if b != nil {
		f.stop = b.Watch(w, f.onState)
	}
	return f
}

func (f *Frame) onState(s State) {
	if f.closed || s == f.state {
		return
	}
	f.state = s
	f.applyActive()
	f.Refresh()
}

// Active reports whether the borders resize and the outline shows (a normal window).
func (f *Frame) Active() bool { return !f.state.Maximized && !f.state.Fullscreen }

func (f *Frame) applyActive() {
	on := f.Active()
	for _, g := range f.grips {
		if on {
			g.Show()
		} else {
			g.Hide()
		}
	}
	for _, o := range f.outline {
		if on {
			o.Show()
		} else {
			o.Hide()
		}
	}
}

// Grips returns the edge of every resize grip with its rectangle in frame coordinates,
// for tests and hit-testing hosts. Inactive frames return nil.
func (f *Frame) Grips() map[display.Edge]GripRect {
	if !f.Active() {
		return nil
	}
	m := map[display.Edge]GripRect{}
	for _, g := range f.grips {
		m[g.edge] = GripRect{Pos: g.Position(), Size: g.Size()}
	}
	return m
}

// Close stops the state watcher.
func (f *Frame) Close() {
	if f.closed {
		return
	}
	f.closed = true
	if f.stop != nil {
		f.stop()
		f.stop = nil
	}
}

func (f *Frame) CreateRenderer() fyne.WidgetRenderer { return &frameRenderer{f: f} }

type frameRenderer struct{ f *Frame }

func (r *frameRenderer) MinSize() fyne.Size {
	if r.f.content == nil {
		return fyne.NewSize(2*GripCorner, 2*GripCorner)
	}
	return r.f.content.MinSize().AddWidthHeight(2*Inset, 2*Inset)
}

func (r *frameRenderer) Objects() []fyne.CanvasObject {
	f := r.f
	var objs []fyne.CanvasObject
	for _, g := range f.grips {
		objs = append(objs, g)
	}
	if f.content != nil {
		objs = append(objs, f.content)
	}
	for _, o := range f.outline {
		objs = append(objs, o)
	}
	return objs
}

func (r *frameRenderer) Layout(s fyne.Size) {
	f := r.f
	if f.content != nil {
		in := float32(Inset)
		if !f.Active() {
			in = 0
		}
		f.content.Move(fyne.NewPos(in, in))
		f.content.Resize(fyne.NewSize(s.Width-2*in, s.Height-2*in))
	}
	e, c := float32(GripEdge), float32(GripCorner)
	rect := func(g *grip, x, y, w, h float32) {
		g.Move(fyne.NewPos(x, y))
		g.Resize(fyne.NewSize(w, h))
	}
	for _, g := range f.grips {
		switch g.edge {
		case display.EdgeTop:
			rect(g, c, 0, s.Width-2*c, e)
		case display.EdgeBottom:
			rect(g, c, s.Height-e, s.Width-2*c, e)
		case display.EdgeLeft:
			rect(g, 0, c, e, s.Height-2*c)
		case display.EdgeRight:
			rect(g, s.Width-e, c, e, s.Height-2*c)
		case display.EdgeTopLeft:
			rect(g, 0, 0, c, c)
		case display.EdgeTopRight:
			rect(g, s.Width-c, 0, c, c)
		case display.EdgeBottomLeft:
			rect(g, 0, s.Height-c, c, c)
		case display.EdgeBottomRight:
			rect(g, s.Width-c, s.Height-c, c, c)
		}
	}
	o := f.outline
	o[0].Move(fyne.NewPos(0, 0))
	o[0].Resize(fyne.NewSize(s.Width, 1))
	o[1].Move(fyne.NewPos(0, s.Height-1))
	o[1].Resize(fyne.NewSize(s.Width, 1))
	o[2].Move(fyne.NewPos(0, 0))
	o[2].Resize(fyne.NewSize(1, s.Height))
	o[3].Move(fyne.NewPos(s.Width-1, 0))
	o[3].Resize(fyne.NewSize(1, s.Height))
}

func (r *frameRenderer) Refresh() {
	f := r.f
	for _, o := range f.outline {
		o.FillColor = studiotheme.Border
		o.Refresh()
	}
	r.Layout(f.Size())
	if f.content != nil {
		f.content.Refresh()
	}
}

func (r *frameRenderer) Destroy() {}

// grip is one invisible resize hit area.
type grip struct {
	widget.BaseWidget
	f    *Frame
	edge display.Edge
}

func (g *grip) CreateRenderer() fyne.WidgetRenderer {
	r := canvas.NewRectangle(color.Transparent)
	return widget.NewSimpleRenderer(r)
}

func (g *grip) MinSize() fyne.Size { return fyne.NewSize(1, 1) }

// Cursor implements desktop.Cursorable with the closest resize cursors Fyne offers.
func (g *grip) Cursor() desktop.Cursor { return CursorFor(g.edge) }

// CursorFor returns the resize cursor of an edge.
func CursorFor(e display.Edge) desktop.Cursor {
	switch e {
	case display.EdgeLeft, display.EdgeRight:
		return desktop.HResizeCursor
	case display.EdgeTop, display.EdgeBottom:
		return desktop.VResizeCursor
	case display.EdgeTopLeft, display.EdgeBottomRight:
		return desktop.NWSEResizeCursor
	case display.EdgeTopRight, display.EdgeBottomLeft:
		return desktop.NESWResizeCursor
	}
	return desktop.DefaultCursor
}

func (g *grip) MouseIn(*desktop.MouseEvent)    {}
func (g *grip) MouseMoved(*desktop.MouseEvent) {}
func (g *grip) MouseOut()                      {}

func (g *grip) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || !g.f.Active() || g.f.back == nil {
		return
	}
	g.f.back.BeginResize(g.f.win, int(g.edge))
}
func (g *grip) MouseUp(*desktop.MouseEvent) {}
