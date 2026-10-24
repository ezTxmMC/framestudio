package session

import (
	"framestudio-proto/src/model"
	"testing"
	"time"
)

func TestLinkedSelectionToggleAndUndo(t *testing.T) {
	p := model.NewProject("links")
	m := model.MediaItem{ID: model.NewID(), Kind: model.KindVideo, HasAudio: true, AudioStreams: 2, Duration: time.Second}
	p.AddMedia(m)
	id, err := p.PlaceMedia(m, 0, 1, p.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	s := New(p)
	s.SelectClip(id)
	if len(s.Selected) != 3 || s.SelectedClip != id {
		t.Fatalf("selection %v primary %s", s.Selected, s.SelectedClip)
	}
	s.ToggleClip(p.LinkedClips(id)[0])
	if len(s.Selected) != 0 {
		t.Fatal("toggle left partners selected")
	}
	s.SelectClip(id)
	if err := s.Edit("unlink", func(p *model.Project) error { return p.UnlinkClips([]string{id}) }); err != nil {
		t.Fatal(err)
	}
	s.SelectClip(id)
	if len(s.Selected) != 1 {
		t.Fatal("unlinked selection expanded")
	}
	s.Undo()
	s.SelectClip(id)
	if len(s.Selected) != 3 {
		t.Fatal("undo lost links")
	}
	s.Redo()
	s.SelectClip(id)
	if len(s.Selected) != 1 {
		t.Fatal("redo lost unlink")
	}
}
