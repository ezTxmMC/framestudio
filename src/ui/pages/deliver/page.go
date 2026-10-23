// Package deliver is the "deliver" workspace page.
//
// OWNER: deliver page agent. New(ctx) is the contract; do not change its signature.
package deliver

import (
	"os/exec"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
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

// columnsLayout places settings, monitor and queue side by side.
type columnsLayout struct{}

func (columnsLayout) widths(total float32) (left, right float32) {
	right = min(queueWidth, max(minQueueWidth, total/4))
	return settingsWidth, right
}

func (l columnsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) != 3 {
		return
	}
	lw, rw := l.widths(size.Width)
	cw := max(size.Width-lw-rw-2*columnGap, 0)
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(fyne.NewSize(lw, size.Height))
	objs[1].Move(fyne.NewPos(lw+columnGap, 0))
	objs[1].Resize(fyne.NewSize(cw, size.Height))
	objs[2].Move(fyne.NewPos(lw+cw+2*columnGap, 0))
	objs[2].Resize(fyne.NewSize(rw, size.Height))
}

func (l columnsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(settingsWidth+minQueueWidth+objs[1].MinSize().Width+2*columnGap, h)
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
	form     *settingsForm
	queue    *queue
	view     *viewer.Viewer
	duration *canvas.Text
	unsub    func()
}

func New(ctx *page.Context) page.Page { return newPage(ctx) }

func newPage(ctx *page.Context) *Page {
	return &Page{
		ctx: ctx,
		do:  kit.Do,
		openFolder: func(dir string) error {
			cmd := exec.Command("xdg-open", dir)
			if err := cmd.Start(); err != nil {
				return err
			}
			go cmd.Wait() // reap the child so it does not linger as a zombie
			return nil
		},
		sendNotification: func(n *fyne.Notification) {
			if a := fyne.CurrentApp(); a != nil {
				a.SendNotification(n)
			}
		},
	}
}

// SetToast connects the in-window toast of the app shell, used when a job finishes.
func (p *Page) SetToast(show func(title, message string, failed bool)) { p.toast = show }

// notify reports a finished or failed job on the desktop and in the window.
func (p *Page) notify(title, message string, failed bool) {
	if p.sendNotification != nil {
		p.sendNotification(fyne.NewNotification(title, message))
	}
	if p.toast != nil {
		p.toast(title, message, failed)
	}
}

func (p *Page) Content() fyne.CanvasObject {
	if p.root != nil {
		return p.root
	}
	p.form = newSettingsForm(p)
	p.queue = newQueue(p)
	p.form.onChange = p.updateSummary

	p.view = viewer.New(p.ctx.Session, p.ctx.Renderer)
	p.duration = kit.MonoText("", studiotheme.Muted, 12)
	monitorBg := canvas.NewRectangle(studiotheme.MonitorOut)
	monitor := container.NewStack(monitorBg, p.view)
	bottom := container.NewVBox(hline(), viewer.Transport(p.ctx.Session))
	center := kit.Panel("Programm", container.NewPadded(p.duration),
		container.NewBorder(nil, bottom, nil, nil, monitor))

	p.root = container.NewPadded(container.New(columnsLayout{}, p.form.content, center, p.queue.content))

	p.unsub = p.ctx.Session.Subscribe(p.sessionChanged)
	p.form.refreshProject()
	p.updateSummary()
	return p.root
}

func (p *Page) sessionChanged(c session.Change) {
	if c&(session.ChangeProject|session.ChangePlayhead) == 0 {
		return
	}
	if c&session.ChangeProject != 0 {
		p.form.refreshProject()
		p.queue.projectChanged()
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
	if p.queue == nil {
		return ""
	}
	return p.queue.busy()
}

// Wait lets cancelled exports remove their partial output before the app exits.
func (p *Page) Wait(timeout time.Duration) bool {
	if p.queue == nil {
		return true
	}
	return p.queue.wait(timeout)
}

func (p *Page) TypedKey(*fyne.KeyEvent) bool { return false }

// Close cancels running jobs and releases the subscription and viewer.
func (p *Page) Close() {
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.queue != nil {
		p.queue.close()
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
