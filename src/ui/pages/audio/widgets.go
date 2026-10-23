package audio

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// amber marks an active solo, like the yellow solo buttons of a mixing console.
var amber = color.NRGBA{0xD6, 0xA2, 0x3E, 0xFF}

func lockIcon(c color.NRGBA) fyne.Resource {
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#%02X%02X%02X" `+
		`d="M12 2a5 5 0 0 0-5 5v3H6.5A1.5 1.5 0 0 0 5 11.5v9A1.5 1.5 0 0 0 6.5 22h11a1.5 1.5 0 0 0 1.5-1.5v-9`+
		`A1.5 1.5 0 0 0 17.5 10H17V7a5 5 0 0 0-5-5zm-3 8V7a3 3 0 0 1 6 0v3H9z"/></svg>`, c.R, c.G, c.B)
	return fyne.NewStaticResource(fmt.Sprintf("lock-%02x%02x%02x.svg", c.R, c.G, c.B), []byte(svg))
}

var (
	lockOff = lockIcon(studiotheme.Muted)
	lockOn  = lockIcon(studiotheme.Gap)
)

// toggle is a compact console button (M, S, lock) that lights up in its active colour.
type toggle struct {
	widget.BaseWidget
	label    string
	icon     [2]fyne.Resource // off, on
	active   color.NRGBA
	on       bool
	hovered  bool
	OnTapped func()
}

func newToggle(label string, active color.NRGBA, tap func()) *toggle {
	t := &toggle{label: label, active: active, OnTapped: tap}
	t.ExtendBaseWidget(t)
	return t
}

func newIconToggle(off, on fyne.Resource, active color.NRGBA, tap func()) *toggle {
	t := &toggle{icon: [2]fyne.Resource{off, on}, active: active, OnTapped: tap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *toggle) SetOn(on bool) {
	if t.on != on {
		t.on = on
		t.Refresh()
	}
}

func (t *toggle) Tapped(*fyne.PointEvent) {
	if t.OnTapped != nil {
		t.OnTapped()
	}
}

func (t *toggle) MouseIn(*desktop.MouseEvent)    { t.hovered = true; t.Refresh() }
func (t *toggle) MouseMoved(*desktop.MouseEvent) {}
func (t *toggle) MouseOut()                      { t.hovered = false; t.Refresh() }

func (t *toggle) MinSize() fyne.Size { return fyne.NewSize(24, 19) }

func (t *toggle) CreateRenderer() fyne.WidgetRenderer {
	r := &toggleRenderer{t: t, bg: canvas.NewRectangle(color.Transparent), text: canvas.NewText(t.label, studiotheme.Muted)}
	r.bg.CornerRadius = 3
	r.bg.StrokeWidth = 1
	r.text.TextSize = 11
	r.text.TextStyle = fyne.TextStyle{Bold: true}
	r.text.Alignment = fyne.TextAlignCenter
	r.img = canvas.NewImageFromResource(t.icon[0])
	r.img.FillMode = canvas.ImageFillContain
	r.Refresh()
	return r
}

type toggleRenderer struct {
	t    *toggle
	bg   *canvas.Rectangle
	text *canvas.Text
	img  *canvas.Image
}

func (r *toggleRenderer) Layout(s fyne.Size) {
	r.bg.Resize(s)
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos(0, (s.Height-ts.Height)/2))
	r.text.Resize(fyne.NewSize(s.Width, ts.Height))
	const ic = 11
	r.img.Move(fyne.NewPos((s.Width-ic)/2, (s.Height-ic)/2))
	r.img.Resize(fyne.NewSquareSize(ic))
}

func (r *toggleRenderer) MinSize() fyne.Size { return r.t.MinSize() }

func (r *toggleRenderer) Refresh() {
	t := r.t
	switch {
	case t.on:
		r.bg.FillColor, r.bg.StrokeColor = t.active, t.active
		r.text.Color = studiotheme.Gap
	case t.hovered:
		r.bg.FillColor, r.bg.StrokeColor = studiotheme.Border, studiotheme.Faint
		r.text.Color = studiotheme.Text
	default:
		r.bg.FillColor, r.bg.StrokeColor = studiotheme.Lane, studiotheme.Border
		r.text.Color = studiotheme.Muted
	}
	if t.icon[0] != nil {
		r.img.Resource = t.icon[0]
		if t.on {
			r.img.Resource = t.icon[1]
		}
		r.img.Show()
		r.text.Hide()
	} else {
		r.img.Hide()
		r.text.Show()
	}
	r.bg.Refresh()
	r.text.Refresh()
	r.img.Refresh()
}

func (r *toggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.text, r.img}
}

