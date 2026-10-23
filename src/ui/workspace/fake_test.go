package workspace

import (
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// --- fake panels ---------------------------------------------------------------

type fakePanel struct {
	id     string
	ctx    *page.Context
	closed bool
	keys   []fyne.KeyName
	eat    map[fyne.KeyName]bool
}

func (p *fakePanel) Content() fyne.CanvasObject {
	r := canvas.NewRectangle(studiotheme.Lane)
	if p.id == page.PanelCleanFeed || p.id == page.PanelProgram {
		r.FillColor = studiotheme.MonitorOut
	}
	return r
}

func (p *fakePanel) TypedKey(k *fyne.KeyEvent) bool {
	p.keys = append(p.keys, k.Name)
	return p.eat[k.Name]
}

func (p *fakePanel) Close() { p.closed = true }

var built []*fakePanel // every panel instance, in creation order

func init() {
	for _, p := range []struct{ id, title string }{
		{page.PanelProgram, "Programm-Monitor"},
		{page.PanelCleanFeed, "Clean-Feed"},
		{page.PanelTimeline, "Timeline"},
		{page.PanelInspector, "Inspector"},
		{page.PanelMediaPool, "Medienpool"},
		{page.PanelScopes, "Scopes"},
		{page.PanelMixer, "Mixer"},
	} {
		id := p.id
		page.RegisterPanel(page.PanelInfo{ID: id, Title: p.title, New: func(ctx *page.Context) page.Panel {
			fp := &fakePanel{id: id, ctx: ctx, eat: map[fyne.KeyName]bool{}}
			built = append(built, fp)
			return fp
		}})
	}
}

// lastPanel returns the newest instance of panel id.
func lastPanel(id string) *fakePanel {
	for i := len(built) - 1; i >= 0; i-- {
		if built[i].id == id {
			return built[i]
		}
	}
	return nil
}

// --- fake host -----------------------------------------------------------------

type fakeHost struct {
	app       fyne.App
	main      fyne.Window
	ctx       *page.Context
	listeners []func()
	pages     map[string]layout.Page
	applied   map[string]layout.Page
	active    string
	shown     []string
	reg       *command.Registry
}

func (h *fakeHost) App() fyne.App                             { return h.app }
func (h *fakeHost) MainWindow() fyne.Window                   { return h.main }
func (h *fakeHost) ActiveContext() *page.Context              { return h.ctx }
func (h *fakeHost) OnActiveChange(fn func())                  { h.listeners = append(h.listeners, fn) }
func (h *fakeHost) PageLayouts() map[string]layout.Page       { return h.pages }
func (h *fakeHost) ApplyPageLayouts(p map[string]layout.Page) { h.applied = p; h.pages = p }
func (h *fakeHost) ActivePage() string                        { return h.active }
func (h *fakeHost) ShowPage(id string)                        { h.active = id; h.shown = append(h.shown, id) }
func (h *fakeHost) Commands() *command.Registry               { return h.reg }

func (h *fakeHost) setActive(ctx *page.Context) {
	h.ctx = ctx
	for _, fn := range h.listeners {
		fn()
	}
}

// --- fake display --------------------------------------------------------------

type fstate struct {
	mon, mode string
	r         layout.Rect
}

type fakeDisplay struct {
	ms       []display.Monitor
	canPos   bool
	platform string
	st       map[fyne.Window]*fstate
	onChange []func()
	cursor   map[fyne.Window]bool
	forgot   int
}

// userMonitors is the user's desk: DP-3 left, DP-2 primary in the middle, HDMI-A-1 right.
func userMonitors() []display.Monitor {
	mk := func(id string, x int, primary bool, hz int) display.Monitor {
		return display.Monitor{ID: id, Name: fmt.Sprintf("%s · 2560×1440", id), X: x, W: 2560, H: 1440, Refresh: hz, Scale: 1.25, Primary: primary}
	}
	return []display.Monitor{mk("DP-2", 2560, true, 155), mk("DP-3", 0, false, 155), mk("HDMI-A-1", 5120, false, 144)}
}

func newFakeDisplay(ms []display.Monitor, x11 bool) *fakeDisplay {
	p := "wayland"
	if x11 {
		p = "x11"
	}
	return &fakeDisplay{ms: ms, canPos: x11, platform: p, st: map[fyne.Window]*fstate{}, cursor: map[fyne.Window]bool{}}
}

func (f *fakeDisplay) state(w fyne.Window) *fstate {
	s := f.st[w]
	if s == nil {
		s = &fstate{mode: layout.ModeWindow}
		f.st[w] = s
	}
	return s
}

func (f *fakeDisplay) has(id string) bool {
	for _, m := range f.ms {
		if m.ID == id {
			return true
		}
	}
	return false
}

func (f *fakeDisplay) Platform() string            { return f.platform }
func (f *fakeDisplay) CanPosition() bool           { return f.canPos }
func (f *fakeDisplay) Monitors() []display.Monitor { return append([]display.Monitor(nil), f.ms...) }

func (f *fakeDisplay) Fullscreen(w fyne.Window, id string, on bool) error {
	s := f.state(w)
	if !on {
		if s.mode == layout.ModeFullscreen {
			s.mode = layout.ModeWindow
		}
		w.SetFullScreen(false)
		return nil
	}
	if id != "" && !f.has(id) {
		return display.ErrNoMonitor
	}
	if id != "" {
		s.mon = id
	}
	s.mode = layout.ModeFullscreen
	w.SetFullScreen(true)
	return nil
}

func (f *fakeDisplay) Maximize(w fyne.Window, id string) error {
	s := f.state(w)
	if id != "" && !f.has(id) {
		return display.ErrNoMonitor
	}
	w.SetFullScreen(false)
	s.mode = layout.ModeMaximized
	if id != "" && !f.canPos && id != s.mon {
		return display.ErrNoPositioning // xdg maximize cannot pick an output
	}
	if id != "" {
		s.mon = id
	}
	return nil
}

func (f *fakeDisplay) Place(w fyne.Window, id string, x, y, width, height int) error {
	s := f.state(w)
	w.SetFullScreen(false)
	s.mode = layout.ModeWindow
	if id != "" && !f.has(id) {
		return display.ErrNoMonitor
	}
	if !f.canPos {
		s.r = layout.Rect{W: width, H: height}
		if id != "" && id != s.mon {
			return display.ErrNoPositioning // the compositor keeps it where it is
		}
		return nil
	}
	s.mon = id
	s.r = layout.Rect{X: x, Y: y, W: width, H: height}
	return nil
}

func (f *fakeDisplay) Geometry(w fyne.Window) (string, int, int, int, int, bool) {
	s := f.state(w)
	if s.mode == layout.ModeFullscreen {
		return s.mon, 0, 0, 2560, 1440, true
	}
	return s.mon, s.r.X, s.r.Y, s.r.W, s.r.H, true
}

func (f *fakeDisplay) IsMaximized(w fyne.Window) bool { return f.state(w).mode == layout.ModeMaximized }
func (f *fakeDisplay) SetCursorHidden(w fyne.Window, hidden bool) {
	f.cursor[w] = hidden
}
func (f *fakeDisplay) Forget(w fyne.Window) { f.forgot++; delete(f.st, w) }
func (f *fakeDisplay) OnChange(fn func())   { f.onChange = append(f.onChange, fn) }

func (f *fakeDisplay) setMonitors(ms []display.Monitor) {
	f.ms = ms
	for _, fn := range f.onChange {
		fn()
	}
}

// --- harness -------------------------------------------------------------------

type harness struct {
	t     *testing.T
	host  *fakeHost
	disp  *fakeDisplay
	m     *Manager
	errs  []error
	timer []func()
	q     *kit.UIQueue
}

func newHarness(t *testing.T, x11 bool) *harness {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	q := kit.UseUIQueue(t)
	if err := settings.Reset(); err != nil {
		t.Fatal(err)
	}
	built = nil
	main := test.NewTempWindow(t, canvas.NewRectangle(studiotheme.Gap))
	main.Resize(fyne.NewSize(1440, 900))
	h := &harness{t: t, q: q, disp: newFakeDisplay(userMonitors(), x11)}
	h.host = &fakeHost{app: a, main: main, reg: command.NewRegistry(), active: page.Edit,
		pages: map[string]layout.Page{page.Edit: {Splits: map[string]float64{"viewer": 0.42}}}}
	h.m = NewWithDisplay(h.host, h.disp)
	h.m.showErr = func(err error) { h.errs = append(h.errs, err) }
	h.m.after = func(d time.Duration, f func()) func() {
		cancelled := false
		h.timer = append(h.timer, func() {
			if !cancelled {
				f()
			}
		})
		return func() { cancelled = true }
	}
	h.m.mods = func() fyne.KeyModifier { return 0 }
	h.host.reg.Bind(command.Global, h.m.Bindings()...)
	// The main window starts maximized on the primary monitor.
	h.disp.Maximize(main, "DP-2")
	t.Cleanup(h.m.Shutdown)
	return h
}

// fireTimers runs the pending idle timers.
func (h *harness) fireTimers() {
	ts := h.timer
	h.timer = nil
	for _, f := range ts {
		if f != nil {
			f()
		}
	}
	h.q.Drain()
}

func (h *harness) state(id string) *fstate {
	w := h.m.Window(id)
	if w == nil {
		h.t.Fatalf("panel %s not detached (open: %v)", id, h.m.Windows())
	}
	return h.disp.state(w)
}
