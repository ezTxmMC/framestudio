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

func TestEditCoalesced(t *testing.T) {
	s := newSession()
	id := s.Project.Tracks[0].Clips[0].ID
	for _, v := range []float64{0.2, 0.4, 0.6} {
		v := v
		_ = s.EditCoalesced("opacity:"+id, "Deckkraft", func(p *model.Project) error {
			c, _, _ := p.Clip(id)
			c.Opacity = v
			return nil
		})
	}
	s.Undo()
	if c, _, _ := s.Project.Clip(id); c.Opacity != 1 || s.CanUndo() {
		t.Fatalf("one undo should revert the whole drag, opacity=%v", c.Opacity)
	}
}

func TestFrameRateChangeMovesPlayheadAndRange(t *testing.T) {
	p := model.NewProject("fps")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 500))
	s := New(p)
	s.Playhead, s.In, s.Out = 50, 25, 75
	check := func(when string, ph, in, out int) {
		t.Helper()
		if s.Playhead != ph || s.In != in || s.Out != out {
			t.Fatalf("%s: playhead %d in %d out %d, want %d %d %d", when, s.Playhead, s.In, s.Out, ph, in, out)
		}
	}
	_ = s.Edit("Bildrate ändern", func(p *model.Project) error {
		p.Settings.FPS = model.Rate50
		p.Tracks[0].Clips[0].Length = 1000
		return nil
	})
	check("edit", 100, 50, 150)
	s.Undo()
	check("undo", 50, 25, 75)
	s.Redo()
	check("redo", 100, 50, 150)
}

func TestMultiSelection(t *testing.T) {
	s := newSession()
	_ = s.Edit("B", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "B", 50, 20))
	})
	a, b := s.Project.Tracks[0].Clips[0].ID, s.Project.Tracks[0].Clips[1].ID
	s.SelectClip(a)
	s.ToggleClip(b)
	if len(s.Selected) != 2 || s.SelectedClip != b || !s.IsSelected(a) {
		t.Fatalf("toggle add failed: %v primary %s", s.Selected, s.SelectedClip)
	}
	s.ToggleClip(b)
	if len(s.Selected) != 1 || s.SelectedClip != a {
		t.Fatal("toggle remove should fall back to remaining clip")
	}
	s.SelectAll()
	_ = s.Edit("del", func(p *model.Project) error { return p.DeleteClip(b, false) })
	if len(s.Selected) != 1 || s.Selected[0] != a {
		t.Fatalf("deleted clip must leave the selection: %v", s.Selected)
	}
	s.SelectClip("")
	if len(s.Selected) != 0 || s.SelectedClip != "" {
		t.Fatal("clear failed")
	}
}

func TestShuttle(t *testing.T) {
	s := newSession()
	s.Seek(25)
	s.Shuttle(1)
	s.Shuttle(1)
	if !s.Playing || s.Rate != 2 {
		t.Fatalf("LL should be 2x, got %v", s.Rate)
	}
	s.Advance(200 * time.Millisecond) // 2x 5 frames
	if s.Playhead != 35 {
		t.Fatalf("playhead %d", s.Playhead)
	}
	s.Shuttle(-1)
	if s.Rate != -1 {
		t.Fatal("J while playing forward should reverse at 1x")
	}
	s.Advance(10 * time.Second)
	if s.Playing || s.Playhead != 0 || s.Rate != 1 {
		t.Fatalf("reverse should stop at start, ph=%d", s.Playhead)
	}
	s.Shuttle(-1)
	if s.Playing {
		t.Fatal("cannot play backwards from the start without loop")
	}
	s.Seek(10)
	s.Shuttle(-1)
	s.Shuttle(0)
	if s.Playing || s.Rate != 1 {
		t.Fatal("K should stop and reset the rate")
	}
}

func TestBulkSelectionKeepsPicturePrimary(t *testing.T) {
	p := model.NewProject("x") // V1, V2, A1
	v := model.NewClip(model.KindColor, "Bild", 0, 50)
	a := model.NewClip(model.KindAudio, "Ton", 0, 50)
	_ = p.PlaceClip(p.Tracks[0].ID, v)
	_ = p.PlaceClip(p.Tracks[2].ID, a)
	s := New(p)
	s.SelectAll()
	if len(s.Selected) != 2 || s.SelectedClip != v.ID {
		t.Fatalf("select all: %v primary %s, want the picture clip", s.Selected, s.SelectedClip)
	}
	// a primary that is part of the group stays primary
	s.SelectClip(a.ID)
	s.SelectAll()
	if s.SelectedClip != a.ID || len(s.Selected) != 2 {
		t.Fatalf("current primary must stay: %s", s.SelectedClip)
	}
}

