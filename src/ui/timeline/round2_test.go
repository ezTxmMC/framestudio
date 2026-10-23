package timeline

import (
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// posterFake records filmstrip requests and paints each frame in its own shade.
type posterFake struct {
	fakeSource
	mu   sync.Mutex
	secs []float64
}

func (f *posterFake) Poster(m model.MediaItem, sec float64, w, h int) (image.Image, error) {
	f.mu.Lock()
	f.secs = append(f.secs, sec)
	f.mu.Unlock()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	v := uint8(int(sec*40) % 200)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sky := uint8(float64(y) / float64(h) * 90)
			img.SetNRGBA(x, y, color.NRGBA{40 + v/3 + sky/2, 70 + sky/2, 110 + v/4, 255})
		}
	}
	return img, nil
}

func (f *posterFake) requests() []float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]float64(nil), f.secs...)
}

func (f *fixture) press(mods fyne.KeyModifier) {
	f.tl.MouseDown(&desktop.MouseEvent{Modifier: mods})
}

func (f *fixture) clipID(track string, i int) string { return f.track(track).Clips[i].ID }

func (f *fixture) place(track string, c model.Clip) string {
	tr := f.track(track)
	if err := f.s.Edit("x", func(p *model.Project) error { return p.PlaceClip(tr.ID, c) }); err != nil {
		panic(err)
	}
	return c.ID
}

func TestClipsInRect(t *testing.T) {
	p := geomProject() // V1: A 0..100, B 100..150; V2; A1; A2
	a1 := p.TrackIndices(model.TrackAudio)[0]
	music := model.NewClip(model.KindAudio, "M", 40, 30)
	_ = p.PlaceClip(p.Tracks[a1].ID, music)
	g := newGeom(p, 1000, 400, 1, 0, 0)
	v1, au := g.rows[g.rowOfTrack(0)], g.rows[g.rowOfTrack(a1)]
	A, B := p.Tracks[0].Clips[0].ID, p.Tracks[0].Clips[1].ID

	// a band inside V1 over the end of A and the start of B
	got := g.clipsInRect(p, fyne.NewPos(g.frameX(90), v1.y+10), fyne.NewPos(g.frameX(110), v1.y+20))
	if !slices.Equal(got, []string{A, B}) {
		t.Fatalf("band over the cut: %v", got)
	}
	// dragged up-left from A1 to V1: corners in any order, touching counts
	got = g.clipsInRect(p, fyne.NewPos(g.frameX(60), au.y+au.h/2), fyne.NewPos(g.frameX(20), v1.y+v1.h-5))
	if !slices.Equal(got, []string{A, music.ID}) {
		t.Fatalf("band across tracks: %v", got)
	}
	// empty space selects nothing; locked tracks are skipped
	if got := g.clipsInRect(p, fyne.NewPos(g.frameX(200), v1.y), fyne.NewPos(g.frameX(220), au.y+au.h)); len(got) != 0 {
		t.Fatalf("empty band: %v", got)
	}
	p.Tracks[0].Locked = true
	if got := g.clipsInRect(p, fyne.NewPos(g.frameX(0), v1.y), fyne.NewPos(g.frameX(150), v1.y+v1.h)); len(got) != 0 {
		t.Fatalf("locked track clips selected: %v", got)
	}
}

func TestMarqueeSelects(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	audio := f.place("A1", model.NewClip(model.KindAudio, "Ton", 20, 40))
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)

	from := fyne.NewPos(f.x(170), f.rowMid("V2"))
	f.press(0)
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(120), f.rowMid("V1"))}, Dragged: fyne.NewDelta(f.x(120)-from.X, f.rowMid("V1")-from.Y)})
	if f.tl.drag == nil || f.tl.drag.mode != dragMarquee {
		t.Fatal("drag on empty lane must start a marquee")
	}
	if !slices.Equal(f.s.Selected, []string{B}) {
		t.Fatalf("live marquee selection %v", f.s.Selected)
	}
	// rubber band is drawn
	if !hasBand(f.tl) {
		t.Fatal("no rubber band drawn")
	}
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(30), f.rowMid("A1"))}, Dragged: fyne.NewDelta(-50, 50)})
	f.tl.DragEnd()
	if !slices.Equal(f.s.Selected, []string{A, B, audio}) {
		t.Fatalf("marquee selection %v", f.s.Selected)
	}

	// Ctrl adds to the selection; Esc during the drag restores it
	f.s.SelectClip(audio)
	f.press(fyne.KeyModifierControl)
	f.drag(fyne.NewPos(f.x(160), f.rowMid("V1")), fyne.NewPos(f.x(140), f.rowMid("V1")))
	if !slices.Equal(f.s.Selected, []string{audio, B}) {
		t.Fatalf("ctrl marquee %v", f.s.Selected)
	}
	f.press(0)
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(10), f.rowMid("V1"))}, Dragged: fyne.NewDelta(f.x(10)-f.x(200), 0)})
	if len(f.s.Selected) != 2 || f.s.Selected[0] != A {
		t.Fatalf("plain marquee replaces the selection: %v", f.s.Selected)
	}
	if !f.tl.Escape() {
		t.Fatal("Esc must cancel the marquee")
	}
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(5), f.rowMid("V1"))}, Dragged: fyne.NewDelta(-5, 0)})
	f.tl.DragEnd()
	if !slices.Equal(f.s.Selected, []string{audio, B}) {
		t.Fatalf("Esc must restore the selection: %v", f.s.Selected)
	}
	// Esc when idle clears the selection
	if !f.tl.Escape() || len(f.s.Selected) != 0 || f.s.SelectedClip != "" {
		t.Fatalf("Esc must clear the selection: %v", f.s.Selected)
	}
	if f.tl.Escape() {
		t.Fatal("nothing left to cancel")
	}
}

func hasBand(tl *Timeline) bool {
	for _, o := range tl.r.lanes.Objects {
		if r, ok := o.(*canvas.Rectangle); ok && r.StrokeColor == alpha(studiotheme.Steel, 0xE0) {
			return true
		}
	}
	return false
}

