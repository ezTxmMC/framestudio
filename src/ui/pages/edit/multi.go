package edit

import (
	"fmt"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
)

// mixed is shown for a property whose value differs between the selected clips.
const mixed = "—"

// multiEditor edits the properties several selected clips share. Every change applies
// to all of them in one undo step.
type multiEditor struct {
	*inspector
	ids []string
	key string // coalescing key prefix for sliders
}

func (in *inspector) buildMulti(ids []string) []fyne.CanvasObject {
	return (&multiEditor{inspector: in, ids: ids, key: "multi:" + strings.Join(ids, ",")}).build()
}

// clips returns the selected clips that still exist in the live project.
func (m *multiEditor) clips() []*model.Clip {
	var out []*model.Clip
	for _, id := range m.ids {
		if c, _, _ := m.s.Project.Clip(id); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// apply changes every selected clip that match accepts in one undo step; sliders
// coalesce under field.
func (m *multiEditor) apply(field, label string, coalesce bool, match func(c *model.Clip) bool, fn func(c *model.Clip)) error {
	if m.syncing {
		return nil
	}
	ids := m.ids
	edit := func(p *model.Project) error {
		for _, id := range ids {
			c, _, _ := p.Clip(id)
			if c == nil || !match(c) {
				continue
			}
			next := *c
			fn(&next)
			if err := p.UpdateClip(next); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if coalesce {
		err = m.s.EditCoalesced(m.key+":"+field, label, edit)
	} else {
		err = m.s.Edit(label, edit)
	}
	if err != nil {
		m.pg.showErr(explain(err))
	}
	return err
}

func anyClip(*model.Clip) bool      { return true }
func labelled(c *model.Clip) bool   { return c.Kind != model.KindColor }
func visualClip(c *model.Clip) bool { return c.Kind.IsVisual() }
func audioClip(c *model.Clip) bool  { return c.Kind == model.KindAudio }
func count(cs []*model.Clip, f func(*model.Clip) bool) int {
	n := 0
	for _, c := range cs {
		if f(c) {
			n++
		}
	}
	return n
}

// same returns the common value of get over the clips that match, or ok=false when
// they differ (or none match).
func same[T comparable](cs []*model.Clip, match func(*model.Clip) bool, get func(*model.Clip) T) (v T, ok bool) {
	first := true
	for _, c := range cs {
		if !match(c) {
			continue
		}
		x := get(c)
		if first {
			v, first = x, false
			continue
		}
		if x != v {
			return v, false
		}
	}
	return v, !first
}

func (m *multiEditor) build() []fyne.CanvasObject {
	cs := m.clips()
	rows := []fyne.CanvasObject{m.header(cs)}
	if count(cs, labelled) > 0 {
		rows = append(rows, m.labelSection())
	}
	if count(cs, visualClip) > 0 {
		rows = append(rows, m.opacitySection())
	}
	if count(cs, audioClip) > 0 {
		rows = append(rows, m.volumeSection())
	}
	rows = append(rows, m.fadeSection())
	hint := widget.NewLabel("Name und Zeit lassen sich nur einzeln bearbeiten. Esc hebt die Auswahl auf.")
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	hint.SizeName = theme.SizeNameCaptionText
	rows = append(rows, container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), hint))
	return rows
}

// header: "N Clips ausgewählt" over a breakdown by kind.
func (m *multiEditor) header(cs []*model.Clip) fyne.CanvasObject {
	chip := canvas.NewRectangle(studiotheme.Steel)
	chip.CornerRadius = 2
	chip.SetMinSize(fyne.NewSize(4, 28))
	name := text(fmt.Sprintf("%d Clips ausgewählt", len(cs)), studiotheme.Text, 13)
	name.TextStyle.Bold = true
	sub := text(kindBreakdown(cs), studiotheme.Muted, 11)
	return container.New(layout.NewCustomPaddedLayout(0, 4, 0, 0), container.NewBorder(nil, nil,
		container.NewCenter(chip), nil, container.NewVBox(name, sub)))
}

func kindBreakdown(cs []*model.Clip) string {
	order := []model.ClipKind{model.KindVideo, model.KindAudio, model.KindImage, model.KindTitle, model.KindColor}
	var parts []string
	for _, k := range order {
		if n := count(cs, func(c *model.Clip) bool { return c.Kind == k }); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, kindNames[k]))
		}
	}
	return strings.Join(parts, "  ·  ")
}

