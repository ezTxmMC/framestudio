package shell

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/audio"
	"framestudio-proto/src/model"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/page"
	editpage "framestudio-proto/src/ui/pages/edit"
	"framestudio-proto/src/ui/timeline"
)

// rtOutput is a fake sound device that consumes 16-bit stereo PCM in real time, so the
// live engine runs at its normal pace without opening the sound card.
type rtOutput struct {
	closed atomic.Bool
	wrote  atomic.Int64
}

func (o *rtOutput) Write(b []byte) (int, error) {
	if !o.closed.Load() {
		time.Sleep(time.Duration(len(b)/4) * time.Second / audio.SampleRate)
	}
	o.wrote.Add(int64(len(b)))
	return len(b), nil
}
func (o *rtOutput) Latency() time.Duration { return 0 }
func (o *rtOutput) Close() error           { o.closed.Store(true); return nil }

// fakeSound is the output of the last useFakeSound (tests in this package run serially).
var fakeSound *rtOutput

// useFakeSound makes the shell's live audio play into an rtOutput.
func useFakeSound(t *testing.T) *rtOutput {
	out := &rtOutput{}
	fakeSound = out
	restore := liveaudio.UseEngine(func() *audio.Engine {
		return audio.New(audio.Config{Open: func() (audio.Output, error) { return out, nil }})
	})
	t.Cleanup(restore)
	return out
}

// findTimeline returns the timeline widget shown in w.
func findTimeline(w fyne.Window) *timeline.Timeline {
	var found *timeline.Timeline
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if found != nil || o == nil || !o.Visible() {
			return
		}
		switch x := o.(type) {
		case *timeline.Timeline:
			found = x
			return
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(x).Objects() {
				walk(c)
			}
		}
	}
	walk(w.Canvas().Content())
	return found
}

func trackID(t *testing.T, p *model.Project, name string) string {
	t.Helper()
	for _, tr := range p.Tracks {
		if tr.Name == name {
			return tr.ID
		}
	}
	t.Fatalf("no track %s", name)
	return ""
}

func clipByName(t *testing.T, p *model.Project, name string) model.Clip {
	t.Helper()
	for _, tr := range p.Tracks {
		for _, c := range tr.Clips {
			if c.Name == name {
				return c
			}
		}
	}
	t.Fatalf("no clip %s", name)
	return model.Clip{}
}

