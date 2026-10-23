package shell

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	wslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
)

// On Farbe and Ton the registry gets the keys (with modifiers and the user's key map),
// not a hard-coded handler of the page: Umschalt+Links is a second back, Umschalt+
// Leertaste plays In to Out, and a remapped play.toggle frees the Space key.
func TestColorAndAudioKeysFollowRegistry(t *testing.T) {
	a, q := newR3App(t)
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Tasten", 1920, 1080, 25, 400)})
	s := sh.Active()
	for _, id := range []string{page.Color, page.Audio} {
		sh.showPageID(id)
		q.Settle(20*time.Millisecond, 2*time.Second)
		s.Seek(100)
		sh.modifiers = func() fyne.KeyModifier { return fyne.KeyModifierShift }
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyLeft})
		if s.Playhead != 75 {
			t.Fatalf("%s: Umschalt+Links went to %d, want 75 (one second back)", id, s.Playhead)
		}
		s.SetIn(10)
		s.SetOut(60)
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
		sh.modifiers = func() fyne.KeyModifier { return 0 }
		if !s.Playing || s.Playhead != 10 {
			t.Fatalf("%s: Umschalt+Leertaste did not play from In (playing %v at %d)", id, s.Playing, s.Playhead)
		}
		s.SetPlaying(false)
		s.ClearRange()
	}
	sh.showPageID(page.Color)
	sh.reg.SetShortcut("play.toggle", "P")
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if s.Playing {
		t.Fatal("Space still plays on Farbe after play.toggle moved to P")
	}
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyP})
	if !s.Playing {
		t.Fatal("P does not play on Farbe")
	}
	s.SetPlaying(false)
	q.Settle(20*time.Millisecond, 2*time.Second)
}

// Viewer fullscreen is not a dialog: Strg+Z and Strg+Alt+L keep working.
func TestShortcutsInViewerFullscreen(t *testing.T) {
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Vollbild", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	q.Settle(20*time.Millisecond, 2*time.Second)
	s := sh.Active()
	s.Seek(50)
	if !sh.reg.Run("clip.split") || clipCount(s.Project) != 2 {
		t.Fatal("split did not run")
	}
	if !sh.reg.Run("view.viewerFullscreen") || w.Canvas().Overlays().Top() == nil {
		t.Fatal("viewer fullscreen did not open")
	}
	typeShortcut(t, sh, &fyne.ShortcutUndo{})
	if clipCount(s.Project) != 1 {
		t.Fatalf("Strg+Z in viewer fullscreen: %d clips, want 1", clipCount(s.Project))
	}
	typeShortcut(t, sh, &desktop.CustomShortcut{KeyName: fyne.KeyL, Modifier: fyne.KeyModifierControl | fyne.KeyModifierAlt})
	if !s.Loop {
		t.Fatal("Strg+Alt+L in viewer fullscreen did not switch the loop on")
	}
	w.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("Esc did not leave the viewer fullscreen")
	}
	// A real dialog still blocks them.
	sh.reg.Run("help.about")
	typeShortcut(t, sh, &fyne.ShortcutRedo{})
	if clipCount(s.Project) != 1 {
		t.Fatal("Strg+Y passed a dialog")
	}
}

// Alt+Tab to another program: GLFW's synthetic Alt release after the focus loss must not
// put the keyboard on the menu bar.
func TestAltTabLeavesMenuBarAlone(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "AltTab", 1920, 1080, 25, 100)})
	alt := &fyne.KeyEvent{Name: desktop.KeyAltLeft}
	sh.keyDown(alt)
	sh.focusLost()
	sh.keyUp(alt)
	if sh.menuBar.IsOpen() {
		t.Fatal("the bar took the keyboard after Alt+Tab")
	}
	// Alt pressed and released alone still focuses the bar.
	sh.keyDown(alt)
	sh.keyUp(alt)
	if !sh.menuBar.IsOpen() {
		t.Fatal("Alt alone no longer focuses the bar")
	}
	sh.menuBar.Close()
}

