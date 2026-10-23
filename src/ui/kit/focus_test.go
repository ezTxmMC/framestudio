package kit

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// A click on a check box, select or radio option leaves the keyboard to the page, so
// Space plays instead of toggling the last clicked "Fett".
func TestChoiceWidgetsGiveFocusBack(t *testing.T) {
	test.NewTempApp(t)
	plain := widget.NewCheck("Plain", nil)
	bold := 0
	ch := NewCheck("Fett", func(bool) { bold++ })
	sel := NewSelect([]string{"A", "B"}, nil)
	radio := NewRadioGroup([]string{"Links", "Mitte"}, nil)
	radio.Horizontal = true
	entry := widget.NewEntry()
	w := test.NewTempWindow(t, container.NewVBox(plain, ch, sel, radio, entry))
	w.Resize(fyne.NewSize(400, 300))
	test.Tap(plain)
	if w.Canvas().Focused() == nil {
		t.Skip("the test driver does not focus tapped check boxes")
	}
	w.Canvas().Unfocus()

	test.Tap(ch)
	if !ch.Checked || bold != 1 {
		t.Fatal("tap did not toggle")
	}
	if f := w.Canvas().Focused(); f != nil {
		t.Fatalf("check box kept the focus (%T)", f)
	}
	test.Tap(sel)
	if w.Canvas().Overlays().Top() == nil {
		t.Fatal("select did not open its menu")
	}
	w.Canvas().Overlays().Remove(w.Canvas().Overlays().Top()) // the option menu
	if f := w.Canvas().Focused(); f != nil {
		t.Fatalf("select kept the focus under its menu (%T)", f)
	}

	item := test.LaidOutObjects(radio)
	tapped := false
	for _, o := range item {
		if tp, ok := o.(fyne.Tappable); ok && o != fyne.CanvasObject(radio) {
			if _, isFocus := o.(fyne.Focusable); isFocus {
				tp.Tapped(&fyne.PointEvent{})
				tapped = true
				break
			}
		}
	}
	if !tapped || radio.Selected == "" {
		t.Fatal("radio option not tapped")
	}
	if f := w.Canvas().Focused(); f != nil {
		t.Fatalf("radio option kept the focus (%T)", f)
	}
	// A text field keeps its focus when a radio group changes programmatically.
	w.Canvas().Focus(entry)
	radio.SetSelected("Mitte")
	if w.Canvas().Focused() != entry {
		t.Fatal("radio change took the focus from a text field")
	}
}

func TestPassiveOverlay(t *testing.T) {
	r := widget.NewLabel("x")
	if IsPassiveOverlay(r) {
		t.Fatal("unmarked")
	}
	SetPassiveOverlay(r, true)
	if !IsPassiveOverlay(r) {
		t.Fatal("marked")
	}
	SetPassiveOverlay(r, false)
	if IsPassiveOverlay(r) || IsPassiveOverlay(nil) {
		t.Fatal("unmarked again")
	}
}
