package edit

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/timeline"
)

// press sends a key like the shell: the page first, then the registry (playback keys
// are global commands).
func press(f *fixture, n fyne.KeyName) bool {
	return f.pg.TypedKey(&fyne.KeyEvent{Name: n}) || f.reg.Dispatch(n, 0)
}

func TestShuttleKeys(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.Seek(50)
	press(f, fyne.KeyL)
	if !f.s.Playing || f.s.Rate != 1 || f.pg.transport.Rate() != "" {
		t.Fatalf("L should play at 1x: playing %v rate %v %q", f.s.Playing, f.s.Rate, f.pg.transport.Rate())
	}
	press(f, fyne.KeyL)
	if f.s.Rate != 2 || f.pg.transport.Rate() != "2×" {
		t.Fatalf("second L should double: %v %q", f.s.Rate, f.pg.transport.Rate())
	}
	press(f, fyne.KeyL)
	press(f, fyne.KeyL)
	press(f, fyne.KeyL)
	if f.s.Rate != 8 {
		t.Fatalf("shuttle should stop at 8x, got %v", f.s.Rate)
	}
	press(f, fyne.KeyJ)
	if !f.s.Playing || f.s.Rate != -1 || f.pg.transport.Rate() != "−1×" {
		t.Fatalf("J should reverse: %v %q", f.s.Rate, f.pg.transport.Rate())
	}
	press(f, fyne.KeyJ)
	if f.s.Rate != -2 {
		t.Fatalf("second J should double reverse speed: %v", f.s.Rate)
	}
	press(f, fyne.KeyK)
	if f.s.Playing || f.s.Rate != 1 || f.pg.transport.Rate() != "" {
		t.Fatal("K should stop and reset the rate")
	}
	press(f, fyne.KeySpace)
	if !f.s.Playing || f.s.Rate != 1 {
		t.Fatal("space should play at normal speed")
	}
	press(f, fyne.KeySpace)
	f.s.Seek(50)
	press(f, fyne.KeyUp)
	if f.s.Playhead != 25 {
		t.Fatalf("Up should go to the previous edit, got %d", f.s.Playhead)
	}
	press(f, fyne.KeyDown)
	if f.s.Playhead != 75 {
		t.Fatalf("Down should go to the next edit, got %d", f.s.Playhead)
	}
}

// multiProject: V1 colour 0-100 (opacity 1), V2 title 25-75 (opacity 0.5), A1 two
// audio clips with different volume.
func multiProject() *model.Project {
	p := basicProject()
	p.Tracks[1].Clips[0].Opacity = 0.5
	a := model.NewClip(model.KindAudio, "Ton 1", 0, 40)
	b := model.NewClip(model.KindAudio, "Ton 2", 50, 40)
	b.Volume = 0.5
	b.FadeIn = 5
	_ = p.PlaceClip(p.Tracks[2].ID, a)
	_ = p.PlaceClip(p.Tracks[2].ID, b)
	return p
}

