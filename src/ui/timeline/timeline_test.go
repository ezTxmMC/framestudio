package timeline

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fakeSource stands in for media.Cache: synthetic thumbnails and waveforms, no ffmpeg.
type fakeSource struct{}

func (fakeSource) Thumbnail(m model.MediaItem, w, h int) (image.Image, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	seed := float64(len(m.Name))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			sky := 1 - fy
			r := 40 + 90*sky + 40*math.Sin(fx*3+seed)
			g := 60 + 80*sky
			b := 80 + 110*sky
			if fy > 0.62+0.08*math.Sin(fx*7+seed) { // hills
				r, g, b = 50+30*fx, 70+40*fx, 45
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r), uint8(g), uint8(b), 255})
		}
	}
	return img, nil
}

func (fakeSource) Waveform(m model.MediaItem, buckets int) ([]float32, error) {
	out := make([]float32, buckets)
	for i := range out {
		f := float64(i)
		env := 0.45 + 0.35*math.Sin(f/37) + 0.15*math.Sin(f/7.3)
		noise := 0.6 + 0.4*math.Abs(math.Sin(f*12.9898)*43758.5453-math.Floor(math.Sin(f*12.9898)*43758.5453))
		out[i] = float32(math.Max(0.03, math.Min(1, env*noise)))
	}
	return out, nil
}

type fixture struct {
	tl   *Timeline
	s    *session.Session
	w    fyne.Window
	errs []error
}

// setup builds a project with V1, V2, A1 and two clips on V1 (0..100, 100..150).
func setup(t *testing.T, w, h float32) *fixture {
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	p := model.NewProject("Test")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 100))
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "B", 100, 50))
	f := &fixture{s: session.New(p)}
	f.tl = New(f.s, nil)
	f.tl.ld.inline = true
	f.tl.OnError = func(err error) { f.errs = append(f.errs, err) }
	f.w = test.NewTempWindow(t, f.tl)
	f.w.SetPadded(false)
	f.w.Resize(fyne.NewSize(w, h))
	t.Cleanup(f.tl.Close)
	return f
}

func (f *fixture) track(name string) *model.Track {
	for i := range f.s.Project.Tracks {
		if f.s.Project.Tracks[i].Name == name {
			return &f.s.Project.Tracks[i]
		}
	}
	return nil
}

// rowMid returns the vertical centre of a track's lane.
func (f *fixture) rowMid(name string) float32 {
	g := f.tl.geom()
	_, ti := f.s.Project.Track(f.track(name).ID)
	r := g.rows[g.rowOfTrack(ti)]
	return r.y + r.h/2
}

func (f *fixture) x(frame float64) float32 { return f.tl.geom().frameX(frame) }

func (f *fixture) drag(from, to fyne.Position) {
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
	f.tl.DragEnd()
}

func (f *fixture) tap(pos fyne.Position) { f.tl.Tapped(&fyne.PointEvent{Position: pos}) }

func TestMoveClipAcrossTracks(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	y := f.rowMid("V1")
	id := f.track("V1").Clips[0].ID
	from := fyne.NewPos(f.x(50), y)
	to := fyne.NewPos(f.x(250), f.rowMid("V2"))
	f.drag(from, to)

	c, ti, _ := f.s.Project.Clip(id)
	if c == nil || f.s.Project.Tracks[ti].Name != "V2" || c.Start != 200 {
		t.Fatalf("clip not moved to V2@200: %+v on %d", c, ti)
	}
	if f.s.UndoLabel() != "Clip verschieben" || f.s.SelectedClip != id {
		t.Fatalf("undo label %q, selection %q", f.s.UndoLabel(), f.s.SelectedClip)
	}
	f.s.Undo()
	if c, ti, _ := f.s.Project.Clip(id); f.s.Project.Tracks[ti].Name != "V1" || c.Start != 0 {
		t.Fatal("move must be one undo step")
	}
}

func TestMoveToIncompatibleTrackStays(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	id := f.track("V1").Clips[1].ID
	f.drag(fyne.NewPos(f.x(125), f.rowMid("V1")), fyne.NewPos(f.x(205), f.rowMid("A1")))
	c, ti, _ := f.s.Project.Clip(id)
	if f.s.Project.Tracks[ti].Name != "V1" || c.Start != 180 {
		t.Fatalf("clip should stay on V1 at 180, got %s@%d", f.s.Project.Tracks[ti].Name, c.Start)
	}
	if len(f.errs) != 0 {
		t.Fatalf("unexpected errors %v", f.errs)
	}
}

