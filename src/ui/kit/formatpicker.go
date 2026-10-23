package kit

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Labeled puts a small muted caption above a control, the form row of the pickers.
func Labeled(label string, control fyne.CanvasObject) fyne.CanvasObject {
	t := canvas.NewText(label, studiotheme.Muted)
	t.TextSize = 11
	return container.New(layout.NewCustomPaddedVBoxLayout(2), t, control)
}

// hint is a wrapping caption-size label.
func newHint(imp widget.Importance) *widget.Label {
	l := widget.NewLabel("")
	l.Wrapping = fyne.TextWrapWord
	l.Importance = imp
	l.SizeName = theme.SizeNameCaptionText
	return l
}

func setHint(l *widget.Label, text string) {
	if l.Text == text {
		return
	}
	l.SetText(text)
	if text == "" {
		l.Hide()
	} else {
		l.Show()
	}
}

// ---- resolution ----

// ResolutionPicker chooses a width and height: presets, free numeric entries with
// validation, aspect-ratio lock with ratio presets, swap, and a readout.
type ResolutionPicker struct {
	// Even adds the hint that odd sizes are rounded down to even ones at export.
	Even bool
	// Relayout runs after hints appeared or vanished, so the form around the picker can
	// lay itself out again.
	Relayout func()

	preset        *Select
	presetBox     fyne.CanvasObject
	Width, Height *widget.Entry
	lock          *Check
	ratioSel      *Select
	swap          *widget.Button
	readout       *canvas.Text
	note          *widget.Label
	box           *fyne.Container

	ratio    float64 // locked ratio w/h; 0 = unlocked
	updating bool
	onChange func()
}

// NewResolutionPicker returns a picker holding w × h; changed runs after every edit
// (read the result with Value).
func NewResolutionPicker(w, h int, changed func()) *ResolutionPicker {
	p := &ResolutionPicker{onChange: changed}
	labels := make([]string, 0, len(ResPresets)+1)
	for _, r := range ResPresets {
		labels = append(labels, r.Group+" · "+r.Label())
	}
	labels = append(labels, ResCustom)
	p.preset = NewSelect(labels, p.presetChosen)
	p.preset.PlaceHolder = "Auswählen"

	p.Width, p.Height = widget.NewEntry(), widget.NewEntry()
	p.Width.Validator = func(s string) error { _, err := ParseDimension(s, "Breite"); return err }
	p.Height.Validator = func(s string) error { _, err := ParseDimension(s, "Höhe"); return err }
	p.Width.OnChanged = func(string) { p.sizeTyped(true) }
	p.Height.OnChanged = func(string) { p.sizeTyped(false) }
	p.swap = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), p.swapSides)
	p.swap.Importance = widget.LowImportance

	p.lock = NewCheck("Sperren", p.lockToggled)
	ratios := []string{"Aktuell"}
	for _, r := range Ratios {
		ratios = append(ratios, r.Name)
	}
	p.ratioSel = NewSelect(ratios, p.ratioChosen)
	p.ratioSel.PlaceHolder = "Verhältnis"

	p.readout = MonoText("", studiotheme.Muted, 11)
	p.note = newHint(widget.LowImportance)
	p.note.Hide()

	times := canvas.NewText("×", studiotheme.Muted)
	times.TextSize = 13
	sizeRow := container.NewBorder(nil, nil, nil, p.swap, container.NewGridWithColumns(2,
		p.Width, container.NewBorder(nil, nil, container.NewPadded(container.NewCenter(times)), nil, p.Height)))
	p.presetBox = Labeled("Auflösung", p.preset)
	p.box = container.New(layout.NewCustomPaddedVBoxLayout(6),
		p.presetBox,
		Labeled("Breite × Höhe in Pixeln", sizeRow),
		Labeled("Seitenverhältnis", container.NewBorder(nil, nil, p.lock, nil, p.ratioSel)),
		p.readout,
		p.note,
	)
	p.Set(w, h)
	return p
}

// HidePreset hides the preset chooser, for forms that have their own list of sizes.
func (p *ResolutionPicker) HidePreset() { p.presetBox.Hide() }

// Object is the widget tree to place in a form.
func (p *ResolutionPicker) Object() fyne.CanvasObject { return p.box }

// Set shows w × h without calling the change callback.
func (p *ResolutionPicker) Set(w, h int) {
	p.updating = true
	p.Width.SetText(strconv.Itoa(w))
	p.Height.SetText(strconv.Itoa(h))
	if p.ratio > 0 && h > 0 {
		p.ratio = float64(w) / float64(h)
	}
	p.updating = false
	p.sync()
}

