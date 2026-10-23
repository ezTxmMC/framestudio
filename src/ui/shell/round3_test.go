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

	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/command"
	wslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// cmdPage provides page commands and a layout state.
type cmdPage struct {
	fakePage
	project string
	runs    map[string]int
	layout  wslayout.Page
	applied []wslayout.Page
}

func (c *cmdPage) Commands() []command.Binding {
	return []command.Binding{{ID: "clip.split", Run: func() { c.runs[c.project+"/"+c.id]++ }}}
}
func (c *cmdPage) LayoutState() wslayout.Page  { return c.layout }
func (c *cmdPage) ApplyLayout(l wslayout.Page) { c.applied = append(c.applied, l) }

// useCmdPages makes the edit and colour pages command providers; runs counts per
// "<project>/<page>".
func useCmdPages(sh *Shell) (runs map[string]int, made map[string][]*cmdPage) {
	runs = map[string]int{}
	made = map[string][]*cmdPage{}
	for i := range sh.pageDefs {
		id := sh.pageDefs[i].ID
		if id != page.Edit && id != page.Color {
			continue
		}
		sh.pageDefs[i].New = func(ctx *page.Context) page.Page {
			c := &cmdPage{fakePage: fakePage{id: id, handle: true}, project: ctx.Session.Project.Name, runs: runs}
			made[id] = append(made[id], c)
			return c
		}
	}
	return runs, made
}

func TestGlobalCommandsOnEveryPage(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Global", 1920, 1080, 25, 100)})
	for _, d := range sh.pageDefs {
		sh.reg.Run("view.page." + d.ID)
		if sh.active.current != d.ID {
			t.Fatalf("view.page.%s showed %s", d.ID, sh.active.current)
		}
		for _, id := range []string{"file.save", "file.saveAs", "file.close", "play.toggle", "play.forward", "app.settings", "help.shortcuts"} {
			if c, _ := sh.reg.Get(id); !c.Available || !c.Enabled {
				t.Errorf("%s on %s: available %v enabled %v", id, d.ID, c.Available, c.Enabled)
			}
		}
		for _, o := range sh.pageDefs {
			if c, _ := sh.reg.Get("view.page." + o.ID); c.Checked != (o.ID == d.ID) || !c.Toggle {
				t.Errorf("on %s: view.page.%s checked %v", d.ID, o.ID, c.Checked)
			}
		}
	}
	// In the manager the project commands are off, file commands stay.
	sh.ShowManager()
	if c, _ := sh.reg.Get("play.toggle"); c.Enabled {
		t.Error("transport enabled in the manager")
	}
	if c, _ := sh.reg.Get("file.new"); !c.Enabled {
		t.Error("file.new disabled in the manager")
	}
	if c, _ := sh.reg.Get("file.close"); c.Enabled {
		t.Error("file.close enabled in the manager")
	}
}

func TestPageScopeFollowsPageAndTab(t *testing.T) {
	sh, _, _ := newTestShell(t)
	runs, _ := useCmdPages(sh)
	a := saveSample(t, "A", 1920, 1080, 25, 100)
	b := saveSample(t, "B", 1920, 1080, 25, 100)
	sh.Start([]string{a})
	if !sh.reg.Run("clip.split") || runs["A/edit"] != 1 {
		t.Fatalf("page command of A/edit: %v", runs)
	}
	sh.showPageID(page.Media) // no commands there
	if c, _ := sh.reg.Get("clip.split"); c.Available {
		t.Fatal("edit page command still available on Medien")
	}
	sh.showPageID(page.Color)
	sh.reg.Run("clip.split")
	if runs["A/color"] != 1 {
		t.Fatalf("colour page command: %v", runs)
	}

	_ = sh.OpenFile(b) // B opens on Schnitt
	sh.reg.Run("clip.split")
	if runs["B/edit"] != 1 || runs["A/edit"] != 1 {
		t.Fatalf("tab switch did not rebind: %v", runs)
	}
	// Keys dispatch through the page scope too (the page declines S here).
	sh.active.pages[page.Edit].(*cmdPage).handle = false
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if runs["B/edit"] != 2 {
		t.Fatalf("S did not reach the page command: %v", runs)
	}
	sh.activateIndex(0) // back to A, on Farbe
	sh.reg.Run("clip.split")
	if runs["A/color"] != 2 || runs["B/edit"] != 2 {
		t.Fatalf("switching back: %v", runs)
	}
	sh.ShowManager()
	if c, _ := sh.reg.Get("clip.split"); c.Available {
		t.Fatal("page command available in the manager")
	}
	sh.leaveManager()
	sh.closeNow(sh.active) // closing A leaves B active
	sh.reg.Run("clip.split")
	if runs["B/edit"] != 3 {
		t.Fatalf("after closing A: %v", runs)
	}
	sh.closeNow(sh.active)
	if c, _ := sh.reg.Get("clip.split"); c.Available {
		t.Error("page command survives its closed project")
	}
}

