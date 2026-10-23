package workspace

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/command"
	fslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/winchrome"
)

// cursorIdle is how long the pointer may rest on a clean feed before it disappears.
const cursorIdle = 2 * time.Second

// detached is one panel in its own window.
type detached struct {
	m    *Manager
	id   string
	info page.PanelInfo
	win  fyne.Window

	panel page.Panel
	ctx   *page.Context // the host context the panel was built for
	built bool

	monitor string        // monitor the window was put on ("" = system decides)
	mode    string        // layout mode requested
	rect    fslayout.Rect // windowed geometry relative to the monitor
	// fallbackFrom is the monitor the layout wanted while it is missing; wantMode its mode.
	fallbackFrom, wantMode string
	// unplaced: the window system could not put the window on monitor (Wayland); the
	// header says so instead of claiming the monitor.
	unplaced bool

	header    *fyne.Container // system title bar mode: the thin header below it
	title     *canvas.Text
	where     *canvas.Text
	icon      fyne.CanvasObject
	fsButton  *barButton
	monButton *barButton
	backBtn   *barButton
	body      *fyne.Container
	stack     fyne.CanvasObject // backdrop, body and the clean feed's idle area
	idle      *idleArea

	// Custom chrome (Display.CustomChrome): the header row is the window's title bar.
	custom      bool
	undecorated bool
	arranged    string // key of the content installed by arrange
	chrome      *winchrome.Chrome
	unwatch     func()
	last        chrome.State // window state at the last watch event

	closing   bool
	done      bool
	shortcuts map[string]*desktop.CustomShortcut
	standard  bool
	handlers  map[string]func(fyne.Shortcut)
	unsub     func()
}

func newDetached(m *Manager, info page.PanelInfo) *detached {
	d := &detached{m: m, id: info.ID, info: info, shortcuts: map[string]*desktop.CustomShortcut{}, handlers: map[string]func(fyne.Shortcut){}}
	d.win = m.host.App().NewWindow(info.Title + " — Frame Studio")
	if info.Icon != nil {
		d.win.SetIcon(info.Icon)
	}
	d.win.SetPadded(false)
	d.custom = winchrome.Wanted(settings.Get().Display)
	d.buildChrome()
	d.arrange()
	winchrome.OnClose(d.win, d.close)
	winchrome.OnFullscreen(d.win, d.setFullscreen)
	d.rebuild()
	c := d.win.Canvas()
	c.SetOnTypedKey(d.typedKey)
	d.installShortcuts()
	if reg := m.host.Commands(); reg != nil {
		d.unsub = reg.Subscribe(d.installShortcuts)
	}
	d.win.SetOnClosed(d.closed)
	d.win.Resize(fyne.NewSize(1280, 720))
	return d
}

func (d *detached) cleanFeed() bool { return d.id == page.PanelCleanFeed }

// buildChrome creates the widgets of the header (icon, title, place, buttons) and the
// body stack; arrange puts them into the system header or the custom title bar.
func (d *detached) buildChrome() {
	d.title = canvas.NewText(d.info.Title, studiotheme.Text)
	d.title.TextSize = 12
	d.title.TextStyle = fyne.TextStyle{Bold: true}
	d.where = canvas.NewText("", studiotheme.Muted)
	d.where.TextSize = 11

	icon := d.info.Icon
	if icon == nil {
		icon = theme.ComputerIcon()
	}
	d.icon = container.NewGridWrap(fyne.NewSize(16, 16), widget.NewIcon(icon))

	d.fsButton = newBarButton("Vollbild", theme.ViewFullScreenIcon(), d.toggleFullscreen)
	d.monButton = newBarButton("Auf Monitor", theme.MenuDropDownIcon(), func() { d.showMonitorMenu(d.monButton) })
	d.monButton.trailingIcon = true
	d.backBtn = newBarButton("Zurück ins Hauptfenster", theme.LogoutIcon(), d.close)

	d.body = container.NewStack()
	backdrop := canvas.NewRectangle(studiotheme.Gap)
	if d.cleanFeed() {
		backdrop.FillColor = studiotheme.MonitorOut
	}
	floor := canvas.NewRectangle(color.Transparent)
	floor.SetMinSize(panelMinSize(d.id))
	stack := container.NewStack(backdrop, floor, d.body)
	if d.cleanFeed() {
		d.idle = newIdleArea(d)
		stack.Add(d.idle)
	}
	d.stack = stack
}

