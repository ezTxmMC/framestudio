package shell

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/display"
	"framestudio-proto/src/ui/kit"
	wslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/settingsui"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/workspace"
)

// spareConfig is a config folder that outlives every test: tests that load the settings
// from their own temporary folder point the settings back here when they end, so later
// settings changes of other tests still have somewhere to be saved.
var spareConfig string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fs-shell-config-")
	if err != nil {
		panic(err)
	}
	spareConfig = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// loadSettingsIn makes dir the config folder and loads the settings from it (so changes
// are saved there, like in the app). The settings go back to factory values afterwards.
func loadSettingsIn(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Setenv("XDG_CONFIG_HOME", spareConfig)
		_, _ = settings.Load()
		_ = settings.Update(func(s *settings.Settings) { *s = settings.Defaults() })
	})
}

// --- fake display backend --------------------------------------------------------

type fdState struct {
	mon, mode string
	r         wslayout.Rect
}

// fakeDisp is the user's desk (DP-3 left, DP-2 primary, HDMI-A-1 right) without GLFW.
type fakeDisp struct {
	ms     []display.Monitor
	canPos bool
	st     map[fyne.Window]*fdState
}

func userDesk() []display.Monitor {
	mk := func(id string, x int, primary bool, hz int) display.Monitor {
		return display.Monitor{ID: id, Name: id + " · 2560×1440", X: x, W: 2560, H: 1440, Refresh: hz, Scale: 1.25, Primary: primary}
	}
	return []display.Monitor{mk("DP-2", 2560, true, 155), mk("DP-3", 0, false, 155), mk("HDMI-A-1", 5120, false, 144)}
}

// useFakeDisplay makes the shells built afterwards use a fake display backend.
func useFakeDisplay(t *testing.T, x11 bool) *fakeDisp {
	t.Helper()
	f := &fakeDisp{ms: userDesk(), canPos: x11, st: map[fyne.Window]*fdState{}}
	old := newWorkspace
	newWorkspace = func(h workspace.Host) *workspace.Manager { return workspace.NewWithDisplay(h, f) }
	t.Cleanup(func() { newWorkspace = old })
	return f
}

func (f *fakeDisp) state(w fyne.Window) *fdState {
	s := f.st[w]
	if s == nil {
		s = &fdState{mode: wslayout.ModeWindow}
		f.st[w] = s
	}
	return s
}

func (f *fakeDisp) has(id string) bool {
	return slices.ContainsFunc(f.ms, func(m display.Monitor) bool { return m.ID == id })
}

func (f *fakeDisp) Platform() string {
	if f.canPos {
		return "x11"
	}
	return "wayland"
}
func (f *fakeDisp) CanPosition() bool           { return f.canPos }
func (f *fakeDisp) Monitors() []display.Monitor { return slices.Clone(f.ms) }
func (f *fakeDisp) Fullscreen(w fyne.Window, id string, on bool) error {
	s := f.state(w)
	if !on {
		s.mode = wslayout.ModeWindow
		w.SetFullScreen(false)
		return nil
	}
	if id != "" && !f.has(id) {
		return display.ErrNoMonitor
	}
	if id != "" {
		s.mon = id
	}
	s.mode = wslayout.ModeFullscreen
	w.SetFullScreen(true)
	return nil
}
func (f *fakeDisp) Maximize(w fyne.Window, id string) error {
	if id != "" && !f.has(id) {
		return display.ErrNoMonitor
	}
	s := f.state(w)
	if id != "" {
		s.mon = id
	}
	w.SetFullScreen(false)
	s.mode = wslayout.ModeMaximized
	return nil
}
func (f *fakeDisp) Place(w fyne.Window, id string, x, y, width, height int) error {
	s := f.state(w)
	w.SetFullScreen(false)
	s.mode, s.mon = wslayout.ModeWindow, id
	if !f.canPos {
		s.r = wslayout.Rect{W: width, H: height}
		if id != "" {
			return display.ErrNoPositioning
		}
		return nil
	}
	s.r = wslayout.Rect{X: x, Y: y, W: width, H: height}
	return nil
}
func (f *fakeDisp) Geometry(w fyne.Window) (string, int, int, int, int, bool) {
	s := f.state(w)
	if s.mode == wslayout.ModeFullscreen {
		return s.mon, 0, 0, 2560, 1440, true
	}
	return s.mon, s.r.X, s.r.Y, s.r.W, s.r.H, true
}
func (f *fakeDisp) IsMaximized(w fyne.Window) bool    { return f.state(w).mode == wslayout.ModeMaximized }
func (f *fakeDisp) SetCursorHidden(fyne.Window, bool) {}
func (f *fakeDisp) Forget(w fyne.Window)              { delete(f.st, w) }
func (f *fakeDisp) OnChange(func())                   {}

