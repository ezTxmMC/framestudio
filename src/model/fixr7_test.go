package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRateChangeKeepsPartialFades(t *testing.T) {
	p := NewProject("x")
	c := NewClip(KindColor, "A", 0, 50)
	c.FadeIn, c.FadeInFrom = 100, 0.5 // split inside a fade
	c.FadeOut, c.FadeOutTo = 100, 0.5
	if err := p.PlaceClip(p.Tracks[0].ID, c); err != nil {
		t.Fatal(err)
	}
	st := p.Settings
	st.FPS = Rate50
	if err := p.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	got := p.Tracks[0].Clips[0]
	if got.Length != 100 || got.FadeIn != 200 || got.FadeOut != 200 {
		t.Fatalf("len=%d fadeIn=%d fadeOut=%d, want 100/200/200", got.Length, got.FadeIn, got.FadeOut)
	}
}

func TestRateTermsBounded(t *testing.T) {
	if r, err := ParseRate("99999999999/3000000001"); err == nil {
		t.Errorf("huge terms accepted: %v", r)
	}
	var r Rate
	if err := json.Unmarshal([]byte(`{"num":99999999999,"den":3000000001}`), &r); err == nil {
		t.Error("huge JSON terms accepted")
	}
	if !(Rate{30000, 1001}).Valid() {
		t.Error("NTSC valid")
	}
	if d := (Rate{25, 1}).FramesToDuration(1000); d != 40*time.Second {
		t.Errorf("%v", d)
	}
}

func TestSecondsToFramesSaturates(t *testing.T) {
	for _, sec := range []float64{1e30, -1e30} {
		if n := Rate25.SecondsToFrames(sec); (sec > 0) != (n > 0) || n == -9223372036854775808 {
			t.Errorf("%g -> %d", sec, n)
		}
	}
}

func TestFractionsSnapToNTSC(t *testing.T) {
	for in, want := range map[string]Rate{"2997/100": NTSC30, "5994/100": NTSC60, "23976/1000": NTSC24, "24000/1001": NTSC24, "25/1": Rate25} {
		got, err := ParseRate(in)
		if err != nil || got != want {
			t.Errorf("%s -> %v %v, want %v", in, got, err, want)
		}
	}
	if r, _ := ParseRate("12345/1000"); r != (Rate{2469, 200}) {
		t.Errorf("non-NTSC fraction stays exact: %v", r)
	}
}
