package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fakePage records calls from the shell.
type fakePage struct {
	id      string
	keys    []fyne.KeyName
	handle  bool
	active  bool
	closed  bool
	dropped []fyne.URI
}

func (f *fakePage) Content() fyne.CanvasObject { return widget.NewLabel(f.id) }
func (f *fakePage) Activate()                  { f.active = true }
func (f *fakePage) Deactivate()                { f.active = false }
func (f *fakePage) Close()                     { f.closed = true }
func (f *fakePage) TypedKey(k *fyne.KeyEvent) bool {
	f.keys = append(f.keys, k.Name)
	return f.handle && k.Name != fyne.KeySpace
}

type dropPage struct{ fakePage }

func (d *dropPage) Dropped(u []fyne.URI) { d.dropped = append(d.dropped, u...) }

func newTestShell(t *testing.T) (*Shell, fyne.Window, map[string][]*fakePage) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	resetSettings(t)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	w := test.NewTempWindow(t, nil)
	w.Resize(fyne.NewSize(1440, 860))
	sh := New(a, w)
	t.Cleanup(sh.dispose)
	sh.closeWindow = func() {} // the test cleanup closes the window
	sh.openPath = func(string) error { return nil }
	// No playback clock goroutine: the test driver runs fyne.Do on the caller's goroutine.
	sh.drivePlayback = func(s *session.Session) func() { return func() { s.SetPlaying(false) } }
	made := map[string][]*fakePage{}
	for i := range sh.pageDefs {
		id := sh.pageDefs[i].ID
		sh.pageDefs[i].New = func(*page.Context) page.Page {
			if id == page.Media {
				d := &dropPage{fakePage{id: id, handle: true}}
				made[id] = append(made[id], &d.fakePage)
				return d
			}
			f := &fakePage{id: id, handle: true}
			made[id] = append(made[id], f)
			return f
		}
	}
	return sh, w, made
}

