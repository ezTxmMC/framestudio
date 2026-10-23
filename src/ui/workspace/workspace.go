// Package workspace manages the windows of the app: detached panel windows on any monitor
// (windowed or fullscreen), layouts (save, restore, presets) and the monitor dialog.
//
// OWNER: workspace agent. Exported signatures in this file are a contract.
package workspace

import (
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/winchrome"
)

// Host is what the shell provides to the workspace manager.
type Host interface {
	App() fyne.App
	MainWindow() fyne.Window
	// ActiveContext returns the page context of the active project tab, nil when none.
	ActiveContext() *page.Context
	// OnActiveChange registers fn to run (UI thread) when the active project tab changes.
	OnActiveChange(fn func())
	// PageLayouts returns the layout state of every created page; ApplyPageLayouts restores it.
	PageLayouts() map[string]layout.Page
	ApplyPageLayouts(map[string]layout.Page)
	ActivePage() string
	ShowPage(id string)
	// Commands is the shared registry; detached windows dispatch their keys to it.
	Commands() *command.Registry
}

// workspaceKey is the reserved entry of Layout.Pages holding workspace state (monitors
// marked "Nicht verwenden"). It is never passed to Host.ApplyPageLayouts.
const workspaceKey = "_workspace"

// Manager owns the detached panel windows and the layouts. UI thread only.
type Manager struct {
	host Host
	disp Display

	name   string          // name of the current layout
	unused map[string]bool // monitors the user does not want to use
	wins   []*detached     // open detached windows, in opening order
	closed bool
	dlg    *monitorDialog
	unsub  func() // settings subscription (custom title bar follows the settings)

	// mods reports the modifiers held with the current key event (desktop driver).
	mods func() fyne.KeyModifier
	// after runs f after d (on any goroutine); the returned func cancels it.
	after func(d time.Duration, f func()) (cancel func())
	// showErr reports an error to the user (dialog on the main window).
	showErr func(err error)
}

// New returns a manager using the real display backend.
func New(h Host) *Manager { return NewWithDisplay(h, System{}) }

// NewWithDisplay returns a manager with a custom display backend (tests).
func NewWithDisplay(h Host, d Display) *Manager {
	m := &Manager{host: h, disp: d, name: "Standard", unused: map[string]bool{}, mods: heldModifiers}
	m.after = func(d time.Duration, f func()) func() {
		t := time.AfterFunc(d, f)
		return func() { t.Stop() }
	}
	m.showErr = func(err error) {
		if w := h.MainWindow(); w != nil {
			dialog.ShowError(err, w)
		}
	}
	if s := settings.Get().Display.Layout; s != "" {
		m.name = s
	}
	h.OnActiveChange(m.activeChanged)
	d.OnChange(m.monitorsChanged)
	last := settings.Get().Display
	m.unsub = settings.Subscribe(func(s settings.Settings) {
		if m.closed || (s.Display.CustomChrome == last.CustomChrome && s.Display.WindowButtons == last.WindowButtons) {
			last = s.Display
			return
		}
		last = s.Display
		for _, d := range append([]*detached(nil), m.wins...) {
			d.applyChrome(s.Display)
		}
	})
	return m
}

// CloseFocused closes the detached window that has the focus and reports whether there
// was one (window.close).
func (m *Manager) CloseFocused() bool {
	for _, d := range m.wins {
		if !d.closing && winchrome.Sys.State(d.win).Focused {
			d.close()
			m.refresh()
			return true
		}
	}
	return false
}

// Bindings returns the workspace.* commands and registers the dynamic submenus.
func (m *Manager) Bindings() []command.Binding {
	reg := m.host.Commands()
	if reg != nil {
		reg.SetDynamic(command.DynLayouts, m.layoutItems)
		reg.SetDynamic(command.DynMonitors, m.monitorItems)
		reg.SetDynamic(command.DynWindows, m.windowItems)
		reg.SetDynamic(command.DynPanelPlace, m.panelPlaceItems)
	}
	open := func() bool { return !m.closed }
	bs := []command.Binding{
		{ID: "workspace.monitors", Run: m.ShowMonitorDialog, Enabled: open},
		{ID: "workspace.saveLayout", Run: m.showSaveLayout, Enabled: open},
		{ID: "workspace.manageLayouts", Run: m.showManageLayouts, Enabled: open},
		{ID: "workspace.resetLayout", Run: m.ResetLayout, Enabled: open},
		{ID: "workspace.closeDetached", Run: m.CloseDetached, Enabled: func() bool { return !m.closed && len(m.wins) > 0 }},
		{ID: "workspace.saveOnQuit", Run: func() {
			_ = settings.Update(func(s *settings.Settings) { s.Display.SaveLayoutOnQuit = !s.Display.SaveLayoutOnQuit })
		}, Checked: func() bool { return settings.Get().Display.SaveLayoutOnQuit }},
	}
	for _, in := range command.Catalog {
		id, ok := strings.CutPrefix(in.ID, "workspace.detach.")
		if !ok {
			continue
		}
		bs = append(bs, command.Binding{ID: in.ID,
			Run: func() { m.ToggleDetached(id) },
			Enabled: func() bool {
				_, ok := page.PanelByID(id)
				return ok && !m.closed
			},
			Checked: func() bool { return m.find(id) != nil },
		})
	}
	return bs
}