func TestCtrlAndShiftClick(t *testing.T) {
	f := setup(t, 1000, 300)
	C := f.place("V1", model.NewClip(model.KindColor, "C", 150, 30))
	D := f.place("V1", model.NewClip(model.KindColor, "D", 200, 30))
	other := f.place("V2", model.NewClip(model.KindTitle, "T", 10, 30))
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)

	f.press(0)
	f.tap(fyne.NewPos(f.x(50), f.rowMid("V1")))
	f.press(fyne.KeyModifierControl)
	f.tap(fyne.NewPos(f.x(20), f.rowMid("V2")))
	if !slices.Equal(f.s.Selected, []string{A, other}) || f.s.SelectedClip != other {
		t.Fatalf("ctrl click adds: %v primary %q", f.s.Selected, f.s.SelectedClip)
	}
	f.tap(fyne.NewPos(f.x(50), f.rowMid("V1")))
	if !slices.Equal(f.s.Selected, []string{other}) {
		t.Fatalf("ctrl click toggles off: %v", f.s.Selected)
	}
	// shift: range on the primary's track
	f.press(0)
	f.tap(fyne.NewPos(f.x(120), f.rowMid("V1"))) // B
	f.press(fyne.KeyModifierShift)
	f.tap(fyne.NewPos(f.x(210), f.rowMid("V1"))) // D
	if !slices.Equal(f.s.Selected, []string{B, C, D}) || f.s.SelectedClip != D {
		t.Fatalf("shift range: %v primary %q", f.s.Selected, f.s.SelectedClip)
	}
	// shift onto another track only adds that clip
	f.tap(fyne.NewPos(f.x(20), f.rowMid("V2")))
	if !slices.Equal(f.s.Selected, []string{B, C, D, other}) {
		t.Fatalf("shift other track: %v", f.s.Selected)
	}
	// ctrl click on empty lane keeps the selection, plain click clears it
	f.press(fyne.KeyModifierControl)
	f.tap(fyne.NewPos(f.x(240), f.rowMid("V2")))
	if len(f.s.Selected) != 4 || f.s.Playhead != 240 {
		t.Fatal("ctrl click on empty lane must keep the selection")
	}
	f.press(0)
	f.tap(fyne.NewPos(f.x(240), f.rowMid("V2")))
	if len(f.s.Selected) != 0 {
		t.Fatal("click on empty lane clears the selection")
	}
	if got := rangeOnTrack(f.s.Project, D, B); !slices.Equal(got, []string{D, C, B}) {
		t.Fatalf("reverse range %v", got)
	}
}

func TestSelectionOutlines(t *testing.T) {
	f := setup(t, 1000, 300)
	f.s.SelectClips([]string{f.clipID("V1", 0), f.clipID("V1", 1)})
	var primary, secondary int
	for _, o := range f.tl.r.lanes.Objects {
		if r, ok := o.(*canvas.Rectangle); ok {
			switch {
			case r.StrokeColor == white && r.StrokeWidth == 2:
				primary++
			case r.StrokeColor == alpha(white, 0xC0):
				secondary++
			}
		}
	}
	if primary != 1 || secondary != 1 {
		t.Fatalf("outlines: primary %d, others %d", primary, secondary)
	}
}

func TestGroupMoveAcrossTracks(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	audio := f.place("A1", model.NewClip(model.KindAudio, "Ton", 0, 100))
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)
	f.s.SelectClips([]string{A, B, audio})

	// grab B (selected) and drag it 30 frames right and up onto V2
	f.press(0)
	from, to := fyne.NewPos(f.x(120), f.rowMid("V1")), fyne.NewPos(f.x(150), f.rowMid("V2"))
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
	if d := f.tl.drag; d == nil || d.mode != dragMove || len(d.group) != 3 || d.dFrames != 30 || d.dTrack != 1 {
		t.Fatalf("group drag state %+v", f.tl.drag)
	}
	ghosts := 0
	for _, o := range f.tl.r.lanes.Objects {
		if r, ok := o.(*canvas.Rectangle); ok && r.StrokeColor == studiotheme.Steel && r.StrokeWidth == 2 {
			ghosts++
		}
	}
	if ghosts != 3 {
		t.Fatalf("want a ghost per selected clip, got %d", ghosts)
	}
	f.tl.DragEnd()

	v2 := f.track("V2").Clips
	if len(v2) != 2 || v2[0].ID != A || v2[0].Start != 30 || v2[1].ID != B || v2[1].Start != 130 {
		t.Fatalf("video clips not moved to V2: %+v", v2)
	}
	if c, ti, _ := f.s.Project.Clip(audio); f.s.Project.Tracks[ti].Name != "A1" || c.Start != 30 {
		t.Fatalf("audio must move in time but stay on its track: %+v on %d", c, ti)
	}
	if f.s.UndoLabel() != "Clips verschieben" || len(f.s.Selected) != 3 {
		t.Fatalf("undo %q, selection %v", f.s.UndoLabel(), f.s.Selected)
	}
	f.s.Undo()
	if len(f.track("V1").Clips) != 2 || f.track("A1").Clips[0].Start != 0 {
		t.Fatal("group move must be one undo step")
	}
}

func TestGroupMoveSnapsGrabbedClip(t *testing.T) {
	f := setup(t, 1000, 300)
	f.s.Seek(300)
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)
	f.s.SelectClips([]string{A, B})
	// grab A and drop its end 2 frames short of the playhead: it snaps to 300
	f.press(0)
	f.drag(fyne.NewPos(f.x(50), f.rowMid("V1")), fyne.NewPos(f.x(248), f.rowMid("V1")))
	a, _, _ := f.s.Project.Clip(A)
	b, _, _ := f.s.Project.Clip(B)
	if a.Start != 200 || b.Start != 300 {
		t.Fatalf("snapped group: A@%d B@%d, want 200/300", a.Start, b.Start)
	}
	// the group never goes below frame 0
	f.drag(fyne.NewPos(f.x(250), f.rowMid("V1")), fyne.NewPos(headerW+1, f.rowMid("V1")))
	a, _, _ = f.s.Project.Clip(A)
	if a.Start != 0 {
		t.Fatalf("group start %d, want clamped to 0", a.Start)
	}
}

