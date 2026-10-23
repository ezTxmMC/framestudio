package deliver

import (
	"fmt"
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// queue is a "Render-Warteschlange" view of a session's renderQueue: the right column
// of the deliver page or the detachable queue panel. The embedded renderQueue is shared;
// the cards belong to the view.
type queue struct {
	*renderQueue
	cards map[*job]*jobView

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

// newQueue builds a view of rq; hint is the second line of the empty state.
func newQueue(rq *renderQueue, hint string) *queue {
	q := &queue{renderQueue: rq, cards: map[*job]*jobView{}}
	q.list = container.NewVBox()
	title := smallText("Keine Aufträge", studiotheme.Muted, 12)
	sub := smallText(hint, studiotheme.Faint, 11)
	q.empty = container.NewCenter(container.NewVBox(container.NewCenter(title), container.NewCenter(sub)))
	q.count = kit.MonoText("0", studiotheme.Faint, 11)
	q.all = widget.NewButtonWithIcon("Alle rendern", theme.MediaPlayIcon(), rq.renderAll)
	q.all.Importance = widget.HighImportance
	clear := widget.NewButton("Fertige entfernen", rq.clearFinished)
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
	rq.attach(q)
	q.sync()
	return q
}

// detach stops the view from following the queue (its page or window closes).
func (q *queue) detach() { q.renderQueue.detach(q) }

// refresh redraws the whole view.
func (q *queue) refresh() { q.sync() }

// sync matches the cards to the jobs and updates the header, the status line and
// "Alle rendern".
func (q *queue) sync() {
	objs := make([]fyne.CanvasObject, len(q.jobs))
	for i, j := range q.jobs {
		v := q.cards[j]
		if v == nil {
			v = newJobView(q, j)
			q.cards[j] = v
		}
		objs[i] = v.content
	}
	for j := range q.cards {
		if !slices.Contains(q.jobs, j) {
			delete(q.cards, j)
		}
	}
	if !slices.Equal(objs, q.list.Objects) {
		q.list.Objects = objs
		q.list.Refresh()
	}
	finished := 0
	for i, j := range q.jobs {
		if j.status == jobDone || j.status == jobCanceled {
			finished++
		}
		v := q.cards[j]
		v.refresh()
		v.setOrder(i > 0, i < len(q.jobs)-1)
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
	if q.hasReady() {
		q.all.Enable()
	} else {
		q.all.Disable()
	}
	q.syncStatus()
}

// syncStatus shows the line about the last finished job.
func (q *queue) syncStatus() {
	if q.lastTitle == "" {
		setVisible(q.statusBox, false, q.content)
		return
	}
	mark := color.Color(studiotheme.Steel)
	if q.lastFailed {
		mark = color.NRGBA{0xE0, 0xA1, 0x3A, 0xFF}
	}
	if q.statusMark.FillColor != mark {
		q.statusMark.FillColor = mark
		q.statusMark.Refresh()
	}
	if text := q.lastTitle + ": " + ellipsize(q.lastName, 32); text != q.status.Text {
		q.status.Text = text
		q.status.Refresh()
	}
	setVisible(q.statusBox, true, q.content)
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
	v.meta = kit.MonoText(jobSummary(j.opts, j.fps, j.frames), studiotheme.Muted, 10.5)
	v.bar = widget.NewProgressBar()
	v.eta = kit.MonoText("", studiotheme.Muted, 11)
	v.result = widget.NewLabel("")
	v.result.Wrapping = fyne.TextWrapWord
	v.result.SizeName = theme.SizeNameCaptionText

	v.cancel = widget.NewButtonWithIcon("Abbrechen", theme.CancelIcon(), func() { q.cancelJob(j) })
	v.remove = widget.NewButtonWithIcon("Entfernen", theme.DeleteIcon(), func() { q.remove(j) })
	v.open = widget.NewButtonWithIcon("Ordner öffnen", theme.FolderOpenIcon(), func() { q.openFolder(j) })
	v.render = widget.NewButtonWithIcon("Rendern", theme.MediaPlayIcon(), func() { q.arm(j) })
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
	return v
}

func (v *jobView) refresh() {
	j := v.j
	if meta := jobSummary(j.opts, j.fps, j.frames); meta != v.meta.Text {
		v.meta.Text = meta
		v.meta.Refresh()
	}
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
	} else if v.render.Text != "Rendern" {
		v.render.SetText("Rendern")
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