// ToggleDetached closes panel id's window when it is open, otherwise opens it on the
// best other monitor (first monitor in use that is not the main window's): the clean
// feed fullscreen, other panels maximized; with a single monitor as a normal window.
func (m *Manager) ToggleDetached(id string) {
	if d := m.find(id); d != nil {
		d.close()
		return
	}
	mon := m.bestOther()
	// The clean feed always fills its monitor; under Wayland, where only fullscreen
	// reaches a chosen monitor, the program monitor does too.
	full := mon != "" && (id == page.PanelCleanFeed || id == page.PanelProgram && !m.disp.CanPosition())
	if mon == "" {
		m.Detach(id, m.mainMonitor(), false)
		return
	}
	if full {
		m.Detach(id, mon, true)
		return
	}
	m.open(id, mon, layout.ModeMaximized, layout.Rect{}, "")
}

// Restore applies the layout saved at last quit (or the named layout from settings).
func (m *Manager) Restore() {
	s := settings.Get().Display
	var l layout.Layout
	var err error
	if s.SaveLayoutOnQuit {
		l, err = layout.Load("", true)
		if err == nil && l.Name == "" {
			l.Name = s.Layout
		}
	}
	if !s.SaveLayoutOnQuit || err != nil {
		l, err = layout.Load(s.Layout, false)
		if err != nil {
			l = layout.Default()
			if s.Layout != "" {
				l.Name = s.Layout
			}
		}
	}
	m.Apply(l)
}

