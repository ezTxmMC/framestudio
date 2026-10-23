package deliver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
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
	fps     model.Rate
	drop    bool // drop-frame timecode in summaries

	status   jobStatus
	progress export.Progress
	err      error
	elapsed  time.Duration
	size     int64
	cancel   context.CancelFunc
}

func (j *job) name() string { return filepath.Base(j.opts.Path) }

// queued reports whether j still waits for its turn and may be reordered.
func (j *job) queued() bool { return j.status == jobReady || j.status == jobWaiting }

// renderQueue holds the render jobs of one session. The deliver page and the detachable
// queue panel show the same renderQueue (see sharedQueue), so jobs exist once however
// many views are open. Jobs render one at a time. UI thread only.
type renderQueue struct {
	sess     *session.Session
	renderer *render.Renderer

	// Hooks; the deliver page points them at its own (test-replaceable) fields.
	do               func(f func())                       // runs f on the UI thread
	openDir          func(dir string) error               // shows a folder in the file manager
	sendNotification func(*fyne.Notification)             // desktop notification
	toast            func(title, msg string, failed bool) // in-window toast of the shell
	showErr          func(error)

	jobs    []*job
	nextID  int
	running *job
	closed  bool
	wg      sync.WaitGroup // export goroutines

	// lastTitle/lastName/lastFailed describe the last finished job for the status line.
	lastTitle  string
	lastName   string
	lastFailed bool

	refs  int
	views []*queue
	unsub func()
}

// queues maps each open session to its render queue (UI thread only).
var queues = map[*session.Session]*renderQueue{}

// sharedQueue returns the render queue of ctx's session, creating it on first use, and
// takes a reference; every caller must call release when its view closes. The last
// release cancels running work.
func sharedQueue(ctx *page.Context) *renderQueue {
	if q := queues[ctx.Session]; q != nil {
		q.refs++
		return q
	}
	q := &renderQueue{
		sess:             ctx.Session,
		renderer:         ctx.Renderer,
		do:               kit.Do,
		openDir:          openWithSystem,
		sendNotification: appNotification,
		showErr:          ctx.ShowErr,
		refs:             1,
	}
	q.unsub = ctx.Session.Subscribe(func(c session.Change) {
		if c&session.ChangeProject != 0 {
			q.projectChanged()
		}
	})
	queues[ctx.Session] = q
	return q
}

// existingQueue returns the session's queue without taking a reference, nil if none.
func existingQueue(s *session.Session) *renderQueue { return queues[s] }

// release drops a reference; the last one cancels all jobs and forgets the queue.
func (q *renderQueue) release() {
	q.refs--
	if q.refs > 0 {
		return
	}
	q.close()
	if queues[q.sess] == q {
		delete(queues, q.sess)
	}
}

func openWithSystem(dir string) error {
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the child so it does not linger as a zombie
	return nil
}

func appNotification(n *fyne.Notification) {
	if a := fyne.CurrentApp(); a != nil {
		a.SendNotification(n)
	}
}

func (q *renderQueue) attach(v *queue) { q.views = append(q.views, v) }

func (q *renderQueue) detach(v *queue) {
	q.views = slices.DeleteFunc(q.views, func(x *queue) bool { return x == v })
}

// changed redraws every view completely (jobs added, removed, reordered or restyled).
func (q *renderQueue) changed() {
	for _, v := range q.views {
		v.sync()
	}
}

// jobChanged redraws the card of j in every view (progress ticks).
func (q *renderQueue) jobChanged(j *job) {
	for _, v := range q.views {
		if c := v.cards[j]; c != nil {
			c.refresh()
		}
	}
}

func (q *renderQueue) report(err error) {
	if q.showErr != nil {
		q.showErr(err)
	}
}

