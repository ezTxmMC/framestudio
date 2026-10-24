package audio

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/liveaudio"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func TestDBLinearMapping(t *testing.T) {
	cases := []struct{ db, lin float64 }{{0, 1}, {-6, 0.501187}, {6, 1.995262}, {MaxDB, 1000}, {-20, 0.1}}
	for _, c := range cases {
		if got := DBToLinear(c.db); math.Abs(got-c.lin) > 1e-5 {
			t.Errorf("DBToLinear(%v) = %v, want %v", c.db, got, c.lin)
		}
		if got := LinearToDB(c.lin); math.Abs(got-c.db) > 1e-4 {
			t.Errorf("LinearToDB(%v) = %v, want %v", c.lin, got, c.db)
		}
	}
	if DBToLinear(MinDB) != 0 || DBToLinear(-90) != 0 {
		t.Error("bottom of the fader must be silence")
	}
	if LinearToDB(0) != MinDB || LinearToDB(1e-9) != MinDB || LinearToDB(10000) != MaxDB {
		t.Error("LinearToDB must clamp to the fader range")
	}
	if DBToLinear(90) != DBToLinear(MaxDB) {
		t.Error("DBToLinear must clamp at MaxDB")
	}
}

func TestFormatDB(t *testing.T) {
	cases := map[float64]string{0: "0,0 dB", -0.04: "0,0 dB", -3.5: "-3,5 dB", 6: "+6,0 dB", MinDB: "-∞ dB"}
	for in, want := range cases {
		if got := FormatDB(in); got != want {
			t.Errorf("FormatDB(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPickPlayer(t *testing.T) {
	installed := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, x := range names {
				if x == n {
					return "/usr/bin/" + n, nil
				}
			}
			return "", exec.ErrNotFound
		}
	}
	cases := []struct {
		have []string
		want string
	}{
		{[]string{"ffplay", "pw-play", "paplay"}, "ffplay"},
		{[]string{"pw-play", "paplay"}, "pw-play"},
		{[]string{"paplay"}, "paplay"},
	}
	for _, c := range cases {
		got, err := pickPlayer(installed(c.have...), "/tmp/a.wav")
		if err != nil || got.Name != c.want || got.Path != "/usr/bin/"+c.want {
			t.Errorf("%v: got %+v, %v; want %s", c.have, got, err, c.want)
		}
		if got.Args[len(got.Args)-1] != "/tmp/a.wav" {
			t.Errorf("%s: file must be the last argument, got %v", got.Name, got.Args)
		}
	}
	ff, _ := pickPlayer(installed("ffplay"), "x.wav")
	if ff.Args[0] != "-nodisp" || ff.Args[1] != "-autoexit" {
		t.Errorf("ffplay must run without window and exit at the end: %v", ff.Args)
	}
	if _, err := pickPlayer(installed(), "x.wav"); !errors.Is(err, errNoPlayer) {
		t.Errorf("no player: got %v", err)
	}
}

func TestPreviewRange(t *testing.T) {
	cases := []struct {
		name                        string
		playhead, in, out, duration int
		from, to                    int
	}{
		{"from playhead", 100, 0, 0, 500, 100, 500},
		{"capped at 30 s", 0, 0, 0, 2000, 0, 750},
		{"past the end rewinds", 600, 0, 0, 500, 0, 500},
		{"in/out range", 300, 50, 200, 500, 50, 200},
		{"range capped", 0, 100, 1900, 2000, 100, 850},
		{"range beyond end clamps", 0, 400, 900, 500, 400, 500},
	}
	for _, c := range cases {
		from, to, err := previewRange(c.playhead, c.in, c.out, c.duration, 25)
		if err != nil || from != c.from || to != c.to {
			t.Errorf("%s: got %d..%d, %v; want %d..%d", c.name, from, to, err, c.from, c.to)
		}
	}
	if _, _, err := previewRange(0, 0, 0, 0, 25); err == nil {
		t.Error("empty timeline must fail")
	}
	if _, _, err := previewRange(0, 600, 700, 500, 25); err == nil {
		t.Error("range behind the end must fail")
	}
}

func TestPeakInAndTicks(t *testing.T) {
	peaks := []float32{0.1, 0.9, 0.2, 0.4}
	if got := peakIn(peaks, 0, 0.5); got != 0.9 {
		t.Errorf("peakIn first half = %v", got)
	}
	if got := peakIn(peaks, 0.75, 0.76); got != 0.4 {
		t.Errorf("peakIn narrow = %v", got)
	}
	if got := peakIn(peaks, 1.5, 2); got != 0.4 {
		t.Errorf("peakIn beyond end must clamp, got %v", got)
	}
	if peakIn(nil, 0, 1) != 0 {
		t.Error("peakIn of nothing")
	}
	if got := tickStep(25*60, 1200, 25, 100); got != 5*25 {
		t.Errorf("tickStep = %d, want 125", got)
	}
}

type fakeWaves struct{}

// Waveform returns a speech-like envelope so screenshots look realistic.
func (fakeWaves) Waveform(m model.MediaItem, buckets int) ([]float32, error) {
	out := make([]float32, buckets)
	seed := float64(len(m.Name))
	for i := range out {
		x := float64(i) / float64(buckets) * m.Duration.Seconds()
		env := 0.35 + 0.3*math.Sin(x*0.9+seed) + 0.2*math.Sin(x*3.7+seed*2)
		jitter := 0.5 + 0.5*math.Abs(math.Sin(x*41+seed)*math.Cos(x*17))
		out[i] = float32(math.Max(0.02, math.Min(1, env*jitter+0.05)))
	}
	return out, nil
}

type harness struct {
	t    *testing.T
	p    *Page
	s    *session.Session
	w    fyne.Window
	jobs []func()
	errs []error

	mods   fyne.KeyModifier
	now    time.Time
	tick   func() // the running meter clock's tick, nil when stopped
	clocks int    // meter clocks started
	level  func() (liveaudio.Level, map[string]liveaudio.Level)
}

// advance moves the fake clock by dt and runs one meter tick if polling.
func (h *harness) advance(dt time.Duration) {
	h.now = h.now.Add(dt)
	if h.tick != nil {
		h.tick()
	}
}

func (h *harness) flush() {
	for len(h.jobs) > 0 {
		j := h.jobs[0]
		h.jobs = h.jobs[1:]
		j()
	}
}

func demoProject() *model.Project {
	pr := model.NewProject("Demo")
	sec := func(s float64) int { return int(s * 25) }
	voice := pr.AddMedia(model.MediaItem{Name: "Interview Anna.wav", Path: "/media/interview-anna.wav", Kind: model.KindAudio, Duration: 50 * time.Second})
	music := pr.AddMedia(model.MediaItem{Name: "Score Morgenlicht.mp3", Path: "/media/morgenlicht.mp3", Kind: model.KindAudio, Duration: 70 * time.Second})
	drone := pr.AddMedia(model.MediaItem{Name: "Drohne Küste.mp4", Path: "/media/drohne.mp4", Kind: model.KindVideo, Duration: 30 * time.Second, HasAudio: true, Width: 1920, Height: 1080, FPS: model.Rate25})
	pr.AddTrack(model.TrackAudio)
	pr.AddTrack(model.TrackAudio)
	pr.AddTrack(model.TrackAudio)
	a := pr.TrackIndices(model.TrackAudio)
	mv, _ := pr.MediaByID(voice)
	mm, _ := pr.MediaByID(music)
	md, _ := pr.MediaByID(drone)

	v := pr.ClipFromMedia(*md, 0, 0)
	_ = pr.PlaceClip(pr.Tracks[0].ID, v)

	c1 := pr.ClipFromMedia(*mv, sec(2), 0)
	c1.SourceIn, c1.Length, c1.Name = sec(3), sec(18), "Interview Anna – Intro"
	_ = pr.PlaceClip(pr.Tracks[a[0]].ID, c1)
	c2 := pr.ClipFromMedia(*mv, sec(21.5), 0)
	c2.SourceIn, c2.Length, c2.Name = sec(25), sec(14), "Interview Anna – Hafen"
	c2.FadeOut = sec(1)
	_ = pr.PlaceClip(pr.Tracks[a[0]].ID, c2)

	m := pr.ClipFromMedia(*mm, 0, 0)
	m.Length, m.FadeIn, m.FadeOut, m.Volume = sec(42), sec(3), sec(5), DBToLinear(-9)
	m.Color = model.FromNRGBA(studiotheme.ClipColors[1].Color)
	_ = pr.PlaceClip(pr.Tracks[a[1]].ID, m)

	d := pr.ClipFromMedia(*md, sec(6), 0)
	d.Kind, d.Length, d.Name = model.KindAudio, sec(12), "Drohne Küste (Ton)"
	d.Color = model.FromNRGBA(studiotheme.ClipColors[4].Color)
	_ = pr.PlaceClip(pr.Tracks[a[2]].ID, d)

	pr.Tracks[a[0]].Name = "Dialog"
	pr.Tracks[a[1]].Name = "Musik"
	pr.Tracks[a[1]].Volume = DBToLinear(-4.5)
	pr.Tracks[a[2]].Name = "Atmo"
	pr.Tracks[a[2]].Muted = true
	pr.Tracks[a[2]].Volume = DBToLinear(-12)
	pr.Tracks[a[3]].Name = "Effekte"
	return pr
}

func newHarness(t *testing.T, pr *model.Project) *harness {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(studiotheme.New())
	h := &harness{t: t, s: session.New(pr)}
	ctx := &page.Context{Session: h.s, ShowErr: func(err error) { h.errs = append(h.errs, err) }}
	h.p = New(ctx).(*Page)
	h.p.waves = fakeWaves{}
	h.p.spawn = func(f func()) { h.jobs = append(h.jobs, f) }
	h.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h.p.now = func() time.Time { return h.now }
	h.p.mods = func() fyne.KeyModifier { return h.mods }
	h.p.prev.live = func() bool { return false }
	h.p.levels = func(*session.Session) (liveaudio.Level, map[string]liveaudio.Level) {
		if h.level != nil {
			return h.level()
		}
		return liveaudio.Level{}, nil
	}
	h.p.clock = func(tick func()) func() {
		h.clocks++
		h.tick = tick
		stopped := false
		return func() {
			if !stopped {
				stopped = true
				h.tick = nil
			}
		}
	}
	h.w = test.NewTempWindow(t, h.p.Content())
	ctx.Window = h.w
	h.w.Resize(fyne.NewSize(1440, 860))
	h.p.Activate()
	h.flush()
	t.Cleanup(h.p.Close)
	return h
}

func audioTrack(s *session.Session, n int) *model.Track {
	return &s.Project.Tracks[s.Project.TrackIndices(model.TrackAudio)[n]]
}

func TestToggleIsOneUndoStep(t *testing.T) {
	h := newHarness(t, demoProject())
	id := audioTrack(h.s, 0).ID
	row := h.p.tracks.rows[id]

	test.Tap(row.mute)
	if !audioTrack(h.s, 0).Muted || h.s.UndoLabel() != "Spur stummschalten" || !row.mute.on {
		t.Fatalf("mute not applied: muted=%v label=%q", audioTrack(h.s, 0).Muted, h.s.UndoLabel())
	}
	test.Tap(h.p.mixer.strips[id].solo)
	if !audioTrack(h.s, 0).Solo || !h.p.tracks.rows[id].solo.on {
		t.Fatal("solo from the mixer must show in the track header")
	}
	test.Tap(row.lock)
	if !audioTrack(h.s, 0).Locked {
		t.Fatal("lock not applied")
	}
	h.s.Undo()
	h.s.Undo()
	if tr := audioTrack(h.s, 0); tr.Solo || tr.Locked || !tr.Muted {
		t.Fatalf("each toggle must undo separately: %+v", tr)
	}
	h.s.Undo()
	if audioTrack(h.s, 0).Muted || h.s.CanUndo() || h.p.tracks.rows[id].mute.on {
		t.Fatal("mute undo failed")
	}
}

func TestAddTrack(t *testing.T) {
	h := newHarness(t, model.NewProject("leer"))
	before := len(h.s.Project.TrackIndices(model.TrackAudio))
	h.p.addTrack()
	idx := h.s.Project.TrackIndices(model.TrackAudio)
	if len(idx) != before+1 || h.s.UndoLabel() != "Audiospur hinzufügen" {
		t.Fatalf("track not added: %d tracks", len(idx))
	}
	nt := h.s.Project.Tracks[idx[len(idx)-1]]
	if nt.Name != "A2" || h.s.SelectedTrack != nt.ID {
		t.Errorf("new track %q should be A2 and selected", nt.Name)
	}
	if len(h.p.mixer.strips) != 2 || len(h.p.tracks.rows) != 2 {
		t.Error("mixer and track list must show the new track")
	}
	h.s.Undo()
	if len(h.p.mixer.strips) != 1 || len(h.p.mixer.box.Objects) != 1 {
		t.Error("undo must remove the strip")
	}
}

func TestFaderSetsLinearVolume(t *testing.T) {
	h := newHarness(t, demoProject())
	id := audioTrack(h.s, 0).ID
	st := h.p.mixer.strips[id]
	if st.fader.Value != 0 {
		t.Fatalf("unity gain should sit at 0 dB, got %v", st.fader.Value)
	}
	for _, db := range []float64{-1, -3, -6} {
		st.fader.SetValue(db)
	}
	if got := audioTrack(h.s, 0).Volume; math.Abs(got-0.501187) > 1e-4 {
		t.Errorf("volume = %v, want -6 dB", got)
	}
	if st.readout.Text != "-6,0 dB" {
		t.Errorf("readout %q", st.readout.Text)
	}
	h.s.Undo()
	if audioTrack(h.s, 0).Volume != 1 || h.s.CanUndo() {
		t.Error("a fader drag must be a single undo step")
	}
	if st.fader.Value != 0 {
		t.Error("fader must follow undo")
	}
}

func TestLaneTapSelectsAndClipPanelEdits(t *testing.T) {
	h := newHarness(t, demoProject())
	tr := audioTrack(h.s, 1)
	l := h.p.tracks.rows[tr.ID].lane
	clip := tr.Clips[0]
	x := h.p.xOf(clip.Start+10, l.Size().Width)
	test.TapAt(l, fyne.NewPos(x, rowH/2))
	if h.s.SelectedClip != clip.ID || len(h.p.clip.ids) != 1 || h.p.clip.ids[0] != clip.ID {
		t.Fatalf("tap should select the clip, selected %q", h.s.SelectedClip)
	}
	if h.p.clip.name.Text != clip.Name || h.p.clip.volVal.Text != "-9,0 dB" {
		t.Errorf("clip panel shows %q / %q", h.p.clip.name.Text, h.p.clip.volVal.Text)
	}
	h.p.clip.vol.SetValue(-3)
	h.p.clip.fadeIn.SetValue(10)
	c, _, _ := h.s.Project.Clip(clip.ID)
	if math.Abs(LinearToDB(c.Volume)+3) > 1e-6 || c.FadeIn != 10 {
		t.Errorf("clip edits not applied: vol %v fade %d", LinearToDB(c.Volume), c.FadeIn)
	}

	// an empty spot seeks and clears the clip selection
	x = h.p.xOf(tr.Clips[0].End()+50, l.Size().Width)
	test.TapAt(l, fyne.NewPos(x, rowH/2))
	if h.s.SelectedClip != "" || h.s.Playhead < tr.Clips[0].End()+45 {
		t.Errorf("empty tap: clip %q playhead %d", h.s.SelectedClip, h.s.Playhead)
	}
	if h.p.clip.scroll.Visible() {
		t.Error("clip panel should show its empty state")
	}
}

func TestLockedClipCannotBeEdited(t *testing.T) {
	h := newHarness(t, demoProject())
	tr := audioTrack(h.s, 0)
	h.s.SelectClip(tr.Clips[0].ID)
	test.Tap(h.p.tracks.rows[tr.ID].lock)
	if !h.p.clip.vol.Disabled() || h.p.clip.hint.Text == "" {
		t.Error("controls of a clip on a locked track must be disabled with a hint")
	}
}

func TestPreviewPlaysAndStops(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no 'true' binary")
	}
	h := newHarness(t, demoProject())
	var got export.Options
	h.p.prev.run = func(_ context.Context, p *model.Project, o export.Options) error {
		got = o
		if p == h.s.Project {
			t.Error("export must get a clone")
		}
		return nil
	}
	h.p.prev.lookPath = func(n string) (string, error) {
		if n == "pw-play" {
			return truePath, nil
		}
		return "", exec.ErrNotFound
	}
	h.s.Seek(100)
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if h.p.prev.state != previewMixing || h.p.stop.Disabled() {
		t.Fatalf("state after start: %v", h.p.prev.state)
	}
	job := h.jobs[0]
	h.jobs = nil
	job()
	if got.Format != export.FormatWAV || got.From != 100 || got.To != 100+30*25 || !got.IncludeAudio || got.Path == "" {
		t.Errorf("export options %+v", got)
	}
	if _, err := os.Stat(got.Path); !os.IsNotExist(err) {
		t.Error("temporary WAV must be removed after playback")
	}
	if h.p.prev.Busy() || h.s.Playing || len(h.errs) > 0 {
		t.Errorf("after the player exits: busy=%v playing=%v errs=%v", h.p.prev.Busy(), h.s.Playing, h.errs)
	}

	// missing players give a clear message and nothing starts
	h.p.prev.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	h.p.prev.Start()
	if len(h.errs) != 1 || !errors.Is(h.errs[0], errNoPlayer) || h.p.prev.Busy() {
		t.Errorf("errs %v", h.errs)
	}
}

