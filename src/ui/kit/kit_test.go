package kit

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestParseFrames(t *testing.T) {
	cases := map[string]int{"2": 50, "1,5": 38, "48f": 48, "00:00:02:12": 62, "01:00": 25, "1:00:00": 1500}
	for in, want := range cases {
		if got, err := ParseFrames(in, 25); err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "00:00:00:30", "-1"} {
		if _, err := ParseFrames(bad, 25); err == nil {
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