func TestMoveSnapsToPlayhead(t *testing.T) {
	f := setup(t, 1000, 300)
	f.s.Seek(200)
	id := f.track("V1").Clips[1].ID
	// drop 2 frames short of the playhead (well within 8 px at this zoom)
	if 2*f.tl.geom().ppf >= float64(snapPx) {
		t.Fatal("test assumes 2 frames < snap distance")
	}
	f.drag(fyne.NewPos(f.x(125), f.rowMid("V1")), fyne.NewPos(f.x(223), f.rowMid("V1")))
	if c, _, _ := f.s.Project.Clip(id); c.Start != 200 {
		t.Fatalf("start %d, want snapped 200", c.Start)
	}
	f.tl.SetSnapping(false)
	f.drag(fyne.NewPos(f.x(225), f.rowMid("V1")), fyne.NewPos(f.x(227), f.rowMid("V1")))
	if c, _, _ := f.s.Project.Clip(id); c.Start != 202 {
		t.Fatalf("start %d, want 202 without snapping", c.Start)
	}
}

func TestTrimEdges(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	y := f.rowMid("V1")
	b := f.track("V1").Clips[1]

	// right edge out by 30 frames
	f.drag(fyne.NewPos(f.x(150)-2, y), fyne.NewPos(f.x(180)-2, y))
	if c, _, _ := f.s.Project.Clip(b.ID); c.Start != 100 || c.Length != 80 {
		t.Fatalf("right trim: %d+%d", c.Start, c.Length)
	}
	// left edge in by 20 frames
	f.drag(fyne.NewPos(f.x(100)+2, y), fyne.NewPos(f.x(120)+2, y))
	if c, _, _ := f.s.Project.Clip(b.ID); c.Start != 120 || c.Length != 60 {
		t.Fatalf("left trim: %d+%d", c.Start, c.Length)
	}
	// left edge cannot overwrite the neighbour that ends at 100
	f.drag(fyne.NewPos(f.x(120)+2, y), fyne.NewPos(f.x(60), y))
	if c, _, _ := f.s.Project.Clip(b.ID); c.Start != 100 {
		t.Fatalf("trim must stop at neighbour, start %d", c.Start)
	}
	if a := f.track("V1").Clips[0]; a.Length != 100 {
		t.Fatalf("neighbour changed: %+v", a)
	}
	if f.s.UndoLabel() != "Clip trimmen" {
		t.Fatalf("undo label %q", f.s.UndoLabel())
	}
}

func TestBladeSplitsClip(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	f.tl.SetTool(ToolBlade)
	if f.tl.Tool() != ToolBlade {
		t.Fatal("tool not set")
	}
	f.tl.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(30), f.rowMid("V1"))}})
	if f.tl.Cursor() != desktop.CrosshairCursor {
		t.Fatal("blade shows a crosshair")
	}
	f.tap(fyne.NewPos(f.x(30), f.rowMid("V1")))
	clips := f.track("V1").Clips
	if len(clips) != 3 || clips[0].Length != 30 || clips[1].Start != 30 {
		t.Fatalf("split failed: %+v", clips)
	}
	// with snapping a click near the playhead cuts exactly at it
	f.tl.SetSnapping(true)
	f.s.Seek(60)
	f.tap(fyne.NewPos(f.x(61), f.rowMid("V1")))
	if clips := f.track("V1").Clips; len(clips) != 4 || clips[2].Start != 60 {
		t.Fatalf("snapped split failed: %+v", clips)
	}
}

func TestTapsSelectAndSeek(t *testing.T) {
	f := setup(t, 1000, 300)
	id := f.track("V1").Clips[0].ID
	f.tap(fyne.NewPos(f.x(40), f.rowMid("V1")))
	if f.s.SelectedClip != id || f.s.Playhead != 0 {
		t.Fatalf("clip click must select without seeking: %q @%d", f.s.SelectedClip, f.s.Playhead)
	}
	f.tap(fyne.NewPos(f.x(77), 10))
	if f.s.Playhead != 77 || f.s.SelectedClip != id {
		t.Fatalf("ruler click must seek: @%d", f.s.Playhead)
	}
	f.tap(fyne.NewPos(f.x(220), f.rowMid("V2")))
	if f.s.SelectedClip != "" || f.s.Playhead != 220 || f.s.SelectedTrack != f.track("V2").ID {
		t.Fatalf("empty lane click: %q @%d", f.s.SelectedClip, f.s.Playhead)
	}
}

