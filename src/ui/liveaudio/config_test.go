package liveaudio

import (
	"testing"
	"time"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/settings"
)

func restoreConfig(t *testing.T) {
	t.Cleanup(func() { Configure(DefaultConfig()) })
}

func TestConfigureLiveMonitor(t *testing.T) {
	restoreConfig(t)
	f := newFixture(t)
	f.s.SetPlaying(true)
	f.within(t, "Play must sound", f.sounding)

	Configure(Config{Player: "auto", Scrub: true, Muted: true, MasterGain: 1, CacheMB: 64})
	f.within(t, "muting the monitor silences the output", func() bool { return !f.sounding() })
	f.within(t, "meters keep running while muted", func() bool { m, _ := Levels(f.s); return m.RMS[0] > 0.2 })
	d := lookup(f.s)
	if d.e.Cache().Budget() != 64<<20 {
		t.Fatalf("audio cache budget %d", d.e.Cache().Budget())
	}

	Configure(Config{Player: "auto", Scrub: true, MasterGain: 0.5, Latency: 90 * time.Millisecond})
	f.within(t, "unmuted output sounds again", f.sounding)
	if g := d.e.MasterGain(); g != 0.5 {
		t.Fatalf("gain %v", g)
	}
	if Latency() != 90*time.Millisecond {
		t.Fatalf("latency %v, want the configured 90 ms", Latency())
	}
}

func TestConfigureSwitchesPlayer(t *testing.T) {
	restoreConfig(t)
	second := &device{}
	var asked []string
	saved := openerFor
	openerFor = func(name string) func() (audio.Output, error) {
		asked = append(asked, name)
		return func() (audio.Output, error) { return second, nil }
	}
	t.Cleanup(func() { openerFor = saved })
	f := newFixture(t)
	f.s.SetPlaying(true)
	f.within(t, "Play must sound", f.sounding)

	Configure(Config{Player: "auto", Scrub: true, MasterGain: 1})
	if len(asked) != 0 {
		t.Fatal("same player: no restart")
	}
	Configure(Config{Player: "paplay", Scrub: true, MasterGain: 1})
	if len(asked) != 1 || asked[0] != "paplay" {
		t.Fatalf("player switch asked for %v", asked)
	}
	f.within(t, "the old output is closed", func() bool { _, _, c := f.dev.state(); return c })
	f.within(t, "the new player gets the sound", func() bool { s, _, _ := second.state(); return s })
}

func TestFollowSettings(t *testing.T) {
	restoreConfig(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := settings.Load(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Reset() })
	stop := FollowSettings()
	defer stop()
	if c := CurrentConfig(); c != ConfigFrom(settings.Defaults()) || !scrubbing {
		t.Fatalf("defaults not applied: %+v", c)
	}
	_ = settings.Update(func(s *settings.Settings) {
		s.Audio.ScrubAudio = false
		s.Audio.LatencyMS = 70
		s.Audio.MasterGain = 0.7
		s.Media.AudioCacheMB = 256
	})
	c := CurrentConfig()
	if scrubbing || c.Latency != 70*time.Millisecond || c.MasterGain != 0.7 || c.CacheMB != 256 {
		t.Fatalf("settings not followed: %+v", c)
	}
	stop()
	_ = settings.Update(func(s *settings.Settings) { s.Audio.ScrubAudio = true })
	if scrubbing {
		t.Fatal("stop ends following")
	}
}