// Value is the entered size, or why it is not valid.
func (p *ResolutionPicker) Value() (w, h int, err error) {
	if w, err = ParseDimension(p.Width.Text, "Breite"); err != nil {
		return 0, 0, err
	}
	if h, err = ParseDimension(p.Height.Text, "Höhe"); err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

// Locked reports whether the aspect ratio is locked.
func (p *ResolutionPicker) Locked() bool { return p.ratio > 0 }

// SetLocked locks (to the current ratio) or unlocks the aspect ratio.
func (p *ResolutionPicker) SetLocked(on bool) { p.lock.SetChecked(on) }

// SetDisabled enables or disables all controls.
func (p *ResolutionPicker) SetDisabled(off bool) {
	for _, d := range []interface {
		Enable()
		Disable()
	}{p.preset, p.Width, p.Height, p.lock, p.ratioSel, p.swap} {
		if off {
			d.Disable()
		} else {
			d.Enable()
		}
	}
}

// Swap exchanges width and height (orientation).
func (p *ResolutionPicker) Swap() { p.swapSides() }

func (p *ResolutionPicker) presetChosen(label string) {
	if p.updating {
		return
	}
	if label == ResCustom {
		if c := fyne.CurrentApp().Driver().CanvasForObject(p.Width); c != nil {
			c.Focus(p.Width)
		}
		return
	}
	for i, r := range ResPresets {
		if r.Group+" · "+r.Label() == label {
			_ = i
			p.updating = true
			p.Width.SetText(strconv.Itoa(r.W))
			p.Height.SetText(strconv.Itoa(r.H))
			if p.ratio > 0 {
				p.ratio = float64(r.W) / float64(r.H)
			}
			p.updating = false
			p.sync()
			p.changed()
			return
		}
	}
}

func (p *ResolutionPicker) sizeTyped(width bool) {
	if p.updating {
		return
	}
	if p.ratio > 0 {
		src, other := p.Width, p.Height
		if !width {
			src, other = p.Height, p.Width
		}
		name := "Breite"
		if !width {
			name = "Höhe"
		}
		if v, err := ParseDimension(src.Text, name); err == nil {
			p.updating = true
			other.SetText(strconv.Itoa(OtherSide(v, p.ratio, width)))
			p.updating = false
		}
	}
	p.sync()
	p.changed()
}

func (p *ResolutionPicker) swapSides() {
	w, h := p.Width.Text, p.Height.Text
	p.updating = true
	p.Width.SetText(h)
	p.Height.SetText(w)
	if p.ratio > 0 {
		p.ratio = 1 / p.ratio
	}
	p.updating = false
	p.sync()
	p.changed()
}

func (p *ResolutionPicker) lockToggled(on bool) {
	if p.updating {
		return
	}
	p.ratio = 0
	if on {
		if w, h, err := p.Value(); err == nil {
			p.ratio = float64(w) / float64(h)
		} else {
			p.updating = true
			p.lock.SetChecked(false)
			p.updating = false
		}
	}
	p.sync()
}

func (p *ResolutionPicker) ratioChosen(name string) {
	if p.updating {
		return
	}
	if name == "Aktuell" {
		p.lock.SetChecked(true)
		return
	}
	for _, r := range Ratios {
		if r.Name != name {
			continue
		}
		p.ratio = r.W / r.H
		p.updating = true
		p.lock.SetChecked(true)
		if w, err := ParseDimension(p.Width.Text, "Breite"); err == nil {
			p.Height.SetText(strconv.Itoa(OtherSide(w, p.ratio, true)))
		}
		p.updating = false
		p.sync()
		p.changed()
		return
	}
}

// sync brings preset select, lock, readout and notes in line with the entries.
func (p *ResolutionPicker) sync() {
	was := p.updating
	p.updating = true
	defer func() { p.updating = was }()
	w, h, err := p.Value()
	sel := ResCustom
	if err == nil {
		for _, r := range ResPresets {
			if r.W == w && r.H == h {
				sel = r.Group + " · " + r.Label()
			}
		}
	}
	p.preset.SetSelected(sel)
	p.lock.SetChecked(p.ratio > 0)
	if p.ratio > 0 {
		cur := "Aktuell"
		for _, r := range Ratios {
			if r.W/r.H == p.ratio || (w > 0 && h > 0 && AspectText(w, h) == r.Name) {
				cur = r.Name
			}
		}
		p.ratioSel.SetSelected(cur)
	} else {
		p.ratioSel.ClearSelected()
	}
	switch {
	case err != nil:
		p.readout.Text = ""
		setHint(p.note, err.Error())
		p.note.Importance = widget.WarningImportance
	default:
		p.readout.Text = SizeReadout(w, h)
		if p.Even && (w%2 != 0 || h%2 != 0) {
			ew, eh := model.EvenSize(w, h)
			setHint(p.note, fmt.Sprintf("Ungerade Größen runden Videoformate beim Export auf gerade Werte ab (%d × %d).", ew, eh))
		} else {
			setHint(p.note, "")
		}
		p.note.Importance = widget.LowImportance
	}
	p.note.Refresh()
	p.readout.Refresh()
	if p.box != nil {
		p.box.Refresh()
		if p.Relayout != nil {
			p.Relayout()
		}
	}
}

func (p *ResolutionPicker) changed() {
	if p.onChange != nil && !p.updating {
		p.onChange()
	}
}

// ---- frame rate ----

// RateOptions configure a RatePicker.
type RateOptions struct {
	// SameLabel adds a first entry such as "Wie Projekt (25 fps)" meaning "no rate of its
	// own"; empty offers presets and custom only.
	SameLabel string
	// DropFrame adds the drop-frame check (enabled only for 29,97 and 59,94 fps).
	DropFrame bool
}

// RatePicker chooses a frame rate: presets plus a typed rate (decimals or fractions).
type RatePicker struct {
	// Relayout runs after hints appeared or vanished, so the form around the picker can
	// lay itself out again.
	Relayout func()

	opts    RateOptions
	details *fyne.Container
	sel     *Select
	custom  *widget.Entry
	drop    *Check
	hint    *widget.Label
	box     *fyne.Container

	same     bool
	updating bool
	wantDrop bool // the user's drop-frame choice, kept while the rate cannot drop frames
	onChange func()
}

// NewRatePicker returns a picker showing rate r (a zero rate selects SameLabel when set).
func NewRatePicker(r model.Rate, opts RateOptions, changed func()) *RatePicker {
	p := &RatePicker{opts: opts, onChange: changed}
	labels := RatePresetLabels()
	if opts.SameLabel != "" {
		labels = append([]string{opts.SameLabel}, labels...)
	}
	labels = append(labels, RateCustom)
	p.sel = NewSelect(labels, p.chosen)
	p.sel.PlaceHolder = "Auswählen"
	p.custom = widget.NewEntry()
	p.custom.PlaceHolder = "z. B. 23,976 oder 24000/1001"
	p.custom.Validator = func(s string) error { _, _, err := ParseRateText(s); return err }
	p.custom.OnChanged = func(string) { p.typed() }
	p.custom.Hide()
	p.drop = NewCheck("Drop-Frame-Timecode (nur 29,97 und 59,94 fps)", func(on bool) {
		if !p.updating {
			p.wantDrop = on
		}
		p.changed()
	})
	p.hint = newHint(widget.LowImportance)
	p.hint.Hide()
	objs := []fyne.CanvasObject{Labeled("Bildrate", p.sel), p.custom, p.hint}
	if opts.DropFrame {
		objs = append(objs, p.drop)
	}
	p.box = container.New(layout.NewCustomPaddedVBoxLayout(6), objs...)
	p.same = opts.SameLabel != "" && !r.Valid()
	p.Set(r, false)
	return p
}

// Object is the widget tree to place in a form.
func (p *RatePicker) Object() fyne.CanvasObject { return p.box }

// Select is the preset chooser alone, for forms that lay it out next to other controls;
// then Details (custom entry, hint, drop-frame check) goes below.
func (p *RatePicker) Select() *Select { return p.sel }

// CustomEntry is the text entry for a typed frame rate.
func (p *RatePicker) CustomEntry() *widget.Entry { return p.custom }

// Details is the custom rate entry with its hint and the drop-frame check.
func (p *RatePicker) Details() fyne.CanvasObject {
	objs := []fyne.CanvasObject{p.custom, p.hint}
	if p.opts.DropFrame {
		objs = append(objs, p.drop)
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(6), objs...)
}

// Set shows rate r without calling the change callback. A zero or invalid rate selects the
// "same" entry when there is one.
func (p *RatePicker) Set(r model.Rate, drop bool) {
	p.updating = true
	defer func() { p.updating = false }()
	p.same = p.opts.SameLabel != "" && !r.Valid()
	label := RateCustom
	if p.same {
		label = p.opts.SameLabel
	} else if r.Valid() {
		for i, pr := range model.RatePresets {
			if pr.Equal(r) {
				label = RatePresetLabels()[i]
			}
		}
	}
	p.sel.SetSelected(label)
	if label == RateCustom {
		if r.Valid() {
			p.custom.SetText(r.Fraction())
		} else {
			p.custom.SetText("")
		}
	} else {
		p.custom.SetText("")
	}
	p.wantDrop = drop
	p.drop.SetChecked(drop)
	p.sync()
}

// SetSameLabel replaces the text of the "same" entry (the project rate changed).
func (p *RatePicker) SetSameLabel(label string) {
	if p.opts.SameLabel == "" {
		return
	}
	was := p.same
	p.opts.SameLabel = label
	labels := append([]string{label}, RatePresetLabels()...)
	labels = append(labels, RateCustom)
	p.updating = true
	p.sel.SetOptions(labels)
	if was {
		p.sel.SetSelected(label)
	}
	p.updating = false
}

// Value is the chosen rate. same is true when the "same" entry is selected (the rate is
// then zero). drop is only true for rates that can drop frames.
func (p *RatePicker) Value() (r model.Rate, drop, same bool, err error) {
	if p.same {
		return model.Rate{}, false, true, nil
	}
	if p.sel.Selected == RateCustom {
		r, _, err = ParseRateText(p.custom.Text)
		if err != nil {
			return model.Rate{}, false, false, err
		}
	} else {
		for i, l := range RatePresetLabels() {
			if l == p.sel.Selected {
				r = model.RatePresets[i]
			}
		}
		if !r.Valid() {
			return model.Rate{}, false, false, fmt.Errorf("Bitte eine Bildrate wählen.")
		}
	}
	return r, p.drop.Checked && r.CanDropFrame(), false, nil
}

func (p *RatePicker) chosen(label string) {
	if p.updating {
		return
	}
	p.same = p.opts.SameLabel != "" && label == p.opts.SameLabel
	if label == RateCustom {
		if p.custom.Text == "" {
			p.custom.SetText("")
		}
		p.sync()
		if c := fyne.CurrentApp().Driver().CanvasForObject(p.custom); c != nil {
			c.Focus(p.custom)
		}
		p.changed()
		return
	}
	p.sync()
	p.changed()
}

func (p *RatePicker) typed() {
	if p.updating {
		return
	}
	p.sync()
	p.changed()
}

func (p *RatePicker) sync() {
	custom := p.sel.Selected == RateCustom
	if custom {
		p.custom.Show()
	} else {
		p.custom.Hide()
	}
	r, _, same, err := p.Value()
	switch {
	case same || (err != nil && !custom):
		setHint(p.hint, "")
		p.hint.Importance = widget.LowImportance
	case err != nil:
		setHint(p.hint, err.Error())
		p.hint.Importance = widget.WarningImportance
	default:
		_, note, _ := ParseRateText(p.custom.Text)
		text := ""
		if custom {
			text = RateDetail(r)
			if note != "" {
				text = note + " " + text
			}
		} else if r.Den != 1 {
			text = RateDetail(r)
		}
		setHint(p.hint, text)
		p.hint.Importance = widget.LowImportance
	}
	if p.opts.DropFrame {
		if err == nil && !same && r.CanDropFrame() {
			p.drop.Enable()
			was := p.updating
			p.updating = true
			p.drop.SetChecked(p.wantDrop)
			p.updating = was
		} else {
			was := p.updating
			p.updating = true
			p.drop.SetChecked(false)
			p.updating = was
			p.drop.Disable()
		}
	}
	p.hint.Refresh()
	if p.box != nil {
		p.box.Refresh()
	}
	if p.details != nil {
		p.details.Refresh()
	}
	if p.Relayout != nil {
		p.Relayout()
	}
}

// SetDisabled enables or disables the picker.
func (p *RatePicker) SetDisabled(off bool) {
	if off {
		p.sel.Disable()
		p.custom.Disable()
		p.drop.Disable()
	} else {
		p.sel.Enable()
		p.custom.Enable()
		p.sync()
	}
}

// SetDropFrame checks or unchecks drop-frame (ignored while the rate cannot drop frames).
func (p *RatePicker) SetDropFrame(on bool) {
	if on && p.drop.Disabled() {
		return
	}
	p.drop.SetChecked(on)
}

// DropEnabled reports whether the drop-frame check can be used for the current rate.
func (p *RatePicker) DropEnabled() bool { return !p.drop.Disabled() }

func (p *RatePicker) changed() {
	if p.onChange != nil && !p.updating {
		p.onChange()
	}
}

// ---- both ----

// FormatOptions configure a FormatPicker.
type FormatOptions struct {
	// MatchMedia, when set, adds the button "Wie erstes Medium"; it returns the format of
	// the first video or image, or an error to show.
	MatchMedia func() (Format, error)
	// Even shows the even-size hint for odd resolutions (exports).
	Even bool
}

// FormatPicker is the resolution and frame rate chooser of project dialogs.
type FormatPicker struct {
	Res  *ResolutionPicker
	Rate *RatePicker

	// Relayout runs after hints appeared or vanished (see ResolutionPicker.Relayout).
	Relayout func()

	match    *widget.Button
	matchErr *widget.Label
	box      *fyne.Container
	opts     FormatOptions
	onChange func(Format, error)
}

// NewFormatPicker returns a picker holding f. changed runs after every edit with the
// current value or the reason it is not valid.
func NewFormatPicker(f Format, opts FormatOptions, changed func(Format, error)) *FormatPicker {
	p := &FormatPicker{opts: opts, onChange: changed}
	p.Res = NewResolutionPicker(f.Width, f.Height, p.changed)
	p.Res.Even = opts.Even
	p.Res.Relayout = func() { p.relayout() }
	p.Res.sync()
	p.Rate = NewRatePicker(f.FPS, RateOptions{DropFrame: true}, p.changed)
	p.Rate.Relayout = func() { p.relayout() }
	p.Rate.Set(f.FPS, f.DropFrame)
	objs := []fyne.CanvasObject{p.Res.Object(), p.Rate.Object()}
	if opts.MatchMedia != nil {
		p.match = widget.NewButtonWithIcon("Wie erstes Medium", theme.MediaVideoIcon(), p.matchTapped)
		p.matchErr = newHint(widget.WarningImportance)
		p.matchErr.Hide()
		objs = append(objs, container.NewHBox(p.match), p.matchErr)
	}
	p.box = container.New(layout.NewCustomPaddedVBoxLayout(12), objs...)
	return p
}

// SetOnSubmitted runs fn when Enter is pressed in one of the text entries (width, height,
// custom frame rate).
func (p *FormatPicker) SetOnSubmitted(fn func()) {
	f := func(string) { fn() }
	p.Res.Width.OnSubmitted = f
	p.Res.Height.OnSubmitted = f
	p.Rate.custom.OnSubmitted = f
}

func (p *FormatPicker) relayout() {
	if p.box != nil {
		p.box.Refresh()
	}
	if p.Relayout != nil {
		p.Relayout()
	}
}

// Object is the widget tree to place in a dialog.
func (p *FormatPicker) Object() fyne.CanvasObject { return p.box }

// Value is the entered format, or why it is not valid.
func (p *FormatPicker) Value() (Format, error) {
	w, h, err := p.Res.Value()
	if err != nil {
		return Format{}, err
	}
	r, drop, _, err := p.Rate.Value()
	if err != nil {
		return Format{}, err
	}
	return Format{Width: w, Height: h, FPS: r, DropFrame: drop}, nil
}

// Set shows f without calling the change callback.
func (p *FormatPicker) Set(f Format) {
	p.Res.Set(f.Width, f.Height)
	p.Rate.Set(f.FPS, f.DropFrame)
}

func (p *FormatPicker) changed() {
	if p.onChange != nil {
		f, err := p.Value()
		p.onChange(f, err)
	}
}

// MatchMedia does what the button "Wie erstes Medium" does.
func (p *FormatPicker) MatchMedia() {
	if p.opts.MatchMedia != nil {
		p.matchTapped()
	}
}

func (p *FormatPicker) matchTapped() {
	f, err := p.opts.MatchMedia()
	defer func() {
		p.box.Refresh()
		if p.Relayout != nil {
			p.Relayout()
		}
	}()
	if err != nil {
		setHint(p.matchErr, err.Error())
		return
	}
	setHint(p.matchErr, "")
	p.Set(f)
	p.changed()
}
