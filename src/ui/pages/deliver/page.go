// Package deliver is the "deliver" workspace page.
//
// OWNER: deliver page agent. New(ctx) is the contract; do not change its signature.
package deliver

import (
	"os/exec"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/viewer"
)

// Panel widths of the settings (left) and queue (right) columns.
const (
	settingsWidth = 340
	queueWidth    = 360
)

// Page is the Resolve-style render page: settings, program monitor and render queue.
type Page struct {
	ctx *page.Context

	// do runs f on the UI thread; tests replace it with a queue they drain themselves.
	do func(f func())
	// openFolder shows a folder in the file manager.
	openFolder func(dir string) error

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
		do:  fyne.Do,
		openFolder: func(dir string) error {
			return exec.Command("xdg-open", dir).Start()
		},
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

	left := withWidth(settingsWidth, p.form.content)
	right := withWidth(queueWidth, p.queue.content)
	p.root = container.NewPadded(container.NewBorder(nil, nil, left, right, center))

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
}

func (p *Page) Deactivate()                  {}
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
