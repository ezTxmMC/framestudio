package keymapui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

// recorder captures the next key combination. It reads modifiers through KeyDown/KeyUp and
// applies the combination when its main key is released, so the shortcut event that
// follows the key press still reaches (and is swallowed by) the recorder instead of
// running a command. Esc cancels, Backspace removes the shortcut.
type recorder struct {
	widget.BaseWidget
	mods     fyne.KeyModifier
	key      fyne.KeyName // captured main key, applied on its release
	done     bool
	focused  bool
	onDone   func(sc string)
	onCancel func()
	onClear  func()

	bg   *canvas.Rectangle
	text *canvas.Text
	hint *canvas.Text
}

func newRecorder(done func(string), cancel, clear func()) *recorder {
	r := &recorder{onDone: done, onCancel: cancel, onClear: clear}
	r.ExtendBaseWidget(r)
	return r
}

var modKeys = map[fyne.KeyName]fyne.KeyModifier{
	desktop.KeyShiftLeft: fyne.KeyModifierShift, desktop.KeyShiftRight: fyne.KeyModifierShift,
	desktop.KeyControlLeft: fyne.KeyModifierControl, desktop.KeyControlRight: fyne.KeyModifierControl,
	desktop.KeyAltLeft: fyne.KeyModifierAlt, desktop.KeyAltRight: fyne.KeyModifierAlt,
	desktop.KeySuperLeft: fyne.KeyModifierSuper, desktop.KeySuperRight: fyne.KeyModifierSuper,
}

func (r *recorder) KeyDown(ev *fyne.KeyEvent) {
	if r.done {
		return
	}
	if m, ok := modKeys[ev.Name]; ok {
		r.mods |= m
		r.Refresh()
		return
	}
	if r.mods == 0 {
		switch ev.Name {
		case fyne.KeyEscape:
			r.finish(r.onCancel)
			return
		case fyne.KeyBackspace:
			r.finish(r.onClear)
			return
		}
	}
	if ev.Name == "" || ev.Name == fyne.KeyUnknown {
		return
	}
	r.key = ev.Name
	r.Refresh()
}

func (r *recorder) KeyUp(ev *fyne.KeyEvent) {
	if r.done {
		return
	}
	if m, ok := modKeys[ev.Name]; ok {
		if r.key == "" {
			r.mods &^= m
			r.Refresh()
		}
		return
	}
	if r.key != "" && ev.Name == r.key {
		sc := command.Format(r.key, r.mods)
		r.finish(func() { r.onDone(sc) })
	}
}

func (r *recorder) finish(fn func()) {
	r.done = true
	if fn != nil {
		fn()
	}
}

func (r *recorder) FocusGained() {
	r.focused = true
	r.Refresh()
}

// FocusLost (a click elsewhere) applies a pressed combination or cancels.
func (r *recorder) FocusLost() {
	r.focused = false
	if r.done {
		return
	}
	if r.key != "" {
		sc := command.Format(r.key, r.mods)
		r.finish(func() { r.onDone(sc) })
		return
	}
	r.finish(r.onCancel)
}

func (r *recorder) TypedRune(rune)                 {}
func (r *recorder) TypedKey(*fyne.KeyEvent)        {}
func (r *recorder) TypedShortcut(fyne.Shortcut)    {}
func (r *recorder) AcceptsTab() bool               { return true }
func (r *recorder) Tapped(*fyne.PointEvent)        {}
func (r *recorder) MinSize() fyne.Size             { return fyne.NewSize(178, 22) }
func (r *recorder) Cursor() desktop.StandardCursor { return desktop.DefaultCursor }

func (r *recorder) preview() string {
	if r.key != "" {
		return command.Display(command.Format(r.key, r.mods))
	}
	if r.mods != 0 {
		d := command.Display(command.Format("X", r.mods))
		return d[:len(d)-1] + "…"
	}
	return "Tasten drücken…"
}

