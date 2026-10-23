package kit

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
)

func TestParseDimension(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		err  string
	}{
		{"1920", 1920, ""},
		{" 16 ", 16, ""},
		{"16384", 16384, ""},
		{"", 0, "fehlt"},
		{"0", 0, "außerhalb"},
		{"15", 0, "außerhalb"},
		{"99999", 0, "außerhalb"},
		{"17x", 0, "keine gültige"},
		{"19,5", 0, "keine gültige"},
		{"-5", 0, "außerhalb"},
		{"99999999999999999999", 0, "zu groß"},
	} {
		got, err := ParseDimension(tc.in, "Breite")
		switch {
		case tc.err == "" && (err != nil || got != tc.want):
			t.Errorf("%q: got %d, %v", tc.in, got, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%q: want error %q, got %v", tc.in, tc.err, err)
		}
	}
}

func TestParseRateText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want model.Rate
		note string
		err  bool
	}{
		{"25", model.Rate25, "", false},
		{"23.976", model.NTSC24, "NTSC", false},
		{"23,976", model.NTSC24, "NTSC", false},
		{"29,97", model.NTSC30, "NTSC", false},
		{"59.94 fps", model.NTSC60, "NTSC", false},
		{"24000/1001", model.NTSC24, "", false},
		{"30000 / 1001", model.NTSC30, "", false},
		{"12.5", model.Rate{Num: 25, Den: 2}, "", false},
		{"", model.Rate{}, "", true},
		{"0", model.Rate{}, "", true},
		{"241", model.Rate{}, "", true},
		{"abc", model.Rate{}, "", true},
		{"1/0", model.Rate{}, "", true},
		{"0,5", model.Rate{}, "", true},
	} {
		got, note, err := ParseRateText(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%q: want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil || !got.Equal(tc.want) || (tc.note == "") != (note == "") || !strings.Contains(note, tc.note) {
			t.Errorf("%q: got %v note %q err %v", tc.in, got, note, err)
		}
	}
}

func TestOtherSideAndReadout(t *testing.T) {
	r := 16.0 / 9
	if OtherSide(1920, r, true) != 1080 || OtherSide(1080, r, false) != 1920 {
		t.Error("16:9 lock")
	}
	if OtherSide(1000, 1.0/1.0, true) != 1000 {
		t.Error("1:1")
	}
	if OtherSide(16384, 0.1, true) != model.MaxSize || OtherSide(20, 100, true) != model.MinSize {
		t.Error("results are clamped to the limits")
	}
	if AspectText(1920, 1080) != "16:9" || AspectText(1080, 1350) != "4:5" || AspectText(2048, 858) == "" {
		t.Error("aspect text")
	}
	if SizeReadout(1920, 1080) != "16:9 · 2,1 MP" {
		t.Errorf("readout %q", SizeReadout(1920, 1080))
	}
}

func newTestPicker(t *testing.T, f Format) (*FormatPicker, *int) {
	t.Helper()
	test.NewTempApp(t)
	n := 0
	p := NewFormatPicker(f, FormatOptions{}, func(Format, error) { n++ })
	return p, &n
}

func TestFormatPickerValidationAndCallback(t *testing.T) {
	p, n := newTestPicker(t, Format{Width: 1920, Height: 1080, FPS: model.Rate25})
	if f, err := p.Value(); err != nil || f.Width != 1920 || !f.FPS.Equal(model.Rate25) {
		t.Fatalf("initial %+v %v", f, err)
	}
	p.Res.Width.SetText("99999")
	if _, err := p.Value(); err == nil || !strings.Contains(p.Res.note.Text, "außerhalb") || !p.Res.note.Visible() {
		t.Errorf("99999 must show an error: %v / %q", err, p.Res.note.Text)
	}
	p.Res.Width.SetText("17x")
	if _, err := p.Value(); err == nil {
		t.Error("17x is invalid")
	}
	p.Res.Width.SetText("1280")
	p.Res.Height.SetText("720")
	if f, err := p.Value(); err != nil || f.Width != 1280 || p.Res.preset.Selected != "Video · HD 720p  1280 × 720" {
		t.Errorf("matching preset not selected: %+v %v %q", f, err, p.Res.preset.Selected)
	}
	if *n == 0 {
		t.Error("callback never ran")
	}
	p.Res.Width.SetText("1281")
	p.Res.Even = true
	p.Res.sync()
	if !strings.Contains(p.Res.note.Text, "1280 × 720") {
		t.Errorf("even hint %q", p.Res.note.Text)
	}
	if p.Res.preset.Selected != ResCustom {
		t.Errorf("odd size is custom: %q", p.Res.preset.Selected)
	}
}

