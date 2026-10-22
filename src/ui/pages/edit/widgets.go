package edit

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// labelWidth aligns the controls of every inspector section.
const labelWidth = 96

// commitEntry applies its text on Enter or when it loses focus; Escape reverts.
type commitEntry struct {
	widget.Entry
	OnCommit func(string)
	shown    string // last text set from the model
}

func newCommitEntry(mono bool, commit func(string)) *commitEntry {
	e := &commitEntry{OnCommit: commit}
	e.TextStyle.Monospace = mono
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { e.commit() }
	return e
}

// set shows a model value unless the user is typing in the entry.
func (e *commitEntry) set(s string) {
	if e.focused() {
		return
	}
	e.shown = s
	if e.Text != s {
		e.SetText(s)
	}
}

// force shows s even while focused, after the entry's own value was applied.
func (e *commitEntry) force(s string) {
	e.shown = s
	e.SetText(s)
}

func (e *commitEntry) focused() bool {
	c := fyne.CurrentApp().Driver().CanvasForObject(e)
	return c != nil && c.Focused() == e
}

func (e *commitEntry) commit() {
	if e.Text != e.shown && e.OnCommit != nil {
		e.OnCommit(e.Text)
	}
}

func (e *commitEntry) FocusLost() {
	e.Entry.FocusLost()
	e.commit()
}

func (e *commitEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.SetText(e.shown)
		return
	}
	e.Entry.TypedKey(k)
}

// row is a tappable list row with selection highlight and double-tap action.
type row struct {
	widget.BaseWidget
	bg       *canvas.Rectangle
	content  fyne.CanvasObject
	OnTap    func()
	OnDouble func()
}

func newRow(content fyne.CanvasObject, tap, double func()) *row {
	r := &row{bg: canvas.NewRectangle(color.Transparent), content: content, OnTap: tap, OnDouble: double}
	r.bg.CornerRadius = 3
	r.ExtendBaseWidget(r)
	return r
}

func (r *row) SetSelected(on bool) {
	c := color.Color(color.Transparent)
	if on {
		c = alpha(studiotheme.Steel, 0x40)
	}
	if r.bg.FillColor != c {
		r.bg.FillColor = c
		r.bg.Refresh()
	}
}

func (r *row) Tapped(*fyne.PointEvent) {
	if r.OnTap != nil {
		r.OnTap()
	}
}

func (r *row) DoubleTapped(*fyne.PointEvent) {
	if r.OnDouble != nil {
		r.OnDouble()
	}
}

func (r *row) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.bg,
		container.New(layout.NewCustomPaddedLayout(4, 4, 6, 6), r.content)))
}

func alpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func text(s string, c color.Color, size float32) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	return t
}

func vcenter(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
}

func fixed(w, h float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

// field is an inspector row: muted label column, control filling the rest.
func field(label string, control fyne.CanvasObject) fyne.CanvasObject {
	l := container.NewStack(fixed(labelWidth, 0), vcenter(text(label, studiotheme.Muted, 12)))
	return container.NewBorder(nil, nil, l, nil, control)
}

// value is a read-only field value, inset to line up with entry text.
func value(o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, 7, 0), vcenter(o))
}

// section groups fields under a small heading with a hairline above it.
func section(title string, rows ...fyne.CanvasObject) fyne.CanvasObject {
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	return container.NewVBox(append([]fyne.CanvasObject{line, sectionHead(title)}, rows...)...)
}

// firstSection is a section directly under the panel header, which already draws a line.
func firstSection(title string, rows ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(append([]fyne.CanvasObject{sectionHead(title)}, rows...)...)
}

func sectionHead(title string) fyne.CanvasObject {
	head := text(title, studiotheme.Text, 12)
	head.TextStyle.Bold = true
	return container.New(layout.NewCustomPaddedLayout(6, 2, 0, 0), head)
}

// readout is the fixed-width mono value next to a slider.
func readout() *canvas.Text {
	t := kit.MonoText("", studiotheme.Text, 12)
	t.Alignment = fyne.TextAlignTrailing
	return t
}

func sliderField(label string, s *widget.Slider, value *canvas.Text) fyne.CanvasObject {
	v := container.NewStack(fixed(58, 0), vcenter(value))
	return field(label, container.NewBorder(nil, nil, nil, v, s))
}

// swatches is a row of colour chips with one selected.
type swatches struct {
	box    *fyne.Container
	chips  []*kit.Swatch
	colors []model.RGBA
}

func newSwatches(colors []model.RGBA, pick func(model.RGBA)) *swatches {
	sw := &swatches{box: container.NewHBox(), colors: colors}
	for _, c := range colors {
		chip := kit.NewSwatch(c.NRGBA(), func() { pick(c) })
		// hairline so dark chips stay visible on the graphite panel
		edge := canvas.NewRectangle(color.Transparent)
		edge.StrokeColor = studiotheme.Border
		edge.StrokeWidth = 1
		edge.CornerRadius = 3
		sw.chips = append(sw.chips, chip)
		sw.box.Add(container.NewGridWrap(fyne.NewSize(26, 26), container.NewStack(
			chip, container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), edge))))
	}
	return sw
}

func (sw *swatches) set(c model.RGBA) {
	for i, chip := range sw.chips {
		chip.SetSelected(sw.colors[i] == c)
	}
}

// note is an inline message under a field, e.g. a rejected timecode.
type note struct {
	box   *fyne.Container
	label *widget.Label
}

func newNote() *note {
	n := &note{label: widget.NewLabel("")}
	n.label.Wrapping = fyne.TextWrapWord
	n.label.SizeName = theme.SizeNameCaptionText
	icon := widget.NewIcon(theme.WarningIcon())
	n.box = container.NewBorder(nil, nil, container.NewStack(fixed(labelWidth, 0),
		container.NewHBox(layout.NewSpacer(), container.NewCenter(icon))), nil, n.label)
	n.box.Hide()
	return n
}

func (n *note) show(err error) {
	if err == nil {
		n.box.Hide()
		return
	}
	n.label.SetText(err.Error() + ".")
	n.box.Show()
}

func rgba(c color.NRGBA) model.RGBA { return model.FromNRGBA(c) }

func newSelect(options []string, changed func(string)) *widget.Select {
	s := widget.NewSelect(options, changed)
	s.PlaceHolder = "Auswählen"
	return s
}