// The Vollbild check mark and F11 follow the window, also when a layout made it fullscreen.
func TestFullscreenFollowsWindow(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "F11", 1920, 1080, 25, 100)})
	w.SetFullScreen(true) // as a restored layout does
	if c, _ := sh.reg.Get("view.fullscreen"); !c.Checked {
		t.Fatal("check mark off although the window is fullscreen")
	}
	sh.reg.Run("view.fullscreen")
	if w.FullScreen() {
		t.Fatal("first F11 did not leave fullscreen")
	}
	if c, _ := sh.reg.Get("view.fullscreen"); c.Checked {
		t.Fatal("check mark still on")
	}
}

// With a menu open, Strg+Z undoes and Alt+3 switches the page (the window's own key
// handling, with its aliases).
func TestShortcutsWhileMenuOpen(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Menü", 1920, 1080, 25, 100)})
	edit(t, sh)
	s := sh.Active()
	sh.menuBar.Open(1)
	sc, ok := w.Canvas().Focused().(fyne.Shortcutable)
	if !ok {
		t.Fatal("open menu holds no keyboard focus")
	}
	sc.TypedShortcut(&fyne.ShortcutUndo{})
	if s.Project.Duration() != 100 || sh.menuBar.IsOpen() {
		t.Fatalf("Strg+Z with an open menu: duration %d, open %v", s.Project.Duration(), sh.menuBar.IsOpen())
	}
	sh.menuBar.Open(1)
	w.Canvas().Focused().(fyne.Shortcutable).TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.Key3, Modifier: fyne.KeyModifierAlt})
	if sh.active.current != page.Color || sh.menuBar.IsOpen() {
		t.Fatalf("Alt+3 with an open menu: page %s", sh.active.current)
	}
}

// A lone In point can be cleared on Farbe too.
func TestClearLoneInOnColor(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "InOut", 1920, 1080, 25, 100)})
	sh.showPageID(page.Color)
	s := sh.Active()
	s.Seek(30)
	s.SetIn(30)
	if c, _ := sh.reg.Get("timeline.clearRange"); !c.Enabled {
		t.Fatal("In/Out löschen disabled with only an In point")
	}
	sh.reg.Run("timeline.clearRange")
	if s.In != 0 {
		t.Fatal("In point not cleared")
	}
}

// The undo step names only the menu entry; help, palette and keymap keep "Rückgängig".
func TestUndoLabelOnlyInMenu(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Undo", 1920, 1080, 25, 100)})
	edit(t, sh)
	for _, g := range shortcutGroups(sh.reg) {
		for _, k := range g.Keys {
			if strings.HasPrefix(k.Action, "Rückgängig:") {
				t.Fatalf("help lists %q", k.Action)
			}
		}
	}
	c, _ := sh.reg.Get("edit.undo")
	if c.Label != "Rückgängig" || c.MenuLabel != "Rückgängig: Clip einfügen" {
		t.Fatalf("label %q menu %q", c.Label, c.MenuLabel)
	}
}

// Quitting closes an open settings window, so the run loop can end.
func TestQuitClosesSettingsWindow(t *testing.T) {
	a, q := newR3App(t)
	sh, _ := buildShell(t, a, q)
	sh.Start(nil)
	sh.reg.Run("app.settings")
	if windowTitled(a, "Einstellungen") == nil {
		t.Fatal("settings window did not open")
	}
	sh.Quit()
	if w := windowTitled(a, "Einstellungen"); w != nil {
		t.Fatalf("still open after quit: %q", w.Title())
	}
}

