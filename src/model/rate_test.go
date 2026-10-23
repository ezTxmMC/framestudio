package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	ok := map[string]Rate{
		"25": Rate25, "25 fps": Rate25, "23.976": NTSC24, "23,976": NTSC24, "29,97": NTSC30,
		"59.94": NTSC60, "24000/1001": NTSC24, "30000/1001": NTSC30, "48000/2002": NTSC24,
		"12.5": {25, 2}, "23.98": {1199, 50}, "240": {240, 1}, "1": {1, 1}, "119.88": NTSC120,
	}
	for in, want := range ok {
		got, err := ParseRate(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "0", "-5", "241", "0.5", "1/0", "0/5", "24/", "NaN", "Inf"} {
		if r, err := ParseRate(bad); err == nil {
			t.Errorf("%q should fail, got %v", bad, r)
		}
	}
}

func TestRateStrings(t *testing.T) {
	for r, want := range map[Rate]string{Rate25: "25", NTSC24: "23.976", NTSC30: "29.97", NTSC60: "59.94", {25, 2}: "12.5"} {
		if r.String() != want {
			t.Errorf("%v: %q, want %q", r, r.String(), want)
		}
	}
	if NTSC24.Label() != "23,976 fps" || NTSC30.Fraction() != "30000/1001" || Rate25.Fraction() != "25" {
		t.Error(NTSC24.Label(), NTSC30.Fraction(), Rate25.Fraction())
	}
	for r, want := range map[Rate]int{NTSC24: 24, NTSC30: 30, NTSC60: 60, Rate25: 25, Rate50: 50, {25, 2}: 13} {
		if r.Nominal() != want {
			t.Errorf("%v nominal %d, want %d", r, r.Nominal(), want)
		}
	}
	if !NTSC30.IsNTSC() || Rate30.IsNTSC() || !NTSC30.CanDropFrame() || !NTSC60.CanDropFrame() || NTSC24.CanDropFrame() || Rate30.CanDropFrame() {
		t.Error("NTSC flags")
	}
}

func TestRateJSON(t *testing.T) {
	b, _ := json.Marshal(Settings{Width: 1920, Height: 1080, FPS: Rate25})
	if !strings.Contains(string(b), `"fps":25`) {
		t.Errorf("integer rate must stay a plain number: %s", b)
	}
	b, _ = json.Marshal(Settings{Width: 1920, Height: 1080, FPS: NTSC30, DropFrame: true})
	if !strings.Contains(string(b), `"fps":{"num":30000,"den":1001}`) || !strings.Contains(string(b), `"dropFrame":true`) {
		t.Errorf("%s", b)
	}
	for in, want := range map[string]Rate{`25`: Rate25, `29.97`: NTSC30, `{"num":48000,"den":2002}`: NTSC24, `"24000/1001"`: NTSC24, `0`: {}, `null`: {}} {
		var r Rate
		if err := json.Unmarshal([]byte(in), &r); err != nil || r != want {
			t.Errorf("%s: %v %v", in, r, err)
		}
	}
	var st Settings
	if err := json.Unmarshal(b, &st); err != nil || st.FPS != NTSC30 || !st.DropFrame {
		t.Errorf("round trip: %+v %v", st, err)
	}
	// media items of old files stored the source rate as a float
	var m MediaItem
	if err := json.Unmarshal([]byte(`{"id":"a","fps":29.97}`), &m); err != nil || m.FPS != NTSC30 {
		t.Errorf("old media fps: %v %v", m.FPS, err)
	}
	b, _ = json.Marshal(MediaItem{ID: "a"})
	if strings.Contains(string(b), "fps") {
		t.Errorf("unset media rate must be omitted: %s", b)
	}
}

