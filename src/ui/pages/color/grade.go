package color

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"strings"

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

// gradeField describes one primary slider.
type gradeField struct {
	key, label string
	min, max   float64
	ptr        func(g *model.Grade) *float64
}

var gradeFields = []gradeField{
	{"exposure", "Belichtung", -1, 1, func(g *model.Grade) *float64 { return &g.Exposure }},
	{"contrast", "Kontrast", -1, 1, func(g *model.Grade) *float64 { return &g.Contrast }},
	{"saturation", "Sättigung", -1, 1, func(g *model.Grade) *float64 { return &g.Saturation }},
	{"temperature", "Temperatur", -1, 1, func(g *model.Grade) *float64 { return &g.Temperature }},
	{"tint", "Tönung", -1, 1, func(g *model.Grade) *float64 { return &g.Tint }},
	{"vignette", "Vignette", 0, 1, func(g *model.Grade) *float64 { return &g.Vignette }},
}

// formatValue renders a grade value with German decimal comma and explicit sign.
func formatValue(v float64) string {
	v = math.Round(v*100) / 100
	if v == 0 {
		return "0,00"
	}
	return strings.Replace(fmt.Sprintf("%+.2f", v), ".", ",", 1)
}

// gradeRow is one slider line: label, slider, readout, reset.
type gradeRow struct {
	field   gradeField
	slider  *widget.Slider
	readout *canvas.Text
	reset   *widget.Button
}

// primaryPanel is the "Primär" grading panel for the selected clip.
type primaryPanel struct {
	p *Page

	rows                        []*gradeRow
	resetAll, copyBtn, pasteBtn *widget.Button
	applyAll                    *widget.Button
	clipName, clipTC            *canvas.Text
	hint, lockHint              *canvas.Text
	grading, empty              fyne.CanvasObject
	updating                    bool // set while sliders are synced from the model
	copied                      *model.Grade
	lastClip                    string
	object                      fyne.CanvasObject
}

func newPrimaryPanel(p *Page) *primaryPanel {
	pp := &primaryPanel{p: p}
	var left, right []fyne.CanvasObject
	for i, f := range gradeFields {
		row := pp.newRow(f)
		pp.rows = append(pp.rows, row)
		line := container.NewBorder(nil, nil,
			fixedWidth(86, vcenter(labelText(f.label, studiotheme.Text))),
			container.NewHBox(fixedWidth(46, vcenter(row.readout)), row.reset),
			row.slider)
		if i < 3 {
			left = append(left, line)
		} else {
			right = append(right, line)
		}
	}
	sliders := container.New(layout.NewGridLayoutWithColumns(2),
		container.NewVBox(left...), container.NewVBox(right...))

	pp.resetAll = widget.NewButtonWithIcon("Grade zurücksetzen", theme.ContentUndoIcon(), pp.resetGrade)
	pp.copyBtn = widget.NewButtonWithIcon("Grade kopieren", theme.ContentCopyIcon(), pp.copyGrade)
	pp.pasteBtn = widget.NewButtonWithIcon("Grade einfügen", theme.ContentPasteIcon(), pp.pasteGrade)
	pp.applyAll = widget.NewButtonWithIcon("Auf alle Clips anwenden", theme.DocumentIcon(), pp.applyToAll)
	for _, b := range []*widget.Button{pp.resetAll, pp.copyBtn, pp.pasteBtn, pp.applyAll} {
		b.Alignment = widget.ButtonAlignLeading
	}
	sep := canvas.NewRectangle(studiotheme.Border)
	sep.SetMinSize(fyne.NewSize(1, 1))
	actions := fixedWidth(206, container.NewVBox(pp.resetAll, sep, pp.copyBtn, pp.pasteBtn, pp.applyAll))
	divider := canvas.NewRectangle(studiotheme.Border)
	divider.SetMinSize(fyne.NewSize(1, 1))

	pp.hint = labelText("", studiotheme.Muted)
	pp.lockHint = labelText("", studiotheme.Muted)
	footer := container.NewHBox(pp.hint, layout.NewSpacer(), pp.lockHint)

	pp.grading = container.NewBorder(nil, footer, nil,
		container.NewHBox(divider, actions),
		container.New(layout.NewCustomPaddedLayout(0, 0, 4, 12), sliders))

	title := canvas.NewText("Kein Bildclip ausgewählt", studiotheme.Text)
	title.TextSize = 14
	title.Alignment = fyne.TextAlignCenter
	sub := labelText("Wähle in der Clip-Leiste einen Video-, Bild-, Farb- oder Titelclip aus, um ihn zu graden.", studiotheme.Muted)
	sub.Alignment = fyne.TextAlignCenter
	icon := widget.NewIcon(theme.ColorPaletteIcon())
	pp.empty = container.NewCenter(container.NewVBox(container.NewCenter(icon), title, sub))

	pp.clipName = labelText("", studiotheme.Text)
	pp.clipTC = kit.MonoText("", studiotheme.Muted, 11)
	tools := container.NewHBox(pp.clipName, vcenter(pp.clipTC), layout.NewSpacer())
	// keep the panel height stable whether the controls or the empty state are shown
	keep := canvas.NewRectangle(color.Transparent)
	keep.SetMinSize(fyne.NewSize(0, pp.grading.MinSize().Height))
	pp.object = kit.Panel("Primär", container.NewPadded(tools), container.NewStack(keep, pp.grading, pp.empty))
	return pp
}

