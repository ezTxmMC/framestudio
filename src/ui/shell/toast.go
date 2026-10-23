package shell

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// toastUser is implemented by pages that announce finished background work (the
// deliver page); the shell hands them its window toast when they are created.
type toastUser interface {
	SetToast(show func(title, message string, failed bool))
}

// toastTime is how long a toast stays before it fades out by itself.
const toastTime = 6 * time.Second

// toaster shows short notices (finished exports) in the bottom-right corner of the
// window above the page bar, on every page and in the manager. It is a plain layer of
// the window content, not an overlay, so it never blocks clicks elsewhere.
type toaster struct {
	layer  *fyne.Container
	corner *cornerLayout
	card   *toastCard
	timer  *time.Timer
	gen    int
}

func newToaster() *toaster {
	t := &toaster{card: newToastCard()}
	t.card.Hide()
	t.card.onTap = t.hide
	t.corner = &cornerLayout{bottom: pageBarHeight + 14, right: 14}
	t.layer = container.New(t.corner, t.card)
	return t
}

// setBottom keeps the toast above the bottom bar of the given height.
func (t *toaster) setBottom(inset float32) {
	t.corner.bottom = inset
	t.layer.Refresh()
}

// show displays title and message; failed uses the warning accent. UI thread only.
func (t *toaster) show(title, message string, failed bool) {
	t.card.set(title, message, failed)
	t.card.Show()
	t.layer.Refresh()
	t.gen++
	gen := t.gen
	t.stopTimer()
	t.timer = time.AfterFunc(toastTime, func() {
		kit.Do(func() {
			if t.gen == gen {
				t.hide()
			}
		})
	})
}

func (t *toaster) hide() {
	t.stopTimer()
	t.card.Hide()
	t.layer.Refresh()
}

func (t *toaster) stopTimer() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// toastCard is the notice itself: accent bar, bold title and one line of detail.
type toastCard struct {
	widget.BaseWidget
	accent *canvas.Rectangle
	title  *canvas.Text
	detail *canvas.Text
	onTap  func()
}

func newToastCard() *toastCard {
	c := &toastCard{accent: canvas.NewRectangle(studiotheme.Steel),
		title: text("", studiotheme.Text, 13, true), detail: text("", studiotheme.Muted, 12, false)}
	c.accent.SetMinSize(fyne.NewSize(3, 0))
	c.ExtendBaseWidget(c)
	return c
}

func (c *toastCard) set(title, detail string, failed bool) {
	c.title.Text = title
	c.detail.Text = ellipsize(detail, 64)
	c.accent.FillColor = studiotheme.Steel
	if failed {
		c.accent.FillColor = color.NRGBA{0xE0, 0xA1, 0x3A, 0xFF}
	}
	c.Refresh()
}

func (c *toastCard) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(studiotheme.Raised)
	bg.CornerRadius = 4
	bg.StrokeColor = studiotheme.Border
	bg.StrokeWidth = 1
	shadow := canvas.NewRectangle(color.NRGBA{0, 0, 0, 0x50})
	shadow.CornerRadius = 6
	body := container.New(layout.NewCustomPaddedLayout(9, 10, 12, 16),
		container.New(layout.NewCustomPaddedVBoxLayout(3), c.title, c.detail))
	card := container.NewStack(bg, container.NewBorder(nil, nil,
		container.New(layout.NewCustomPaddedLayout(6, 6, 0, 0), c.accent), nil, body))
	return widget.NewSimpleRenderer(container.NewStack(
		container.New(layout.NewCustomPaddedLayout(2, -2, 2, -2), shadow), card))
}

func (c *toastCard) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

// cornerLayout places its visible objects at their minimum size in the bottom-right
// corner, inset by the margins.
type cornerLayout struct{ bottom, right float32 }

func (l *cornerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		ms := o.MinSize()
		o.Resize(ms)
		o.Move(fyne.NewPos(size.Width-ms.Width-l.right, size.Height-ms.Height-l.bottom))
	}
}

func (l *cornerLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }
