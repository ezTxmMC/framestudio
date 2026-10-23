package kit

import (
	"testing"
	"time"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
)

func clockSession() *session.Session {
	p := model.NewProject("Takt")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, 3000))
	return session.New(p)
}

// A UI thread that stops draining for a while must neither block the clock goroutine
// nor lose time: after it resumes, the playhead catches up to the wall clock in one
// coalesced update.
func TestClockNeverWaitsForUIAndCatchesUp(t *testing.T) {
	q := UseUIQueue(t)
	s := clockSession()
	stop := DrivePlayback(s)
	defer stop()
	t0 := time.Now()
	s.SetPlaying(true)
	time.Sleep(400 * time.Millisecond) // UI thread "stalled": nothing drains
	q.mu.Lock()
	queued := len(q.fns)
	q.mu.Unlock()
	if queued != 1 {
		t.Fatalf("%d clock updates queued during the stall, want exactly 1 (coalesced)", queued)
	}
	q.Drain()
	want := int(time.Since(t0).Seconds() * s.FPS().Float())
	if d := s.Playhead - want; d < -3 || d > 3 {
		t.Fatalf("playhead %d after stall, want about %d", s.Playhead, want)
	}
	// keeps running with regular small updates
	f0 := s.Playhead
	q.Until(func() bool { return s.Playhead >= f0+9 }, time.Second)
	if s.Playhead < f0+9 {
		t.Fatalf("playhead stuck at %d", s.Playhead)
	}
	s.SetPlaying(false)
	q.Settle(40*time.Millisecond, time.Second)
	at := s.Playhead
	time.Sleep(100 * time.Millisecond)
	q.Drain()
	if s.Playhead != at {
		t.Fatal("playhead moved while paused")
	}
}

// Pausing and resuming quickly must not count the pause.
func TestClockPauseIsNotCounted(t *testing.T) {
	q := UseUIQueue(t)
	s := clockSession()
	stop := DrivePlayback(s)
	defer stop()
	s.SetPlaying(true)
	q.Until(func() bool { return false }, 100*time.Millisecond)
	s.SetPlaying(false)
	f := s.Playhead
	time.Sleep(300 * time.Millisecond)
	s.SetPlaying(true)
	q.Until(func() bool { return false }, 100*time.Millisecond)
	if d := s.Playhead - f; d > 6 {
		t.Fatalf("playhead jumped %d frames over a 300 ms pause", d)
	}
}
