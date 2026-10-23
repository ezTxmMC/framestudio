package color

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func registryFor(p *Page) *command.Registry {
	r := command.NewRegistry()
	scope := command.PageScope(page.Color)
	r.Bind(scope, p.Commands()...)
	r.SetScopeActive(scope, true)
	return r
}

func cmd(t *testing.T, r *command.Registry, id string) command.Command {
	t.Helper()
	c, ok := r.Get(id)
	if !ok || !c.Available {
		t.Fatalf("%s not bound", id)
	}
	return c
}

func TestGradeCommands(t *testing.T) {
	f := newFixture(t)
	r := registryFor(f.p)
	f.s.SelectClip(f.a) // audio: nothing to grade
	for _, id := range []string{"grade.reset", "grade.copy", "grade.paste", "grade.applySelection", "grade.applyAll"} {
		if cmd(t, r, id).Enabled {
			t.Errorf("%s enabled without a picture clip", id)
		}
	}
	f.s.SelectClip(f.video)
	if cmd(t, r, "grade.reset").Enabled || !cmd(t, r, "grade.copy").Enabled || cmd(t, r, "grade.paste").Enabled {
		t.Fatal("neutral grade: reset off, copy on, paste off before copying")
	}
	f.p.primary.rows[0].slider.SetValue(0.3)
	if !cmd(t, r, "grade.reset").Enabled {
		t.Fatal("reset should be enabled for a graded clip")
	}
	r.Run("grade.copy")
	if !cmd(t, r, "grade.paste").Enabled {
		t.Fatal("paste enabled after copying")
	}
	f.s.SelectClips([]string{f.img, f.colr})
	if !cmd(t, r, "grade.applySelection").Enabled {
		t.Fatal("applySelection needs two picture clips")
	}
	r.Run("grade.paste")
	if g := f.grade(f.colr); g.Exposure != 0.3 {
		t.Fatalf("paste via command: %+v", g)
	}
	r.Run("grade.applySelection")
	if g := f.grade(f.img); g.Exposure != 0.3 {
		t.Fatalf("applySelection via command: %+v", g)
	}
	r.Run("grade.reset")
	if !f.grade(f.colr).IsNeutral() {
		t.Fatal("reset via command")
	}
	r.Run("grade.applyAll")
	if !f.grade(f.video).IsNeutral() || !f.grade(f.title).IsNeutral() {
		t.Fatal("applyAll should copy the neutral grade to every picture clip")
	}
	// Locked track: no editing commands.
	f.s.SelectClip(f.video)
	f.p.primary.rows[0].slider.SetValue(0.2)
	_ = f.s.Edit("lock", func(p *model.Project) error { p.Tracks[0].Locked = true; return nil })
	if cmd(t, r, "grade.reset").Enabled || cmd(t, r, "grade.paste").Enabled {
		t.Fatal("locked track: reset and paste disabled")
	}
}

func TestCompareAndScopeToggles(t *testing.T) {
	f := newFixture(t)
	r := registryFor(f.p)
	c := cmd(t, r, "grade.compare")
	if !c.Toggle || c.Checked {
		t.Fatal("compare is an unchecked toggle")
	}
	r.Run("grade.compare")
	if !f.p.compare || !cmd(t, r, "grade.compare").Checked || !f.p.compareBadge.Visible() {
		t.Fatal("compare on via command")
	}
	test.Tap(f.p.compareBtn)
	if cmd(t, r, "grade.compare").Checked {
		t.Fatal("button and command share the state")
	}
	if !cmd(t, r, "scopes.histogram").Checked || cmd(t, r, "scopes.waveform").Checked {
		t.Fatal("histogram is the default scope")
	}
	r.Run("scopes.vectorscope")
	for id, want := range map[string]bool{"scopes.histogram": false, "scopes.waveform": false, "scopes.vectorscope": true} {
		if cmd(t, r, id).Checked != want {
			t.Errorf("%s checked = %v", id, !want)
		}
	}
	if f.p.sv.btns[scopeVectorscope].Importance != widget.HighImportance {
		t.Error("scope buttons follow the command")
	}
	// Up/Down step through the strip on this page.
	f.s.SelectClip(f.video)
	r.Run("play.nextEdit")
	if f.s.SelectedClip == f.video {
		t.Fatal("play.nextEdit should step to the next clip of the strip")
	}
}

func TestColorLayoutRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.p.top.SetOffset(0.45)
	f.p.setScope(scopeWaveform)
	f.p.SetAreaVisible(areaStrip, false)
	l := f.p.LayoutState()
	if l.Splits[splitTop] != 0.45 || l.Extra[extraScope] != "waveform" || !l.Hidden[areaStrip] || l.Hidden[areaScopes] {
		t.Fatalf("state %+v", l)
	}
	f.p.ApplyLayout(layout.Page{})
	if f.p.top.Offset != defaultTopSplit || f.p.sv.kind != scopeHistogram || !f.p.stripPanel.Visible() {
		t.Fatal("empty layout restores defaults")
	}
	f.p.ApplyLayout(layout.Page{Hidden: map[string]bool{areaScopes: true}})
	if f.p.sv.object.Visible() {
		t.Fatal("scopes hidden")
	}
	f.w.Resize(fyne.NewSize(1440, 860))
	if f.p.viewer.Size().Width < 1000 {
		t.Fatalf("viewer should take the scopes' width, has %.0f", f.p.viewer.Size().Width)
	}
	f.p.ApplyLayout(l)
	if got := f.p.LayoutState(); got.Splits[splitTop] != 0.45 || got.Extra[extraScope] != "waveform" ||
		!got.Hidden[areaStrip] || got.Hidden[areaScopes] {
		t.Fatalf("round trip %+v", got)
	}
	// A layout given before the page is built waits for it.
	p2 := New(f.p.ctx).(*Page)
	p2.ApplyLayout(l)
	if p2.LayoutState().Extra[extraScope] != "waveform" {
		t.Fatal("pending layout lost")
	}
	p2.Content()
	defer p2.Close()
	if p2.sv.kind != scopeWaveform || p2.stripPanel.Visible() {
		t.Fatal("pending layout not applied")
	}
}

// panelFixture is a scopes panel on a session with a bright colour clip, without any
// colour page.
func panelFixture(t *testing.T) (*scopesPanel, *session.Session, *kit.UIQueue, fyne.Window) {
	t.Helper()
	app := test.NewTempApp(t)
	q := kit.UseUIQueue(t)
	app.Settings().SetTheme(studiotheme.New())
	pr := model.NewProject("Scopes")
	c := model.NewClip(model.KindColor, "Orange", 0, 50)
	c.Color = model.RGBA{R: 0xE0, G: 0x80, B: 0x30, A: 0xFF}
	if err := pr.PlaceClip(pr.Tracks[0].ID, c); err != nil {
		t.Fatal(err)
	}
	c2 := model.NewClip(model.KindColor, "Blau", 50, 50)
	c2.Color = model.RGBA{R: 0x20, G: 0x40, B: 0xD0, A: 0xFF}
	if err := pr.PlaceClip(pr.Tracks[0].ID, c2); err != nil {
		t.Fatal(err)
	}
	s := session.New(pr)
	cache := media.NewCache()
	pi, ok := page.PanelByID(page.PanelScopes)
	if !ok {
		t.Fatal("scopes panel not registered")
	}
	sp := pi.New(&page.Context{Session: s, Renderer: render.New(cache), Media: cache}).(*scopesPanel)
	w := test.NewTempWindow(t, sp.Content())
	w.Resize(fyne.NewSize(560, 380))
	t.Cleanup(func() {
		sp.Close()
		if !sp.src.stopped(5 * time.Second) {
			t.Error("frame source goroutine still running after Close")
		}
	})
	return sp, s, q, w
}

