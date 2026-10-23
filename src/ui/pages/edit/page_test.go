package edit

import (
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// queue collects UI callbacks from background goroutines; the test drains it itself.
type queue struct {
	mu  sync.Mutex
	fns []func()
}

func (q *queue) post(f func()) {
	q.mu.Lock()
	q.fns = append(q.fns, f)
	q.mu.Unlock()
}

func (q *queue) drain() {
	for i := 0; i < 50; i++ {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		for _, f := range fns {
			f()
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type fixture struct {
	pg   *Page
	s    *session.Session
	w    fyne.Window
	q    *queue
	errs []error
	nav  string
}

func newFixture(t *testing.T, p *model.Project) *fixture {
	t.Helper()
	app := test.NewTempApp(t)
	kit.UseUIQueue(t)
	app.Settings().SetTheme(studiotheme.New())
	f := &fixture{s: session.New(p), q: &queue{}}
	cache := media.NewCache()
	ctx := &page.Context{
		Session: f.s, Renderer: render.New(cache), Media: cache,
		Navigate: func(id string) { f.nav = id },
		ShowErr:  func(err error) { f.errs = append(f.errs, err) },
	}
	f.pg = New(ctx).(*Page)
	f.pg.post = f.q.post
	f.w = test.NewTempWindow(t, f.pg.Content())
	ctx.Window = f.w
	f.w.Resize(fyne.NewSize(1440, 860))
	t.Cleanup(func() {
		f.pg.Close()
		f.q.drain()
	})
	return f
}

// basicProject: V1 colour 0-100, V2 title 25-75, A1 empty; 25 fps.
func basicProject() *model.Project {
	p := model.NewProject("Test")
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, 100))
	_ = p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "Titel", 25, 50))
	return p
}

func find[T any](o fyne.CanvasObject) []T {
	var out []T
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if v, ok := o.(T); ok {
			out = append(out, v)
		}
		switch c := o.(type) {
		case *fyne.Container:
			for _, ch := range c.Objects {
				walk(ch)
			}
		case fyne.Widget:
			if _, ok := o.(T); ok {
				return
			}
			for _, ch := range test.WidgetRenderer(c).Objects() {
				walk(ch)
			}
		}
	}
	walk(o)
	return out
}

func enter(e *commitEntry, s string) {
	e.SetText(s)
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
}

func clipNamed(t *testing.T, p *model.Project, name string) *model.Clip {
	t.Helper()
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			if p.Tracks[ti].Clips[ci].Name == name {
				return &p.Tracks[ti].Clips[ci]
			}
		}
	}
	t.Fatalf("no clip %q", name)
	return nil
}

func TestInspectorProjectName(t *testing.T) {
	f := newFixture(t, basicProject())
	entries := find[*commitEntry](f.pg.insp.box)
	if len(entries) == 0 || entries[0].Text != "Test" {
		t.Fatal("project name entry missing")
	}
	enter(entries[0], "Sommerfilm")
	if f.s.Project.Name != "Sommerfilm" || f.s.UndoLabel() != "Projekt umbenennen" {
		t.Fatalf("name not applied: %q", f.s.Project.Name)
	}
}

func TestInspectorClipNameAndDuration(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	entries := find[*commitEntry](f.pg.insp.box)
	name, dur := entries[0], entries[2]
	if dur.Text != "00:00:04:00" {
		t.Fatalf("duration shows %q", dur.Text)
	}
	enter(name, "Hintergrund")
	c := clipNamed(t, f.s.Project, "Hintergrund")
	enter(dur, "00:00:02:00")
	if c = clipNamed(t, f.s.Project, "Hintergrund"); c.Length != 50 {
		t.Fatalf("length %d, want 50", c.Length)
	}
	enter(dur, "zwei")
	if c = clipNamed(t, f.s.Project, "Hintergrund"); c.Length != 50 {
		t.Fatal("invalid input changed the clip")
	}
	notes := find[*widget.Label](f.pg.insp.box)
	shown := false
	for _, n := range notes {
		if n.Visible() && n.Text != "" && n.Wrapping == fyne.TextWrapWord {
			shown = true
		}
	}
	if !shown {
		t.Fatal("validation message not shown")
	}
	enter(entries[1], "1") // start at 1 s
	if c = clipNamed(t, f.s.Project, "Hintergrund"); c.Start != 25 {
		t.Fatalf("start %d, want 25", c.Start)
	}
}

