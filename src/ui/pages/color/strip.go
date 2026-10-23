package color

import (
	"fmt"
	"image"
	"image/color"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// stripClip is one entry of the clip strip.
type stripClip struct {
	Clip  model.Clip
	Track string // track name, e.g. "V2"
	Index int    // track index in the project
}

// visualClips lists every clip on the video tracks in time order; clips starting on
// the same frame are ordered bottom track first.
func visualClips(p *model.Project) []stripClip {
	var out []stripClip
	for _, ti := range p.TrackIndices(model.TrackVideo) {
		t := &p.Tracks[ti]
		for _, c := range t.Clips {
			if c.Kind.IsVisual() {
				out = append(out, stripClip{Clip: c, Track: t.Name, Index: ti})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Clip.Start != out[j].Clip.Start {
			return out[i].Clip.Start < out[j].Clip.Start
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// clipUnder returns the clip at frame f on the top-most visible video track, or nil.
func clipUnder(p *model.Project, f int) *model.Clip {
	idx := p.TrackIndices(model.TrackVideo)
	for i := len(idx) - 1; i >= 0; i-- {
		t := &p.Tracks[idx[i]]
		if t.Hidden {
			continue
		}
		if ci := t.ClipAt(f); ci >= 0 {
			return &t.Clips[ci]
		}
	}
	return nil
}

const (
	cardW  = 124
	thumbW = 112
	thumbH = 63
)

// clipCard is one tappable thumbnail card of the clip strip.
type clipCard struct {
	widget.BaseWidget
	// OnTapped receives the modifier keys held when the click started (Ctrl toggles,
	// Shift extends the selection).
	OnTapped func(mods fyne.KeyModifier)

	entry    stripClip
	selected bool
	primary  bool // the selected clip the grade panel shows
	hovered  bool
	mods     fyne.KeyModifier

	bg, frame  *canvas.Rectangle
	thumbBG    *canvas.Rectangle
	thumbFill  *canvas.Rectangle
	thumb      *canvas.Image
	glyph      *canvas.Text
	missing    *widget.Icon
	number     *canvas.Text
	track      *canvas.Text
	timecode   *canvas.Text
	gradeDot   *canvas.Circle
	labelColor *canvas.Rectangle
	content    fyne.CanvasObject
}

func newClipCard(tap func(fyne.KeyModifier)) *clipCard {
	c := &clipCard{OnTapped: tap}
	c.bg = canvas.NewRectangle(studiotheme.Raised)
	c.bg.CornerRadius = 4
	c.frame = canvas.NewRectangle(color.Transparent)
	c.frame.CornerRadius = 4
	c.frame.StrokeWidth = 2
	c.thumbBG = canvas.NewRectangle(studiotheme.Lane)
	c.thumbFill = canvas.NewRectangle(color.Transparent)
	c.thumb = canvas.NewImageFromImage(nil)
	c.thumb.FillMode = canvas.ImageFillCover
	c.thumb.ScaleMode = canvas.ImageScaleSmooth
	c.glyph = canvas.NewText("T", studiotheme.Text)
	c.glyph.TextSize = 30
	c.glyph.TextStyle = fyne.TextStyle{Bold: true}
	c.glyph.Alignment = fyne.TextAlignCenter
	c.missing = widget.NewIcon(theme.BrokenImageIcon())
	c.number = kit.MonoText("", studiotheme.Muted, 11)
	c.track = canvas.NewText("", studiotheme.Muted)
	c.track.TextSize = 11
	c.timecode = kit.MonoText("", studiotheme.Text, 11)
	c.gradeDot = canvas.NewCircle(studiotheme.Steel)
	c.labelColor = canvas.NewRectangle(color.Transparent)
	c.labelColor.SetMinSize(fyne.NewSize(thumbW, 2))

	thumbSize := canvas.NewRectangle(color.Transparent)
	thumbSize.SetMinSize(fyne.NewSize(thumbW, thumbH))
	thumb := container.NewStack(thumbSize, c.thumbBG, c.thumbFill, c.thumb,
		container.NewCenter(c.glyph), container.NewCenter(c.missing))
	thumbArea := container.NewBorder(nil, c.labelColor, nil, nil, thumb)

	dot := container.New(&fixedLayout{size: fyne.NewSize(7, 7)}, c.gradeDot)
	top := container.NewHBox(c.number, layout.NewSpacer(), container.NewCenter(dot), c.track)
	body := container.New(layout.NewCustomPaddedVBoxLayout(3), top, thumbArea, c.timecode)
	c.content = container.NewStack(c.bg, c.frame,
		container.New(layout.NewCustomPaddedLayout(5, 6, 6, 6), body))
	c.ExtendBaseWidget(c)
	return c
}

func (c *clipCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *clipCard) MinSize() fyne.Size {
	return fyne.NewSize(cardW, c.content.MinSize().Height)
}

func (c *clipCard) Tapped(*fyne.PointEvent) {
	mods := c.mods
	c.mods = 0
	if c.OnTapped != nil {
		c.OnTapped(mods)
	}
}

// MouseDown remembers the modifiers of the click; Tapped follows on release.
func (c *clipCard) MouseDown(e *desktop.MouseEvent) { c.mods = e.Modifier }
func (c *clipCard) MouseUp(*desktop.MouseEvent)     {}

func (c *clipCard) MouseIn(*desktop.MouseEvent)    { c.hovered = true; c.applyState() }
func (c *clipCard) MouseMoved(*desktop.MouseEvent) {}
func (c *clipCard) MouseOut()                      { c.hovered = false; c.applyState() }

// set shows entry e at strip position n (1-based); thumb is the media poster if loaded,
// mediaMissing marks a clip whose media item is gone or failed to decode.
func (c *clipCard) set(n int, e stripClip, pr *model.Project, thumb image.Image, mediaMissing bool) {
	c.entry = e
	cl := e.Clip
	c.number.Text = fmt.Sprintf("%02d", n)
	c.track.Text = e.Track
	c.timecode.Text = pr.Timecode(cl.Start)
	c.labelColor.FillColor = cl.Color.NRGBA()
	if cl.Grade.IsNeutral() {
		c.gradeDot.FillColor = color.Transparent
	} else {
		c.gradeDot.FillColor = studiotheme.Steel
	}

	c.thumbFill.FillColor = color.Transparent
	c.thumb.Image = nil
	c.thumb.Hide()
	c.glyph.Hide()
	c.missing.Hide()
	switch cl.Kind {
	case model.KindColor:
		c.thumbFill.FillColor = cl.Color.NRGBA()
	case model.KindTitle:
		if cl.Title.Background.A > 0 {
			c.thumbFill.FillColor = cl.Title.Background.NRGBA()
		}
		c.glyph.Color = cl.Title.Color.NRGBA()
		if cl.Title.Color.A == 0 {
			c.glyph.Color = studiotheme.Text
		}
		c.glyph.Show()
	default:
		switch {
		case thumb != nil:
			c.thumb.Image = thumb
			c.thumb.Show()
		case mediaMissing:
			c.missing.Show()
		}
	}
	c.applyState()
	c.Refresh()
}

// setSelected marks the card as part of the selection; primary is the clip being graded.
func (c *clipCard) setSelected(on, primary bool) {
	primary = on && primary
	if c.selected != on || c.primary != primary {
		c.selected, c.primary = on, primary
		c.applyState()
	}
}

func (c *clipCard) applyState() {
	c.frame.StrokeWidth = 2
	switch {
	case c.primary:
		c.frame.StrokeColor = studiotheme.Steel
		c.bg.FillColor = color.NRGBA{0x2F, 0x37, 0x46, 0xFF}
		c.number.Color = studiotheme.Text
	case c.selected: // also selected, not the one in the grade panel
		c.frame.StrokeColor = color.NRGBA{studiotheme.Steel.R, studiotheme.Steel.G, studiotheme.Steel.B, 0x90}
		c.frame.StrokeWidth = 1
		c.bg.FillColor = color.NRGBA{0x2A, 0x30, 0x3B, 0xFF}
		c.number.Color = studiotheme.Text
	case c.hovered:
		c.frame.StrokeColor = studiotheme.Faint
		c.bg.FillColor = studiotheme.Raised
		c.number.Color = studiotheme.Muted
	default:
		c.frame.StrokeColor = color.Transparent
		c.bg.FillColor = studiotheme.Raised
		c.number.Color = studiotheme.Muted
	}
	c.frame.Refresh()
	c.bg.Refresh()
	c.number.Refresh()
}

// fixedLayout gives its objects a fixed size, used for small glyphs like the grade dot.
type fixedLayout struct{ size fyne.Size }

func (f *fixedLayout) Layout(objs []fyne.CanvasObject, _ fyne.Size) {
	for _, o := range objs {
		o.Resize(f.size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (f *fixedLayout) MinSize([]fyne.CanvasObject) fyne.Size { return f.size }