// saveSample writes a project with one clip into the library and returns its path.
func saveSample(t *testing.T, name string, w, h, fps, length int) string {
	t.Helper()
	p := model.NewProject(name)
	p.Settings.Width, p.Settings.Height, p.Settings.FPS = w, h, model.IntRate(fps)
	if length > 0 {
		if err := p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, length)); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := store.ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	path := store.SuggestPath(dir, name)
	if err := store.Save(p, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func edit(t *testing.T, sh *Shell) {
	t.Helper()
	s := sh.Active()
	if err := s.Edit("Clip einfügen", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Rot", p.Duration(), 50))
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStartShowsManagerWithoutFiles(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.Start(nil)
	if !sh.showManager || w.Title() != "Frame Studio — Projektmanager" {
		t.Fatalf("manager not shown, title %q", w.Title())
	}
	sh.Start([]string{filepath.Join(t.TempDir(), "fehlt.fsproj")})
	if len(sh.projects) != 0 || !sh.showManager {
		t.Error("missing file must not open a tab")
	}
}

func TestOpenTwoProjectsAndSwitch(t *testing.T) {
	sh, w, made := newTestShell(t)
	a := saveSample(t, "Alpha", 1920, 1080, 25, 100)
	b := saveSample(t, "Beta", 1280, 720, 25, 50)
	sh.Start([]string{a, b})
	if len(sh.projects) != 2 || sh.Active().Project.Name != "Beta" || sh.showManager {
		t.Fatalf("tabs %d, active %v", len(sh.projects), sh.Active())
	}
	if w.Title() != "Frame Studio — Beta" {
		t.Errorf("title %q", w.Title())
	}
	if len(made[page.Edit]) != 2 || made[page.Edit][0].active || !made[page.Edit][1].active {
		t.Fatalf("edit page of Beta not active: %v", made[page.Edit])
	}

	sh.Active().SetLoop(true)
	sh.activateIndex(0)
	if sh.Active().Project.Name != "Alpha" || w.Title() != "Frame Studio — Alpha" {
		t.Fatalf("switch failed: %q", w.Title())
	}
	if !made[page.Edit][0].active || made[page.Edit][1].active {
		t.Error("switching tabs must deactivate the old page and activate the new one")
	}

	// Opening an open file again focuses its tab.
	if err := sh.OpenFile(b); err != nil {
		t.Fatal(err)
	}
	if len(sh.projects) != 2 || sh.Active().Project.Name != "Beta" {
		t.Error("reopen should focus the existing tab")
	}

	// Page switching is lazy and calls Deactivate/Activate.
	sh.showPageID(page.Color)
	if len(made[page.Color]) != 1 || !made[page.Color][0].active || made[page.Edit][0].active {
		t.Error("page switch failed")
	}
	sh.ShowManager()
	if made[page.Color][0].active || !sh.showManager {
		t.Error("manager must deactivate the visible page")
	}
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if !made[page.Color][0].active || sh.showManager {
		t.Error("leaving the manager must reactivate the page")
	}
	if got := store.Recent(); len(got) != 2 || got[0] != b {
		t.Errorf("recent = %v", got)
	}
}

func TestDirtyTitleAndStatus(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Gamma", 1920, 1080, 25, 0)})
	edit(t, sh)
	if !strings.HasSuffix(w.Title(), "•") {
		t.Errorf("title %q lacks dirty dot", w.Title())
	}
	if sh.bars.status.Text != "Ungespeichert" {
		t.Errorf("status %q", sh.bars.status.Text)
	}
	if c, _ := sh.reg.Get("edit.undo"); c.MenuLabel != "Rückgängig: Clip einfügen" || c.Label != "Rückgängig" || !c.Enabled {
		t.Errorf("undo command %q enabled %v", c.Label, c.Enabled)
	}
	sh.reg.Run("edit.undo")
	if c, _ := sh.reg.Get("edit.redo"); sh.Active().Project.Duration() != 0 || !c.Enabled || c.MenuLabel != "Wiederholen: Clip einfügen" || c.Label != "Wiederholen" {
		t.Errorf("undo via command failed: redo %q", c.MenuLabel)
	}
}

func TestSaveWithoutPathWritesIntoLibrary(t *testing.T) {
	sh, w, _ := newTestShell(t)
	sh.newProject("Mein Film", Templates[5])
	s := sh.Active()
	if s.Project.Settings.Width != 1080 || s.Project.Settings.Height != 1920 || !s.Project.Settings.FPS.Equal(model.Rate30) {
		t.Fatalf("template not applied: %+v", s.Project.Settings)
	}
	if s.SelectedTrack != s.Project.Tracks[0].ID {
		t.Error("V1 not selected")
	}
	edit(t, sh)
	sh.saveActive()
	dir, _ := store.ProjectsDir()
	want := filepath.Join(dir, "Mein Film.fsproj")
	if s.Path != want || s.Dirty {
		t.Fatalf("path %q dirty %v", s.Path, s.Dirty)
	}
	if p, err := store.Load(want); err != nil || p.Duration() != 50 {
		t.Fatalf("saved file: %v", err)
	}
	if strings.Contains(w.Title(), "•") || !strings.HasPrefix(sh.bars.status.Text, "Gespeichert ") {
		t.Errorf("title %q status %q", w.Title(), sh.bars.status.Text)
	}
	if r := store.Recent(); len(r) == 0 || r[0] != want {
		t.Errorf("recent %v", r)
	}
	// A second unsaved project with the same name gets its own file.
	sh.newProject("Mein Film", Templates[0])
	sh.saveActive()
	if sh.Active().Path != filepath.Join(dir, "Mein Film 2.fsproj") {
		t.Errorf("dedup path %q", sh.Active().Path)
	}
}

func TestResolveClose(t *testing.T) {
	cases := []struct {
		dirty       bool
		c           choice
		save, close bool
	}{
		{false, choiceCancel, false, true},
		{true, choiceSave, true, true},
		{true, choiceDiscard, false, true},
		{true, choiceCancel, false, false},
	}
	for _, c := range cases {
		save, cl := resolveClose(c.dirty, c.c)
		if save != c.save || cl != c.close {
			t.Errorf("%+v: got %v %v", c, save, cl)
		}
	}
}

func TestCloseDirtyTabPrompts(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Delta", 1920, 1080, 25, 10), saveSample(t, "Epsilon", 1920, 1080, 25, 10)})
	delta := sh.projects[0]
	sh.activate(delta)
	edit(t, sh)

	sh.closeProject(delta)
	if len(sh.projects) != 2 || sh.pendingAnswer == nil {
		t.Fatal("dirty close must prompt")
	}
	sh.pendingAnswer(choiceCancel)
	if len(sh.projects) != 2 || !delta.sess.Dirty {
		t.Fatal("cancel must keep the tab")
	}
	sh.closeProject(delta)
	sh.pendingAnswer(choiceSave)
	if len(sh.projects) != 1 || sh.Active().Project.Name != "Epsilon" {
		t.Fatal("save+close failed")
	}
	if p, _ := store.Load(delta.sess.Path); p.Duration() != 60 {
		t.Error("changes not saved before close")
	}
	if !made[page.Edit][0].closed {
		t.Error("pages of a closed tab must be closed")
	}

	// Clean tabs close without asking; the last one leads to the manager.
	sh.closeActive()
	if len(sh.projects) != 0 || !sh.showManager || sh.pendingAnswer != nil {
		t.Error("clean close should not prompt and should show the manager")
	}
}

func TestQuitAsksOnceForAllDirty(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Eins", 1920, 1080, 25, 10), saveSample(t, "Zwei", 1920, 1080, 25, 10)})
	for _, p := range sh.projects {
		sh.activate(p)
		edit(t, sh)
	}
	sh.Quit()
	if sh.pendingAnswer == nil {
		t.Fatal("quit with dirty projects must prompt")
	}
	sh.pendingAnswer(choiceSave)
	if len(sh.projects) != 0 {
		t.Error("projects not released")
	}
	for _, name := range []string{"Eins", "Zwei"} {
		dir, _ := store.ProjectsDir()
		if p, err := store.Load(filepath.Join(dir, name+".fsproj")); err != nil || p.Duration() != 60 {
			t.Errorf("%s not saved: %v", name, err)
		}
	}
}

func TestKeyRoutingToActivePage(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Keys", 1920, 1080, 25, 100)})
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyJ})
	edit0 := made[page.Edit][0]
	if len(edit0.keys) != 1 || edit0.keys[0] != fyne.KeyJ {
		t.Fatalf("keys %v", edit0.keys)
	}
	// Unhandled Space falls back to play/pause.
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if !sh.Active().Playing {
		t.Error("space should start playback")
	}
	sh.reg.Run("play.toggle")

	sh.modifiers = func() fyne.KeyModifier { return fyne.KeyModifierShift }
	sh.typedKey(&fyne.KeyEvent{Name: fyne.Key4})
	sh.modifiers = func() fyne.KeyModifier { return 0 }
	if sh.active.current != page.Audio || len(edit0.keys) != 2 {
		// Space reached the edit page before falling back.
		t.Fatalf("Shift+4 should open Ton, got %s", sh.active.current)
	}
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyK})
	if a := made[page.Audio][0]; len(a.keys) != 1 {
		t.Error("keys not routed to the new active page")
	}
	// Menu commands act on the active project.
	sh.reg.Run("play.loop")
	if c, _ := sh.reg.Get("play.loop"); !sh.Active().Loop || !c.Checked {
		t.Error("loop command failed")
	}
}

