package media

import (
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func pageRegistry(p *Page) *command.Registry {
	r := command.NewRegistry()
	scope := command.PageScope(page.Media)
	r.Bind(scope, p.Commands()...)
	r.SetScopeActive(scope, true)
	return r
}

func isEnabled(t *testing.T, r *command.Registry, id string) bool {
	t.Helper()
	c, ok := r.Get(id)
	if !ok || !c.Available {
		t.Fatalf("%s not bound", id)
	}
	return c.Enabled
}

// tapButton taps the button labelled text in the window's top overlay (a dialog).
func tapButton(t *testing.T, w fyne.Window, text string) {
	t.Helper()
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatalf("no dialog open to tap %q", text)
	}
	var found *widget.Button
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Button:
			if x.Text == text {
				found = x
			}
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(x).Objects() {
				walk(c)
			}
		}
	}
	walk(top)
	if found == nil {
		t.Fatalf("button %q not found", text)
	}
	test.Tap(found)
}

// importPNGs imports n small pictures and returns them in pool order.
func (h *harness) importPNGs(t *testing.T, names ...string) []model.MediaItem {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for _, n := range names {
		paths = append(paths, writePNG(t, dir, n+".png", 32, 18, color.NRGBA{90, 140, 230, 255}))
	}
	h.p.importPaths(paths)
	h.drain()
	if len(h.s.Project.Media) != len(names) {
		t.Fatalf("imported %d of %d", len(h.s.Project.Media), len(names))
	}
	return h.s.Project.Media
}

func TestMediaCommands(t *testing.T) {
	h := newHarness(t)
	r := pageRegistry(h.p)
	for _, id := range []string{"media.relink", "media.reveal", "media.removeUnused", "media.insert", "media.append", "edit.delete", "edit.selectAll"} {
		if isEnabled(t, r, id) {
			t.Errorf("%s enabled with an empty pool", id)
		}
	}
	if !isEnabled(t, r, "media.import") || !isEnabled(t, r, "media.importFolder") {
		t.Fatal("import is always possible when idle")
	}
	items := h.importPNGs(t, "A", "B", "C")
	if !isEnabled(t, r, "media.removeUnused") || !isEnabled(t, r, "media.relink") {
		t.Fatal("unused media and one selected item")
	}

	// Insert and append the selection.
	h.s.SelectMedia(items[0].ID)
	r.Run("media.insert")
	if len(h.s.Project.Tracks[0].Clips) != 1 || h.s.UndoLabel() != "In Timeline einfügen" {
		t.Fatal("media.insert")
	}
	h.s.SelectMedia(items[1].ID)
	r.Run("media.append")
	if c, _ := h.s.Clip(); c == nil || c.MediaID != items[1].ID || c.Start == 0 {
		t.Fatal("media.append places behind the last clip")
	}

	// Select all, then relink needs exactly one item.
	r.Run("edit.selectAll")
	if len(h.p.sel) != 3 || isEnabled(t, r, "media.relink") || !isEnabled(t, r, "edit.delete") {
		t.Fatalf("select all: %d selected", len(h.p.sel))
	}
	r.Run("edit.deselect")
	if len(h.p.sel) != 0 || h.s.SelectedMedia != "" {
		t.Fatal("deselect")
	}

	// Reveal opens the containing folder.
	var opened string
	h.p.openDir = func(d string) error { opened = d; return nil }
	h.s.SelectMedia(items[2].ID)
	r.Run("media.reveal")
	if opened != filepath.Dir(items[2].Path) {
		t.Fatalf("revealed %q", opened)
	}
	h.p.openDir = func(string) error { return errors.New("kein xdg-open") }
	r.Run("media.reveal")
	if len(h.errors) != 1 || !strings.Contains(h.errors[0].Error(), "xdg-open") {
		t.Fatalf("errors %v", h.errors)
	}

	// Remove unused: confirm, one undo step, used media stay.
	r.Run("media.removeUnused")
	if len(h.s.Project.Media) != 3 {
		t.Fatal("removeUnused must ask first")
	}
	tapButton(t, h.w, "Entfernen")
	if len(h.s.Project.Media) != 2 || h.s.UndoLabel() != "Unbenutzte Medien entfernen" {
		t.Fatalf("media %d, undo %q", len(h.s.Project.Media), h.s.UndoLabel())
	}
	if _, ok := h.s.Project.MediaByID(items[2].ID); ok {
		t.Fatal("C is unused and should be gone")
	}
	if isEnabled(t, r, "media.removeUnused") {
		t.Fatal("nothing unused left")
	}
	h.s.Undo()
	if len(h.s.Project.Media) != 3 {
		t.Fatal("one undo brings it back")
	}

	// Import is disabled while an import runs.
	h.p.importPaths([]string{t.TempDir()})
	if isEnabled(t, r, "media.import") {
		t.Fatal("import must be disabled while importing")
	}
	h.drain()
}