// Current captures the current layout (main window, detached windows, page states).
func (m *Manager) Current() layout.Layout {
	l := layout.Layout{Version: layout.Version, Name: m.name, ActivePage: m.host.ActivePage(), Pages: map[string]layout.Page{}}
	for k, v := range m.host.PageLayouts() {
		if k != workspaceKey {
			l.Pages[k] = v
		}
	}
	if len(m.unused) > 0 {
		var ids []string
		for id := range m.unused {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		l.Pages[workspaceKey] = layout.Page{Extra: map[string]string{"unused": strings.Join(ids, ",")}}
	}
	main := layout.Window{ID: "main", Mode: layout.ModeWindow}
	if w := m.host.MainWindow(); w != nil {
		main.Monitor, main.Rect, main.Mode = m.windowState(w, "")
	}
	l.Windows = append(l.Windows, main)
	for _, d := range m.wins {
		mon, r, mode := m.windowState(d.win, d.monitor)
		if d.fallbackFrom != "" && !m.available(d.fallbackFrom) {
			mon, mode = d.fallbackFrom, d.wantMode // keep the intent until the monitor returns
		}
		l.Windows = append(l.Windows, layout.Window{ID: d.id, Panel: d.id, Monitor: mon, Mode: mode, Rect: r})
	}
	return l
}

// windowState reads monitor, rect and mode of w; known is used when the backend cannot
// tell the monitor (Wayland windows).
func (m *Manager) windowState(w fyne.Window, known string) (string, layout.Rect, string) {
	mon, x, y, ww, hh, ok := m.disp.Geometry(w)
	if !ok || mon == "" {
		mon = known
	}
	mode := layout.ModeWindow
	switch {
	case w.FullScreen():
		mode = layout.ModeFullscreen
	case m.disp.IsMaximized(w):
		mode = layout.ModeMaximized
	}
	r := layout.Rect{X: x, Y: y, W: ww, H: hh}
	if mode == layout.ModeFullscreen {
		r = layout.Rect{}
		if p, ok := m.lastRect(w); ok {
			r = p
		}
	}
	return mon, r, mode
}

// lastRect is the windowed rect remembered for a detached window.
func (m *Manager) lastRect(w fyne.Window) (layout.Rect, bool) {
	for _, d := range m.wins {
		if d.win == w && d.rect.W > 0 {
			return d.rect, true
		}
	}
	return layout.Rect{}, false
}

// Detach opens panel id in its own window (on monitor, fullscreen or not). An already
// detached panel moves to the monitor instead of opening twice.
func (m *Manager) Detach(panelID, monitorID string, fullscreen bool) {
	mode := layout.ModeWindow
	if fullscreen {
		mode = layout.ModeFullscreen
	}
	m.open(panelID, monitorID, mode, layout.Rect{}, "")
}

// open creates or moves the window of panel id. fallbackFrom names the monitor the
// layout asked for when it was missing.
func (m *Manager) open(id, monitor, mode string, r layout.Rect, fallbackFrom string) *detached {
	if m.closed {
		return nil
	}
	info, ok := page.PanelByID(id)
	if !ok {
		m.showErr(errors.New("Dieses Fenster gibt es in dieser Version nicht: " + id))
		return nil
	}
	d := m.find(id)
	if d == nil {
		d = newDetached(m, info)
		m.wins = append(m.wins, d)
		d.win.Show()
		d.decorate() // the system title bar goes once the native window exists
	}
	d.fallbackFrom = fallbackFrom
	if fallbackFrom != "" {
		d.wantMode = mode
		mode = layout.ModeWindow
	}
	m.placeDetached(d, monitor, mode, r)
	d.win.RequestFocus()
	m.refresh()
	return d
}

// placeDetached puts d on monitor in mode; windowed without a rect it gets a default
// size centred on the monitor.
func (m *Manager) placeDetached(d *detached, monitor, mode string, r layout.Rect) {
	if r.W <= 0 || r.H <= 0 {
		r = m.defaultRect(monitor, len(m.wins)-1)
	}
	if mode == layout.ModeMaximized && monitor != "" && !m.disp.CanPosition() {
		// Wayland maximizes where the compositor has the window; only fullscreen
		// reaches the chosen monitor.
		mode = layout.ModeFullscreen
	}
	d.monitor, d.mode, d.rect = monitor, mode, r
	d.unplaced = !m.place(d.win, monitor, mode, r)
	d.updateHeader()
}

// defaultRect is a 1280×720 window centred on the monitor, staggered by n.
func (m *Manager) defaultRect(monitor string, n int) layout.Rect {
	r := layout.Rect{X: 80, Y: 80, W: 1280, H: 720}
	if mon, ok := m.monitor(monitor); ok {
		if r.W > mon.W-80 {
			r.W, r.H = mon.W*2/3, mon.H*2/3
		}
		r.X, r.Y = (mon.W-r.W)/2, (mon.H-r.H)/2
	}
	if n > 0 {
		r.X += 32 * (n % 6)
		r.Y += 32 * (n % 6)
	}
	return r
}

// place applies monitor and mode to any window. Wayland's "no positioning" is expected
// and not reported as an error; place returns false then (the window is wherever the
// compositor put it).
func (m *Manager) place(w fyne.Window, monitor, mode string, r layout.Rect) bool {
	var err error
	switch mode {
	case layout.ModeFullscreen:
		err = m.disp.Fullscreen(w, monitor, true)
	case layout.ModeMaximized:
		err = m.disp.Maximize(w, monitor)
	default:
		if r.W > 0 && r.H > 0 {
			err = m.disp.Place(w, monitor, r.X, r.Y, r.W, r.H)
		} else if w.FullScreen() {
			err = m.disp.Fullscreen(w, "", false)
		}
	}
	if errors.Is(err, display.ErrNoPositioning) {
		return false
	}
	if err != nil {
		m.showErr(err)
	}
	return true
}

// Apply restores a layout: main window, detached windows (missing or unused monitors
// fall back to the main window's monitor, windowed), page states and the active page.
func (m *Manager) Apply(l layout.Layout) {
	if m.closed {
		return
	}
	m.unused = map[string]bool{}
	if ws, ok := l.Pages[workspaceKey]; ok {
		for _, id := range strings.Split(ws.Extra["unused"], ",") {
			if id = strings.TrimSpace(id); id != "" {
				m.unused[id] = true
			}
		}
	}
	main := *l.Main()
	mainMon := main.Monitor
	if mainMon != "" && !m.available(mainMon) {
		mainMon = m.firstUsable()
		if main.Mode == layout.ModeFullscreen {
			main.Mode = layout.ModeMaximized
		}
		if main.Mode == layout.ModeWindow {
			main.Rect = m.defaultRect(mainMon, 0)
		}
	}
	if mainMon == "" && main.Mode != layout.ModeWindow {
		// A layout without a monitor (the default) starts on the primary monitor, not
		// wherever the window system first put the window.
		mainMon = m.firstUsable()
	}
	if w := m.host.MainWindow(); w != nil {
		m.place(w, mainMon, main.Mode, main.Rect)
	}
	fallback := mainMon
	if fallback == "" {
		fallback = m.mainMonitor()
	}

	want := map[string]bool{}
	for _, w := range l.Windows {
		if w.Panel != "" {
			want[w.Panel] = true
		}
	}
	for _, d := range append([]*detached(nil), m.wins...) {
		if !want[d.id] {
			d.close()
		}
	}
	n := 0
	for _, w := range l.Windows {
		if w.Panel == "" {
			continue
		}
		if _, ok := page.PanelByID(w.Panel); !ok {
			continue
		}
		mon, from := w.Monitor, ""
		r := w.Rect
		if mon != "" && !m.available(mon) {
			from, mon = mon, fallback
			r = m.defaultRect(mon, n+1)
			if w.Rect.W > 0 && w.Rect.H > 0 {
				r.W, r.H = w.Rect.W, w.Rect.H
				if mm, ok := m.monitor(mon); ok {
					r.W, r.H = min(r.W, mm.W-120), min(r.H, mm.H-120)
				}
			}
		}
		m.open(w.Panel, mon, w.Mode, r, from)
		n++
	}

	pages := map[string]layout.Page{}
	for k, v := range l.Pages {
		if k != workspaceKey {
			pages[k] = v
		}
	}
	m.host.ApplyPageLayouts(pages)
	if l.ActivePage != "" {
		m.host.ShowPage(l.ActivePage)
	}
	if l.Name != "" {
		m.name = l.Name
	}
	m.refresh()
}

// LoadLayout loads and applies a saved layout by name.
func (m *Manager) LoadLayout(name string) {
	l, err := layout.Load(name, false)
	if err != nil {
		m.showErr(errors.New("Das Layout „" + name + "“ ließ sich nicht laden: " + plainErr(err)))
		return
	}
	l.Name = name
	m.Apply(l)
}

// SaveLayout saves the current state under name and makes it the current layout.
func (m *Manager) SaveLayout(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Bitte einen Namen für das Layout eingeben.")
	}
	if strings.HasPrefix(name, "_") {
		return errors.New("Layoutnamen dürfen nicht mit „_“ beginnen.")
	}
	m.name = name
	l := m.Current()
	if err := layout.Save(l, false); err != nil {
		return errors.New("Das Layout ließ sich nicht speichern: " + plainErr(err))
	}
	m.refresh()
	return nil
}

