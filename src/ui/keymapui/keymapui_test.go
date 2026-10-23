package keymapui

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

type harness struct {
	t   *testing.T
	w   fyne.Window
	reg *command.Registry
	e   *editor
	dir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	reg := command.NewRegistry()
	for _, id := range []string{"file.new", "file.save", "clip.split", "edit.undo", "play.toggle"} {
		reg.Bind(command.Global, command.Binding{ID: id, Run: func() {}})
	}
	w := test.NewWindow(nil)
	w.Resize(fyne.NewSize(760, 560))
	t.Cleanup(w.Close)
	e := newEditor(w, reg)
	w.SetContent(e.content)
	return &harness{t: t, w: w, reg: reg, e: e, dir: dir}
}

func (h *harness) row(id string) *kmRow {
	h.t.Helper()
	r := h.e.rows[id]
	if r == nil {
		h.t.Fatalf("row %s not shown", id)
	}
	return r
}

// press records key with the given modifier keys held.
func (h *harness) press(key fyne.KeyName, mods ...fyne.KeyName) {
	h.t.Helper()
	rec := h.e.rec
	if rec == nil {
		h.t.Fatal("not recording")
	}
	for _, m := range mods {
		rec.KeyDown(&fyne.KeyEvent{Name: m})
	}
	rec.KeyDown(&fyne.KeyEvent{Name: key})
	// the shortcut event that follows must be swallowed by the still focused recorder
	if sc, ok := h.w.Canvas().Focused().(fyne.Shortcutable); !ok || sc != rec {
		h.t.Fatal("recorder lost focus before key release")
	}
	rec.KeyUp(&fyne.KeyEvent{Name: key})
	for _, m := range mods {
		rec.KeyUp(&fyne.KeyEvent{Name: m})
	}
}

func savedKeymap(t *testing.T) map[string]string {
	t.Helper()
	s, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	return s.Keymap
}