func TestResolutionAspectLockAndSwap(t *testing.T) {
	p, _ := newTestPicker(t, Format{Width: 1920, Height: 1080, FPS: model.Rate25})
	r := p.Res
	r.SetLocked(true)
	if !r.Locked() {
		t.Fatal("lock on")
	}
	r.Width.SetText("1280")
	if r.Height.Text != "720" {
		t.Errorf("height follows width: %s", r.Height.Text)
	}
	r.Height.SetText("1080")
	if r.Width.Text != "1920" {
		t.Errorf("width follows height: %s", r.Width.Text)
	}
	r.Width.SetText("1")
	if r.Height.Text != "1080" {
		t.Errorf("an invalid width leaves the height alone: %s", r.Height.Text)
	}
	r.Width.SetText("1920")
	r.Swap()
	if r.Width.Text != "1080" || r.Height.Text != "1920" {
		t.Fatalf("swap %sx%s", r.Width.Text, r.Height.Text)
	}
	r.Width.SetText("540")
	if r.Height.Text != "960" {
		t.Errorf("swapped ratio 9:16 stays locked: %s", r.Height.Text)
	}
	// ratio preset: keeps the width, recomputes the height and locks
	r.SetLocked(false)
	r.Width.SetText("2000")
	r.Height.SetText("1000")
	r.ratioSel.SetSelected("4:5")
	if !r.Locked() || r.Width.Text != "2000" || r.Height.Text != "2500" {
		t.Errorf("4:5 preset: %sx%s locked %v", r.Width.Text, r.Height.Text, r.Locked())
	}
	r.ratioSel.SetSelected("2,39:1")
	if r.Height.Text != "837" {
		t.Errorf("2.39:1 height %s", r.Height.Text)
	}
	// unlocked entries are independent
	r.SetLocked(false)
	r.Width.SetText("800")
	if r.Height.Text != "837" {
		t.Error("unlocked width must not touch the height")
	}
	// preset choice fills both entries
	r.preset.SetSelected("Social · Quadrat 1:1  1080 × 1080")
	if r.Width.Text != "1080" || r.Height.Text != "1080" {
		t.Error("preset fills the entries")
	}
}

func TestRatePickerDropFrameAndSnap(t *testing.T) {
	p, _ := newTestPicker(t, Format{Width: 1920, Height: 1080, FPS: model.Rate25})
	rp := p.Rate
	if rp.DropEnabled() {
		t.Error("drop-frame must be off at 25 fps")
	}
	rp.Select().SetSelected(model.NTSC30.Label())
	if !rp.DropEnabled() {
		t.Error("drop-frame is possible at 29,97")
	}
	rp.drop.SetChecked(true)
	if f, err := p.Value(); err != nil || !f.DropFrame || !f.FPS.Equal(model.NTSC30) {
		t.Fatalf("%+v %v", f, err)
	}
	rp.Select().SetSelected(model.Rate30.Label())
	if rp.DropEnabled() || rp.drop.Checked {
		t.Error("30 fps has no drop-frame and the check is cleared")
	}
	if f, _ := p.Value(); f.DropFrame {
		t.Error("value must not carry drop-frame at 30 fps")
	}
	rp.Select().SetSelected(model.NTSC60.Label())
	if !rp.DropEnabled() {
		t.Error("59,94 allows drop-frame")
	}
	rp.Select().SetSelected(RateCustom)
	rp.CustomEntry().SetText("29,97")
	if f, err := p.Value(); err != nil || !f.FPS.Equal(model.NTSC30) || !rp.DropEnabled() {
		t.Fatalf("custom 29,97: %+v %v drop %v", f, err, rp.DropEnabled())
	}
	if !strings.Contains(rp.hint.Text, "30000/1001") || !strings.Contains(rp.hint.Text, "NTSC") {
		t.Errorf("snap hint %q", rp.hint.Text)
	}
	rp.CustomEntry().SetText("12,5")
	if f, err := p.Value(); err != nil || !f.FPS.Equal(model.Rate{Num: 25, Den: 2}) {
		t.Fatalf("12,5: %+v %v", f, err)
	}
	rp.CustomEntry().SetText("300")
	if _, err := p.Value(); err == nil || !rp.hint.Visible() {
		t.Error("300 fps is out of range")
	}
	// a project rate outside the presets opens as custom
	p.Set(Format{Width: 1920, Height: 1080, FPS: model.Rate{Num: 25, Den: 2}})
	if rp.sel.Selected != RateCustom || rp.custom.Text != "25/2" {
		t.Errorf("custom rate shown as %q %q", rp.sel.Selected, rp.custom.Text)
	}
}

