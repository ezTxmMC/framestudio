package audio

import (
	"image/png"
	"math"
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// mixerHarness is a standalone mixer panel (no audio page) with fake meters.
type mixerHarness struct {
	harness
	mp *mixerPanel
}

func newMixerHarness(t *testing.T, pr *model.Project) *mixerHarness {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	h := &mixerHarness{harness: harness{t: t, s: session.New(pr)}}
	ctx := &page.Context{Session: h.s, ShowErr: func(err error) { h.errs = append(h.errs, err) }}
	p := New(ctx).(*Page)
	p.spawn = func(f func()) { h.jobs = append(h.jobs, f) }
	h.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return h.now }
	p.levels = func(*session.Session) (liveaudio.Level, map[string]liveaudio.Level) {
		if h.level != nil {
			return h.level()
		}
		return liveaudio.Level{}, nil
	}
	p.clock = func(tick func()) func() {
		h.clocks++
		h.tick = tick
		return func() { h.tick = nil }
	}
	h.p = p
	h.mp = mixerPanelOf(p)
	h.w = test.NewTempWindow(t, h.mp.Content())
	ctx.Window = h.w
	h.w.Resize(fyne.NewSize(760, 420))
	t.Cleanup(h.mp.Close)
	return h
}

func TestMixerPanelStandalone(t *testing.T) {
	h := newMixerHarness(t, demoProject())
	p := h.p
	if p.tracks != nil || p.clip != nil || p.play != nil {
		t.Fatal("the mixer panel builds only the mixer")
	}
	if len(p.mixer.strips) != 4 || p.mixer.master == nil {
		t.Fatalf("%d strips", len(p.mixer.strips))
	}
	music := audioTrack(h.s, 1).ID
	if st := p.mixer.strips[music]; math.Abs(st.fader.Value+4.5) > 0.01 {
		t.Fatalf("fader shows %v dB", st.fader.Value)
	}
	// Faders and mute write to the session like on the page.
	p.mixer.strips[music].fader.SetValue(-10)
	if db := LinearToDB(audioTrack(h.s, 1).Volume); math.Abs(db+10) > 0.01 {
		t.Fatalf("volume %v dB", db)
	}
	test.Tap(p.mixer.strips[music].mute)
	if !audioTrack(h.s, 1).Muted {
		t.Fatal("mute from the panel")
	}
	// Edits elsewhere show in the panel.
	_ = h.s.Edit("Spur", func(pr *model.Project) error { pr.AddTrack(model.TrackAudio); return nil })
	if len(p.mixer.strips) != 5 {
		t.Fatal("new track should get a strip")
	}
	// Live meters run while playing, the playhead moves without lanes.
	dialog := audioTrack(h.s, 0).ID
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		l := liveaudio.Level{Peak: [2]float32{0.5, 0.5}}
		return l, map[string]liveaudio.Level{dialog: l}
	}
	h.s.SetPlaying(true)
	if h.tick == nil {
		t.Fatal("playback must start the meters of the panel")
	}
	h.advance(time.Second / 30)
	h.s.Seek(40)
	if p.mixer.strips[dialog].meter.state.resting() || p.mixer.master.meter.state.resting() {
		t.Fatal("strip and master meters must move")
	}
	if !h.mp.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace}) || !h.s.Playing {
		t.Fatal("Space plays in the panel")
	}
	h.mp.Close()
	if h.tick != nil {
		t.Fatal("closing the panel stops the meters")
	}
	pi, ok := page.PanelByID(page.PanelMixer)
	if !ok {
		t.Fatal("mixer panel not registered")
	}
	other := pi.New(&page.Context{Session: h.s})
	other.Close()
}