// systemHeader is the thin studio header below the system title bar: icon, title and
// place at the left, the window buttons at the right.
func (d *detached) systemHeader() *fyne.Container {
	sep := canvas.NewRectangle(studiotheme.Border)
	sep.SetMinSize(fyne.NewSize(1, 14))
	left := container.NewHBox(spacer(6), centerV(d.icon), spacer(2), centerV(d.title), spacer(8), centerV(sep), spacer(8), centerV(d.where))
	right := container.NewHBox(d.fsButton, d.monButton, d.backBtn, spacer(2))
	bg := canvas.NewRectangle(studiotheme.Panel)
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	height := canvas.NewRectangle(color.Transparent)
	height.SetMinSize(fyne.NewSize(0, 30))
	return container.NewVBox(container.NewStack(bg, height, container.NewBorder(nil, nil, left, right)), line)
}

// arrange installs the window content for the current mode: the custom title bar in a
// resize frame, or the system header; a fullscreen clean feed shows neither (no bar, no
// frame, no resize grips).
func (d *detached) arrange() {
	bare := d.custom && d.cleanFeed() && d.win.FullScreen()
	key := fmt.Sprint(d.custom, bare)
	if key == d.arranged {
		return
	}
	d.arranged = key
	if d.chrome != nil {
		d.chrome.Close()
		d.chrome = nil
	}
	switch {
	case d.custom && bare:
		d.header = nil
		d.win.SetContent(d.stack)
	case d.custom:
		d.header = nil
		o := winchrome.Options(settings.Get().Display, true, true, true)
		parts := winchrome.Parts{
			Leading:  container.NewHBox(spacer(4), centerV(d.icon), spacer(6), centerV(d.where)),
			Center:   container.NewCenter(d.title),
			Trailing: container.NewHBox(d.fsButton, d.monButton, d.backBtn, spacer(4)),
		}
		root, c := winchrome.Build(d.win, o, parts, d.stack)
		d.chrome = c
		d.win.SetContent(root)
	default:
		d.header = d.systemHeader()
		d.win.SetContent(container.NewBorder(d.header, nil, nil, nil, d.stack))
	}
}

// decorate takes the system title bar off the native window (custom mode) or gives it
// back; call it once the window is shown. If the window system refuses, the window keeps
// its system title bar and the header.
func (d *detached) decorate() {
	if d.custom {
		if err := winchrome.Undecorate(d.win); err != nil {
			d.custom = false
			d.arrange()
		} else {
			d.undecorated = true
		}
	} else if d.undecorated {
		_ = winchrome.Redecorate(d.win)
		d.undecorated = false
	}
	if d.unwatch == nil && !d.closing {
		d.unwatch = winchrome.Sys.Watch(d.win, d.stateChanged)
	}
}

// stateChanged follows maximize and fullscreen done with the title bar or the window
// manager: the place text and the layout mode stay true.
func (d *detached) stateChanged(s chrome.State) {
	if d.closing || d.done {
		return
	}
	was := d.last
	d.last = s
	switch {
	case s.Fullscreen:
		d.mode = fslayout.ModeFullscreen
	case s.Maximized:
		d.mode = fslayout.ModeMaximized
	case was.Maximized || was.Fullscreen: // left maximize or fullscreen
		d.mode = fslayout.ModeWindow
	}
	d.updateHeader()
	d.m.refresh()
}

// applyChrome follows Display.CustomChrome and Display.WindowButtons live.
func (d *detached) applyChrome(s settings.Display) {
	if d.closing || d.done {
		return
	}
	if want := winchrome.Wanted(s); want != d.custom {
		d.custom = want
		d.arrange()
		d.decorate()
		d.updateHeader()
		return
	}
	if d.chrome != nil {
		d.chrome.Bar.SetStyle(winchrome.Style(s))
	}
}

// panelMinSize is the smallest body a detached panel stays usable at: the scopes need
// room for the graph, the mixer for its fader scale (their own minimums are what they
// need inside a page, where the page gives them more).
func panelMinSize(id string) fyne.Size {
	switch id {
	case page.PanelScopes:
		return fyne.NewSize(420, 300)
	case page.PanelMixer:
		return fyne.NewSize(420, 440)
	}
	return fyne.Size{}
}

func spacer(w float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 1))
	return r
}

func centerV(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}

