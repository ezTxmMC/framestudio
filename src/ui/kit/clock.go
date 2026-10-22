package kit

import (
	"time"

	"framestudio-proto/src/session"
)

// DrivePlayback runs the session's playback clock at ~60 Hz whenever it is playing.
// Call once per session; the returned func stops it.
func DrivePlayback(s *session.Session) (stop func()) {
	running := false
	start := func() {
		if running || !s.Playing {
			return
		}
		running = true
		go func() {
			ticker := time.NewTicker(time.Second / 60)
			defer ticker.Stop()
			last := time.Now()
			for range ticker.C {
				done := false
				DoAndWait(func() {
					now := time.Now()
					s.Advance(now.Sub(last))
					last = now
					if !s.Playing {
						running = false
						done = true
					}
				})
				if done {
					return
				}
			}
		}()
	}
	unsub := s.Subscribe(func(c session.Change) {
		if c&session.ChangePlayback != 0 {
			start()
		}
	})
	return func() {
		unsub()
		s.SetPlaying(false)
	}
}
