package edit

import (
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/page"
)

// TestScreenshotRound3 writes /tmp/fs-r3-edit*.png when FS_SCREENSHOT is set: the page
// with the viewer hidden (program on another monitor), the clean feed and program
// panels, the timeline panel, safe-area guides and the marker dialog.
func TestScreenshotRound3(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r3-edit*.png")
	}
	f := newFixture(t, demoProject())
	f.pg.viewer.Source = fakeFrame
	f.w.SetContent(shellFrame(f.pg.Content()))
	f.w.Resize(fyne.NewSize(1440, 900))
	f.pg.Activate()
	inter := clipNamed(t, f.s.Project, "Interview_Anna.mov")
	f.s.SelectClip(inter.ID)
	f.s.Seek(330)
	f.s.SetIn(250)
	f.s.SetOut(600)
	run(t, f, CmdHideViewer)
	f.q.drain()
	capture(t, f.w, "/tmp/fs-r3-edit-noviewer.png")

	run(t, f, CmdHideLibrary)
	run(t, f, CmdHideInspector)
	f.q.drain()
	capture(t, f.w, "/tmp/fs-r3-edit-timelineonly.png")
	run(t, f, CmdHideLibrary)
	run(t, f, CmdHideInspector)
	run(t, f, CmdHideViewer)

	// safe-area guides and the marker dialog on the full page
	_ = settings.Update(func(s *settings.Settings) { s.Playback.SafeArea = true })
	f.s.Seek(250)
	run(t, f, "marker.edit")
	f.q.drain()
	capture(t, f.w, "/tmp/fs-r3-edit-marker.png")
	tapOverlay(t, f.w, "Abbrechen")
	_ = settings.Update(func(s *settings.Settings) { s.Playback.SafeArea = false })

	run(t, f, "view.viewerFullscreen")
	f.q.drain()
	capture(t, f.w, "/tmp/fs-r3-edit-fullscreen.png")
	run(t, f, "view.viewerFullscreen")

	// detached panels on their own windows
	for _, p := range []struct {
		id   string
		w, h float32
	}{{page.PanelCleanFeed, 1280, 720}, {page.PanelProgram, 1100, 700}, {page.PanelTimeline, 1600, 420}} {
		w := test.NewTempWindow(t, nil)
		w.SetPadded(false)
		ctx := *f.pg.ctx
		ctx.Window = w
		pn := NewPanel(&ctx, p.id)
		if pn.pg.viewer != nil {
			pn.pg.viewer.Source = fakeFrame
			pn.pg.viewer.SetDeliver(f.q.post)
		}
		pn.pg.post = f.q.post
		w.SetContent(pn.Content())
		w.Resize(fyne.NewSize(p.w, p.h))
		f.s.Seek(120)
		f.q.drain()
		capture(t, w, "/tmp/fs-r3-edit-"+p.id+".png")
		pn.Close()
	}
}
