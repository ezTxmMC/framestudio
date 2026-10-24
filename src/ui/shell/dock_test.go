package shell

import (
	"framestudio-proto/src/ui/page"
	"fyne.io/fyne/v2"
	"image/png"
	"os"
	"testing"
)

func TestPrimaryDockKeyboardAndLayout(t *testing.T) {
	a, q := newR3App(t)
	sh, w := buildShell(t, a, q)
	sh.Start([]string{saveSample(t, "Docking", 1920, 1080, 25, 100)})
	sh.showPageID(page.Color)
	before := len(a.Driver().AllWindows())
	sh.ws.Detach(page.PanelTimeline, "", false)
	if sh.ws.Window(page.PanelTimeline) != w || sh.mainDockSplit == nil || len(a.Driver().AllWindows()) != before {
		t.Fatal("primary panel did not dock in the main window")
	}
	s := sh.Active()
	s.Seek(25)
	oldCount := clipCount(s.Project)
	if !sh.combo(fyne.KeyK, fyne.KeyModifierControl) || clipCount(s.Project) <= oldCount {
		t.Fatal("Ctrl+K did not reach the docked timeline on the colour workspace")
	}
	sh.SetMainDockOffset(0.62)
	if err := sh.ws.SaveLayout("Docking test"); err != nil {
		t.Fatal(err)
	}
	sh.ws.CloseDetached()
	if sh.mainDockSplit != nil {
		t.Fatal("main dock not removed")
	}
	sh.ws.LoadLayout("Docking test")
	if sh.mainDockSplit == nil || sh.MainDockOffset() != 0.62 {
		t.Fatalf("main divider not restored: %v", sh.MainDockOffset())
	}
	if path := os.Getenv("FS_DOCK_PREVIEW"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, w.Canvas().Capture())
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	// The project remains valid after cutting from a dock on another workspace.
	if err := s.Project.Validate(); err != nil {
		t.Fatal(err)
	}
}
