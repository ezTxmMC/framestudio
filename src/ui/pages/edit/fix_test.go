package edit

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"

	"framestudio-proto/src/model"
)

// S with the selected clip on a locked track reports the lock instead of cutting the
// other tracks.
func TestSplitOnLockedSelectionReportsLock(t *testing.T) {
	f := newFixture(t, basicProject())
	colour := f.s.Project.Tracks[0].Clips[0].ID
	f.s.SelectClip(colour)
	_ = f.s.Edit("Spur sperren", func(p *model.Project) error { p.Tracks[0].Locked = true; return nil })
	f.s.Seek(50)
	undo := f.s.UndoLabel()
	f.pg.dispatch(fyne.KeyK, fyne.KeyModifierControl)
	if len(f.s.Project.Tracks[1].Clips) != 1 || f.s.UndoLabel() != undo {
		t.Fatalf("the title on V2 was cut (undo %q)", f.s.UndoLabel())
	}
	if len(f.errs) != 1 {
		t.Fatalf("errors %v", f.errs)
	}
	if got := f.errs[0].Error(); got != "Die Spur ist gesperrt. Spur in der Timeline entsperren und erneut versuchen" {
		t.Fatalf("message %q", got)
	}
}

// The master meter does not poll while the edit page is hidden, e.g. when playback runs
// on the Farbe page.
func TestMeterIdleWhileHidden(t *testing.T) {
	f := newFixture(t, basicProject())
	m := f.pg.transport.Meter()
	f.pg.Activate()
	f.pg.Deactivate()
	f.s.SetPlaying(true)
	if m.Running() {
		t.Fatal("hidden meter polls")
	}
	f.pg.Activate()
	if !m.Running() {
		t.Fatal("meter must poll again once the page shows")
	}
	f.pg.Deactivate()
	if m.Running() {
		t.Fatal("hiding the page must stop the meter")
	}
	f.s.SetPlaying(false)
}

// Files dragged from the file manager onto the timeline are imported and placed at the
// pointer; the page stays on Schnitt.
func TestFileDropOnTimeline(t *testing.T) {
	f := newFixture(t, basicProject())
	f.pg.Activate()
	dir := t.TempDir()
	path := filepath.Join(dir, "Logo.png")
	img := image.NewNRGBA(image.Rect(0, 0, 16, 9))
	fh, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = png.Encode(fh, img)
	_ = fh.Close()
	f.pg.spawn = func(fn func()) { fn() }
	v1 := f.s.Project.Tracks[0].ID
	rel, ok := f.pg.tl.LanePoint(150, v1)
	if !ok {
		t.Fatal("no V1 lane")
	}
	abs := fyne.CurrentApp().Driver().AbsolutePositionForObject(f.pg.tl).Add(rel)
	f.pg.DroppedAt(abs, []fyne.URI{storage.NewFileURI(path)})
	f.q.drain()
	if len(f.s.Project.Media) != 1 {
		t.Fatalf("media %d, errors %v", len(f.s.Project.Media), f.errs)
	}
	clips := f.s.Project.Tracks[0].Clips
	last := clips[len(clips)-1]
	if last.MediaID != f.s.Project.Media[0].ID || last.Start < 140 || last.Start > 160 {
		t.Fatalf("dropped file not placed at the pointer: %+v", last)
	}
	if f.nav != "" {
		t.Fatalf("drop switched to %q", f.nav)
	}
	// dropped elsewhere it only joins the library, without a second copy
	f.pg.DroppedAt(fyne.NewPos(-1, -1), []fyne.URI{storage.NewFileURI(path)})
	f.q.drain()
	if len(f.s.Project.Media) != 1 || len(f.s.Project.Tracks[0].Clips) != len(clips) {
		t.Fatal("drop outside the timeline must not place or duplicate")
	}
}