// --- helpers ----------------------------------------------------------------------

// buildShell builds the real shell with the real pages in app a (several shells may
// share one app, as after a restart).
func buildShell(t *testing.T, a fyne.App, q *kit.UIQueue) (*Shell, fyne.Window) {
	t.Helper()
	w := a.NewWindow("Frame Studio")
	w.Resize(fyne.NewSize(1440, 900))
	sh := New(a, w)
	sh.closeWindow = func() {}
	t.Cleanup(func() {
		sh.shutdown()
		q.Settle(50*time.Millisecond, 2*time.Second)
		sh.cache.Close()
		w.Close()
	})
	return sh, w
}

func newR3App(t *testing.T) (fyne.App, *kit.UIQueue) {
	t.Helper()
	useFakeSound(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // layouts and recent files stay in the test
	resetSettings(t)
	a := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	return a, q
}

func clipCount(p *model.Project) int {
	n := 0
	for _, tr := range p.Tracks {
		n += len(tr.Clips)
	}
	return n
}

func trackCount(p *model.Project, kind model.TrackKind) int {
	n := 0
	for _, tr := range p.Tracks {
		if tr.Kind == kind {
			n++
		}
	}
	return n
}

// catalogOwner is the page whose scope binds id ("" = bound globally, available on
// every page). It follows the OWNER comments of command/ids.go; the shell additionally
// binds global fallbacks of a few edit-page commands for the other viewer pages.
func catalogOwner(id string) string {
	global := []string{
		"edit.undo", "edit.redo", "edit.selectAll", "edit.deselect",
		"timeline.setIn", "timeline.setOut", "timeline.clearRange",
		"timeline.follow.page", "timeline.follow.smooth", "timeline.follow.off",
		"timeline.showFilmstrip", "timeline.showWaveforms", "view.safeArea",
		"media.import", "media.importFolder",
	}
	switch {
	case slices.Contains(global, id):
		return ""
	case strings.HasPrefix(id, "media."):
		return page.Media
	case strings.HasPrefix(id, "grade."), strings.HasPrefix(id, "scopes."):
		return page.Color
	case strings.HasPrefix(id, "edit."), strings.HasPrefix(id, "clip."), strings.HasPrefix(id, "tool."),
		strings.HasPrefix(id, "timeline."), strings.HasPrefix(id, "marker."), id == "view.viewerFullscreen":
		return page.Edit
	}
	return "" // file, deliver, app, play, view, workspace, window, help
}

// TestCatalogBoundEverywhere builds the real shell with every page created and checks
// that each catalog command is bound where its owner says, and that no two commands
// available together share a shortcut.
func TestCatalogBoundEverywhere(t *testing.T) {
	a, q := newR3App(t)
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Katalog", 1920, 1080, 25, 100)})
	if sh.Active() == nil {
		t.Fatal("project did not open")
	}
	pages := []string{page.Media, page.Edit, page.Color, page.Audio, page.Deliver}
	available := map[string]map[string]bool{}
	for _, id := range pages {
		sh.showPageID(id)
		q.Settle(30*time.Millisecond, 3*time.Second)
		set := map[string]bool{}
		byKey := map[string][]string{}
		for _, c := range sh.reg.Available() {
			set[c.ID] = true
			if sc := command.Normalize(c.Shortcut); sc != "" {
				byKey[sc] = append(byKey[sc], c.ID)
			}
		}
		available[id] = set
		for sc, ids := range byKey {
			if len(ids) > 1 {
				t.Errorf("page %s: shortcut %s is used by %v", id, command.Display(sc), ids)
			}
		}
	}
	if n := len(available[page.Edit]); n < len(command.Catalog)-20 {
		t.Fatalf("only %d commands available on the edit page", n)
	}
	for _, in := range command.Catalog {
		owner := catalogOwner(in.ID)
		if owner == "" {
			for _, pg := range pages {
				if !available[pg][in.ID] {
					t.Errorf("global command %s (%s) not available on page %s", in.ID, in.Label, pg)
				}
			}
			continue
		}
		if !available[owner][in.ID] {
			t.Errorf("command %s (%s) not available on its page %s", in.ID, in.Label, owner)
		}
	}
	// The manager shows no page: page commands are off, global ones stay.
	sh.ShowManager()
	if c, _ := sh.reg.Get("clip.split"); c.Available {
		t.Error("page commands must be off in the manager")
	}
	if c, _ := sh.reg.Get("file.open"); !c.Available {
		t.Error("global commands stay in the manager")
	}
}

// TestMenuBarAndPaletteRunCommands opens a menu by clicking its title, clicks an item,
// and runs a command from the palette.
func TestMenuBarAndPaletteRunCommands(t *testing.T) {
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Menü", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	q.Settle(30*time.Millisecond, 3*time.Second)
	s := sh.Active()
	mb := sh.bars.menu
	videos := trackCount(s.Project, model.TrackVideo)

	pos, ok := mb.TitlePos("Timeline")
	if !ok {
		t.Fatal("no Timeline menu")
	}
	test.TapCanvas(w.Canvas(), pos)
	if !mb.IsOpen() {
		t.Fatal("clicking the title did not open the menu")
	}
	item, ok := mb.ItemPos("Videospur hinzufügen")
	if !ok {
		t.Fatal("Videospur hinzufügen not in the Timeline menu")
	}
	test.TapCanvas(w.Canvas(), item)
	if mb.IsOpen() {
		t.Fatal("menu still open after running an entry")
	}
	if got := trackCount(s.Project, model.TrackVideo); got != videos+1 {
		t.Fatalf("video tracks %d, want %d", got, videos+1)
	}

	// Ansicht › Seite › Farbe through the keyboard: Alt+A opens Ansicht.
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyA, Modifier: fyne.KeyModifierAlt})
	if !mb.IsOpen() {
		t.Fatal("Alt+A did not open Ansicht")
	}
	mb.Close()

	// Palette: search, Enter runs the first match.
	audios := trackCount(s.Project, model.TrackAudio)
	if !sh.reg.Run("app.palette") {
		t.Fatal("palette did not open")
	}
	if w.Canvas().Focused() == nil {
		t.Fatal("palette search field has no focus")
	}
	test.Type(w.Canvas().Focused().(fyne.Focusable), "audiospur hinzu")
	w.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := trackCount(s.Project, model.TrackAudio); got != audios+1 {
		t.Fatalf("audio tracks %d, want %d (palette did not run the command)", got, audios+1)
	}
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("palette still open")
	}
}

