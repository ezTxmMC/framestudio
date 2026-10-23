package deliver

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func init() {
	page.RegisterPanel(page.PanelInfo{
		ID: page.PanelQueue, Title: "Render-Warteschlange", Icon: theme.UploadIcon(),
		New: func(ctx *page.Context) page.Panel { return newQueuePanel(ctx) },
	})
}

// queuePanel is the render queue in its own window. It shows the same jobs as the
// deliver page (the session's shared renderQueue); jobs are added on the deliver page.
type queuePanel struct {
	rq      *renderQueue
	view    *queue
	content fyne.CanvasObject
	closed  bool
}

func newQueuePanel(ctx *page.Context) *queuePanel {
	rq := sharedQueue(ctx)
	if rq.showErr == nil {
		rq.showErr = ctx.ShowErr
	}
	qp := &queuePanel{rq: rq, view: newQueue(rq, "Aufträge auf der Seite „Export“ hinzufügen")}
	bg := canvas.NewRectangle(studiotheme.Gap)
	qp.content = container.NewStack(bg, container.NewPadded(qp.view.content))
	return qp
}

func (qp *queuePanel) Content() fyne.CanvasObject { return qp.content }

func (qp *queuePanel) TypedKey(*fyne.KeyEvent) bool { return false }

// Close detaches the view. When the deliver page does not exist, closing the last view
// cancels running exports, like closing the project does.
func (qp *queuePanel) Close() {
	if qp.closed {
		return
	}
	qp.closed = true
	qp.view.detach()
	qp.rq.release()
}

// Busy and Wait mirror the deliver page, for a panel that outlives it.
func (qp *queuePanel) Busy() string { return qp.rq.busy() }

func (qp *queuePanel) Wait(timeout time.Duration) bool { return qp.rq.wait(timeout) }
