package chrome

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestMiddleEllipsis(t *testing.T) {
	long := "Ein sehr langer Projektname für den Schnitt"
	full := fyne.MeasureText(long, 12, fyne.TextStyle{}).Width
	if middleEllipsis(long, 12, full+1) != long {
		t.Fatal("fits")
	}
	s := middleEllipsis(long, 12, full/2)
	if fyne.MeasureText(s, 12, fyne.TextStyle{}).Width > full/2 || s == long {
		t.Fatalf("%q", s)
	}
}
