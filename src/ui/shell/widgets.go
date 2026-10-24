package shell

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

var hoverTint = color.NRGBA{0xFF, 0xFF, 0xFF, 0x0C}

// strut is an invisible rectangle that gives a container a minimum size.
func strut(w, h float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

func text(s string, c color.Color, size float32, bold bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle.Bold = bold
	return t
}

// vline is a 1 px vertical divider.
func vline(h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(studiotheme.Border)
	r.SetMinSize(fyne.NewSize(1, h))
	return container.NewCenter(r)
}

// ellipsize shortens s to at most n runes.
func ellipsize(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// iconTap is a small flat icon button with a hover plate, used for tab close and bar tools.
type iconTap struct {
	widget.BaseWidget
	icon  *canvas.Image
	bg    *canvas.Rectangle
	size  float32
	onTap func()
}

func newIconTap(res fyne.Resource, size float32, tap func()) *iconTap {
	b := &iconTap{icon: canvas.NewImageFromResource(theme.NewThemedResource(res)), size: size, onTap: tap}
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.Translucency = 0.35
	b.bg = canvas.NewRectangle(color.Transparent)
	b.bg.CornerRadius = 3
	b.ExtendBaseWidget(b)
	return b
}

func (b *iconTap) CreateRenderer() fyne.WidgetRenderer {
	pad := (b.MinSize().Width - b.size) / 2
	return widget.NewSimpleRenderer(container.NewStack(b.bg,
		container.New(layout.NewCustomPaddedLayout(pad, pad, pad, pad), b.icon)))
}

func (b *iconTap) MinSize() fyne.Size             { return fyne.NewSquareSize(b.size + 8) }
func (b *iconTap) Tapped(*fyne.PointEvent)        { b.onTap() }
func (b *iconTap) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (b *iconTap) MouseMoved(*desktop.MouseEvent) {}
func (b *iconTap) MouseIn(*desktop.MouseEvent) {
	b.bg.FillColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x18}
	b.icon.Translucency = 0
	b.Refresh()
}
func (b *iconTap) MouseOut() {
	b.bg.FillColor = color.Transparent
	b.icon.Translucency = 0.35
	b.Refresh()
}

// tabButton is one project tab in the top bar: name, dirty dot, close cross and a Steel
// underline when active.
type tabButton struct {
	widget.BaseWidget
	name     *canvas.Text
	dot      *canvas.Circle
	bg, line *canvas.Rectangle
	closeBtn *iconTap
	onTap    func()
	active   bool
	hovered  bool
}

func newTabButton(onTap, onClose func()) *tabButton {
	t := &tabButton{
		name:  text("", studiotheme.Muted, 12, false),
		dot:   canvas.NewCircle(color.Transparent),
		bg:    canvas.NewRectangle(color.Transparent),
		line:  canvas.NewRectangle(color.Transparent),
		onTap: onTap,
	}
	t.closeBtn = newIconTap(theme.CancelIcon(), 12, onClose)
	t.line.SetMinSize(fyne.NewSize(1, 2))
	t.ExtendBaseWidget(t)
	return t
}

// set updates the label; dirty shows the unsaved dot.
func (t *tabButton) set(name string, dirty, active bool) {
	t.name.Text = ellipsize(name, 30)
	t.active = active
	if dirty {
		t.dot.FillColor = studiotheme.Text
	} else {
		t.dot.FillColor = color.Transparent
	}
	t.restyle()
}

func (t *tabButton) restyle() {
	switch {
	case t.active:
		t.bg.FillColor = studiotheme.Panel
		t.line.FillColor = studiotheme.Steel
		t.name.Color = studiotheme.Text
	case t.hovered:
		t.bg.FillColor = hoverTint
		t.line.FillColor = color.Transparent
		t.name.Color = studiotheme.Text
	default:
		t.bg.FillColor = color.Transparent
		t.line.FillColor = color.Transparent
		t.name.Color = studiotheme.Muted
	}
	t.Refresh()
}

func (t *tabButton) CreateRenderer() fyne.WidgetRenderer {
	dot := container.NewCenter(container.NewGridWrap(fyne.NewSquareSize(6), t.dot))
	row := container.NewHBox(container.NewCenter(t.name), strut(2, 0), dot, container.NewCenter(t.closeBtn))
	body := container.New(layout.NewCustomPaddedLayout(0, 0, 14, 4), row)
	return widget.NewSimpleRenderer(container.NewStack(t.bg, body, container.NewBorder(nil, t.line, nil, nil)))
}

func (t *tabButton) Tapped(*fyne.PointEvent)        { t.onTap() }
func (t *tabButton) MouseMoved(*desktop.MouseEvent) {}
func (t *tabButton) MouseIn(*desktop.MouseEvent)    { t.hovered = true; t.restyle() }
func (t *tabButton) MouseOut()                      { t.hovered = false; t.restyle() }

// pageButton is an entry of the bottom page bar: icon over label, Steel when active.
type pageButton struct {
	widget.BaseWidget
	id      string
	res     fyne.Resource
	icon    *canvas.Image
	label   *canvas.Text
	marker  *canvas.Rectangle
	bg      *canvas.Rectangle
	onTap   func()
	active  bool
	hovered bool
}

func newPageButton(id, label string, res fyne.Resource, tap func()) *pageButton {
	b := &pageButton{id: id, res: res, onTap: tap,
		icon:   canvas.NewImageFromResource(theme.NewThemedResource(res)),
		label:  text(label, studiotheme.Muted, 11, false),
		marker: canvas.NewRectangle(color.Transparent),
		bg:     canvas.NewRectangle(color.Transparent),
	}
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(fyne.NewSquareSize(16))
	b.marker.SetMinSize(fyne.NewSize(1, 2))
	b.bg.CornerRadius = 3
	b.ExtendBaseWidget(b)
	b.setActive(false)
	return b
}

func (b *pageButton) setActive(on bool) {
	b.active = on
	b.restyle()
}

func (b *pageButton) restyle() {
	if b.active {
		b.icon.Resource = theme.NewPrimaryThemedResource(b.res)
		b.icon.Translucency = 0
		b.label.Color = studiotheme.Steel
		b.marker.FillColor = studiotheme.Steel
	} else {
		b.icon.Resource = theme.NewThemedResource(b.res)
		b.icon.Translucency = 0.45
		b.label.Color = studiotheme.Muted
		b.marker.FillColor = color.Transparent
		if b.hovered {
			b.icon.Translucency = 0.1
			b.label.Color = studiotheme.Text
		}
	}
	if b.hovered {
		b.bg.FillColor = hoverTint
	} else {
		b.bg.FillColor = color.Transparent
	}
	b.Refresh()
}

func (b *pageButton) CreateRenderer() fyne.WidgetRenderer {
	marker := container.New(layout.NewCustomPaddedLayout(0, 0, 18, 18), b.marker)
	body := container.NewCenter(container.NewHBox(container.NewCenter(b.icon), container.NewCenter(b.label)))
	return widget.NewSimpleRenderer(container.NewStack(b.bg, container.NewBorder(marker, nil, nil, nil, body)))
}

func (b *pageButton) MinSize() fyne.Size {
	return fyne.NewSize(92, b.BaseWidget.MinSize().Height)
}

func (b *pageButton) Tapped(*fyne.PointEvent)        { b.onTap() }
func (b *pageButton) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (b *pageButton) MouseMoved(*desktop.MouseEvent) {}
func (b *pageButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.restyle() }
func (b *pageButton) MouseOut()                      { b.hovered = false; b.restyle() }