func TestScrubOnRuler(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(40), 12)}, Dragged: fyne.NewDelta(f.x(40)-f.x(10), 0)})
	if f.s.Playhead != 40 {
		t.Fatalf("scrub @%d", f.s.Playhead)
	}
	f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(f.x(90), 12)}, Dragged: fyne.NewDelta(50, 0)})
	f.tl.DragEnd()
	if f.s.Playhead != 90 {
		t.Fatalf("scrub @%d", f.s.Playhead)
	}
}

func (f *fixture) buttonPos(name string, b button) fyne.Position {
	g := f.tl.geom()
	tr := f.track(name)
	_, ti := f.s.Project.Track(tr.ID)
	for _, hb := range headerButtons(tr.Kind, g.rows[g.rowOfTrack(ti)]) {
		if hb.b == b {
			return fyne.NewPos(hb.rect.X+hb.size.Width/2, hb.rect.Y+hb.size.Height/2)
		}
	}
	panic("no such button")
}

func TestHeaderToggles(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tap(f.buttonPos("A1", btnMute))
	f.tap(f.buttonPos("A1", btnSolo))
	f.tap(f.buttonPos("V2", btnEye))
	f.tap(f.buttonPos("V1", btnLock))
	a1, v2, v1 := f.track("A1"), f.track("V2"), f.track("V1")
	if !a1.Muted || !a1.Solo || !v2.Hidden || !v1.Locked {
		t.Fatalf("toggles: A1 %+v V2 hidden %v V1 locked %v", a1, v2.Hidden, v1.Locked)
	}
	f.tap(f.buttonPos("A1", btnMute))
	if f.track("A1").Muted {
		t.Fatal("mute should toggle off")
	}
	f.s.Undo()
	if !f.track("A1").Muted {
		t.Fatal("each toggle is one undo step")
	}
	f.tap(fyne.NewPos(30, f.rowMid("V2")))
	if f.s.SelectedTrack != f.track("V2").ID {
		t.Fatal("header click selects the track")
	}
}

func TestLockedTrackRejectsEdits(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tap(f.buttonPos("V1", btnLock))
	before := f.s.Project.Clone()
	y := f.rowMid("V1")
	f.drag(fyne.NewPos(f.x(50), y), fyne.NewPos(f.x(300), y))    // move
	f.drag(fyne.NewPos(f.x(150)-2, y), fyne.NewPos(f.x(200), y)) // trim
	f.tl.SetTool(ToolBlade)
	f.tap(fyne.NewPos(f.x(30), y))
	if len(f.errs) != 3 {
		t.Fatalf("want 3 rejected edits, got %v", f.errs)
	}
	for _, err := range f.errs {
		if !errors.Is(err, model.ErrLocked) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	got := f.track("V1").Clips
	want := before.Tracks[0].Clips
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("locked track changed: %+v", got)
	}
	// moving onto a locked track keeps the clip on its own track
	f.tl.SetTool(ToolSelect)
	f.tl.SetSnapping(false)
	_ = f.s.Edit("x", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "T", 300, 20))
	})
	f.drag(fyne.NewPos(f.x(310), f.rowMid("V2")), fyne.NewPos(f.x(320), y))
	if c := f.track("V2").Clips; len(c) != 1 || c[0].Start != 310 {
		t.Fatalf("clip should stay on V2: %+v", c)
	}
}

func TestContextMenuActions(t *testing.T) {
	f := setup(t, 1000, 300)
	a := f.track("V1").Clips[0]
	f.s.Seek(40)
	m := f.tl.clipMenu(a)
	find := func(m *fyne.Menu, label string) *fyne.MenuItem {
		for _, it := range m.Items {
			if it.Label == label {
				return it
			}
		}
		t.Fatalf("menu item %q missing", label)
		return nil
	}
	find(m, "Teilen am Playhead").Action()
	if n := len(f.track("V1").Clips); n != 3 {
		t.Fatalf("split at playhead: %d clips", n)
	}
	b := f.track("V1").Clips[1]
	find(f.tl.clipMenu(b), "Clipfarbe").ChildMenu.Items[0].Action()
	if c, _, _ := f.s.Project.Clip(b.ID); c.Color != model.FromNRGBA(studiotheme.ClipColors[0].Color) {
		t.Fatalf("colour not applied: %+v", c.Color)
	}
	find(f.tl.clipMenu(b), "Lücke schließen").Action()
	if clips := f.track("V1").Clips; len(clips) != 2 || clips[1].Start != 40 {
		t.Fatalf("ripple delete: %+v", clips)
	}
	find(f.tl.trackMenu(*f.track("V2")), "Spur löschen").Action()
	if f.track("V2") != nil {
		t.Fatal("empty track should be deleted without asking")
	}
	find(f.tl.trackMenu(*f.track("V1")), "Audiospur hinzufügen").Action()
	if f.track("A2") == nil {
		t.Fatal("audio track not added")
	}
	f.tl.TappedSecondary(&fyne.PointEvent{Position: fyne.NewPos(f.x(10), f.rowMid("V1"))})
	if f.s.SelectedClip != f.track("V1").Clips[0].ID {
		t.Fatal("right click selects the clip")
	}
}

