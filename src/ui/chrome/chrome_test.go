package chrome

import (
	"runtime"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/display"
)

type fakeBackend struct {
	calls    []string
	edges    []int
	st       State
	watchers map[int]func(State)
	nextID   int
	stops    int
}

func newFake() *fakeBackend {
	return &fakeBackend{st: State{Focused: true}, watchers: map[int]func(State){}}
}

func (f *fakeBackend) rec(s string)          { f.calls = append(f.calls, s) }
func (f *fakeBackend) BeginMove(fyne.Window) { f.rec("move") }
func (f *fakeBackend) BeginResize(_ fyne.Window, e int) {
	f.rec("resize")
	f.edges = append(f.edges, e)
}
func (f *fakeBackend) Minimize(fyne.Window)       { f.rec("min") }
func (f *fakeBackend) ToggleMaximize(fyne.Window) { f.rec("max") }
func (f *fakeBackend) Close(fyne.Window)          { f.rec("close") }
func (f *fakeBackend) State(fyne.Window) State    { return f.st }
func (f *fakeBackend) SetFullscreen(_ fyne.Window, on bool) {
	if on {
		f.rec("fs-on")
	} else {
		f.rec("fs-off")
	}
}
func (f *fakeBackend) Watch(_ fyne.Window, fn func(State)) func() {
	id := f.nextID
	f.nextID++
	f.watchers[id] = fn
	return func() {
		if _, ok := f.watchers[id]; ok {
			delete(f.watchers, id)
			f.stops++
		}
	}
}
func (f *fakeBackend) set(s State) {
	f.st = s
	for _, fn := range f.watchers {
		fn(s)
	}
}
func (f *fakeBackend) last() string {
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func pt(x, y float32) fyne.Position { return fyne.NewPos(x, y) }

func down(btn desktop.MouseButton, mod fyne.KeyModifier, p fyne.Position) *desktop.MouseEvent {
	return &desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: p, AbsolutePosition: p}, Button: btn, Modifier: mod}
}

func click(c *Controls, p fyne.Position, mod fyne.KeyModifier) {
	c.MouseIn(down(0, mod, p))
	c.MouseDown(down(desktop.MouseButtonPrimary, mod, p))
	c.MouseUp(down(desktop.MouseButtonPrimary, mod, p))
}

// centres of the mac lights at the standard height 28
var (
	posClose = pt(18.25, 14)
	posMin   = pt(38.75, 14)
	posZoom  = pt(59.25, 14)
)

func newBar(t *testing.T, o Options, fb *fakeBackend) (*Titlebar, fyne.Window) {
	t.Helper()
	w := test.NewTempWindow(t, nil)
	w.SetPadded(false)
	o.Backend = fb
	tb := NewTitlebar(w, o, nil, nil, nil)
	t.Cleanup(tb.Close)
	w.SetContent(tb)
	w.Resize(fyne.NewSize(600, 28))
	return tb, w
}

var full = Options{Style: StyleMac, CanMaximize: true, CanFullscreen: true}

func TestMacButtonActions(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	c := tb.Controls()
	click(c, posClose, 0)
	click(c, posMin, 0)
	click(c, posZoom, 0)
	click(c, posZoom, fyne.KeyModifierAlt)
	fb.st.Fullscreen = true
	tb.onState(fb.st)
	click(c, posZoom, 0)
	want := []string{"close", "min", "fs-on", "max", "fs-off"}
	if len(fb.calls) != len(want) {
		t.Fatalf("calls %v want %v", fb.calls, want)
	}
	for i := range want {
		if fb.calls[i] != want[i] {
			t.Fatalf("calls %v want %v", fb.calls, want)
		}
	}
}

func TestMacButtonNeedsReleaseOnSameButton(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	c := tb.Controls()
	c.MouseDown(down(desktop.MouseButtonPrimary, 0, posClose))
	c.MouseUp(down(desktop.MouseButtonPrimary, 0, posMin))
	c.MouseDown(down(desktop.MouseButtonSecondary, 0, posClose))
	c.MouseUp(down(desktop.MouseButtonSecondary, 0, posClose))
	if len(fb.calls) != 0 {
		t.Fatalf("unexpected %v", fb.calls)
	}
}

