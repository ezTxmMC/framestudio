package media

import (
	"fmt"
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	cardW, cardH = 180, 146
	thumbH       = 96 // 16:9 area at the top of a card
	cardPad      = 5  // around the thumbnail
	textPad      = 8  // left and right of the text lines
)

// card is one media pool tile: thumbnail, name with kind icon and a timecode/size line.
type card struct {
	widget.BaseWidget
	onTap       func()
	onSecondary func(pos fyne.Position)

	bg, frame, well *canvas.Rectangle
	thumb           *canvas.Image
	placeholder     *canvas.Image // kind icon shown until the thumbnail is ready
	icon            *canvas.Image
	name            *canvas.Text
	info, detail    *canvas.Text
	badge           *canvas.Rectangle
	badgeText       *canvas.Text
	fullName        string
}

func newCard() *card {
	c := &card{
		bg:          canvas.NewRectangle(studiotheme.Raised),
		frame:       canvas.NewRectangle(color.Transparent),
		well:        canvas.NewRectangle(studiotheme.MonitorOut),
		thumb:       canvas.NewImageFromImage(nil),
		placeholder: canvas.NewImageFromResource(nil),
		icon:        canvas.NewImageFromResource(theme.MediaVideoIcon()),
		name:        canvas.NewText("", studiotheme.Text),
		info:        kit.MonoText("", studiotheme.Muted, 11),
		detail:      kit.MonoText("", studiotheme.Faint, 11),
		badge:       canvas.NewRectangle(studiotheme.Tally),
		badgeText:   canvas.NewText("fehlt", color.White),
	}
	c.bg.CornerRadius = 4
	c.frame.CornerRadius = 4
	c.frame.StrokeWidth = 2
	c.well.CornerRadius = 2
	c.thumb.FillMode = canvas.ImageFillContain
	c.thumb.ScaleMode = canvas.ImageScaleSmooth
	c.name.TextSize = 12
	c.badge.CornerRadius = 3
	c.badgeText.TextSize = 10
	c.badgeText.TextStyle = fyne.TextStyle{Bold: true}
	c.ExtendBaseWidget(c)
	return c
}

func kindIcon(k model.ClipKind) fyne.Resource {
	switch k {
	case model.KindImage:
		return theme.MediaPhotoIcon()
	case model.KindAudio:
		return theme.MediaMusicIcon()
	}
	return theme.MediaVideoIcon()
}

// infoLine returns the primary (duration or size) and secondary text of a media item.
func infoLine(m model.MediaItem, fps int) (string, string) {
	size := ""
	if m.Width > 0 {
		size = fmt.Sprintf("%d×%d", m.Width, m.Height)
	}
	switch m.Kind {
	case model.KindImage:
		return size, "Standbild"
	case model.KindAudio:
		return model.Timecode(int(m.Duration.Seconds()*float64(fps)+0.5), fps), "Audio"
	}
	return model.Timecode(int(m.Duration.Seconds()*float64(fps)+0.5), fps), size
}

func (c *card) set(m model.MediaItem, fps int, thumb image.Image, selected, missing bool) {
	c.fullName = m.Name
	c.icon.Resource = kindIcon(m.Kind)
	c.info.Text, c.detail.Text = infoLine(m, fps)
	c.thumb.Image = thumb
	c.placeholder.Resource = theme.NewColoredResource(kindIcon(m.Kind), theme.ColorNameDisabled)
	c.placeholder.Hidden = thumb != nil
	c.thumb.Translucency = 0
	if missing {
		c.thumb.Translucency = 0.6
	}
	c.badge.Hidden, c.badgeText.Hidden = !missing, !missing
	if selected {
		c.bg.FillColor = color.NRGBA{0x2E, 0x36, 0x46, 0xFF}
		c.frame.StrokeColor = studiotheme.Steel
		c.name.Color = color.White
	} else {
		c.bg.FillColor = studiotheme.Raised
		c.frame.StrokeColor = color.Transparent
		c.name.Color = studiotheme.Text
	}
	c.Refresh()
}

func (c *card) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

func (c *card) TappedSecondary(e *fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
	if c.onSecondary != nil {
		c.onSecondary(e.AbsolutePosition)
	}
}

func (c *card) MinSize() fyne.Size { return fyne.NewSize(cardW, cardH) }

func (c *card) CreateRenderer() fyne.WidgetRenderer {
	return &cardRenderer{c: c, objs: []fyne.CanvasObject{c.bg, c.well, c.placeholder, c.thumb, c.badge, c.badgeText,
		c.icon, c.name, c.info, c.detail, c.frame}}
}

type cardRenderer struct {
	c    *card
	objs []fyne.CanvasObject
}

func (r *cardRenderer) Layout(size fyne.Size) {
	c := r.c
	c.bg.Resize(size)
	c.frame.Resize(size)
	inner := size.Width - 2*cardPad
	c.well.Move(fyne.NewPos(cardPad, cardPad))
	c.well.Resize(fyne.NewSize(inner, thumbH))
	c.thumb.Move(c.well.Position())
	c.thumb.Resize(c.well.Size())

	bs := fyne.MeasureText(c.badgeText.Text, c.badgeText.TextSize, c.badgeText.TextStyle)
	bw, bh := bs.Width+10, bs.Height+2
	c.badge.Move(fyne.NewPos(cardPad+inner-bw-4, cardPad+4))
	c.badge.Resize(fyne.NewSize(bw, bh))
	c.badgeText.Move(c.badge.Position().AddXY(5, 1))
	c.badgeText.Resize(bs)

	c.placeholder.Resize(fyne.NewSize(28, 28))
	c.placeholder.Move(fyne.NewPos(cardPad+(inner-28)/2, cardPad+(thumbH-28)/2))

	mid := float32(cardPad + thumbH + 15) // centre line of the name row
	c.icon.Move(fyne.NewPos(textPad, mid-7))
	c.icon.Resize(fyne.NewSize(14, 14))
	nameX := float32(textPad + 19)
	c.name.Text = ellipsize(c.fullName, size.Width-nameX-textPad, c.name.TextSize, c.name.TextStyle)
	ns := fyne.MeasureText(c.name.Text, c.name.TextSize, c.name.TextStyle)
	c.name.Move(fyne.NewPos(nameX, mid-ns.Height/2))
	c.name.Resize(ns)

	mid += 19
	is := fyne.MeasureText(c.info.Text, c.info.TextSize, c.info.TextStyle)
	c.info.Move(fyne.NewPos(textPad, mid-is.Height/2))
	c.info.Resize(is)
	ds := fyne.MeasureText(c.detail.Text, c.detail.TextSize, c.detail.TextStyle)
	c.detail.Move(fyne.NewPos(size.Width-textPad-ds.Width, mid-ds.Height/2))
	c.detail.Resize(ds)
}

func (r *cardRenderer) MinSize() fyne.Size           { return r.c.MinSize() }
func (r *cardRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *cardRenderer) Destroy()                     {}

func (r *cardRenderer) Refresh() {
	r.Layout(r.c.Size())
	for _, o := range r.objs {
		o.Refresh()
	}
}

// ellipsize shortens s with a trailing "…" so it fits into width.
func ellipsize(s string, width, size float32, style fyne.TextStyle) string {
	if fyne.MeasureText(s, size, style).Width <= width {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(string(r[:mid])+"…", size, style).Width <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(r[:lo]) + "…"
}
