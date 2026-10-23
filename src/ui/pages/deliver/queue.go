package deliver

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

type jobStatus int

const (
	jobReady    jobStatus = iota // queued, waits for "Alle rendern"
	jobWaiting                   // will start when the running job ends
	jobRunning                   // rendering
	jobDone                      // finished successfully
	jobFailed                    // export returned an error
	jobCanceled                  // cancelled by the user
)

func (s jobStatus) String() string {
	return [...]string{"Bereit", "Wartet", "Rendert", "Fertig", "Fehlgeschlagen", "Abgebrochen"}[s]
}

// job is one entry of the render queue. Only the UI thread touches it, except the export
// goroutine which reads the immutable opts and project.
type job struct {
	id      int
	opts    export.Options
	project *model.Project // clone of the timeline, retaken when rendering starts
	frames  int

	status   jobStatus
	progress export.Progress
	err      error
	elapsed  time.Duration
	size     int64
	cancel   context.CancelFunc
	view     *jobView
}

func (j *job) name() string { return filepath.Base(j.opts.Path) }

// queue is the "Render-Warteschlange" panel. Jobs render one at a time.
type queue struct {
	pg      *Page
	jobs    []*job
	nextID  int
	running *job
	closed  bool
	wg      sync.WaitGroup // export goroutines

	list    *fyne.Container
	empty   fyne.CanvasObject
	count   *canvas.Text
	all     *widget.Button
	clear   *widget.Button
	tools   *fyne.Container
	scroll  *container.Scroll
	content fyne.CanvasObject

	// status is the in-app line about the last finished job, above "Alle rendern".
	status     *canvas.Text
	statusMark *canvas.Rectangle
	statusBox  fyne.CanvasObject
}

func newQueue(pg *Page) *queue {
	q := &queue{pg: pg}
	q.list = container.NewVBox()
	hint := smallText("Keine Aufträge", studiotheme.Muted, 12)
	sub := smallText("Einstellungen links wählen und hinzufügen", studiotheme.Faint, 11)
	q.empty = container.NewCenter(container.NewVBox(container.NewCenter(hint), container.NewCenter(sub)))
	q.count = kit.MonoText("0", studiotheme.Faint, 11)
	q.all = widget.NewButtonWithIcon("Alle rendern", theme.MediaPlayIcon(), q.renderAll)
	q.all.Importance = widget.HighImportance
	clear := widget.NewButton("Fertige entfernen", q.clearFinished)
	clear.Importance = widget.LowImportance
	q.clear = clear
	// Fixed width so the header never needs a re-layout of the panel.
	q.tools = container.NewHBox(layout.NewSpacer(), vcenter(q.count), clear)
	q.scroll = container.NewVScroll(q.list)
	q.status = smallText("", studiotheme.Text, 12)
	q.statusMark = canvas.NewRectangle(studiotheme.Steel)
	q.statusMark.SetMinSize(fyne.NewSize(3, 0))
	q.statusBox = container.NewBorder(nil, nil, q.statusMark, nil,
		container.New(layout.NewCustomPaddedLayout(4, 4, 8, 4), q.status))
	q.statusBox.Hide()
	bottom := container.New(layout.NewCustomPaddedVBoxLayout(6), q.statusBox, q.all)
	body := container.NewBorder(nil, bottom, nil, nil, container.NewStack(q.empty, q.scroll))
	q.content = kit.Panel("Render-Warteschlange", withWidth(210, q.tools), body)
	q.refresh()
	return q
}

// uniquePath avoids overwriting existing files and outputs of queued jobs by appending
// " (2)", " (3)" ... to the name.
func (q *queue) uniquePath(path string) string {
	ext := filepath.Ext(path)
	if strings.HasSuffix(path, string(filepath.Separator)) {
		ext = ""
	}
	base := strings.TrimSuffix(path, ext)
	for n := 1; ; n++ {
		cand := path
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		if _, err := os.Stat(cand); err == nil {
			continue
		}
		if q.uses(cand) {
			continue
		}
		return cand
	}
}

func (q *queue) uses(path string) bool {
	for _, j := range q.jobs {
		if j.opts.Path == path && j.status != jobFailed && j.status != jobCanceled {
			return true
		}
	}
	return false
}

// add enqueues an export of project; start marks it to render as soon as possible.
func (q *queue) add(o export.Options, project *model.Project, start bool) *job {
	q.nextID++
	j := &job{id: q.nextID, opts: o, project: project, frames: frameCount(project, o)}
	if start {
		j.status = jobWaiting
	}
	j.view = newJobView(q, j)
	q.jobs = append(q.jobs, j)
	q.list.Add(j.view.content)
	q.refresh()
	q.runNext()
	return j
}

