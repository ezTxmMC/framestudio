package color

import (
	"image"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// fixture is a page on a project with V1: video 0-50, colour 50-100, image 100-150;
// V2: title 20-40; A1: audio 0-80.
type fixture struct {
	p                          *Page
	s                          *session.Session
	w                          fyne.Window
	video, colr, img, title, a string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())

	pr := model.NewProject("Test")
	v1, v2, a1 := pr.Tracks[0].ID, pr.Tracks[1].ID, pr.Tracks[2].ID
	vid := pr.AddMedia(model.MediaItem{Name: "Interview.mov", Path: "/nicht/da.mov", Kind: model.KindVideo,
		Width: 1920, Height: 1080, Duration: 4e9, FPS: 25, HasAudio: true})
	still := pr.AddMedia(model.MediaItem{Name: "Logo.png", Path: "/nicht/da.png", Kind: model.KindImage, Width: 800, Height: 600})
	m, _ := pr.MediaByID(vid)
	cv := pr.ClipFromMedia(*m, 0, 50)
	cv.Length = 50
	cc := model.NewClip(model.KindColor, "Schwarzblende", 50, 50)
	cc.Color = model.RGBA{R: 0xB8, G: 0x87, B: 0x3F, A: 0xFF}
	mi, _ := pr.MediaByID(still)
	ci := pr.ClipFromMedia(*mi, 100, 50)
	ct := model.NewClip(model.KindTitle, "Bauchbinde", 20, 20)
	ca := pr.ClipFromMedia(*m, 0, 80)
	ca.Kind, ca.Length = model.KindAudio, 80
	for _, e := range []struct {
		track string
		c     model.Clip
	}{{v1, cv}, {v1, cc}, {v1, ci}, {v2, ct}, {a1, ca}} {
		if err := pr.PlaceClip(e.track, e.c); err != nil {
			t.Fatal(err)
		}
	}

	s := session.New(pr)
	cache := media.NewCache()
	ctx := &page.Context{Session: s, Renderer: render.New(cache), Media: cache,
		ShowErr: func(err error) { t.Logf("ShowErr: %v", err) }}
	p := New(ctx).(*Page)
	p.async = func(f func()) { f() }
	w := test.NewTempWindow(t, p.Content())
	w.Resize(fyne.NewSize(1440, 860))
	t.Cleanup(p.Close)
	return &fixture{p: p, s: s, w: w, video: cv.ID, colr: cc.ID, img: ci.ID, title: ct.ID, a: ca.ID}
}

func (f *fixture) grade(id string) model.Grade {
	c, _, _ := f.s.Project.Clip(id)
	return c.Grade
}

func TestSliderEditIsOneUndoStep(t *testing.T) {
	f := newFixture(t)
	f.s.SelectClip(f.colr)
	row := f.p.primary.rows[0] // Belichtung
	for _, v := range []float64{0.1, 0.2, 0.35} {
		row.slider.SetValue(v)
	}
	if g := f.grade(f.colr); g.Exposure != 0.35 {
		t.Fatalf("exposure = %v, want 0.35", g.Exposure)
	}
	if row.readout.Text != "+0,35" {
		t.Errorf("readout = %q", row.readout.Text)
	}
	f.p.primary.rows[2].slider.SetValue(-0.5) // Sättigung is its own step
	if f.s.UndoLabel() != "Sättigung ändern" {
		t.Errorf("undo label = %q", f.s.UndoLabel())
	}
	f.s.Undo()
	f.s.Undo()
	if g := f.grade(f.colr); !g.IsNeutral() {
		t.Fatalf("after two undos grade = %+v, want neutral", g)
	}
	if row.slider.Value != 0 || f.s.CanUndo() {
		t.Errorf("slider = %v, canUndo = %v", row.slider.Value, f.s.CanUndo())
	}
}

func TestResetButtons(t *testing.T) {
	f := newFixture(t)
	f.s.SelectClip(f.video)
	f.p.primary.rows[3].slider.SetValue(0.4)
	f.p.primary.rows[5].slider.SetValue(0.6)
	test.Tap(f.p.primary.rows[3].reset)
	if g := f.grade(f.video); g.Temperature != 0 || g.Vignette != 0.6 {
		t.Fatalf("after field reset: %+v", g)
	}
	test.Tap(f.p.primary.resetAll)
	if !f.grade(f.video).IsNeutral() || !f.p.primary.resetAll.Disabled() {
		t.Fatal("grade reset should neutralise and disable the button")
	}
}

