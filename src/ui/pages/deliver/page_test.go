package deliver

import (
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/export"
	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

type harness struct {
	pg   *Page
	w    fyne.Window
	s    *session.Session
	ui   chan func()
	errs []error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	a := test.NewTempApp(t)
	kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	p := model.NewProject("Showreel 2026")
	must(t, p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Hintergrund", 0, 75)))
	must(t, p.PlaceClip(p.Tracks[1].ID, model.NewClip(model.KindTitle, "Frame Studio", 10, 50)))
	h := &harness{s: session.New(p), ui: make(chan func(), 4096)}
	h.w = test.NewTempWindow(t, nil)
	cache := media.NewCache()
	ctx := &page.Context{Window: h.w, Session: h.s, Renderer: render.New(cache), Media: cache,
		Navigate: func(string) {}, ShowErr: func(err error) { h.errs = append(h.errs, err) }}
	h.pg = newPage(ctx)
	h.pg.do = func(f func()) { h.ui <- f }
	h.w.SetContent(h.pg.Content())
	h.w.Resize(fyne.NewSize(1440, 860))
	t.Cleanup(h.pg.Close)
	return h
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// pump runs queued UI-thread callbacks until cond holds.
func (h *harness) pump(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for !cond() {
		select {
		case f := <-h.ui:
			f()
		case <-deadline:
			t.Fatal("timeout waiting for the render queue")
		}
	}
}

// configure points the form at a temp folder with a tiny custom size.
func (h *harness) configure(t *testing.T, ft export.Format) string {
	dir := t.TempDir()
	f := h.pg.form
	f.format.SetSelectedIndex(formatIndex(ft))
	f.folder.SetText(dir)
	f.name.SetText("clip")
	f.res.SetSelected(resCustom)
	f.width.SetText("160")
	f.height.SetText("90")
	f.quality.Set(string(export.QualityLow))
	return dir
}

func needFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

func TestPresetFillsForm(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	if f.presetAt != 0 || f.currentFormat() != export.FormatMP4 || !f.audio.Checked {
		t.Fatalf("default preset not applied: %d %s", f.presetAt, f.currentFormat())
	}
	test.Tap(f.rows[4]) // GIF 480p
	if f.currentFormat() != export.FormatGIF || f.ext.Text != ".gif" || !f.audio.Disabled() || f.presetAt != 4 {
		t.Errorf("gif preset: format %s ext %q preset %d", f.currentFormat(), f.ext.Text, f.presetAt)
	}
	o, err := f.options()
	must(t, err)
	if o.Width != 854 || o.Height != 480 || o.IncludeAudio {
		t.Errorf("gif options %+v", o)
	}
	test.Tap(f.rows[6]) // WAV
	if !f.res.Disabled() || !f.audio.Checked {
		t.Error("wav should disable the resolution and force audio")
	}
	test.Tap(f.rows[0])
	if !f.audio.Checked {
		t.Error("audio choice should come back for MP4")
	}
	f.quality.Set(string(export.QualityLow))
	f.changed()
	if f.presetAt != -1 || f.rows[0].selected {
		t.Error("changed settings should no longer highlight the preset")
	}
	if f.sumVideo.Text != "H.264 · 1920 × 1080 · 25 fps" || f.sumAudio.Text != "AAC 192 kb/s · 48 kHz Stereo" ||
		f.sumTime.Text != "00:00:03:00 · 75 Frames" || !strings.HasSuffix(f.sumPath.Text, "Showreel 2026.mp4") {
		t.Errorf("summary %q %q %q %q", f.sumPath.Text, f.sumVideo.Text, f.sumAudio.Text, f.sumTime.Text)
	}
}

func TestValidationInline(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	h.configure(t, export.FormatMP4)
	f.width.SetText("161")
	test.Tap(f.addBtn)
	if f.errBox.Visible() || len(h.pg.queue.jobs) != 1 {
		t.Errorf("odd width is rounded, not rejected: visible=%v %q jobs=%d", f.errBox.Visible(), f.errText.Text, len(h.pg.queue.jobs))
	}
	if got := videoSummary(h.pg.queue.jobs[0].opts, model.Rate25); !strings.Contains(got, "gerundet von 161×") {
		t.Errorf("summary should say the size is rounded: %q", got)
	}
	h.pg.queue.jobs = h.pg.queue.jobs[:0]
	f.width.SetText("160")
	if f.errBox.Visible() {
		t.Error("editing should clear the error")
	}
	f.name.SetText("  ")
	test.Tap(f.addBtn)
	if !strings.Contains(f.errText.Text, "Dateiname fehlt") {
		t.Errorf("empty name: %q", f.errText.Text)
	}
	f.name.SetText("x")
	f.folder.SetText(filepath.Join(t.TempDir(), "missing"))
	test.Tap(f.addBtn)
	if !strings.Contains(f.errText.Text, "existiert nicht") {
		t.Errorf("missing folder: %q", f.errText.Text)
	}
	if len(h.pg.queue.jobs) != 0 {
		t.Error("invalid settings must not enqueue")
	}
}

func TestRenderNow(t *testing.T) {
	needFFmpeg(t)
	h := newHarness(t)
	dir := h.configure(t, export.FormatMP4)
	test.Tap(h.pg.form.renderBtn)
	q := h.pg.queue
	if len(q.jobs) != 1 || q.jobs[0].status != jobRunning {
		t.Fatalf("job should run right away")
	}
	j := q.jobs[0]
	h.pump(t, func() bool { return j.status >= jobDone })
	if j.status != jobDone {
		t.Fatalf("status %v, err %v", j.status, j.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	if !q.cards[j].open.Visible() || q.cards[j].cancel.Visible() || !strings.Contains(q.cards[j].result.Text, "gerendert") {
		t.Errorf("done card: open=%v cancel=%v %q", q.cards[j].open.Visible(), q.cards[j].cancel.Visible(), q.cards[j].result.Text)
	}
	var opened string
	h.pg.openFolder = func(d string) error { opened = d; return nil }
	test.Tap(q.cards[j].open)
	if opened != dir {
		t.Errorf("opened %q", opened)
	}
	h.pg.openFolder = func(string) error { return errors.New("xdg-open fehlt") }
	test.Tap(q.cards[j].open)
	if len(h.errs) != 1 {
		t.Error("open failure should be reported")
	}

	// A second export with the same name gets a fresh file name.
	test.Tap(h.pg.form.addBtn)
	if got := q.jobs[1].name(); got != "clip (2).mp4" || q.jobs[1].status != jobReady {
		t.Errorf("second job %q %v", got, q.jobs[1].status)
	}
}

func TestQueueRenderAllAndCancel(t *testing.T) {
	h := newHarness(t)
	h.configure(t, export.FormatPNG)
	f, q := h.pg.form, h.pg.queue
	test.Tap(f.addBtn)
	test.Tap(f.addBtn)
	if len(q.jobs) != 2 || q.running != nil || q.all.Disabled() {
		t.Fatalf("two ready jobs expected")
	}
	if q.jobs[1].name() != "clip (2)" {
		t.Errorf("second name %q", q.jobs[1].name())
	}
	test.Tap(q.all)
	a, b := q.jobs[0], q.jobs[1]
	if a.status != jobRunning || b.status != jobWaiting || !q.all.Disabled() {
		t.Fatalf("statuses %v %v", a.status, b.status)
	}
	test.Tap(q.cards[a].cancel)
	h.pump(t, func() bool { return b.status >= jobDone })
	if a.status != jobCanceled || b.status != jobDone {
		t.Fatalf("after cancel: %v %v (%v)", a.status, b.status, b.err)
	}
	if _, err := os.Stat(a.opts.Path); err == nil {
		files, _ := os.ReadDir(a.opts.Path)
		if len(files) != 0 {
			t.Errorf("cancelled job left %d files", len(files))
		}
	}
	files, _ := filepath.Glob(filepath.Join(b.opts.Path, "*.png"))
	if len(files) != 75 {
		t.Errorf("%d png files", len(files))
	}
	img, err := os.Open(files[0])
	must(t, err)
	_, err = png.DecodeConfig(img)
	img.Close()
	must(t, err)

	test.Tap(q.cards[a].remove)
	if len(q.jobs) != 1 {
		t.Error("remove failed")
	}
	test.Tap(q.cards[b].remove)
	if len(q.jobs) != 0 || !q.empty.Visible() {
		t.Error("queue should be empty")
	}
}

func TestCloseCancelsRunningJob(t *testing.T) {
	h := newHarness(t)
	h.configure(t, export.FormatPNG)
	test.Tap(h.pg.form.renderBtn)
	j := h.pg.queue.jobs[0]
	h.pg.Close()
	h.pump(t, func() bool { return j.status >= jobDone })
	if j.status != jobCanceled && j.status != jobDone {
		t.Errorf("status %v", j.status)
	}
}

func TestRangeChoice(t *testing.T) {
	h := newHarness(t)
	f := h.pg.form
	if len(f.rng.Options) != 1 {
		t.Fatal("no In-Out range without marks")
	}
	h.s.SetIn(10)
	h.s.SetOut(40)
	if len(f.rng.Options) != 2 {
		t.Fatal("In-Out range should be offered")
	}
	f.rng.SetSelected(rangeIO)
	o, err := f.options()
	must(t, err)
	if o.From != 10 || o.To != 40 || f.rangeInfo.Text != "00:00:00:10 – 00:00:01:15" {
		t.Errorf("range %d-%d %q", o.From, o.To, f.rangeInfo.Text)
	}
	h.s.ClearRange()
	if f.rng.Selected != rangeAll {
		t.Error("cleared range should fall back to the whole timeline")
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{
		formatBytes(512):              "512 B",
		formatBytes(12_400_000):       "12,4 MB",
		formatClock(83 * time.Second): "1:23",
		formatClock(time.Hour):        "1:00:00",
		fileName(" a/b: c "):          "a_b_ c",
		fileName(""):                  "Export",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

// TestScreenshot renders the page with a populated queue to $FS_SCREENSHOT (or /tmp/fs-deliver.png).
func TestScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render")
	}
	h := newHarness(t)
	h.s.SetIn(10)
	h.s.SetOut(60)
	q := h.pg.queue
	dir := defaultFolder()
	add := func(name string, ps int) *job {
		o := export.Presets(h.s.Project)[ps].Options
		o.Path = filepath.Join(dir, name+export.Extension(o.Format))
		return q.add(o, h.s.Project.Clone(), false)
	}
	done := add("Showreel 2026", 0)
	done.status, done.size, done.elapsed = jobDone, 48_300_000, 94*time.Second
	run := add("Showreel 2026 Web", 1)
	run.status = jobRunning
	run.progress = export.Progress{Frame: 31, Total: 75, ETA: 41 * time.Second, Stage: "Bilder rendern"}
	failed := add("Showreel Loop", 4)
	failed.status = jobFailed
	failed.err = errors.New("ffmpeg ist fehlgeschlagen: Kein Speicherplatz auf dem Gerät. Gib Speicher frei und rendere erneut.")
	add("Showreel 2026 Audio", 6)
	for _, j := range q.jobs {
		q.cards[j].refresh()
	}
	q.refresh()
	h.pg.form.name.SetText("Showreel 2026")
	q.announce(done)

	out := os.Getenv("FS_SCREENSHOT")
	if out == "1" || out == "true" {
		out = "/tmp/fs-r2-deliver.png"
	}
	// The page area of a 1440x900 and a 1280x800 window (minus the app's bars).
	shots := map[string]fyne.Size{out: {Width: 1440, Height: 900 - shellChrome},
		strings.TrimSuffix(out, ".png") + "-1280.png": {Width: 1280, Height: 800 - shellChrome}}
	for path, size := range shots {
		h.w.Resize(size)
		img := h.w.Canvas().Capture()
		file, err := os.Create(path)
		must(t, err)
		must(t, png.Encode(file, img))
		file.Close()
	}
}

func TestQueuedJobRendersCurrentTimeline(t *testing.T) {
	h := newHarness(t)
	h.configure(t, export.FormatPNG)
	f := h.pg.form
	test.Tap(f.addBtn) // queued, not started
	q := h.pg.queue
	if len(q.jobs) != 1 || q.jobs[0].status != jobReady {
		t.Fatalf("job not queued: %+v", q.jobs)
	}
	if got := h.pg.Busy(); !strings.Contains(got, "Auftrag wartet") {
		t.Fatalf("Busy = %q", got)
	}
	// Edit the timeline after queueing: the job must render the new length.
	must(t, h.s.Edit("verlängern", func(p *model.Project) error {
		return p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "Mehr", 75, 25))
	}))
	if j := q.jobs[0]; j.frames != 100 || !strings.Contains(q.cards[j].meta.Text, "00:00:04:00") {
		t.Fatalf("queued job shows %d frames (%q)", j.frames, q.cards[j].meta.Text)
	}
	q.renderAll()
	j := q.jobs[0]
	if j.project.Duration() != 100 {
		t.Fatalf("job renders a stale snapshot of %d frames", j.project.Duration())
	}
	if got := h.pg.Busy(); !strings.Contains(got, "läuft") {
		t.Fatalf("Busy while running = %q", got)
	}
	h.pump(t, func() bool { return j.status >= jobDone })
	if j.status != jobDone {
		t.Fatalf("status %v: %v", j.status, j.err)
	}
	if h.pg.Busy() != "" || !h.pg.Wait(time.Second) {
		t.Fatal("finished queue must not be busy")
	}
}

func TestOfflineMediaAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.configure(t, export.FormatPNG)
	must(t, h.s.Edit("offline", func(p *model.Project) error {
		m := model.MediaItem{ID: "weg", Name: "Strand", Path: "/nonexistent/strand.png", Kind: model.KindImage}
		p.AddMedia(m)
		return p.PlaceClip(p.Tracks[0].ID, p.ClipFromMedia(m, 0, 25))
	}))
	test.Tap(h.pg.form.addBtn)
	if len(h.pg.queue.jobs) != 0 {
		t.Fatal("offline media must not be queued without asking")
	}
	if !strings.Contains(h.pg.form.errText.Text, "offline (Strand)") {
		t.Fatalf("message %q", h.pg.form.errText.Text)
	}
}

func TestHiddenPageStopsViewer(t *testing.T) {
	h := newHarness(t)
	h.pg.Activate()
	h.pg.Deactivate()
	if h.pg.view.Active() {
		t.Fatal("hidden export page must pause its monitor")
	}
	h.pg.Activate()
	if !h.pg.view.Active() {
		t.Fatal("monitor must render again when shown")
	}
}
