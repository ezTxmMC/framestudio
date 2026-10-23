package workgate

import "sync"

// Activity folds what the sessions are doing into the process-wide gate state: playing
// wins over scrubbing wins over idle. Several open projects can exist, but only the
// keys that really play count.
var activity struct {
	mu       sync.Mutex
	playing  map[any]bool
	scrubbed int
}

func publish() {
	st := Idle
	switch {
	case len(activity.playing) > 0:
		st = Playing
	case activity.scrubbed > 0:
		st = Scrubbing
	}
	Default().SetState(st)
}

// SetPlaying marks the session key as playing or stopped.
func SetPlaying(key any, on bool) {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if on {
		if activity.playing == nil {
			activity.playing = map[any]bool{}
		}
		activity.playing[key] = true
	} else {
		delete(activity.playing, key)
	}
	publish()
}

// SetScrubbing marks the start (true) or end (false) of a playhead drag. The gate
// returns to Idle 120 ms after the last drag ended.
func SetScrubbing(on bool) {
	activity.mu.Lock()
	defer activity.mu.Unlock()
	if on {
		activity.scrubbed++
	} else if activity.scrubbed > 0 {
		activity.scrubbed--
	}
	publish()
}

// ClockTick tells the gate that the playback clock is alive (see Impl.Tick).
func ClockTick() {
	g := Default()
	if t, ok := g.(interface{ Heartbeat() }); ok {
		t.Heartbeat()
	}
	// The watchdog drops to Idle after a UI stall; a resumed clock while a session still
	// plays puts the gate back to Playing.
	if g.State() == Idle {
		activity.mu.Lock()
		if len(activity.playing) > 0 {
			publish()
		}
		activity.mu.Unlock()
	}
}