func TestDisabledGreen(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, Options{Style: StyleMac}, fb)
	click(tb.Controls(), posZoom, 0)
	click(tb.Controls(), posZoom, fyne.KeyModifierAlt)
	if len(fb.calls) != 0 {
		t.Fatalf("disabled green acted: %v", fb.calls)
	}
	// only fullscreen allowed: alt falls back to fullscreen, no maximize
	tb2, _ := newBar(t, Options{Style: StyleMac, CanFullscreen: true}, fb)
	click(tb2.Controls(), posZoom, fyne.KeyModifierAlt)
	if fb.last() != "fs-on" {
		t.Fatalf("got %v", fb.calls)
	}
}

func TestHoverGroupShowsGlyphsOnAll(t *testing.T) {
	fb := newFake()
	tb, w := newBar(t, full, fb)
	c := tb.Controls()
	c.Refresh()
	if c.hover {
		t.Fatal("hover at start")
	}
	// through the real driver: moving over the group
	test.MoveMouse(w.Canvas(), pt(40, 14))
	if !c.hover {
		t.Fatal("pointer over the group did not set hover")
	}
	test.MoveMouse(w.Canvas(), pt(300, 14))
	if c.hover {
		t.Fatal("hover stayed after leaving")
	}
	// outside the circles but inside the group margin still counts
	c.MouseMoved(down(0, 0, pt(8, 14)))
	if !c.hover {
		t.Fatal("margin of the group should reveal glyphs")
	}
	c.MouseOut()
	if c.hover || c.alt {
		t.Fatal("MouseOut must reset")
	}
	// Alt while hovering switches the green glyph to plus
	c.MouseMoved(down(0, fyne.KeyModifierAlt, posZoom))
	if !c.alt {
		t.Fatal("alt not tracked")
	}
	c.MouseMoved(down(0, 0, posZoom))
	if c.alt {
		t.Fatal("alt stuck")
	}
}

func TestPressedAndInactiveState(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	c := tb.Controls()
	c.MouseDown(down(desktop.MouseButtonPrimary, 0, posMin))
	if c.pressed != kindMinimize {
		t.Fatal("pressed state missing")
	}
	c.MouseUp(down(desktop.MouseButtonPrimary, 0, posMin))
	if c.pressed != kindNone {
		t.Fatal("pressed state stuck")
	}
	fb.set(State{Focused: false})
	if tb.State().Focused || c.state.Focused {
		t.Fatal("inactive state not applied")
	}
	fb.set(State{Focused: true, Maximized: true})
	if !tb.State().Maximized {
		t.Fatal("maximize not applied")
	}
}

func TestWinButtons(t *testing.T) {
	fb := newFake()
	o := full
	o.Style = StyleWin
	tb, _ := newBar(t, o, fb)
	c := tb.Controls()
	if c.MinSize().Width != 138 {
		t.Fatalf("win controls width %v", c.MinSize().Width)
	}
	click(c, pt(23, 14), 0)
	click(c, pt(23+46, 14), 0)
	click(c, pt(23+92, 14), 0)
	want := []string{"min", "max", "close"}
	for i := range want {
		if fb.calls[i] != want[i] {
			t.Fatalf("calls %v", fb.calls)
		}
	}
	c.MouseMoved(down(0, 0, pt(120, 14)))
	if !c.hover || c.hit(pt(120, 14)) != kindClose {
		t.Fatal("close hover missing")
	}
}

