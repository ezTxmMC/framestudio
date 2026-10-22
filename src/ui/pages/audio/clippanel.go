package audio

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	labelW = 92
	valueW = 70
)

// clipPanel edits the selected audio clip: name, gain and fades, plus source details.
type clipPanel struct {
	pg       *Page
	id       string
	updating bool

	name                    *widget.Entry
	source, file            *canvas.Text
	srcRange, place, length *canvas.Text
	vol, fadeIn, fadeOut    *widget.Slider
	volVal, inVal, outVal   *canvas.Text
	hint                    *canvas.Text
	form, empty, scroll     fyne.CanvasObject
	obj                     fyne.CanvasObject
}

func newClipPanel(pg *Page) *clipPanel {
	c := &clipPanel{pg: pg}
	c.name = widget.NewEntry()
	c.name.OnChanged = func(s string) {
		if !c.updating {
			c.edit("clipname", "Clip umbenennen", func(cl *model.Clip) { cl.Name = s })
		}
	}
	c.source = valueText()
	c.file = valueText()
	c.file.Color = studiotheme.Muted
	c.srcRange = kit.MonoText("", studiotheme.Text, 12)
	c.place = kit.MonoText("", studiotheme.Text, 12)
	c.length = kit.MonoText("", studiotheme.Text, 12)

	c.vol, c.volVal = c.slider(MinDB, MaxDB, 0.5, "clipvol", "Clip-Lautstärke", func(cl *model.Clip, v float64) {
		cl.Volume = DBToLinear(v)
	})
	c.fadeIn, c.inVal = c.slider(0, 1, 1, "fadein", "Einblenden", func(cl *model.Clip, v float64) {
		cl.FadeIn = int(v)
	})
	c.fadeOut, c.outVal = c.slider(0, 1, 1, "fadeout", "Ausblenden", func(cl *model.Clip, v float64) {
		cl.FadeOut = int(v)
	})
	c.hint = canvas.NewText("", studiotheme.Muted)
	c.hint.TextSize = 11

	c.form = container.NewVBox(
		row("Name", c.name, nil),
		row("Quelle", c.source, nil),
		row("", c.file, nil),
		row("Quellbereich", c.srcRange, nil),
		row("Zeitleiste", c.place, nil),
		row("Dauer", c.length, nil),
		divider(),
		row("Lautstärke", c.vol, c.volVal),
		row("Einblenden", c.fadeIn, c.inVal),
		row("Ausblenden", c.fadeOut, c.outVal),
		container.New(layout.NewCustomPaddedLayout(2, 0, labelW, 0), c.hint),
	)
	msg := canvas.NewText("Audioclip in den Spuren anklicken, um ihn zu bearbeiten.", studiotheme.Muted)
	msg.TextSize = 12
	c.empty = container.NewCenter(msg)
	c.scroll = container.NewVScroll(c.form)
	c.obj = container.NewStack(c.scroll, c.empty)
	return c
}

func valueText() *canvas.Text {
	t := canvas.NewText("", studiotheme.Text)
	t.TextSize = 12
	return t
}

func divider() fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), hline())
}

// row lays out a form line: label column, control, optional value column.
func row(label string, control, value fyne.CanvasObject) fyne.CanvasObject {
	l := canvas.NewText(label, studiotheme.Muted)
	l.TextSize = 12
	left := container.NewStack(fixedSize(labelW, 28), container.NewHBox(container.NewCenter(l)))
	var right fyne.CanvasObject
	if value != nil {
		right = container.NewStack(fixedSize(valueW, 0), container.NewCenter(value))
	}
	return container.NewBorder(nil, nil, left, right, container.NewVBox(layout.NewSpacer(), control, layout.NewSpacer()))
}

// slider builds a horizontal control that edits the bound clip with coalesced undo steps.
func (c *clipPanel) slider(minV, maxV, step float64, key, label string, apply func(*model.Clip, float64)) (*widget.Slider, *canvas.Text) {
	s := widget.NewSlider(minV, maxV)
	s.Step = step
	val := kit.MonoText("", studiotheme.Text, 11)
	val.Alignment = fyne.TextAlignTrailing
	s.OnChanged = func(v float64) {
		if !c.updating {
			c.edit(key, label, func(cl *model.Clip) { apply(cl, v) })
		}
	}
	return s, val
}

