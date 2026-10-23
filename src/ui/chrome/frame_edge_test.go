package chrome

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// The grips must lie below the content so controls at the window edge keep their clicks.
func TestFrameGripsBelowContent(t *testing.T) {
	w := test.NewWindow(nil)
	content := canvas.NewRectangle(nil)
	f := NewFrame(w, newFake(), content)
	defer f.Close()
	f.Resize(fyne.NewSize(300, 200))
	objs := test.WidgetRenderer(f).Objects()
	ci, gi := -1, -1
	for i, o := range objs {
		if o == fyne.CanvasObject(content) {
			ci = i
		}
		if _, ok := o.(*grip); ok {
			gi = i
		}
	}
	if ci < 0 || gi < 0 || gi > ci {
		t.Fatalf("grips must come before content in z-order (grip %d, content %d)", gi, ci)
	}
	if got := content.Position(); got.X != Inset || got.Y != Inset {
		t.Fatalf("content not inset: %v", got)
	}
	if content.Size().Width != 300-2*Inset {
		t.Fatalf("content width %v", content.Size().Width)
	}
}