func TestDragStartsOnceFromMouseDown(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	tb.MouseDown(down(desktop.MouseButtonPrimary, 0, pt(300, 10)))
	tb.MouseUp(down(desktop.MouseButtonPrimary, 0, pt(300, 10)))
	if len(fb.calls) != 1 || fb.calls[0] != "move" {
		t.Fatalf("calls %v", fb.calls)
	}
	tb.MouseDown(down(desktop.MouseButtonSecondary, 0, pt(300, 10)))
	if len(fb.calls) != 1 {
		t.Fatal("secondary button must not drag")
	}
	// padding of the controls drags as well
	tb.Controls().MouseDown(down(desktop.MouseButtonPrimary, 0, pt(2, 14)))
	if len(fb.calls) != 2 || fb.last() != "move" {
		t.Fatalf("control padding should drag: %v", fb.calls)
	}
	// a press on a light never drags
	tb.Controls().MouseDown(down(desktop.MouseButtonPrimary, 0, posClose))
	if len(fb.calls) != 2 {
		t.Fatalf("light started a drag: %v", fb.calls)
	}
}

func TestDragThroughDriverIgnoresInteractiveLeading(t *testing.T) {
	fb := newFake()
	w := test.NewTempWindow(t, nil)
	w.SetPadded(false)
	tapped := 0
	btn := widget.NewButton("Datei", func() { tapped++ })
	tb := NewTitlebar(w, Options{Style: StyleMac, Backend: fb, CanMaximize: true}, btn, widget.NewLabel("Projekt"), nil)
	t.Cleanup(tb.Close)
	w.SetContent(tb)
	w.Resize(fyne.NewSize(600, 28))
	bp := btn.Position().Add(pt(5, 5)).Add(tb.Position())
	test.TapCanvas(w.Canvas(), bp)
	if tapped != 1 {
		t.Fatalf("button tapped %d", tapped)
	}
	if len(fb.calls) != 0 {
		t.Fatalf("pressing a leading button must not drag: %v", fb.calls)
	}
	tb.MouseDown(down(desktop.MouseButtonPrimary, 0, pt(500, 14)))
	if len(fb.calls) != 1 || fb.calls[0] != "move" {
		t.Fatalf("empty area: %v", fb.calls)
	}
}

func TestDoubleClickZooms(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	tb.DoubleTapped(&fyne.PointEvent{Position: pt(300, 10)})
	if fb.last() != "max" {
		t.Fatalf("calls %v", fb.calls)
	}
	tb.Controls().DoubleTapped(&fyne.PointEvent{Position: pt(2, 14)})
	if len(fb.calls) != 2 {
		t.Fatal("double click on control padding should zoom")
	}
	tb.Controls().DoubleTapped(&fyne.PointEvent{Position: posClose})
	if len(fb.calls) != 2 {
		t.Fatal("double click on a light must not zoom")
	}
	fb2 := newFake()
	tb2, _ := newBar(t, Options{Style: StyleMac}, fb2)
	tb2.DoubleTapped(&fyne.PointEvent{})
	if len(fb2.calls) != 0 {
		t.Fatal("zoom must be ignored when maximizing is not allowed")
	}
}

func TestWindowMenu(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	names := func() []string {
		var n []string
		for _, it := range tb.WindowMenu().Items {
			n = append(n, it.Label)
		}
		return n
	}
	want := []string{"Wiederherstellen", "Minimieren", "Maximieren", "Vollbild", "", "Schließen"}
	got := names()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("menu %v", got)
		}
	}
	if !tb.WindowMenu().Items[0].Disabled {
		t.Fatal("restore must be disabled in a normal window")
	}
	fb.set(State{Focused: true, Maximized: true})
	m := tb.WindowMenu()
	if m.Items[0].Disabled || m.Items[2].Label != "Wiederherstellen" {
		t.Fatalf("maximized menu: %v", names())
	}
	m.Items[0].Action()
	m.Items[5].Action()
	if fb.calls[0] != "max" || fb.calls[1] != "close" {
		t.Fatalf("calls %v", fb.calls)
	}
	fb.set(State{Focused: true, Fullscreen: true})
	m = tb.WindowMenu()
	if m.Items[3].Label != "Vollbild beenden" || !m.Items[2].Disabled {
		t.Fatalf("fullscreen menu %v", names())
	}
	m.Items[0].Action()
	if fb.last() != "fs-off" {
		t.Fatalf("calls %v", fb.calls)
	}
	// the popup opens on secondary tap without panicking
	tb.TappedSecondary(&fyne.PointEvent{AbsolutePosition: pt(200, 20)})
}