func TestRatePickerSameEntry(t *testing.T) {
	test.NewTempApp(t)
	changed := 0
	rp := NewRatePicker(model.Rate{}, RateOptions{SameLabel: "Wie Projekt (25 fps)"}, func() { changed++ })
	if _, _, same, err := rp.Value(); !same || err != nil {
		t.Fatal("starts on the same entry")
	}
	rp.Select().SetSelected(model.Rate24.Label())
	if r, _, same, _ := rp.Value(); same || !r.Equal(model.Rate24) || changed == 0 {
		t.Fatal("24 chosen")
	}
	rp.SetSameLabel("Wie Projekt (30 fps)")
	rp.Select().SetSelected("Wie Projekt (30 fps)")
	if _, _, same, _ := rp.Value(); !same {
		t.Fatal("same chosen again")
	}
}

func TestSummarizeChange(t *testing.T) {
	p := model.NewProject("S")
	for i := 0; i < 14; i++ {
		c := model.NewClip(model.KindColor, "C", i*10, 10)
		if err := p.PlaceClip(p.Tracks[0].ID, c); err != nil {
			t.Fatal(err)
		}
	}
	p.Media = append(p.Media, model.MediaItem{ID: "m", Kind: model.KindVideo, Duration: time.Minute})

	st := p.Settings
	if s := SummarizeChange(p, st); s.Changed || !strings.Contains(s.Text(), "Keine Änderung") {
		t.Errorf("no change: %+v", s)
	}
	st.Width, st.Height = 3840, 2160
	s := SummarizeChange(p, st)
	if !s.Changed || s.Clips != 0 || s.Warning != "" || !strings.Contains(s.Text(), "1920×1080 → 3840×2160") {
		t.Errorf("size only: %+v", s)
	}
	st.FPS = model.NTSC30
	s = SummarizeChange(p, st)
	if s.Clips != 14 || !strings.Contains(s.Text(), "Bildrate 25 → 29,97 fps: 14 Clips werden auf das Bildraster angepasst") {
		t.Errorf("rate change: %q", s.Text())
	}
	if s.Rounded == 0 || !strings.Contains(s.Warning, "gerundet") {
		t.Errorf("25 → 29,97 puts cuts between frames: %+v", s)
	}
	st.FPS = model.Rate50
	s = SummarizeChange(p, st)
	if s.Rounded != 0 || s.Warning != "" || s.Clips != 14 {
		t.Errorf("25 → 50 is exact: %+v", s)
	}
	st.FPS = model.NTSC30
	st.DropFrame = true
	if s = SummarizeChange(p, st); !strings.Contains(s.Text(), "Drop-Frame") {
		t.Errorf("drop-frame line missing: %q", s.Text())
	}
	st.FPS = model.Rate30
	s = SummarizeChange(p, st)
	if strings.Contains(s.Text(), "Drop-Frame") {
		t.Errorf("30 fps cannot drop frames: %q", s.Text())
	}
}

func TestDropFrameChoiceSurvivesRateRoundTrip(t *testing.T) {
	test.NewTempApp(t)
	p := NewFormatPicker(Format{Width: 1920, Height: 1080, FPS: model.NTSC30, DropFrame: true}, FormatOptions{}, func(Format, error) {})
	p.Rate.Select().SetSelected(model.Rate25.Label())
	if f, _ := p.Value(); f.DropFrame {
		t.Fatal("25 fps has no drop-frame")
	}
	p.Rate.Select().SetSelected(model.NTSC30.Label())
	if f, _ := p.Value(); !f.DropFrame {
		t.Fatal("drop-frame must come back with 29,97")
	}
	p.Rate.SetDropFrame(false)
	p.Rate.Select().SetSelected(model.Rate25.Label())
	p.Rate.Select().SetSelected(model.NTSC30.Label())
	if f, _ := p.Value(); f.DropFrame {
		t.Fatal("an explicit off stays off")
	}
}