// edit applies fn to the bound clip as a coalesced undo step.
func (c *clipPanel) edit(key, label string, fn func(*model.Clip)) {
	id := c.id
	if id == "" {
		return
	}
	err := c.pg.s.EditCoalesced(key+":"+id, label, func(p *model.Project) error {
		cl, _, _ := p.Clip(id)
		if cl == nil {
			return model.ErrNotFound
		}
		upd := *cl
		fn(&upd)
		return p.UpdateClip(upd)
	})
	if err != nil {
		c.pg.showErr(clipError(err))
		c.update()
	}
}

func clipError(err error) error {
	if err == model.ErrLocked {
		return errLockedClip
	}
	return err
}

func (c *clipPanel) update() {
	s := c.pg.s
	cl, ti, _ := s.Project.Clip(s.SelectedClip)
	if cl == nil || cl.Kind != model.KindAudio {
		c.id = ""
		c.scroll.Hide()
		c.empty.Show()
		return
	}
	c.updating = true
	defer func() { c.updating = false }()
	c.id = cl.ID
	c.empty.Hide()
	c.scroll.Show()
	p := s.Project
	fps := p.Settings.FPS

	if c.name.Text != cl.Name {
		c.name.SetText(cl.Name)
	}
	c.source.Color = studiotheme.Text
	if m, ok := p.MediaByID(cl.MediaID); ok {
		src := m.Name
		if m.Kind == model.KindVideo {
			src += "  ·  Ton aus Video"
		}
		c.source.Text = fitText(src, 12, fyne.TextStyle{}, 300)
		file := filepath.Base(m.Path)
		if m.Duration > 0 {
			file += "  ·  " + model.Timecode(p.MediaFrames(*m), fps)
		}
		c.file.Text = fitText(file, 12, fyne.TextStyle{}, 300)
	} else {
		c.source.Text = "Medium fehlt"
		c.source.Color = studiotheme.Muted
		c.file.Text = "Im Medien-Bereich neu verknüpfen."
	}
	c.srcRange.Text = model.Timecode(cl.SourceIn, fps) + " – " + model.Timecode(cl.SourceIn+cl.Length, fps)
	c.place.Text = model.Timecode(cl.Start, fps) + " – " + model.Timecode(cl.End(), fps)
	c.length.Text = model.Timecode(cl.Length, fps)
	for _, t := range []*canvas.Text{c.source, c.file, c.srcRange, c.place, c.length} {
		t.Refresh()
	}

	maxFade := float64(max(cl.Length/2, 1))
	setSlider(c.vol, MinDB, MaxDB, LinearToDB(cl.Volume))
	setSlider(c.fadeIn, 0, maxFade, float64(cl.FadeIn))
	setSlider(c.fadeOut, 0, maxFade, float64(cl.FadeOut))
	setText(c.volVal, FormatDB(LinearToDB(cl.Volume)))
	setText(c.inVal, formatSeconds(cl.FadeIn, fps))
	setText(c.outVal, formatSeconds(cl.FadeOut, fps))

	locked := p.Tracks[ti].Locked
	for _, w := range []fyne.Disableable{c.name, c.vol, c.fadeIn, c.fadeOut} {
		if locked {
			w.Disable()
		} else {
			w.Enable()
		}
	}
	if locked {
		setText(c.hint, "Spur "+p.Tracks[ti].Name+" ist gesperrt. Im Spurkopf entsperren, um den Clip zu ändern.")
	} else {
		setText(c.hint, "")
	}
}

// setSlider updates range and value without firing OnChanged.
func setSlider(s *widget.Slider, minV, maxV, v float64) {
	if s.Min == minV && s.Max == maxV && s.Value == v {
		return
	}
	s.Min, s.Max, s.Value = minV, maxV, v
	s.Refresh()
}

func setText(t *canvas.Text, s string) {
	if t.Text != s {
		t.Text = s
		t.Refresh()
	}
}
