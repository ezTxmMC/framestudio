package audio

import (
	"image/png"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/liveaudio"
)

func TestMeterScale(t *testing.T) {
	cases := map[float64]float64{0: 1, -6: 0.85, -12: 0.70, -24: 0.42, -48: 0.09, -70: 0, -90: 0, 3: 1}
	for db, want := range cases {
		if got := meterFrac(db); math.Abs(got-want) > 1e-9 {
			t.Errorf("meterFrac(%v) = %v, want %v", db, got, want)
		}
	}
	prev := -1.0
	for db := -80.0; db <= 0; db += 0.5 {
		f := meterFrac(db)
		if f < prev {
			t.Fatalf("meterFrac must rise monotonically, %v at %v dB", f, db)
		}
		prev = f
	}
	if ampToDB(1) != 0 || ampToDB(0) != meterFloor || math.Abs(ampToDB(0.5)+6.0206) > 1e-3 {
		t.Error("ampToDB")
	}
	if zoneColor(-20) != meterGreen || zoneColor(-12) != meterGreen || zoneColor(-6) != meterAmber || zoneColor(-1) != meterRed {
		t.Error("zone colours: green to -12, amber to -3, red above")
	}
	if math.Abs(fracToDB(meterFrac(-24))+24) > 0.01 {
		t.Error("fracToDB must invert meterFrac")
	}
}

func TestBallistics(t *testing.T) {
	b := newBallistics()
	if !b.resting() {
		t.Fatal("new meter rests")
	}
	b.step(-6, false, 33*time.Millisecond)
	if b.level != -6 || b.peak != -6 {
		t.Fatalf("rise must be instant: %+v", b)
	}
	// release: 24 dB/s, peak holds 1.5 s
	b.step(meterFloor, false, 500*time.Millisecond)
	if math.Abs(b.level-(-18)) > 1e-9 || b.peak != -6 {
		t.Fatalf("after 0.5 s: level %v peak %v", b.level, b.peak)
	}
	b.step(meterFloor, false, time.Second)
	b.step(meterFloor, false, 100*time.Millisecond) // hold over: peak starts to fall
	if b.peak >= -6 || b.peak < -6-peakFall*0.1-1e-9 {
		t.Errorf("peak after hold: %v", b.peak)
	}
	if math.Abs(b.level-(-6-meterFall*1.6)) > 1e-6 {
		t.Errorf("level after 1.6 s: %v", b.level)
	}
	for range 100 {
		b.step(meterFloor, false, 50*time.Millisecond)
	}
	if !b.resting() {
		t.Errorf("must come to rest: %+v", b)
	}
	// clip latches until reset
	var m meterState = newMeterState()
	m.step(liveaudio.Level{Peak: [2]float32{1, 0.2}, Clipped: true}, 33*time.Millisecond)
	if !m[0].clip || m[1].clip {
		t.Errorf("clip must light on the clipping channel only: %v %v", m[0].clip, m[1].clip)
	}
	for range 100 {
		m.step(liveaudio.Level{}, 50*time.Millisecond)
	}
	if !m.clipped() {
		t.Error("clip LED must latch")
	}
	m.resetClip()
	if m.clipped() {
		t.Error("reset must clear the clip LED")
	}
}

