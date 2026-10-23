package menubar

import (
	"image/png"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

type harness struct {
	t    *testing.T
	w    fyne.Window
	reg  *command.Registry
	bar  *MenuBar
	ran  []string
	snap bool
	col  int
	dyn  int // calls of the projects generator
	proj []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	oldOpen, oldSafe, oldIgn := openDelay, safeDelay, ignoreUpWithin
	openDelay, safeDelay, ignoreUpWithin = 0, 0, 0
	t.Cleanup(func() { openDelay, safeDelay, ignoreUpWithin = oldOpen, oldSafe, oldIgn; mru = nil })

	h := &harness{t: t, reg: command.NewRegistry(), col: 2}
	run := func(id string) func() { return func() { h.ran = append(h.ran, id) } }
	for _, id := range []string{"file.new", "file.open", "file.save", "edit.undo", "clip.split", "clip.rename",
		"clip.fadeIn", "play.toggle", "help.about", "app.palette", "tool.select"} {
		h.reg.Bind(command.Global, command.Binding{ID: id, Run: run(id)})
	}
	h.reg.Bind(command.Global, command.Binding{ID: "edit.redo", Run: run("edit.redo"), Enabled: func() bool { return false }})
	h.reg.Bind(command.Global, command.Binding{ID: "timeline.snapping", Run: func() { h.snap = !h.snap }, Checked: func() bool { return h.snap }})
	for i := 0; i < 7; i++ {
		i := i
		h.reg.Bind(command.PageScope("edit"), command.Binding{ID: "clip.color." + string(rune('0'+i)),
			Run: func() { h.col = i; h.ran = append(h.ran, "color") }, Checked: func() bool { return h.col == i }})
	}
	h.reg.SetScopeActive(command.PageScope("edit"), true)
	h.reg.SetDynamic(command.DynProjects, func() []command.Item {
		h.dyn++
		var out []command.Item
		for i, p := range h.proj {
			p := p
			c := 1
			if i == 0 {
				c = 2
			}
			out = append(out, command.Item{Label: p, Checked: c, Run: func() { h.ran = append(h.ran, "proj:"+p) }})
		}
		return out
	})

	h.w = test.NewWindow(nil)
	h.bar = New(h.w, h.reg, command.MenuBar)
	logo := canvas.NewText("Frame Studio", studiotheme.Text)
	logo.TextStyle = fyne.TextStyle{Bold: true}
	logo.TextSize = 13
	top := container.NewBorder(nil, nil, container.NewPadded(logo), nil, h.bar)
	body := canvas.NewRectangle(studiotheme.Panel)
	h.w.SetContent(container.NewBorder(top, nil, nil, nil, body))
	h.w.Resize(fyne.NewSize(1280, 800))
	t.Cleanup(h.w.Close)
	return h
}

func (h *harness) c() fyne.Canvas { return h.w.Canvas() }

// titlePos is the canvas position of the centre of menu title i.
func (h *harness) titlePos(i int) fyne.Position {
	bp, bs := h.bar.barRect()
	return fyne.NewPos(bp.X+h.bar.titleX[i]+h.bar.titleW[i]/2, bp.Y+bs.Height/2)
}

func (h *harness) menuIndex(title string) int {
	for i, m := range h.bar.menus {
		if m.Title == title {
			return i
		}
	}
	h.t.Fatalf("no menu %s", title)
	return -1
}

func (h *harness) pane(d int) *pane {
	h.t.Helper()
	if h.bar.layer == nil || len(h.bar.layer.panes) <= d {
		h.t.Fatalf("pane %d not open", d)
	}
	return h.bar.layer.panes[d]
}

func (h *harness) rowIndex(d int, label string) int {
	h.t.Helper()
	for i, r := range h.pane(d).rows {
		if r.label == label {
			return i
		}
	}
	h.t.Fatalf("no row %q in pane %d", label, d)
	return -1
}

// rowPos is the canvas position of the middle of a row.
func (h *harness) rowPos(d int, label string) fyne.Position {
	p := h.pane(d)
	i := h.rowIndex(d, label)
	return p.Position().Add(fyne.NewPos(60, p.tops[i]+menustyle.RowH/2))
}

func (h *harness) openTitle(title string) {
	test.TapCanvas(h.c(), h.titlePos(h.menuIndex(title)))
}

func TestOpenHoverSwitchAndClose(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Datei")
	if !h.bar.IsOpen() || h.bar.active != 0 {
		t.Fatal("Datei did not open")
	}
	if len(h.c().Overlays().List()) != 1 {
		t.Fatal("menu layer missing")
	}
	if r := h.pane(0).rows[0]; r.label != "Neues Projekt…" || r.shortcut != "Strg+N" || !r.enabled {
		t.Fatalf("first row %+v", r)
	}
	// hovering another title switches the open menu
	test.MoveMouse(h.c(), h.titlePos(h.menuIndex("Clip")))
	if h.bar.active != h.menuIndex("Clip") || h.pane(0).rows[0].label != "Am Abspielkopf teilen" {
		t.Fatal("hover did not switch to Clip")
	}
	// clicking the open title closes
	h.openTitle("Clip")
	if h.bar.IsOpen() {
		t.Fatal("click on the open title did not close")
	}
	// click outside closes
	h.openTitle("Hilfe")
	test.TapCanvas(h.c(), fyne.NewPos(700, 600))
	if h.bar.IsOpen() || len(h.c().Overlays().List()) != 0 {
		t.Fatal("click outside did not close")
	}
	// hovering titles while closed only highlights
	test.MoveMouse(h.c(), h.titlePos(2))
	if h.bar.hover != 2 || h.bar.IsOpen() {
		t.Fatal("hover while closed")
	}
}

func TestRunClosesThenRuns(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Datei")
	var openWhenRun bool
	h.reg.Bind(command.Global, command.Binding{ID: "file.save", Run: func() { openWhenRun = h.bar.IsOpen(); h.ran = append(h.ran, "save") }})
	test.TapCanvas(h.c(), h.rowPos(0, "Speichern"))
	if len(h.ran) != 1 || h.ran[0] != "save" || openWhenRun || h.bar.IsOpen() {
		t.Fatalf("ran %v, open during run %v", h.ran, openWhenRun)
	}
}

func TestDisabledItemsDoNotRun(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Bearbeiten")
	redo := h.pane(0).rows[h.rowIndex(0, "Wiederholen")]
	if redo.enabled {
		t.Fatal("redo should be disabled")
	}
	cut := h.pane(0).rows[h.rowIndex(0, "Ausschneiden")]
	if cut.enabled {
		t.Fatal("unbound command should be disabled")
	}
	test.TapCanvas(h.c(), h.rowPos(0, "Wiederholen"))
	test.TapCanvas(h.c(), h.rowPos(0, "Ausschneiden"))
	if len(h.ran) != 0 || !h.bar.IsOpen() {
		t.Fatalf("disabled ran %v / menu closed", h.ran)
	}
	// hover does not highlight a disabled row
	test.MoveMouse(h.c(), h.rowPos(0, "Wiederholen"))
	if h.pane(0).hover != -1 {
		t.Fatal("disabled row highlighted")
	}
	// keyboard skips disabled rows: Rückgängig -> (Wiederholen skipped) -> next enabled
	h.bar.Close()
	h.bar.Open(h.menuIndex("Bearbeiten"))
	p := h.pane(0)
	if p.rows[p.hover].label != "Rückgängig" {
		t.Fatalf("first selection %q", p.rows[p.hover].label)
	}
	h.key(fyne.KeyDown)
	if p.rows[p.hover].label == "Wiederholen" {
		t.Fatal("keyboard selected a disabled row")
	}
}

func TestSubmenuHoverAndCheckMarks(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Clip")
	test.MoveMouse(h.c(), h.rowPos(0, "Clipfarbe"))
	sub := h.pane(1)
	if len(sub.rows) != 7 || sub.rows[0].label != "Teal" {
		t.Fatalf("Clipfarbe rows %v", len(sub.rows))
	}
	if !sub.rows[2].checked || sub.rows[0].checked || !sub.rows[0].toggle {
		t.Fatal("check marks wrong")
	}
	// submenu opens to the right of its parent
	if sub.Position().X <= h.pane(0).Position().X {
		t.Fatal("submenu not on the right")
	}
	// moving to another plain row closes the submenu
	test.MoveMouse(h.c(), h.rowPos(0, "Am Abspielkopf teilen"))
	if len(h.bar.layer.panes) != 1 {
		t.Fatal("submenu stayed open")
	}
	test.MoveMouse(h.c(), h.rowPos(0, "Clipfarbe"))
	test.MoveMouse(h.c(), h.rowPos(1, "Blau"))
	if h.pane(0).hover != h.rowIndex(0, "Clipfarbe") {
		t.Fatal("parent row lost its highlight")
	}
	test.TapCanvas(h.c(), h.rowPos(1, "Blau"))
	if h.col != 5 || h.bar.IsOpen() {
		t.Fatal("submenu entry did not run")
	}
	// the toggle state is read at open time
	h.openTitle("Timeline")
	if h.pane(0).rows[h.rowIndex(0, "Magnetisch einrasten")].checked {
		t.Fatal("snapping checked too early")
	}
	test.TapCanvas(h.c(), h.rowPos(0, "Magnetisch einrasten"))
	h.openTitle("Timeline")
	if !h.pane(0).rows[h.rowIndex(0, "Magnetisch einrasten")].checked {
		t.Fatal("snapping not checked after toggle")
	}
}

func TestSubmenuFlipsLeftAtScreenEdge(t *testing.T) {
	h := newHarness(t)
	h.w.Resize(fyne.NewSize(560, 700))
	h.openTitle("Clip")
	test.MoveMouse(h.c(), h.rowPos(0, "Farbkorrektur"))
	par, sub := h.pane(0), h.pane(1)
	if sub.Position().X+sub.size.Width > h.c().Size().Width+0.5 {
		t.Fatal("submenu leaves the window")
	}
	if sub.Position().X >= par.Position().X {
		t.Fatalf("submenu not flipped: parent %v sub %v", par.Position(), sub.Position())
	}
}

func TestDynamicItems(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Fenster")
	if h.dyn != 1 {
		t.Fatalf("generator calls %d", h.dyn)
	}
	// no projects: the leading separator is dropped
	if h.pane(0).rows[0].label != "Nächstes Projekt" {
		t.Fatalf("first row %q", h.pane(0).rows[0].label)
	}
	h.bar.Close()
	h.proj = []string{"Showreel", "Werbespot"}
	h.openTitle("Fenster")
	if h.dyn != 2 {
		t.Fatal("generator not called on every open")
	}
	p := h.pane(0)
	if p.rows[0].label != "Showreel" || !p.rows[0].checked || p.rows[1].checked || !p.rows[1].toggle || !p.rows[2].sep {
		t.Fatalf("dynamic rows %+v", p.rows[:3])
	}
	test.TapCanvas(h.c(), h.rowPos(0, "Werbespot"))
	if len(h.ran) != 1 || h.ran[0] != "proj:Werbespot" {
		t.Fatalf("ran %v", h.ran)
	}
	// an empty dynamic submenu shows a disabled placeholder
	h.openTitle("Datei")
	test.MoveMouse(h.c(), h.rowPos(0, "Zuletzt geöffnet"))
	sub := h.pane(1)
	if len(sub.rows) != 1 || sub.rows[0].label != "Keine Einträge" || sub.rows[0].enabled {
		t.Fatalf("empty dynamic %+v", sub.rows)
	}
}

func TestRegistryChangesRefreshOpenMenu(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Bearbeiten")
	if h.pane(0).rows[h.rowIndex(0, "Kopieren")].enabled {
		t.Fatal("copy enabled before binding")
	}
	h.reg.Bind(command.Global, command.Binding{ID: "edit.copy", Run: func() {}})
	if !h.pane(0).rows[h.rowIndex(0, "Kopieren")].enabled {
		t.Fatal("open menu did not refresh")
	}
	h.reg.SetShortcut("edit.copy", "Ctrl+Alt+C")
	if got := h.pane(0).rows[h.rowIndex(0, "Kopieren")].shortcut; got != "Strg+Alt+C" {
		t.Fatalf("shortcut %q", got)
	}
	// closing unsubscribes: later changes do not touch the closed layer
	l := h.bar.layer
	h.bar.Close()
	n := len(l.panes[0].rows)
	h.reg.Refresh()
	if len(l.panes[0].rows) != n || h.bar.unsub != nil {
		t.Fatal("still subscribed")
	}
}

func (h *harness) key(k fyne.KeyName) {
	h.c().Focused().TypedKey(&fyne.KeyEvent{Name: k})
}

func TestKeyboardNavigation(t *testing.T) {
	h := newHarness(t)
	if got := string(h.bar.mnemonics); got != "dbctwarfh" {
		t.Fatalf("mnemonics %q", got)
	}
	if !h.bar.HandleShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyC, Modifier: fyne.KeyModifierAlt}) {
		t.Fatal("Alt+C not handled")
	}
	if h.bar.active != h.menuIndex("Clip") {
		t.Fatal("Alt+C did not open Clip")
	}
	if h.c().Focused() != h.bar.layer {
		t.Fatal("layer has no focus")
	}
	p := h.pane(0)
	if p.rows[p.hover].label != "Am Abspielkopf teilen" {
		t.Fatal("first row not selected")
	}
	// down to Clipfarbe: split, splitAll(disabled), rename, Clipfarbe
	h.key(fyne.KeyDown)
	if p.rows[p.hover].label != "Umbenennen…" {
		t.Fatalf("down -> %q", p.rows[p.hover].label)
	}
	h.key(fyne.KeyDown)
	if p.rows[p.hover].label != "Clipfarbe" {
		t.Fatalf("down -> %q", p.rows[p.hover].label)
	}
	h.key(fyne.KeyRight)
	sub := h.pane(1)
	if sub.rows[sub.hover].label != "Teal" {
		t.Fatal("Right did not open the submenu")
	}
	h.key(fyne.KeyLeft)
	if len(h.bar.layer.panes) != 1 {
		t.Fatal("Left did not close the submenu")
	}
	h.key(fyne.KeyReturn)
	sub = h.pane(1)
	h.key(fyne.KeyDown)
	h.key(fyne.KeyEscape)
	if len(h.bar.layer.panes) != 1 {
		t.Fatal("Esc did not close the submenu first")
	}
	h.key(fyne.KeyRight)
	h.key(fyne.KeyDown)
	h.key(fyne.KeyReturn)
	if h.col != 1 || h.bar.IsOpen() {
		t.Fatalf("Enter did not run Violett (col %d)", h.col)
	}
	// Left/Right on a plain row switch menus
	h.bar.Open(h.menuIndex("Clip"))
	h.key(fyne.KeyRight)
	if h.bar.active != h.menuIndex("Timeline") {
		t.Fatal("Right did not switch to Timeline")
	}
	h.key(fyne.KeyLeft)
	h.key(fyne.KeyLeft)
	if h.bar.active != h.menuIndex("Bearbeiten") {
		t.Fatal("Left did not switch back")
	}
	// typing a letter jumps to the entry
	h.key(fyne.KeyRight)
	h.c().Focused().TypedRune('u')
	p = h.pane(0)
	if p.hover < 0 || p.rows[p.hover].label != "Umbenennen…" {
		t.Fatal("letter did not jump")
	}
	h.key(fyne.KeyEscape) // menu -> keyboard mode on the bar
	if !h.bar.IsOpen() || len(h.bar.layer.panes) != 0 {
		t.Fatal("Esc did not leave the menu for the bar")
	}
	h.key(fyne.KeyEscape)
	if h.bar.IsOpen() {
		t.Fatal("second Esc did not close")
	}
}