// TestKeymapOverridePersists changes a shortcut like the keymap editor does, uses it,
// and finds it again in the settings file and in a new shell.
func TestKeymapOverridePersists(t *testing.T) {
	a, q := newR3App(t)
	cfg := t.TempDir()
	loadSettingsIn(t, cfg)
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Tasten", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	q.Settle(30*time.Millisecond, 3*time.Second)
	s := sh.Active()
	s.Seek(40)

	// What keymapui does on "Aufnehmen": set the shortcut and save the keymap.
	sh.reg.SetShortcut("clip.split", "Ctrl+Alt+J")
	if err := settings.Update(func(st *settings.Settings) { st.Keymap = sh.reg.Keymap() }); err != nil {
		t.Fatal(err)
	}
	n := clipCount(s.Project)
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if clipCount(s.Project) != n {
		t.Fatal("S still splits after the shortcut moved")
	}
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	if clipCount(s.Project) != n+1 {
		t.Fatalf("Strg+Alt+J did not split: %d clips, want %d", clipCount(s.Project), n+1)
	}
	raw, err := os.ReadFile(filepath.Join(cfg, "framestudio", "settings.json"))
	if err != nil || !strings.Contains(string(raw), `"clip.split": "Ctrl+Alt+J"`) {
		t.Fatalf("keymap not saved: %v\n%s", err, raw)
	}
	// Next start: settings come from the file, the new registry uses the override.
	loaded, err := settings.Load()
	if err != nil || loaded.Keymap["clip.split"] != "Ctrl+Alt+J" {
		t.Fatalf("reloaded keymap %v (%v)", loaded.Keymap, err)
	}
	sh2, _ := buildShell(t, a, q)
	if got := sh2.reg.Shortcut("clip.split"); got != "Ctrl+Alt+J" {
		t.Fatalf("new shell uses %q for clip.split", got)
	}
}