func TestExactTimeMath(t *testing.T) {
	rates := append([]Rate{{25, 2}, {2398, 100}, Rate{1, 1}, Rate{240, 1}}, RatePresets...)
	for _, r := range rates {
		for _, f := range []int{0, 1, 2, 3, 29, 30, 31, 1000, 17982, 107892, 2589407, 24 * 3600 * 240} {
			d := r.FramesToDuration(f)
			if got := r.DurationToFrames(d); got != f {
				t.Fatalf("%v frame %d -> %v -> %d", r, f, d, got)
			}
			if f > 0 && r.DurationToFrames(d-1) != f-1 {
				t.Fatalf("%v: one ns before frame %d must still be frame %d", r, f, f-1)
			}
			if r.FrameAt(r.FrameStart(f)) != f {
				t.Fatalf("%v FrameAt/FrameStart %d", r, f)
			}
		}
	}
	// 24 hours of frames: no drift. 24h at 29.97 is 2589410.58 frames.
	day := 24 * time.Hour
	if got := NTSC30.DurationToFrames(day); got != 2589410 {
		t.Errorf("24h at 29.97: %d frames, want 2589410", got)
	}
	if got := NTSC24.DurationToFrames(day); got != 2071528 {
		t.Errorf("24h at 23.976: %d frames, want 2071528", got)
	}
	if got := NTSC60.DurationToFrames(day); got != 5178821 {
		t.Errorf("24h at 59.94: %d frames, want 5178821", got)
	}
	if got := Rate25.DurationToFrames(day); got != 25*86400 {
		t.Errorf("24h at 25: %d", got)
	}
	if NTSC30.FramesToDuration(30000) != 1001*time.Second {
		t.Error("30000 frames at 29.97 are exactly 1001 s")
	}
	if NTSC30.FramesToDuration(-30000) != -1001*time.Second {
		t.Error("negative frames")
	}
}

func TestSamplesAt(t *testing.T) {
	if got := NTSC24.SamplesAt(1, 48000); got != 2002 {
		t.Errorf("one frame at 23.976 = %d samples, want 2002", got)
	}
	if got := NTSC30.SamplesAt(30000, 48000); got != 48048000 {
		t.Errorf("30000 frames at 29.97 = %d samples", got)
	}
	if got := NTSC60.SamplesAt(60000, 48000); got != 48048000 {
		t.Errorf("60000 frames at 59.94 = %d samples", got)
	}
	for _, r := range RatePresets {
		prev := int64(0)
		for f := 1; f < 5000; f++ {
			s := r.SamplesAt(f, 48000)
			// Each frame is within one sample of its exact length, errors never accumulate.
			if d := s - prev; d < 48000*int64(r.Den)/int64(r.Num) || d > 48000*int64(r.Den)/int64(r.Num)+2 {
				t.Fatalf("%v frame %d: %d samples", r, f, d)
			}
			prev = s
			if r.FrameAtSample(s, 48000) != f {
				t.Fatalf("%v: FrameAtSample(SamplesAt(%d)) = %d", r, f, r.FrameAtSample(s, 48000))
			}
		}
	}
}

func TestConvertFrames(t *testing.T) {
	if ConvertFrames(25, Rate25, Rate50) != 50 || ConvertFrames(50, Rate50, Rate25) != 25 {
		t.Error("25<->50")
	}
	if got := ConvertFrames(24000, NTSC24, Rate24); got != 24024 {
		t.Errorf("24000 frames at 23.976 (1001 s) are 24024 frames at 24: %d", got)
	}
	if got := ConvertFrames(24024, Rate24, NTSC24); got != 24000 {
		t.Errorf("%d", got)
	}
}

func TestDropFrameTimecode(t *testing.T) {
	cases := []struct {
		f    int
		r    Rate
		want string
	}{
		{0, NTSC30, "00:00:00;00"},
		{1799, NTSC30, "00:00:59;29"},
		{1800, NTSC30, "00:01:00;02"},
		{17982, NTSC30, "00:10:00;00"},
		{17981, NTSC30, "00:09:59;29"},
		{107892, NTSC30, "01:00:00;00"},
		{3600, NTSC30, "00:02:00;04"},
		{3597, NTSC30, "00:01:59;29"},
		{3598, NTSC30, "00:02:00;02"},
		{3600, NTSC60, "00:01:00;04"},
		{35964, NTSC60, "00:10:00;00"},
		{0, NTSC24, "00:00:00:00"},
		{24, NTSC24, "00:00:01:00"},
		{1800, NTSC30, "00:01:00:00"}, // non-drop below
	}
	for i, c := range cases {
		drop := i < len(cases)-1
		if got := Timecode(c.f, c.r, drop); got != c.want {
			t.Errorf("Timecode(%d, %v, %v) = %q, want %q", c.f, c.r, drop, got, c.want)
		}
	}
	if Timecode(1800, NTSC24, true) != "00:01:15:00" {
		t.Error("drop-frame must be ignored at 23.976")
	}
	if Timecode(-5, Rate25, false) != "00:00:00:00" {
		t.Error("negative frames show as zero")
	}
	if Timecode(25*3661+3, Rate25, false) != "01:01:01:03" {
		t.Error("plain timecode")
	}
}