func TestF10AndAltFocusTheBar(t *testing.T) {
	h := newHarness(t)
	if !h.bar.HandleKey(&fyne.KeyEvent{Name: fyne.KeyF10}) || !h.bar.IsOpen() || len(h.bar.layer.panes) != 0 {
		t.Fatal("F10 did not focus the bar")
	}
	h.key(fyne.KeyRight)
	h.key(fyne.KeyRight)
	if h.bar.active != 2 {
		t.Fatal("Right on the bar")
	}
	h.key(fyne.KeyDown)
	if len(h.bar.layer.panes) != 1 || h.bar.active != 2 {
		t.Fatal("Down did not open the focused menu")
	}
	h.key(fyne.KeyEscape)
	h.key(fyne.KeyEscape)
	if h.bar.HandleKey(&fyne.KeyEvent{Name: fyne.KeyA}) {
		t.Fatal("plain keys must pass through")
	}
	// Alt alone: down + up without another key
	alt := &fyne.KeyEvent{Name: desktop.KeyAltLeft}
	h.bar.HandleKeyDown(alt)
	if !h.bar.altHeld {
		t.Fatal("alt not tracked")
	}
	if !h.bar.HandleKeyUp(alt) || !h.bar.IsOpen() || h.bar.active != 0 {
		t.Fatal("Alt alone did not focus the bar")
	}
	// a mnemonic letter on the focused bar opens that menu
	h.c().Focused().TypedRune('h')
	if h.bar.active != h.menuIndex("Hilfe") || len(h.bar.layer.panes) != 1 {
		t.Fatal("letter on focused bar")
	}
	// Alt alone again inside the menu closes it
	l := h.bar.layer
	l.KeyDown(alt)
	l.KeyUp(alt)
	if h.bar.IsOpen() {
		t.Fatal("Alt did not close the menu")
	}
	// Alt + other key is not a solo press
	h.bar.HandleKeyDown(alt)
	h.bar.HandleKeyDown(&fyne.KeyEvent{Name: fyne.KeyS})
	if h.bar.HandleKeyUp(alt) || h.bar.IsOpen() {
		t.Fatal("Alt+S focused the bar")
	}
	// Ctrl shortcuts while a menu is open close it and run the command
	h.openTitle("Datei")
	h.c().Focused().(fyne.Shortcutable).TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyN, Modifier: fyne.KeyModifierControl})
	if h.bar.IsOpen() || len(h.ran) != 1 || h.ran[0] != "file.new" {
		t.Fatalf("shortcut while open: %v", h.ran)
	}
}

