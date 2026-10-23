// Package deliver is the "deliver" workspace page.
//
// OWNER: deliver page agent. New(ctx) is the contract; do not change its signature.
package deliver

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer"
)

// Panel widths of the settings (left) and queue (right) columns. The queue narrows
// down to minQueueWidth on small windows so the monitor and its transport keep room.
const (
	settingsWidth = 340
	queueWidth    = 360
	minQueueWidth = 300
	columnGap     = 4
)

// Layout area IDs (layout.Page.Hidden).
const (
	areaSettings = "settings"
	areaQueue    = "queue"
)

// columnsLayout places settings, monitor and queue side by side. Hidden side columns
// give their width to the monitor.
type columnsLayout struct{}

func (columnsLayout) widths(objs []fyne.CanvasObject, total float32) (left, right float32) {
	if objs[0].Visible() {
		left = settingsWidth
	}
	if objs[2].Visible() {
		right = min(queueWidth, max(minQueueWidth, total/4))
	}
	return left, right
}

func (l columnsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) != 3 {
		return
	}
	lw, rw := l.widths(objs, size.Width)
	x := float32(0)
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(lw, size.Height))
	if lw > 0 {
		x = lw + columnGap
	}
	cw := size.Width - x - rw
	if rw > 0 {
		cw -= columnGap
	}
	cw = max(cw, 0)
	objs[1].Move(fyne.NewPos(x, 0))
	objs[1].Resize(fyne.NewSize(cw, size.Height))
	objs[2].Move(fyne.NewPos(x+cw+columnGap, 0))
	objs[2].Resize(fyne.NewSize(rw, size.Height))
}

func (l columnsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if o.Visible() {
			h = max(h, o.MinSize().Height)
		}
	}
	w := objs[1].MinSize().Width
	if objs[0].Visible() {
		w += settingsWidth + columnGap
	}
	if objs[2].Visible() {
		w += minQueueWidth + columnGap
	}
	return fyne.NewSize(w, h)
}

// Page is the Resolve-style render page: settings, program monitor and render queue.
type Page struct {
	ctx *page.Context

	// do runs f on the UI thread; tests replace it with a queue they drain themselves.
	do func(f func())
	// openFolder shows a folder in the file manager.
	openFolder func(dir string) error
	// sendNotification posts a desktop notification.
	sendNotification func(*fyne.Notification)
	// toast shows a short message in the window on every page; set by the app shell
	// through SetToast. Nil leaves only the desktop notification and the queue status.
	toast func(title, message string, failed bool)

	root     fyne.CanvasObject
	columns  *fyne.Container
	form     *settingsForm
	rq       *renderQueue // shared with the queue panel (sharedQueue)
	queue    *queue       // this page's view of rq
	view     *viewer.Viewer
	duration *canvas.Text
	unsub    func()
	closed   bool
	// pending is a layout applied before Content was built.
	pending *layout.Page
}

func New(ctx *page.Context) page.Page { return newPage(ctx) }

func newPage(ctx *page.Context) *Page {
	p := &Page{
		ctx:              ctx,
		do:               kit.Do,
		openFolder:       openWithSystem,
		sendNotification: appNotification,
	}
	pages[ctx.Session] = p
	return p
}

// pages maps each session to its deliver page, for the global commands (UI thread only).
var pages = map[*session.Session]*Page{}

// SetToast connects the in-window toast of the app shell, used when a job finishes.
func (p *Page) SetToast(show func(title, message string, failed bool)) {
	p.toast = show
}

// notify reports a finished or failed job on the desktop and in the window.
func (p *Page) notify(title, message string, failed bool) {
	p.Content()
	p.rq.notify(title, message, failed)
}

