package liveaudio

import (
	"time"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/settings"
)

// Config is the user's audio configuration (settings.Audio plus the PCM cache size).
type Config struct {
	Player     string        // "auto", "pw-play", "paplay" or "ffplay"
	Latency    time.Duration // output latency to compensate; 0 = the player's estimate
	Scrub      bool          // play snippets when the paused playhead moves
	Muted      bool          // silence the preview output (meters keep running)
	MasterGain float64       // linear monitor gain, 1 = unity (preview only)
	CacheMB    int           // memory for decoded audio; <= 0 = default
}

// DefaultConfig matches settings.Defaults.
func DefaultConfig() Config {
	return Config{Player: "auto", Scrub: true, MasterGain: 1}
}

// ConfigFrom maps the settings onto the live audio configuration.
func ConfigFrom(s settings.Settings) Config {
	return Config{
		Player:     s.Audio.Player,
		Latency:    time.Duration(max(s.Audio.LatencyMS, 0)) * time.Millisecond,
		Scrub:      s.Audio.ScrubAudio,
		Muted:      s.Audio.Muted,
		MasterGain: s.Audio.MasterGain,
		CacheMB:    s.Media.AudioCacheMB,
	}
}

// Configure applies c live: the running engine changes gain, mute, latency and cache
// size at once, and a different player restarts the output process cleanly (the engine
// and the decoded audio stay). Engines created later start with c. UI thread only.
func Configure(c Config) {
	if c.Player == "" {
		c.Player = "auto"
	}
	mu.Lock()
	old := cfg
	cfg = c
	e := engine
	mu.Unlock()
	scrubbing = c.Scrub
	if e == nil {
		return
	}
	applyMonitor(e, c)
	if c.Player != old.Player {
		e.SetOpener(openerFor(c.Player))
	}
}

// CurrentConfig returns the configuration in effect.
func CurrentConfig() Config {
	mu.Lock()
	defer mu.Unlock()
	return cfg
}

// FollowSettings applies the current settings now and after every change until stop is
// called. UI thread only (settings.Update notifies on the caller's thread).
func FollowSettings() (stop func()) {
	Configure(ConfigFrom(settings.Get()))
	return settings.Subscribe(func(s settings.Settings) {
		if c := ConfigFrom(s); c != CurrentConfig() {
			Configure(c)
		}
	})
}

// applyMonitor sets the engine's monitor controls from c (everything but the player).
func applyMonitor(e *audio.Engine, c Config) {
	e.SetMasterGain(c.MasterGain)
	e.SetMuted(c.Muted)
	e.SetLatency(c.Latency)
	if c.CacheMB > 0 {
		e.Cache().SetBudget(int64(c.CacheMB) << 20)
	}
}