// TestIntegrationRound2 drives the round-2 flows through the real shell and pages:
// marquee and group move, roll and ripple trims, dropping media from the pool onto the
// timeline, J/K/L shuttle, Ctrl+A and live meters.
func TestIntegrationRound2(t *testing.T) {
	sh, w, q := newIntegrationShell(t)
	out := fakeSound
	still, video, tone := demoMedia(t, t.TempDir())
	if video == "" {
		t.Skip("ffmpeg fehlt")
	}
	proj := demoProject(t, still, video, tone)
	dir, err := store.ProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	path := store.SuggestPath(dir, proj.Name)
	if err := store.Save(proj, path); err != nil {
		t.Fatal(err)
	}
	sh.Start([]string{path})
	s := sh.Active()
	if s == nil {
		t.Fatal("project did not open")
	}
	sh.showPageID(page.Edit)
	q.Settle(200*time.Millisecond, 10*time.Second)
	tl := findTimeline(w)
	if tl == nil {
		t.Fatal("no timeline on the edit page")
	}
	key := func(k fyne.KeyName) { sh.typedKey(&fyne.KeyEvent{Name: k}) }
	at := func(frame float64, track string) fyne.Position {
		t.Helper()
		pos, ok := tl.LanePoint(frame, trackID(t, s.Project, track))
		if !ok {
			t.Fatalf("track %s not shown", track)
		}
		return pos
	}
	drag := func(from, to fyne.Position) {
		tl.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: from}, Button: desktop.MouseButtonPrimary})
		mid := fyne.NewPos((from.X+to.X)/2, (from.Y+to.Y)/2)
		tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: mid}, Dragged: fyne.NewDelta(mid.X-from.X, mid.Y-from.Y)})
		tl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-mid.X, to.Y-mid.Y)})
		tl.DragEnd()
		q.Settle(30*time.Millisecond, 2*time.Second)
	}

	// Marquee over empty V2 space down to A1 selects the still on V1 and the music on A1.
	img, music := clipByName(t, s.Project, "Logo"), clipByName(t, s.Project, "Musik")
	if img.Start != 200 || music.Start != 0 {
		t.Fatalf("demo layout changed: still %d, music %d", img.Start, music.Start)
	}
	key(fyne.KeyA)
	drag(at(240, "V2"), at(210, "A1"))
	if !slices.Equal(s.Selected, []string{img.ID, music.ID}) && !slices.Equal(s.Selected, []string{music.ID, img.ID}) {
		t.Fatalf("marquee selected %v, want still and music", s.Selected)
	}
	// Group move: grab the still and drag it 40 frames right; the music follows.
	drag(at(250, "V1"), at(290, "V1"))
	if img, music = clipByName(t, s.Project, "Logo"), clipByName(t, s.Project, "Musik"); img.Start != 240 || music.Start != 40 {
		t.Fatalf("group move: still at %d, music at %d; want 240 and 40", img.Start, music.Start)
	}
	if s.UndoLabel() != "Clips verschieben" {
		t.Fatalf("undo label %q", s.UndoLabel())
	}

	// Roll (N): move the cut between the colour matte and the video from 50 to 60.
	key(fyne.KeyN)
	if tl.Tool() != timeline.ToolRoll {
		t.Fatal("N must pick the roll tool")
	}
	drag(at(50, "V1").SubtractXY(2, 0), at(60, "V1").SubtractXY(2, 0))
	bg, vid := clipByName(t, s.Project, "Farbfläche"), clipByName(t, s.Project, "Testbild")
	if bg.Length != 60 || vid.Start != 60 || vid.End() != 200 || vid.SourceIn != 10 || s.UndoLabel() != "Schnitt verschieben" {
		t.Fatalf("roll: matte %+v, video start %d end %d in %d (%q)", bg.Length, vid.Start, vid.End(), vid.SourceIn, s.UndoLabel())
	}
	// Ripple (R): pull the matte's end back to 50; everything after it on V1 follows.
	key(fyne.KeyR)
	drag(at(60, "V1").SubtractXY(2, 0), at(50, "V1").SubtractXY(2, 0))
	bg, vid, img = clipByName(t, s.Project, "Farbfläche"), clipByName(t, s.Project, "Testbild"), clipByName(t, s.Project, "Logo")
	if bg.Length != 50 || vid.Start != 50 || img.Start != 230 || s.UndoLabel() != "Ripple-Trimmen" {
		t.Fatalf("ripple: matte %d, video %d, still %d (%q)", bg.Length, vid.Start, img.Start, s.UndoLabel())
	}
	key(fyne.KeyA)

	// Drag the music from the pool onto A2 at frame 315 through the kit drag API.
	var toneID string
	for _, m := range s.Project.Media {
		if m.Name == "Musik" {
			toneID = m.ID
		}
	}
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(tl)
	drop := origin.Add(at(315, "A2"))
	kit.BeginMediaDrag(w, []string{toneID}, "Musik")
	kit.UpdateMediaDrag(origin.Add(at(280, "A2")))
	kit.UpdateMediaDrag(drop)
	if !kit.EndMediaDrag(drop) {
		t.Fatal("the timeline did not accept the drop")
	}
	q.Settle(30*time.Millisecond, 2*time.Second)
	a2 := trackID(t, s.Project, "A2")
	tr, _ := s.Project.Track(a2)
	var dropped *model.Clip
	for i := range tr.Clips {
		if tr.Clips[i].MediaID == toneID {
			dropped = &tr.Clips[i]
		}
	}
	if dropped == nil || dropped.Start != 315 || s.UndoLabel() != "Medien einfügen" {
		t.Fatalf("dropped clip %+v (%q), want the music on A2 at 315", dropped, s.UndoLabel())
	}
	if s.SelectedClip != dropped.ID {
		t.Fatal("the dropped clip should be selected")
	}

	// Ctrl+A selects every clip; Esc clears.
	typeShortcut(t, sh, &fyne.ShortcutSelectAll{})
	total := 0
	for _, tr := range s.Project.Tracks {
		total += len(tr.Clips)
	}
	if len(s.Selected) != total {
		t.Fatalf("Ctrl+A selected %d of %d clips", len(s.Selected), total)
	}
	key(fyne.KeyEscape)
	if len(s.Selected) != 0 {
		t.Fatal("Esc must clear the selection")
	}

	// J/K/L shuttle on the edit page.
	s.Seek(150)
	key(fyne.KeyL)
	if !s.Playing || s.Rate != 1 {
		t.Fatalf("L: playing %v rate %v", s.Playing, s.Rate)
	}
	key(fyne.KeyL)
	if s.Rate != 2 {
		t.Fatalf("L L: rate %v", s.Rate)
	}
	key(fyne.KeyK)
	if s.Playing || s.Rate != 1 {
		t.Fatalf("K: playing %v rate %v", s.Playing, s.Rate)
	}
	key(fyne.KeyJ)
	if !s.Playing || s.Rate != -1 {
		t.Fatalf("J: playing %v rate %v", s.Playing, s.Rate)
	}
	key(fyne.KeyK)

	// Live meters: the engine mixes the demo's sine tones while playing.
	s.Seek(150)
	s.SetPlaying(true)
	var master liveaudio.Level
	var tracks map[string]liveaudio.Level
	ok := q.Until(func() bool {
		master, tracks = liveaudio.Levels(s)
		return master.Peak[0] > 0.05 && tracks[trackID(t, s.Project, "A1")].Peak[0] > 0.05
	}, 20*time.Second)
	s.SetPlaying(false)
	if !ok {
		t.Fatalf("meters stayed at %+v (tracks %+v) while playing", master, tracks)
	}
	if out.wrote.Load() == 0 {
		t.Fatal("the live engine wrote nothing")
	}
}

// TestHelpCoversEditKeyMap keeps the shell's help dialog in step with the keys the edit
// page handles.
func TestHelpCoversEditKeyMap(t *testing.T) {
	help := map[string]bool{}
	for _, col := range shortcutColumns {
		for _, g := range col {
			for _, sc := range g.Keys {
				for _, k := range strings.Split(sc.Keys, " | ") {
					help[k] = true
				}
			}
		}
	}
	alias := map[string]string{"+": "Plus"}
	for _, g := range editpage.KeyMap {
		for _, k := range g.Keys {
			for _, key := range strings.Split(k[0], " / ") {
				if a, ok := alias[key]; ok {
					key = a
				}
				if !help[key] {
					t.Errorf("help dialog misses %q (%s)", key, k[1])
				}
			}
		}
	}
}