func TestGroupMoveLockedTracks(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	audio := f.place("A1", model.NewClip(model.KindAudio, "Ton", 0, 100))
	A := f.clipID("V1", 0)
	f.tap(f.buttonPos("A1", btnLock))
	before := f.s.Project.Clone()
	undo := f.s.UndoLabel()

	// a member on a locked track: rejected with a message, nothing moves
	f.s.SelectClips([]string{A, audio})
	f.press(0)
	f.drag(fyne.NewPos(f.x(50), f.rowMid("V1")), fyne.NewPos(f.x(80), f.rowMid("V1")))
	if len(f.errs) != 1 || !errors.Is(f.errs[0], model.ErrLocked) {
		t.Fatalf("want one ErrLocked, got %v", f.errs)
	}
	if f.errs[0].Error() != "Clips verschieben nicht möglich: Die Spur ist gesperrt. Schloss im Spurkopf lösen und erneut versuchen." {
		t.Fatalf("message %q", f.errs[0])
	}
	if f.s.UndoLabel() != undo || f.track("V1").Clips[0] != before.Tracks[0].Clips[0] {
		t.Fatal("rejected group move changed the project")
	}

	// a locked target track: the group stays on its tracks but still moves in time
	f.tap(f.buttonPos("A1", btnLock))
	f.tap(f.buttonPos("V2", btnLock))
	f.s.SelectClips([]string{A, f.clipID("V1", 1)})
	f.drag(fyne.NewPos(f.x(50), f.rowMid("V1")), fyne.NewPos(f.x(70), f.rowMid("V2")))
	if v1 := f.track("V1").Clips; len(v1) != 2 || v1[0].Start != 20 {
		t.Fatalf("group must stay on V1, moved 20: %+v", v1)
	}
	if len(f.errs) != 1 {
		t.Fatalf("no error expected for a locked target: %v", f.errs)
	}
}

func TestContextMenuDeletesSelection(t *testing.T) {
	f := setup(t, 1000, 300)
	C := f.place("V1", model.NewClip(model.KindColor, "C", 200, 30))
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)
	f.s.SelectClips([]string{A, B})
	// right click on a selected clip keeps the selection
	f.tl.TappedSecondary(&fyne.PointEvent{Position: fyne.NewPos(f.x(120), f.rowMid("V1"))})
	if len(f.s.Selected) != 2 {
		t.Fatalf("right click must keep the selection: %v", f.s.Selected)
	}
	m := f.tl.clipMenu(f.track("V1").Clips[1])
	for _, it := range m.Items {
		if it.Label == "Lücke schließen" {
			it.Action()
		}
	}
	clips := f.track("V1").Clips
	if len(clips) != 1 || clips[0].ID != C || clips[0].Start != 50 {
		t.Fatalf("ripple delete of the selection: %+v", clips)
	}
	f.s.Undo()
	f.s.SelectClips([]string{A, B})
	for _, it := range f.tl.clipMenu(f.track("V1").Clips[0]).Items {
		if it.Label == "2 Clips löschen" {
			it.Action()
		}
	}
	if clips := f.track("V1").Clips; len(clips) != 1 || clips[0].Start != 200 || f.s.UndoLabel() != "2 Clips löschen" {
		t.Fatalf("delete selection: %+v (%q)", clips, f.s.UndoLabel())
	}
}

// rippleProject: V1 with a 200-frame video clip (source 40..140 used, 100 frames at 0),
// a still 100..150 and a title 150..200; frame rate 25.
func rippleProject() (*model.Project, string, string, string) {
	p := model.NewProject("r")
	m := model.MediaItem{ID: "m", Kind: model.KindVideo, Duration: 8 * time.Second} // 200 frames
	p.AddMedia(m)
	v := p.ClipFromMedia(m, 0, 0)
	v.SourceIn, v.Length = 40, 100
	v.FadeOut = 30
	_ = p.PlaceClip(p.Tracks[0].ID, v)
	s := model.NewClip(model.KindColor, "S", 100, 50)
	_ = p.PlaceClip(p.Tracks[0].ID, s)
	ti := model.NewClip(model.KindTitle, "T", 150, 50)
	_ = p.PlaceClip(p.Tracks[0].ID, ti)
	return p, v.ID, s.ID, ti.ID
}

func TestRippleTrimMath(t *testing.T) {
	p, v, s, ti := rippleProject()
	if lo, hi := rippleLimits(p, v, false); lo != -99 || hi != 60 {
		t.Fatalf("right ripple limits %d..%d, want -99..60", lo, hi)
	}
	if lo, hi := rippleLimits(p, v, true); lo != -40 || hi != 99 {
		t.Fatalf("left ripple limits %d..%d, want -40..99", lo, hi)
	}
	if lo, hi := rippleLimits(p, s, true); lo != -unbounded || hi != 49 {
		t.Fatalf("still has no source limit: %d..%d", lo, hi)
	}
	// right edge +20: later clips shift right by 20
	q := p.Clone()
	if err := rippleTrim(q, v, false, 20); err != nil {
		t.Fatal(err)
	}
	if c := q.Tracks[0].Clips; c[0].Length != 120 || c[1].Start != 120 || c[2].Start != 170 {
		t.Fatalf("ripple right: %+v", c)
	}
	// right edge -70: fade clamped, later clips shift left
	q = p.Clone()
	_ = rippleTrim(q, v, false, -80)
	if c := q.Tracks[0].Clips; c[0].Length != 20 || c[0].FadeOut != 20 || c[1].Start != 20 || c[2].Start != 70 {
		t.Fatalf("ripple right shorten: %+v", c)
	}
	// left edge cuts 30 from the head: start stays, source moves, later clips move left
	q = p.Clone()
	_ = rippleTrim(q, v, true, 30)
	if c := q.Tracks[0].Clips; c[0].Start != 0 || c[0].SourceIn != 70 || c[0].Length != 70 || c[1].Start != 70 {
		t.Fatalf("ripple left: %+v", c)
	}
	// left edge extended by 40 (to the first source frame), past it is refused
	q = p.Clone()
	_ = rippleTrim(q, v, true, -40)
	if c := q.Tracks[0].Clips; c[0].SourceIn != 0 || c[0].Length != 140 || c[1].Start != 140 {
		t.Fatalf("ripple left extend: %+v", c)
	}
	if err := rippleTrim(p.Clone(), v, true, -41); !errors.Is(err, model.ErrInvalidEdit) {
		t.Fatalf("beyond source start: %v", err)
	}
	if err := rippleTrim(p.Clone(), v, false, 61); !errors.Is(err, model.ErrInvalidEdit) {
		t.Fatalf("beyond source end: %v", err)
	}
	// untimed clip: no source shift
	q = p.Clone()
	_ = rippleTrim(q, ti, true, 10)
	if c := q.Tracks[0].Clips[2]; c.SourceIn != 0 || c.Length != 40 || c.Start != 150 {
		t.Fatalf("title head ripple: %+v", c)
	}
	p.Tracks[0].Locked = true
	if err := rippleTrim(p, v, false, 5); !errors.Is(err, model.ErrLocked) {
		t.Fatalf("locked: %v", err)
	}
}