// rebuild (re)creates the panel for the active project tab; the old one is closed.
func (d *detached) rebuild() {
	ctx := d.m.host.ActiveContext()
	if d.built && ctx == d.ctx {
		return
	}
	if d.panel != nil {
		d.panel.Close()
		d.panel = nil
	}
	d.ctx, d.built = ctx, true
	var content fyne.CanvasObject
	switch {
	case ctx == nil:
		content = d.emptyState("Kein Projekt geöffnet",
			"Öffne ein Projekt, dann erscheint hier „"+d.info.Title+"“.")
	case d.info.New == nil:
		content = d.emptyState("Nicht verfügbar", "Dieses Fenster gibt es in dieser Version noch nicht.")
	default:
		c := *ctx
		c.Window = d.win // dialogs of the panel open in its own window
		d.panel = d.info.New(&c)
		content = d.panel.Content()
	}
	d.body.Objects = []fyne.CanvasObject{content}
	d.body.Refresh()
}

func (d *detached) emptyState(title, hint string) fyne.CanvasObject {
	fg, muted := studiotheme.Text, studiotheme.Muted
	if d.cleanFeed() {
		fg, muted = studiotheme.Faint, color.NRGBA{0x40, 0x42, 0x48, 0xFF}
	}
	t := canvas.NewText(title, fg)
	t.TextSize = 15
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.Alignment = fyne.TextAlignCenter
	h := canvas.NewText(hint, muted)
	h.TextSize = 12
	h.Alignment = fyne.TextAlignCenter
	return container.NewCenter(container.NewVBox(t, h))
}

// updateHeader shows where the window is and hides the header of a fullscreen clean feed.
func (d *detached) updateHeader() {
	full := d.win.FullScreen()
	where := "Fenster"
	if mon, ok := d.m.monitor(d.monitor); ok {
		where = mon.Name
	}
	switch {
	case d.unplaced && !full:
		where = "Fenster · Unter Wayland selbst auf " + d.monitor + " ziehen oder „Vollbild“ wählen"
		if d.fallbackFrom != "" {
			where += " · Ersatz für " + d.fallbackFrom + " (nicht angeschlossen)"
		}
	case d.fallbackFrom != "":
		where += " · Ersatz für " + d.fallbackFrom + " (nicht angeschlossen)"
	case full:
		where += " · Vollbild"
	case d.mode == fslayout.ModeMaximized:
		where += " · Maximiert"
	}
	d.where.Text = where
	d.where.Refresh()
	if full {
		d.fsButton.SetText("Vollbild beenden")
		d.fsButton.SetIcon(theme.ViewRestoreIcon())
	} else {
		d.fsButton.SetText("Vollbild")
		d.fsButton.SetIcon(theme.ViewFullScreenIcon())
	}
	d.arrange()
	if d.header != nil {
		if d.cleanFeed() && full {
			d.header.Hide()
		} else {
			d.header.Show()
		}
	}
	if d.idle != nil && !(d.cleanFeed() && full) {
		d.idle.show()
	}
}

func (d *detached) toggleFullscreen() { d.setFullscreen(!d.win.FullScreen()) }

func (d *detached) setFullscreen(on bool) {
	// An explicit choice replaces the arrangement remembered for a missing monitor.
	d.fallbackFrom, d.wantMode = "", ""
	mon := d.monitor
	if mon == "" || !d.m.available(mon) {
		if id, _, _, _, _, ok := d.m.disp.Geometry(d.win); ok && id != "" {
			mon = id
		}
	}
	if on {
		d.m.placeDetached(d, mon, fslayout.ModeFullscreen, d.rect)
	} else {
		d.m.placeDetached(d, mon, fslayout.ModeWindow, d.rect)
	}
	d.m.refresh()
}

// moveTo puts the window on monitor id, keeping fullscreen; windowed panels maximize.
func (d *detached) moveTo(id string) {
	mode := fslayout.ModeMaximized
	if d.win.FullScreen() {
		mode = fslayout.ModeFullscreen
	}
	d.fallbackFrom = ""
	d.m.placeDetached(d, id, mode, d.m.defaultRect(id, 0))
	d.m.refresh()
}

func (d *detached) showMonitorMenu(anchor fyne.CanvasObject) {
	var items []*fyne.MenuItem
	for _, mon := range d.m.monitors() {
		id := mon.ID
		it := fyne.NewMenuItem(monitorLabel(mon), func() { d.moveTo(id) })
		it.Checked = id == d.monitor
		it.Disabled = d.m.unused[id]
		items = append(items, it)
	}
	if len(items) == 0 {
		it := fyne.NewMenuItem("Keine Monitorinformationen verfügbar", nil)
		it.Disabled = true
		items = append(items, it)
	}
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor).Add(fyne.NewPos(0, anchor.Size().Height))
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), d.win.Canvas(), pos)
}