// uniquePath avoids overwriting existing files and outputs of queued jobs by appending
// " (2)", " (3)" ... to the name.
func (q *renderQueue) uniquePath(path string) string {
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

func (q *renderQueue) uses(path string) bool {
	for _, j := range q.jobs {
		if j.opts.Path == path && j.status != jobFailed && j.status != jobCanceled {
			return true
		}
	}
	return false
}

// add enqueues an export of project; start marks it to render as soon as possible.
func (q *renderQueue) add(o export.Options, project *model.Project, start bool) *job {
	q.nextID++
	j := &job{id: q.nextID, opts: o, project: project, frames: frameCount(project, o), fps: project.Rate(), drop: project.DropFrame()}
	if start {
		j.status = jobWaiting
	}
	q.jobs = append(q.jobs, j)
	q.changed()
	q.runNext()
	return j
}

// arm marks j to render when it is its turn.
func (q *renderQueue) arm(j *job) {
	j.status = jobWaiting
	q.changed()
	q.runNext()
}

// renderAll arms every ready job.
func (q *renderQueue) renderAll() {
	for _, j := range q.jobs {
		if j.status == jobReady {
			j.status = jobWaiting
		}
	}
	q.runNext()
	q.changed()
}

// cancelAll stops the running job and withdraws every waiting one.
func (q *renderQueue) cancelAll() {
	for _, j := range q.jobs {
		switch j.status {
		case jobWaiting:
			j.status = jobReady
		case jobRunning:
			j.cancel()
		}
	}
	q.changed()
}

// hasReady reports whether a job waits for "Alle rendern".
func (q *renderQueue) hasReady() bool {
	return slices.ContainsFunc(q.jobs, func(j *job) bool { return j.status == jobReady })
}

// hasActive reports whether a job renders or waits for its turn.
func (q *renderQueue) hasActive() bool {
	return slices.ContainsFunc(q.jobs, func(j *job) bool { return j.status == jobRunning || j.status == jobWaiting })
}

// runNext starts the first waiting job if nothing renders.
func (q *renderQueue) runNext() {
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

func (q *renderQueue) start(j *job) {
	// Render the timeline as it is now, not as it was when the job was added.
	j.project = q.sess.Project.Clone()
	j.frames = frameCount(j.project, j.opts)
	j.fps, j.drop = j.project.Rate(), j.project.DropFrame()
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.status = jobRunning
	j.progress = export.Progress{Total: j.frames, Stage: "Vorbereiten"}
	q.running = j
	q.changed()

	do := q.do
	r := q.renderer
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		begin := time.Now()
		err := export.Run(ctx, j.project, r, j.opts, func(pr export.Progress) {
			do(func() {
				if j.status == jobRunning {
					j.progress = pr
					q.jobChanged(j)
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

func (q *renderQueue) finished(j *job, err error, elapsed time.Duration, size int64) {
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
	q.announce(j)
	q.changed()
	q.runNext()
}

// announce tells the user that j has ended: a desktop notification (unless switched off
// in the settings), a toast in the window (visible on every page) and the status line of
// every queue view. Cancelled jobs are the user's own doing and stay quiet.
func (q *renderQueue) announce(j *job) {
	var title, body string
	switch j.status {
	case jobDone:
		title = "Export fertig"
		body = fmt.Sprintf("„%s“ wurde in %s gerendert (%s).", j.name(), formatClock(j.elapsed), formatBytes(j.size))
	case jobFailed:
		title = "Export fehlgeschlagen"
		body = fmt.Sprintf("„%s“: %v", j.name(), j.err)
	default:
		return
	}
	q.lastTitle, q.lastName, q.lastFailed = title, j.name(), j.status == jobFailed
	q.changed()
	q.notify(title, body, j.status == jobFailed)
}

// notify posts a desktop notification (Export.Notify) and the in-window toast.
func (q *renderQueue) notify(title, message string, failed bool) {
	if q.sendNotification != nil && settings.Get().Export.Notify {
		q.sendNotification(fyne.NewNotification(title, message))
	}
	if q.toast != nil {
		q.toast(title, message, failed)
	}
}

// move shifts a queued job d places up (negative) or down in the render order.
func (q *renderQueue) move(j *job, d int) {
	i := slices.Index(q.jobs, j)
	k := i + d
	if i < 0 || k < 0 || k >= len(q.jobs) {
		return
	}
	q.jobs[i], q.jobs[k] = q.jobs[k], q.jobs[i]
	q.changed()
}

// cancelJob stops a running job or withdraws a waiting one.
func (q *renderQueue) cancelJob(j *job) {
	switch j.status {
	case jobRunning:
		j.cancel()
	case jobWaiting:
		j.status = jobReady
		q.changed()
	}
}

func (q *renderQueue) remove(j *job) {
	if j.status == jobRunning {
		return
	}
	q.jobs = slices.DeleteFunc(q.jobs, func(x *job) bool { return x == j })
	q.changed()
}

// clearFinished removes the jobs that are done or cancelled. Failed jobs stay so they
// can be retried with "Erneut" or removed one by one.
func (q *renderQueue) clearFinished() {
	q.jobs = slices.DeleteFunc(q.jobs, func(j *job) bool { return j.status == jobDone || j.status == jobCanceled })
	q.changed()
}

// openFolder shows the output folder of j.
func (q *renderQueue) openFolder(j *job) {
	dir := filepath.Dir(j.opts.Path)
	if j.opts.Format == export.FormatPNG {
		dir = j.opts.Path
	}
	q.openFolderPath(dir)
}

func (q *renderQueue) openFolderPath(dir string) {
	if err := q.openDir(dir); err != nil {
		q.report(fmt.Errorf("Der Ordner %s kann nicht geöffnet werden. Ist xdg-open installiert? (%v)", dir, err))
	}
}

// projectChanged updates the duration shown on jobs that have not started yet.
func (q *renderQueue) projectChanged() {
	if q.closed {
		return
	}
	p := q.sess.Project
	for _, j := range q.jobs {
		if !j.queued() {
			continue
		}
		if n := frameCount(p, j.opts); n != j.frames || !j.fps.Equal(p.Rate()) || j.drop != p.DropFrame() {
			j.frames, j.fps, j.drop = n, p.Rate(), p.DropFrame()
			q.jobChanged(j)
		}
	}
}

// busy describes unfinished work, or "" when nothing would be lost by closing.
func (q *renderQueue) busy() string {
	open := 0
	for _, j := range q.jobs {
		if j.queued() {
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
func (q *renderQueue) wait(timeout time.Duration) bool {
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

// close cancels all running work; called when the last view closes.
func (q *renderQueue) close() {
	if q.closed {
		return
	}
	q.closed = true
	if q.unsub != nil {
		q.unsub()
		q.unsub = nil
	}
	for _, j := range q.jobs {
		if j.cancel != nil {
			j.cancel()
		}
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