func TestSafeTriangleAndDelays(t *testing.T) {
	if !inTriangle(fyne.NewPos(5, 5), fyne.NewPos(0, 5), fyne.NewPos(10, 0), fyne.NewPos(10, 10)) {
		t.Fatal("inside")
	}
	if inTriangle(fyne.NewPos(5, 12), fyne.NewPos(0, 5), fyne.NewPos(10, 0), fyne.NewPos(10, 10)) {
		t.Fatal("outside")
	}
	h := newHarness(t)
	q := kit.UseUIQueue(t)
	openDelay, safeDelay = 30*time.Millisecond, 60*time.Millisecond
	h.openTitle("Clip")
	test.MoveMouse(h.c(), h.rowPos(0, "Clipfarbe"))
	if len(h.bar.layer.panes) != 1 {
		t.Fatal("submenu opened without delay")
	}
	if !q.Until(func() bool { return len(h.bar.layer.panes) == 2 }, time.Second) {
		t.Fatal("submenu did not open after the delay")
	}
	// diagonal move towards the submenu across the next row keeps it open
	from := h.rowPos(0, "Clipfarbe")
	from.X = h.pane(0).Position().X + h.pane(0).size.Width - 30
	test.MoveMouse(h.c(), from)
	next := h.rowPos(0, "Blenden")
	next.X = from.X + 20
	test.MoveMouse(h.c(), next)
	time.Sleep(40 * time.Millisecond)
	q.Drain()
	if len(h.bar.layer.panes) != 2 || h.pane(0).sub != h.rowIndex(0, "Clipfarbe") {
		t.Fatal("diagonal move closed the submenu")
	}
	test.MoveMouse(h.c(), h.rowPos(1, "Oliv"))
	time.Sleep(100 * time.Millisecond)
	q.Drain()
	if len(h.bar.layer.panes) != 2 || h.pane(1).hover != h.rowIndex(1, "Oliv") {
		t.Fatal("reaching the submenu did not keep it")
	}
	// closing stops pending timers
	test.MoveMouse(h.c(), h.rowPos(0, "Am Abspielkopf teilen"))
	h.bar.Close()
	time.Sleep(80 * time.Millisecond)
	q.Drain()
	if h.bar.IsOpen() {
		t.Fatal("timer reopened")
	}
}

