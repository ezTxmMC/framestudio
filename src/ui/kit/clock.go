package kit

import (
	"sync/atomic"
	"time"

	"framestudio-proto/src/session"
)

// DrivePlayback runs the session's playback clock whenever it is playing. Call once
// per session; the returned func stops it.
//
// The clock lives on its own goroutine (60 Hz, monotonic time) and never waits for the
// UI thread. Elapsed time accumulates atomically; one coalesced update at a time is
// posted to the UI thread, where it advances the session by everything accumulated
// since the last update. A busy UI thread therefore delays the on-screen playhead but
// never loses time, never builds a backlog, and never slows the clock or the picture
// scheduler behind it.
func DrivePlayback(s *session.Session) (stop func()) {
	var cur *clockRun // UI thread only
	wasPlaying := false
	start := func() {
		if (cur != nil && !cur.done.Load()) || !s.Playing {
			return
		}
		r := &clockRun{}
		cur = r
		go r.loop(s)
	}
	unsub := s.Subscribe(func(c session.Change) {
		if c&session.ChangePlayback == 0 {
			return
		}
		if !s.Playing {
			wasPlaying = false
			return
		}
		if !wasPlaying {
			wasPlaying = true
			if cur != nil && !cur.done.Load() {
				cur.pending.Store(0) // quick pause/resume: do not count the pause
			}
		}
		start()
	})
	return func() {
		unsub()
		s.SetPlaying(false)
		if cur != nil {
			cur.done.Store(true)
		}
	}
}

// clockRun is one play run: a ticker goroutine and the state it shares with the UI.
type clockRun struct {
	pending atomic.Int64 // nanoseconds not yet applied to the session
	queued  atomic.Bool  // an update is waiting for the UI thread
	done    atomic.Bool
}

func (r *clockRun) loop(s *session.Session) {
	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()
	last := time.Now()
	for range ticker.C {
		if r.done.Load() {
			return
		}
		now := time.Now()
		r.pending.Add(int64(now.Sub(last)))
		last = now
		if r.queued.CompareAndSwap(false, true) {
			Do(func() { r.apply(s) })
		}
	}
}

// apply runs on the UI thread.
func (r *clockRun) apply(s *session.Session) {
	r.queued.Store(false)
	if r.done.Load() {
		return
	}
	if dt := time.Duration(r.pending.Swap(0)); dt > 0 {
		s.Advance(dt)
	}
	if !s.Playing {
		r.done.Store(true)
	}
}
