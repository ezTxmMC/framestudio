package edit

import (
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/page"
	"testing"
	"time"
)

func TestLinkCommandsAndIndependentCopies(t *testing.T) {
	p := model.NewProject("links")
	m := model.MediaItem{ID: model.NewID(), Kind: model.KindVideo, HasAudio: true, AudioStreams: 2, Duration: time.Second}
	p.AddMedia(m)
	id, err := p.PlaceMedia(m, 0, 1, p.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, p)
	f.s.SelectClip(id)
	if len(f.s.Selected) != 3 || !enabled(f, "clip.unlink") {
		t.Fatal("imported group not selected/unlinkable")
	}
	original, _, _ := f.s.Project.Clip(id)
	originalGroup := original.LinkID
	run(t, f, "edit.duplicate")
	copyID := f.s.SelectedClip
	copy, _, _ := f.s.Project.Clip(copyID)
	if copy.LinkID == originalGroup || len(f.s.Selected) != 3 {
		t.Fatal("duplicate linked back to originals")
	}
	run(t, f, "edit.copy")
	f.s.Seek(75)
	run(t, f, "edit.paste")
	pasted, _, _ := f.s.Project.Clip(f.s.SelectedClip)
	if pasted.LinkID == copy.LinkID || pasted.LinkID == originalGroup || len(f.s.Selected) != 3 {
		t.Fatal("paste linked back to source")
	}
	run(t, f, "clip.unlink")
	pastedID := f.s.SelectedClip
	f.s.SelectClip(pastedID)
	if len(f.s.Selected) != 1 || enabled(f, "clip.unlink") {
		t.Fatal("unlink failed")
	}
	if len(f.s.Project.ExplicitGroup(id)) != 3 {
		t.Fatal("unlink affected originals")
	}
	f.s.SelectClips(f.s.Project.ExplicitGroup(id))
	run(t, f, "clip.unlink")
	if !enabled(f, "clip.link") {
		t.Fatal("manual link disabled")
	}
	run(t, f, "clip.link")
	f.s.SelectClip(id)
	if len(f.s.Selected) != 3 {
		t.Fatal("manual link did not select partners")
	}
}

func TestDockedAreasDoNotOverwriteLayoutVisibility(t *testing.T) {
	f := newFixture(t, basicProject())
	before := f.pg.LayoutState()
	f.pg.SetDockedPanels([]string{page.PanelTimeline, page.PanelProgram})
	if f.pg.vsplit != nil || !f.pg.areaHidden(areaTimeline) || !f.pg.areaHidden(areaViewer) {
		t.Fatal("original views still present")
	}
	if f.pg.LayoutState().Hidden[areaViewer] != before.Hidden[areaViewer] {
		t.Fatal("docking overwrote saved visibility")
	}
	f.pg.SetDockedPanels(nil)
	if f.pg.vsplit == nil || f.pg.areaHidden(areaTimeline) || f.pg.areaHidden(areaViewer) {
		t.Fatal("source views did not return")
	}
}
