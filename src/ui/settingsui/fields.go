package settingsui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// field is one setting: its row in the window and how it maps to settings.Settings.
type field struct {
	cat     *category
	label   string
	help    string
	restart bool // applies after a restart only

	control fyne.CanvasObject
	row     fyne.CanvasObject
	errText *canvas.Text // validation message under the control (number entries)
	input   any          // the interactive widget (tests)

	load  func(s settings.Settings)                       // sync the control with s
	reset func(s *settings.Settings, d settings.Settings) // copy the default into s
}

func (f *field) matches(needle string) bool {
	return strings.Contains(strings.ToLower(f.label), needle) ||
		strings.Contains(strings.ToLower(f.help), needle)
}

// resetter copies one field from the defaults.
func resetter[T any](get func(*settings.Settings) *T) func(s *settings.Settings, d settings.Settings) {
	return func(s *settings.Settings, d settings.Settings) { *get(s) = *get(&d) }
}

// add finishes a field: builds its row and registers it with the category and window.
func (w *window) add(c *category, f *field) *field {
	f.cat = c
	f.row = w.buildRow(f)
	c.fields = append(c.fields, f)
	w.fields = append(w.fields, f)
	return f
}

// buildRow lays out label, explanation and control like a pro NLE preference row.
func (w *window) buildRow(f *field) fyne.CanvasObject {
	text := canvas.NewText(f.label, studiotheme.Text)
	text.TextSize = 13
	text.TextStyle = fyne.TextStyle{Bold: true}
	left := []fyne.CanvasObject{text}
	if f.help != "" {
		// RichText wraps; its inner padding is cancelled so it lines up with the label.
		help := widget.NewRichText(&widget.TextSegment{Text: f.help, Style: widget.RichTextStyle{
			ColorName: studiotheme.ColorNameMuted, SizeName: theme.SizeNameCaptionText}})
		help.Wrapping = fyne.TextWrapWord
		p := theme.InnerPadding()
		left = append(left, container.New(layout.NewCustomPaddedLayout(-p, -p, -p, -p), help))
	}
	if f.restart {
		left = append(left, container.New(layout.NewCustomPaddedLayout(3, 0, 0, 0), badge("Wirkt nach Neustart", warning)))
	}
	leftBox := container.New(&vstack{gap: 2}, left...)

	ctrl := []fyne.CanvasObject{f.control}
	if f.errText != nil {
		ctrl = append(ctrl, f.errText)
	}
	ctrlBox := container.New(&fixedWidth{w: controlW}, container.New(vcenter{}, container.New(&vstack{gap: 2}, ctrl...)))

	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	body := container.New(layout.NewCustomPaddedLayout(rowPadY, rowPadY, rowPadX, rowPadX),
		container.NewBorder(nil, nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, 24, 0), ctrlBox), leftBox))
	return container.NewBorder(nil, container.New(layout.NewCustomPaddedLayout(0, 0, rowPadX, rowPadX), line), nil, nil, body)
}

// check is an on/off setting.
func (w *window) check(c *category, label, help string, get func(*settings.Settings) *bool) *field {
	ch := widget.NewCheck("", nil)
	ch.OnChanged = func(on bool) { w.set(func(s *settings.Settings) { *get(s) = on }) }
	return w.add(c, &field{label: label, help: help, control: ch, input: ch,
		load:  func(s settings.Settings) { ch.SetChecked(*get(&s)) },
		reset: resetter(get)})
}

// option is one choice of a select.
type option[T comparable] struct {
	value T
	label string
}

