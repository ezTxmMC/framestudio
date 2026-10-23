package session

import (
	"encoding/json"
	"math/rand"
	"testing"

	"framestudio-proto/src/model"
)

func asJSON(t *testing.T, p *model.Project) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Changing the rate through any chain of rates never creates overlaps, and one undo per
// change restores the project, the playhead and In/Out exactly (not only to a frame near them).
func TestRateChangeChainUndoIsExact(t *testing.T) {
	rates := []model.Rate{model.Rate25, model.NTSC24, model.NTSC30, model.NTSC60, model.Rate50, model.Rate120, model.Rate24, model.Rate30}
	rnd := rand.New(rand.NewSource(7))
	for trial := 0; trial < 40; trial++ {
		p := model.NewProject("r")
		p.Settings.FPS = rates[rnd.Intn(len(rates))]
		for ti := 0; ti < 2; ti++ {
			at := rnd.Intn(5)
			for c := 0; c < 6; c++ {
				length := 1 + rnd.Intn(40)
				if err := p.PlaceClip(p.Tracks[ti].ID, model.NewClip(model.KindColor, "C", at, length)); err != nil {
					t.Fatal(err)
				}
				at += length + rnd.Intn(3) // tight and touching clips
			}
		}
		p.AddMarker(rnd.Intn(200), "m")
		s := New(p)
		s.Playhead, s.In, s.Out = rnd.Intn(150)+1, rnd.Intn(50), 50+rnd.Intn(100)
		var states []string
		var times [][3]int
		for step := 0; step < 5; step++ {
			states = append(states, asJSON(t, s.Project))
			times = append(times, [3]int{s.Playhead, s.In, s.Out})
			st := s.Project.Settings
			st.FPS = rates[rnd.Intn(len(rates))]
			if err := s.SetSettings(st); err != nil {
				t.Fatal(err)
			}
			if err := s.Project.Validate(); err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
		}
		for step := 4; step >= 0; step-- {
			s.Undo()
			if got := asJSON(t, s.Project); got != states[step] {
				t.Fatalf("trial %d: undo to step %d does not restore the project", trial, step)
			}
			if got := [3]int{s.Playhead, s.In, s.Out}; got != times[step] {
				t.Fatalf("trial %d: undo to step %d: playhead/in/out %v, want %v", trial, step, got, times[step])
			}
		}
	}
}

// A playhead that was moved after the rate change is converted, not reset.
func TestUndoRateChangeKeepsMovedPlayhead(t *testing.T) {
	s := newSession()
	s.Playhead = 10
	st := s.Project.Settings
	st.FPS = model.Rate50
	if err := s.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	s.Playhead = 80 // 1.6 s
	s.Undo()
	if s.Playhead != 40 {
		t.Errorf("playhead %d, want 40", s.Playhead)
	}
}