func (p *Page) Content() fyne.CanvasObject {
	if p.root != nil {
		return p.root
	}
	p.rq = sharedQueue(p.ctx)
	// The shared queue reaches the desktop and the shell through this page's fields, so
	// a page created after the queue panel still gets toasts.
	p.rq.do = func(f func()) { p.do(f) }
	p.rq.openDir = func(dir string) error { return p.openFolder(dir) }
	p.rq.sendNotification = func(n *fyne.Notification) {
		if p.sendNotification != nil {
			p.sendNotification(n)
		}
	}
	p.rq.toast = func(title, msg string, failed bool) {
		if p.toast != nil {
			p.toast(title, msg, failed)
		}
	}
	p.rq.showErr = p.showErr

	p.form = newSettingsForm(p)
	p.queue = newQueue(p.rq, "Einstellungen links wählen und hinzufügen")
	p.form.onChange = p.updateSummary

	p.view = viewer.New(p.ctx.Session, p.ctx.Renderer)
	p.duration = kit.MonoText("", studiotheme.Muted, 12)
	monitorBg := canvas.NewRectangle(studiotheme.MonitorOut)
	monitor := container.NewStack(monitorBg, p.view)
	bottom := container.NewVBox(hline(), viewer.Transport(p.ctx.Session))
	center := kit.Panel("Programm", container.NewPadded(p.duration),
		container.NewBorder(nil, bottom, nil, nil, monitor))

	p.columns = container.New(columnsLayout{}, p.form.content, center, p.queue.content)
	p.root = container.NewPadded(p.columns)

	p.unsub = p.ctx.Session.Subscribe(p.sessionChanged)
	p.form.refreshProject()
	p.updateSummary()
	if p.pending != nil {
		p.ApplyLayout(*p.pending)
		p.pending = nil
	}
	return p.root
}

func (p *Page) sessionChanged(c session.Change) {
	if c&(session.ChangeProject|session.ChangePlayhead) == 0 {
		return
	}
	if c&session.ChangeProject != 0 {
		p.form.refreshProject()
	} else if p.form.rangeMoved() {
		p.form.refreshRange()
	} else {
		return // playhead only
	}
	p.updateSummary()
}

// updateSummary refreshes the timeline duration and the output summary of the form.
func (p *Page) updateSummary() {
	s := p.ctx.Session
	p.duration.Text = s.Project.Timecode(s.Project.Duration())
	p.duration.Refresh()
	p.form.updateSummary()
}

func (p *Page) Activate() {
	if p.form != nil {
		p.form.refreshProject()
		p.updateSummary()
	}
	if p.view != nil {
		p.view.SetActive(true)
	}
}

// Deactivate stops the hidden monitor from rendering every seek and playback tick.
func (p *Page) Deactivate() {
	if p.view != nil {
		p.view.SetActive(false)
	}
}

// Busy reports a running export or queued jobs, which closing the project would cancel.
func (p *Page) Busy() string {
	if p.rq == nil {
		return ""
	}
	return p.rq.busy()
}

// Wait lets cancelled exports remove their partial output before the app exits.
func (p *Page) Wait(timeout time.Duration) bool {
	if p.rq == nil {
		return true
	}
	return p.rq.wait(timeout)
}

func (p *Page) TypedKey(*fyne.KeyEvent) bool { return false }

// Close releases the subscription, the viewer and this page's hold on the render queue.
// Jobs are cancelled when no queue panel of the project is open any more.
func (p *Page) Close() {
	if p.closed {
		return
	}
	p.closed = true
	if pages[p.ctx.Session] == p {
		delete(pages, p.ctx.Session)
	}
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.queue != nil {
		p.queue.detach()
		p.rq.release()
	}
	if p.view != nil {
		p.view.Close()
	}
}

func (p *Page) showErr(err error) {
	if p.ctx.ShowErr != nil {
		p.ctx.ShowErr(err)
	}
}

// LayoutState reports which side columns are hidden and the chosen preset.
func (p *Page) LayoutState() layout.Page {
	if p.root == nil {
		if p.pending != nil {
			return *p.pending
		}
		return layout.Page{}
	}
	l := layout.Page{Hidden: map[string]bool{}}
	if !p.form.content.Visible() {
		l.Hidden[areaSettings] = true
	}
	if !p.queue.content.Visible() {
		l.Hidden[areaQueue] = true
	}
	if len(l.Hidden) == 0 {
		l.Hidden = nil
	}
	return l
}

// ApplyLayout shows or hides the settings and queue columns.
func (p *Page) ApplyLayout(l layout.Page) {
	if p.root == nil {
		p.pending = &l
		return
	}
	p.SetAreaVisible(areaSettings, !l.Hidden[areaSettings])
	p.SetAreaVisible(areaQueue, !l.Hidden[areaQueue])
}

// SetAreaVisible shows or hides the "settings" or "queue" column (e.g. while the queue
// is detached into its own window).
func (p *Page) SetAreaVisible(area string, on bool) {
	if p.root == nil {
		return
	}
	var o fyne.CanvasObject
	switch area {
	case areaSettings:
		o = p.form.content
	case areaQueue:
		o = p.queue.content
	default:
		return
	}
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
	p.columns.Refresh()
}