func TestDropRoutesToMediaPage(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Drop", 1920, 1080, 25, 10)})
	other := saveSample(t, "Anderes", 1920, 1080, 25, 10)
	clip := storage.NewFileURI("/tmp/clip.mp4")
	sh.dropped([]fyne.URI{clip, storage.NewFileURI(other)})
	if len(sh.projects) != 2 {
		t.Fatal("dropped project file not opened")
	}
	// The media file went to the media page of the then-active project.
	m := made[page.Media]
	if len(m) != 1 || len(m[0].dropped) != 1 || m[0].dropped[0] != clip {
		t.Fatalf("media drop: %+v", m)
	}
}

func TestAutosave(t *testing.T) {
	sh, _, _ := newTestShell(t)
	sh.Start([]string{saveSample(t, "Auto", 1920, 1080, 25, 10)})
	sh.newProject("Ohne Datei", Templates[0])
	edit(t, sh)
	sh.activateIndex(0)
	edit(t, sh)

	jobs := sh.autosaveJobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs %d, want only the project with a file", len(jobs))
	}
	runAutosave(jobs)
	sh.finishAutosave(jobs)
	s := sh.Active()
	if s.Dirty || !strings.HasPrefix(sh.bars.status.Text, "Automatisch gespeichert") {
		t.Fatalf("dirty %v status %q", s.Dirty, sh.bars.status.Text)
	}

	// An edit during the background write keeps the project dirty.
	edit(t, sh)
	jobs = sh.autosaveJobs()
	runAutosave(jobs)
	edit(t, sh)
	sh.finishAutosave(jobs)
	if !s.Dirty {
		t.Error("newer edit must stay unsaved")
	}
}