func TestCopyPasteGrade(t *testing.T) {
	f := newFixture(t)
	pp := f.p.primary
	f.s.SelectClip(f.video)
	if !pp.pasteBtn.Disabled() {
		t.Fatal("paste must be disabled before copying")
	}
	pp.rows[1].slider.SetValue(0.25)
	test.Tap(pp.copyBtn)
	f.s.SelectClip(f.img)
	if pp.pasteBtn.Disabled() {
		t.Fatal("paste should be enabled after copying")
	}
	test.Tap(pp.pasteBtn)
	if g := f.grade(f.img); g.Contrast != 0.25 {
		t.Fatalf("pasted grade = %+v", g)
	}
	if f.s.UndoLabel() != "Grade einfügen" {
		t.Errorf("undo label = %q", f.s.UndoLabel())
	}
}

func TestApplyToAll(t *testing.T) {
	f := newFixture(t)
	f.s.SelectClip(f.title)
	f.p.primary.rows[4].slider.SetValue(-0.3)
	want := f.grade(f.title)
	undoDepth := 0
	for f.s.CanUndo() {
		f.s.Undo()
		undoDepth++
	}
	for range undoDepth {
		f.s.Redo()
	}
	test.Tap(f.p.primary.applyAll)
	for _, id := range []string{f.video, f.colr, f.img, f.title} {
		if g := f.grade(id); g != want {
			t.Errorf("clip %s grade = %+v, want %+v", id, g, want)
		}
	}
	if !f.grade(f.a).IsNeutral() {
		t.Error("audio clip must not be graded")
	}
	f.s.Undo()
	if !f.grade(f.video).IsNeutral() || f.grade(f.title) != want {
		t.Error("apply to all should undo in one step")
	}
	// the strip marks graded clips with a steel dot
	for i, e := range f.p.entries {
		graded := f.p.cards[i].gradeDot.FillColor == studiotheme.Steel
		if graded != !e.Clip.Grade.IsNeutral() {
			t.Errorf("card %d dot = %v", i, graded)
		}
	}
}

func TestStripListsVisualClipsInTimeOrder(t *testing.T) {
	f := newFixture(t)
	want := []string{f.video, f.title, f.colr, f.img}
	if len(f.p.entries) != len(want) || len(f.p.stripBox.Objects) != len(want) {
		t.Fatalf("strip has %d entries, %d cards; want %d", len(f.p.entries), len(f.p.stripBox.Objects), len(want))
	}
	for i, id := range want {
		if f.p.entries[i].Clip.ID != id {
			t.Errorf("entry %d = %s (%s), want %s", i, f.p.entries[i].Clip.Name, f.p.entries[i].Clip.ID, id)
		}
	}
	if f.p.cards[1].track.Text != "V2" || f.p.cards[1].number.Text != "02" || f.p.cards[1].timecode.Text != "00:00:00:20" {
		t.Errorf("card 2 = %s %s %s", f.p.cards[1].number.Text, f.p.cards[1].track.Text, f.p.cards[1].timecode.Text)
	}
	if !f.p.cards[0].missing.Visible() {
		t.Error("video card with undecodable media should show the missing icon")
	}
	if f.p.stripCount.Text != "4 Clips" {
		t.Errorf("count = %q", f.p.stripCount.Text)
	}

	// deleting clips updates the strip
	_ = f.s.Edit("del", func(p *model.Project) error { return p.DeleteClip(f.title, false) })
	if len(f.p.stripBox.Objects) != 3 {
		t.Errorf("after delete: %d cards", len(f.p.stripBox.Objects))
	}
}

func TestEmptyProject(t *testing.T) {
	app := test.NewTempApp(t)
	app.Settings().SetTheme(studiotheme.New())
	s := session.New(model.NewProject("Leer"))
	p := New(&page.Context{Session: s}).(*Page)
	w := test.NewTempWindow(t, p.Content())
	w.Resize(fyne.NewSize(1200, 800))
	p.Activate()
	for _, k := range []fyne.KeyName{fyne.KeyUp, fyne.KeyDown, fyne.KeyLeft, fyne.KeySpace} {
		p.TypedKey(&fyne.KeyEvent{Name: k})
	}
	if !p.stripEmpty.Visible() || !p.primary.empty.Visible() || p.primary.grading.Visible() {
		t.Error("empty project should show both empty states")
	}
	p.Close()
}

func TestTapCardSelectsAndSeeks(t *testing.T) {
	f := newFixture(t)
	test.Tap(f.p.cards[2])
	if f.s.SelectedClip != f.colr || f.s.Playhead != 50 {
		t.Fatalf("selected %s at %d", f.s.SelectedClip, f.s.Playhead)
	}
	if !f.p.cards[2].selected || f.p.cards[0].selected {
		t.Error("card selection state not updated")
	}
	if f.p.primary.clipName.Text != "Schwarzblende" {
		t.Errorf("panel shows %q", f.p.primary.clipName.Text)
	}
}