func TestStyleSwitchLive(t *testing.T) {
	fb := newFake()
	tb, _ := newBar(t, full, fb)
	lead := widget.NewLabel("Menü")
	tb.lead = lead
	tb.Refresh()
	if lead.Position().X < 70 {
		t.Fatalf("mac: leading starts at %v", lead.Position().X)
	}
	tb.SetStyle(StyleWin)
	if tb.Style() != StyleWin || tb.Controls().Position().X < 600-139 {
		t.Fatalf("win controls at %v", tb.Controls().Position())
	}
	if lead.Position().X != 0 {
		t.Fatalf("win leading at %v", lead.Position().X)
	}
	tb.SetStyle("unsinn")
	if tb.Style() != ResolveStyle("auto", runtime.GOOS) {
		t.Fatal("unknown style follows the system")
	}
}

func TestMinSizes(t *testing.T) {
	fb := newFake()
	for _, compact := range []bool{false, true} {
		for _, st := range []string{StyleMac, StyleWin, StyleLinux} {
			w := test.NewTempWindow(t, nil)
			tb := NewTitlebar(w, Options{Style: st, Compact: compact, Backend: fb}, nil, nil, nil)
			m := tb.MinSize()
			wantH := float32(28)
			if compact {
				wantH = 24
			}
			if m.Height != wantH || m.Width < 70 || m.Width > 160 {
				t.Fatalf("%s compact=%v min %v", st, compact, m)
			}
			tb.Close()
		}
	}
	w := test.NewTempWindow(t, nil)
	f := NewFrame(w, fb, widget.NewLabel("x"))
	if f.MinSize().Width < 10 {
		t.Fatalf("frame min %v", f.MinSize())
	}
	f.Close()
}

func TestGripsEdgesAndCorners(t *testing.T) {
	fb := newFake()
	w := test.NewTempWindow(t, nil)
	w.SetPadded(false)
	f := NewFrame(w, fb, widget.NewLabel("Inhalt"))
	t.Cleanup(f.Close)
	w.SetContent(f)
	w.Resize(fyne.NewSize(400, 300))
	g := f.Grips()
	if len(g) != 8 {
		t.Fatalf("%d grips", len(g))
	}
	at := map[display.Edge]fyne.Position{
		display.EdgeTop: pt(200, 2), display.EdgeBottom: pt(200, 297),
		display.EdgeLeft: pt(2, 150), display.EdgeRight: pt(397, 150),
		display.EdgeTopLeft: pt(3, 3), display.EdgeTopRight: pt(396, 3),
		display.EdgeBottomLeft: pt(3, 296), display.EdgeBottomRight: pt(396, 296),
	}
	for e, p := range at {
		before := len(fb.edges)
		var gr *grip
		for _, c := range f.grips {
			if c.edge == e {
				gr = c
			}
		}
		r := g[e]
		if p.X < r.Pos.X || p.X > r.Pos.X+r.Size.Width || p.Y < r.Pos.Y || p.Y > r.Pos.Y+r.Size.Height {
			t.Fatalf("edge %v: %v outside %v", e, p, r)
		}
		gr.MouseDown(down(desktop.MouseButtonPrimary, 0, pt(1, 1)))
		if len(fb.edges) != before+1 || fb.edges[before] != int(e) {
			t.Fatalf("edge %v: got %v", e, fb.edges)
		}
	}
	// the interior is not covered by any grip
	for _, r := range g {
		in := pt(200, 150)
		if in.X >= r.Pos.X && in.X <= r.Pos.X+r.Size.Width && in.Y >= r.Pos.Y && in.Y <= r.Pos.Y+r.Size.Height {
			t.Fatal("grip covers the centre")
		}
	}
	if g[display.EdgeTop].Size.Height != 6 || g[display.EdgeTopLeft].Size.Width != 8 {
		t.Fatalf("grip sizes %v %v", g[display.EdgeTop], g[display.EdgeTopLeft])
	}
	cur := map[display.Edge]desktop.Cursor{
		display.EdgeLeft: desktop.HResizeCursor, display.EdgeRight: desktop.HResizeCursor,
		display.EdgeTop: desktop.VResizeCursor, display.EdgeBottom: desktop.VResizeCursor,
		display.EdgeTopLeft: desktop.NWSEResizeCursor, display.EdgeBottomRight: desktop.NWSEResizeCursor,
		display.EdgeTopRight: desktop.NESWResizeCursor, display.EdgeBottomLeft: desktop.NESWResizeCursor,
	}
	for e, c := range cur {
		if CursorFor(e) != c {
			t.Fatalf("cursor of %v", e)
		}
	}
}