func TestFocusLossCloses(t *testing.T) {
	h := newHarness(t)
	h.openTitle("Datei")
	h.bar.layer.FocusLost()
	if h.bar.IsOpen() {
		t.Fatal("focus loss did not close")
	}
}

func TestPalette(t *testing.T) {
	h := newHarness(t)
	p := showPalette(h.w, h.reg)
	if h.c().Focused() != p.entry {
		t.Fatal("search not focused")
	}
	n := 0
	for _, e := range p.entries {
		if e.kind == kindCommand {
			n++
		}
	}
	if n != len(h.reg.Available()) {
		t.Fatalf("palette lists %d of %d", n, len(h.reg.Available()))
	}
	test.Type(p.entry, "speich")
	if p.entries[0].cmd.ID != "file.save" || p.entries[p.sel].cmd.ID != "file.save" {
		t.Fatalf("filter: %+v", p.entries)
	}
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(h.ran) != 1 || h.ran[0] != "file.save" || p.pop.Visible() {
		t.Fatal("Enter did not run")
	}
	if len(mru) != 1 || mru[0] != "file.save" {
		t.Fatal("not remembered")
	}
	// recently used first, under a header
	p = showPalette(h.w, h.reg)
	if p.entries[0].kind != kindHeader || p.entries[1].cmd.ID != "file.save" || !p.entries[1].recent {
		t.Fatal("MRU not first")
	}
	if p.entries[p.sel].cmd.ID != "file.save" {
		t.Fatal("MRU not selected")
	}
	// disabled entries are shown dimmed but do not run
	p.entry.SetText("wiederholen")
	if len(p.entries) != 1 || p.entries[0].cmd.Enabled {
		t.Fatalf("redo entry %+v", p.entries)
	}
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(h.ran) != 1 || !p.pop.Visible() {
		t.Fatal("disabled command ran")
	}
	// keys move the selection; a click runs
	p.entry.SetText("")
	first := p.sel
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if p.sel == first {
		t.Fatal("Down did not move")
	}
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
	if p.sel != first {
		t.Fatal("Up did not move back")
	}
	p.entry.SetText("abspielen")
	test.Tap(p.rows[0])
	if h.ran[len(h.ran)-1] != "play.toggle" || mru[0] != "play.toggle" || mru[1] != "file.save" {
		t.Fatalf("click run %v mru %v", h.ran, mru)
	}
	// shortcut text finds commands too; nothing found shows a hint; Esc closes
	p = showPalette(h.w, h.reg)
	p.entry.SetText("strg+n")
	if p.entries[0].cmd.ID != "file.new" {
		t.Fatalf("shortcut search %+v", p.entries)
	}
	p.entry.SetText("xyzzy")
	if len(p.entries) != 1 || p.entries[0].kind != kindHeader || p.sel != -1 {
		t.Fatal("empty result")
	}
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if p.pop.Visible() {
		t.Fatal("Esc did not close")
	}
}

