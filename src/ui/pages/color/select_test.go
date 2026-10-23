package color

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
)

// click taps strip card i with the given modifiers, like a mouse press and release.
func (f *fixture) click(i int, mods fyne.KeyModifier) {
	c := f.p.cards[i]
	c.MouseDown(&desktop.MouseEvent{Modifier: mods})
	c.MouseUp(&desktop.MouseEvent{Modifier: mods})
	test.Tap(c)
}

func TestCtrlClickMultiSelectAndApply(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	// strip order: video, title, colour, image
	f.click(0, 0)
	f.p.primary.rows[0].slider.SetValue(0.3) // grade the video
	want := f.grade(f.video)
	if !f.p.primary.applySel.Disabled() {
		t.Fatal("apply to selection needs more than one selected clip")
	}
	f.click(2, fyne.KeyModifierControl)
	f.click(3, fyne.KeyModifierControl)
	if len(f.s.Selected) != 3 || f.s.SelectedClip != f.img {
		t.Fatalf("ctrl-click selection %v primary %s", f.s.Selected, f.s.SelectedClip)
	}
	if f.s.Playhead != 0 {
		t.Errorf("ctrl-click must not move the playhead (at %d)", f.s.Playhead)
	}
	if !f.p.cards[0].selected || f.p.cards[0].primary || !f.p.cards[3].primary || f.p.cards[1].selected {
		t.Error("card states do not match the selection")
	}
	f.click(3, fyne.KeyModifierControl) // toggles the image off again
	if len(f.s.Selected) != 2 || f.s.SelectedClip != f.colr {
		t.Fatalf("toggle off: %v primary %s", f.s.Selected, f.s.SelectedClip)
	}
	// The colour clip is graded now; copy the video's grade to it via the selection.
	f.click(0, fyne.KeyModifierControl) // off
	f.click(0, fyne.KeyModifierControl) // on again, video is the graded clip
	if f.s.SelectedClip != f.video || f.p.primary.applySel.Disabled() {
		t.Fatalf("primary %s, apply disabled %v", f.s.SelectedClip, f.p.primary.applySel.Disabled())
	}
	depth := len(undoLabels(f))
	test.Tap(f.p.primary.applySel)
	if f.grade(f.colr) != want || !f.grade(f.title).IsNeutral() || !f.grade(f.img).IsNeutral() {
		t.Fatalf("apply to selection: colour %+v title %+v image %+v", f.grade(f.colr), f.grade(f.title), f.grade(f.img))
	}
	if len(undoLabels(f)) != depth+1 || f.s.UndoLabel() != "Grade auf Auswahl anwenden" {
		t.Errorf("apply must be one undo step, got %q", f.s.UndoLabel())
	}
	f.s.Undo()
	if !f.grade(f.colr).IsNeutral() {
		t.Error("undo should restore the colour clip")
	}
}

// playTo plays forward until the playhead reaches frame n (25 fps).
func (f *fixture) playTo(n int) {
	f.s.Advance(time.Duration(n-f.s.Playhead) * 40 * time.Millisecond)
}

func undoLabels(f *fixture) []string {
	var out []string
	for f.s.CanUndo() {
		out = append(out, f.s.UndoLabel())
		f.s.Undo()
	}
	for range out {
		f.s.Redo()
	}
	return out
}

func TestShiftClickSelectsRange(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	f.click(1, 0)
	f.click(3, fyne.KeyModifierShift)
	if len(f.s.Selected) != 3 || f.s.SelectedClip != f.img || f.s.IsSelected(f.video) {
		t.Fatalf("range %v primary %s", f.s.Selected, f.s.SelectedClip)
	}
	f.click(0, fyne.KeyModifierShift) // from the image back to the start
	if len(f.s.Selected) != 4 || f.s.SelectedClip != f.video {
		t.Fatalf("backwards range %v primary %s", f.s.Selected, f.s.SelectedClip)
	}
	f.click(2, 0) // plain click: single selection again
	if len(f.s.Selected) != 1 || f.s.SelectedClip != f.colr || f.s.Playhead != 50 {
		t.Fatalf("plain click %v at %d", f.s.Selected, f.s.Playhead)
	}
}