func TestLockingDropsClipsFromGroup(t *testing.T) {
	p := model.NewProject("x")
	v := model.NewClip(model.KindColor, "Bild", 0, 50)
	a := model.NewClip(model.KindAudio, "Ton", 0, 50)
	_ = p.PlaceClip(p.Tracks[0].ID, v)
	_ = p.PlaceClip(p.Tracks[2].ID, a)
	s := New(p)
	s.SelectClips([]string{v.ID, a.ID})
	_ = s.Edit("lock", func(p *model.Project) error { p.Tracks[2].Locked = true; return nil })
	if len(s.Selected) != 1 || s.SelectedClip != v.ID {
		t.Fatalf("locked clip must leave the group: %v primary %s", s.Selected, s.SelectedClip)
	}
	s.SelectClips([]string{v.ID, a.ID})
	if len(s.Selected) != 1 {
		t.Fatalf("a group skips locked clips: %v", s.Selected)
	}
	s.SelectClip(a.ID) // a single locked clip may be inspected
	if s.SelectedClip != a.ID {
		t.Fatal("single clip on a locked track must stay selectable")
	}
}

func TestSelectedTrackFollowsPrimary(t *testing.T) {
	p := model.NewProject("x")
	v := model.NewClip(model.KindColor, "Bild", 0, 50)
	_ = p.PlaceClip(p.Tracks[0].ID, v)
	s := New(p)
	s.SelectClip(v.ID)
	_ = s.Edit("move", func(p *model.Project) error { return p.MoveClips([]string{v.ID}, 10, 1) })
	if s.SelectedTrack != p.Tracks[1].ID {
		t.Fatal("selected track must follow the moved clip to V2")
	}
	s.Undo()
	if s.SelectedTrack != s.Project.Tracks[0].ID {
		t.Fatal("undo must bring the selected track back to V1")
	}
}

// After 24 h of playback in small ticks the playhead is exactly the frame of that time, for
// integer and NTSC rates: the sub-frame carry never loses or invents time.
func TestAdvanceNoDriftOver24Hours(t *testing.T) {
	for _, r := range []model.Rate{model.Rate25, model.NTSC24, model.NTSC30, model.NTSC60} {
		p := model.NewProject("long")
		p.Settings.FPS = r
		_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 6000000))
		s := New(p)
		s.SetPlaying(true)
		const tick = 16667 * time.Microsecond // a ~60 Hz clock
		total := time.Duration(0)
		for total+tick <= 24*time.Hour {
			s.Advance(tick)
			total += tick
		}
		if want := r.DurationToFrames(total); s.Playhead != want {
			t.Errorf("%v: playhead %d after %v, want %d", r, s.Playhead, total, want)
		}
	}
}

func TestSetSettingsIsOneUndoableEdit(t *testing.T) {
	s := newSession()
	s.Playhead, s.In, s.Out = 25, 0, 50
	st := s.Project.Settings
	st.FPS, st.Width, st.Height, st.DropFrame = model.NTSC30, 1280, 720, true
	if err := s.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	if s.Project.Rate() != model.NTSC30 || s.Project.Settings.Width != 1280 || !s.Project.DropFrame() || s.Playhead != 30 || s.Out != 60 {
		t.Fatalf("settings %+v playhead %d out %d", s.Project.Settings, s.Playhead, s.Out)
	}
	if got := s.Timecode(1800); got != "00:01:00;02" {
		t.Errorf("drop-frame timecode %q", got)
	}
	if s.SecondFrames() != 30 {
		t.Errorf("second step %d", s.SecondFrames())
	}
	s.Undo()
	if s.Project.Rate() != model.Rate25 || s.Playhead != 25 || s.Out != 50 || s.Project.Settings.Width != 1920 || s.CanUndo() {
		t.Fatalf("one undo must restore everything: %+v ph %d", s.Project.Settings, s.Playhead)
	}
	bad := st
	bad.FPS = model.Rate{Num: 500, Den: 1}
	if err := s.SetSettings(bad); err == nil || s.CanUndo() {
		t.Error("invalid rate must be rejected without an undo step")
	}
}
