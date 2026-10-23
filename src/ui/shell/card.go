package shell

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// cardSize is the grid cell of a project card in the manager.
var cardSize = fyne.NewSize(232, 188)

// projectCard shows one library project: thumbnail in the project's aspect ratio with
// duration, then name, date and frame rate. Tap selects, double tap opens.
type projectCard struct {
	widget.BaseWidget
	d        store.Details
	open     bool
	thumb    *canvas.Image
	bg, ring *canvas.Rectangle
	name     *canvas.Text
	selected bool
	hovered  bool
	onSelect func()
	onOpen   func()
	onMenu   func(at fyne.Position)
}

func newProjectCard(d store.Details, open bool) *projectCard {
	c := &projectCard{d: d, open: open,
		thumb: canvas.NewImageFromImage(nil),
		bg:    canvas.NewRectangle(color.Transparent),
		ring:  canvas.NewRectangle(color.Transparent),
		name:  text(ellipsize(d.Name, 30), studiotheme.Text, 13, true),
	}
	c.thumb.FillMode = canvas.ImageFillContain
	c.thumb.ScaleMode = canvas.ImageScaleSmooth
	c.bg.CornerRadius = 4
	c.ring.CornerRadius = 3
	c.ring.StrokeWidth = 2
	c.ExtendBaseWidget(c)
	return c
}

func (c *projectCard) setSelected(on bool) {
	c.selected = on
	c.restyle()
}

func (c *projectCard) restyle() {
	switch {
	case c.selected:
		c.bg.FillColor = color.NRGBA{studiotheme.Steel.R, studiotheme.Steel.G, studiotheme.Steel.B, 0x26}
		c.ring.StrokeColor = studiotheme.Steel
	case c.hovered:
		c.bg.FillColor = hoverTint
		c.ring.StrokeColor = studiotheme.Faint
	default:
		c.bg.FillColor = color.Transparent
		c.ring.StrokeColor = color.Transparent
	}
	c.bg.Refresh()
	c.ring.Refresh()
}

func (c *projectCard) CreateRenderer() fyne.WidgetRenderer {
	d := c.d
	ratio := float32(16) / 9
	if d.Width > 0 && d.Height > 0 {
		ratio = float32(d.Width) / float32(d.Height)
	}
	plate := canvas.NewRectangle(studiotheme.Lane)
	plate.CornerRadius = 3
	frameBG := canvas.NewRectangle(studiotheme.MonitorOut)
	frameEdge := canvas.NewRectangle(color.Transparent)
	frameEdge.StrokeColor = studiotheme.Border
	frameEdge.StrokeWidth = 1
	res := kit.MonoText(fmt.Sprintf("%d×%d", d.Width, d.Height), studiotheme.Faint, 10)
	frame := container.New(kit.AspectLayout{Ratio: ratio},
		container.NewStack(frameBG, container.NewCenter(res), c.thumb, frameEdge))
	inset := container.New(layout.NewCustomPaddedLayout(8, 8, 8, 8), frame)

	dur := kit.MonoText(d.Timecode(d.Duration), studiotheme.Text, 10)
	durBG := canvas.NewRectangle(color.NRGBA{0, 0, 0, 0xB0})
	durBG.CornerRadius = 2
	durChip := container.NewStack(durBG, container.New(layout.NewCustomPaddedLayout(1, 1, 5, 5), dur))
	corner := container.NewVBox(layout.NewSpacer(), container.NewHBox(layout.NewSpacer(), durChip, strut(4, 0)), strut(0, 4))
	overlays := []fyne.CanvasObject{plate, inset, corner}
	if c.open {
		badgeBG := canvas.NewRectangle(studiotheme.Steel)
		badgeBG.CornerRadius = 2
		badge := container.NewStack(badgeBG, container.New(layout.NewCustomPaddedLayout(1, 1, 5, 5),
			text("Geöffnet", color.White, 10, true)))
		overlays = append(overlays, container.NewVBox(strut(0, 4), container.NewHBox(strut(4, 0), badge)))
	}
	overlays = append(overlays, c.ring)
	thumbBox := container.NewStack(overlays...)
	thumbArea := container.NewGridWrap(fyne.NewSize(cardSize.Width-12, (cardSize.Width-12)*9/16), thumbBox)

	date := text(d.Modified.Format("02.01.2006 · 15:04"), studiotheme.Muted, 11, false)
	fps := text(d.FPS.Label(), studiotheme.Muted, 11, false)
	meta := container.NewBorder(nil, nil, date, fps)
	info := container.New(layout.NewCustomPaddedLayout(0, 0, 2, 2),
		container.NewVBox(strut(0, 2), c.name, meta))
	body := container.New(layout.NewCustomPaddedLayout(6, 6, 6, 6), container.NewVBox(thumbArea, info))
	c.restyle()
	return widget.NewSimpleRenderer(container.NewStack(c.bg, body))
}

func (c *projectCard) Tapped(*fyne.PointEvent) { c.onSelect() }
func (c *projectCard) DoubleTapped(*fyne.PointEvent) {
	c.onSelect()
	c.onOpen()
}
func (c *projectCard) TappedSecondary(e *fyne.PointEvent) {
	c.onSelect()
	c.onMenu(e.AbsolutePosition)
}
func (c *projectCard) MouseMoved(*desktop.MouseEvent) {}
func (c *projectCard) MouseIn(*desktop.MouseEvent)    { c.hovered = true; c.restyle() }
func (c *projectCard) MouseOut()                      { c.hovered = false; c.restyle() }

// tapRow is a full-width list row with hover highlight, used for recent projects.
type tapRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	bg      *canvas.Rectangle
	onTap   func()
}

func newTapRow(content fyne.CanvasObject, tap func()) *tapRow {
	r := &tapRow{content: content, bg: canvas.NewRectangle(color.Transparent), onTap: tap}
	r.bg.CornerRadius = 3
	r.ExtendBaseWidget(r)
	return r
}

func (r *tapRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.bg,
		container.New(layout.NewCustomPaddedLayout(5, 5, 8, 8), r.content)))
}

func (r *tapRow) Tapped(*fyne.PointEvent)        { r.onTap() }
func (r *tapRow) Cursor() desktop.Cursor         { return desktop.PointerCursor }
func (r *tapRow) MouseMoved(*desktop.MouseEvent) {}
func (r *tapRow) MouseIn(*desktop.MouseEvent) {
	r.bg.FillColor = hoverTint
	r.bg.Refresh()
}
func (r *tapRow) MouseOut() {
	r.bg.FillColor = color.Transparent
	r.bg.Refresh()
}