func TestInspectorOpacityCoalesced(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	sliders := find[*kit.Slider](f.pg.insp.box)
	if len(sliders) != 1 {
		t.Fatalf("color clip should have one slider, got %d", len(sliders))
	}
	for _, v := range []float64{80, 55, 30} {
		sliders[0].SetValue(v)
	}
	if got := clipNamed(t, f.s.Project, "Farbe").Opacity; got != 0.3 {
		t.Fatalf("opacity %v", got)
	}
	f.s.Undo()
	if got := clipNamed(t, f.s.Project, "Farbe").Opacity; got != 1 {
		t.Fatalf("one undo should restore full opacity, got %v", got)
	}
	if f.s.CanUndo() {
		t.Fatal("slider drag produced more than one undo step")
	}
}

func TestInspectorTitleText(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectClip(clipNamed(t, f.s.Project, "Titel").ID)
	var body *widget.Entry
	for _, e := range find[*widget.Entry](f.pg.insp.box) {
		if e.MultiLine {
			body = e
		}
	}
	if body == nil {
		t.Fatal("no title text entry")
	}
	body.SetText("Hallo Welt")
	if got := clipNamed(t, f.s.Project, "Titel").Title.Text; got != "Hallo Welt" {
		t.Fatalf("title text %q", got)
	}
	checks := find[*widget.Check](f.pg.insp.box)
	test.Tap(checks[0]) // Fett off
	if clipNamed(t, f.s.Project, "Titel").Title.Bold {
		t.Fatal("bold not toggled")
	}
	links := find[*widget.Hyperlink](f.pg.insp.box)
	links[0].OnTapped()
	if f.nav != page.Color {
		t.Fatal("colour link did not navigate")
	}
}

func TestInspectorAudioVolume(t *testing.T) {
	p := basicProject()
	_ = p.PlaceClip(p.Tracks[2].ID, model.NewClip(model.KindAudio, "Ton", 0, 50))
	f := newFixture(t, p)
	f.s.SelectClip(clipNamed(t, f.s.Project, "Ton").ID)
	sliders := find[*kit.Slider](f.pg.insp.box)
	sliders[0].SetValue(-6)
	if v := clipNamed(t, f.s.Project, "Ton").Volume; v < 0.49 || v > 0.51 {
		t.Fatalf("-6 dB should be ~0.5 gain, got %v", v)
	}
	if formatDB(-6) != "−6,0 dB" || formatDB(3) != "+3,0 dB" {
		t.Fatal("dB format")
	}
}

func TestGeneratorInsertion(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.SelectTrack(f.s.Project.Tracks[1].ID)
	f.s.Seek(200)
	test.DoubleTap(f.pg.lib.genRows[2])
	c, ok := f.s.Clip()
	if !ok || c.Kind != model.KindTitle || c.Title.Y != 0.82 || c.Title.Background.A == 0 {
		t.Fatalf("lower third not inserted/selected: %+v", c)
	}
	if c.Start != 200 || c.Length != 125 {
		t.Fatalf("lower third at %d+%d", c.Start, c.Length)
	}
	if _, ti, _ := f.s.Project.Clip(c.ID); f.s.Project.Tracks[ti].Name != "V1" {
		t.Fatal("nothing under the playhead: the lowest free video track takes the title")
	}
	f.s.Undo()
	if _, ok := f.s.Clip(); ok {
		t.Fatal("insertion must be one undo step")
	}
	f.pg.insertGenerator("color")
	if c, ok := f.s.Clip(); !ok || c.Kind != model.KindColor {
		t.Fatal("colour generator")
	}
	if len(f.errs) > 0 {
		t.Fatal(f.errs)
	}
}

