package deliver

import (
	"errors"
	"image/png"
	"os"
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
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// bound binds bs in a fresh registry and returns it.
func bound(bs []command.Binding) *command.Registry {
	r := command.NewRegistry()
	r.Bind(command.Global, bs...)
	return r
}

func enabled(t *testing.T, r *command.Registry, id string) bool {
	t.Helper()
	c, ok := r.Get(id)
	if !ok || !c.Available {
		t.Fatalf("%s not bound", id)
	}
	return c.Enabled
}

func TestPanelSharesQueueWithPage(t *testing.T) {
	h := newHarness(t)
	ctx := h.pg.ctx
	pi, ok := page.PanelByID(page.PanelQueue)
	if !ok {
		t.Fatal("queue panel not registered")
	}
	panel := pi.New(ctx).(*queuePanel)
	t.Cleanup(panel.Close)
	if panel.rq != h.pg.rq {
		t.Fatal("panel and page must share one render queue")
	}
	js := h.addJobs("A", "B")
	pv := panel.view
	if len(pv.jobs) != 2 || len(pv.list.Objects) != 2 || pv.cards[js[0]] == nil {
		t.Fatalf("panel shows %d jobs, %d cards", len(pv.jobs), len(pv.list.Objects))
	}
	if pv.cards[js[0]] == h.pg.queue.cards[js[0]] {
		t.Fatal("each view needs its own cards")
	}
	// Removing in the panel removes it on the page, once.
	test.Tap(pv.cards[js[0]].remove)
	if got := jobNames(h.pg.queue); got != "B" || len(h.pg.queue.list.Objects) != 1 {
		t.Fatalf("page after panel remove: %q (%d cards)", got, len(h.pg.queue.list.Objects))
	}
	// A finished job notifies once although two views show it.
	var sent int
	h.pg.sendNotification = func(*fyne.Notification) { sent++ }
	js[1].status, js[1].cancel = jobRunning, func() {}
	h.pg.rq.finished(js[1], nil, time.Second, 10)
	if sent != 1 {
		t.Fatalf("%d notifications for one job", sent)
	}
	if !pv.statusBox.Visible() || !h.pg.queue.statusBox.Visible() || pv.status.Text != h.pg.queue.status.Text {
		t.Fatal("both views show the status line")
	}
	// Closing the panel leaves the page's queue alive.
	panel.Close()
	if h.pg.rq.closed || queues[h.s] != h.pg.rq {
		t.Fatal("closing the panel must not close the queue while the page has it")
	}
	if len(h.pg.rq.views) != 1 {
		t.Fatalf("%d views attached after panel close", len(h.pg.rq.views))
	}
}

func TestQueuePanelStandalone(t *testing.T) {
	a := test.NewTempApp(t)
	kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	s := session.New(model.NewProject("Solo"))
	ctx := &page.Context{Session: s, Renderer: render.New(media.NewCache())}
	pi, _ := page.PanelByID(page.PanelQueue)
	p := pi.New(ctx)
	w := test.NewTempWindow(t, p.Content())
	w.Resize(fyne.NewSize(420, 600))
	qp := p.(*queuePanel)
	if !qp.view.empty.Visible() || !qp.view.all.Disabled() {
		t.Fatal("empty panel should show the hint")
	}
	o := export.Presets(s.Project)[0].Options
	o.Path = filepath.Join(t.TempDir(), "x.mp4")
	j := qp.rq.add(o, s.Project.Clone(), false)
	j.status, j.cancel = jobRunning, func() {}
	qp.rq.running = j
	canceled := false
	j.cancel = func() { canceled = true }
	if qp.Busy() == "" {
		t.Fatal("running job must make the panel busy")
	}
	p.Close()
	if !canceled || queues[s] != nil {
		t.Fatal("closing the only view must cancel and forget the queue")
	}
	p.Close() // idempotent
}

func TestDeliverCommands(t *testing.T) {
	h := newHarness(t)
	r := bound(h.pg.Commands())
	if enabled(t, r, "deliver.renderAll") || enabled(t, r, "deliver.cancelAll") {
		t.Fatal("empty queue: nothing to render or cancel")
	}
	h.configure(t, export.FormatPNG)
	if !r.Run("deliver.addToQueue") || len(h.pg.rq.jobs) != 1 || h.pg.rq.jobs[0].status != jobReady {
		t.Fatal("addToQueue should queue one ready job")
	}
	if !enabled(t, r, "deliver.renderAll") || enabled(t, r, "deliver.cancelAll") {
		t.Fatal("a ready job can be rendered, not cancelled")
	}
	// Arm without starting: hold the job as waiting behind a fake running one.
	fake := &job{status: jobRunning, cancel: func() {}}
	h.pg.rq.running = fake
	r.Run("deliver.renderAll")
	if h.pg.rq.jobs[0].status != jobWaiting || !enabled(t, r, "deliver.cancelAll") {
		t.Fatal("renderAll should arm the job")
	}
	r.Run("deliver.cancelAll")
	if h.pg.rq.jobs[0].status != jobReady {
		t.Fatal("cancelAll should withdraw waiting jobs")
	}
	h.pg.rq.running = nil

	// Export folder: the form's folder, missing folders are reported.
	var opened string
	h.pg.openFolder = func(d string) error { opened = d; return nil }
	dir := t.TempDir()
	h.pg.form.folder.SetText(dir)
	r.Run("deliver.openFolder")
	if opened != dir {
		t.Fatalf("opened %q, want %q", opened, dir)
	}
	h.pg.form.folder.SetText(filepath.Join(dir, "fehlt"))
	r.Run("deliver.openFolder")
	if len(h.errs) == 0 || !strings.Contains(h.errs[len(h.errs)-1].Error(), "existiert nicht") {
		t.Fatalf("missing folder not reported: %v", h.errs)
	}

	// Exportieren… focuses the file name, also with the settings column hidden.
	h.pg.ApplyLayout(layout.Page{Hidden: map[string]bool{areaSettings: true}})
	r.Run("deliver.export")
	if !h.pg.form.content.Visible() || h.w.Canvas().Focused() != h.pg.form.name {
		t.Fatal("deliver.export should show the form and focus the file name")
	}
}

func TestGlobalCommandsNavigate(t *testing.T) {
	a := test.NewTempApp(t)
	kit.UseUIQueue(t)
	a.Settings().SetTheme(studiotheme.New())
	pr := model.NewProject("Global")
	must(t, pr.PlaceClip(pr.Tracks[0].ID, model.NewClip(model.KindColor, "Farbe", 0, 25)))
	s := session.New(pr)
	w := test.NewTempWindow(t, nil)
	var shown []string
	ctx := &page.Context{Window: w, Session: s, Renderer: render.New(media.NewCache())}
	var pg *Page
	ctx.Navigate = func(id string) {
		shown = append(shown, id)
		if pg == nil { // the shell creates the page on first navigation
			pg = New(ctx).(*Page)
			w.SetContent(pg.Content())
			t.Cleanup(pg.Close)
		}
	}
	r := bound(GlobalCommands(ctx))
	if enabled(t, r, "deliver.renderAll") {
		t.Fatal("no queue yet: renderAll disabled")
	}
	r.Run("deliver.export")
	if len(shown) != 1 || shown[0] != page.Deliver || pg == nil || w.Canvas().Focused() != pg.form.name {
		t.Fatalf("export should open the deliver page and focus the form (shown %v)", shown)
	}
	pg.form.folder.SetText(t.TempDir())
	r.Run("deliver.addToQueue")
	if len(pg.rq.jobs) != 1 || !enabled(t, r, "deliver.renderAll") {
		t.Fatal("global addToQueue should add to the page's queue")
	}
}

func TestDeliverLayoutRoundTrip(t *testing.T) {
	h := newHarness(t)
	if l := h.pg.LayoutState(); len(l.Hidden) != 0 {
		t.Fatalf("default layout %+v", l)
	}
	h.pg.ApplyLayout(layout.Page{Hidden: map[string]bool{areaQueue: true}})
	if h.pg.queue.content.Visible() {
		t.Fatal("queue column should be hidden")
	}
	// The monitor takes the queue's width.
	h.w.Resize(fyne.NewSize(1440, 860))
	center := h.pg.columns.Objects[1]
	if got := center.Size().Width; got < 1440-settingsWidth-3*columnGap-20 {
		t.Fatalf("monitor width %.0f with the queue hidden", got)
	}
	l := h.pg.LayoutState()
	if !l.Hidden[areaQueue] || l.Hidden[areaSettings] {
		t.Fatalf("state %+v", l)
	}
	// Applying before the content exists is kept until it is built.
	h2 := newPage(h.pg.ctx)
	h2.ApplyLayout(l)
	if !h2.LayoutState().Hidden[areaQueue] {
		t.Fatal("pending layout lost")
	}
	h2.Content()
	defer h2.Close()
	if h2.queue.content.Visible() {
		t.Fatal("pending layout not applied")
	}
	h.pg.ApplyLayout(layout.Page{})
	if !h.pg.queue.content.Visible() || len(h.pg.LayoutState().Hidden) != 0 {
		t.Fatal("empty layout should show every column")
	}
}

func TestSettingsDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = settings.Reset() })
	_ = settings.Update(func(s *settings.Settings) {
		s.Export.Folder = dir
		s.Export.Preset = "GIF 480p"
		s.Export.Notify = false
	})
	h := newHarness(t)
	f := h.pg.form
	if f.folder.Text != dir {
		t.Errorf("folder %q, want %q", f.folder.Text, dir)
	}
	if f.presets[f.presetAt].Name != "GIF 480p" || f.currentFormat() != export.FormatGIF {
		t.Errorf("preset %d / %v", f.presetAt, f.currentFormat())
	}
	var sent, toasts int
	h.pg.sendNotification = func(*fyne.Notification) { sent++ }
	h.pg.SetToast(func(string, string, bool) { toasts++ })
	js := h.addJobs("Leise")
	js[0].status, js[0].cancel = jobRunning, func() {}
	h.pg.rq.finished(js[0], errors.New("kaputt"), time.Second, 0)
	if sent != 0 || toasts != 1 {
		t.Fatalf("Notify off: %d desktop notifications, %d toasts", sent, toasts)
	}
}

// TestPanelScreenshot renders the standalone queue panel to /tmp/fs-r3-panels-queue.png.
func TestPanelScreenshot(t *testing.T) {
	if os.Getenv("FS_SCREENSHOT") == "" {
		t.Skip("set FS_SCREENSHOT to render")
	}
	h := newHarness(t)
	pi, _ := page.PanelByID(page.PanelQueue)
	p := pi.New(h.pg.ctx)
	defer p.Close()
	q := h.pg.rq
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
	add("Showreel 2026 Audio", 6)
	add("Teaser 15 s", 4)
	q.announce(done)
	w := test.NewTempWindow(t, p.Content())
	w.Resize(fyne.NewSize(440, 720))
	out, err := os.Create("/tmp/fs-r3-panels-queue.png")
	must(t, err)
	defer out.Close()
	must(t, png.Encode(out, w.Canvas().Capture()))
}