func TestScopesPanelRendersWithoutPage(t *testing.T) {
	sp, s, q, _ := panelFixture(t)
	shown := func() *image.NRGBA {
		img, _ := sp.view.img.Image.(*image.NRGBA)
		return img
	}
	if !q.Until(func() bool { return shown() != nil }, 5*time.Second) {
		t.Fatal("the panel should show a scope of the playhead frame by itself")
	}
	if sp.view.waiting.Visible() {
		t.Fatal("waiting hint must hide once a scope is shown")
	}
	first := shown()
	// Seeking renders the new frame (another colour gives another scope).
	s.Seek(60)
	if !q.Until(func() bool { return shown() != first }, 5*time.Second) {
		t.Fatal("seek should update the scope")
	}
	if sp.tc.Text != s.Project.Timecode(60) {
		t.Errorf("timecode %q", sp.tc.Text)
	}
	// Keys switch the scope; the choice is remembered for the next panel.
	sp.TypedKey(&fyne.KeyEvent{Name: fyne.Key3})
	if sp.view.kind != scopeVectorscope || panelScope != scopeVectorscope {
		t.Fatal("3 picks the vectorscope")
	}
	if !q.Until(func() bool { img := shown(); return img != nil && img.Rect.Dx() == vectorSize }, 5*time.Second) {
		t.Fatal("vectorscope not computed from the latest frame")
	}
	sp.TypedKey(&fyne.KeyEvent{Name: fyne.Key2})
}

func TestFrameSourceLatestWinsAndThrottles(t *testing.T) {
	sp, s, q, _ := panelFixture(t)
	src := sp.src
	var got []int
	src.deliver = func(img *image.NRGBA) { got = append(got, img.Rect.Dx()) }
	q.Settle(50*time.Millisecond, 2*time.Second)
	got = nil
	// A burst of seeks yields far fewer frames than requests, the last one included.
	for f := 1; f <= 40; f++ {
		s.Seek(f)
	}
	q.Settle(150*time.Millisecond, 5*time.Second)
	if len(got) == 0 || len(got) >= 40 {
		t.Fatalf("%d frames for 40 seeks", len(got))
	}
	if src.last.f != 40 {
		t.Fatalf("last request frame %d", src.last.f)
	}
	if w, h := frameSize(s.Project); got[len(got)-1] != w || w > sourceW || h > sourceH {
		t.Fatalf("frame size %d (want %dx%d within %dx%d)", got[len(got)-1], w, h, sourceW, sourceH)
	}
	// Playback is throttled to about 10 frames a second.
	got = nil
	s.Seek(0)
	q.Settle(50*time.Millisecond, 2*time.Second)
	got = nil
	s.SetPlaying(true)
	start := time.Now()
	for time.Since(start) < 500*time.Millisecond {
		s.Advance(40 * time.Millisecond)
		q.Drain()
		time.Sleep(5 * time.Millisecond)
	}
	s.SetPlaying(false)
	q.Settle(150*time.Millisecond, 5*time.Second)
	if len(got) > 9 {
		t.Fatalf("%d frames in 0.5 s of playback, want about 5", len(got))
	}
}

// TestScopesPanelScreenshot writes the standalone scopes panel to /tmp/fs-r3-panels-scopes.png.
func TestScopesPanelScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render")
	}
	sp, _, q, w := panelFixture(t)
	sp.src.deliver = func(*image.NRGBA) {} // show a rich test frame instead of the flat colour
	q.Settle(50*time.Millisecond, 2*time.Second)
	sp.view.setKind(scopeWaveform)
	sp.view.submit(testFrame(960, 540))
	q.Until(func() bool { return sp.view.img.Image != nil }, 5*time.Second)
	w.Resize(fyne.NewSize(620, 400))
	out, err := os.Create("/tmp/fs-r3-panels-scopes.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
	sp.view.setKind(scopeVectorscope)
	q.Until(func() bool {
		img, _ := sp.view.img.Image.(*image.NRGBA)
		return img != nil && img.Rect.Dx() == vectorSize
	}, 5*time.Second)
	out2, err := os.Create("/tmp/fs-r3-panels-scopes-vector.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out2.Close()
	if err := png.Encode(out2, w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

// Inside the shell (a registry exists) the page leaves keys to the registry, which knows
// the modifiers and the user's key map.
func TestKeysGoToRegistryInShell(t *testing.T) {
	f := newFixture(t)
	f.p.ctx.Commands = command.NewRegistry()
	f.s.Seek(10)
	for _, k := range []fyne.KeyName{fyne.KeySpace, fyne.KeyLeft, fyne.KeyRight, fyne.KeyUp, fyne.KeyDown} {
		if f.p.TypedKey(&fyne.KeyEvent{Name: k}) {
			t.Fatalf("%s used by the page", k)
		}
	}
	if f.s.Playing || f.s.Playhead != 10 {
		t.Fatal("the page acted on a key the registry owns")
	}
}