// choice is a select between fixed values.
func choice[T comparable](w *window, c *category, label, help string, opts []option[T], get func(*settings.Settings) *T) *field {
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	sel := widget.NewSelect(labels, nil)
	sel.PlaceHolder = "Eigener Wert"
	sel.OnChanged = func(l string) {
		for _, o := range opts {
			if o.label == l {
				v := o.value
				w.set(func(s *settings.Settings) { *get(s) = v })
				return
			}
		}
	}
	return w.add(c, &field{label: label, help: help, control: sel, input: sel,
		load: func(s settings.Settings) {
			v := *get(&s)
			for i, o := range opts {
				if o.value == v {
					if sel.SelectedIndex() != i {
						sel.SetSelectedIndex(i)
					}
					return
				}
			}
			sel.ClearSelected()
		},
		reset: resetter(get)})
}

// number is a validated numeric entry with a unit; ints and floats share it. German
// decimal commas are accepted.
func (w *window) number(c *category, label, help, unit string, lo, hi float64, decimals int,
	read func(settings.Settings) float64, write func(*settings.Settings, float64),
	reset func(*settings.Settings, settings.Settings)) *field {
	e := widget.NewEntry()
	errText := canvas.NewText("", warning)
	errText.TextSize = 11
	errText.Hide()
	rangeMsg := fmt.Sprintf("Bitte einen Wert von %s bis %s eingeben.", formatNum(lo, decimals), formatNum(hi, decimals))
	parse := func(t string) (float64, error) {
		t = strings.ReplaceAll(strings.TrimSpace(t), ",", ".")
		v, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(v) || v < lo || v > hi {
			return 0, fmt.Errorf("%s", rangeMsg)
		}
		if decimals == 0 && v != math.Trunc(v) {
			return 0, fmt.Errorf("Bitte eine ganze Zahl eingeben.")
		}
		return v, nil
	}
	e.OnChanged = func(t string) {
		v, err := parse(t)
		if err != nil {
			errText.Text = err.Error()
			errText.Show()
			errText.Refresh()
			return
		}
		errText.Hide()
		w.set(func(s *settings.Settings) { write(s, v) })
	}
	unitText := canvas.NewText(unit, studiotheme.Muted)
	unitText.TextSize = 12
	ctrl := container.NewBorder(nil, nil, nil, container.NewCenter(unitText), container.New(&fixedWidth{w: 120}, e))
	f := &field{label: label, help: help, control: container.NewHBox(ctrl), errText: errText, reset: reset, input: e}
	f.load = func(s settings.Settings) {
		v := read(s)
		if cur, err := parse(e.Text); err == nil && cur == v {
			return // the user is typing this value: keep the cursor
		}
		e.SetText(formatNum(v, decimals))
		errText.Hide()
	}
	return w.add(c, f)
}

func (w *window) intNumber(c *category, label, help, unit string, lo, hi int, get func(*settings.Settings) *int) *field {
	return w.number(c, label, help, unit, float64(lo), float64(hi), 0,
		func(s settings.Settings) float64 { return float64(*get(&s)) },
		func(s *settings.Settings, v float64) { *get(s) = int(v) }, resetter(get))
}

func (w *window) floatNumber(c *category, label, help, unit string, lo, hi float64, get func(*settings.Settings) *float64) *field {
	return w.number(c, label, help, unit, lo, hi, 2,
		func(s settings.Settings) float64 { return *get(&s) },
		func(s *settings.Settings, v float64) { *get(s) = v }, resetter(get))
}

// formatNum prints v with up to decimals digits and a German decimal comma.
func formatNum(v float64, decimals int) string {
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	if decimals > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return strings.ReplaceAll(s, ".", ",")
}

// slider is a range setting with its value shown on the right.
func (w *window) slider(c *category, label, help string, lo, hi, step float64, format func(float64) string,
	read func(settings.Settings) float64, write func(*settings.Settings, float64),
	reset func(*settings.Settings, settings.Settings)) *field {
	sl := kit.NewSlider(lo, hi)
	sl.Step = step
	val := canvas.NewText("", studiotheme.Text)
	val.TextSize = 12
	val.Alignment = fyne.TextAlignTrailing
	sl.OnChanged = func(v float64) {
		val.Text = format(v)
		val.Refresh()
		w.set(func(s *settings.Settings) { write(s, v) })
	}
	ctrl := container.NewBorder(nil, nil, nil, container.New(&fixedWidth{w: 86}, container.New(vcenter{}, val)), sl)
	return w.add(c, &field{label: label, help: help, control: ctrl, reset: reset, input: sl,
		load: func(s settings.Settings) {
			v := read(s)
			if sl.Value != v {
				sl.SetValue(v)
			}
			val.Text = format(v)
			val.Refresh()
		}})
}