// SIGTERM: the handler runs the normal shutdown on the UI thread; when the loop ended
// without it, ShutdownAfterLoop still writes the layout.
func TestSignalRunsShutdown(t *testing.T) {
	useFakeDisplay(t, false)
	a, q := newR3App(t)
	loadSettingsIn(t, t.TempDir())
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Signal", 1920, 1080, 25, 100)})
	c := catchSignals(sh)
	c.ch <- syscall.SIGTERM
	<-c.done // the handler ran
	c.stop()
	q.Settle(20*time.Millisecond, 2*time.Second)
	if !sh.shutdownDone {
		t.Fatal("SIGTERM did not shut down")
	}
	dir, _ := settings.Dir()
	if _, err := os.Stat(filepath.Join(dir, "layouts", "_current.json")); err != nil {
		t.Fatalf("layout not saved: %v", err)
	}
	if got := settings.Get().General.LastOpen; len(got) != 1 {
		t.Fatalf("last open %v", got)
	}

	// Loop ended without the handler: the workspace saves without touching windows.
	sh2, _ := buildShell(t, a, q)
	sh2.Start([]string{saveSample(t, "Spät", 1920, 1080, 25, 100)})
	_ = os.Remove(filepath.Join(dir, "layouts", "_current.json"))
	sh2.ws.Detach(page.PanelScopes, "DP-3", true)
	sh2.ws.ShutdownAfterLoop()
	l, err := wslayout.Load("", true)
	if err != nil || len(l.Windows) != 2 || l.Windows[1].Panel != page.PanelScopes {
		t.Fatalf("after-loop layout %+v (%v)", l.Windows, err)
	}
	if len(sh2.ws.Windows()) != 0 {
		t.Fatal("detached panel not released")
	}
}

// One page arrangement for all tabs: a new tab and the layout saved on quit keep what
// the user set in another tab.
func TestPageLayoutSharedAcrossTabs(t *testing.T) {
	a, q := newR3App(t)
	loadSettingsIn(t, t.TempDir())
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "A", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	q.Settle(20*time.Millisecond, 2*time.Second)
	sh.reg.Run("edit.hide.inspector")
	if !editLayout(t, sh).Hidden["inspector"] {
		t.Fatal("inspector not hidden in A")
	}
	if err := sh.OpenFile(saveSample(t, "B", 1920, 1080, 25, 100)); err != nil {
		t.Fatal(err)
	}
	sh.showPageID(page.Edit)
	q.Settle(20*time.Millisecond, 2*time.Second)
	if sh.active.sess.Project.Name != "B" || !editLayout(t, sh).Hidden["inspector"] {
		t.Fatal("tab B shows the inspector the user hid in A")
	}
	sh.reg.Run("edit.hide.inspector") // shown again in B
	sh.activateIndex(0)
	if editLayout(t, sh).Hidden["inspector"] {
		t.Fatal("tab A still hides the inspector shown again in B")
	}
	sh.reg.Run("edit.hide.inspector")
	sh.activateIndex(1)
	sh.Quit()
	l, err := wslayout.Load("", true)
	if err != nil || !l.Pages[page.Edit].Hidden["inspector"] {
		t.Fatalf("saved layout lost the hidden inspector: %+v (%v)", l.Pages[page.Edit], err)
	}
}

// "Schleife als Standard" switches the loop on for new tabs.
func TestLoopDefault(t *testing.T) {
	sh, _, _ := newTestShell(t)
	_ = settings.Update(func(s *settings.Settings) { s.Playback.LoopDefault = true })
	p := sh.newProject("Schleife", Templates[0])
	if !p.sess.Loop {
		t.Fatal("new project without loop")
	}
	if c, _ := sh.reg.Get("play.loop"); !c.Checked {
		t.Fatal("loop button off")
	}
}

// Without "Beim Schließen nachfragen", quitting saves projects with a file silently.
func TestQuitWithoutConfirmSaves(t *testing.T) {
	sh, w, _ := newTestShell(t)
	path := saveSample(t, "Still", 1920, 1080, 25, 100)
	sh.Start([]string{path})
	edit(t, sh)
	_ = settings.Update(func(s *settings.Settings) { s.General.ConfirmClose = false })
	sh.Quit()
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("quit asked although Beim Schließen nachfragen is off")
	}
	if !sh.shutdownDone || sh.Active() != nil {
		t.Fatal("did not quit")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "Rot") {
		t.Fatal("changes not saved on quit")
	}
}