func TestZoomAnchorsPlayheadAndWheelPans(t *testing.T) {
	f := setup(t, 1000, 300)
	f.s.Seek(120)
	before := f.x(120)
	f.tl.SetZoom(8)
	if f.tl.Zoom() != 8 {
		t.Fatal("zoom not set")
	}
	if got := f.x(120); math.Abs(float64(got-before)) > 1 {
		t.Fatalf("playhead moved from %v to %v", before, got)
	}
	off := f.tl.offset
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(500, 100)}, Scrolled: fyne.NewDelta(0, -40)})
	if f.tl.offset <= off {
		t.Fatalf("wheel down should pan right: %v -> %v", off, f.tl.offset)
	}
	f.tl.SetZoom(500)
	if f.tl.Zoom() != maxZoom {
		t.Fatal("zoom not clamped")
	}
	f.tl.SetZoom(1)
	if f.tl.offset != 0 {
		t.Fatal("zoom 1 shows everything")
	}
}

func TestPlaybackPagesView(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetZoom(10)
	f.s.Seek(0)
	f.s.Seek(140)
	g := f.tl.geom()
	if x := g.frameX(140); x < headerW || x > g.w {
		t.Fatalf("playhead off screen at %v", x)
	}
}

func TestHeaderWheelScrollsTracks(t *testing.T) {
	f := setup(t, 1000, 150)
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(40, 100)}, Scrolled: fyne.NewDelta(0, -50)})
	if f.tl.vscroll != 50 {
		t.Fatalf("vscroll %v", f.tl.vscroll)
	}
}

func TestEmptyProjectAndClose(t *testing.T) {
	f := setup(t, 800, 200)
	_ = f.s.Edit("leeren", func(p *model.Project) error {
		p.Tracks = nil
		return nil
	})
	f.tap(fyne.NewPos(400, 100))
	f.drag(fyne.NewPos(400, 100), fyne.NewPos(500, 120))
	f.tl.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(40, 100)}, Scrolled: fyne.NewDelta(0, -50)})
	f.tl.Close()
	f.s.Seek(10) // no longer subscribed: must not panic
}

func TestOfflineMediaDetected(t *testing.T) {
	f := setup(t, 1000, 300)
	m := model.MediaItem{ID: "gone", Name: "fehlt.mov", Path: "/nonexistent/fehlt.mov", Kind: model.KindVideo, Duration: 4 * time.Second}
	_ = f.s.Edit("x", func(p *model.Project) error {
		p.AddMedia(m)
		return p.PlaceClip(p.Tracks[1].ID, p.ClipFromMedia(m, 0, 0))
	})
	if a := f.tl.ld.snapshot(f.tl.ld.get(m, false, 0)); !a.done || !a.offline {
		t.Fatalf("missing file not flagged offline: %+v", a)
	}
}

func TestToggleUndoLabels(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tap(f.buttonPos("V1", btnLock))
	if f.s.UndoLabel() != "Spur sperren" {
		t.Fatalf("label %q", f.s.UndoLabel())
	}
	f.tap(f.buttonPos("V1", btnLock))
	if f.s.UndoLabel() != "Spur entsperren" {
		t.Fatalf("label %q", f.s.UndoLabel())
	}
}

func TestSameImageTwiceGetsTwoObjects(t *testing.T) {
	f := setup(t, 1000, 300)
	_ = f.s.Edit("sperren", func(p *model.Project) error {
		p.Tracks[0].Locked, p.Tracks[1].Locked = true, true
		return nil
	})
	seen := map[fyne.CanvasObject]bool{}
	for _, o := range f.tl.r.lanes.Objects {
		if seen[o] {
			t.Fatalf("object %T added twice", o)
		}
		seen[o] = true
	}
}