func TestMediaInsertion(t *testing.T) {
	p := basicProject()
	id := p.AddMedia(model.MediaItem{Name: "Strand.mp4", Kind: model.KindVideo, Duration: 4 * time.Second, HasAudio: true, Width: 1920, Height: 1080})
	f := newFixture(t, p)
	f.q.drain()
	f.s.SelectMedia(id)
	f.pg.insertMedia(id, true)
	c, ok := f.s.Clip()
	if !ok || c.Start != 100 || c.Length != 100 || c.Kind != model.KindVideo {
		t.Fatalf("video clip %+v", c)
	}
	a := f.s.Project.Tracks[2].Clips
	if len(a) != 1 || a[0].Kind != model.KindAudio || a[0].MediaID != id {
		t.Fatal("sound of the video not placed on A1")
	}
	if f.s.Playhead != 200 {
		t.Fatal("playhead should move to the end of the inserted clip")
	}
	if !f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) {
		t.Fatal("Enter should insert the selected media")
	}
}

func TestSplitAndDeleteShortcuts(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.Seek(50)
	if !f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyS}) {
		t.Fatal("S not handled")
	}
	if len(f.s.Project.Tracks[0].Clips) != 2 || len(f.s.Project.Tracks[1].Clips) != 2 {
		t.Fatal("split without selection must cut every track")
	}
	f.s.Undo()
	f.s.SelectClip(f.s.Project.Tracks[1].Clips[0].ID)
	f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if len(f.s.Project.Tracks[0].Clips) != 1 || len(f.s.Project.Tracks[1].Clips) != 2 {
		t.Fatal("split with selection must only cut the selected track")
	}
	f.s.SelectClip(f.s.Project.Tracks[1].Clips[1].ID)
	if !f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDelete}) || len(f.s.Project.Tracks[1].Clips) != 1 {
		t.Fatal("Delete should remove the selected clip")
	}
	if f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDelete}) {
		t.Fatal("Delete without selection must not be handled")
	}
	f.s.Seek(500)
	f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if len(f.errs) != 1 {
		t.Fatal("split outside clips should report an error")
	}
}

func TestTransportShortcuts(t *testing.T) {
	f := newFixture(t, basicProject())
	key := func(n fyne.KeyName) { f.pg.TypedKey(&fyne.KeyEvent{Name: n}) }
	key(fyne.KeyDown)
	if f.s.Playhead != 25 {
		t.Fatalf("next edit: %d", f.s.Playhead)
	}
	key(fyne.KeyRight)
	key(fyne.KeyI)
	key(fyne.KeyEnd)
	key(fyne.KeyO)
	if f.s.In != 26 || f.s.Out != 100 {
		t.Fatalf("in/out %d/%d", f.s.In, f.s.Out)
	}
	key(fyne.KeyM)
	if len(f.s.Project.Markers) != 1 || f.s.Project.Markers[0].Frame != 100 {
		t.Fatal("marker")
	}
	key(fyne.KeyB)
	if f.pg.tool != 1 {
		t.Fatal("blade tool")
	}
	key(fyne.KeyPlus)
	if f.pg.zoom != 1.5 {
		t.Fatalf("zoom %v", f.pg.zoom)
	}
	key(fyne.KeySpace)
	if !f.s.Playing {
		t.Fatal("space should play")
	}
	key(fyne.KeyK)
	if f.s.Playing {
		t.Fatal("K should pause")
	}
	if f.pg.bar.undo.Tip != "Rückgängig: Marker setzen" {
		t.Fatalf("undo tip %q", f.pg.bar.undo.Tip)
	}
}

func TestTracks(t *testing.T) {
	f := newFixture(t, basicProject())
	f.pg.addTrack(model.TrackVideo)
	if len(f.s.Project.TrackIndices(model.TrackVideo)) != 3 {
		t.Fatal("video track not added")
	}
	f.pg.removeSelectedTrack() // the new, empty track: no confirmation
	if len(f.s.Project.TrackIndices(model.TrackVideo)) != 2 {
		t.Fatal("empty track not removed")
	}
}