func editLayout(t *testing.T, sh *Shell) wslayout.Page {
	t.Helper()
	la, ok := sh.active.pages[page.Edit].(page.LayoutAware)
	if !ok {
		t.Fatal("edit page saves no layout")
	}
	return la.LayoutState()
}

// TestLayoutSurvivesQuit hides the viewer and detaches the clean feed fullscreen on
// HDMI-A-1, quits, and finds both again in a new shell (fake display backend).
func TestLayoutSurvivesQuit(t *testing.T) {
	disp := useFakeDisplay(t, false)
	a, q := newR3App(t)
	loadSettingsIn(t, t.TempDir())
	path := saveSample(t, "Layout", 1920, 1080, 25, 100)

	sh, _ := buildShell(t, a, q)
	sh.Start([]string{path})
	sh.showPageID(page.Edit)
	q.Settle(30*time.Millisecond, 3*time.Second)
	if !sh.reg.Run("edit.hide.viewer") {
		t.Fatal("edit.hide.viewer did not run")
	}
	if !editLayout(t, sh).Hidden["viewer"] {
		t.Fatal("viewer not hidden")
	}
	sh.ws.Detach(page.PanelCleanFeed, "HDMI-A-1", true)
	cf := sh.ws.Window(page.PanelCleanFeed)
	if cf == nil || !cf.FullScreen() || disp.state(cf).mon != "HDMI-A-1" {
		t.Fatal("clean feed not fullscreen on HDMI-A-1")
	}
	sh.Quit()
	q.Settle(30*time.Millisecond, 3*time.Second)
	if len(sh.ws.Windows()) != 0 {
		t.Fatalf("detached windows still open after quit: %v", sh.ws.Windows())
	}

	sh2, _ := buildShell(t, a, q)
	sh2.Start([]string{path})
	q.Settle(30*time.Millisecond, 3*time.Second)
	if got := sh2.ws.Windows(); !slices.Contains(got, page.PanelCleanFeed) {
		t.Fatalf("restored windows %v, want the clean feed", got)
	}
	cf2 := sh2.ws.Window(page.PanelCleanFeed)
	if st := disp.state(cf2); !cf2.FullScreen() || st.mon != "HDMI-A-1" {
		t.Fatalf("restored clean feed on %q fullscreen=%v", st.mon, cf2.FullScreen())
	}
	if sh2.active.current != page.Edit {
		t.Fatalf("restored page %s, want the edit page", sh2.active.current)
	}
	if !editLayout(t, sh2).Hidden["viewer"] {
		t.Fatal("viewer visible again after restore")
	}
	if c, _ := sh2.reg.Get("edit.hide.viewer"); !c.Checked {
		t.Fatal("menu check mark of edit.hide.viewer lost")
	}
}

// TestDetachedTimelineFollowsTab detaches the timeline and switches project tabs: the
// detached timeline shows the active project.
func TestDetachedTimelineFollowsTab(t *testing.T) {
	useFakeDisplay(t, true)
	a, q := newR3App(t)
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Erstes", 1920, 1080, 25, 100), saveSample(t, "Zweites", 1280, 720, 25, 60)})
	if len(sh.projects) != 2 {
		t.Fatal("two projects expected")
	}
	sh.activateIndex(0)
	sh.ws.Detach(page.PanelTimeline, "DP-3", false)
	tw := sh.ws.Window(page.PanelTimeline)
	if tw == nil {
		t.Fatal("timeline not detached")
	}
	shows := func(i int) bool {
		q.Settle(30*time.Millisecond, 3*time.Second)
		tl := findTimeline(tw)
		if tl == nil {
			t.Fatal("no timeline in the detached window")
		}
		_, ok := tl.LanePoint(10, sh.projects[i].sess.Project.Tracks[0].ID)
		return ok
	}
	if !shows(0) || shows(1) {
		t.Fatal("detached timeline does not show the first project")
	}
	sh.activateIndex(1)
	if !shows(1) || shows(0) {
		t.Fatal("detached timeline did not follow the tab switch")
	}
	// Closing the active tab moves it to the remaining project.
	sh.closeProject(sh.active)
	if !shows(0) {
		t.Fatal("detached timeline did not follow after closing a tab")
	}
}

