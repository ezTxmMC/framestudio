package shell

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"framestudio-proto/src/ui/page"
)

// While a dialog is open, keys do not reach the pages behind it.
func TestKeysIgnoredWhileDialogOpen(t *testing.T) {
	sh, w, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Dialog", 1920, 1080, 25, 100)})
	ed := made[page.Edit][0]
	d := dialog.NewInformation("Hinweis", "Test", w)
	d.Show()
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if len(ed.keys) != 0 || sh.Active().Playing {
		t.Fatalf("keys reached the page behind the dialog: %v, playing %v", ed.keys, sh.Active().Playing)
	}
	d.Hide()
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if len(ed.keys) != 1 {
		t.Fatal("keys must reach the page again once the dialog closed")
	}
}

// Ctrl+A and Esc on Medien do not touch the timeline's clips; Space does not start
// timeline playback there.
func TestMediaPageKeysLeaveTimelineAlone(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Medien", 1920, 1080, 25, 100)})
	edit(t, sh)
	s := sh.Active()
	sh.showPageID(page.Media)
	made[page.Media][0].handle = false
	typeShortcut(t, sh, &fyne.ShortcutSelectAll{})
	if len(s.Selected) != 0 {
		t.Fatalf("Ctrl+A on Medien selected clips %v", s.Selected)
	}
	sh.showPageID(page.Edit)
	typeShortcut(t, sh, &fyne.ShortcutSelectAll{})
	if len(s.Selected) != 2 {
		t.Fatal("Ctrl+A on Schnitt selects the clips")
	}
	sh.showPageID(page.Media)
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	sh.typedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if len(s.Selected) != 2 || s.Playing {
		t.Fatalf("Esc/Space on Medien acted on the timeline: %v, playing %v", s.Selected, s.Playing)
	}
}

// The playback keys work on Farbe, Ton and Export when the page leaves them.
func TestTransportKeysOnViewerPages(t *testing.T) {
	sh, _, made := newTestShell(t)
	sh.Start([]string{saveSample(t, "Transport", 1920, 1080, 25, 100)})
	s := sh.Active()
	for _, id := range []string{page.Color, page.Audio, page.Deliver} {
		sh.showPageID(id)
		made[id][0].handle = false
		s.Seek(40)
		for _, k := range []fyne.KeyName{fyne.KeyI, fyne.KeyRight, fyne.KeyO} {
			sh.typedKey(&fyne.KeyEvent{Name: k})
		}
		if s.In != 40 || s.Out != 41 {
			t.Fatalf("%s: In %d Out %d", id, s.In, s.Out)
		}
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
		if s.Playhead != s.Project.Duration() {
			t.Fatalf("%s: End went to %d", id, s.Playhead)
		}
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyHome})
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyL})
		if !s.Playing || s.Rate != 1 {
			t.Fatalf("%s: L did not play", id)
		}
		sh.typedKey(&fyne.KeyEvent{Name: fyne.KeyK})
		if s.Playing {
			t.Fatalf("%s: K did not stop", id)
		}
		s.ClearRange()
	}
}
