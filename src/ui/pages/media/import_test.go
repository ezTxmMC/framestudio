package media

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

func TestPortalImportAndFallbackBrowser(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	a := writePNG(t, dir, "Anfang.png", 32, 18, color.NRGBA{200, 80, 40, 255})
	b := writePNG(t, dir, "Mitte.png", 32, 18, color.NRGBA{40, 200, 80, 255})
	writePNG(t, dir, "Schluss.png", 32, 18, color.NRGBA{40, 80, 200, 255})
	_ = os.WriteFile(filepath.Join(dir, "liesmich.txt"), []byte("x"), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "Unterordner"), 0o755)

	// The portal returns several files: they are imported without another dialog.
	var asked pickRequest
	h.p.pickFiles = func(req pickRequest) ([]string, error) { asked = req; return []string{a, b}, nil }
	h.p.importBtn.OnTapped()
	if !h.p.picking {
		t.Fatal("the chooser should be marked open while the portal runs")
	}
	h.drain()
	if !asked.multi || asked.folders || asked.accept != "Importieren" || len(h.s.Project.Media) != 2 || h.p.fileDlg != nil {
		t.Fatalf("portal import: req %+v, %d media", asked, len(h.s.Project.Media))
	}

	// Cancelling the portal does nothing.
	h.p.pickFiles = func(pickRequest) ([]string, error) { return nil, nil }
	h.p.importBtn.OnTapped()
	h.drain()
	if h.p.fileDlg != nil || len(h.s.Project.Media) != 2 {
		t.Fatal("cancel should neither import nor open the fallback")
	}

	// Without a portal the in-app browser opens on the start folder.
	h.p.pickFiles = func(pickRequest) ([]string, error) { return nil, os.ErrNotExist }
	h.p.files.open(dir)
	h.p.importBtn.OnTapped()
	h.drain()
	d := h.p.fileDlg
	if d == nil || d.dir != dir {
		t.Fatal("fallback browser should open in the files panel folder")
	}
	if len(d.entries) != 4 || !d.entries[0].dir { // folder first, the .txt is filtered out
		t.Fatalf("entries %+v", d.entries)
	}
	if !d.okBtn.Disabled() || d.count.Text != "Keine Datei ausgewählt" {
		t.Fatal("nothing chosen yet")
	}
	d.tap(d.entries[1], 100)
	d.tap(d.entries[3], 100)
	if d.okBtn.Text != "Importieren (2)" || d.okBtn.Disabled() {
		t.Fatalf("button %q", d.okBtn.Text)
	}
	d.tap(d.entries[3], 100) // tapping again unchecks
	d.toggleAll()
	if len(d.order) != 3 || d.allBtn.Text != "Auswahl aufheben" {
		t.Fatalf("select all: %v %q", d.order, d.allBtn.Text)
	}
	d.toggleAll()
	if len(d.order) != 0 {
		t.Fatal("second click should clear the folder")
	}
	d.kindSel.SetSelected("Audio")
	if len(d.entries) != 1 || !d.entries[0].dir {
		t.Fatalf("type filter: %+v", d.entries)
	}
	d.kindSel.SetSelectedIndex(0)
	d.tap(d.entries[0], 100) // folder rows open
	if d.dir != filepath.Join(dir, "Unterordner") {
		t.Fatalf("folder not opened: %s", d.dir)
	}
	d.up.OnTapped()
	for _, e := range d.entries {
		if e.name == "Schluss.png" {
			d.tap(e, 100)
		}
	}
	d.okBtn.OnTapped()
	h.drain()
	if len(h.s.Project.Media) != 3 || h.p.fileDlg != nil {
		t.Fatalf("fallback import: %d media", len(h.s.Project.Media))
	}
	if len(h.errors) != 0 {
		t.Fatalf("errors %v", h.errors)
	}
}

