package chrome

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// TitleText is the title of a window in its title bar (project name): one line of text
// that shortens itself in the middle ("Mein Pro…ekt") when its box is narrower than the
// text. In a Titlebar it is centred on the window width, not on the free space.
type TitleText struct {
	widget.BaseWidget
	text string
	size float32
	col  color.Color
	t    *canvas.Text
	// OnResize is called when the text changed, so a host can lay out again.
	OnResize func()
}

// NewTitleText creates a title of the given text size in the Muted colour.
func NewTitleText(size float32) *TitleText {
	t := &TitleText{size: size, col: studiotheme.Muted}
	t.ExtendBaseWidget(t)
	return t
}

// Text returns the full text.
func (t *TitleText) Text() string { return t.text }

// SetText sets the text and repaints.
func (t *TitleText) SetText(s string) {
	if s == t.text {
		return
	}
	t.text = s
	t.Refresh()
	if t.OnResize != nil {
		t.OnResize()
	}
}

// SetColor sets the text colour and repaints.
func (t *TitleText) SetColor(c color.Color) {
	t.col = c
	t.Refresh()
}

// SetSize sets the text size and repaints.
func (t *TitleText) SetSize(s float32) {
	t.size = s
	t.Refresh()
}

// MinSize is the size of the full text.
func (t *TitleText) MinSize() fyne.Size {
	return fyne.MeasureText(t.text, t.size, fyne.TextStyle{})
}

// Shown returns the text as painted at the current width.
func (t *TitleText) Shown() string { return middleEllipsis(t.text, t.size, t.Size().Width) }

// middleEllipsis shortens s in the middle until it fits width.
func middleEllipsis(s string, size, width float32) string {
	if width <= 0 || fyne.MeasureText(s, size, fyne.TextStyle{}).Width <= width {
		return s
	}
	r := []rune(s)
	for keep := len(r) - 1; keep >= 2; keep-- {
		head := (keep + 1) / 2
		cand := string(r[:head]) + "…" + string(r[len(r)-(keep-head):])
		if fyne.MeasureText(cand, size, fyne.TextStyle{}).Width <= width {
			return cand
		}
	}
	return "…"
}

func (t *TitleText) CreateRenderer() fyne.WidgetRenderer {
	r := &titleTextRenderer{w: t, txt: canvas.NewText(t.text, t.col)}
	return r
}

type titleTextRenderer struct {
	w   *TitleText
	txt *canvas.Text
}

func (r *titleTextRenderer) Layout(size fyne.Size) {
	r.apply()
	ts := r.txt.MinSize()
	r.txt.Resize(ts)
	r.txt.Move(fyne.NewPos((size.Width-ts.Width)/2, (size.Height-ts.Height)/2))
}

func (r *titleTextRenderer) apply() {
	r.txt.Text = middleEllipsis(r.w.text, r.w.size, r.w.Size().Width)
	r.txt.TextSize = r.w.size
	r.txt.Color = r.w.col
}

func (r *titleTextRenderer) MinSize() fyne.Size { return r.w.MinSize() }
func (r *titleTextRenderer) Refresh() {
	r.Layout(r.w.Size())
	r.txt.Refresh()
}
func (r *titleTextRenderer) Destroy()                     {}
func (r *titleTextRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.txt} }
