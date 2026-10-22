package tip

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestTipShowsBelowButtonAndHidesOnTap(t *testing.T) {
	test.NewTempApp(t)
	l := NewLayer(true)
	tapped := false
	b := l.Button(theme.ContentUndoIcon(), "Rückgängig", func() { tapped = true })
	w := test.NewTempWindow(t, container.NewStack(container.NewHBox(b), l))
	w.Resize(fyne.NewSize(300, 100))

	b.SetTip("Rückgängig: Clip teilen")
	l.ShowTip(b)
	text, on := l.Showing()
	if !on || text != "Rückgängig: Clip teilen" {
		t.Fatalf("tip not shown: %q %v", text, on)
	}
	if l.bg.Position().Y < b.Size().Height {
		t.Fatal("tip should open below the button")
	}
	test.Tap(b)
	if _, on := l.Showing(); on || !tapped {
		t.Fatal("tap must hide the tip and run the action")
	}
}