func TestRollEditMath(t *testing.T) {
	p, v, s, _ := rippleProject()
	// v may grow by 60 (source end), s keeps one frame: cut in 1..149 ∩ ..160
	if lo, hi := rollLimits(p, v, s); lo != 1 || hi != 149 {
		t.Fatalf("roll limits %d..%d", lo, hi)
	}
	q := p.Clone()
	if err := rollEdit(q, v, s, 120); err != nil {
		t.Fatal(err)
	}
	c := q.Tracks[0].Clips
	if c[0].Length != 120 || c[1].Start != 120 || c[1].Length != 30 || c[2].Start != 150 {
		t.Fatalf("roll right: %+v", c)
	}
	if q.Duration() != p.Duration() {
		t.Fatal("roll must keep the programme length")
	}
	// a timed clip on the right side cannot start before its source
	m := model.MediaItem{ID: "m2", Kind: model.KindVideo, Duration: 4 * time.Second} // 100 frames
	p.AddMedia(m)
	w := p.ClipFromMedia(m, 200, 0)
	w.SourceIn, w.Length = 10, 50
	_ = p.PlaceClip(p.Tracks[0].ID, w)
	title := p.Tracks[0].Clips[2].ID
	if lo, hi := rollLimits(p, title, w.ID); lo != 190 || hi != 249 {
		t.Fatalf("roll limits with source head %d..%d", lo, hi)
	}
	if err := rollEdit(p.Clone(), title, w.ID, 189); !errors.Is(err, model.ErrInvalidEdit) {
		t.Fatalf("past the source head: %v", err)
	}
	q = p.Clone()
	_ = rollEdit(q, title, w.ID, 190)
	if c := q.Tracks[0].Clips[3]; c.Start != 190 || c.SourceIn != 0 || c.Length != 60 {
		t.Fatalf("roll left: %+v", c)
	}
	if err := rollEdit(p.Clone(), v, title, 120); !errors.Is(err, model.ErrInvalidEdit) {
		t.Fatalf("clips that do not touch: %v", err)
	}
}

func TestRippleAndRollTools(t *testing.T) {
	f := setup(t, 1000, 300) // V1: A 0..100, B 100..150
	f.tl.SetSnapping(false)
	C := f.place("V1", model.NewClip(model.KindColor, "C", 200, 30))
	y := f.rowMid("V1")
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)

	f.tl.SetTool(ToolRipple)
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(150)-2, y)}})
	if f.tl.Cursor() != desktop.HResizeCursor {
		t.Fatal("ripple edge cursor")
	}
	// B's end +20: C follows
	from, to := fyne.NewPos(f.x(150)-2, y), fyne.NewPos(f.x(170)-2, y)
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, 0)})
	if d := f.tl.drag; d == nil || d.mode != dragRippleR || d.preview == nil || d.preview.Tracks[0].Clips[2].Start != 220 {
		t.Fatalf("ripple preview %+v", f.tl.drag)
	}
	f.tl.DragEnd()
	if c := f.track("V1").Clips; c[1].Length != 70 || c[2].ID != C || c[2].Start != 220 || f.s.UndoLabel() != "Ripple-Trimmen" {
		t.Fatalf("ripple: %+v %q", c, f.s.UndoLabel())
	}

	// roll the cut between A and B (touching) 30 frames left
	f.tl.SetTool(ToolRoll)
	f.drag(fyne.NewPos(f.x(100)-2, y), fyne.NewPos(f.x(70)-2, y))
	a, _, _ := f.s.Project.Clip(A)
	b, _, _ := f.s.Project.Clip(B)
	if a.Length != 70 || b.Start != 70 || b.Length != 100 || f.s.UndoLabel() != "Schnitt verschieben" {
		t.Fatalf("roll: A %+v B %+v (%q)", a, b, f.s.UndoLabel())
	}
	// roll on an edge without a neighbour trims normally
	f.drag(fyne.NewPos(f.x(250)-2, y), fyne.NewPos(f.x(260)-2, y))
	if c, _, _ := f.s.Project.Clip(C); c.Start != 220 || c.Length != 40 || f.s.UndoLabel() != "Clip trimmen" {
		t.Fatalf("roll fallback trim: %+v", c)
	}
	// ripple on a locked track is rejected
	f.tl.SetTool(ToolRipple)
	f.tap(f.buttonPos("V1", btnLock))
	f.drag(fyne.NewPos(f.x(170)-2, y), fyne.NewPos(f.x(180)-2, y))
	if len(f.errs) != 1 || !errors.Is(f.errs[0], model.ErrLocked) {
		t.Fatalf("locked ripple: %v", f.errs)
	}
}

func TestBladeAllTracks(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	f.place("V2", model.NewClip(model.KindTitle, "T", 20, 100))
	f.place("A1", model.NewClip(model.KindAudio, "Ton", 0, 150))
	f.tl.SetTool(ToolBlade)
	f.tl.SetBladeAllTracks(true)
	if !f.tl.BladeAllTracks() {
		t.Fatal("flag not set")
	}
	f.tap(fyne.NewPos(f.x(60), f.tl.geom().tailY()+10)) // below all tracks still cuts
	if n := len(f.track("V1").Clips) + len(f.track("V2").Clips) + len(f.track("A1").Clips); n != 7 {
		t.Fatalf("blade all: %d clips, want 7", n)
	}
	if f.s.UndoLabel() != "Alle Spuren teilen" {
		t.Fatalf("label %q", f.s.UndoLabel())
	}
	// locked tracks are skipped
	f.tap(f.buttonPos("V2", btnLock))
	f.tap(fyne.NewPos(f.x(80), f.rowMid("V1")))
	if len(f.track("V2").Clips) != 2 || len(f.track("V1").Clips) != 4 || len(f.track("A1").Clips) != 3 {
		t.Fatalf("locked V2 must stay: V1 %d V2 %d A1 %d", len(f.track("V1").Clips), len(f.track("V2").Clips), len(f.track("A1").Clips))
	}
	// only locked clips under the blade: say so
	f.tap(fyne.NewPos(f.x(110), f.rowMid("V2")))
	f.tap(fyne.NewPos(f.x(160), f.rowMid("V1")))
	if len(f.errs) != 0 {
		t.Fatalf("cuts on unlocked tracks: %v", f.errs)
	}
	_ = f.s.Edit("lock", func(p *model.Project) error {
		for i := range p.Tracks {
			p.Tracks[i].Locked = true
		}
		return nil
	})
	f.tap(fyne.NewPos(f.x(30), f.rowMid("V1")))
	if len(f.errs) != 1 || !errors.Is(f.errs[0], model.ErrLocked) {
		t.Fatalf("all locked: %v", f.errs)
	}
}