func TestMeterPollingStartsAndStops(t *testing.T) {
	h := newHarness(t, demoProject())
	dialog := audioTrack(h.s, 0).ID
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		l := liveaudio.Level{Peak: [2]float32{0.5, 0.25}}
		return l, map[string]liveaudio.Level{dialog: l}
	}
	if h.tick != nil {
		t.Fatal("meters must not poll while stopped")
	}
	h.s.SetPlaying(true)
	if h.tick == nil || h.clocks != 1 {
		t.Fatal("playback must start polling")
	}
	h.advance(time.Second / 30)
	st := h.p.mixer.strips[dialog]
	if math.Abs(st.meter.state[0].level+6.02) > 0.01 || h.p.mixer.master.meter.state.resting() {
		t.Fatalf("meters must show the levels: %+v", st.meter.state)
	}
	if !h.p.mixer.strips[audioTrack(h.s, 1).ID].meter.state.resting() {
		t.Error("tracks without level stay down")
	}
	h.s.SetPlaying(false)
	if h.tick == nil {
		t.Fatal("meters must keep polling to fall back")
	}
	for i := 0; i < 200 && h.tick != nil; i++ {
		h.advance(time.Second / 30)
	}
	if h.tick != nil || !h.p.mixer.resting() {
		t.Fatal("polling must stop once the meters rest")
	}
	if h.p.mixer.master.readout.Text != "-∞ dB" {
		t.Errorf("master readout %q", h.p.mixer.master.readout.Text)
	}
	// hidden pages do not poll
	h.p.Deactivate()
	h.s.SetPlaying(true)
	if h.tick != nil {
		t.Error("an inactive page must not poll")
	}
	h.p.Activate()
	if h.tick == nil {
		t.Error("activating while playing must start polling")
	}
	h.p.Close()
	if h.tick != nil {
		t.Error("Close must stop polling")
	}
}

func TestClipLEDResetsOnClick(t *testing.T) {
	h := newHarness(t, demoProject())
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		return liveaudio.Level{Peak: [2]float32{1, 1}, Clipped: true}, nil
	}
	h.s.SetPlaying(true)
	h.advance(time.Second / 30)
	m := h.p.mixer.master.meter
	if !m.state.clipped() {
		t.Fatal("master must show clipping")
	}
	test.Tap(m)
	if m.state.clipped() {
		t.Error("click must reset the clip LED")
	}
}

func TestViewportMath(t *testing.T) {
	const span, fps = 2500, 25
	v := viewport{zoom: 1}
	v.clamp(span, fps)
	if v.visible(span) != span || v.xOf(1250, span, 1000) != 500 || v.frameAt(500, 1000, span) != 1250 {
		t.Fatal("zoom 1 shows the whole span")
	}
	if maxZoom(span, fps) != 50 {
		t.Errorf("maxZoom = %v", maxZoom(span, fps))
	}
	// zooming keeps the anchor under the pointer
	v.zoomAround(4, 1000, 0.25, span, fps)
	if math.Abs(float64(v.xOf(1000, span, 800))-200) > 1e-3 || v.visible(span) != 625 {
		t.Errorf("anchor moved: start %v", v.start)
	}
	v.zoomAround(1000, 0, 0, span, fps)
	if v.zoom != 50 || v.start != 0 {
		t.Errorf("zoom must clamp: %+v", v)
	}
	v.scrollBy(1e6, span, fps)
	if v.start != span-v.visible(span) {
		t.Errorf("scroll must stop at the end: %v", v.start)
	}
	v.scrollBy(-1e6, span, fps)
	if v.start != 0 {
		t.Errorf("scroll must stop at 0: %v", v.start)
	}
	// following: forward pages ahead, reverse pages back, stopped centres
	v = viewport{zoom: 5} // 500 frames visible
	if v.follow(300, true, 1, span, fps) {
		t.Error("visible playhead must not scroll")
	}
	if !v.follow(480, true, 2, span, fps) || v.start != 430 {
		t.Errorf("forward follow: start %v", v.start)
	}
	if !v.follow(440, true, -1, span, fps) || v.start != 440-450 && v.start != 0 {
		t.Errorf("reverse follow: start %v", v.start)
	}
	v = viewport{start: 1000, zoom: 5}
	if !v.follow(400, true, -4, span, fps) || v.start != 400-450 && v.start != 0 {
		t.Errorf("reverse follow back: start %v", v.start)
	}
	v = viewport{start: 1000, zoom: 5}
	if !v.follow(2000, false, 1, span, fps) || v.start != 1750 {
		t.Errorf("stopped playhead off screen must be centred: %v", v.start)
	}
	for _, z := range []float64{1, 2, 7.5, 50} {
		if got := sliderToZoom(zoomToSlider(z, 50), 50); math.Abs(got-z) > 1e-9 {
			t.Errorf("slider round trip %v -> %v", z, got)
		}
	}
}

