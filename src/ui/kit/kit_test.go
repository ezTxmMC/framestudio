package kit

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
)

func TestParseFrames(t *testing.T) {
	cases := map[string]int{"2": 50, "1,5": 38, "48f": 48, "00:00:02:12": 62, "01:00": 25, "1:00:00": 1500}
	for in, want := range cases {
		if got, err := ParseFrames(in, model.Rate25, false); err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "00:00:00:30", "-1"} {
		if _, err := ParseFrames(bad, model.Rate25, false); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

// A click on a kit.Slider sets the value but leaves the keyboard to the page.
func TestSliderGivesFocusBack(t *testing.T) {
	test.NewTempApp(t)
	s := NewSlider(0, 10)
	plain := widget.NewSlider(0, 10)
	w := test.NewTempWindow(t, container.NewVBox(s, plain))
	w.Resize(fyne.NewSize(300, 100))
	test.Tap(plain)
	if w.Canvas().Focused() == nil {
		t.Skip("the test driver does not focus tapped sliders")
	}
	w.Canvas().Unfocus()
	test.TapAt(s, fyne.NewPos(s.Size().Width*0.8, s.Size().Height/2))
	if s.Value <= 0 {
		t.Fatalf("tap did not set the value: %v", s.Value)
	}
	if f := w.Canvas().Focused(); f != nil {
		t.Fatalf("slider kept the focus (%T)", f)
	}
	s.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(10, 5)}})
	w.Canvas().Focus(s)
	s.DragEnd()
	if w.Canvas().Focused() != nil {
		t.Fatal("drag end must release the focus")
	}
}

func TestParseFramesNTSCAndDropFrame(t *testing.T) {
	cases := []struct {
		in   string
		rate model.Rate
		drop bool
		want int
	}{
		{"00:01:00;02", model.NTSC30, true, 1800},
		{"00:01:00:02", model.NTSC30, true, 1800}, // either separator
		{"00:10:00;00", model.NTSC30, true, 17982},
		{"1:00", model.NTSC30, false, 30},
		{"00:00:01:00", model.NTSC24, false, 24},
		{"2", model.NTSC30, false, 60},
		{"1,5", model.NTSC24, false, 36},
		{"48f", model.NTSC24, true, 48},
	}
	for _, c := range cases {
		if got, err := ParseFrames(c.in, c.rate, c.drop); err != nil || got != c.want {
			t.Errorf("%q at %v drop=%v: %d, %v; want %d", c.in, c.rate, c.drop, got, err, c.want)
		}
	}
	if _, err := ParseFrames("00:01:00;00", model.NTSC30, true); err == nil {
		t.Error("00:01:00;00 does not exist in drop-frame")
	}
	if _, err := ParseFrames("00:00:00:24", model.NTSC24, false); err == nil {
		t.Error("frame 24 does not exist at 23.976")
	}
}
