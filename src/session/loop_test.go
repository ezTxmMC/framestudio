package session

import (
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func loopSession(r model.Rate) *Session {
	p := model.NewProject("loop")
	p.Settings.FPS = r
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 6000000))
	return New(p)
}

// A loop over In/Out stays exact for 24 h of 60 Hz ticks, forward and at the shuttle speeds.
func TestLoopNoDriftOver24Hours(t *testing.T) {
	const tick = 16667 * time.Microsecond
	for _, r := range []model.Rate{model.NTSC30, model.NTSC60} {
		for _, speed := range []float64{1, 2, 4, 8} {
			s := loopSession(r)
			s.Loop = true
			s.In, s.Out = 100, 1100
			s.Seek(100)
			s.SetPlaying(true)
			s.Rate = speed
			total := time.Duration(0)
			for total+tick <= 24*time.Hour {
				s.Advance(tick)
				total += tick
			}
			// the carry is exact, so the frame count is floor(total*speed*rate)
			frames := int(float64(total) * speed * float64(r.Num) / (float64(r.Den) * float64(time.Second)))
			want := 100 + frames%1000
			// the float reference is only accurate to ~1e-16 relative; allow a frame at the boundary
			if d := s.Playhead - want; d < -1 || d > 1 {
				t.Errorf("%v at %vx: playhead %d, want %d", r, speed, s.Playhead, want)
			}
		}
	}
}

// Reverse playback in a loop stays inside [In, Out) for any step, also when a tick lands
// exactly a whole number of loop lengths before the start.
func TestReverseLoopStaysInRange(t *testing.T) {
	s := loopSession(model.Rate25)
	s.Loop = true
	s.In, s.Out = 10, 20
	s.Seek(10)
	s.Shuttle(-1)
	s.Advance(400 * time.Millisecond) // 10 frames = one loop length
	if s.Playhead < 10 || s.Playhead >= 20 {
		t.Fatalf("playhead %d outside the loop [10,20)", s.Playhead)
	}
	for i := 0; i < 100; i++ {
		s.Advance(40 * time.Millisecond * time.Duration(1+i%25))
		if s.Playhead < 10 || s.Playhead >= 20 {
			t.Fatalf("step %d: playhead %d outside the loop", i, s.Playhead)
		}
	}
}