func allClipIDs(p *model.Project) []string {
	var ids []string
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func TestMultiInspectorShowsMixedAndEditsInOneStep(t *testing.T) {
	f := newFixture(t, multiProject())
	f.s.SelectClips(allClipIDs(f.s.Project))
	if f.pg.insp.kind.Text != "Auswahl" {
		t.Fatalf("panel header %q", f.pg.insp.kind.Text)
	}
	var head bool
	for _, o := range texts(f.pg.insp.box) {
		if o == "4 Clips ausgewählt" {
			head = true
		}
	}
	if !head {
		t.Fatalf("no \"4 Clips ausgewählt\" header in %v", texts(f.pg.insp.box))
	}
	sliders := find[*kit.Slider](f.pg.insp.box)
	if len(sliders) != 2 {
		t.Fatalf("want opacity and volume sliders, got %d", len(sliders))
	}
	mixedCount := 0
	for _, s := range texts(f.pg.insp.box) {
		if s == mixed {
			mixedCount++
		}
	}
	if mixedCount < 2 { // opacity and volume readouts
		t.Fatalf("mixed values not shown as —: %v", texts(f.pg.insp.box))
	}
	entries := find[*commitEntry](f.pg.insp.box)
	if len(entries) != 2 || entries[0].Text != mixed || entries[1].Text != "00:00:00:00" {
		t.Fatalf("fade entries %q %q", entries[0].Text, entries[1].Text)
	}

	// opacity drag: every visual clip, one undo step
	for _, v := range []float64{80, 60, 40} {
		sliders[0].SetValue(v)
	}
	if clipNamed(t, f.s.Project, "Farbe").Opacity != 0.4 || clipNamed(t, f.s.Project, "Titel").Opacity != 0.4 {
		t.Fatal("opacity not applied to all visual clips")
	}
	if clipNamed(t, f.s.Project, "Ton 1").Opacity != 1 {
		t.Fatal("opacity must not touch audio clips")
	}
	if f.s.UndoLabel() != "Deckkraft ändern" {
		t.Fatalf("undo label %q", f.s.UndoLabel())
	}
	f.s.Undo()
	if f.s.CanUndo() {
		t.Fatal("bulk slider drag must be one undo step")
	}
	if clipNamed(t, f.s.Project, "Titel").Opacity != 0.5 {
		t.Fatal("undo did not restore the opacity")
	}

	// volume applies to audio clips only
	f.s.SelectClips(allClipIDs(f.s.Project))
	sliders = find[*kit.Slider](f.pg.insp.box)
	sliders[1].SetValue(-6)
	if v := clipNamed(t, f.s.Project, "Ton 1").Volume; v != clipNamed(t, f.s.Project, "Ton 2").Volume || v >= 1 {
		t.Fatal("volume not applied to both audio clips")
	}

	// fades through the entry, one undo step
	entries = find[*commitEntry](f.pg.insp.box)
	enter(entries[0], "00:00:00:10")
	for _, n := range []string{"Farbe", "Titel", "Ton 1", "Ton 2"} {
		if clipNamed(t, f.s.Project, n).FadeIn != 10 {
			t.Fatalf("fade in not applied to %s", n)
		}
	}
	if entries[0].Text != "00:00:00:10" {
		t.Fatalf("fade entry shows %q", entries[0].Text)
	}
	enter(entries[1], "00:00:01:20")
	if len(f.errs) != 0 || clipNamed(t, f.s.Project, "Ton 1").FadeOut != 0 {
		t.Fatal("fade longer than the shortest clip must be rejected inline")
	}

	// label colour: one tap, all non-matte clips
	f.s.SelectClips(allClipIDs(f.s.Project))
	sw := find[*kit.Swatch](f.pg.insp.box)
	test.Tap(sw[0])
	want := labelColors()[0]
	if clipNamed(t, f.s.Project, "Titel").Color != want || clipNamed(t, f.s.Project, "Ton 2").Color != want {
		t.Fatal("label colour not applied")
	}
	if clipNamed(t, f.s.Project, "Farbe").Color == want {
		t.Fatal("label colour must not repaint colour mattes")
	}
}

// texts lists the strings of every canvas.Text and label under o.
func texts(o fyne.CanvasObject) []string {
	var out []string
	for _, l := range find[*widget.Label](o) {
		out = append(out, l.Text)
	}
	for _, e := range find[*widget.Entry](o) {
		out = append(out, e.Text)
	}
	for _, tx := range find[*canvas.Text](o) {
		out = append(out, tx.Text)
	}
	return out
}

func TestDeleteSelectionAndShortcuts(t *testing.T) {
	f := newFixture(t, multiProject())
	f.pg.Activate()
	f.s.SelectClips([]string{clipNamed(t, f.s.Project, "Ton 1").ID, clipNamed(t, f.s.Project, "Titel").ID})
	if f.pg.bar.del.Disabled() || f.pg.bar.del.Tip != "2 Clips löschen (Entf)" {
		t.Fatalf("delete button %v %q", f.pg.bar.del.Disabled(), f.pg.bar.del.Tip)
	}
	if !press(f, fyne.KeyDelete) {
		t.Fatal("Delete not handled")
	}
	if len(f.s.Project.Tracks[1].Clips) != 0 || len(f.s.Project.Tracks[2].Clips) != 1 {
		t.Fatal("both selected clips should be gone")
	}
	f.s.Undo()
	if len(f.s.Project.Tracks[2].Clips) != 2 || len(f.s.Project.Tracks[1].Clips) != 1 {
		t.Fatal("one undo should restore both clips")
	}

	// ripple delete closes the gap on each track
	f.s.SelectClips([]string{clipNamed(t, f.s.Project, "Ton 1").ID})
	test.Tap(f.pg.bar.ripple)
	if c := clipNamed(t, f.s.Project, "Ton 2"); c.Start != 10 {
		t.Fatalf("ripple delete should pull Ton 2 to 10, got %d", c.Start)
	}

	// SelectAll (the shell routes Ctrl+A to the session); Esc clears
	f.pg.SelectAll()
	if len(f.s.Selected) != 3 {
		t.Fatalf("Ctrl+A selected %d clips", len(f.s.Selected))
	}
	if !press(f, fyne.KeyEscape) || len(f.s.Selected) != 0 || f.s.SelectedClip != "" {
		t.Fatal("Esc should clear the selection")
	}
	if press(f, fyne.KeyEscape) {
		t.Fatal("Esc without selection must not be handled")
	}
}

func TestToolbarTools(t *testing.T) {
	f := newFixture(t, basicProject())
	for i, d := range toolDefs {
		test.Tap(f.pg.bar.tools[i].Button)
		if f.pg.tool != d.tool || f.pg.tl.Tool() != d.tool {
			t.Fatalf("tool %d not set", i)
		}
		for j := range toolDefs {
			if f.pg.bar.tools[j].on != (i == j) {
				t.Fatalf("segment %d active state wrong after tapping %d", j, i)
			}
		}
	}
	keys := map[fyne.KeyName]timeline.Tool{fyne.KeyA: timeline.ToolSelect, fyne.KeyB: timeline.ToolBlade,
		fyne.KeyR: timeline.ToolRipple, fyne.KeyN: timeline.ToolRoll}
	for k, tool := range keys {
		press(f, k)
		if f.pg.tl.Tool() != tool {
			t.Fatalf("key %s: tool %v", k, f.pg.tl.Tool())
		}
	}
	test.Tap(f.pg.bar.bladeAll.Button)
	if !f.pg.tl.BladeAllTracks() || !f.pg.bar.bladeAll.on {
		t.Fatal("Alle Spuren toggle not applied")
	}
	test.Tap(f.pg.bar.bladeAll.Button)
	if f.pg.tl.BladeAllTracks() {
		t.Fatal("Alle Spuren toggle not cleared")
	}
}

func TestZoomSliderFollowsTimeline(t *testing.T) {
	f := newFixture(t, basicProject())
	if f.pg.tl.OnZoomChanged == nil {
		t.Fatal("OnZoomChanged not wired")
	}
	f.pg.tl.SetZoom(6) // as if the user Ctrl+scrolled in the timeline
	f.pg.tl.OnZoomChanged(f.pg.tl.Zoom())
	if f.pg.zoom != 6 || f.pg.bar.zoom.Value != 6 {
		t.Fatalf("slider did not follow: page %v slider %v", f.pg.zoom, f.pg.bar.zoom.Value)
	}
	if f.s.CanUndo() {
		t.Fatal("zoom is no edit")
	}
	f.pg.bar.zoom.SetValue(3)
	if f.pg.tl.Zoom() != 3 {
		t.Fatal("slider does not drive the timeline")
	}
	press(f, fyne.KeyPlus)
	if f.pg.bar.zoom.Value != 4.5 {
		t.Fatalf("slider after + key %v", f.pg.bar.zoom.Value)
	}
}

func TestMeterFollowsPlayback(t *testing.T) {
	f := newFixture(t, basicProject())
	m := f.pg.transport.Meter()
	if m == nil || m.Running() {
		t.Fatal("meter must exist and idle while stopped")
	}
	press(f, fyne.KeySpace)
	if !m.Running() {
		t.Fatal("meter should poll while playing")
	}
	press(f, fyne.KeyL)
	if !m.Running() {
		t.Fatal("meter should keep polling while shuttling")
	}
	press(f, fyne.KeyK)
	if m.Running() {
		t.Fatal("meter ticker must stop on pause")
	}
	press(f, fyne.KeySpace)
	f.pg.Close()
	if m.Running() {
		t.Fatal("Close must stop the meter")
	}
}

// dropZone records media drops for the library drag test.
type dropZone struct {
	widget.Label
	over    []string
	dropped []string
}

func (z *dropZone) MediaDragOver(ids []string, _ fyne.Position) { z.over = ids }
func (z *dropZone) MediaDragLeave()                             {}
func (z *dropZone) MediaDrop(ids []string, _ fyne.Position) bool {
	z.dropped = ids
	return true
}

func TestLibraryMultiSelectAndDrag(t *testing.T) {
	p := basicProject()
	var ids []string
	for _, n := range []string{"A.mp4", "B.mp4", "C.mp4", "D.mp4"} {
		ids = append(ids, p.AddMedia(model.MediaItem{Name: n, Kind: model.KindVideo, Duration: 2 * time.Second, Width: 640, Height: 360}))
	}
	f := newFixture(t, p)
	f.q.drain()
	lib := f.pg.lib
	click := func(id string, mod fyne.KeyModifier) {
		r := lib.rows[id]
		r.MouseDown(&desktop.MouseEvent{Modifier: mod})
		r.Tapped(&fyne.PointEvent{})
	}
	click(ids[0], 0)
	click(ids[2], fyne.KeyModifierControl)
	if got := lib.pickedIDs(); len(got) != 2 || got[0] != ids[0] || got[1] != ids[2] || f.s.SelectedMedia != ids[2] {
		t.Fatalf("ctrl click: %v primary %s", got, f.s.SelectedMedia)
	}
	click(ids[3], fyne.KeyModifierShift)
	if got := lib.pickedIDs(); len(got) != 2 || got[0] != ids[2] || got[1] != ids[3] {
		t.Fatalf("shift click should select C..D, got %v", got)
	}
	click(ids[2], fyne.KeyModifierControl)
	if got := lib.pickedIDs(); len(got) != 1 || f.s.SelectedMedia != ids[3] {
		t.Fatalf("ctrl click should deselect: %v", got)
	}
	click(ids[0], 0)
	click(ids[1], fyne.KeyModifierShift)
	if len(lib.pickedIDs()) != 2 {
		t.Fatal("A..B")
	}
	if _, _, _, a := lib.rows[ids[0]].bg.FillColor.RGBA(); a == 0 {
		t.Fatal("picked rows must be highlighted")
	}
	if _, _, _, a := lib.rows[ids[3]].bg.FillColor.RGBA(); a != 0 {
		t.Fatal("unpicked rows must not be highlighted")
	}

	// drag both onto a drop zone that stands in for the timeline
	z := &dropZone{}
	z.ExtendBaseWidget(z)
	f.pg.timelineHost.Objects = []fyne.CanvasObject{z}
	f.pg.timelineHost.Refresh()
	unreg := kit.RegisterDropZone(f.w, z)
	defer unreg()
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(z).Add(fyne.NewPos(40, 30))
	r := lib.rows[ids[1]]
	r.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: at}})
	if !kit.MediaDragActive() || len(z.over) != 2 {
		t.Fatalf("drag not started over the zone: %v", z.over)
	}
	r.DragEnd()
	if len(z.dropped) != 2 || z.dropped[0] != ids[0] || z.dropped[1] != ids[1] || kit.MediaDragActive() {
		t.Fatalf("dropped %v", z.dropped)
	}

	// dragging an unselected row drags just that one
	r = lib.rows[ids[3]]
	r.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: at}})
	r.DragEnd()
	if len(z.dropped) != 1 || z.dropped[0] != ids[3] {
		t.Fatalf("dropped %v", z.dropped)
	}

	// inserting several media is one undo step, clips back to back
	click(ids[0], 0)
	click(ids[1], fyne.KeyModifierControl)
	f.s.Seek(0)
	lib.insertMedia(true)
	a, b := clipNamed(t, f.s.Project, "A.mp4"), clipNamed(t, f.s.Project, "B.mp4")
	if a.Start != 100 || b.Start != a.End() || len(f.s.Selected) != 2 {
		t.Fatalf("A at %d, B at %d, %d selected", a.Start, b.Start, len(f.s.Selected))
	}
	if f.s.UndoLabel() != "2 Clips anhängen" {
		t.Fatalf("undo label %q", f.s.UndoLabel())
	}
}