func (w *window) intSlider(c *category, label, help string, lo, hi, step int, format func(int) string, get func(*settings.Settings) *int) *field {
	return w.slider(c, label, help, float64(lo), float64(hi), float64(step),
		func(v float64) string { return format(int(math.Round(v))) },
		func(s settings.Settings) float64 { return float64(*get(&s)) },
		func(s *settings.Settings, v float64) { *get(s) = int(math.Round(v)) }, resetter(get))
}

// folder is a directory setting: path, "Wählen…" and a reset to the default.
func (w *window) folder(c *category, label, help, emptyText, title string, get func(*settings.Settings) *string) *field {
	path := widget.NewLabel("")
	path.Truncation = fyne.TextTruncateEllipsis
	var choose, clear *widget.Button
	choose = widget.NewButton("Wählen…", func() {
		w.pickFolder(title, *get(ptr(settings.Get())), func(p string) {
			if p != "" {
				w.set(func(s *settings.Settings) { *get(s) = p })
			}
		})
	})
	clear = kit.ToolButton(theme.ContentClearIcon(), func() { w.set(func(s *settings.Settings) { *get(s) = "" }) })
	pathBG := canvas.NewRectangle(studiotheme.Lane)
	pathBG.CornerRadius = 3
	pathBG.StrokeColor = studiotheme.Border
	pathBG.StrokeWidth = 1
	ctrl := container.NewBorder(nil, nil, nil, container.NewHBox(choose, clear), container.NewStack(pathBG, path))
	return w.add(c, &field{label: label, help: help, control: ctrl, reset: resetter(get), input: choose,
		load: func(s settings.Settings) {
			p := *get(&s)
			if p == "" {
				path.SetText(emptyText)
				path.Importance = widget.LowImportance
				clear.Disable()
			} else {
				path.SetText(p)
				path.Importance = widget.MediumImportance
				clear.Enable()
			}
			path.Refresh()
		}})
}

func ptr[T any](v T) *T { return &v }

// accent is the colour choice: a row of swatches with their names.
func (w *window) accent(c *category, label, help string) *field {
	type chip struct {
		id string
		sw *kit.Swatch
	}
	var chips []chip
	var objs []fyne.CanvasObject
	for _, a := range studiotheme.Accents {
		id := a.ID
		sw := kit.NewSwatch(a.Color, func() { w.set(func(s *settings.Settings) { s.Display.Accent = id }) })
		name := canvas.NewText(a.Label, studiotheme.Muted)
		name.TextSize = 10.5
		name.Alignment = fyne.TextAlignCenter
		chips = append(chips, chip{id, sw})
		objs = append(objs, container.New(&vstack{gap: 1}, container.NewCenter(sw), name))
	}
	ctrl := container.NewGridWithColumns(len(objs), objs...)
	get := func(s *settings.Settings) *string { return &s.Display.Accent }
	sws := make([]*kit.Swatch, len(chips))
	for i, ch := range chips {
		sws[i] = ch.sw
	}
	return w.add(c, &field{label: label, help: help, control: ctrl, reset: resetter(get), input: sws,
		load: func(s settings.Settings) {
			known := false
			for _, ch := range chips {
				if ch.id == s.Display.Accent {
					known = true
				}
			}
			for i, ch := range chips {
				ch.sw.SetSelected(ch.id == s.Display.Accent || (!known && i == 0))
			}
		}})
}