func TestSelectionFollowsPlayheadOnlyWhenAutoSelected(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	if f.s.SelectedClip != f.video || f.p.autoID != f.video {
		t.Fatalf("auto-selected %q (auto %q), want the video under the playhead", f.s.SelectedClip, f.p.autoID)
	}
	// Paused seeks keep the selection.
	f.s.Seek(60)
	if f.s.SelectedClip != f.video {
		t.Fatalf("paused seek changed the selection to %q", f.s.SelectedClip)
	}
	// While playing, the auto selection follows the top-most picture under the playhead.
	f.s.Seek(10)
	f.s.SetPlaying(true)
	f.playTo(25)
	if f.s.SelectedClip != f.title {
		t.Fatalf("playing over the title selected %q", f.s.SelectedClip)
	}
	f.playTo(70)
	if f.s.SelectedClip != f.colr {
		t.Fatalf("playing over the colour clip selected %q", f.s.SelectedClip)
	}
	f.s.SetPlaying(false)

	// A manual pick stops the following, even during playback.
	f.click(3, 0)
	f.s.Seek(15)
	f.s.SetPlaying(true)
	f.playTo(25)
	if f.s.SelectedClip != f.img {
		t.Fatalf("manual selection followed the playhead to %q", f.s.SelectedClip)
	}
	f.s.SetPlaying(false)

	// A selection made elsewhere (e.g. the edit page) is manual too.
	f.s.Seek(0)
	f.s.SelectClip("")
	if f.p.autoID == "" {
		t.Fatal("clearing the selection should auto-select again")
	}
	f.s.SelectClip(f.colr)
	f.s.SetPlaying(true)
	f.playTo(25)
	if f.s.SelectedClip != f.colr {
		t.Fatalf("selection from another page followed the playhead to %q", f.s.SelectedClip)
	}
	f.s.SetPlaying(false)
}

func TestStripPosterRefreshesAfterRelink(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	imgCard := f.p.cards[3]
	if !imgCard.missing.Visible() || imgCard.thumb.Image != nil {
		t.Fatal("still with a missing file should show the missing icon")
	}
	path := filepath.Join(t.TempDir(), "logo.png")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewNRGBA(image.Rect(0, 0, 64, 36))
	for i := range src.Pix {
		src.Pix[i] = 0xC0
	}
	if err := png.Encode(out, src); err != nil {
		t.Fatal(err)
	}
	out.Close()
	c, _, _ := f.s.Project.Clip(f.img)
	mediaID := c.MediaID
	if err := f.s.Edit("Neu verknüpfen", func(p *model.Project) error {
		for i := range p.Media {
			if p.Media[i].ID == mediaID {
				p.Media[i].Path = path
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if imgCard.thumb.Image == nil || imgCard.missing.Visible() {
		t.Fatal("relinked still should show its poster")
	}
	if len(f.p.thumbs) != 1 {
		t.Errorf("stale posters kept: %d", len(f.p.thumbs))
	}
}

func TestThumbKeyFollowsSourceTimeAndPath(t *testing.T) {
	m := model.MediaItem{ID: "m1", Path: "/a.mov", Kind: model.KindVideo}
	c := model.Clip{Kind: model.KindVideo, SourceIn: 50}
	k1 := thumbKey(m, posterSec(c, 25))
	if posterSec(c, 25) != 2 {
		t.Errorf("poster at %v s, want 2", posterSec(c, 25))
	}
	c.SourceIn = 75
	if thumbKey(m, posterSec(c, 25)) == k1 {
		t.Error("trimmed clip must get a new poster key")
	}
	m.Path = "/b.mov"
	if thumbKey(m, 2) == k1 {
		t.Error("relinked media must get a new poster key")
	}
	if posterSec(model.Clip{Kind: model.KindImage, SourceIn: 10}, 25) != 0 {
		t.Error("stills use time 0")
	}
}