func TestRecordPersists(t *testing.T) {
	h := newHarness(t)
	test.Tap(h.row("clip.split").record)
	if h.e.recording != "clip.split" || h.w.Canvas().Focused() != h.e.rec {
		t.Fatal("recording did not start with focus")
	}
	h.e.rec.KeyDown(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
	if !strings.HasPrefix(h.e.rec.preview(), "Strg+") {
		t.Fatalf("preview %q", h.e.rec.preview())
	}
	h.e.rec.KeyUp(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
	h.press(fyne.KeyJ, desktop.KeyControlLeft, desktop.KeyAltLeft)
	if got := h.reg.Shortcut("clip.split"); got != "Ctrl+Alt+J" {
		t.Fatalf("shortcut %q", got)
	}
	if h.e.recording != "" {
		t.Fatal("still recording")
	}
	if got := savedKeymap(t)["clip.split"]; got != "Ctrl+Alt+J" {
		t.Fatalf("persisted %q", got)
	}
	if h.row("clip.split").reset == nil || !strings.Contains(h.e.summary.Text, "1 geändert") {
		t.Fatal("modified state not shown")
	}
}

func TestEscCancelsBackspaceClears(t *testing.T) {
	h := newHarness(t)
	test.Tap(h.row("file.save").record)
	h.e.rec.KeyDown(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if h.e.recording != "" || h.reg.Shortcut("file.save") != "Ctrl+S" {
		t.Fatal("Esc did not cancel")
	}
	test.Tap(h.row("file.save").record)
	h.e.rec.KeyDown(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	if h.reg.Shortcut("file.save") != "" {
		t.Fatal("Backspace did not clear")
	}
	if km := savedKeymap(t); km["file.save"] != "" || len(km) != 1 {
		t.Fatalf("cleared shortcut not persisted: %v", km)
	}
	// Esc with a modifier held is recordable
	test.Tap(h.row("file.save").record)
	h.press(fyne.KeyEscape, desktop.KeyShiftLeft)
	if h.reg.Shortcut("file.save") != "Shift+Escape" {
		t.Fatalf("got %q", h.reg.Shortcut("file.save"))
	}
	// losing focus without a key cancels; starting another row moves the recording
	test.Tap(h.row("file.new").record)
	test.Tap(h.row("clip.split").record)
	if h.e.recording != "clip.split" {
		t.Fatal("recording did not move")
	}
	h.e.rec.FocusLost()
	if h.e.recording != "" {
		t.Fatal("focus loss did not cancel")
	}
}

func TestConflictReplaceAndCancel(t *testing.T) {
	h := newHarness(t)
	test.Tap(h.row("file.new").record)
	h.press(fyne.KeyS, desktop.KeyControlLeft)
	if h.e.pending == nil || h.reg.Shortcut("file.new") != "Ctrl+N" {
		t.Fatal("conflict not held")
	}
	r := h.row("file.new")
	if r.conflict == nil || r.conflict.Text != "Belegt durch: Speichern" {
		t.Fatalf("conflict text %v", r.conflict)
	}
	test.Tap(r.abort)
	if h.e.pending != nil || h.reg.Shortcut("file.new") != "Ctrl+N" || h.reg.Shortcut("file.save") != "Ctrl+S" {
		t.Fatal("Abbrechen changed something")
	}
	if len(savedKeymap(t)) != 0 {
		t.Fatal("cancelled conflict persisted")
	}
	test.Tap(h.row("file.new").record)
	h.press(fyne.KeyS, desktop.KeyControlRight)
	test.Tap(h.row("file.new").replace)
	if h.reg.Shortcut("file.new") != "Ctrl+S" || h.reg.Shortcut("file.save") != "" {
		t.Fatal("Ersetzen did not move the shortcut")
	}
	km := savedKeymap(t)
	if v, ok := km["file.save"]; km["file.new"] != "Ctrl+S" || !ok || v != "" {
		t.Fatalf("persisted %v", km)
	}
	if len(h.reg.Conflicts("Ctrl+S", "")) != 1 {
		t.Fatal("conflict remains")
	}
}

func TestResetAndFilter(t *testing.T) {
	h := newHarness(t)
	h.reg.SetShortcut("file.save", "F5")
	h.reg.SetShortcut("clip.split", "Ctrl+Alt+J")
	h.e.rebuild()
	if h.e.resetAllBtn.Disabled() {
		t.Fatal("reset all disabled with overrides")
	}
	// Nur geänderte
	h.e.onlyChanged.SetChecked(true)
	if len(h.e.rows) != 2 {
		t.Fatalf("only changed shows %d", len(h.e.rows))
	}
	test.Tap(h.row("file.save").reset)
	if h.reg.Shortcut("file.save") != "Ctrl+S" || len(h.e.rows) != 1 {
		t.Fatal("row reset")
	}
	if _, ok := savedKeymap(t)["file.save"]; ok {
		t.Fatal("reset not persisted")
	}
	h.e.onlyChanged.SetChecked(false)
	// search by label and by shortcut text
	h.e.search.SetText("speich")
	if _, ok := h.e.rows["file.save"]; !ok || len(h.e.rows) > 8 {
		t.Fatalf("search label: %d rows", len(h.e.rows))
	}
	h.e.search.SetText("strg+alt+j")
	if len(h.e.rows) != 1 || h.e.rows["clip.split"] == nil {
		t.Fatalf("search shortcut: %v", len(h.e.rows))
	}
	h.e.search.SetText("")
	if len(h.e.rows) != len(h.reg.All()) {
		t.Fatal("unfiltered table incomplete")
	}
	h.e.resetAll()
	if len(h.reg.Keymap()) != 0 || len(savedKeymap(t)) != 0 || !h.e.resetAllBtn.Disabled() {
		t.Fatal("reset all")
	}
}

func TestShowDialog(t *testing.T) {
	h := newHarness(t)
	w := test.NewWindow(canvas.NewRectangle(studiotheme.Gap))
	w.Resize(fyne.NewSize(1000, 700))
	defer w.Close()
	e := show(w, h.reg)
	if len(w.Canvas().Overlays().List()) != 1 || w.Canvas().Focused() != e.search {
		t.Fatal("dialog not shown with search focused")
	}
}

// TestScreenshot writes /tmp/fs-r3-menubar-keymap.png when FS_SCREENSHOT is set.
func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r3-menubar-keymap.png")
	}
	h := newHarness(t)
	w := test.NewWindow(canvas.NewRectangle(studiotheme.Gap))
	w.Resize(fyne.NewSize(1440, 900))
	defer w.Close()
	h.reg.SetShortcut("file.saveAll", "Ctrl+Shift+Alt+S")
	h.reg.SetShortcut("file.manager", "")
	e := show(w, h.reg)
	e.rebuild()
	test.Tap(e.rows["file.new"].record)
	e.press(t, fyne.KeyS, desktop.KeyControlLeft)
	write(t, "/tmp/fs-r3-menubar-keymap.png", w.Canvas())
	test.Tap(e.rows["file.open"].record)
	e.rec.KeyDown(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
	e.rec.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	write(t, "/tmp/fs-r3-menubar-keymap-record.png", w.Canvas())
}

func (e *editor) press(t *testing.T, key fyne.KeyName, mods ...fyne.KeyName) {
	for _, m := range mods {
		e.rec.KeyDown(&fyne.KeyEvent{Name: m})
	}
	e.rec.KeyDown(&fyne.KeyEvent{Name: key})
	e.rec.KeyUp(&fyne.KeyEvent{Name: key})
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