func TestPreviewSyncsSessionPlayback(t *testing.T) {
	h := newHarness(t, demoProject())
	h.p.prev.lookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	h.p.prev.Start()
	gen := h.p.prev.gen
	h.p.prev.playing(gen, "ffplay")
	if !h.s.Playing || h.p.prev.state != previewPlaying || h.p.status.Text != "Wiedergabe über ffplay" {
		t.Fatalf("playing state wrong: %v %q", h.s.Playing, h.p.status.Text)
	}
	h.s.Seek(10) // e.g. a click in the ruler stops session playback
	if h.p.prev.Busy() {
		t.Error("stopping session playback must stop the preview")
	}
	h.jobs = nil
}

func TestTypedKeyStepsFrames(t *testing.T) {
	h := newHarness(t, demoProject())
	h.s.Seek(10)
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyLeft})
	if h.s.Playhead != 11 {
		t.Errorf("playhead %d, want 11", h.s.Playhead)
	}
	if h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyA}) {
		t.Error("unhandled keys must return false")
	}
}

func TestEmptyProject(t *testing.T) {
	pr := model.NewProject("leer")
	pr.Tracks = pr.Tracks[:2]
	h := newHarness(t, pr)
	if !h.p.tracks.empty.Visible() || !h.p.mixer.empty.Visible() {
		t.Error("empty states must show without audio tracks")
	}
	h.p.prev.Start()
	if len(h.errs) != 1 || h.p.prev.Busy() {
		t.Errorf("preview of an empty project: %v", h.errs)
	}
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
}