func (pp *primaryPanel) newRow(f gradeField) *gradeRow {
	row := &gradeRow{field: f}
	row.slider = widget.NewSlider(f.min, f.max)
	row.slider.Step = 0.01
	row.slider.OnChanged = func(v float64) {
		if pp.updating {
			return
		}
		v = math.Round(v*100) / 100
		row.readout.Text = formatValue(v)
		row.readout.Refresh()
		pp.setField(f, v)
	}
	row.readout = kit.MonoText("0,00", studiotheme.Text, 12)
	row.readout.Alignment = fyne.TextAlignTrailing
	row.reset = kit.ToolButton(theme.ViewRefreshIcon(), func() {
		pp.edit(f.label+" zurücksetzen", "", func(g *model.Grade) { *f.ptr(g) = 0 })
	})
	return row
}

// target returns the selected clip if it can be graded.
func (pp *primaryPanel) target() (*model.Clip, bool) {
	c, ok := pp.p.s.Clip()
	if !ok || !c.Kind.IsVisual() {
		return nil, false
	}
	return c, true
}

func (pp *primaryPanel) setField(f gradeField, v float64) {
	pp.edit(f.label+" ändern", f.key, func(g *model.Grade) { *f.ptr(g) = v })
}

// edit changes the selected clip's grade as one undo step. A non-empty coalesce key
// merges consecutive edits (slider drags) into a single step.
func (pp *primaryPanel) edit(label, coalesce string, fn func(g *model.Grade)) {
	c, ok := pp.target()
	if !ok {
		return
	}
	id := c.ID
	apply := func(p *model.Project) error {
		cur, _, _ := p.Clip(id)
		if cur == nil {
			return model.ErrNotFound
		}
		next := *cur
		fn(&next.Grade)
		return p.UpdateClip(next)
	}
	s := pp.p.s
	var err error
	if coalesce != "" {
		err = s.EditCoalesced("grade:"+id+":"+coalesce, label, apply)
	} else {
		err = s.Edit(label, apply)
	}
	if err != nil {
		pp.p.showErr(gradeError(err))
		pp.refresh()
	}
}

func gradeError(err error) error {
	if errors.Is(err, model.ErrLocked) {
		return errors.New("Grade lässt sich nicht ändern: die Spur ist gesperrt. Entsperre die Spur in der Timeline und versuche es erneut.")
	}
	return fmt.Errorf("Grade lässt sich nicht ändern: %w", err)
}