// TestScreenshotIntegrationR3 renders the main window with an open menu and submenu,
// the command palette, the settings window and a detached clean feed.
func TestScreenshotIntegrationR3(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render screenshots")
	}
	useFakeDisplay(t, false)
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	still, video, tone := demoMedia(t, t.TempDir())
	proj := demoProject(t, still, video, tone)
	path := filepath.Join(t.TempDir(), "Integration Demo.fsproj")
	if err := store.Save(proj, path); err != nil {
		t.Fatal(err)
	}
	sh.Start([]string{path})
	s := sh.Active()
	s.Seek(110)
	sh.showPageID(page.Edit)
	q.Settle(400*time.Millisecond, 15*time.Second)
	mb := sh.bars.menu

	pos, _ := mb.TitlePos("Clip")
	test.TapCanvas(w.Canvas(), pos)
	q.Settle(50*time.Millisecond, 2*time.Second)
	writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-menu.png")
	mb.Close()

	mb.Open(slices.IndexFunc(command.MenuBar, func(m command.Menu) bool { return m.Title == "Arbeitsbereich" }))
	q.Settle(50*time.Millisecond, 2*time.Second)
	writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-menu-workspace.png")
	mb.Close()

	sh.reg.Run("app.palette")
	test.Type(w.Canvas().Focused().(fyne.Focusable), "spur")
	q.Settle(50*time.Millisecond, 2*time.Second)
	writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-palette.png")
	w.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})

	settingsui.Show(a, w, sh.reg, "")
	sw := windowTitled(a, "Einstellungen")
	if sw == nil {
		t.Fatal("settings window not found")
	}
	sw.Resize(fyne.NewSize(980, 680))
	q.Settle(100*time.Millisecond, 3*time.Second)
	writePNG(t, sw.Canvas().Capture(), "/tmp/fs-int-settings.png")
	settingsui.Show(a, w, sh.reg, "keymap")
	q.Settle(100*time.Millisecond, 3*time.Second)
	writePNG(t, sw.Canvas().Capture(), "/tmp/fs-int-settings-keymap.png")
	settingsui.Close()

	sh.ws.Detach(page.PanelTimeline, "DP-3", false)
	if tw := sh.ws.Window(page.PanelTimeline); tw != nil {
		tw.Resize(fyne.NewSize(1440, 500))
		q.Settle(300*time.Millisecond, 5*time.Second)
		writePNG(t, tw.Canvas().Capture(), "/tmp/fs-int-detached-timeline.png")
	}
	sh.ws.Detach(page.PanelProgram, "HDMI-A-1", false)
	if pw := sh.ws.Window(page.PanelProgram); pw != nil {
		pw.Resize(fyne.NewSize(1280, 720))
		q.Settle(300*time.Millisecond, 5*time.Second)
		writePNG(t, pw.Canvas().Capture(), "/tmp/fs-int-detached-program.png")
	}
	sh.reg.Run("workspace.monitors")
	q.Settle(100*time.Millisecond, 3*time.Second)
	writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-monitors.png")
}

func windowTitled(a fyne.App, prefix string) fyne.Window {
	for _, w := range a.Driver().AllWindows() {
		if strings.HasPrefix(w.Title(), prefix) {
			return w
		}
	}
	return nil
}

func TestParseDebugMultimon(t *testing.T) {
	got := parseDebugMultimon("1")
	want := []debugPlacement{{"cleanfeed", "HDMI-A-1", "fullscreen"}, {"timeline", "DP-3", "window"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	got = parseDebugMultimon(" program=DP-2:maximized, scopes=DP-3 ,bogus")
	want = []debugPlacement{{"program", "DP-2", "maximized"}, {"scopes", "DP-3", "window"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}