func TestRenameTrack(t *testing.T) {
	f := setup(t, 1000, 300)
	v2 := f.track("V2").ID
	name := fyne.NewPos(40, f.rowMid("V2"))
	f.tap(name)
	if f.tl.rename != nil {
		t.Fatal("single click must not rename")
	}
	f.tap(name)
	st := f.tl.rename
	if st == nil || st.track != v2 || st.entry.Text != "V2" {
		t.Fatalf("double click on the name opens the entry: %+v", st)
	}
	if len(f.tl.r.overlay.Objects) != 1 || f.tl.r.overlay.Objects[0] != st.entry {
		t.Fatal("entry not shown")
	}
	if f.w.Canvas().Focused() != st.entry {
		t.Fatal("entry must have the focus")
	}
	st.entry.SetText("  Grafik ")
	st.entry.OnSubmitted(st.entry.Text)
	if f.track("Grafik") == nil || f.s.UndoLabel() != "Spur umbenennen" || f.tl.rename != nil {
		t.Fatalf("rename not applied: %q", f.s.UndoLabel())
	}
	if len(f.tl.r.overlay.Objects) != 0 {
		t.Fatal("entry must close")
	}

	// Esc cancels, empty names are refused, the menu item opens the entry too
	f.tl.startRename(v2)
	f.tl.rename.entry.SetText("Weg")
	f.tl.rename.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if f.track("Grafik") == nil || f.tl.rename != nil {
		t.Fatal("Esc must cancel")
	}
	for _, it := range f.tl.trackMenu(*f.track("Grafik")).Items {
		if it.Label == "Umbenennen" {
			it.Action()
		}
	}
	if f.tl.rename == nil {
		t.Fatal("menu Umbenennen")
	}
	f.tl.rename.entry.SetText("  ")
	f.w.Canvas().Unfocus() // focus loss commits
	if f.track("Grafik") == nil || len(f.errs) != 1 {
		t.Fatalf("empty name: errs %v", f.errs)
	}
	// a double click on the switches does not rename
	f.tap(f.buttonPos("A1", btnMute))
	f.tap(f.buttonPos("A1", btnMute))
	if f.tl.rename != nil {
		t.Fatal("switch clicks must not rename")
	}
}

func TestInsertTrackAboveBelow(t *testing.T) {
	f := setup(t, 1000, 300) // V1 V2 A1
	names := func() []string {
		var out []string
		for _, ti := range displayOrder(f.s.Project) {
			out = append(out, f.s.Project.Tracks[ti].Name)
		}
		return out
	}
	run := func(track, label string) {
		for _, it := range f.tl.trackMenu(*f.track(track)).Items {
			if it.Label == label {
				it.Action()
				return
			}
		}
		t.Fatalf("menu item %q missing", label)
	}
	run("V1", "Spur darüber hinzufügen")
	if got := names(); !slices.Equal(got, []string{"V2", "V3", "V1", "A1"}) {
		t.Fatalf("above V1: %v", got)
	}
	run("V1", "Spur darunter hinzufügen")
	if got := names(); !slices.Equal(got, []string{"V2", "V3", "V1", "V4", "A1"}) {
		t.Fatalf("below V1: %v", got)
	}
	run("A1", "Spur darüber hinzufügen")
	run("A1", "Spur darunter hinzufügen")
	if got := names(); !slices.Equal(got, []string{"V2", "V3", "V1", "V4", "A2", "A1", "A3"}) {
		t.Fatalf("audio: %v", got)
	}
	if f.s.UndoLabel() != "Spur hinzufügen" {
		t.Fatalf("label %q", f.s.UndoLabel())
	}
	if err := f.s.Project.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackResize(t *testing.T) {
	f := setup(t, 1000, 400)
	g := f.tl.geom()
	r := g.rows[g.rowOfTrack(1)] // V1 under V2
	border := r.y + r.h - 1
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(30, border)}})
	if f.tl.Cursor() != desktop.VResizeCursor {
		t.Fatal("header border shows a resize cursor")
	}
	f.drag(fyne.NewPos(30, border), fyne.NewPos(30, border+40))
	if h := f.tl.heights[f.track("V2").ID]; h != videoRowH+40 {
		t.Fatalf("height %v", h)
	}
	g = f.tl.geom()
	if r := g.rows[0]; r.h != videoRowH+40 || g.rows[1].y != r.y+r.h {
		t.Fatalf("rows not relaid: %+v", g.rows)
	}
	f.drag(fyne.NewPos(30, g.rows[0].y+g.rows[0].h), fyne.NewPos(30, 0))
	if h := f.tl.heights[f.track("V2").ID]; h != minRowH {
		t.Fatalf("height %v, want min %v", h, minRowH)
	}
	if f.s.CanUndo() {
		t.Fatal("track height is view state, not an edit")
	}
	for _, it := range f.tl.trackMenu(*f.track("V2")).Items {
		if it.Label == "Standardhöhe" {
			it.Action()
		}
	}
	if _, ok := f.tl.heights[f.track("V2").ID]; ok {
		t.Fatal("reset height")
	}
}

func TestDefaultHeightsFitPanel(t *testing.T) {
	p := geomProject() // V1 V2 A1 A2
	g := newGeom(p, 1200, 300, 1, 0, 0)
	if g.maxScroll() > 0 || g.tailY() > 300-tailH+1 {
		t.Fatalf("2 video + 2 audio tracks must fit 300 px: content %v", g.content)
	}
	if _, _, ok := g.scrollThumb(); ok {
		t.Fatal("no scroll indicator when everything fits")
	}
	p.AddTrack(model.TrackAudio)
	p.AddTrack(model.TrackVideo)
	g = newGeom(p, 1200, 300, 1, 0, 0)
	if _, _, ok := g.scrollThumb(); !ok {
		t.Fatal("overflowing tracks need a scroll indicator")
	}
}

func TestScrollIndicatorDrag(t *testing.T) {
	f := setup(t, 1000, 150)
	g := f.tl.geom()
	y, h, ok := g.scrollThumb()
	if !ok {
		t.Fatal("tracks overflow 150 px")
	}
	x := g.w - 4
	f.drag(fyne.NewPos(x, rulerH+y+h/2), fyne.NewPos(x, 400))
	if f.tl.vscroll != g.maxScroll() {
		t.Fatalf("vscroll %v, want %v", f.tl.vscroll, g.maxScroll())
	}
	// Shift+wheel over the lanes scrolls vertically as well
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(500, 100)}, Modifier: fyne.KeyModifierShift})
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(500, 100)}, Scrolled: fyne.NewDelta(0, 30)})
	if f.tl.vscroll != g.maxScroll()-30 {
		t.Fatalf("shift wheel vscroll %v", f.tl.vscroll)
	}
}