func TestMenuCommandsRun(t *testing.T) {
	sh, w, _ := newTestShell(t)
	pa := saveSample(t, "Menü", 1920, 1080, 25, 100)
	sh.Start([]string{pa})
	s := sh.Active()

	// Datei
	edit(t, sh)
	if !sh.reg.Run("file.save") || s.Dirty {
		t.Fatal("file.save did not save")
	}
	edit(t, sh)
	if !sh.reg.Run("file.saveAll") || s.Dirty {
		t.Fatal("file.saveAll did not save")
	}
	sh.reg.Run("file.new")
	if w.Canvas().Overlays().Top() == nil {
		t.Fatal("file.new shows no dialog")
	}
	w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())

	// Bearbeiten
	edit(t, sh)
	n := s.Project.Duration()
	sh.reg.Run("edit.undo")
	if s.Project.Duration() == n {
		t.Fatal("edit.undo did nothing")
	}
	sh.reg.Run("edit.redo")
	if s.Project.Duration() != n {
		t.Fatal("edit.redo did nothing")
	}
	// file.revert reloads the file in the same tab and page.
	sh.showPageID(page.Color)
	if err := sh.revert(sh.active); err != nil {
		t.Fatal(err)
	}
	if len(sh.projects) != 1 || sh.Active() == s || sh.Active().Dirty || sh.active.current != page.Color {
		t.Fatalf("revert: tabs %d dirty %v page %s", len(sh.projects), sh.Active().Dirty, sh.active.current)
	}
	s = sh.Active()
	if s.Project.Duration() >= n {
		t.Fatalf("revert kept the unsaved clip: %d", s.Project.Duration())
	}

	// Wiedergabe
	sh.reg.Run("play.forward")
	sh.reg.Run("play.forward")
	if !s.Playing || s.Rate != 2 {
		t.Fatalf("play.forward twice: playing %v rate %v", s.Playing, s.Rate)
	}
	sh.reg.Run("play.stop")
	sh.reg.Run("play.end")
	if s.Playhead != s.Project.Duration() {
		t.Fatal("play.end")
	}
	sh.reg.Run("play.secondBack")
	if s.Playhead != s.Project.Duration()-25 {
		t.Fatalf("play.secondBack to %d", s.Playhead)
	}
	s.SetIn(10)
	s.SetOut(40)
	sh.reg.Run("play.inOut")
	if !s.Playing || s.Playhead != 10 {
		t.Fatalf("play.inOut: playing %v at %d", s.Playing, s.Playhead)
	}
	sh.reg.Run("play.stop")
	sh.reg.Run("play.quality.half")
	if settings.Get().Playback.PreviewQuality != "half" {
		t.Fatal("play.quality.half not saved")
	}
	for _, q := range []string{"auto", "full", "half", "quarter"} {
		if c, _ := sh.reg.Get("play.quality." + q); c.Checked != (q == "half") {
			t.Errorf("quality %s checked %v", q, c.Checked)
		}
	}
	sh.reg.Run("play.mute")
	sh.reg.Run("timeline.follow.smooth")
	sh.reg.Run("view.safeArea")
	st := settings.Get()
	if !st.Audio.Muted || st.Playback.Follow != "smooth" || !st.Playback.SafeArea {
		t.Fatalf("toggles: %+v %+v", st.Audio, st.Playback)
	}

	// Ansicht
	sh.reg.Run("view.pageBar")
	if settings.Get().Display.PageBar || len(sh.bars.pages[0].label.Text) == 0 {
		t.Fatal("view.pageBar did not switch the setting")
	}
	if !sh.bars.bottom.Visible() || sh.bars.bottom.MinSize().Height >= pageBarHeight {
		t.Fatalf("without page bar the bottom shows the status line only: %v", sh.bars.bottom.MinSize())
	}
	sh.reg.Run("view.statusBar")
	if sh.bars.bottom.Visible() {
		t.Fatal("bottom bar visible with page bar and status line off")
	}
	sh.reg.Run("view.pageBar")
	sh.reg.Run("view.statusBar")
	if !sh.bars.bottom.Visible() || sh.bars.bottom.MinSize().Height < pageBarHeight {
		t.Fatal("page bar did not come back")
	}
	sh.reg.Run("view.fullscreen")
	if c, _ := sh.reg.Get("view.fullscreen"); !c.Checked {
		t.Error("view.fullscreen not checked")
	}
	sh.reg.Run("view.fullscreen")

	// Fenster
	_ = sh.OpenFile(saveSample(t, "Zweites", 1920, 1080, 25, 50))
	sh.reg.Run("window.nextProject")
	if sh.Active() != s {
		t.Fatal("window.nextProject did not wrap to the first tab")
	}
	sh.reg.Run("window.prevProject")
	if sh.Active().Project.Name != "Zweites" {
		t.Fatal("window.prevProject")
	}
	items := sh.reg.DynamicItems(command.DynProjects)
	if len(items) != 2 || items[1].Checked != 2 || items[0].Checked != 1 {
		t.Fatalf("project items %+v", items)
	}
	items[0].Run()
	if sh.Active() != s {
		t.Fatal("project item did not switch")
	}
	if rec := sh.reg.DynamicItems(command.DynRecent); len(rec) == 0 || rec[0].Run == nil || !strings.HasPrefix(rec[0].Label, "Zweites") {
		t.Fatalf("recent items %+v", rec)
	}

	// Hilfe
	var opened string
	sh.openPath = func(dir string) error { opened = dir; return nil }
	sh.reg.Run("help.configFolder")
	if want, _ := settings.Dir(); opened != want {
		t.Errorf("config folder opened %q, want %q", opened, want)
	}
	rows := sh.diagnostics()
	joined := ""
	for _, r := range rows {
		joined += r[0] + "=" + r[1] + "\n"
	}
	for _, k := range []string{"Go=", "Fyne=", "ffmpeg=", "ffprobe=", "Tonausgabe=", "Einstellungsordner=", "Cache-Grenzen=", "Geöffnete Projekte=2"} {
		if !strings.Contains(joined, k) {
			t.Errorf("diagnostics lack %q:\n%s", k, joined)
		}
	}
	sh.reg.Run("help.diagnostics")
	if w.Canvas().Overlays().Top() == nil {
		t.Fatal("diagnostics dialog not shown")
	}
	w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())

	// Alle schließen, Beenden
	sh.reg.Run("file.closeAll")
	if len(sh.projects) != 0 || !sh.showManager {
		t.Fatalf("closeAll left %d tabs", len(sh.projects))
	}
	closed := false
	sh.closeWindow = func() { closed = true }
	sh.reg.Run("file.quit")
	if !closed {
		t.Error("file.quit did not close the window")
	}
}