func TestLoaderBackground(t *testing.T) {
	test.NewTempApp(t)
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan struct{}, 8)
	l := newLoader(fakeSource{}, func() { refreshed <- struct{}{} })
	m := model.MediaItem{ID: "a", Path: path, Kind: model.KindAudio}
	a := l.get(m, true, 64)
	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("no refresh after background load")
	}
	if got := l.snapshot(a); !got.done || len(got.peaks) != 64 || got.offline {
		t.Fatalf("asset %+v", got)
	}
	if l.get(m, true, 64) != a {
		t.Fatal("asset must be cached")
	}
	l.forget("a")
	l.close()
	if got := l.snapshot(l.get(m, true, 64)); got.done {
		t.Fatal("closed loader must not start work")
	}
}

// TestScreenshot renders a rich project to /tmp/fs-timeline.png when FS_SCREENSHOT is set.
func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT=1 to write /tmp/fs-timeline.png")
	}
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	p := screenshotProject(t)
	s := session.New(p)
	tl := New(s, nil)
	tl.ld.src = fakeSource{}
	tl.ld.inline = true
	defer tl.Close()
	w := test.NewTempWindow(t, tl)
	w.SetPadded(false)
	w.Resize(fyne.NewSize(1440, 360))
	s.Seek(312)
	s.SetIn(25 * 9)
	s.SetOut(25 * 15)
	s.SelectClip(p.Tracks[1].Clips[0].ID)
	tl.SetZoom(1.6)

	capture(t, w, "fs-timeline.png")

	// mid-drag: ghost of the B-roll on V2, snapping to the Out point
	g := tl.geom()
	b := p.Tracks[0].Clips[2]
	v1, v2 := g.rows[g.rowOfTrack(0)], g.rows[g.rowOfTrack(1)]
	from := fyne.NewPos(g.frameX(float64(b.Start+20)), v1.y+v1.h/2)
	to := fyne.NewPos(g.frameX(float64(b.Start+44)), v2.y+v2.h/2)
	tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
	capture(t, w, "fs-timeline-drag.png")
	tl.DragEnd()

	_ = s.Edit("leeren", func(p *model.Project) error {
		for i := range p.Tracks {
			p.Tracks[i].Clips = []model.Clip{}
		}
		p.Markers = nil
		return nil
	})
	s.ClearRange()
	tl.SetZoom(1)
	s.Seek(0)
	capture(t, w, "fs-timeline-empty.png")
}