func TestGripsInertWhenMaximizedOrFullscreen(t *testing.T) {
	fb := newFake()
	w := test.NewTempWindow(t, nil)
	f := NewFrame(w, fb, widget.NewLabel("Inhalt"))
	t.Cleanup(f.Close)
	w.SetContent(f)
	w.Resize(fyne.NewSize(400, 300))
	for _, st := range []State{{Maximized: true}, {Fullscreen: true}} {
		fb.set(st)
		if f.Active() || f.Grips() != nil {
			t.Fatal("frame must be inactive")
		}
		for _, g := range f.grips {
			g.MouseDown(down(desktop.MouseButtonPrimary, 0, pt(1, 1)))
			if g.Visible() {
				t.Fatal("grip still visible")
			}
		}
		if f.outline[0].Visible() {
			t.Fatal("outline visible")
		}
	}
	if len(fb.edges) != 0 {
		t.Fatalf("resize while maximized: %v", fb.edges)
	}
	fb.set(State{Focused: true})
	if !f.Active() || !f.grips[0].Visible() || !f.outline[0].Visible() {
		t.Fatal("frame did not come back")
	}
}

func TestRegistryClose(t *testing.T) {
	w := test.NewWindow(nil)
	n := 0
	OnClose(w, func() { n++ })
	DisplayBackend{}.Close(w)
	if n != 1 {
		t.Fatal("registered close not called")
	}
	OnClose(w, nil)
	closed := false
	w.SetOnClosed(func() { closed = true })
	RequestClose(w)
	if !closed {
		t.Fatal("default must close the window")
	}
}

func TestCloseStopsWatchersNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	fb := newFake()
	w := test.NewTempWindow(t, nil)
	tb := NewTitlebar(w, full, nil, nil, nil)
	_ = tb
	tb = NewTitlebar(w, Options{Backend: fb, Style: StyleMac}, nil, nil, nil)
	f := NewFrame(w, fb, widget.NewLabel("x"))
	if len(fb.watchers) != 2 {
		t.Fatalf("watchers %d", len(fb.watchers))
	}
	tb.Close()
	tb.Close()
	f.Close()
	f.Close()
	if len(fb.watchers) != 0 || fb.stops != 2 {
		t.Fatalf("watchers %d stops %d", len(fb.watchers), fb.stops)
	}
	fb.set(State{Maximized: true}) // must not touch closed widgets
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines %d > %d", n, before)
	}
}

func TestPainterTriangleAndShapes(t *testing.T) {
	p := newPainter(40, 40, 40)
	p.tri(5, 5, 35, 5, 5, 35, withAlpha(macRed, 1))
	if p.img.NRGBAAt(10, 10).A != 255 {
		t.Fatal("triangle interior not filled")
	}
	if p.img.NRGBAAt(30, 30).A != 0 {
		t.Fatal("triangle exterior painted")
	}
	p2 := newPainter(80, 80, 40) // 2x
	p2.disc(20, 20, 10, macRed)
	if p2.img.NRGBAAt(40, 40).A != 255 || p2.img.NRGBAAt(5, 5).A != 0 {
		t.Fatal("disc at 2x wrong")
	}
}