func TestCtrlWheelZoomsAndShiftWheelPans(t *testing.T) {
	h := newHarness(t, demoProject())
	l := h.p.tracks.rows[audioTrack(h.s, 0).ID].lane
	w := l.Size().Width
	anchor := h.p.frameAt(w/2, w)

	// plain wheel scrolls the tracks, not the time
	l.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(w/2, 10)}, Scrolled: fyne.Delta{DY: -10}})
	if h.p.view.zoom != 1 {
		t.Fatal("plain wheel must not zoom")
	}
	h.mods = fyne.KeyModifierControl
	for range 4 {
		l.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(w/2, 10)}, Scrolled: fyne.Delta{DY: 10}})
	}
	if h.p.view.zoom < 2 {
		t.Fatalf("Ctrl+wheel must zoom in, zoom %v", h.p.view.zoom)
	}
	if got := h.p.frameAt(w/2, w); abs(got-anchor) > 2 {
		t.Errorf("frame under the pointer moved from %d to %d", anchor, got)
	}
	if h.p.zoom.Value <= 0 {
		t.Error("zoom slider must follow")
	}
	h.mods = fyne.KeyModifierShift
	before := h.p.view.start
	l.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: -10}})
	if h.p.view.start <= before {
		t.Errorf("Shift+wheel must pan right: %v -> %v", before, h.p.view.start)
	}
	h.mods = 0
	// slider zoom and fit
	h.p.zoom.SetValue(1)
	if h.p.view.zoom != maxZoom(h.p.span(), 25) {
		t.Errorf("slider at max: zoom %v", h.p.view.zoom)
	}
	h.p.setZoom(1, -1)
	if h.p.view.zoom != 1 || h.p.view.start != 0 || h.p.zoom.Value != 0 {
		t.Error("fit must show everything")
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestPlayheadFollowsBothDirections(t *testing.T) {
	h := newHarness(t, demoProject())
	h.p.setZoom(8, -1)
	vis := h.p.view.visible(h.p.span())
	w := h.p.laneWidth()
	h.s.Seek(10)
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyL})
	if !h.s.Playing || h.s.Rate != 1 {
		t.Fatal("L must play")
	}
	for range 400 {
		h.s.Advance(40 * time.Millisecond)
		if x := h.p.xOf(h.s.Playhead, w); x < 0 || x > w {
			t.Fatalf("playhead %d left the view (x %v)", h.s.Playhead, x)
		}
	}
	if h.p.view.start < vis/2 {
		t.Errorf("view must have followed, start %v", h.p.view.start)
	}
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyJ})
	if !h.s.Playing || h.s.Rate >= 0 {
		t.Fatalf("J must play backwards, rate %v", h.s.Rate)
	}
	for h.s.Playing {
		h.s.Advance(40 * time.Millisecond)
		if x := h.p.xOf(h.s.Playhead, w); x < 0 || x > w {
			t.Fatalf("reverse: playhead %d left the view (x %v)", h.s.Playhead, x)
		}
	}
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeyK})
	if h.s.Playing {
		t.Error("K must stop")
	}
	// the ruler seeks in the zoomed view
	h.p.view.start = 200
	h.p.redrawLanes()
	test.TapAt(h.p.tracks.ruler, fyne.NewPos(0, 5))
	if h.s.Playhead != 200 {
		t.Errorf("ruler click at the left edge: playhead %d", h.s.Playhead)
	}
}

func TestCtrlClickTogglesSelection(t *testing.T) {
	h := newHarness(t, demoProject())
	d, m := audioTrack(h.s, 0), audioTrack(h.s, 1)
	ld, lm := h.p.tracks.rows[d.ID].lane, h.p.tracks.rows[m.ID].lane
	at := func(c model.Clip, l *lane) fyne.Position {
		return fyne.NewPos(h.p.xOf(c.Start+5, l.Size().Width), rowH/2)
	}
	test.TapAt(ld, at(d.Clips[0], ld))
	h.mods = fyne.KeyModifierControl
	test.TapAt(lm, at(m.Clips[0], lm))
	test.TapAt(ld, at(d.Clips[1], ld))
	if len(h.s.Selected) != 3 || h.s.SelectedClip != d.Clips[1].ID {
		t.Fatalf("Ctrl+click must add: %v", h.s.Selected)
	}
	test.TapAt(lm, at(m.Clips[0], lm))
	if len(h.s.Selected) != 2 || h.s.IsSelected(m.Clips[0].ID) {
		t.Fatalf("Ctrl+click must remove: %v", h.s.Selected)
	}
	if !strings.Contains(h.p.clip.count.Text, "2 Clips") || h.p.clip.name.Visible() {
		t.Errorf("clip panel must show the count, got %q", h.p.clip.count.Text)
	}
	h.mods = 0
	test.TapAt(lm, at(m.Clips[0], lm))
	if len(h.s.Selected) != 1 {
		t.Error("plain click must select one clip")
	}
}

