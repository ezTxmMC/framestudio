// Package kit holds small widgets and helpers shared by all pages.
package kit

import (
	"image/color"

	studiotheme "framestudio-proto/src/ui/theme"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// Panel frames content like a docked NLE panel: title strip, optional tools on the right.
func Panel(title string, tools fyne.CanvasObject, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Panel)
	bg.CornerRadius = 4
	label := canvas.NewText(title, studiotheme.Muted)
	label.TextSize = 12
	if tools == nil {
		tools = layout.NewSpacer()
	}
	height := canvas.NewRectangle(color.Transparent)
	height.SetMinSize(fyne.NewSize(0, 38))
	header := container.NewStack(height, container.NewBorder(nil, nil,
		container.NewPadded(container.NewCenter(label)), container.NewCenter(tools)))
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	return container.NewStack(bg, container.NewBorder(
		container.NewVBox(header, line), nil, nil, nil,
		container.NewPadded(content)))
}

// ToolButton is a flat icon button for panel headers and toolbars.
func ToolButton(icon fyne.Resource, tap func()) *widget.Button {
	b := widget.NewButtonWithIcon("", icon, tap)
	b.Importance = widget.LowImportance
	return b
}

// MonoText is a canvas text in the timecode font.
func MonoText(s string, c color.Color, size float32) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Monospace: true}
	return t
}

// AspectLayout fits its children into the largest centred box of the given ratio.
type AspectLayout struct{ Ratio float32 }

func (a AspectLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w, h := size.Width, size.Width/a.Ratio
	if h > size.Height {
		h = size.Height
		w = h * a.Ratio
	}
	for _, o := range objs {
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos((size.Width-w)/2, (size.Height-h)/2))
	}
}

func (a AspectLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(160, 160/a.Ratio)
}

// Swatch is a tappable clip colour chip.
type Swatch struct {
	widget.BaseWidget
	fill, ring *canvas.Rectangle
	OnTapped   func()
}

func NewSwatch(c color.Color, tap func()) *Swatch {
	s := &Swatch{fill: canvas.NewRectangle(c), ring: canvas.NewRectangle(color.Transparent), OnTapped: tap}
	s.fill.CornerRadius = 3
	s.ring.CornerRadius = 5
	s.ring.StrokeWidth = 2
	s.ExtendBaseWidget(s)
	return s
}

func (s *Swatch) SetSelected(on bool) {
	if on {
		s.ring.StrokeColor = studiotheme.Text
	} else {
		s.ring.StrokeColor = color.Transparent
	}
	s.ring.Refresh()
}

func (s *Swatch) Tapped(*fyne.PointEvent) {
	if s.OnTapped != nil {
		s.OnTapped()
	}
}

func (s *Swatch) CreateRenderer() fyne.WidgetRenderer {
	inner := container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), s.fill)
	return widget.NewSimpleRenderer(container.NewStack(s.ring, inner))
}

func (s *Swatch) MinSize() fyne.Size { return fyne.NewSize(30, 30) }