// Diagnostics use the names and units of the rest of the UI.
func TestDiagnosticsWording(t *testing.T) {
	sh, _, _ := newTestShell(t)
	_ = settings.Update(func(s *settings.Settings) { s.Media.AudioCacheMB = 1536 })
	for _, r := range sh.diagnostics() {
		if r[0] == "Fenstersystem" && (r[1] == "wayland" || r[1] == "x11") {
			t.Fatalf("raw platform id %q", r[1])
		}
		if r[0] == "Cache-Grenzen" && !strings.Contains(r[1], "Ton 1,5 GB") {
			t.Fatalf("cache row %q", r[1])
		}
	}
}

// Clicking "Fett" in the inspector leaves the keyboard to the shell: Space plays and
// does not toggle bold again.
func TestInspectorCheckLeavesKeysToShell(t *testing.T) {
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Titel", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	q.Settle(20*time.Millisecond, 2*time.Second)
	if !sh.reg.Run("clip.addTitle") {
		t.Fatal("no title clip")
	}
	q.Settle(20*time.Millisecond, 2*time.Second)
	var bold *kit.Check
	for _, o := range test.LaidOutObjects(w.Content()) {
		if c, ok := o.(*kit.Check); ok && c.Text == "Fett" && c.Visible() {
			bold = c
		}
	}
	if bold == nil {
		t.Fatal("no Fett check box in the inspector")
	}
	s := sh.Active()
	undo := s.UndoLabel()
	test.Tap(bold)
	if s.UndoLabel() == undo {
		t.Fatal("Fett did not change the title")
	}
	if f := w.Canvas().Focused(); f != nil {
		t.Fatalf("%T kept the keyboard", f)
	}
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if !s.Playing {
		t.Fatal("Space did not play after clicking Fett")
	}
	s.SetPlaying(false)
	q.Settle(20*time.Millisecond, 2*time.Second)
}

// Strg+A in a detached timeline selects its clips while the main window shows Medien
// (the media page's own Strg+A must not run instead).
func TestDetachedTimelineChordsWhateverMainShows(t *testing.T) {
	useFakeDisplay(t, true)
	a, q := newR3App(t)
	sh, _ := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Gelöst", 1920, 1080, 25, 100)})
	sh.showPageID(page.Edit)
	sh.showPageID(page.Media)
	q.Settle(20*time.Millisecond, 2*time.Second)
	sh.ws.Detach(page.PanelTimeline, "DP-3", false)
	s := sh.Active()
	if !sh.ws.TypeShortcut(page.PanelTimeline, &fyne.ShortcutSelectAll{}) {
		t.Fatal("no Strg+A in the detached window")
	}
	if len(s.Selected) != 1 {
		t.Fatalf("Strg+A in the detached timeline selected %d clips, want 1", len(s.Selected))
	}
	n := clipCount(s.Project)
	sh.ws.TypeShortcut(page.PanelTimeline, &desktop.CustomShortcut{KeyName: fyne.KeyD, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	if clipCount(s.Project) != n+1 {
		t.Fatalf("Strg+Umschalt+D in the detached timeline: %d clips, want %d", clipCount(s.Project), n+1)
	}
}

// TestScreenshotDetachedMinimum renders detached scopes and mixer at their minimum size.
func TestScreenshotDetachedMinimum(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render screenshots")
	}
	useFakeDisplay(t, true)
	sh, _, q := newIntegrationShell(t)
	dir := t.TempDir()
	still, video, tone := demoMedia(t, dir)
	path := filepath.Join(dir, "demo.fsproj")
	if err := store.Save(demoProject(t, still, video, tone), path); err != nil {
		t.Fatal(err)
	}
	sh.Start([]string{path})
	q.Settle(50*time.Millisecond, 5*time.Second)
	for _, id := range []string{page.PanelScopes, page.PanelMixer} {
		sh.ws.Detach(id, "DP-3", false)
		w := sh.ws.Window(id)
		w.Resize(w.Content().MinSize())
		q.Settle(100*time.Millisecond, 5*time.Second)
		writePNG(t, w.Canvas().Capture(), "/tmp/fs-int-min-"+id+".png")
	}
}
