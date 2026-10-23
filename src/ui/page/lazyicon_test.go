package page

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// LazyIcon can be made before the app exists and works in widgets, which compare
// resources (a value with a func field would panic there).
func TestLazyIcon(t *testing.T) {
	ic := LazyIcon(theme.FolderIcon)
	test.NewTempApp(t)
	w := widget.NewIcon(ic)
	w.SetResource(ic)
	if ic.Name() != theme.FolderIcon().Name() || len(ic.Content()) == 0 {
		t.Fatal("lazy icon does not stand for the theme icon")
	}
	if th, ok := ic.(fyne.ThemedResource); !ok || th.ThemeColorName() != theme.ColorNameForeground {
		t.Fatal("theme colour lost")
	}
}