func TestCloseAllStopsAtCancel(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Eins", 1920, 1080, 25, 10), saveSample(t, "Zwei", 1920, 1080, 25, 10)})
	sh.activateIndex(0)
	edit(t, sh)
	sh.reg.Run("file.closeAll")
	if sh.pendingAnswer == nil {
		t.Fatal("no save prompt for the dirty project")
	}
	sh.pendingAnswer(choiceCancel)
	if len(sh.projects) != 2 {
		t.Fatalf("cancel closed tabs anyway: %d", len(sh.projects))
	}
	sh.reg.Run("file.closeAll")
	sh.pendingAnswer(choiceDiscard)
	if len(sh.projects) != 0 {
		t.Fatalf("discard left %d tabs", len(sh.projects))
	}
}

func TestShortcutsFollowKeymap(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Tasten", 1920, 1080, 25, 100)})
	s := sh.Active()
	saveKey := &desktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierControl}
	edit(t, sh)
	typeShortcut(t, sh, saveKey)
	if s.Dirty {
		t.Fatal("Strg+S did not save")
	}

	// A new keymap from the settings re-registers the window shortcuts.
	if err := settings.Update(func(st *settings.Settings) { st.Keymap = map[string]string{"file.save": "Ctrl+Alt+Y"} }); err != nil {
		t.Fatal(err)
	}
	if got := sh.reg.Shortcut("file.save"); got != "Ctrl+Alt+Y" {
		t.Fatalf("registry keymap %q", got)
	}
	if _, ok := sh.shortcuts[saveKey.ShortcutName()]; ok {
		t.Error("old shortcut Strg+S still registered")
	}
	edit(t, sh)
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyY, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	if s.Dirty {
		t.Fatal("new shortcut did not save")
	}
	// Changes made in the registry directly (keymap editor) register as well.
	sh.reg.SetShortcut("play.loop", "Ctrl+Alt+J")
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyJ, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	if !s.Loop {
		t.Fatal("re-assigned loop shortcut")
	}

	// Driver shortcut types map to their key combinations.
	edit(t, sh)
	typeShortcut(t, sh, &fyne.ShortcutUndo{})
	if !s.CanRedo() {
		t.Fatal("Strg+Z (ShortcutUndo) did not undo")
	}
	typeShortcut(t, sh, &fyne.ShortcutRedo{})
	if s.CanRedo() {
		t.Fatal("Strg+Y (ShortcutRedo) did not redo")
	}

	// No shortcut acts behind a dialog.
	sh.reg.Run("help.about")
	edit(t, sh)
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyY, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	if !s.Dirty {
		t.Fatal("shortcut saved behind a dialog")
	}
	w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())
}