func TestRecursiveFolderImport(t *testing.T) {
	root := t.TempDir()
	writePNG(t, root, "b.png", 4, 4, color.NRGBA{1, 2, 3, 255})
	sub := filepath.Join(root, "Szene 1")
	deep := filepath.Join(sub, "Take 2")
	_ = os.MkdirAll(deep, 0o755)
	_ = os.Mkdir(filepath.Join(root, ".cache"), 0o755)
	writePNG(t, filepath.Join(root, ".cache"), "versteckt.png", 4, 4, color.NRGBA{1, 2, 3, 255})
	writePNG(t, sub, "a.png", 4, 4, color.NRGBA{1, 2, 3, 255})
	writePNG(t, deep, "c.png", 4, 4, color.NRGBA{1, 2, 3, 255})
	writePNG(t, root, "A.png", 4, 4, color.NRGBA{1, 2, 3, 255})

	flat := planImport([]string{root}, map[string]bool{}, false)
	if len(flat.files) != 2 {
		t.Fatalf("top level only: %v", flat.files)
	}
	all := planImport([]string{root}, map[string]bool{}, true)
	want := []string{filepath.Join(root, "A.png"), filepath.Join(root, "b.png"), filepath.Join(sub, "a.png"), filepath.Join(deep, "c.png")}
	if len(all.files) != len(want) {
		t.Fatalf("recursive: %v", all.files)
	}
	for i := range want {
		if all.files[i] != want[i] {
			t.Fatalf("order: %v", all.files)
		}
	}

	// Through the UI: folder chooser (fallback) with "Unterordner einbeziehen".
	h := newHarness(t)
	var asked pickRequest
	h.p.pickFiles = func(req pickRequest) ([]string, error) { asked = req; return nil, os.ErrNotExist }
	h.p.files.open(root)
	h.p.folderBtn.OnTapped()
	h.drain()
	d := h.p.fileDlg
	if d == nil || !asked.folders {
		t.Fatal("folder chooser should fall back to the browser")
	}
	if d.okBtn.Disabled() || d.okBtn.Text != "Importieren" {
		t.Fatal("folder mode imports the open folder when nothing is marked")
	}
	// Folder rows: the check box column marks, the rest opens.
	var szene browserEntry
	for _, e := range d.entries {
		if e.name == "Szene 1" {
			szene = e
		}
	}
	d.tap(szene, 10)
	if !d.chosen[sub] || d.dir != root {
		t.Fatal("check box column should mark the folder")
	}
	h.p.recursive.SetChecked(true)
	d.okBtn.OnTapped()
	h.drain()
	if n := len(h.s.Project.Media); n != 2 {
		t.Fatalf("recursive import of Szene 1: %d media", n)
	}
}

func TestPoolMultiSelect(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Edit("x", func(p *model.Project) error {
		for _, id := range []string{"1", "2", "3", "4"} {
			p.AddMedia(model.MediaItem{ID: id, Name: "Bild " + id, Kind: model.KindImage, Path: "/nicht/da" + id + ".png", Width: 16, Height: 9})
		}
		return nil
	})
	cards := findCards(h.w.Content())
	if len(cards) != 4 {
		t.Fatalf("%d cards", len(cards))
	}
	click := func(c *card, mod fyne.KeyModifier) {
		c.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary, Modifier: mod})
		c.Tapped(&fyne.PointEvent{})
	}
	click(cards[0], 0)
	click(cards[2], fyne.KeyModifierControl)
	if len(h.p.sel) != 2 || h.s.SelectedMedia != "3" || !h.p.isPicked("1") {
		t.Fatalf("ctrl+click: %v primary %q", h.p.sel, h.s.SelectedMedia)
	}
	if h.p.count.Text != "2 ausgewählt · 4 Medien" || h.p.insp.insertBtn.Text != "In Timeline einfügen (2)" {
		t.Fatalf("count %q, button %q", h.p.count.Text, h.p.insp.insertBtn.Text)
	}
	click(cards[0], fyne.KeyModifierControl) // toggles off
	if len(h.p.sel) != 1 || h.s.SelectedMedia != "3" {
		t.Fatalf("ctrl+click off: %v", h.p.sel)
	}
	click(cards[1], fyne.KeyModifierShift) // range 3..2 from the anchor
	if len(h.p.sel) != 2 || h.s.SelectedMedia != "2" {
		t.Fatalf("shift+click: %v", h.p.sel)
	}
	click(cards[3], fyne.KeyModifierShift)
	if len(h.p.sel) != 2 || !h.p.isPicked("4") || h.p.isPicked("2") {
		t.Fatalf("shift range should restart from the anchor: %v", h.p.sel)
	}
	click(cards[1], 0)
	if len(h.p.sel) != 1 || h.s.SelectedMedia != "2" {
		t.Fatal("plain click selects one item")
	}
	// Another page choosing a media item resets the pool selection.
	h.p.selectAll()
	h.s.SelectMedia("1")
	if len(h.p.sel) != 1 || h.p.sel[0] != "1" {
		t.Fatalf("external selection: %v", h.p.sel)
	}

	// Enter inserts the whole selection one after another as one undo step.
	h.p.selectAll()
	if !h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) {
		t.Fatal("Enter not handled")
	}
	if len(h.s.Selected) != 4 || h.s.Playhead != 4*125 || h.s.UndoLabel() != "In Timeline einfügen" {
		t.Fatalf("inserted %v, playhead %d", h.s.Selected, h.s.Playhead)
	}
	h.s.Undo()
	if usage(h.s.Project, "1")+usage(h.s.Project, "4") != 0 {
		t.Fatal("multi insert must be one undo step")
	}
	// Removing the selection is one step too.
	h.p.selectAll()
	h.p.remove(h.p.sel...)
	if len(h.s.Project.Media) != 0 || h.s.UndoLabel() != "Medien entfernen" || len(h.p.sel) != 0 {
		t.Fatalf("remove: %d media, %v", len(h.s.Project.Media), h.p.sel)
	}
}