func (r *recorder) CreateRenderer() fyne.WidgetRenderer {
	r.bg = canvas.NewRectangle(color.NRGBA{0x5A, 0x8C, 0xEB, 0x22})
	r.bg.CornerRadius = 3
	r.bg.StrokeColor = studiotheme.Steel
	r.bg.StrokeWidth = 1
	r.text = canvas.NewText("", color.White)
	r.text.TextSize = 11.5
	r.text.TextStyle = fyne.TextStyle{Monospace: true}
	r.hint = canvas.NewText("Esc · Rück leert", studiotheme.Muted)
	r.hint.TextSize = 10
	return &recorderRenderer{r: r}
}

type recorderRenderer struct{ r *recorder }

func (rr *recorderRenderer) Layout(size fyne.Size) {
	r := rr.r
	r.bg.Resize(size)
	ts := r.text.MinSize()
	r.text.Move(fyne.NewPos(7, (size.Height-ts.Height)/2))
	r.text.Resize(ts)
	hs := r.hint.MinSize()
	r.hint.Move(fyne.NewPos(size.Width-hs.Width-7, (size.Height-hs.Height)/2))
	r.hint.Resize(hs)
}

func (rr *recorderRenderer) MinSize() fyne.Size { return rr.r.MinSize() }
func (rr *recorderRenderer) Refresh() {
	r := rr.r
	r.text.Text = r.preview()
	if r.key == "" && r.mods == 0 {
		r.text.TextStyle = fyne.TextStyle{}
		r.text.Color = studiotheme.Text
	} else {
		r.text.TextStyle = fyne.TextStyle{Monospace: true}
		r.text.Color = color.White
	}
	r.bg.StrokeWidth = 1
	if r.focused {
		r.bg.StrokeWidth = 1.5
	}
	r.hint.Hidden = r.key != "" || r.mods != 0
	rr.Layout(r.Size())
	canvas.Refresh(r)
}
func (rr *recorderRenderer) Destroy() {}
func (rr *recorderRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.r.bg, rr.r.text, rr.r.hint}
}

// flatButton is a compact text button for table rows.
type flatButton struct {
	widget.BaseWidget
	text    string
	primary bool
	hovered bool
	onTap   func()
}

func newFlatButton(text string, primary bool, tap func()) *flatButton {
	b := &flatButton{text: text, primary: primary, onTap: tap}
	b.ExtendBaseWidget(b)
	return b
}

func (b *flatButton) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (b *flatButton) MouseIn(*desktop.MouseEvent)    { b.hovered = true; b.Refresh() }
func (b *flatButton) MouseMoved(*desktop.MouseEvent) {}
func (b *flatButton) MouseOut()                      { b.hovered = false; b.Refresh() }
func (b *flatButton) Cursor() desktop.StandardCursor { return desktop.PointerCursor }

func (b *flatButton) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = 3
	t := canvas.NewText(b.text, studiotheme.Muted)
	t.TextSize = 11.5
	r := &flatRenderer{b: b, bg: bg, t: t}
	r.Refresh()
	return r
}

type flatRenderer struct {
	b  *flatButton
	bg *canvas.Rectangle
	t  *canvas.Text
}

func (r *flatRenderer) MinSize() fyne.Size {
	ts := r.t.MinSize()
	return fyne.NewSize(ts.Width+16, 22)
}

func (r *flatRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	ts := r.t.MinSize()
	r.t.Move(fyne.NewPos((size.Width-ts.Width)/2, (size.Height-ts.Height)/2))
	r.t.Resize(ts)
}

func (r *flatRenderer) Refresh() {
	b := r.b
	switch {
	case b.primary:
		r.bg.FillColor = studiotheme.Steel
		if b.hovered {
			r.bg.FillColor = color.NRGBA{0x6C, 0x9B, 0xF0, 0xFF}
		}
		r.t.Color = color.White
	case b.hovered:
		r.bg.FillColor = studiotheme.Raised
		r.t.Color = studiotheme.Text
	default:
		r.bg.FillColor = color.Transparent
		r.t.Color = studiotheme.Muted
	}
	r.Layout(b.Size())
	canvas.Refresh(b)
}

func (r *flatRenderer) Destroy() {}
func (r *flatRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.t}
}