func TestCtrlWheelZoomsAtPointer(t *testing.T) {
	f := setup(t, 1000, 300)
	var reported []float32
	f.tl.OnZoomChanged = func(z float32) { reported = append(reported, z) }
	px := float32(600)
	frame := f.tl.geom().xFrame(px)
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(px, 100)}, Modifier: fyne.KeyModifierControl})
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(px, 100)}, Scrolled: fyne.NewDelta(0, 40)})
	if f.tl.Zoom() <= 1 || len(reported) != 1 || reported[0] != f.tl.Zoom() {
		t.Fatalf("zoom %v, reported %v", f.tl.Zoom(), reported)
	}
	if got := f.tl.geom().xFrame(px); math.Abs(got-frame) > 0.01 {
		t.Fatalf("frame under the pointer moved from %v to %v", frame, got)
	}
	z := f.tl.Zoom()
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(300, 100)}, Scrolled: fyne.NewDelta(0, -20)})
	if f.tl.Zoom() >= z || len(reported) != 2 {
		t.Fatalf("zoom out: %v -> %v", z, f.tl.Zoom())
	}
	// without Ctrl the wheel pans again; SetZoom from outside is not reported back
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(px, 100)}})
	z = f.tl.Zoom()
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(px, 100)}, Scrolled: fyne.NewDelta(0, -40)})
	f.tl.SetZoom(3)
	if f.tl.Zoom() != 3 || len(reported) != 2 || z == 3 {
		t.Fatalf("plain wheel / SetZoom: %v %v", f.tl.Zoom(), reported)
	}
}

func TestReversePlaybackFollows(t *testing.T) {
	f := setup(t, 1000, 300)
	_ = f.s.Edit("x", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindColor, "lang", 150, 1000))
	})
	f.tl.SetZoom(10)
	f.s.Seek(1000)
	f.s.Shuttle(-1)
	f.s.Shuttle(-1) // 2x backwards
	if !f.s.Playing || f.s.Rate != -2 {
		t.Fatalf("shuttle state %v %v", f.s.Playing, f.s.Rate)
	}
	for i := 0; i < 400 && f.s.Playing; i++ {
		f.s.Advance(40 * time.Millisecond)
		g := f.tl.geom()
		if x := g.frameX(float64(f.s.Playhead)); x < headerW || x > g.w {
			t.Fatalf("playhead @%d off screen at x=%v", f.s.Playhead, x)
		}
	}
	// paging backwards puts the playhead near the right edge
	f.s.Seek(600)
	f.s.Shuttle(-1)
	f.s.Seek(600) // stop, stay
	f.s.Shuttle(-1)
	g := f.tl.geom()
	f.s.Advance(time.Duration(float64(time.Second) * (float64(f.s.Playhead) - (g.offset - 1)) / 25))
	g = f.tl.geom()
	if x := g.frameX(float64(f.s.Playhead)); x < headerW+g.laneW()/2 {
		t.Fatalf("reverse paging should land the playhead on the right, x=%v", x)
	}
	// fast forward follows too
	f.s.Seek(0)
	f.s.Shuttle(1)
	f.s.Shuttle(1)
	f.s.Shuttle(1) // 4x
	for i := 0; i < 100 && f.s.Playing; i++ {
		f.s.Advance(40 * time.Millisecond)
		g := f.tl.geom()
		if x := g.frameX(float64(f.s.Playhead)); x < headerW || x > g.w {
			t.Fatalf("ff playhead @%d off screen at x=%v", f.s.Playhead, x)
		}
	}
}

// dropFixture adds media: a video with sound, a still and an audio file.
func dropFixture(t *testing.T) (*fixture, model.MediaItem, model.MediaItem, model.MediaItem) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	vid := model.MediaItem{ID: "vid", Name: "Interview.mov", Path: "/x/i.mov", Kind: model.KindVideo, Duration: 4 * time.Second, HasAudio: true}
	img := model.MediaItem{ID: "img", Name: "Logo.png", Path: "/x/l.png", Kind: model.KindImage}
	snd := model.MediaItem{ID: "snd", Name: "Musik.wav", Path: "/x/m.wav", Kind: model.KindAudio, Duration: 2 * time.Second}
	_ = f.s.Edit("media", func(p *model.Project) error {
		p.AddMedia(vid)
		p.AddMedia(img)
		p.AddMedia(snd)
		return nil
	})
	return f, vid, img, snd
}

func TestDropPlacesMedia(t *testing.T) {
	f, vid, img, _ := dropFixture(t)
	pos := fyne.NewPos(f.x(100), f.rowMid("V2"))
	f.tl.MediaDragOver([]string{vid.ID, img.ID}, pos)
	plan := f.tl.drop
	if plan == nil || plan.at != 100 || len(plan.ghosts) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	_, v2 := f.s.Project.Track(f.track("V2").ID)
	_, a1 := f.s.Project.Track(f.track("A1").ID)
	g0, g1, g2 := plan.ghosts[0], plan.ghosts[1], plan.ghosts[2]
	if g0.track != v2 || g0.clip.Start != 100 || g0.clip.Length != 100 || g1.track != a1 || g1.clip.Length != 100 {
		t.Fatalf("video ghosts %+v %+v", g0, g1)
	}
	if g2.track != v2 || g2.clip.Start != 200 || g2.clip.Length != 125 {
		t.Fatalf("still ghost back to back: %+v", g2)
	}
	ghosts := 0
	for _, o := range f.tl.r.lanes.Objects {
		if r, ok := o.(*canvas.Rectangle); ok && r.StrokeColor == studiotheme.Steel && r.StrokeWidth == 2 {
			ghosts++
		}
	}
	if ghosts != 3 {
		t.Fatalf("drawn ghosts %d", ghosts)
	}
	f.tl.MediaDragLeave()
	if f.tl.drop != nil {
		t.Fatal("leave clears the preview")
	}

	if !f.tl.MediaDrop([]string{vid.ID, img.ID}, pos) {
		t.Fatal("drop refused")
	}
	v := f.track("V2").Clips
	a := f.track("A1").Clips
	if len(v) != 2 || v[0].MediaID != "vid" || v[0].Start != 100 || v[1].MediaID != "img" || v[1].Start != 200 {
		t.Fatalf("V2 after drop: %+v", v)
	}
	if len(a) != 1 || a[0].MediaID != "vid" || a[0].Start != 100 || a[0].Kind != model.KindAudio {
		t.Fatalf("linked audio: %+v", a)
	}
	if f.s.UndoLabel() != "Medien einfügen" || !slices.Equal(f.s.Selected, []string{a[0].ID, v[1].ID, v[0].ID}) || f.s.SelectedClip != v[0].ID {
		t.Fatalf("label %q selection %v", f.s.UndoLabel(), f.s.Selected)
	}
	f.s.Undo()
	if len(f.track("V2").Clips)+len(f.track("A1").Clips) != 0 {
		t.Fatal("drop must be one undo step")
	}
}