func TestMenuKeys(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Menütasten", 1920, 1080, 25, 100)})
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyF10})
	sh.keyDown(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	sh.typedKey(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	sh.keyUp(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	// Alt then another key: no menu.
	sh.keyDown(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	sh.keyDown(&fyne.KeyEvent{Name: fyne.KeyTab})
	sh.keyUp(&fyne.KeyEvent{Name: desktop.KeyAltLeft})
	// Alt+H opens Hilfe (no command uses it); Alt+X runs its command instead.
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyH, Modifier: fyne.KeyModifierAlt})
	s := sh.Active()
	s.SetIn(5)
	s.SetOut(20)
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyX, Modifier: fyne.KeyModifierAlt})
	hilfe := slices.IndexFunc(command.MenuBar, func(m command.Menu) bool { return m.Title == "Hilfe" })
	if want := []int{0, 0, hilfe}; !slices.Equal(sh.menuOpened, want) {
		t.Fatalf("menus opened %v, want %v", sh.menuOpened, want)
	}
	if s.HasRange() {
		t.Error("Alt+X did not clear In/Out")
	}
	m := menuMnemonics(command.MenuBar)
	if len(m) != len(command.MenuBar) {
		t.Errorf("menu letters %v", m)
	}
}

func TestStartupModes(t *testing.T) {
	t.Run("last", func(t *testing.T) {
		sh, w, _ := newTestShell(t)
		a := saveSample(t, "Letztes A", 1920, 1080, 25, 10)
		b := saveSample(t, "Letztes B", 1920, 1080, 25, 10)
		gone := filepath.Join(t.TempDir(), "Weg.fsproj")
		_ = settings.Update(func(s *settings.Settings) {
			s.General.Startup = "last"
			s.General.LastOpen = []string{a, gone, b}
		})
		sh.Start(nil)
		if len(sh.projects) != 2 || sh.showManager {
			t.Fatalf("reopened %d projects", len(sh.projects))
		}
		if w.Canvas().Overlays().Top() == nil {
			t.Error("missing project not reported")
		}
	})
	t.Run("new", func(t *testing.T) {
		sh, _, _ := newTestShell(t)
		_ = settings.Update(func(s *settings.Settings) {
			s.General.Startup = "new"
			s.General.DefaultTemplate = "720p25"
		})
		sh.Start(nil)
		if len(sh.projects) != 1 || sh.showManager {
			t.Fatalf("new: %d projects", len(sh.projects))
		}
		if st := sh.Active().Project.Settings; st.Width != 1280 || st.Height != 720 || st.FPS != 25 {
			t.Errorf("template %+v", st)
		}
	})
	t.Run("manager", func(t *testing.T) {
		sh, _, _ := newTestShell(t)
		_ = settings.Update(func(s *settings.Settings) { s.General.LastOpen = []string{saveSample(t, "X", 1920, 1080, 25, 10)} })
		sh.Start(nil)
		if len(sh.projects) != 0 || !sh.showManager {
			t.Fatal("manager mode opened projects")
		}
	})
	t.Run("files win", func(t *testing.T) {
		sh, _, _ := newTestShell(t)
		_ = settings.Update(func(s *settings.Settings) { s.General.Startup = "new" })
		sh.Start([]string{saveSample(t, "Datei", 1920, 1080, 25, 10)})
		if len(sh.projects) != 1 || sh.Active().Project.Name != "Datei" {
			t.Fatal("files on the command line must replace the startup mode")
		}
	})
	for id, want := range map[string]string{"1080p25": "1080p HD · 25 fps", "2160p25": "4K UHD · 25 fps",
		"1080x1920@30": "Hochkant 1080×1920 · 30 fps (Social)", "unsinn": Templates[0].Label} {
		if got := templateByID(id); got.Label != want {
			t.Errorf("template %q = %q", id, got.Label)
		}
	}
}