func TestBulkClipEditsAreOneUndoStep(t *testing.T) {
	h := newHarness(t, demoProject())
	d, m := audioTrack(h.s, 0), audioTrack(h.s, 1)
	ids := []string{d.Clips[0].ID, d.Clips[1].ID, m.Clips[0].ID}
	h.s.SelectClips(ids)
	cp := h.p.clip
	if cp.volVal.Text != mixed || cp.outVal.Text != mixed || cp.count.Text != "3 Clips" {
		t.Fatalf("mixed values must show a dash: vol %q out %q", cp.volVal.Text, cp.outVal.Text)
	}
	if cp.srcRange.Text != mixed {
		t.Errorf("source range of several clips: %q", cp.srcRange.Text)
	}
	undo := len(h.s.UndoLabel())
	_ = undo
	for _, db := range []float64{-2, -4, -6} {
		cp.vol.SetValue(db)
	}
	cp.fadeOut.SetValue(20)
	for _, id := range ids {
		c, _, _ := h.s.Project.Clip(id)
		if math.Abs(LinearToDB(c.Volume)+6) > 1e-6 || c.FadeOut != 20 {
			t.Errorf("clip %s: %v dB, fade %d", c.Name, LinearToDB(c.Volume), c.FadeOut)
		}
	}
	if cp.volVal.Text != "-6,0 dB" {
		t.Errorf("equal values must show: %q", cp.volVal.Text)
	}
	if !strings.Contains(h.s.UndoLabel(), "3 Clips") {
		t.Errorf("undo label %q", h.s.UndoLabel())
	}
	h.s.Undo() // fade
	h.s.Undo() // the whole volume drag on all clips
	if h.s.CanUndo() {
		t.Error("volume drag on several clips must be one undo step")
	}
	c, _, _ := h.s.Project.Clip(m.Clips[0].ID)
	if math.Abs(LinearToDB(c.Volume)+9) > 1e-6 {
		t.Errorf("undo must restore every clip, music at %v dB", LinearToDB(c.Volume))
	}

	// clips on locked tracks are skipped
	h.s.SelectClips(ids)
	h.p.toggleLock(m.ID)
	h.s.SelectClips(ids)
	if !strings.Contains(cp.hint.Text, "1 von 3") {
		t.Errorf("hint %q", cp.hint.Text)
	}
	cp.fadeIn.SetValue(5)
	mc, _, _ := h.s.Project.Clip(m.Clips[0].ID)
	dc, _, _ := h.s.Project.Clip(d.Clips[0].ID)
	if mc.FadeIn == 5 || dc.FadeIn != 5 {
		t.Errorf("locked clip must stay: music %d dialog %d", mc.FadeIn, dc.FadeIn)
	}
}

func TestRenameTrack(t *testing.T) {
	h := newHarness(t, demoProject())
	id := audioTrack(h.s, 0).ID
	n := h.p.tracks.rows[id].name
	test.DoubleTap(n)
	if !n.entry.Visible() || n.entry.Text != "Dialog" {
		t.Fatalf("double-click must open the entry, text %q", n.entry.Text)
	}
	n.entry.SetText("Sprache")
	n.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if audioTrack(h.s, 0).Name != "Sprache" || h.s.UndoLabel() != "Spur umbenennen" || n.entry.Visible() {
		t.Fatalf("rename: %q, undo %q", audioTrack(h.s, 0).Name, h.s.UndoLabel())
	}
	if h.p.mixer.strips[id].name.text.Text != "Sprache" {
		t.Error("mixer strip must show the new name")
	}
	// Escape cancels, empty names are ignored
	test.DoubleTap(h.p.mixer.strips[id].name)
	e := h.p.mixer.strips[id].name.entry
	e.SetText("X")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	test.DoubleTap(n)
	n.entry.SetText("  ")
	n.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if audioTrack(h.s, 0).Name != "Sprache" {
		t.Errorf("cancelled rename changed the name to %q", audioTrack(h.s, 0).Name)
	}
	h.s.Undo()
	if audioTrack(h.s, 0).Name != "Dialog" || n.text.Text != "Dialog" {
		t.Error("undo must restore the name")
	}
}

