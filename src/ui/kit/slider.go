package kit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Slider is a widget.Slider that gives the keyboard back when the pointer is done with
// it. A plain slider takes focus on a click and keeps it, so Space, J/K/L, the arrows
// and the edit keys would go to the slider (or nowhere) instead of the page until the
// user clicks somewhere else. Tab can still focus it for keyboard use.
type Slider struct {
	widget.Slider
}

// NewSlider returns a horizontal slider from min to max with step 1, like
// widget.NewSlider.
func NewSlider(min, max float64) *Slider {
	s := &Slider{}
	s.Min, s.Max, s.Step = min, max, 1
	s.Orientation = widget.Horizontal
	s.ExtendBaseWidget(s)
	return s
}

// Tapped sets the value like widget.Slider and then releases the keyboard focus.
func (s *Slider) Tapped(e *fyne.PointEvent) {
	s.Slider.Tapped(e)
	s.release()
}

// DragEnd ends the drag like widget.Slider and releases the keyboard focus.
func (s *Slider) DragEnd() {
	s.Slider.DragEnd()
	s.release()
}

func (s *Slider) release() {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	if c := app.Driver().CanvasForObject(s); c != nil && c.Focused() == fyne.Focusable(s) {
		c.Unfocus()
	}
}