func TestTimelineShowsAllTracks(t *testing.T) {
	f := newFixture(t, demoProject())
	f.w.SetContent(shellFrame(f.pg.Content()))
	for _, sz := range []fyne.Size{fyne.NewSize(1440, 900), fyne.NewSize(1280, 800)} {
		f.w.Resize(sz)
		lay := f.pg.root.(*fyne.Container).Layout.(*fitTimeline)
		want := lay.wantHeight()
		if got := f.pg.vsplit.Trailing.Size().Height; got < want-1 {
			t.Fatalf("%v: timeline panel %v high, needs %v", sz, got, want)
		}
		if w := f.pg.rsplit.Trailing.Size().Width; w < inspMinW-4 {
			t.Fatalf("%v: inspector only %v wide", sz, w)
		}
	}
	// a dragged divider stays where the user put it
	f.pg.vsplit.SetOffset(0.5)
	f.w.Resize(fyne.NewSize(1440, 900))
	if f.pg.vsplit.Offset != 0.5 {
		t.Fatal("manual divider position overridden")
	}
}

func TestKeyMapLettersAreHandled(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.Seek(50)
	for _, g := range KeyMap {
		for _, k := range g.Keys {
			for _, tok := range strings.Split(k[0], " / ") {
				if len(tok) != 1 || tok[0] < 'A' || tok[0] > 'Z' {
					continue // named and modified keys are covered elsewhere
				}
				if !press(f, fyne.KeyName(tok)) {
					t.Errorf("key %s listed but not handled", tok)
				}
			}
		}
	}
}