// ResetLayout closes all detached windows, maximizes the main window and resets the pages.
func (m *Manager) ResetLayout() {
	l := layout.Default()
	for k := range m.host.PageLayouts() {
		l.Pages[k] = layout.Page{}
	}
	l.ActivePage = ""
	m.Apply(l)
}

// CloseDetached closes every detached window.
func (m *Manager) CloseDetached() {
	for _, d := range append([]*detached(nil), m.wins...) {
		d.close()
	}
	m.refresh()
}

// Windows returns the panel IDs of the open detached windows (in opening order).
func (m *Manager) Windows() []string {
	var ids []string
	for _, d := range m.wins {
		ids = append(ids, d.id)
	}
	return ids
}

// Window returns the window of a detached panel, nil when it is not detached.
func (m *Manager) Window(panelID string) fyne.Window {
	if d := m.find(panelID); d != nil {
		return d.win
	}
	return nil
}

// LayoutName is the name of the current layout.
func (m *Manager) LayoutName() string { return m.name }

// Shutdown saves the layout if enabled and closes all detached windows.
func (m *Manager) Shutdown() { m.shutdown(true) }

// ShutdownAfterLoop is Shutdown for when the run loop has already ended without it (the
// process got SIGTERM before the shell could quit normally): the layout is saved from
// what the manager knows and the panels are released, but no window is touched, since
// GLFW is gone.
func (m *Manager) ShutdownAfterLoop() { m.shutdown(false) }

func (m *Manager) shutdown(live bool) {
	if m.closed {
		return
	}
	if settings.Get().Display.SaveLayoutOnQuit {
		if err := layout.Save(m.Current(), true); err != nil {
			fyne.LogError("Layout beim Beenden speichern", err)
		}
	}
	if m.dlg != nil && live {
		m.dlg.hide()
	}
	m.closed = true
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	for _, d := range append([]*detached(nil), m.wins...) {
		if live {
			d.close()
		} else {
			d.closing = true
			d.closed()
		}
	}
	m.wins = nil
	if reg := m.host.Commands(); reg != nil {
		reg.Refresh()
	}
}

