package liveaudio

import "framestudio-proto/src/session"

// Level is the recent output level of a track or the master bus, linear 0..1 per
// channel (L, R). Peak decays over ~300 ms; RMS covers ~50 ms.
type Level struct {
	Peak, RMS [2]float32
	Clipped   bool // true for ~1 s after a sample exceeded full scale
}

// Levels returns the live levels while s plays (zero when silent or stopped).
// Safe to call from the UI thread at display rate.
func Levels(s *session.Session) (master Level, tracks map[string]Level) {
	return Level{}, nil // TODO(audio engine agent)
}
