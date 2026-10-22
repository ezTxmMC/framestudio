package session

import (
	"errors"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func newSession() *Session {
	p := model.NewProject("t")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 50))
	return New(p)
}

func TestEditUndoRedo(t *testing.T) {
	s := newSession()
	var got Change
	s.Subscribe(func(c Change) { got |= c })
	id := s.Project.Tracks[0].Clips[0].ID
	s.SelectClip(id)
	if err := s.Edit("Löschen", func(p *model.Project) error { return p.DeleteClip(id, false) }); err != nil {
		t.Fatal(err)
	}
	if got&ChangeProject == 0 || !s.Dirty || s.SelectedClip != "" || s.UndoLabel() != "Löschen" {
		t.Fatal("edit bookkeeping wrong")
	}
	s.Undo()
	if len(s.Project.Tracks[0].Clips) != 1 || !s.CanRedo() {
		t.Fatal("undo failed")
	}
	s.Redo()
	if len(s.Project.Tracks[0].Clips) != 0 {
		t.Fatal("redo failed")
	}
	err := s.Edit("kaputt", func(p *model.Project) error {
		p.Name = "verändert"
		return errors.New("nein")
	})
	if err == nil || s.Project.Name != "t" || s.UndoLabel() != "Löschen" {
		t.Fatal("failed edit must roll back and not be recorded")
	}
}

func TestPlaybackAdvance(t *testing.T) {
	s := newSession()
	s.SetPlaying(true)
	s.Advance(100 * time.Millisecond) // 2.5 frames at 25 fps
	if s.Playhead != 2 {
		t.Fatalf("playhead %d", s.Playhead)
	}
	s.Advance(20 * time.Millisecond) // carry completes frame 3
	if s.Playhead != 3 {
		t.Fatalf("playhead %d", s.Playhead)
	}
	s.Advance(10 * time.Second)
	if s.Playing || s.Playhead != 50 {
		t.Fatalf("should stop at end, ph=%d", s.Playhead)
	}
	s.SetPlaying(true)
	if s.Playhead != 0 {
		t.Fatal("play at end should rewind")
	}
	s.Loop = true
	s.SetIn(10)
	s.SetOut(20)
	s.Seek(18)
	s.SetPlaying(true)
	s.Advance(200 * time.Millisecond) // 5 frames: 18 -> 23 -> wraps to 13
	if s.Playhead != 13 || !s.Playing {
		t.Fatalf("loop in range, ph=%d", s.Playhead)
	}
}