func TestLastOpenSavedOnQuit(t *testing.T) {
	sh, _, _ := newTestShell(t)
	a := saveSample(t, "Quit A", 1920, 1080, 25, 10)
	b := saveSample(t, "Quit B", 1920, 1080, 25, 10)
	sh.Start([]string{a, b})
	sh.newProject("Ohne Datei", Templates[0]) // clean, no file: not remembered
	sh.Quit()
	got := settings.Get().General.LastOpen
	if len(got) != 2 || !samePath(got[0], a) || !samePath(got[1], b) {
		t.Fatalf("LastOpen %v", got)
	}
}

func TestWorkspaceHost(t *testing.T) {
	sh, _, _ := newTestShell(t)
	_, made := useCmdPages(sh)
	changes := 0
	sh.OnActiveChange(func() { changes++ })
	if sh.ActiveContext() != nil || sh.ActivePage() != "" {
		t.Fatal("host reports a project before any is open")
	}
	sh.Start([]string{saveSample(t, "Host", 1920, 1080, 25, 10)})
	if changes != 1 || sh.ActiveContext() == nil || sh.ActiveContext().Commands != sh.Commands() {
		t.Fatalf("active change %d, context %v", changes, sh.ActiveContext())
	}
	if sh.ActivePage() != page.Edit {
		t.Fatalf("active page %q", sh.ActivePage())
	}
	sh.ShowPage(page.Color)
	if sh.ActivePage() != page.Color {
		t.Fatal("ShowPage")
	}
	made[page.Edit][0].layout = wslayout.Page{Splits: map[string]float64{"timeline": 0.4}}
	ls := sh.PageLayouts()
	if ls[page.Edit].Splits["timeline"] != 0.4 {
		t.Fatalf("page layouts %+v", ls)
	}
	want := map[string]wslayout.Page{
		page.Edit:  {Splits: map[string]float64{"timeline": 0.3}},
		page.Color: {Hidden: map[string]bool{"scopes": true}},
	}
	sh.ApplyPageLayouts(want)
	if len(made[page.Edit][0].applied) != 1 || made[page.Edit][0].applied[0].Splits["timeline"] != 0.3 {
		t.Fatalf("edit page got %+v", made[page.Edit][0].applied)
	}
	// A project opened later starts with the layout.
	_ = sh.OpenFile(saveSample(t, "Host 2", 1920, 1080, 25, 10))
	if changes != 2 {
		t.Fatalf("second tab: %d changes", changes)
	}
	if n := len(made[page.Edit]); n != 2 || len(made[page.Edit][1].applied) != 1 {
		t.Fatalf("new edit page layouts: %+v", made[page.Edit])
	}
	sh.closeNow(sh.active)
	sh.closeNow(sh.active)
	if changes != 4 || sh.ActiveContext() != nil {
		t.Fatalf("after closing all: %d changes", changes)
	}
}