func TestGlobalImportFromAnyPage(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	s := session.New(model.NewProject("Global"))
	w := test.NewTempWindow(t, nil)
	cache := media.NewCache()
	t.Cleanup(cache.Close)
	var shown []string
	var queue []func()
	var asked []pickRequest
	ctx := &page.Context{Window: w, Session: s, Media: cache}
	var pg *Page
	ctx.Navigate = func(id string) {
		shown = append(shown, id)
		if pg == nil {
			pg = New(ctx).(*Page)
			pg.spawn = func(f func()) { queue = append(queue, f) }
			pg.pickFiles = func(r pickRequest) ([]string, error) { asked = append(asked, r); return nil, nil }
			w.SetContent(pg.Content())
			t.Cleanup(pg.Close)
		}
	}
	r := command.NewRegistry()
	r.Bind(command.Global, GlobalCommands(ctx)...)
	if !r.Run("media.importFolder") {
		t.Fatal("global media.importFolder must run without the media page")
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		f()
	}
	if len(shown) != 1 || shown[0] != page.Media || len(asked) != 1 || !asked[0].folders {
		t.Fatalf("shown %v asked %+v", shown, asked)
	}
	r.Run("media.import")
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		f()
	}
	if len(asked) != 2 || asked[1].folders || !asked[1].multi {
		t.Fatalf("file import request %+v", asked)
	}
	pg.Close()
	if pages[s] != nil {
		t.Fatal("closed page must leave the registry")
	}
}

func TestImportRecursiveSetting(t *testing.T) {
	t.Cleanup(func() { _ = settings.Reset() })
	h := newHarness(t)
	if h.p.recursive.Checked {
		t.Fatal("default: subfolders off")
	}
	_ = settings.Update(func(s *settings.Settings) { s.Media.ImportRecursive = true })
	if !h.p.recursive.Checked {
		t.Fatal("a settings change should update the check")
	}
	h2 := newHarness(t)
	if !h2.p.recursive.Checked {
		t.Fatal("new pages start with the setting")
	}
	// The setting really includes subfolders.
	dir := t.TempDir()
	sub := filepath.Join(dir, "Tag 1")
	_ = os.Mkdir(sub, 0o755)
	writePNG(t, sub, "tief.png", 8, 8, color.NRGBA{A: 255})
	h2.p.importPaths([]string{dir})
	h2.drain()
	if len(h2.s.Project.Media) != 1 {
		t.Fatal("folder import should include subfolders")
	}
}

func TestMediaLayoutRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.importPNGs(t, "A")
	if l := h.p.LayoutState(); l.Splits[splitFiles] != defaultFilesSplit || len(l.Hidden) != 0 || l.Extra != nil {
		t.Fatalf("default %+v", l)
	}
	h.p.outer.SetOffset(0.25)
	h.p.inner.SetOffset(0.6)
	h.p.SetAreaVisible(areaInspector, false)
	h.p.kindSel.SetSelected("Audio")
	l := h.p.LayoutState()
	if l.Splits[splitFiles] != 0.25 || l.Splits[splitInspector] != 0.6 || !l.Hidden[areaInspector] || l.Extra[extraKind] != "audio" {
		t.Fatalf("state %+v", l)
	}
	if len(h.p.view) != 0 {
		t.Fatal("audio filter hides the picture")
	}
	h.p.ApplyLayout(layout.Page{})
	if h.p.outer.Offset != defaultFilesSplit || !h.p.inspBox.Visible() || h.p.kindFilter != "" || len(h.p.view) != 1 {
		t.Fatal("empty layout restores the defaults")
	}
	h.p.ApplyLayout(l)
	if got := h.p.LayoutState(); got.Splits[splitFiles] != 0.25 || !got.Hidden[areaInspector] || got.Extra[extraKind] != "audio" {
		t.Fatalf("round trip %+v", got)
	}
	// Hidden files: the pool starts at the left edge.
	h.p.SetAreaVisible(areaFiles, false)
	h.w.Resize(fyne.NewSize(1440, 900))
	if x := fyne.CurrentApp().Driver().AbsolutePositionForObject(h.p.grid).X; x > 40 {
		t.Fatalf("with the file browser hidden the pool starts at x=%.0f", x)
	}
}