func TestProjectFPSChange(t *testing.T) {
	p := basicProject()
	p.AddMarker(50, "M")
	f := newFixture(t, p)
	f.s.Seek(50)
	var rate *widget.Select
	for _, s := range find[*widget.Select](f.pg.insp.box) {
		if s.Selected == "25 fps" {
			rate = s
		}
	}
	if rate == nil {
		t.Fatal("no frame rate select")
	}
	rate.SetSelected("50 fps")
	title := clipNamed(t, f.s.Project, "Titel")
	if f.s.FPS() != 50 || title.Start != 50 || title.Length != 100 || f.s.Project.Markers[0].Frame != 100 {
		t.Fatalf("not rescaled: fps %d title %d+%d", f.s.FPS(), title.Start, title.Length)
	}
	if f.s.Playhead != 100 {
		t.Fatal("playhead not rescaled")
	}
	f.s.Undo()
	if f.s.FPS() != 25 || clipNamed(t, f.s.Project, "Titel").Start != 25 {
		t.Fatal("fps change must be one undo step")
	}
}

func TestRescaleFPSKeepsMediaInBounds(t *testing.T) {
	p := model.NewProject("x")
	p.Settings.FPS = 30
	id := p.AddMedia(model.MediaItem{Name: "v", Kind: model.KindVideo, Duration: 1001 * time.Millisecond})
	c := p.ClipFromMedia(p.Media[0], 0, 0)
	_ = p.PlaceClip(p.Tracks[0].ID, c)
	if err := rescaleFPS(p, 24); err != nil {
		t.Fatal(err)
	}
	got := p.Tracks[0].Clips[0]
	m, _ := p.MediaByID(id)
	if got.SourceIn+got.Length > p.MediaFrames(*m) {
		t.Fatalf("clip exceeds source after rescale: %+v", got)
	}
}

func TestTitlesOverlayThePicture(t *testing.T) {
	f := newFixture(t, basicProject()) // V1 colour 0-100, V2 title 25-75
	f.s.SelectTrack(f.s.Project.Tracks[0].ID)
	trackOf := func(id string) string {
		_, ti, _ := f.s.Project.Clip(id)
		return f.s.Project.Tracks[ti].Name
	}
	f.s.Seek(80) // over the colour clip; V2 is free from 80 on
	f.pg.insertGenerator("lower")
	c, _ := f.s.Clip()
	if trackOf(c.ID) != "V2" || len(f.s.Project.Tracks[0].Clips) != 1 || f.s.Project.Tracks[0].Clips[0].Length != 100 {
		t.Fatalf("lower third must sit above the picture on V2, got %s", trackOf(c.ID))
	}
	f.s.Undo()
	f.s.Seek(50) // over both V1 and the title on V2: a new track is added on top
	f.pg.insertGenerator("title")
	c, _ = f.s.Clip()
	if trackOf(c.ID) != "V3" || len(f.s.Project.TrackIndices(model.TrackVideo)) != 3 {
		t.Fatalf("title must go to a new V3, got %s", trackOf(c.ID))
	}
	if len(f.errs) > 0 {
		t.Fatal(f.errs)
	}
}

func TestSplitFallsBackWhenSelectionIsElsewhere(t *testing.T) {
	p := basicProject()
	m := model.MediaItem{ID: "mus", Name: "Musik", Kind: model.KindAudio, Duration: 20 * time.Second, Path: "/m.wav"}
	p.AddMedia(m)
	music := p.ClipFromMedia(m, 150, 0)
	_ = p.PlaceClip(p.Tracks[2].ID, music)
	f := newFixture(t, p)
	f.s.SelectClip(music.ID)
	f.s.Seek(10) // inside the colour clip on V1, not under the music
	f.pg.TypedKey(&fyne.KeyEvent{Name: fyne.KeyS})
	if len(f.errs) > 0 {
		t.Fatalf("split failed: %v", f.errs)
	}
	if len(f.s.Project.Tracks[0].Clips) != 2 {
		t.Fatal("clip under the playhead not cut")
	}
}