// renderAll arms every ready job.
func (q *queue) renderAll() {
	for _, j := range q.jobs {
		if j.status == jobReady {
			j.status = jobWaiting
			j.view.refresh()
		}
	}
	q.runNext()
	q.refresh()
}

// runNext starts the first waiting job if nothing renders.
func (q *queue) runNext() {
	if q.closed || q.running != nil {
		return
	}
	for _, j := range q.jobs {
		if j.status == jobWaiting {
			q.start(j)
			return
		}
	}
}

func (q *queue) start(j *job) {
	// Render the timeline as it is now, not as it was when the job was added.
	j.project = q.pg.ctx.Session.Project.Clone()
	j.frames = frameCount(j.project, j.opts)
	j.view.meta.Text = jobSummary(j.opts, j.project.Settings.FPS, j.frames)
	j.view.meta.Refresh()
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.status = jobRunning
	j.progress = export.Progress{Total: j.frames, Stage: "Vorbereiten"}
	q.running = j
	j.view.refresh()
	q.refresh()

	do := q.pg.do
	r := q.pg.ctx.Renderer
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		begin := time.Now()
		err := export.Run(ctx, j.project, r, j.opts, func(pr export.Progress) {
			do(func() {
				if j.status == jobRunning {
					j.progress = pr
					j.view.refresh()
				}
			})
		})
		elapsed := time.Since(begin)
		var size int64
		if err == nil {
			size = outputSize(j.opts)
		}
		do(func() { q.finished(j, err, elapsed, size) })
	}()
}

func (q *queue) finished(j *job, err error, elapsed time.Duration, size int64) {
	j.cancel()
	j.elapsed, j.size, j.err = elapsed, size, err
	switch {
	case err == nil:
		j.status = jobDone
	case errors.Is(err, context.Canceled):
		j.status = jobCanceled
		j.err = nil
	default:
		j.status = jobFailed
	}
	if q.running == j {
		q.running = nil
	}
	if q.closed {
		return
	}
	j.view.refresh()
	q.refresh()
	q.announce(j)
	q.runNext()
}

// announce tells the user that j has ended: a desktop notification, a toast in the
// window (visible on every page) and the status line of the queue. Cancelled jobs are
// the user's own doing and stay quiet.
func (q *queue) announce(j *job) {
	var title, body string
	switch j.status {
	case jobDone:
		title = "Export fertig"
		body = fmt.Sprintf("„%s“ wurde in %s gerendert (%s).", j.name(), formatClock(j.elapsed), formatBytes(j.size))
		q.statusMark.FillColor = studiotheme.Steel
	case jobFailed:
		title = "Export fehlgeschlagen"
		body = fmt.Sprintf("„%s“: %v", j.name(), j.err)
		q.statusMark.FillColor = color.NRGBA{0xE0, 0xA1, 0x3A, 0xFF}
	default:
		return
	}
	q.status.Text = title + ": " + ellipsize(j.name(), 32)
	q.status.Refresh()
	q.statusMark.Refresh()
	setVisible(q.statusBox, true, q.content)
	q.pg.notify(title, body, j.status == jobFailed)
}

// move shifts a queued job d places up (negative) or down in the render order.
func (q *queue) move(j *job, d int) {
	i := slices.Index(q.jobs, j)
	k := i + d
	if i < 0 || k < 0 || k >= len(q.jobs) {
		return
	}
	q.jobs[i], q.jobs[k] = q.jobs[k], q.jobs[i]
	objs := make([]fyne.CanvasObject, len(q.jobs))
	for n, x := range q.jobs {
		objs[n] = x.view.content
	}
	q.list.Objects = objs
	q.list.Refresh()
	q.refresh()
}

// queued reports whether j still waits for its turn and may be reordered.
func (j *job) queued() bool { return j.status == jobReady || j.status == jobWaiting }

// cancelJob stops a running job or withdraws a waiting one.
func (q *queue) cancelJob(j *job) {
	switch j.status {
	case jobRunning:
		j.cancel()
	case jobWaiting:
		j.status = jobReady
		j.view.refresh()
		q.refresh()
	}
}

func (q *queue) remove(j *job) {
	if j.status == jobRunning {
		return
	}
	for i, x := range q.jobs {
		if x == j {
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			break
		}
	}
	q.list.Remove(j.view.content)
	q.refresh()
}

// clearFinished removes the jobs that are done or cancelled. Failed jobs stay so they
// can be retried with "Erneut" or removed one by one.
func (q *queue) clearFinished() {
	for _, j := range append([]*job(nil), q.jobs...) {
		if j.status == jobDone || j.status == jobCanceled {
			q.remove(j)
		}
	}
}

