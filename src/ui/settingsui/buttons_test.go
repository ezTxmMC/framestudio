package settingsui

import (
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/winchrome"
)

func TestWindowButtonsOptionsAndCurrentStyle(t *testing.T) {
	winchrome.UseFake(t)
	f := newFixture(t, "display")
	fld := f.field("Fensterknöpfe")
	sel, ok := fld.input.(*widget.Select)
	if !ok {
		t.Fatalf("input is %T", fld.input)
	}
	want := []string{"Automatisch (passend zum System)", "macOS", "Windows", "Linux"}
	if len(sel.Options) != 4 {
		t.Fatalf("options %v", sel.Options)
	}
	for i := range want {
		if sel.Options[i] != want[i] {
			t.Fatalf("options %v", sel.Options)
		}
	}
	if sel.Selected != want[0] {
		t.Fatalf("default selection %q", sel.Selected)
	}
	cur := "Aktuell: " + styleNames[chrome.ResolveStyle("auto", runtime.GOOS)]
	if !strings.Contains(fld.help, cur) {
		t.Fatalf("help %q lacks %q", fld.help, cur)
	}
	sel.SetSelected("Windows")
	if f.saved().Display.WindowButtons != "win" || !strings.Contains(fld.help, "Aktuell: Windows") {
		t.Fatalf("after Windows: %q %q", f.saved().Display.WindowButtons, fld.help)
	}
	_ = settings.Update(func(s *settings.Settings) { s.Display.WindowButtons = "linux" })
	if !strings.Contains(fld.help, "Aktuell: Linux") || sel.Selected != "Linux" {
		t.Fatalf("after linux: %q %q", fld.help, sel.Selected)
	}
}