func TestAudioCommands(t *testing.T) {
	h := newHarness(t, demoProject())
	r := command.NewRegistry()
	// the shell's global binding exists before the page binds its own
	r.Bind(command.Global, command.Binding{ID: "play.toggle", Run: func() { t.Fatal("global play.toggle ran") }})
	scope := command.PageScope(page.Audio)
	r.Bind(scope, h.p.Commands()...)
	r.SetScopeActive(scope, true)
	before := len(h.s.Project.TrackIndices(model.TrackAudio))
	r.Run("timeline.addAudioTrack")
	if len(h.s.Project.TrackIndices(model.TrackAudio)) != before+1 {
		t.Fatal("addAudioTrack on the audio page")
	}
	r.Run("timeline.zoomIn")
	if h.p.view.zoom <= 1 {
		t.Fatal("zoomIn")
	}
	r.Run("timeline.zoomFit")
	if h.p.view.zoom != 1 {
		t.Fatal("zoomFit")
	}
	c, ok := r.Get("audio.preview")
	if !ok || !c.Available || !c.Toggle || c.Label != "Vorhören" || c.Checked {
		t.Fatalf("audio.preview %+v", c)
	}
	// Space (play.toggle) keeps meaning Vorhören on this page: page scope wins.
	h.s.SetPlaying(true)
	if !r.Dispatch(fyne.KeySpace, 0) || h.s.Playing {
		t.Fatal("Space should stop playback through the page binding")
	}
}

func TestAudioLayoutRoundTrip(t *testing.T) {
	h := newHarness(t, demoProject())
	h.p.main.SetOffset(0.62)
	h.p.bottom.SetOffset(0.3)
	h.p.SetAreaVisible(areaMixer, false)
	l := h.p.LayoutState()
	if l.Splits[splitMain] != 0.62 || l.Splits[splitBottom] != 0.3 || !l.Hidden[areaMixer] || l.Hidden[areaClip] {
		t.Fatalf("state %+v", l)
	}
	h.p.SetAreaVisible(areaClip, false)
	if h.p.bottom.Visible() {
		t.Fatal("with clip and mixer hidden the bottom row goes away")
	}
	h.p.ApplyLayout(layout.Page{})
	if h.p.main.Offset != defaultMainSplit || h.p.bottom.Offset != defaultBottomSplit ||
		!h.p.mixerBox.Visible() || !h.p.clipBox.Visible() || !h.p.bottom.Visible() {
		t.Fatal("empty layout restores the defaults")
	}
	h.p.ApplyLayout(l)
	if got := h.p.LayoutState(); got.Splits[splitMain] != 0.62 || !got.Hidden[areaMixer] {
		t.Fatalf("round trip %+v", got)
	}
	p2 := New(h.p.ctx).(*Page)
	p2.ApplyLayout(l)
	if !p2.LayoutState().Hidden[areaMixer] {
		t.Fatal("pending layout lost")
	}
	p2.Content()
	defer p2.Close()
	if p2.mixerBox.Visible() || p2.main.Offset != 0.62 {
		t.Fatal("pending layout not applied")
	}
}

// TestMixerPanelScreenshot writes the standalone mixer to /tmp/fs-r3-panels-mixer.png.
func TestMixerPanelScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render")
	}
	h := newMixerHarness(t, demoProject())
	ids := h.s.Project.TrackIndices(model.TrackAudio)
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		m := map[string]liveaudio.Level{}
		for i, ti := range ids {
			v := float32([]float64{0.7, 0.35, 0.08, 0.02}[i%4])
			m[h.s.Project.Tracks[ti].ID] = liveaudio.Level{Peak: [2]float32{v, v * 0.85}, RMS: [2]float32{v * 0.6, v * 0.5}}
		}
		return liveaudio.Level{Peak: [2]float32{0.8, 0.72}, RMS: [2]float32{0.5, 0.45}}, m
	}
	h.s.SetPlaying(true)
	for i := 0; i < 10; i++ {
		h.advance(time.Second / 30)
	}
	out, err := os.Create("/tmp/fs-r3-panels-mixer.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, h.w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