func (q *queue) openFolder(j *job) {
	dir := filepath.Dir(j.opts.Path)
	if j.opts.Format == export.FormatPNG {
		dir = j.opts.Path
	}
	if err := q.pg.openFolder(dir); err != nil {
		q.pg.showErr(fmt.Errorf("Der Ordner %s kann nicht geöffnet werden. Ist xdg-open installiert? (%v)", dir, err))
	}
}

// projectChanged updates the duration shown on jobs that have not started yet.
func (q *queue) projectChanged() {
	p := q.pg.ctx.Session.Project
	for _, j := range q.jobs {
		if j.status != jobReady && j.status != jobWaiting {
			continue
		}
		if n := frameCount(p, j.opts); n != j.frames || j.project.Settings.FPS != p.Settings.FPS {
			j.frames = n
			j.view.meta.Text = jobSummary(j.opts, p.Settings.FPS, n)
			j.view.meta.Refresh()
		}
	}
}

// busy describes unfinished work, or "" when nothing would be lost by closing.
func (q *queue) busy() string {
	open := 0
	for _, j := range q.jobs {
		if j.status == jobReady || j.status == jobWaiting {
			open++
		}
	}
	msg := ""
	if j := q.running; j != nil {
		pct := 0
		if j.progress.Total > 0 {
			pct = j.progress.Frame * 100 / j.progress.Total
		}
		msg = fmt.Sprintf("Export „%s“ läuft (%d %%)", j.name(), pct)
	}
	switch {
	case open == 0:
	case msg != "":
		msg += fmt.Sprintf(", %s %s", plural(open, "weiterer Auftrag wartet", "weitere Aufträge warten"), "in der Render-Warteschlange")
	default:
		msg = fmt.Sprintf("%s in der Render-Warteschlange", plural(open, "Auftrag wartet", "Aufträge warten"))
	}
	return msg
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// wait blocks until export goroutines have ended (partial output removed) or timeout.
func (q *queue) wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// close cancels all running work; called when the project closes.
func (q *queue) close() {
	q.closed = true
	for _, j := range q.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
}

// refresh updates the header count, empty hint and "Alle rendern".
func (q *queue) refresh() {
	finished := 0
	for i, j := range q.jobs {
		if j.status == jobDone || j.status == jobCanceled {
			finished++
		}
		j.view.setOrder(i > 0, i < len(q.jobs)-1)
	}
	switch len(q.jobs) {
	case 0:
		q.count.Text = ""
	case 1:
		q.count.Text = "1 Auftrag"
	default:
		q.count.Text = fmt.Sprintf("%d Aufträge", len(q.jobs))
	}
	q.count.Refresh()
	setVisible(q.clear, finished > 0, nil)
	q.tools.Refresh()
	if len(q.jobs) == 0 {
		q.empty.Show()
	} else {
		q.empty.Hide()
	}
	ready := false
	for _, j := range q.jobs {
		if j.status == jobReady {
			ready = true
		}
	}
	if ready {
		q.all.Enable()
	} else {
		q.all.Disable()
	}
}

// outputSize is the size of a finished export (sum of the files for a PNG sequence).
func outputSize(o export.Options) int64 {
	if o.Format != export.FormatPNG {
		if st, err := os.Stat(o.Path); err == nil {
			return st.Size()
		}
		return 0
	}
	var total int64
	files, _ := filepath.Glob(filepath.Join(o.Path, filepath.Base(o.Path)+"_*.png"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			total += st.Size()
		}
	}
	return total
}

// jobView is the card of one job in the queue.
type jobView struct {
	q *queue
	j *job

	bg        *canvas.Rectangle
	accent    *canvas.Rectangle
	title     *canvas.Text
	status    *canvas.Text
	meta      *canvas.Text
	bar       *widget.ProgressBar
	eta       *canvas.Text
	result    *widget.Label
	cancel    *widget.Button
	remove    *widget.Button
	open      *widget.Button
	render    *widget.Button
	up, down  *widget.Button
	order     fyne.CanvasObject
	progress  fyne.CanvasObject
	resultBox fyne.CanvasObject
	body      *fyne.Container
	content   fyne.CanvasObject
}

func newJobView(q *queue, j *job) *jobView {
	v := &jobView{q: q, j: j}
	v.bg = canvas.NewRectangle(studiotheme.Raised)
	v.bg.CornerRadius = 3
	v.accent = canvas.NewRectangle(color.Transparent)
	v.accent.SetMinSize(fyne.NewSize(3, 0))
	v.title = smallText(j.name(), studiotheme.Text, 13)
	v.title.TextStyle = fyne.TextStyle{Bold: true}
	v.status = smallText("", studiotheme.Muted, 12)
	v.meta = kit.MonoText(jobSummary(j.opts, j.project.Settings.FPS, j.frames), studiotheme.Muted, 10.5)
	v.bar = widget.NewProgressBar()
	v.eta = kit.MonoText("", studiotheme.Muted, 11)
	v.result = widget.NewLabel("")
	v.result.Wrapping = fyne.TextWrapWord
	v.result.SizeName = theme.SizeNameCaptionText

	v.cancel = widget.NewButtonWithIcon("Abbrechen", theme.CancelIcon(), func() { q.cancelJob(j) })
	v.remove = widget.NewButtonWithIcon("Entfernen", theme.DeleteIcon(), func() { q.remove(j) })
	v.open = widget.NewButtonWithIcon("Ordner öffnen", theme.FolderOpenIcon(), func() { q.openFolder(j) })
	v.render = widget.NewButtonWithIcon("Rendern", theme.MediaPlayIcon(), func() {
		j.status = jobWaiting
		v.refresh()
		q.refresh()
		q.runNext()
	})
	for _, b := range []*widget.Button{v.cancel, v.remove, v.open, v.render} {
		b.Importance = widget.LowImportance
	}
	v.up = kit.ToolButton(theme.MoveUpIcon(), func() { q.move(j, -1) })
	v.down = kit.ToolButton(theme.MoveDownIcon(), func() { q.move(j, 1) })
	v.order = container.NewHBox(v.up, v.down)

	head := container.NewBorder(nil, nil, nil, v.status, v.title)
	v.progress = container.NewBorder(nil, nil, nil, container.NewPadded(vcenter(v.eta)), v.bar)
	buttons := container.NewHBox(v.order, layout.NewSpacer(), v.render, v.open, v.cancel, v.remove)
	// The label's own inner padding is cancelled so its text lines up with the meta line.
	v.resultBox = container.New(layout.NewCustomPaddedLayout(-4, -4, -6, 0), v.result)
	v.body = container.NewVBox(head, v.meta, v.progress, v.resultBox, buttons)
	card := container.NewBorder(nil, nil, v.accent, nil,
		container.New(layout.NewCustomPaddedLayout(8, 2, 10, 8), v.body))
	v.content = container.NewStack(v.bg, card)
	v.refresh()
	return v
}

func (v *jobView) refresh() {
	j := v.j
	v.status.Text = j.status.String()
	v.status.Color = studiotheme.Muted
	v.accent.FillColor = color.Transparent
	v.result.Importance = widget.MediumImportance
	show := func(o fyne.CanvasObject, on bool) {
		if on {
			o.Show()
		} else {
			o.Hide()
		}
	}
	result := ""
	switch j.status {
	case jobRunning:
		v.accent.FillColor = studiotheme.Steel
		v.status.Color = studiotheme.Steel
		ratio := 0.0
		if j.progress.Total > 0 {
			ratio = float64(j.progress.Frame) / float64(j.progress.Total)
		}
		v.bar.SetValue(ratio)
		v.status.Text = j.progress.Stage
		if v.status.Text == "" {
			v.status.Text = j.status.String()
		}
		v.eta.Text = "Restzeit –:––"
		if j.progress.ETA > 0 {
			v.eta.Text = "Restzeit " + formatClock(j.progress.ETA)
		}
	case jobDone:
		v.status.Color = studiotheme.Text
		result = fmt.Sprintf("%s in %s gerendert", formatBytes(j.size), formatClock(j.elapsed))
	case jobFailed:
		v.result.Importance = widget.WarningImportance
		result = j.err.Error()
	case jobCanceled:
		result = "Abgebrochen, unvollständige Ausgabe wurde gelöscht."
	case jobWaiting:
		v.status.Color = studiotheme.Text
	}
	v.status.Refresh()
	v.accent.Refresh()
	v.eta.Refresh()
	show(v.progress, j.status == jobRunning)
	v.result.SetText(result)
	show(v.resultBox, result != "")
	show(v.cancel, j.status == jobRunning || j.status == jobWaiting)
	show(v.remove, j.status != jobRunning)
	show(v.open, j.status == jobDone)
	show(v.render, j.status == jobReady || j.status == jobFailed || j.status == jobCanceled)
	if j.status == jobFailed || j.status == jobCanceled {
		v.render.SetText("Erneut")
	}
	show(v.order, j.queued())
	v.body.Refresh()
	v.q.scroll.Refresh()
}

// setOrder enables the move buttons that have a neighbour to swap with.
func (v *jobView) setOrder(up, down bool) {
	setEnabledBtn(v.up, up)
	setEnabledBtn(v.down, down)
}

func setEnabledBtn(b *widget.Button, on bool) {
	if on && b.Disabled() {
		b.Enable()
	} else if !on && !b.Disabled() {
		b.Disable()
	}
}
