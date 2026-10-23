package workspace

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

const barButtonHeight = 20

// barButton is a flat, compact button for the title bar of a detached window: an icon
// and a short text, 20 px high so it fits the 24 px bar.
type barButton struct {
	widget.BaseWidget
	Text         string
	icon         fyne.Resource
	trailingIcon bool // icon after the text
	tap          func()
	hover        bool
}

func newBarButton(text string, icon fyne.Resource, tap func()) *barButton {
	b := &barButton{Text: text, icon: icon, tap: tap}
	b.ExtendBaseWidget(b)
	return b
}

// SetText changes the label.
func (b *barButton) SetText(s string) {
	if b.Text != s {
		b.Text = s
		b.Refresh()
	}
}

// SetIcon changes the icon.
func (b *barButton) SetIcon(r fyne.Resource) {
	b.icon = r
	b.Refresh()
}

func (b *barButton) Tapped(*fyne.PointEvent) {
	if b.tap != nil {
		b.tap()
	}
}
func (b *barButton) MouseIn(*desktop.MouseEvent)    { b.hover = true; b.Refresh() }
func (b *barButton) MouseMoved(*desktop.MouseEvent) {}
func (b *barButton) MouseOut()                      { b.hover = false; b.Refresh() }
func (b *barButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (b *barButton) CreateRenderer() fyne.WidgetRenderer {
	r := &barButtonRenderer{b: b,
		bg:   canvas.NewRectangle(color.Transparent),
		icon: canvas.NewImageFromResource(nil),
		text: canvas.NewText("", studiotheme.Text)}
	r.bg.CornerRadius = 3
	r.icon.FillMode = canvas.ImageFillContain
	r.text.TextSize = 11
	r.Refresh()
	return r
}

type barButtonRenderer struct {
	b    *barButton
	bg   *canvas.Rectangle
	icon *canvas.Image
	text *canvas.Text
}

const (
	barPad  = 6
	barIcon = 13
	barGap  = 4
)

func (r *barButtonRenderer) MinSize() fyne.Size {
	w := float32(barPad * 2)
	if r.b.icon != nil {
		w += barIcon
	}
	if r.b.Text != "" {
		if r.b.icon != nil {
			w += barGap
		}
		w += fyne.MeasureText(r.b.Text, r.text.TextSize, r.text.TextStyle).Width
	}
	return fyne.NewSize(w, barButtonHeight)
}

func (r *barButtonRenderer) Layout(s fyne.Size) {
	y := (s.Height - barButtonHeight) / 2
	r.bg.Move(fyne.NewPos(0, y))
	r.bg.Resize(fyne.NewSize(s.Width, barButtonHeight))
	x := float32(barPad)
	tw := fyne.MeasureText(r.b.Text, r.text.TextSize, r.text.TextStyle).Width
	th := fyne.MeasureText("Ag", r.text.TextSize, r.text.TextStyle).Height
	place := func(icon bool) {
		if icon && r.b.icon != nil {
			r.icon.Resize(fyne.NewSize(barIcon, barIcon))
			r.icon.Move(fyne.NewPos(x, (s.Height-barIcon)/2))
			x += barIcon + barGap
		} else if !icon && r.b.Text != "" {
			r.text.Resize(fyne.NewSize(tw, th))
			r.text.Move(fyne.NewPos(x, (s.Height-th)/2))
			x += tw + barGap
		}
	}
	if r.b.trailingIcon {
		place(false)
		place(true)
	} else {
		place(true)
		place(false)
	}
}

func (r *barButtonRenderer) Refresh() {
	r.bg.FillColor = color.Transparent
	if r.b.hover {
		r.bg.FillColor = studiotheme.Raised
	}
	r.icon.Resource = nil
	if r.b.icon != nil {
		r.icon.Resource = theme.NewThemedResource(r.b.icon)
	}
	r.text.Text = r.b.Text
	r.text.Color = studiotheme.Text
	r.bg.Refresh()
	r.icon.Refresh()
	r.text.Refresh()
}

func (r *barButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.icon, r.text}
}
func (r *barButtonRenderer) Destroy() {}