func TestManagerActions(t *testing.T) {
	sh, _, _ := newTestShell(t)
	a := saveSample(t, "Archiv", 1920, 1080, 25, 10)
	sh.Start(nil)
	if len(sh.mgr.cards) != 1 || sh.mgr.selected != a {
		t.Fatalf("cards %d selected %q", len(sh.mgr.cards), sh.mgr.selected)
	}
	dup, err := sh.mgr.duplicate(a)
	if err != nil || filepath.Base(dup) != "Archiv Kopie.fsproj" {
		t.Fatalf("duplicate %q %v", dup, err)
	}
	if err := sh.mgr.rename(dup, "Zweitfassung"); err != nil {
		t.Fatal(err)
	}
	sh.mgr.reload()
	var names []string
	for _, c := range sh.mgr.cards {
		names = append(names, c.d.Name)
	}
	if len(names) != 2 || !strings.Contains(strings.Join(names, ","), "Zweitfassung") {
		t.Errorf("cards %v", names)
	}
	if _, err := os.Stat(dup); !os.IsNotExist(err) {
		t.Error("library file should be renamed")
	}
	sh.mgr.typedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(sh.projects) != 1 || sh.showManager {
		t.Error("enter should open the selected project")
	}
	sh.ShowManager()
	if !sh.mgr.cards[slicesIndex(sh.mgr.cards, sh.Active().Path)].open {
		t.Error("open project not marked")
	}
}

func slicesIndex(cs []*projectCard, path string) int {
	for i, c := range cs {
		if c.d.Path == path {
			return i
		}
	}
	return -1
}

func TestStatusText(t *testing.T) {
	sh, _, _ := newTestShell(t)
	p := sh.newProject("X", Templates[0])
	now := time.Date(2026, 10, 22, 14, 32, 0, 0, time.Local)
	if got := statusText(p, now); got != "Noch nicht gespeichert" {
		t.Error(got)
	}
	p.savedAt = now
	p.sess.Path = "/x.fsproj"
	if got := statusText(p, now); got != "Gespeichert 14:32" {
		t.Error(got)
	}
	p.savedAt = now.AddDate(0, 0, -1)
	if got := statusText(p, now); got != "Gespeichert 21.10.2026 14:32" {
		t.Error(got)
	}
}

func capture(t *testing.T, w fyne.Window, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render screenshots")
	}
	sh, w, _ := newTestShell(t)
	for i := range sh.pageDefs { // real stub pages
		sh.pageDefs[i].New = defaultPages()[i].New
	}
	w.Resize(fyne.NewSize(1440, 860))
	specs := []struct {
		name          string
		w, h, fps, ln int
	}{
		{"Imagefilm Stadtwerke", 1920, 1080, 25, 25 * 94},
		{"Hochzeit Lena & Tom", 3840, 2160, 25, 25 * 1260},
		{"Reel Herbstkollektion", 1080, 1920, 30, 30 * 28},
		{"Podcast Folge 12", 1920, 1080, 30, 30 * 2710},
		{"Produktvideo Quadrat", 1080, 1080, 30, 30 * 45},
		{"Interview Rohschnitt", 1280, 720, 25, 25 * 610},
	}
	var paths []string
	for i, s := range specs {
		p := saveSample(t, s.name, s.w, s.h, s.fps, s.ln)
		mt := time.Date(2026, 10, 22, 14, 32, 0, 0, time.Local).Add(-time.Duration(i*i) * 7 * time.Hour)
		_ = os.Chtimes(p, mt, mt)
		paths = append(paths, p)
	}
	store.AddRecent(paths[2])
	store.AddRecent(paths[0])
	q := kit.UseUIQueue(t)
	sh.thumbnails = true
	sh.Start(nil)
	q.Until(func() bool { return len(sh.mgr.thumbJobs()) == 0 }, 20*time.Second)
	sh.thumbnails = false
	w.Resize(fyne.NewSize(1440, 900))
	capture(t, w, "/tmp/fs-r2-manager.png")
	w.Resize(fyne.NewSize(1440, 860))
	if os.Getenv("FS_SCREENSHOT") == "all" {
		sh.newProjectDialog()
		capture(t, w, "/tmp/fs-newproject.png")
		w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top())
	}

	_ = sh.OpenFile(paths[0])
	_ = sh.OpenFile(paths[1])
	edit(t, sh)
	sh.activateIndex(0)
	sh.toast.show("Export fertig", "„Imagefilm Stadtwerke.mp4“ wurde in 1:34 gerendert (48,3 MB).", false)
	capture(t, w, "/tmp/fs-shell.png")
	sh.toast.hide()
	w.Resize(fyne.NewSize(1440, 900))
	sh.showShortcuts()
	capture(t, w, "/tmp/fs-r2-help.png")
}

// busyPage is a deliver page stand-in with an export running.
type busyPage struct {
	fakePage
	busy   string
	waited bool
}

func (b *busyPage) Busy() string { return b.busy }
func (b *busyPage) Wait(time.Duration) bool {
	b.waited = true
	return true
}

func TestAutosaveCannotOverwriteNewerSave(t *testing.T) {
	sh, _, _ := newTestShell(t)
	path := saveSample(t, "Wettlauf", 1920, 1080, 25, 10)
	sh.Start([]string{path})
	edit(t, sh) // 2 clips
	jobs := sh.autosaveJobs()
	edit(t, sh) // 3 clips, then Ctrl+S before the autosave writes
	sh.saveActive()
	runAutosave(jobs) // the older snapshot arrives late
	sh.finishAutosave(jobs)
	p, err := store.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(p.Tracks[0].Clips); n != 3 {
		t.Fatalf("file holds %d clips, want the 3 of the explicit save", n)
	}
	if sh.Active().Dirty {
		t.Fatal("explicit save is clean")
	}
}

func TestDiscardUndoesAutosaveDuringPrompt(t *testing.T) {
	sh, _, _ := newTestShell(t)
	path := saveSample(t, "Verwerfen", 1920, 1080, 25, 10)
	sh.Start([]string{path})
	p := sh.active
	edit(t, sh)
	sh.closeProject(p)
	if sh.pendingAnswer == nil {
		t.Fatal("dirty close must prompt")
	}
	jobs := sh.autosaveJobs() // the autosave tick fires while the prompt is open
	runAutosave(jobs)
	sh.finishAutosave(jobs)
	sh.pendingAnswer(choiceDiscard)
	if len(sh.projects) != 0 {
		t.Fatal("tab not closed")
	}
	if q, _ := store.Load(path); len(q.Tracks[0].Clips) != 1 {
		t.Fatalf("discarded edit is on disk: %d clips", len(q.Tracks[0].Clips))
	}
}

func TestCloseAndQuitAskWhileExporting(t *testing.T) {
	sh, _, _ := newTestShell(t)
	bp := &busyPage{fakePage: fakePage{id: page.Deliver}, busy: "Export „Film.mp4“ läuft (17 %)"}
	for i := range sh.pageDefs {
		if sh.pageDefs[i].ID == page.Deliver {
			sh.pageDefs[i].New = func(*page.Context) page.Page { return bp }
		}
	}
	sh.Start([]string{saveSample(t, "Render", 1920, 1080, 25, 10)})
	sh.showPageID(page.Deliver)
	var asked string
	var confirm func()
	sh.confirmHook = func(msg string, ok func()) { asked, confirm = msg, ok }
	sh.closeActive()
	if len(sh.projects) != 1 || !strings.Contains(asked, "17 %") {
		t.Fatalf("closing during an export must ask first, got %q", asked)
	}
	asked = ""
	sh.Quit()
	if len(sh.projects) != 1 || !strings.Contains(asked, "beenden") {
		t.Fatalf("quitting during an export must ask first, got %q", asked)
	}
	confirm()
	if len(sh.projects) != 0 || !bp.closed || !bp.waited {
		t.Fatal("confirmed quit must close the page and wait for its cleanup")
	}
}

func TestRenameOpenProjectKeepsEditsUnsaved(t *testing.T) {
	sh, _, _ := newTestShell(t)
	path := saveSample(t, "Alt", 1920, 1080, 25, 10)
	sh.Start([]string{path})
	p := sh.active
	edit(t, sh)
	sh.ShowManager()
	if err := sh.mgr.rename(path, "Neu"); err != nil {
		t.Fatal(err)
	}
	if p.sess.Project.Name != "Neu" || !p.sess.Dirty {
		t.Fatalf("session name %q dirty %v", p.sess.Project.Name, p.sess.Dirty)
	}
	q, err := store.Load(p.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if q.Name != "Neu" || len(q.Tracks[0].Clips) != 1 {
		t.Fatalf("file has name %q and %d clips; unsaved edits must not be written", q.Name, len(q.Tracks[0].Clips))
	}
	if filepath.Base(p.sess.Path) != "Neu.fsproj" {
		t.Fatalf("library file not renamed: %s", p.sess.Path)
	}
}
