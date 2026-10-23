package edit

import (
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/ui/liveaudio"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/timeline"
)

// shellFrame stands in for the shell chrome (project tabs on top, page bar below) so
// page screenshots have the proportions of the real window.
func shellFrame(content fyne.CanvasObject) fyne.CanvasObject {
	bar := func(h float32) fyne.CanvasObject {
		r := canvas.NewRectangle(studiotheme.Gap)
		r.SetMinSize(fyne.NewSize(1, h))
		return r
	}
	return container.NewBorder(bar(40), bar(58), nil, nil, content)
}

// TestScreenshotRound2 captures the page as it sits in a 1440x900 and a 1280x800
// window: shuttle rate and meter in the transport, tools, multi-selection inspector.
func TestScreenshotRound2(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r2-edit*.png")
	}
	for _, sz := range []struct {
		w, h float32
		name string
	}{{1440, 900, ""}, {1280, 800, "-1280"}} {
		f := newFixture(t, demoProject())
		f.pg.viewer.Source = fakeFrame
		f.w.SetContent(shellFrame(f.pg.Content()))
		f.w.Resize(fyne.NewSize(sz.w, sz.h))
		inter := clipNamed(t, f.s.Project, "Interview_Anna.mov")
		f.s.SelectClip(inter.ID)
		f.s.Seek(80)
		f.s.SetIn(25)
		f.s.SetOut(600)
		press(f, fyne.KeyL)
		press(f, fyne.KeyL)
		f.pg.setTool(timeline.ToolRipple)
		f.pg.transport.Meter().SetLevel(liveaudio.Level{Peak: [2]float32{0.62, 0.81}, RMS: [2]float32{0.3, 0.4}})
		f.q.drain()
		capture(t, f.w, "/tmp/fs-r2-edit"+sz.name+".png")

		var ids []string
		for _, n := range []string{"Haupttitel", "Strand_Drohne.mp4", "Interview_Anna.mov", "Musik_Sommer.wav"} {
			for ti := range f.s.Project.Tracks {
				for _, c := range f.s.Project.Tracks[ti].Clips {
					if c.Name == n {
						ids = append(ids, c.ID)
					}
				}
			}
		}
		f.s.SelectClips(ids)
		f.s.SetPlaying(false)
		f.q.drain()
		capture(t, f.w, "/tmp/fs-r2-edit-multi"+sz.name+".png")
	}
}
