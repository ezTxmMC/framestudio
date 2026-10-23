package timeline

import (
	"image"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
)

// A dropped video's sound goes to the audio track under the pointer, or to a free one;
// existing sound (the music bed) is never cut, and the ghost shows the same tracks.
func TestDropSoundFollowsPointer(t *testing.T) {
	f, vid, _, snd := dropFixture(t)
	musik := f.place("A1", model.NewClip(model.KindAudio, "Musik", 0, 400))
	_ = f.s.Edit("A2", func(p *model.Project) error { p.AddTrack(model.TrackAudio); return nil })
	_, a1 := f.s.Project.Track(f.track("A1").ID)
	_, a2 := f.s.Project.Track(f.track("A2").ID)

	// on A2 at 200: picture on V1, sound on A2, music untouched
	pos := fyne.NewPos(f.x(200), f.rowMid("A2"))
	f.tl.MediaDragOver([]string{vid.ID}, pos)
	if g := f.tl.drop.ghosts; len(g) != 2 || g[1].track != a2 {
		t.Fatalf("ghost of the sound must be on A2: %+v", g)
	}
	f.tl.MediaDrop([]string{vid.ID}, pos)
	if a := f.track("A1").Clips; len(a) != 1 || a[0].ID != musik || a[0].Length != 400 {
		t.Fatalf("music on A1 must stay whole: %+v", a)
	}
	if a := f.track("A2").Clips; len(a) != 1 || a[0].Start != 200 || a[0].MediaID != vid.ID {
		t.Fatalf("sound on A2: %+v", a)
	}
	if v := f.track("V1").Clips; v[len(v)-1].Start != 200 {
		t.Fatalf("picture on V1: %+v", v)
	}
	f.s.Undo()

	// on V2: A1 is busy, A2 (same position as V2) is free
	pos = fyne.NewPos(f.x(200), f.rowMid("V2"))
	f.tl.MediaDragOver([]string{vid.ID}, pos)
	if g := f.tl.drop.ghosts; g[1].track != a2 {
		t.Fatalf("ghost of the sound must be on A2: %+v", g)
	}
	f.tl.MediaDrop([]string{vid.ID}, pos)
	if len(f.track("A1").Clips) != 1 || len(f.track("A2").Clips) != 1 {
		t.Fatalf("V2 drop: A1 %+v A2 %+v", f.track("A1").Clips, f.track("A2").Clips)
	}
	f.s.Undo()

	// a pure audio file dropped on a video track goes to a free audio track
	pos = fyne.NewPos(f.x(100), f.rowMid("V1"))
	f.tl.MediaDragOver([]string{snd.ID}, pos)
	if g := f.tl.drop.ghosts; g[0].track != a2 {
		t.Fatalf("audio ghost must be on the free A2, got %d (A1 %d)", g[0].track, a1)
	}
	f.tl.MediaDrop([]string{snd.ID}, pos)
	if len(f.track("A1").Clips) != 1 || len(f.track("A2").Clips) != 1 {
		t.Fatal("audio dropped on V1 must not cut the music")
	}
}

// Ripple trim moves the linked sound and every later clip on all unlocked tracks.
func TestRippleKeepsSoundInSync(t *testing.T) {
	f := setup(t, 1000, 300) // V1: A 0..100, B 100..150
	f.tl.SetSnapping(false)
	B := f.clipID("V1", 1)
	m := model.MediaItem{ID: "m", Name: "B", Path: "/x/b.mov", Kind: model.KindVideo, HasAudio: true, Duration: 10 * time.Second}
	var sound string
	_ = f.s.Edit("media", func(p *model.Project) error {
		p.AddMedia(m)
		_ = p.DeleteClip(B, false)
		id, err := p.PlaceMedia(m, 100, 125, p.Tracks[0].ID) // B with sound 100..350
		B = id
		sound = p.LinkedClips(id)[0]
		return err
	})
	title := f.place("V2", model.NewClip(model.KindTitle, "Titel", 400, 20))
	A := f.clipID("V1", 0)
	y := f.rowMid("V1")
	f.tl.SetTool(ToolRipple)
	f.press(0)
	f.drag(fyne.NewPos(f.x(100)-2, y), fyne.NewPos(f.x(70)-2, y)) // A's right edge 30 left
	a, _, _ := f.s.Project.Clip(A)
	b, _, _ := f.s.Project.Clip(B)
	s, _, _ := f.s.Project.Clip(sound)
	tc, _, _ := f.s.Project.Clip(title)
	if a.Length != 70 || b.Start != 70 || s.Start != 70 || tc.Start != 370 {
		t.Fatalf("A len %d, B at %d, sound at %d, title at %d; want 70, 70, 70, 370 (errors %v)", a.Length, b.Start, s.Start, tc.Start, f.errs)
	}
	// ripple trimming B's tail trims its sound too
	f.press(0)
	f.drag(fyne.NewPos(f.x(320)-2, y), fyne.NewPos(f.x(300)-2, y))
	b, _, _ = f.s.Project.Clip(B)
	s, _, _ = f.s.Project.Clip(sound)
	if b.Length != 230 || s.Length != 230 {
		t.Fatalf("B len %d, sound len %d; want 230 both (errors %v)", b.Length, s.Length, f.errs)
	}
}