func (r *toggleRenderer) Destroy() {}

// tapArea makes any content tappable, e.g. a track header that selects its track.
type tapArea struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

func newTapArea(content fyne.CanvasObject, tap func()) *tapArea {
	t := &tapArea{content: content, onTap: tap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapArea) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *tapArea) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.content) }

// fitText shortens s with an ellipsis so it fits into width w.
func fitText(s string, size float32, style fyne.TextStyle, w float32) string {
	if w <= 0 {
		return ""
	}
	if fyne.MeasureText(s, size, style).Width <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 {
		r = r[:len(r)-1]
		if t := string(r) + "…"; fyne.MeasureText(t, size, style).Width <= w {
			return t
		}
	}
	return ""
}

// fixedSize is a transparent spacer with a fixed minimum size.
func fixedSize(w, h float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 0xFF}
}

// hCenter centres its objects horizontally at their minimum width and full height.
type hCenter struct{}

func (hCenter) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		w := min(o.MinSize().Width, size.Width)
		o.Move(fyne.NewPos((size.Width-w)/2, 0))
		o.Resize(fyne.NewSize(w, size.Height))
	}
}

func (hCenter) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		s = s.Max(o.MinSize())
	}
	return s
}

var trashIcon = fyne.NewStaticResource("trash.svg", []byte(fmt.Sprintf(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#%02X%02X%02X" `+
		`d="M9 3h6l1 2h4v2H4V5h4l1-2zM6 9h12l-1 12H7L6 9zm4 2v8h1.5v-8H10zm2.5 0v8H14v-8h-1.5z"/></svg>`,
	studiotheme.Muted.R, studiotheme.Muted.G, studiotheme.Muted.B)))

// hScrollBar pans the lanes: the thumb shows the visible part of the program.
type hScrollBar struct {
	widget.BaseWidget
	pg      *Page
	hovered bool
	drag    bool
}

const hbarH = 10

func newHScrollBar(pg *Page) *hScrollBar {
	b := &hScrollBar{pg: pg}
	b.ExtendBaseWidget(b)
	return b
}

// thumb returns the thumb's x and width on a bar w pixels wide.
func (b *hScrollBar) thumb(w float32) (x, tw float32) {
	span := b.pg.span()
	v := b.pg.view
	tw = max(float32(v.visible(span)/float64(span))*w, 24)
	if tw >= w {
		return 0, w
	}
	free := float64(span) - v.visible(span)
	return float32(v.start/free) * (w - tw), tw
}

func (b *hScrollBar) Dragged(e *fyne.DragEvent) {
	b.drag = true
	w := b.Size().Width
	_, tw := b.thumb(w)
	if w-tw <= 0 {
		return
	}
	span := b.pg.span()
	free := float64(span) - b.pg.view.visible(span)
	lw := b.pg.laneWidth()
	if lw <= 0 {
		return
	}
	// convert the thumb movement to lane pixels
	frames := float64(e.Dragged.DX) / float64(w-tw) * free
	b.pg.scrollLanes(float32(frames / b.pg.view.visible(span) * float64(lw)))
}

func (b *hScrollBar) DragEnd() { b.drag = false; b.Refresh() }

// Tapped pages towards the click.
func (b *hScrollBar) Tapped(e *fyne.PointEvent) {
	x, tw := b.thumb(b.Size().Width)
	lw := b.pg.laneWidth()
	switch {
	case e.Position.X < x:
		b.pg.scrollLanes(-lw * 0.9)
	case e.Position.X > x+tw:
		b.pg.scrollLanes(lw * 0.9)
	}
}

func (b *hScrollBar) Scrolled(e *fyne.ScrollEvent) {
	if !b.pg.wheel(e, -1) {
		b.pg.scrollLanes(-e.Scrolled.DY * 3)
	}
}

func (b *hScrollBar) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *hScrollBar) MouseMoved(*desktop.MouseEvent) {}
func (b *hScrollBar) MouseOut()                      { b.hovered = false; b.Refresh() }

func (b *hScrollBar) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(b, fyne.NewSize(40, hbarH), func(size fyne.Size) []fyne.CanvasObject {
		objs := []fyne.CanvasObject{place(canvas.NewRectangle(studiotheme.Panel), 0, 0, size.Width, size.Height)}
		x, tw := b.thumb(size.Width)
		if tw >= size.Width {
			return objs // everything fits
		}
		c := studiotheme.Faint
		if b.hovered || b.drag {
			c = studiotheme.Muted
		}
		th := canvas.NewRectangle(c)
		th.CornerRadius = 3
		return append(objs, place(th, x+1, 2, tw-2, size.Height-4))
	})
}