func capture(t *testing.T, w fyne.Window, name string) {
	out, err := os.Create(filepath.Join(os.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

func screenshotProject(t *testing.T) *model.Project {
	dir := t.TempDir()
	file := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	p := model.NewProject("Demo") // V1, V2, A1
	p.AddTrack(model.TrackVideo)
	p.AddTrack(model.TrackAudio)
	v1, v2, v3 := p.Tracks[0].ID, p.Tracks[1].ID, p.Tracks[2].ID
	a1, a2 := p.Tracks[3].ID, p.Tracks[4].ID

	interview := model.MediaItem{ID: "iv", Name: "Interview_A.mov", Path: file("iv.mov"), Kind: model.KindVideo, Duration: 14 * time.Second, HasAudio: true}
	broll := model.MediaItem{ID: "br", Name: "Stadt_Totale.mp4", Path: file("br.mp4"), Kind: model.KindVideo, Duration: 9 * time.Second, HasAudio: true}
	still := model.MediaItem{ID: "st", Name: "Logo.png", Path: file("logo.png"), Kind: model.KindImage}
	music := model.MediaItem{ID: "mu", Name: "Score_Main.wav", Path: file("mu.wav"), Kind: model.KindAudio, Duration: 30 * time.Second}
	lost := model.MediaItem{ID: "lo", Name: "Drohne_04.mov", Path: "/nonexistent/drohne.mov", Kind: model.KindVideo, Duration: 6 * time.Second}
	for _, m := range []model.MediaItem{interview, broll, still, music, lost} {
		p.AddMedia(m)
	}
	place := func(track string, c model.Clip) {
		if err := p.PlaceClip(track, c); err != nil {
			t.Fatal(err)
		}
	}
	c := p.ClipFromMedia(interview, 50, 0)
	c.Length = 300
	place(v1, c)
	c = p.ClipFromMedia(broll, 350, 0)
	c.Length = 150
	c.FadeOut = 20
	place(v1, c)
	c = model.NewClip(model.KindColor, "Schwarzblende", 0, 50)
	c.Color = model.RGBA{R: 0x30, G: 0x32, B: 0x38, A: 0xFF}
	place(v1, c)

	c = model.NewClip(model.KindTitle, "Bauchbinde Name", 100, 110)
	c.Title.Text = "Dr. Anna Berger – Stadtplanerin"
	c.FadeIn, c.FadeOut = 12, 12
	place(v2, c)
	c = p.ClipFromMedia(still, 420, 75)
	c.FadeIn = 15
	place(v2, c)

	c = p.ClipFromMedia(lost, 260, 0)
	c.Length = 120
	place(v3, c)

	c = p.ClipFromMedia(interview, 50, 0)
	c.Kind, c.Length, c.Name = model.KindAudio, 300, "Interview_A.mov"
	c.Color = model.RGBA{R: 0x3D, G: 0x9A, B: 0x5B, A: 0xFF}
	place(a1, c)
	c = p.ClipFromMedia(broll, 350, 0)
	c.Kind, c.Length = model.KindAudio, 150
	c.Color = model.RGBA{R: 0x3D, G: 0x9A, B: 0x5B, A: 0xFF}
	c.Volume = 0.6
	place(a1, c)
	c = p.ClipFromMedia(music, 0, 0)
	c.Length, c.FadeIn, c.FadeOut = 500, 40, 60
	c.Volume = 0.8
	c.Color = model.FromNRGBA(studiotheme.ClipColors[1].Color)
	place(a2, c)

	p.Tracks[2].Locked = true
	p.Tracks[4].Muted = true
	p.AddMarker(100, "Name einblenden")
	p.AddMarker(350, "Schnitt B-Roll")
	m := p.AddMarker(470, "Logo")
	p.Markers[2].Color = model.FromNRGBA(studiotheme.ClipColors[4].Color)
	_ = m
	return p
}

func TestBladeIgnoresSnapToNeighbourEdge(t *testing.T) {
	f := setup(t, 1000, 300) // A 0..100, B 100..150 on V1
	f.tl.SetSnapping(true)
	f.tl.SetTool(ToolBlade)
	f.s.Seek(400)
	g := f.tl.geom()
	x := g.frameX(100) - 2 // inside A, within the snap zone of B's start
	f.tap(fyne.NewPos(x, f.rowMid("V1")))
	if len(f.errs) != 0 {
		t.Fatalf("blade near the edit point failed: %v", f.errs)
	}
	clips := f.track("V1").Clips
	if len(clips) != 3 || clips[0].Start != 0 || clips[1].End() != 100 || clips[1].Start <= 0 {
		t.Fatalf("expected A cut just before 100: %+v", clips)
	}
}

func TestOfflineMediaComesBack(t *testing.T) {
	f := setup(t, 1000, 300)
	path := filepath.Join(t.TempDir(), "spaeter.wav")
	m := model.MediaItem{ID: "later", Name: "spaeter.wav", Path: path, Kind: model.KindAudio, Duration: 4 * time.Second}
	l := f.tl.ld
	a := l.get(m, true, 64)
	if got := l.snapshot(a); !got.offline {
		t.Fatalf("missing file not offline: %+v", got)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	a.checked = time.Now().Add(-offlineRecheck) // the recheck interval has passed
	l.mu.Unlock()
	if got := l.snapshot(l.get(m, true, 64)); got.offline {
		t.Fatal("file is back but still drawn offline")
	}
}

func TestRelinkReloadsAssets(t *testing.T) {
	f := setup(t, 1000, 300)
	m := model.MediaItem{ID: "rl", Name: "a.wav", Path: "/nonexistent/a.wav", Kind: model.KindAudio, Duration: 4 * time.Second}
	_ = f.s.Edit("x", func(p *model.Project) error { p.AddMedia(m); return nil })
	a := f.tl.ld.get(m, true, 64)
	_ = f.s.Edit("Neu verknüpfen", func(p *model.Project) error {
		p.Media[0].Duration = 5 * time.Second
		return nil
	})
	if f.tl.ld.get(p0(f), true, 64) == a {
		t.Fatal("changed media must load again")
	}
}

func p0(f *fixture) model.MediaItem { return f.s.Project.Media[0] }