func TestDropOnIncompatibleAndLockedTracks(t *testing.T) {
	f, vid, _, snd := dropFixture(t)
	// audio dropped on V2 goes to the first audio track
	f.tl.MediaDrop([]string{snd.ID}, fyne.NewPos(f.x(40), f.rowMid("V2")))
	if a := f.track("A1").Clips; len(a) != 1 || a[0].Start != 40 {
		t.Fatalf("audio on V2: %+v", a)
	}
	// locked pointer track: the first unlocked video track is used
	f.tap(f.buttonPos("V2", btnLock))
	f.tl.MediaDragOver([]string{vid.ID}, fyne.NewPos(f.x(400), f.rowMid("V2")))
	_, v1 := f.s.Project.Track(f.track("V1").ID)
	if f.tl.drop.ghosts[0].track != v1 {
		t.Fatalf("ghost on %d, want V1", f.tl.drop.ghosts[0].track)
	}
	f.tl.MediaDrop([]string{vid.ID}, fyne.NewPos(f.x(400), f.rowMid("V2")))
	if v := f.track("V1").Clips; len(v) != 3 || v[2].Start != 400 {
		t.Fatalf("V1: %+v", v)
	}
	// every video track locked: rejected with the shared message
	f.tap(f.buttonPos("V1", btnLock))
	n := len(f.s.Project.Tracks)
	if f.tl.MediaDrop([]string{vid.ID}, fyne.NewPos(f.x(600), f.rowMid("V1"))) {
		t.Fatal("drop onto locked tracks accepted")
	}
	if len(f.errs) != 1 || !errors.Is(f.errs[0], model.ErrLocked) ||
		f.errs[0].Error() != "Medien einfügen nicht möglich: Alle Videospuren sind gesperrt. Eine Videospur entsperren und erneut einfügen." {
		t.Fatalf("errors %v", f.errs)
	}
	if len(f.s.Project.Tracks) != n {
		t.Fatal("rejected drop changed the tracks")
	}
	if f.tl.MediaDrop([]string{"gibt-es-nicht"}, fyne.NewPos(f.x(10), f.rowMid("V1"))) {
		t.Fatal("unknown media accepted")
	}
}

func TestDropBelowTracksCreatesTrack(t *testing.T) {
	f, vid, _, snd := dropFixture(t)
	g := f.tl.geom()
	below := fyne.NewPos(f.x(100), g.tailY()+8)
	f.tl.MediaDragOver([]string{vid.ID}, below)
	plan := f.tl.drop
	if plan == nil || !plan.newTrack || plan.newKind != model.TrackVideo || plan.ghosts[0].track != -1 {
		t.Fatalf("plan %+v", plan)
	}
	f.tl.MediaDrop([]string{vid.ID}, below)
	v3 := f.track("V3")
	if v3 == nil || len(v3.Clips) != 1 || v3.Clips[0].Start != 100 {
		t.Fatalf("new video track: %+v", v3)
	}
	if a := f.track("A1").Clips; len(a) != 1 {
		t.Fatalf("linked audio on A1: %+v", a)
	}
	f.s.Undo()
	if f.track("V3") != nil {
		t.Fatal("new track and clip are one undo step")
	}
	f.tl.MediaDrop([]string{snd.ID}, below)
	if a2 := f.track("A2"); a2 == nil || len(a2.Clips) != 1 {
		t.Fatal("audio dropped below the tracks creates an audio track")
	}
}

func TestDropZoneRegistration(t *testing.T) {
	q := kit.UseUIQueue(t)
	_ = q
	f, vid, _, _ := dropFixture(t)
	// put the timeline below a bar so it is not at the window origin
	bar := canvas.NewRectangle(color.Black)
	bar.SetMinSize(fyne.NewSize(10, 40))
	f.w.SetContent(container.NewBorder(bar, nil, nil, nil, f.tl))
	f.w.Resize(fyne.NewSize(1000, 340))
	f.tl.AttachWindow(f.w)
	abs := func(p fyne.Position) fyne.Position { return p.Add(fyne.NewPos(0, 40)) }
	pos := fyne.NewPos(f.x(50), f.rowMid("V2"))
	kit.BeginMediaDrag(f.w, []string{vid.ID}, "Interview.mov")
	kit.UpdateMediaDrag(abs(pos))
	if f.tl.drop == nil {
		t.Fatal("registered timeline gets drag-over")
	}
	if !kit.EndMediaDrag(abs(pos)) {
		t.Fatal("drop not accepted")
	}
	if f.tl.drop != nil || len(f.track("V2").Clips) != 1 {
		t.Fatal("drop not placed")
	}
	f.tl.Close()
	kit.BeginMediaDrag(f.w, []string{vid.ID}, "Interview.mov")
	if kit.EndMediaDrag(abs(pos)) {
		t.Fatal("closed timeline must be unregistered")
	}
}

func TestPosterGrid(t *testing.T) {
	if posterStep(0.5) != 1 || posterStep(3) != 2 || posterStep(4) != 4 || posterStep(37) != 32 {
		t.Fatal("posterStep")
	}
	// tiles 10 frames apart on a grid of 8 never repeat a frame
	if posterFrame(5, 0, 8, 1000) != 5 {
		t.Fatal("the first tile shows the clip's first frame")
	}
	prev := 5
	for i := 1; i < 20; i++ {
		f := posterFrame(5, float64(i)*10, 8, 1000)
		if f%8 != 0 || f <= prev {
			t.Fatalf("tile %d frame %d after %d", i, f, prev)
		}
		prev = f
	}
	if posterFrame(0, 500, 8, 100) != 96 {
		t.Fatal("frames stay inside the source")
	}
}