// fakeZone is a drop zone below the page that records what it receives.
type fakeZone struct {
	widget.BaseWidget
	over    int
	dropped []string
	at      fyne.Position
}

func (z *fakeZone) MediaDragOver(ids []string, pos fyne.Position) { z.over++ }
func (z *fakeZone) MediaDragLeave()                               {}
func (z *fakeZone) MediaDrop(ids []string, pos fyne.Position) bool {
	z.dropped, z.at = ids, pos
	return true
}
func (z *fakeZone) MinSize() fyne.Size { return fyne.NewSize(100, 120) }
func (z *fakeZone) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack())
}

func TestCardDragToTimeline(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Edit("x", func(p *model.Project) error {
		for _, id := range []string{"1", "2", "3"} {
			p.AddMedia(model.MediaItem{ID: id, Name: "Testbild " + id, Kind: model.KindImage, Path: "/x" + id + ".png"})
		}
		return nil
	})
	zone := &fakeZone{}
	zone.ExtendBaseWidget(zone)
	h.w.SetContent(container.NewBorder(nil, zone, nil, nil, h.p.Content()))
	h.w.Resize(fyne.NewSize(1440, 900))
	defer kit.RegisterDropZone(h.w, zone)()
	cards := findCards(h.w.Content())

	ids, label := h.p.dragItems("2")
	if len(ids) != 1 || label != "Testbild 2" || h.s.SelectedMedia != "2" {
		t.Fatalf("single drag %v %q", ids, label)
	}
	h.p.pick("3", fyne.KeyModifierControl)
	if ids, label := h.p.dragItems("3"); len(ids) != 2 || label != "2 Clips" {
		t.Fatalf("selection drag %v %q", ids, label)
	}

	zonePos := fyne.CurrentApp().Driver().AbsolutePositionForObject(zone)
	start := fyne.CurrentApp().Driver().AbsolutePositionForObject(cards[2])
	drag := func(abs fyne.Position) {
		cards[2].Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: abs}, Dragged: fyne.Delta{DX: 5, DY: 5}})
	}
	drag(start.AddXY(20, 20))
	if !kit.MediaDragActive() {
		t.Fatal("drag should start on the first movement")
	}
	drag(zonePos.AddXY(300, 40))
	if zone.over == 0 {
		t.Fatal("zone should see the drag")
	}
	cards[2].DragEnd()
	if kit.MediaDragActive() || len(zone.dropped) != 2 || zone.dropped[0] != "2" || zone.dropped[1] != "3" {
		t.Fatalf("drop: active %v, ids %v", kit.MediaDragActive(), zone.dropped)
	}
	if zone.at.X != 300 || zone.at.Y != 40 {
		t.Fatalf("drop position %v", zone.at)
	}
	// A tap after a drag is a normal click again.
	cards[0].Tapped(&fyne.PointEvent{})
	if len(h.p.sel) != 1 || h.s.SelectedMedia != "1" {
		t.Fatal("tap after drag should select one item")
	}
	// Dropping outside every zone cancels.
	drag(start.AddXY(10, 10))
	drag(start.AddXY(15, 15))
	cards[2].DragEnd()
	if kit.MediaDragActive() || len(zone.dropped) != 2 {
		t.Fatal("drop outside the zone must not reach it")
	}
}

func TestOfflineWatcher(t *testing.T) {
	h := newHarness(t)
	q := kit.UseUIQueue(t)
	h.p.spawn = func(f func()) { go f() }
	h.p.offlineEvery = 20 * time.Millisecond
	path := writePNG(t, t.TempDir(), "Wechsel.png", 8, 8, color.NRGBA{9, 9, 9, 255})
	_ = h.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(model.MediaItem{ID: "w", Name: "Wechsel", Kind: model.KindImage, Path: path, Width: 8, Height: 8})
		return nil
	})
	h.p.Activate()
	if h.p.watchStop == nil {
		t.Fatal("activation should start the watcher")
	}
	_ = os.Remove(path)
	badge := func() bool {
		cards := findCards(h.w.Content())
		return len(cards) == 1 && !cards[0].badge.Hidden
	}
	if !q.Until(func() bool { return h.p.missing("w") && badge() }, 3*time.Second) {
		t.Fatal("removed file should be flagged offline on its pool card")
	}
	if h.p.insp.status.Hidden {
		t.Fatal("inspector should show the missing file")
	}
	writePNG(t, filepath.Dir(path), "Wechsel.png", 8, 8, color.NRGBA{9, 9, 9, 255})
	if !q.Until(func() bool { return !h.p.missing("w") && !badge() }, 3*time.Second) {
		t.Fatal("restored file should come back online")
	}
	h.p.Deactivate()
	if h.p.watchStop != nil {
		t.Fatal("deactivation should stop the watcher")
	}
	// Let background work finish while the queue still serialises its callbacks.
	q.Until(func() bool { return len(h.p.loading) == 0 && !h.p.checking }, 3*time.Second)
	h.p.Close()
	q.Settle(50*time.Millisecond, time.Second)
}