func TestSettingsApplyLive(t *testing.T) {
	sh, _, _ := newTestShell(t)
	// Autosave interval
	_ = settings.Update(func(s *settings.Settings) { s.General.AutosaveSeconds = 30 })
	select {
	case d := <-sh.autosaveSet:
		if d != 30*time.Second {
			t.Errorf("autosave interval %v", d)
		}
	default:
		t.Error("autosave interval not passed on")
	}
	if autosaveInterval(settings.Settings{}) != 0 {
		t.Error("0 s must turn autosave off")
	}
	stop := sh.StartAutosave(0)
	_ = settings.Update(func(s *settings.Settings) { s.General.AutosaveSeconds = 0 })
	stop()

	// Projects folder
	dir := filepath.Join(t.TempDir(), "Meine Projekte")
	_ = settings.Update(func(s *settings.Settings) { s.General.ProjectsDir = dir })
	if got, _ := store.ProjectsDir(); got != dir {
		t.Fatalf("projects dir %q", got)
	}
	sh.newProject("Bibliothek", Templates[0])
	if err := sh.Save(sh.active); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(sh.Active().Path) != dir {
		t.Fatalf("saved to %s", sh.Active().Path)
	}

	// Export notifications
	sh.notify("Export fertig", "a.mp4", false)
	if !sh.toast.card.Visible() {
		t.Fatal("toast hidden with notifications on")
	}
	sh.toast.hide()
	_ = settings.Update(func(s *settings.Settings) { s.Export.Notify = false })
	sh.notify("Export fertig", "a.mp4", false)
	if sh.toast.card.Visible() {
		t.Fatal("toast shown with notifications off")
	}
	sh.notify("Export fehlgeschlagen", "a.mp4", true)
	if !sh.toast.card.Visible() {
		t.Fatal("failures must always show")
	}
	sh.toast.hide()

	// Without ConfirmClose a project with a file is saved on close without asking.
	_ = settings.Update(func(s *settings.Settings) { s.General.ConfirmClose = false })
	edit(t, sh)
	path := sh.Active().Path
	sh.reg.Run("file.close")
	if sh.pendingAnswer != nil || len(sh.projects) != 0 {
		t.Fatal("close asked or kept the tab")
	}
	if d, err := store.Describe(path); err != nil || d.Clips != 1 {
		t.Fatalf("unsaved edit lost: %+v %v", d, err)
	}
	_ = os.Remove(path)
}

// TestScreenshotRound3 renders the window with the custom menu bar, open menus, the help
// dialog built from the registry and the diagnostics to /tmp/fs-r3-shell*.png.
func TestScreenshotRound3(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render screenshots")
	}
	sh, w, _ := newTestShell(t)
	for i := range sh.pageDefs { // the real pages
		sh.pageDefs[i].New = defaultPages()[i].New
	}
	t.Cleanup(func() { sh.releaseAll(); sh.cache.Close() })
	w.Resize(fyne.NewSize(1440, 900))
	a := saveSample(t, "Imagefilm Stadtwerke", 1920, 1080, 25, 25*94)
	b := saveSample(t, "Hochzeit Lena & Tom", 3840, 2160, 25, 25*120)
	sh.Start([]string{a, b})
	edit(t, sh)
	sh.activateIndex(0)
	capture(t, w, "/tmp/fs-r3-shell.png")
	sh.menuBar.Open(0)
	capture(t, w, "/tmp/fs-r3-shell-menu.png")
	for w.Canvas().Overlays().Top() != nil {
		w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.PageBar = false })
	capture(t, w, "/tmp/fs-r3-shell-nopagebar.png")
	_ = settings.Update(func(s *settings.Settings) { s.Display.PageBar = true })
	sh.showShortcuts()
	capture(t, w, "/tmp/fs-r3-shell-help.png")
	w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())
	sh.showDiagnostics()
	capture(t, w, "/tmp/fs-r3-shell-diagnostics.png")
}
