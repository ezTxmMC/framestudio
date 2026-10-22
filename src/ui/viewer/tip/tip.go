// Package tip provides flat icon buttons with hover tooltips. Fyne has no tooltips and
// pop-ups grab all input, so tips are drawn by a Layer stacked over the toolbar instead.
package tip

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// Delay is how long the pointer must rest on a button before its tip appears.
const Delay = 550 * time.Millisecond

// Layer draws the tooltips of its buttons. Stack it over the bar that holds them,
// container.NewStack(bar, layer); it never takes input.
type Layer struct {
	widget.BaseWidget
	below bool // tips under the button (toolbars) instead of above (transport)

	bg   *canvas.Rectangle
	text *canvas.Text
	gen  int
}

// NewLayer creates a tooltip layer. With below set, tips open under the buttons.
func NewLayer(below bool) *Layer {
	l := &Layer{below: below}
	l.bg = canvas.NewRectangle(studiotheme.Raised)
	l.bg.StrokeColor = studiotheme.Border
	l.bg.StrokeWidth = 1
	l.bg.CornerRadius = 3
	l.text = canvas.NewText("", studiotheme.Text)
	l.text.TextSize = 12
	l.bg.Hide()
	l.text.Hide()
	l.ExtendBaseWidget(l)
	return l
}

func (l *Layer) CreateRenderer() fyne.WidgetRenderer { return &layerRenderer{l: l} }

func (l *Layer) MinSize() fyne.Size { return fyne.Size{} }

// Button creates a flat icon button whose tooltip is drawn by this layer.
func (l *Layer) Button(icon fyne.Resource, tip string, tap func()) *Button {
	b := &Button{Tip: tip, layer: l}
	b.Icon = icon
	b.OnTapped = tap
	b.Importance = widget.LowImportance
	b.ExtendBaseWidget(b)
	return b
}

// Showing reports whether a tooltip is visible and returns its text.
func (l *Layer) Showing() (string, bool) { return l.text.Text, l.bg.Visible() }

func (l *Layer) schedule(b *Button) {
	l.gen++
	gen := l.gen
	time.AfterFunc(Delay, func() {
		fyne.Do(func() {
			if gen == l.gen {
				l.ShowTip(b)
			}
		})
	})
}

// ShowTip displays the tip of b immediately.
func (l *Layer) ShowTip(b *Button) {
	d := fyne.CurrentApp().Driver()
	if b.Tip == "" || d.CanvasForObject(b) == nil || d.CanvasForObject(l) == nil {
		return
	}
	l.text.Text = b.Tip
	l.text.Refresh()
	ts := l.text.MinSize()
	size := fyne.NewSize(ts.Width+14, ts.Height+8)
	pos := d.AbsolutePositionForObject(b).Subtract(d.AbsolutePositionForObject(l))
	x := pos.X + (b.Size().Width-size.Width)/2
	y := pos.Y - size.Height - 4
	if l.below {
		y = pos.Y + b.Size().Height + 4
	}
	if right := l.Size().Width - size.Width - 2; x > right {
		x = right
	}
	if x < 2 {
		x = 2
	}
	l.bg.Resize(size)
	l.bg.Move(fyne.NewPos(x, y))
	l.text.Resize(ts)
	l.text.Move(fyne.NewPos(x+7, y+4))
	l.bg.Show()
	l.text.Show()
	l.bg.Refresh()
}

// HideTip removes a visible tip and cancels a pending one.
func (l *Layer) HideTip() {
	l.gen++
	if l.bg.Visible() {
		l.bg.Hide()
		l.text.Hide()
	}
}

type layerRenderer struct{ l *Layer }

func (r *layerRenderer) Layout(fyne.Size)             {}
func (r *layerRenderer) MinSize() fyne.Size           { return fyne.Size{} }
func (r *layerRenderer) Refresh()                     {}
func (r *layerRenderer) Destroy()                     {}
func (r *layerRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.l.bg, r.l.text} }

// Button is a flat icon button that shows Tip after resting the pointer on it.
type Button struct {
	widget.Button
	Tip   string
	layer *Layer
}

var _ desktop.Hoverable = (*Button)(nil)

// SetTip changes the tooltip text, e.g. "Rückgängig: Clip teilen".
func (b *Button) SetTip(s string) { b.Tip = s }

func (b *Button) MouseIn(e *desktop.MouseEvent) {
	b.Button.MouseIn(e)
	if b.layer != nil && !b.Disabled() {
		b.layer.schedule(b)
	}
}

func (b *Button) MouseOut() {
	b.Button.MouseOut()
	if b.layer != nil {
		b.layer.HideTip()
	}
}

func (b *Button) Tapped(e *fyne.PointEvent) {
	if b.layer != nil {
		b.layer.HideTip()
	}
	b.Button.Tapped(e)
}
