package settingsui

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// fixedWidth gives its children a fixed width and their natural height.
type fixedWidth struct{ w float32 }

func (l *fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

func (l *fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	for _, o := range objs {
		if o.Visible() {
			h = max(h, o.MinSize().Height)
		}
	}
	return fyne.NewSize(l.w, h)
}

// vstack stacks children at their min height with a small gap (tighter than VBox).
type vstack struct{ gap float32 }

func (l *vstack) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		o.Resize(fyne.NewSize(size.Width, h))
		o.Move(fyne.NewPos(0, y))
		y += h + l.gap
	}
}

func (l *vstack) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	n := 0
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		m := o.MinSize()
		w = max(w, m.Width)
		h += m.Height
		n++
	}
	if n > 1 {
		h += l.gap * float32(n-1)
	}
	return fyne.NewSize(w, h)
}

// vcenter centres its children vertically at their min height, full width.
type vcenter struct{}

func (vcenter) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		h := min(o.MinSize().Height, size.Height)
		o.Resize(fyne.NewSize(size.Width, h))
		o.Move(fyne.NewPos(0, (size.Height-h)/2))
	}
}

func (vcenter) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		s = s.Max(o.MinSize())
	}
	return s
}

// catItem is one entry of the category sidebar.
type catItem struct {
	widget.BaseWidget
	cat      *category
	onTap    func()
	selected bool
	dimmed   bool
	hits     int
	hovered  bool

	bg, bar *canvas.Rectangle
	icon    *widget.Icon
	text    *canvas.Text
	count   *canvas.Text
}

func newCatItem(c *category, tap func()) *catItem {
	it := &catItem{cat: c, onTap: tap}
	it.bg = canvas.NewRectangle(color.Transparent)
	it.bg.CornerRadius = 4
	it.bar = canvas.NewRectangle(color.Transparent)
	it.bar.CornerRadius = 1.5
	it.icon = widget.NewIcon(c.icon)
	it.text = canvas.NewText(c.title, studiotheme.Text)
	it.text.TextSize = 13
	it.count = canvas.NewText("", studiotheme.Muted)
	it.count.TextSize = 11
	it.ExtendBaseWidget(it)
	return it
}

func (it *catItem) setState(selected, dimmed bool, hits int) {
	it.selected, it.dimmed, it.hits = selected, dimmed, hits
	it.Refresh()
}

func (it *catItem) Tapped(*fyne.PointEvent) {
	if it.onTap != nil {
		it.onTap()
	}
}

func (it *catItem) MouseIn(*desktop.MouseEvent)    { it.hovered = true; it.Refresh() }
func (it *catItem) MouseMoved(*desktop.MouseEvent) {}
func (it *catItem) MouseOut()                      { it.hovered = false; it.Refresh() }

func (it *catItem) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (it *catItem) Refresh() {
	switch {
	case it.selected:
		it.bg.FillColor = studiotheme.Raised
		it.bar.FillColor = studiotheme.Steel
		it.text.Color = studiotheme.Text
		it.text.TextStyle = fyne.TextStyle{Bold: true}
	case it.hovered:
		it.bg.FillColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x0C}
		it.bar.FillColor = color.Transparent
		it.text.Color = studiotheme.Text
		it.text.TextStyle = fyne.TextStyle{}
	default:
		it.bg.FillColor = color.Transparent
		it.bar.FillColor = color.Transparent
		it.text.Color = studiotheme.Text
		it.text.TextStyle = fyne.TextStyle{}
	}
	if it.dimmed {
		it.text.Color = studiotheme.Faint
	}
	it.count.Text = ""
	if it.hits > 0 {
		it.count.Text = strconv.Itoa(it.hits)
		it.count.Color = studiotheme.Steel
	}
	if it.dimmed {
		it.icon.SetResource(theme.NewDisabledResource(it.cat.icon))
	} else {
		it.icon.SetResource(it.cat.icon)
	}
	it.bg.Refresh()
	it.bar.Refresh()
	it.text.Refresh()
	it.count.Refresh()
}

func (it *catItem) CreateRenderer() fyne.WidgetRenderer {
	bar := container.New(&fixedWidth{w: 3}, it.bar)
	row := container.New(layout.NewCustomPaddedLayout(0, 0, 10, 10),
		container.NewBorder(nil, nil, container.NewHBox(container.NewCenter(it.icon), layout.NewSpacer()), container.NewCenter(it.count),
			container.New(layout.NewCustomPaddedLayout(0, 0, 6, 0), container.New(vcenter{}, it.text))))
	barBox := container.New(layout.NewCustomPaddedLayout(8, 8, 0, 0), bar)
	return widget.NewSimpleRenderer(container.NewStack(it.bg, container.NewBorder(nil, nil, barBox, nil, row)))
}

func (it *catItem) MinSize() fyne.Size { return fyne.NewSize(sidebarW-12, 34) }

// badge is a small rounded label ("Wirkt nach Neustart").
func badge(text string, c color.NRGBA) fyne.CanvasObject {
	bg := canvas.NewRectangle(withAlpha(c, 0x22))
	bg.StrokeColor = withAlpha(c, 0x90)
	bg.StrokeWidth = 1
	bg.CornerRadius = 3
	t := canvas.NewText(text, c)
	t.TextSize = 10.5
	t.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewHBox(container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(1, 1, 6, 6), t)))
}