func TestDeleteEmptyTrack(t *testing.T) {
	h := newHarness(t, demoProject())
	full, empty := audioTrack(h.s, 0), audioTrack(h.s, 3)
	if h.p.tracks.rows[full.ID].del.Visible() || !h.p.tracks.rows[empty.ID].del.Visible() {
		t.Fatal("only empty tracks offer delete")
	}
	emptyID := empty.ID
	test.Tap(h.p.tracks.rows[emptyID].del)
	if tr, _ := h.s.Project.Track(emptyID); tr != nil || h.s.UndoLabel() != "Spur Effekte löschen" {
		t.Fatalf("empty track not deleted, undo %q", h.s.UndoLabel())
	}
	if _, ok := h.p.tracks.rows[emptyID]; ok || len(h.p.mixer.strips) != 3 {
		t.Error("row and strip must go")
	}
	h.p.removeTrack(full.ID)
	if tr, _ := h.s.Project.Track(full.ID); tr == nil || len(h.errs) != 1 {
		t.Errorf("tracks with clips must stay with an error, errs %v", h.errs)
	}
	h.s.Undo()
	if tr, _ := h.s.Project.Track(emptyID); tr == nil {
		t.Error("undo must bring the track back")
	}
}

func TestLivePreviewPlaysRangeWithoutLoop(t *testing.T) {
	h := newHarness(t, demoProject())
	h.p.prev.live = func() bool { return true }
	h.p.updateTransport()
	if h.p.status.Text != "Ab Abspielkopf" {
		t.Errorf("idle status %q", h.p.status.Text)
	}
	h.s.SetIn(50)
	h.s.SetOut(100)
	h.s.SetLoop(true)
	h.s.Seek(300)
	test.Tap(h.p.play)
	if !h.s.Playing || h.s.Playhead != 50 || h.s.Loop || !h.p.prev.Busy() || h.p.stop.Disabled() {
		t.Fatalf("live preview: playing %v at %d loop %v", h.s.Playing, h.s.Playhead, h.s.Loop)
	}
	if len(h.jobs) != 0 {
		t.Error("live preview must not mix a file")
	}
	for h.s.Playing {
		h.s.Advance(100 * time.Millisecond)
	}
	if h.s.Playhead != 100 || h.p.prev.Busy() || !h.s.Loop {
		t.Errorf("after the range: at %d busy %v loop %v", h.s.Playhead, h.p.prev.Busy(), h.s.Loop)
	}
	// Stopp ends it early, Space too
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if !h.p.prev.Busy() {
		t.Fatal("Space must start the preview")
	}
	h.p.TypedKey(&fyne.KeyEvent{Name: fyne.KeySpace})
	if h.p.prev.Busy() || h.s.Playing || !h.s.Loop {
		t.Error("Space must stop and restore the loop")
	}
	// external stop (transport) ends the preview
	h.p.prev.Start()
	h.s.SetPlaying(false)
	if h.p.prev.Busy() {
		t.Error("stopping playback elsewhere must end the preview")
	}
}

// formFits checks the clip panel shows every control without scrolling.
func formFits(t *testing.T, h *harness, size fyne.Size) {
	t.Helper()
	h.w.Resize(size)
	cp := h.p.clip
	need := cp.form.MinSize().Height
	have := cp.scroll.Size().Height
	if need > have {
		t.Errorf("%v: clip panel needs %v px, has %v", size, need, have)
	}
	if cp.vol.Size().Width < 80 {
		t.Errorf("%v: volume slider too narrow: %v", size, cp.vol.Size().Width)
	}
	for _, st := range h.p.mixer.strips {
		if st.fader.Size().Height < 100 {
			t.Errorf("%v: fader only %v px high", size, st.fader.Size().Height)
		}
	}
}