func TestPoolPanelStandalone(t *testing.T) {
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	s := session.New(model.NewProject("Pool"))
	w := test.NewTempWindow(t, nil)
	cache := media.NewCache()
	t.Cleanup(cache.Close)
	pi, ok := page.PanelByID(page.PanelMediaPool)
	if !ok {
		t.Fatal("media pool panel not registered")
	}
	panel := pi.New(&page.Context{Window: w, Session: s, Media: cache})
	pp := panel.(*poolPanel)
	if pages[s] != nil {
		t.Fatal("the panel is not the media page")
	}
	if pp.p.outer != nil || pp.p.importBtn == nil {
		t.Fatal("the panel has the pool with its import buttons, no side panels")
	}
	var queue []func()
	pp.p.spawn = func(f func()) { queue = append(queue, f) }
	dir := t.TempDir()
	path := writePNG(t, dir, "Panel.png", 32, 18, color.NRGBA{200, 100, 50, 255})
	pp.p.pickFiles = func(pickRequest) ([]string, error) { return []string{path}, nil }
	w.SetContent(panel.Content())
	w.Resize(fyne.NewSize(700, 480))
	test.Tap(pp.p.importBtn)
	for i := 0; i < 20 && len(queue) > 0; i++ {
		q := queue
		queue = nil
		for _, f := range q {
			f()
		}
	}
	if len(s.Project.Media) != 1 || len(pp.p.view) != 1 {
		t.Fatalf("panel import: %d media", len(s.Project.Media))
	}
	if !panel.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) || s.SelectedMedia != "" {
		t.Fatal("Esc clears the pool selection in the panel")
	}
	panel.Close()
	if pp.p.watchStop != nil {
		t.Fatal("closing the panel stops the offline watcher")
	}
}

// TestPoolPanelScreenshot writes the standalone media pool to /tmp/fs-r3-panels-pool.png.
func TestPoolPanelScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("FS_SCREENSHOT nicht gesetzt")
	}
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	s := session.New(model.NewProject("Pool"))
	w := test.NewTempWindow(t, nil)
	cache := media.NewCache()
	t.Cleanup(cache.Close)
	pp := newPoolPanel(&page.Context{Window: w, Session: s, Media: cache})
	t.Cleanup(pp.Close)
	var queue []func()
	pp.p.spawn = func(f func()) { queue = append(queue, f) }
	dir := t.TempDir()
	var paths []string
	cols := []color.NRGBA{{90, 140, 230, 255}, {230, 140, 70, 255}, {120, 200, 150, 255}, {200, 90, 160, 255}, {160, 160, 170, 255}}
	for i, n := range []string{"Logo Bauchbinde", "Standbild Abendlicht", "Titelkarte", "Moodboard Küste", "Abspann"} {
		paths = append(paths, writePNG(t, dir, n+".png", 640, 360, cols[i]))
	}
	pp.p.importPaths(paths)
	w.SetContent(pp.Content())
	w.Resize(fyne.NewSize(860, 520))
	for i := 0; i < 20 && len(queue) > 0; i++ {
		q := queue
		queue = nil
		for _, f := range q {
			f()
		}
		w.Canvas().Refresh(w.Content())
	}
	pp.p.pick(s.Project.Media[1].ID, 0)
	capture(t, w, "/tmp/fs-r3-panels-pool.png")
}