func (pp *primaryPanel) resetGrade() {
	pp.edit("Grade zurücksetzen", "", func(g *model.Grade) { *g = model.Grade{} })
}

func (pp *primaryPanel) copyGrade() {
	if c, ok := pp.target(); ok {
		g := c.Grade
		pp.copied = &g
		pp.refresh()
	}
}

func (pp *primaryPanel) pasteGrade() {
	if pp.copied == nil {
		return
	}
	g := *pp.copied
	pp.edit("Grade einfügen", "", func(dst *model.Grade) { *dst = g })
}

// applyToAll copies the selected clip's grade to every visual clip on unlocked tracks.
func (pp *primaryPanel) applyToAll() {
	c, ok := pp.target()
	if !ok {
		return
	}
	g := c.Grade
	err := pp.p.s.Edit("Grade auf alle Clips anwenden", func(p *model.Project) error {
		for _, ti := range p.TrackIndices(model.TrackVideo) {
			t := &p.Tracks[ti]
			if t.Locked {
				continue
			}
			for ci := range t.Clips {
				if t.Clips[ci].Kind.IsVisual() {
					t.Clips[ci].Grade = g
				}
			}
		}
		return nil
	})
	if err != nil {
		pp.p.showErr(gradeError(err))
	}
}

// refresh syncs the panel with the selected clip.
func (pp *primaryPanel) refresh() {
	c, ok := pp.target()
	if !ok {
		pp.grading.Hide()
		pp.empty.Show()
		pp.setHeader("", "")
		pp.lastClip = ""
		return
	}
	pp.empty.Hide()
	pp.grading.Show()

	_, ti, _ := pp.p.s.Project.Clip(c.ID)
	track := pp.p.s.Project.Tracks[ti]
	pp.setHeader(c.Name, track.Name+"  "+pp.p.s.Project.Timecode(c.Start))

	pp.updating = true
	for _, row := range pp.rows {
		v := *row.field.ptr(&c.Grade)
		row.slider.SetValue(v)
		row.readout.Text = formatValue(v)
		row.readout.Refresh()
		setEnabled(row.slider, !track.Locked)
		setEnabled(row.reset, !track.Locked && v != 0)
	}
	pp.updating = false

	setEnabled(pp.resetAll, !track.Locked && !c.Grade.IsNeutral())
	setEnabled(pp.pasteBtn, !track.Locked && pp.copied != nil)
	setEnabled(pp.applyAll, true)

	switch {
	case pp.p.compare:
		pp.hint.Text = "Vorher-Ansicht aktiv: der Viewer zeigt den Clip ohne Grade."
	default:
		pp.hint.Text = "„Vorher/Nachher“ über dem Viewer vergleicht mit dem ungegradeten Bild."
	}
	if track.Locked {
		pp.lockHint.Text = "Spur " + track.Name + " ist gesperrt"
	} else {
		pp.lockHint.Text = ""
	}
	pp.hint.Refresh()
	pp.lockHint.Refresh()
	pp.lastClip = c.ID
}

// setHeader shows the clip name and position in the panel header. The header is laid
// out again only when the text changes.
func (pp *primaryPanel) setHeader(name, pos string) {
	if pp.clipName.Text == name && pp.clipTC.Text == pos {
		return
	}
	pp.clipName.Text, pp.clipTC.Text = name, pos
	pp.object.Refresh()
}

type disableable interface {
	Enable()
	Disable()
	Disabled() bool
}

func setEnabled(w disableable, on bool) {
	if on && w.Disabled() {
		w.Enable()
	} else if !on && !w.Disabled() {
		w.Disable()
	}
}

func labelText(s string, c color.Color) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = 12
	return t
}

// fixedWidth reserves at least w points of width for obj.
func fixedWidth(w float32, obj fyne.CanvasObject) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, 0))
	return container.NewStack(r, obj)
}

// vcenter centres obj vertically while letting it fill the width.
func vcenter(obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(layout.NewSpacer(), obj, layout.NewSpacer())
}