func TestAutoSelectAndAudioEmptyState(t *testing.T) {
	f := newFixture(t)
	f.s.Seek(25)
	if f.s.SelectedClip != "" {
		t.Fatal("inactive page must not change the selection")
	}
	f.p.Activate()
	if f.s.SelectedClip != f.title {
		t.Fatalf("auto-selected %q, want title on V2", f.s.SelectedClip)
	}
	if !f.p.primary.grading.Visible() || f.p.primary.empty.Visible() {
		t.Error("grading controls should be shown for a visual clip")
	}

	f.s.SelectClip(f.a)
	if f.p.primary.grading.Visible() || !f.p.primary.empty.Visible() {
		t.Error("audio selection should show the empty state")
	}

	// hidden tracks are skipped
	f.s.Project.Tracks[1].Hidden = true
	f.s.SelectClip("")
	if f.s.SelectedClip != f.video {
		t.Errorf("with V2 hidden auto-selected %q, want video on V1", f.s.SelectedClip)
	}
}

func TestTypedKeys(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	key := func(k fyne.KeyName) {
		if !f.p.TypedKey(&fyne.KeyEvent{Name: k}) {
			t.Fatalf("key %s not handled", k)
		}
	}
	key(fyne.KeyRight)
	key(fyne.KeyRight)
	key(fyne.KeyLeft)
	if f.s.Playhead != 1 {
		t.Errorf("playhead = %d, want 1", f.s.Playhead)
	}
	key(fyne.KeyDown)
	if f.s.SelectedClip != f.title || f.s.Playhead != 20 {
		t.Errorf("down: %s at %d", f.s.SelectedClip, f.s.Playhead)
	}
	key(fyne.KeyDown)
	key(fyne.KeyDown)
	key(fyne.KeyDown) // stays on the last clip
	if f.s.SelectedClip != f.img {
		t.Errorf("down x3: %s", f.s.SelectedClip)
	}
	key(fyne.KeyUp)
	if f.s.SelectedClip != f.colr || f.s.Playhead != 50 {
		t.Errorf("up: %s at %d", f.s.SelectedClip, f.s.Playhead)
	}
	key(fyne.KeySpace)
	if !f.s.Playing {
		t.Error("space should start playback")
	}
	key(fyne.KeySpace)
	if f.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyA}) {
		t.Error("unrelated keys must not be handled")
	}
}

func TestLockedTrack(t *testing.T) {
	f := newFixture(t)
	_ = f.s.Edit("lock", func(p *model.Project) error { p.Tracks[0].Locked = true; return nil })
	f.s.SelectClip(f.video)
	if !f.p.primary.rows[0].slider.Disabled() || f.p.primary.lockHint.Text == "" {
		t.Error("sliders should be disabled on a locked track")
	}
}

func TestCompareToggle(t *testing.T) {
	f := newFixture(t)
	test.Tap(f.p.compareBtn)
	if !f.p.compare || !f.p.compareBadge.Visible() {
		t.Error("compare should be on")
	}
	test.Tap(f.p.compareBtn)
	if f.p.compare || f.p.compareBadge.Visible() {
		t.Error("compare should be off")
	}
}

func TestScopesFromViewerFrames(t *testing.T) {
	f := newFixture(t)
	f.p.Activate()
	f.p.setScope(scopeWaveform)
	f.p.onFrame(testFrame(320, 180))
	img, ok := f.p.scopeImg.Image.(*image.NRGBA)
	if !ok || img.Rect.Dx() != scopeW || f.p.scopeWaiting.Visible() {
		t.Fatal("waveform should be shown after a frame arrives")
	}
	f.p.setScope(scopeVectorscope)
	if img := f.p.scopeImg.Image.(*image.NRGBA); img.Rect.Dx() != vectorSize {
		t.Errorf("vectorscope size %v", img.Rect)
	}
}

func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-color.png")
	}
	f := newFixture(t)
	f.p.Activate()
	f.s.SelectClip(f.colr)
	f.p.primary.rows[0].slider.SetValue(0.18)
	f.p.primary.rows[3].slider.SetValue(-0.32)
	f.p.primary.rows[5].slider.SetValue(0.4)
	test.Tap(f.p.primary.copyBtn)
	f.p.setScope(scopeWaveform)
	f.p.onFrame(testFrame(640, 360))
	out, err := os.Create("/tmp/fs-color.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, f.w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