// TestScreenshot renders the bar with Clip > Clipfarbe open and the palette to
// /tmp/fs-r3-menubar*.png when FS_SCREENSHOT is set.
func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r3-menubar*.png")
	}
	h := newHarness(t)
	h.w.Resize(fyne.NewSize(1440, 900))
	h.reg.SetShortcut("clip.color.2", "Alt+3")
	h.bar.HandleKeyDown(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	h.openTitle("Clip")
	test.MoveMouse(h.c(), h.rowPos(0, "Clipfarbe"))
	test.MoveMouse(h.c(), h.rowPos(1, "Rosé"))
	write(t, "/tmp/fs-r3-menubar.png", h.c())
	h.bar.Close()
	h.bar.HandleKeyUp(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	h.bar.Open(h.menuIndex("Timeline"))
	h.key(fyne.KeyDown)
	h.key(fyne.KeyDown)
	h.key(fyne.KeyDown)
	write(t, "/tmp/fs-r3-menubar-timeline.png", h.c())
	h.bar.Close()
	remember("clip.split")
	remember("file.save")
	p := showPalette(h.w, h.reg)
	write(t, "/tmp/fs-r3-menubar-palette.png", h.c())
	p.entry.SetText("ab")
	write(t, "/tmp/fs-r3-menubar-palette-search.png", h.c())
}

func write(t *testing.T, path string, c fyne.Canvas) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Capture()); err != nil {
		t.Fatal(err)
	}
}