func (m *multiEditor) labelSection() fyne.CanvasObject {
	note := readout()
	note.Alignment = fyne.TextAlignLeading
	note.Color = studiotheme.Muted
	sw := newSwatches(labelColors(), func(c model.RGBA) {
		m.apply("color", "Clipfarbe ändern", false, labelled, func(cl *model.Clip) { cl.Color = c })
	})
	m.onSync(func() {
		c, ok := same(m.clips(), labelled, func(c *model.Clip) model.RGBA { return c.Color })
		if !ok {
			c = model.RGBA{}
		}
		sw.set(c)
		setText(note, map[bool]string{true: "", false: mixed}[ok])
	})
	return section("Bezeichnung", field("Clipfarbe", container.NewHBox(sw.box, vcenter(note))))
}

func (m *multiEditor) opacitySection() fyne.CanvasObject {
	val := readout()
	sl := m.slider(0, 100, 1, func(v float64) {
		m.apply("opacity", "Deckkraft ändern", true, visualClip, func(c *model.Clip) { c.Opacity = v / 100 })
	})
	m.onSync(func() {
		cs := m.clips()
		v, ok := same(cs, visualClip, func(c *model.Clip) float64 { return math.Round(c.Opacity * 100) })
		if !ok {
			v = avg(cs, visualClip, func(c *model.Clip) float64 { return c.Opacity * 100 })
			setText(val, mixed)
		} else {
			setText(val, percent(v))
		}
		sl.SetValue(v)
	})
	return section("Komposition", sliderField("Deckkraft", sl, val))
}

func (m *multiEditor) volumeSection() fyne.CanvasObject {
	val := readout()
	sl := m.slider(-60, 60, 0.5, func(db float64) {
		m.apply("volume", "Lautstärke ändern", true, audioClip, func(c *model.Clip) { c.Volume = fromDB(db) })
	})
	m.onSync(func() {
		cs := m.clips()
		db, ok := same(cs, audioClip, func(c *model.Clip) float64 { return math.Round(toDB(c.Volume)*2) / 2 })
		if !ok {
			db = avg(cs, audioClip, func(c *model.Clip) float64 { return toDB(c.Volume) })
			setText(val, mixed)
		} else {
			setText(val, formatDB(db))
		}
		sl.SetValue(db)
	})
	return section("Audio", sliderField("Lautstärke", sl, val))
}

func avg(cs []*model.Clip, match func(*model.Clip) bool, get func(*model.Clip) float64) float64 {
	sum, n := 0.0, 0
	for _, c := range cs {
		if match(c) {
			sum += get(c)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func (m *multiEditor) fadeSection() fyne.CanvasObject {
	n := newNote()
	entry := func(label string, set func(c *model.Clip, f int)) *commitEntry {
		var e *commitEntry
		e = newCommitEntry(true, func(s string) {
			f, err := m.parse(s)
			if err == nil {
				shortest := math.MaxInt
				for _, c := range m.clips() {
					shortest = min(shortest, c.Length)
				}
				if f > shortest {
					err = fmt.Errorf("Die Blende ist länger als der kürzeste Clip (%s). Kürzere Blende eingeben", m.tc(shortest))
				}
			}
			n.show(err)
			if err == nil && m.apply(label, label+" ändern", false, anyClip, func(c *model.Clip) { set(c, f) }) == nil {
				e.force(m.tc(f))
			}
		})
		e.SetPlaceHolder(mixed)
		return e
	}
	fadeIn := entry("Einblendung", func(c *model.Clip, f int) { c.FadeIn = f })
	fadeOut := entry("Ausblendung", func(c *model.Clip, f int) { c.FadeOut = f })
	m.onSync(func() {
		cs := m.clips()
		show := func(e *commitEntry, get func(c *model.Clip) int) {
			if v, ok := same(cs, anyClip, get); ok {
				e.set(m.tc(v))
			} else {
				e.set(mixed)
			}
		}
		show(fadeIn, func(c *model.Clip) int { return c.FadeIn })
		show(fadeOut, func(c *model.Clip) int { return c.FadeOut })
	})
	return section("Blenden", field("Einblenden", fadeIn), field("Ausblenden", fadeOut), n.box)
}