func TestLayoutFits(t *testing.T) {
	h := newHarness(t, demoProject())
	h.s.SelectClip(audioTrack(h.s, 0).Clips[0].ID)
	// page area inside the shell at 1440x900 and 1280x800
	formFits(t, h, fyne.NewSize(1440, 800))
	formFits(t, h, fyne.NewSize(1280, 700))
}

func TestScreenshot(t *testing.T) {
	out := os.Getenv("FS_SCREENSHOT")
	if out == "" {
		t.Skip("set FS_SCREENSHOT to write /tmp/fs-r2-audio.png")
	}
	h := newHarness(t, demoProject())
	h.w.Resize(fyne.NewSize(1440, 900))
	a := h.s.Project.TrackIndices(model.TrackAudio)
	tr := func(i int) *model.Track { return &h.s.Project.Tracks[a[i]] }
	h.s.SelectClips([]string{tr(0).Clips[0].ID, tr(1).Clips[0].ID})
	h.s.SetIn(25 * 8)
	h.s.SetOut(25 * 30)
	h.s.Seek(25 * 12)
	h.p.setZoom(1.6, -1)
	ids := []string{tr(0).ID, tr(1).ID}
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		amp := func(db float64) float32 { return float32(math.Pow(10, db/20)) }
		return liveaudio.Level{Peak: [2]float32{amp(-4.5), amp(-6)}}, map[string]liveaudio.Level{
			ids[0]: {Peak: [2]float32{amp(-0.5), amp(0)}, Clipped: true},
			ids[1]: {Peak: [2]float32{amp(-16), amp(-14)}},
		}
	}
	h.s.SetPlaying(true)
	h.advance(time.Second / 30)
	// let peaks hold above the bars, as a running meter shows
	h.level = func() (liveaudio.Level, map[string]liveaudio.Level) {
		amp := func(db float64) float32 { return float32(math.Pow(10, db/20)) }
		return liveaudio.Level{Peak: [2]float32{amp(-7), amp(-8)}}, map[string]liveaudio.Level{
			ids[0]: {Peak: [2]float32{amp(-8), amp(-9.5)}},
			ids[1]: {Peak: [2]float32{amp(-19), amp(-17)}},
		}
	}
	h.advance(400 * time.Millisecond)
	h.flush()
	img := h.w.Canvas().Capture()
	path := "/tmp/fs-r2-audio.png"
	if out != "1" {
		path = out
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	// the smaller laptop size, for checking the layout
	h.w.Resize(fyne.NewSize(1280, 800))
	small, err := os.Create(strings.TrimSuffix(path, ".png") + "-1280.png")
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	if err := png.Encode(small, h.w.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
	h.s.SetPlaying(false)
}

func TestTickerClockRunsOnUIThreadAndStops(t *testing.T) {
	q := kit.UseUIQueue(t)
	n := 0
	stop := tickerClock(func() { n++ })
	if !q.Until(func() bool { return n >= 3 }, 2*time.Second) {
		t.Fatalf("clock ticked %d times", n)
	}
	stop()
	stop() // idempotent
	q.Settle(100*time.Millisecond, time.Second)
	got := n
	time.Sleep(120 * time.Millisecond)
	q.Drain()
	if n != got {
		t.Errorf("clock kept ticking after stop: %d -> %d", got, n)
	}
}

func TestLiveAvailableIsCached(t *testing.T) {
	calls := 0
	v := &previewer{lookPath: func(n string) (string, error) {
		calls++
		if n == "ffmpeg" || n == "paplay" {
			return "/usr/bin/" + n, nil
		}
		return "", exec.ErrNotFound
	}}
	if !v.liveAvailable() || !v.liveAvailable() || calls != 3 {
		t.Errorf("liveAvailable: %d lookups", calls)
	}
	v = &previewer{lookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if v.liveAvailable() {
		t.Error("no ffmpeg, no live sound")
	}
}