// close closes the window (the panel goes back into the main window).
func (d *detached) close() {
	if d.closing {
		return
	}
	d.closing = true
	d.win.Close()
	d.closed() // the test driver does not always call OnClosed
}

// closed cleans up after the window closed, by the user or by close.
func (d *detached) closed() {
	if d.done {
		return
	}
	d.done, d.closing = true, true
	if d.unsub != nil {
		d.unsub()
		d.unsub = nil
	}
	if d.idle != nil {
		d.idle.stop()
	}
	if d.panel != nil {
		d.panel.Close()
		d.panel = nil
	}
	d.ctx, d.built = nil, false
	if d.unwatch != nil {
		d.unwatch()
		d.unwatch = nil
	}
	if d.chrome != nil {
		d.chrome.Close()
		d.chrome = nil
	}
	winchrome.Forget(d.win)
	d.m.disp.Forget(d.win)
	d.m.remove(d)
}

func (d *detached) modal() bool { return d.win.Canvas().Overlays().Top() != nil }

// typedKey: Esc leaves fullscreen of a clean feed, F11 toggles fullscreen, other keys go
// to the panel and then to the command registry (as in the main window).
func (d *detached) typedKey(k *fyne.KeyEvent) {
	if d.modal() || d.closing {
		return
	}
	mods := d.m.mods()
	if d.idle != nil {
		d.idle.show()
	}
	if k.Name == fyne.KeyEscape && d.cleanFeed() && d.win.FullScreen() {
		d.setFullscreen(false)
		return
	}
	if reg := d.m.host.Commands(); reg != nil {
		if sc := command.Format(k.Name, mods); sc != "" && sc == command.Normalize(reg.Shortcut("view.fullscreen")) {
			d.toggleFullscreen()
			return
		}
	}
	if d.panel != nil && d.panel.TypedKey(k) {
		return
	}
	if k.Name == fyne.KeyEscape && d.win.FullScreen() {
		d.setFullscreen(false)
		return
	}
	if reg := d.m.host.Commands(); reg != nil {
		d.regDispatch(reg, k.Name, mods)
	}
}

// regDispatch runs the registry command of a key in this window: the commands of the
// panel's own page apply (a detached timeline cuts with the edit page's commands while
// the main window shows Farbe), not those of the page the main window shows.
func (d *detached) regDispatch(reg *command.Registry, key fyne.KeyName, mods fyne.KeyModifier) bool {
	if pg := page.PanelPage(d.id); pg != "" {
		return reg.DispatchAs(command.PageScope(pg), key, mods)
	}
	return reg.Dispatch(key, mods)
}

// standardShortcuts are the driver's named shortcuts. They are matched by name only, so
// one handler serves e.g. Strg+X and Umschalt+Entf (ShortcutCut with Secondary set): the
// handler maps the shortcut it receives with command.ComboOf, as the main window does.
var standardShortcuts = []fyne.Shortcut{
	&fyne.ShortcutUndo{}, &fyne.ShortcutRedo{}, &fyne.ShortcutCopy{},
	&fyne.ShortcutPaste{}, &fyne.ShortcutCut{}, &fyne.ShortcutSelectAll{},
}

// installShortcuts registers every Ctrl/Alt/Super chord of the registry on the window's
// canvas (the desktop driver delivers those as shortcuts, not typed keys) and routes
// them to Dispatch. It re-runs when the registry changes and only touches differences.
func (d *detached) installShortcuts() {
	if d.closing {
		return
	}
	reg := d.m.host.Commands()
	if reg == nil {
		return
	}
	c := d.win.Canvas()
	want := map[string]*desktop.CustomShortcut{}
	// Catalog IDs plus overridden ones: cheap lookups, no Enabled/Checked evaluation.
	seen := map[string]bool{}
	var idList []string
	for _, in := range command.Catalog {
		seen[in.ID] = true
		idList = append(idList, in.ID)
	}
	for id := range reg.Keymap() {
		if !seen[id] {
			idList = append(idList, id)
		}
	}
	for _, id := range idList {
		key, mods, ok := command.Parse(reg.Shortcut(id))
		if !ok || mods&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) == 0 {
			continue
		}
		sc := &desktop.CustomShortcut{KeyName: key, Modifier: mods}
		want[sc.ShortcutName()] = sc
	}
	for name, sc := range d.shortcuts {
		if _, ok := want[name]; !ok {
			c.RemoveShortcut(sc)
			delete(d.shortcuts, name)
			delete(d.handlers, name)
		}
	}
	for name, sc := range want {
		if d.shortcuts[name] != nil {
			continue
		}
		d.shortcuts[name] = sc
		d.addShortcut(sc, d.shortcutTyped)
	}
	if !d.standard {
		d.standard = true
		for _, sc := range standardShortcuts {
			d.addShortcut(sc, d.shortcutTyped)
		}
	}
}