func TestInsertSkipsLockedTracks(t *testing.T) {
	p := basicProject()
	a2 := p.AddTrack(model.TrackAudio).ID
	id := p.AddMedia(model.MediaItem{Name: "Strand.mp4", Kind: model.KindVideo, Duration: 4 * time.Second, HasAudio: true, Width: 1920, Height: 1080})
	p.Tracks[1].Locked = true // V2
	p.Tracks[2].Locked = true // A1
	f := newFixture(t, p)
	f.s.SelectTrack(p.Tracks[1].ID)
	f.s.Seek(300)
	f.pg.insertMedia(id, false)
	if len(f.errs) > 0 {
		t.Fatalf("insert failed: %v", f.errs)
	}
	if tr, _ := f.s.Project.Track(a2); len(tr.Clips) != 1 {
		t.Fatal("sound must go to the free A2")
	}
	tr, _ := f.s.Project.Track(a2)
	tr.Locked = true
	f.pg.insertMedia(id, true)
	if len(f.errs) != 1 || !strings.Contains(f.errs[0].Error(), "Audiospuren sind gesperrt") {
		t.Fatalf("expected a hint about locked audio tracks, got %v", f.errs)
	}
}

func TestFPSChangeUndoKeepsRange(t *testing.T) {
	f := newFixture(t, basicProject())
	f.s.Playhead, f.s.In, f.s.Out = 50, 25, 75
	f.pg.setFPS(50)
	if f.s.Playhead != 100 || f.s.In != 50 || f.s.Out != 150 {
		t.Fatalf("after change: %d %d %d", f.s.Playhead, f.s.In, f.s.Out)
	}
	f.s.Undo()
	if f.s.FPS() != 25 || f.s.Playhead != 50 || f.s.In != 25 || f.s.Out != 75 {
		t.Fatalf("after undo: fps %d, %d %d %d", f.s.FPS(), f.s.Playhead, f.s.In, f.s.Out)
	}
}

func TestInspectorDurationStopsAtNextClip(t *testing.T) {
	p := basicProject() // V1 Farbe 0-100
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "B", 100, 100))
	f := newFixture(t, p)
	f.s.SelectClip(clipNamed(t, f.s.Project, "Farbe").ID)
	dur := find[*commitEntry](f.pg.insp.box)[2]
	enter(dur, "00:00:06:00")
	if a, b := clipNamed(t, f.s.Project, "Farbe"), clipNamed(t, f.s.Project, "B"); a.Length != 100 || b.Start != 100 || b.Length != 100 {
		t.Fatalf("next clip overwritten: A %d+%d, B %d+%d", a.Start, a.Length, b.Start, b.Length)
	}
	shown := false
	for _, n := range find[*widget.Label](f.pg.insp.box) {
		if n.Visible() && strings.Contains(n.Text, "nächste Clip beginnt bei 00:00:04:00") {
			shown = true
		}
	}
	if !shown {
		t.Fatal("no hint about the next clip")
	}
	enter(dur, "00:00:03:00") // shorter is fine
	if a := clipNamed(t, f.s.Project, "Farbe"); a.Length != 75 {
		t.Fatalf("length %d, want 75", a.Length)
	}
}

func TestLibraryThumbRetriesAfterFailure(t *testing.T) {
	p := basicProject()
	p.AddMedia(model.MediaItem{Name: "weg.mp4", Path: "/nonexistent/weg.mp4", Kind: model.KindVideo, Duration: time.Second, Width: 16, Height: 9})
	f := newFixture(t, p)
	f.q.drain()
	if len(f.pg.lib.loading) != 0 || !f.pg.lib.failed {
		t.Fatalf("failed load must be retried later: loading %v failed %v", f.pg.lib.loading, f.pg.lib.failed)
	}
	f.pg.Activate()
	if f.pg.lib.failed {
		t.Fatal("activation should retry the thumbnail")
	}
}
