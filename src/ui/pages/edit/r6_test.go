package edit

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
)

func TestInspectorShowsProjectFormat(t *testing.T) {
	f := newFixture(t, demoProject())
	f.s.Project.Settings.FPS = model.NTSC30
	f.s.Project.Settings.DropFrame = true
	f.s.SelectClip("")
	f.pg.insp.sync()
	var found bool
	for _, tx := range find[*canvas.Text](f.pg.insp.box) {
		if strings.Contains(tx.Text, "29,97 fps") && strings.Contains(tx.Text, "Drop-Frame") {
			found = true
		}
	}
	if !found {
		t.Error("the project section shows resolution, rate and drop-frame")
	}
	// no inline controls for resolution or rate any more: only the "Ändern…" button
	for _, s := range find[*kit.Select](f.pg.insp.box) {
		if strings.Contains(s.Selected, "fps") || strings.Contains(s.Selected, "×") {
			t.Errorf("duplicate format control %q", s.Selected)
		}
	}
	ran := false
	f.reg.Bind("test", command.Binding{ID: "file.projectSettings", Run: func() { ran = true }})
	f.reg.SetScopeActive("test", true)
	for _, b := range find[*widget.Button](f.pg.insp.box) {
		if b.Text == "Ändern…" {
			b.OnTapped()
		}
	}
	if !ran {
		t.Error("Ändern… opens the project settings command")
	}
}

func TestInspectorShot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT")
	}
	f := newFixture(t, demoProject())
	f.w.SetContent(shellFrame(f.pg.Content()))
	f.w.Resize(fyne.NewSize(1440, 900))
	f.pg.Activate()
	f.s.SelectClip("")
	f.q.drain()
	capture(t, f.w, "/tmp/fs-r6-inspector.png")
}
