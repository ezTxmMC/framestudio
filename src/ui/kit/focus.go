package kit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Check, Select and RadioGroup give the keyboard back after a click, like Slider. Fyne's
// own widgets take the keyboard focus when clicked and keep it, so Space would toggle the
// last clicked check box (an undo step "Schriftstil ändern" instead of playback) and the
// single-key shortcuts (S, J/K/L, I/O, M) would do nothing until the user clicked an
// empty area. Tab can still focus them for keyboard use.

// Check is a widget.Check that releases the keyboard focus after a click.
type Check struct {
	widget.Check
}

// NewCheck returns a check box like widget.NewCheck.
func NewCheck(label string, changed func(bool)) *Check {
	c := &Check{}
	c.Text = label
	c.OnChanged = changed
	c.ExtendBaseWidget(c)
	return c
}

// Tapped toggles like widget.Check and then releases the keyboard focus.
func (c *Check) Tapped(e *fyne.PointEvent) {
	c.Check.Tapped(e)
	releaseFocus(c)
}

// Select is a widget.Select that does not keep the keyboard focus from a click (the
// pop-up menu it opens handles its own keys).
type Select struct {
	widget.Select
	tapping bool
}

// NewSelect returns a select like widget.NewSelect.
func NewSelect(options []string, changed func(string)) *Select {
	s := &Select{}
	s.Options = options
	s.OnChanged = changed
	s.PlaceHolder = "(Auswählen)"
	s.ExtendBaseWidget(s)
	return s
}

// Tapped opens the menu like widget.Select without keeping the keyboard focus. Fyne
// focuses the select before it opens the menu, and the menu's overlay then hides the
// main layer's focus from Unfocus, so the focus is given back as soon as it arrives
// (FocusGained).
func (s *Select) Tapped(e *fyne.PointEvent) {
	s.tapping = true
	s.Select.Tapped(e)
	s.tapping = false
}

// FocusGained is widget.Select's, but a focus that comes from a click is released at once.
func (s *Select) FocusGained() {
	s.Select.FocusGained()
	if s.tapping {
		releaseFocus(s)
	}
}

// NewRadioGroup returns a widget.RadioGroup whose options release the keyboard focus
// after a click. The options are internal widgets of Fyne, so the release happens in
// OnChanged: set OnChanged through this constructor, not afterwards.
func NewRadioGroup(options []string, changed func(string)) *widget.RadioGroup {
	r := widget.NewRadioGroup(options, nil)
	r.OnChanged = func(s string) {
		if changed != nil {
			changed(s)
		}
		releaseFocusIn(r)
	}
	return r
}

// releaseFocus unfocuses o's canvas when o has the focus.
func releaseFocus(o fyne.Focusable) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	obj, ok := o.(fyne.CanvasObject)
	if !ok {
		return
	}
	if c := app.Driver().CanvasForObject(obj); c != nil && c.Focused() == o {
		c.Unfocus()
	}
}

// releaseFocusIn unfocuses the canvas of o unless a text field has the focus (a radio
// option, an internal widget of o, takes it on a click).
func releaseFocusIn(o fyne.CanvasObject) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	c := app.Driver().CanvasForObject(o)
	if c == nil {
		return
	}
	f := c.Focused()
	if f == nil {
		return
	}
	if _, text := f.(interface{ SelectedText() string }); text {
		return
	}
	c.Unfocus()
}