func TestTimecodeRoundTrip(t *testing.T) {
	for _, r := range RatePresets {
		for _, drop := range []bool{false, true} {
			if drop && !r.CanDropFrame() {
				continue
			}
			prevLabel := -1
			for f := 0; f < 130000; f++ {
				tc := Timecode(f, r, drop)
				got, err := ParseTimecode(tc, r, drop)
				if err != nil || got != f {
					t.Fatalf("%v drop=%v: frame %d -> %q -> %d, %v", r, drop, f, tc, got, err)
				}
				l := LabelFrame(f, r, drop)
				if l <= prevLabel {
					t.Fatalf("labels must increase: frame %d label %d after %d", f, l, prevLabel)
				}
				prevLabel = l
				if back, ok := FrameFromLabel(l, r, drop); !ok || back != f {
					t.Fatalf("%v drop=%v: label %d -> %d %v, want %d", r, drop, l, back, ok, f)
				}
			}
		}
	}
	// drop-frame follows the wall clock: 24 h of frames show 24:00:00;00 (+- one frame)
	f := NTSC30.DurationToFrames(24 * time.Hour)
	if tc := Timecode(f, NTSC30, true); tc != "24:00:00;02" || Timecode(24*107892, NTSC30, true) != "24:00:00;00" {
		t.Errorf("24 h in drop-frame shows %s", tc)
	}
}

func TestParseTimecodeErrors(t *testing.T) {
	for _, bad := range []string{"", "1:2:3", "00:00:00:30", "00:61:00:00", "a:b:c:d", "00:01:00;00", "00:01:00;01", "-1:00:00:00"} {
		if _, err := ParseTimecode(bad, NTSC30, true); err == nil {
			t.Errorf("%q should fail in drop-frame at 29.97", bad)
		}
	}
	if f, err := ParseTimecode("00:10:00;00", NTSC30, true); err != nil || f != 17982 {
		t.Errorf("tenth minute keeps frame 0: %d %v", f, err)
	}
	if f, err := ParseTimecode("00:00:01.05", Rate25, false); err != nil || f != 30 {
		t.Errorf("dot separator: %d %v", f, err)
	}
	if _, err := ParseTimecode("00:00:00:24", NTSC24, false); err == nil {
		t.Error("frame 24 does not exist at 23.976 (nominal 24)")
	}
}

func TestSettingsRescaleAndValidate(t *testing.T) {
	p := NewProject("x")
	_ = p.PlaceClip(p.Tracks[0].ID, NewClip(KindColor, "A", 25, 50))
	p.AddMarker(100, "m")
	st := p.Settings
	st.FPS = NTSC30
	st.DropFrame = true
	if err := p.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	c := p.Tracks[0].Clips[0]
	if c.Start != 30 || c.Length != 60 || p.Markers[0].Frame != 120 || !p.DropFrame() {
		t.Errorf("rescale 25 -> 29.97: clip %d+%d marker %d", c.Start, c.Length, p.Markers[0].Frame)
	}
	st.FPS = Rate25
	if err := p.SetSettings(st); err != nil || p.Settings.DropFrame {
		t.Errorf("drop-frame must switch off at 25 fps: %v %v", err, p.Settings.DropFrame)
	}
	for _, bad := range []Settings{
		{Width: 1920, Height: 1080, FPS: Rate{241, 1}}, {Width: 1920, Height: 1080, FPS: Rate{1, 2}},
		{Width: 15, Height: 1080, FPS: Rate25}, {Width: 1920, Height: 16385, FPS: Rate25}, {Width: 1920, Height: 1080},
	} {
		if err := p.SetSettings(bad); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
	if err := (&Project{Settings: Settings{Width: 16384, Height: 16, FPS: Rate{240, 1}}}).Validate(); err != nil {
		t.Errorf("limits are inclusive: %v", err)
	}
}

func TestMatchFirstMedia(t *testing.T) {
	p := NewProject("x")
	if _, err := p.MatchFirstMedia(); err == nil {
		t.Error("empty pool")
	}
	p.AddMedia(MediaItem{Name: "a", Kind: KindAudio})
	p.AddMedia(MediaItem{Name: "v", Kind: KindVideo, Width: 3840, Height: 2160, FPS: NTSC30})
	st, err := p.MatchFirstMedia()
	if err != nil || st.Width != 3840 || st.FPS != NTSC30 {
		t.Errorf("%+v %v", st, err)
	}
}

func TestOldProjectJSON(t *testing.T) {
	var p Project
	old := `{"version":1,"name":"Alt","settings":{"width":1280,"height":720,"fps":30},"tracks":[],"media":[{"id":"m","fps":24}]}`
	if err := json.Unmarshal([]byte(old), &p); err != nil || p.Settings.FPS != Rate30 || p.Media[0].FPS != Rate24 {
		t.Fatalf("%+v %v", p, err)
	}
}