// shortcutTyped handles every registered shortcut of the window.
func (d *detached) shortcutTyped(sc fyne.Shortcut) {
	key, mods, ok := command.ComboOf(sc)
	if !ok {
		return
	}
	if !d.dispatch(key, mods) && key == fyne.KeyY && mods == fyne.KeyModifierShortcutDefault {
		if reg := d.m.host.Commands(); reg != nil && !d.modal() && !d.closing {
			reg.Run("edit.redo")
		}
	}
}

// TypeShortcut delivers sc to the detached window of panelID as its canvas would (for UI
// automation and integration tests). It reports whether the window has a handler for it.
func (m *Manager) TypeShortcut(panelID string, sc fyne.Shortcut) bool {
	d := m.find(panelID)
	if d == nil {
		return false
	}
	fn := d.handlers[sc.ShortcutName()]
	if fn == nil {
		return false
	}
	fn(sc)
	return true
}

// addShortcut registers fn for sc on the window canvas (handlers keeps it for tests).
func (d *detached) addShortcut(sc fyne.Shortcut, fn func(fyne.Shortcut)) {
	d.handlers[sc.ShortcutName()] = fn
	d.win.Canvas().AddShortcut(sc, fn)
}

// dispatch runs a key combination with Ctrl, Alt or Super: F11 (view.fullscreen) is
// this window's fullscreen, then the panel gets it (page.ShortcutPanel), then the
// registry with the panel's page scope.
func (d *detached) dispatch(key fyne.KeyName, mods fyne.KeyModifier) bool {
	if d.modal() || d.closing {
		return false
	}
	reg := d.m.host.Commands()
	if reg == nil {
		return false
	}
	if command.Format(key, mods) == command.Normalize(reg.Shortcut("view.fullscreen")) {
		d.toggleFullscreen()
		return true
	}
	if sp, ok := d.panel.(page.ShortcutPanel); ok && sp.Shortcut(key, mods) {
		return true
	}
	return d.regDispatch(reg, key, mods)
}

// idleArea covers a clean feed: it hides the pointer after cursorIdle without movement
// and toggles fullscreen on double click.
type idleArea struct {
	widget.BaseWidget
	d      *detached
	inside bool
	hidden bool
	gen    int
	cancel func()
}

func newIdleArea(d *detached) *idleArea {
	a := &idleArea{d: d}
	a.ExtendBaseWidget(a)
	return a
}

func (a *idleArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (a *idleArea) MouseIn(*desktop.MouseEvent)    { a.inside = true; a.show() }
func (a *idleArea) MouseMoved(*desktop.MouseEvent) { a.inside = true; a.show() }
func (a *idleArea) MouseOut()                      { a.inside = false; a.show() }
func (a *idleArea) Tapped(*fyne.PointEvent)        { a.show() }
func (a *idleArea) DoubleTapped(*fyne.PointEvent)  { a.d.toggleFullscreen() }

// show makes the pointer visible and restarts the idle timer while the pointer is inside.
func (a *idleArea) show() {
	if a.hidden {
		a.hidden = false
		a.d.m.disp.SetCursorHidden(a.d.win, false)
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if !a.inside || a.d.closing {
		return
	}
	a.gen++
	gen := a.gen
	a.cancel = a.d.m.after(cursorIdle, func() {
		uiDo(func() {
			if gen == a.gen && a.inside && !a.d.closing {
				a.hidden = true
				a.d.m.disp.SetCursorHidden(a.d.win, true)
			}
		})
	})
}

func (a *idleArea) stop() {
	a.gen++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.hidden {
		a.hidden = false
		a.d.m.disp.SetCursorHidden(a.d.win, false)
	}
}