func (m *Manager) find(id string) *detached {
	for _, d := range m.wins {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (m *Manager) remove(d *detached) {
	for i, x := range m.wins {
		if x == d {
			m.wins = append(m.wins[:i], m.wins[i+1:]...)
			break
		}
	}
	m.refresh()
}

// refresh tells menus and the monitor dialog that windows or monitors changed.
func (m *Manager) refresh() {
	if m.dlg != nil {
		m.dlg.refresh()
	}
	if reg := m.host.Commands(); reg != nil {
		reg.Refresh()
	}
}

func (m *Manager) activeChanged() {
	if m.closed {
		return
	}
	for _, d := range m.wins {
		d.rebuild()
	}
}

// monitorsChanged handles hot-plug: windows on a vanished monitor go windowed onto the
// main window's monitor; when it comes back they return in their original mode.
func (m *Manager) monitorsChanged() {
	if m.closed {
		return
	}
	for _, d := range m.wins {
		switch {
		case d.fallbackFrom != "" && m.available(d.fallbackFrom):
			from, mode := d.fallbackFrom, d.wantMode
			d.fallbackFrom = ""
			m.placeDetached(d, from, mode, d.rect)
		case d.fallbackFrom == "" && d.monitor != "" && !m.available(d.monitor):
			d.fallbackFrom, d.wantMode = d.monitor, d.mode
			m.placeDetached(d, m.mainMonitor(), layout.ModeWindow, m.defaultRect(m.mainMonitor(), 1))
		}
	}
	m.refresh()
}

// monitors returns the connected monitors, primary first.
func (m *Manager) monitors() []display.Monitor {
	ms := m.disp.Monitors()
	display.SortMonitors(ms)
	return ms
}

func (m *Manager) monitor(id string) (display.Monitor, bool) {
	for _, mon := range m.monitors() {
		if mon.ID == id {
			return mon, true
		}
	}
	return display.Monitor{}, false
}

// available reports whether monitor id is connected and not marked unused.
func (m *Manager) available(id string) bool {
	_, ok := m.monitor(id)
	return ok && !m.unused[id]
}

// mainMonitor is the monitor of the main window (primary when unknown).
func (m *Manager) mainMonitor() string {
	if w := m.host.MainWindow(); w != nil {
		if id, _, _, _, _, ok := m.disp.Geometry(w); ok && id != "" {
			if _, there := m.monitor(id); there {
				return id
			}
		}
	}
	return m.firstUsable()
}

// firstUsable is the primary monitor, or the first one in use.
func (m *Manager) firstUsable() string {
	ms := m.monitors()
	for _, mon := range ms {
		if !m.unused[mon.ID] {
			return mon.ID
		}
	}
	if len(ms) > 0 {
		return ms[0].ID
	}
	return ""
}

// bestOther is the first monitor in use that is not the main window's, "" if none.
func (m *Manager) bestOther() string {
	main := m.mainMonitor()
	for _, mon := range m.monitors() {
		if mon.ID != main && !m.unused[mon.ID] {
			return mon.ID
		}
	}
	return ""
}

// setUnused marks a monitor as not to be used; its windows move to the main monitor.
// Like a monitor that is unplugged, the windows remember it and go back when it is in
// use again.
func (m *Manager) setUnused(id string, unused bool) {
	if !unused {
		delete(m.unused, id)
		m.monitorsChanged() // windows waiting for id return in their mode
		return
	}
	m.unused[id] = true
	if m.mainMonitor() == id || m.firstUsable() == id {
		if other := m.firstUsable(); other != id && other != "" {
			if w := m.host.MainWindow(); w != nil {
				m.place(w, other, layout.ModeMaximized, layout.Rect{})
			}
		}
	}
	for _, d := range m.wins {
		if d.monitor == id && d.fallbackFrom == "" {
			d.fallbackFrom, d.wantMode = id, d.mode
			mon := m.mainMonitor()
			m.placeDetached(d, mon, layout.ModeWindow, m.defaultRect(mon, 1))
		}
	}
	m.refresh()
}

// heldModifiers returns the modifier keys the desktop driver reports for the current event.
func heldModifiers() fyne.KeyModifier {
	if a := fyne.CurrentApp(); a != nil {
		if d, ok := a.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return 0
}

// plainErr strips Go's wrapping noise from file errors for German messages.
func plainErr(err error) string {
	s := err.Error()
	if errors.Is(err, os.ErrNotExist) {
		return "Die Datei fehlt."
	}
	return s
}

// uiDo runs f on the UI thread (used by timers).
func uiDo(f func()) { kit.Do(f) }