// A background edit (an import finishing) during a clip drag keeps the drag going when
// the dragged clips are untouched, and never turns the rest of it into a rubber band.
func TestBackgroundEditDuringDrag(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	A, B := f.clipID("V1", 0), f.clipID("V1", 1)
	f.s.SelectClips([]string{A, B})
	y := f.rowMid("V1")
	from := fyne.NewPos(f.x(50), y)
	step := func(to fyne.Position, prev fyne.Position) {
		f.tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-prev.X, to.Y-prev.Y)})
	}
	f.press(0)
	mid := fyne.NewPos(f.x(100), y)
	step(mid, from)
	_ = f.s.Edit("Medien importieren", func(p *model.Project) error {
		p.AddMedia(model.MediaItem{ID: "neu", Name: "neu.wav", Path: "/x/neu.wav", Kind: model.KindAudio})
		return nil
	})
	end := fyne.NewPos(f.x(170), y)
	step(end, mid)
	f.tl.DragEnd()
	a, _, _ := f.s.Project.Clip(A)
	b, _, _ := f.s.Project.Clip(B)
	if a == nil || b == nil || a.Start != 120 || b.Start != 220 || len(f.s.Selected) != 2 {
		t.Fatalf("A %v B %v selected %v", a, b, f.s.Selected)
	}

	// an edit that changes the dragged clips cancels the move, and the rest of the
	// gesture is swallowed instead of starting a rubber band
	f.press(0)
	from = fyne.NewPos(f.x(150), y)
	mid = fyne.NewPos(f.x(160), y)
	step(mid, from)
	_ = f.s.Edit("trim", func(p *model.Project) error { return p.TrimClip(A, 120, 50) })
	step(fyne.NewPos(f.x(400), f.rowMid("A1")), mid)
	f.tl.DragEnd()
	if len(f.s.Selected) != 2 {
		t.Fatalf("selection lost: %v", f.s.Selected)
	}
	if b, _, _ := f.s.Project.Clip(B); b.Start != 220 {
		t.Fatalf("cancelled move still moved B to %d", b.Start)
	}
}

// Moving a clip to another track takes the selected track along.
func TestSelectedTrackFollowsMovedClip(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	B := f.clipID("V1", 1)
	f.tap(fyne.NewPos(f.x(120), f.rowMid("V1")))
	if f.s.SelectedClip != B || f.s.SelectedTrack != f.track("V1").ID {
		t.Fatal("click must select B on V1")
	}
	f.press(0)
	f.drag(fyne.NewPos(f.x(120), f.rowMid("V1")), fyne.NewPos(f.x(320), f.rowMid("V2")))
	if _, ti, _ := f.s.Project.Clip(B); f.s.Project.Tracks[ti].Name != "V2" {
		t.Fatal("B must be on V2")
	}
	if f.s.SelectedTrack != f.track("V2").ID {
		t.Fatalf("selected track %s, want V2", f.s.SelectedTrack)
	}
}

// A rubber band that reaches the audio tracks keeps a picture clip primary.
func TestMarqueeKeepsPicturePrimary(t *testing.T) {
	f := setup(t, 1000, 300)
	f.tl.SetSnapping(false)
	f.place("A1", model.NewClip(model.KindAudio, "Ton", 0, 100))
	f.press(0)
	f.drag(fyne.NewPos(f.x(160), f.rowMid("V2")), fyne.NewPos(f.x(10), f.rowMid("A1")))
	c, ok := f.s.Clip()
	if len(f.s.Selected) != 3 || !ok || !c.Kind.IsVisual() {
		t.Fatalf("selection %v, primary %+v", f.s.Selected, c)
	}
}

// blockingSource holds every load until released and counts the loads started.
type blockingSource struct {
	mu      sync.Mutex
	started int
	release chan struct{}
}

func (b *blockingSource) Thumbnail(model.MediaItem, int, int) (image.Image, error) {
	b.mu.Lock()
	b.started++
	b.mu.Unlock()
	<-b.release
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (b *blockingSource) Waveform(model.MediaItem, int) ([]float32, error) { return nil, nil }

func (b *blockingSource) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started
}

// Loads queued behind the loader's semaphore do not start once the timeline closed.
func TestLoaderStopsQueuedLoadsOnClose(t *testing.T) {
	test.NewTempApp(t)
	dir := t.TempDir()
	src := &blockingSource{release: make(chan struct{})}
	l := newLoader(src, func() {})
	for i := range 9 {
		path := filepath.Join(dir, string(rune('a'+i))+".mov")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		l.get(model.MediaItem{ID: path, Path: path, Kind: model.KindVideo}, false, 0)
	}
	deadline := time.Now().Add(2 * time.Second)
	for src.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("loads did not start")
		}
		time.Sleep(time.Millisecond)
	}
	l.close()
	close(src.release)
	time.Sleep(100 * time.Millisecond)
	if n := src.count(); n != 3 {
		t.Fatalf("%d loads started, want only the 3 running before close", n)
	}
}