func TestPosterLoaderBackground(t *testing.T) {
	q := kit.UseUIQueue(t)
	test.NewTempApp(t)
	src := &posterFake{}
	refreshed := 0
	l := newPosterLoader(src, func() { refreshed++ })
	m := model.MediaItem{ID: "v", Path: "/x/v.mov", Kind: model.KindVideo, Duration: 10 * time.Second}
	for f := 0; f < 5; f++ {
		if l.get(m, f*25, 25) != nil {
			t.Fatal("first request is asynchronous")
		}
	}
	if !q.Until(func() bool {
		for f := 0; f < 5; f++ {
			if l.get(m, f*25, 25) == nil {
				return false
			}
		}
		return true
	}, 5*time.Second) {
		t.Fatal("posters never arrived")
	}
	if refreshed == 0 {
		t.Fatal("no refresh after loading")
	}
	secs := src.requests()
	slices.Sort(secs)
	if !slices.Equal(secs, []float64{0, 1, 2, 3, 4}) {
		t.Fatalf("requested seconds %v", secs)
	}
	l.close()
	l.wg.Wait()
	if l.get(m, 999, 25) != nil {
		t.Fatal("closed loader must not start work")
	}
}

func TestPosterQueueBound(t *testing.T) {
	block := make(chan struct{})
	src := &blockingPoster{release: block}
	l := newPosterLoader(src, nil)
	m := model.MediaItem{ID: "v", Path: "/x/v.mov"}
	for f := 0; f < posterQueueMax+posterWorkers+10; f++ {
		l.get(m, f, 25)
	}
	l.mu.Lock()
	qn, items := len(l.queue), len(l.items)
	l.mu.Unlock()
	if qn > posterQueueMax || items > posterQueueMax+posterWorkers {
		t.Fatalf("queue %d, items %d", qn, items)
	}
	l.close()
	close(block)
	l.wg.Wait()
}

type blockingPoster struct{ release chan struct{} }

func (b *blockingPoster) Poster(model.MediaItem, float64, int, int) (image.Image, error) {
	<-b.release
	return image.NewNRGBA(image.Rect(0, 0, 4, 4)), nil
}

func TestFilmstripRequestsFramesAlongClip(t *testing.T) {
	f := setup(t, 1200, 300)
	path := filepath.Join(t.TempDir(), "v.mov")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &posterFake{}
	f.tl.ld.src = src
	f.tl.posters.src = src
	f.tl.posters.inline = true
	m := model.MediaItem{ID: "v", Name: "v.mov", Path: path, Kind: model.KindVideo, Duration: 40 * time.Second, Width: 1920, Height: 1080}
	_ = f.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(m)
		c := p.ClipFromMedia(m, 0, 0)
		c.SourceIn, c.Length = 100, 600
		return p.PlaceClip(p.Tracks[1].ID, c)
	})
	f.tl.Refresh()
	secs := src.requests()
	if len(secs) < 5 {
		t.Fatalf("want several frames along the clip, got %v", secs)
	}
	if secs[0] < 4-0.01 || secs[0] > 4.01 {
		t.Fatalf("first tile shows the clip's first source frame (4 s), got %v", secs[0])
	}
	for i := 1; i < len(secs); i++ {
		if secs[i] <= secs[i-1] {
			t.Fatalf("frames must advance along the clip: %v", secs)
		}
	}
	// redraws reuse loaded frames
	n := len(secs)
	f.tl.Refresh()
	if len(src.requests()) != n {
		t.Fatal("frames requested twice")
	}
}

// TestScreenshotRound2 renders multi-selection, a marquee in progress and a drop ghost to
// /tmp/fs-r2-timeline.png when FS_SCREENSHOT is set.
func TestScreenshotRound2(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1 to write /tmp/fs-r2-timeline.png")
	}
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	p := screenshotProject(t)
	for i := range p.Media {
		p.Media[i].Width, p.Media[i].Height = 1920, 1080
	}
	s := session.New(p)
	tl := New(s, nil)
	src := &posterFake{}
	tl.ld.src = src
	tl.ld.inline = true
	tl.posters.src = src
	tl.posters.inline = true
	defer tl.Close()

	// the timeline as the bottom panel of a 1440x900 edit page
	top := canvas.NewRectangle(studiotheme.Gap)
	top.SetMinSize(fyne.NewSize(1440, 560))
	w := test.NewTempWindow(t, container.New(layout.NewBorderLayout(top, nil, nil, nil), top, tl))
	w.SetPadded(false)
	w.Resize(fyne.NewSize(1440, 900))
	p.AddMedia(model.MediaItem{ID: "nx", Name: "Nachdreh_02.mov", Path: p.Media[0].Path, Kind: model.KindVideo, Duration: 5 * time.Second, HasAudio: true, Width: 1920, Height: 1080})

	s.Seek(312)
	// multi-selection: B-roll picture + sound, primary the B-roll picture
	s.SelectClips([]string{p.Tracks[3].Clips[1].ID, p.Tracks[0].Clips[2].ID})

	// Ctrl+marquee in progress from empty V2 space down into V1: adds the title end and
	// the interview
	g := tl.geom()
	v2, v1 := g.rows[g.rowOfTrack(1)], g.rows[g.rowOfTrack(0)]
	tl.mods = fyne.KeyModifierControl
	from := fyne.NewPos(g.frameX(235), v2.y+8)
	to := fyne.NewPos(g.frameX(160), v1.y+v1.h/2)
	tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})

	// drop ghost of a video with sound on V2 between the title and the logo
	tl.MediaDragOver([]string{"nx"}, fyne.NewPos(g.frameX(262), v2.y+v2.h/2))
	capture(t, w, "fs-r2-timeline.png")

	// group move ghosts and a drop below the tracks
	tl.DragEnd()
	tl.MediaDragLeave()
	tl.mods = 0
	g = tl.geom()
	b := p.Tracks[0].Clips[2]
	v1 = g.rows[g.rowOfTrack(0)]
	from = fyne.NewPos(g.frameX(float64(b.Start+20)), v1.y+v1.h/2)
	to = fyne.NewPos(g.frameX(float64(b.Start+60)), v1.y+v1.h/2)
	tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
	tl.MediaDragOver([]string{"st"}, fyne.NewPos(g.frameX(150), g.tailY()+10))
	capture(t, w, "fs-r2-timeline-move.png")
	tl.DragEnd()
	tl.MediaDragLeave()
	tl.startRename(p.Tracks[1].ID)
	capture(t, w, "fs-r2-timeline-rename.png")
}
