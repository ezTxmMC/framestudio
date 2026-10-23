package statsui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
)

func sample() Snapshot {
	return Snapshot{HasData: true, Playing: true, FPS: 29.8, ProjectFPS: 30, Dropped: 12, DroppedPerSec: 0.4,
		Quality: "Halb", W: 640, H: 360, RenderMS: 8.2, CachePct: 87, Decoder: "hw", UIBusyPct: 4}
}

func TestLinesFormat(t *testing.T) {
	got := Lines(sample())
	want := [3]string{
		"Bild 29.8/30 fps  verworfen 12 (0.4/s)",
		"Qualität: Halb (auto)  640x360  Render 8.2 ms",
		"Cache 87%  Decoder: hw  UI 4%",
	}
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	s := sample()
	s.ProjectFPS, s.QualityPinned, s.CachePct, s.Decoder = 29.97, true, -1, "proxy"
	got = Lines(s)
	if got[0] != "Bild 29.8/29.97 fps  verworfen 12 (0.4/s)" || got[1] != "Qualität: Halb (fest)  640x360  Render 8.2 ms" ||
		got[2] != "Cache –  Decoder: proxy  UI 4%" {
		t.Fatalf("%q", got)
	}
	if l := Lines(Snapshot{}); l[0] == "" || l[2] == "" {
		t.Fatal("no data still shows a placeholder")
	}
}

func TestOverloadChipTimingAndRecovery(t *testing.T) {
	var c chips
	t0 := time.Unix(1000, 0)
	if o, _ := c.update(t0, Snapshot{Playing: true}); o {
		t.Fatal("no under-run, no chip")
	}
	if o, _ := c.update(t0, Snapshot{Playing: true, UnderRun: true}); !o {
		t.Fatal("chip must appear at once")
	}
	if o, _ := c.update(t0.Add(2*time.Second), Snapshot{Playing: true}); !o {
		t.Fatal("chip stays 3 s after recovery")
	}
	if o, _ := c.update(t0.Add(2500*time.Millisecond), Snapshot{Playing: true, UnderRun: true}); !o {
		t.Fatal("renewed under-run")
	}
	if o, _ := c.update(t0.Add(5*time.Second), Snapshot{Playing: true}); !o {
		t.Fatal("timer restarts at the last under-run")
	}
	if o, _ := c.update(t0.Add(5600*time.Millisecond), Snapshot{Playing: true}); o {
		t.Fatal("chip must hide 3 s after recovery")
	}
	if _, k := c.update(t0, Snapshot{Playing: true, KeyframesOnly: true}); !k {
		t.Fatal("keyframe chip")
	}
	if _, k := c.update(t0, Snapshot{Playing: false, KeyframesOnly: true}); k {
		t.Fatal("keyframe chip only while playing")
	}
}

func TestOverlayFollowsSettingsAndShowsChipWithoutPanel(t *testing.T) {
	test.NewApp()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = settings.Load()
	snap := sample()
	o := New(SourceFunc(func() Snapshot { return snap }))
	o.Resize(fyne.NewSize(800, 450))
	stop := o.FollowSettings()
	defer stop()

	if o.bg.Visible() {
		t.Fatal("panel is off by default")
	}
	snap.UnderRun = true
	o.poll()
	if !o.chipsUI[0].bg.Visible() || o.chipsUI[0].txt.Text != "Wiedergabe überlastet" {
		t.Fatal("the overload chip shows even with the panel off")
	}
	if o.bg.Visible() {
		t.Fatal("panel appeared on its own")
	}

	if err := settings.Update(func(s *settings.Settings) { s.Playback.Stats = true }); err != nil {
		t.Fatal(err)
	}
	if !o.bg.Visible() || o.lines[0].Text != "Bild 29.8/30 fps  verworfen 12 (0.4/s)" {
		t.Fatalf("panel not shown after enabling: %v %q", o.bg.Visible(), o.lines[0].Text)
	}
	// the chip sits at the right edge, the panel at the left
	if c := o.chipsUI[0].bg; c.Position().X+c.Size().Width > 800 || c.Position().X < 400 {
		t.Fatalf("chip at %v", c.Position())
	}
	if o.bg.Position().X != pad {
		t.Fatal("panel top left")
	}

	snap.FPS = 12.5
	o.poll()
	if o.lines[0].Text[:10] != "Bild 12.5/" {
		t.Fatalf("poll did not update: %q", o.lines[0].Text)
	}
	if err := settings.Update(func(s *settings.Settings) { s.Playback.Stats = false }); err != nil {
		t.Fatal(err)
	}
	if o.bg.Visible() || o.lines[1].Visible() {
		t.Fatal("panel must hide again")
	}
}

func TestPollPostsOnlyOnChange(t *testing.T) {
	test.NewApp()
	n := 0
	snap := sample()
	o := New(SourceFunc(func() Snapshot { n++; return snap }))
	o.setShown(true)
	n = 0
	o.mu.Lock()
	o.last = Lines(snap)
	o.mu.Unlock()
	before := o.lines[0].Text
	for i := 0; i < 5; i++ {
		o.poll()
	}
	if n != 5 {
		t.Fatalf("source read %d times", n)
	}
	if o.lines[0].Text != before {
		t.Fatal("unchanged stats rewrote the text")
	}
}
