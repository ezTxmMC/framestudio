package audio

import (
	"fmt"
	"strings"

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
	labelW = 84
	valueW = 64
	rowH2  = 24 // compact form row
	mixed  = "—"
)

// clipPanel edits the selected audio clips: name, gain and fades, plus source details.
// With several clips selected, gain and fades apply to all of them in one undo step.
type clipPanel struct {
	pg       *Page
	ids      []string // selected audio clips, primary last
	updating bool

	name                    *widget.Entry
	count                   *canvas.Text
	nameRow                 *fyne.Container
	source                  *canvas.Text
	srcRange, place, length *canvas.Text
	vol, fadeIn, fadeOut    *kit.Slider
	volVal, inVal, outVal   *canvas.Text
	hint                    *canvas.Text
	form, empty, scroll     fyne.CanvasObject
	obj                     fyne.CanvasObject
}

func newClipPanel(pg *Page) *clipPanel {
	c := &clipPanel{pg: pg}
	c.name = widget.NewEntry()
	c.name.OnChanged = func(s string) {
		if !c.updating && len(c.ids) == 1 {
			c.edit("clipname", "Clip umbenennen", func(cl *model.Clip) { cl.Name = s })
		}
	}
	c.count = canvas.NewText("", studiotheme.Text)
	c.count.TextSize = 13
	c.count.TextStyle = fyne.TextStyle{Bold: true}
	c.nameRow = container.NewStack(c.name, container.NewHBox(container.NewCenter(c.count)))
	c.source = valueText()
	c.srcRange = kit.MonoText("", studiotheme.Text, 11)
	c.place = kit.MonoText("", studiotheme.Text, 11)
	c.length = kit.MonoText("", studiotheme.Text, 11)

	c.vol, c.volVal = c.slider(MinDB, MaxDB, 0.5, "clipvol", "Clip-Lautstärke", func(cl *model.Clip, v float64) {
		cl.Volume = DBToLinear(v)
	})
	c.fadeIn, c.inVal = c.slider(0, 1, 1, "fadein", "Einblenden", func(cl *model.Clip, v float64) {
		cl.FadeIn = min(int(v), cl.Length/2)
	})
	c.fadeOut, c.outVal = c.slider(0, 1, 1, "fadeout", "Ausblenden", func(cl *model.Clip, v float64) {
		cl.FadeOut = min(int(v), cl.Length/2)
	})
	c.hint = canvas.NewText("", studiotheme.Muted)
	c.hint.TextSize = 11

	c.form = container.NewVBox(
		row("Name", c.nameRow, nil),
		row("Quelle", c.source, nil),
		row("Quellbereich", c.srcRange, nil),
		row("Zeitleiste", c.place, nil),
		row("Dauer", c.length, nil),
		divider(),
		row("Lautstärke", c.vol, c.volVal),
		row("Einblenden", c.fadeIn, c.inVal),
		row("Ausblenden", c.fadeOut, c.outVal),
		container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), c.hint),
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
	return container.New(layout.NewCustomPaddedLayout(2, 2, 0, 0), hline())
}

// row lays out a compact form line: label column, control, optional value column.
func row(label string, control, value fyne.CanvasObject) fyne.CanvasObject {
	l := canvas.NewText(label, studiotheme.Muted)
	l.TextSize = 12
	left := container.NewStack(fixedSize(labelW, rowH2), container.NewHBox(container.NewCenter(l)))
	var right fyne.CanvasObject
	if value != nil {
		right = container.NewStack(fixedSize(valueW, 0), container.NewCenter(value))
	}
	return container.NewBorder(nil, nil, left, right, container.NewVBox(layout.NewSpacer(), control, layout.NewSpacer()))
}

// slider builds a horizontal control that edits the bound clips with coalesced undo steps.
func (c *clipPanel) slider(minV, maxV, step float64, key, label string, apply func(*model.Clip, float64)) (*kit.Slider, *canvas.Text) {
	s := kit.NewSlider(minV, maxV)
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

// edit applies fn to every bound clip on an unlocked track as one coalesced undo step.
func (c *clipPanel) edit(key, label string, fn func(*model.Clip)) {
	ids := c.ids
	if len(ids) == 0 {
		return
	}
	if len(ids) > 1 {
		label += fmt.Sprintf(" (%d Clips)", len(ids))
	}
	err := c.pg.s.EditCoalesced(key+":"+strings.Join(ids, ","), label, func(p *model.Project) error {
		changed := 0
		for _, id := range ids {
			cl, ti, _ := p.Clip(id)
			if cl == nil {
				return model.ErrNotFound
			}
			if p.Tracks[ti].Locked {
				continue
			}
			upd := *cl
			fn(&upd)
			if err := p.UpdateClip(upd); err != nil {
				return err
			}
			changed++
		}
		if changed == 0 {
			return model.ErrLocked
		}
		return nil
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

// selectedAudio returns the selected audio clips, primary clip last.
func selectedAudio(p *model.Project, sel []string, primary string) []string {
	var ids []string
	for _, id := range sel {
		if cl, _, _ := p.Clip(id); cl != nil && cl.Kind == model.KindAudio && id != primary {
			ids = append(ids, id)
		}
	}
	if cl, _, _ := p.Clip(primary); cl != nil && cl.Kind == model.KindAudio {
		ids = append(ids, primary)
	}
	return ids
}

func (c *clipPanel) update() {
	s := c.pg.s
	p := s.Project
	c.ids = selectedAudio(p, s.Selected, s.SelectedClip)
	if len(c.ids) == 0 {
		c.scroll.Hide()
		c.empty.Show()
		return
	}
	c.updating = true
	defer func() { c.updating = false }()
	c.empty.Hide()
	c.scroll.Show()
	fps := p.Settings.FPS

	clips := make([]model.Clip, 0, len(c.ids))
	locked := 0
	var lockedTrack string
	for _, id := range c.ids {
		cl, ti, _ := p.Clip(id)
		clips = append(clips, *cl)
		if p.Tracks[ti].Locked {
			locked++
			lockedTrack = p.Tracks[ti].Name
		}
	}
	cl := clips[len(clips)-1] // primary
	multi := len(clips) > 1

	if multi {
		c.name.Hide()
		c.count.Show()
		setText(c.count, fmt.Sprintf("%d Clips", len(clips)))
	} else {
		c.count.Hide()
		c.name.Show()
		if c.name.Text != cl.Name {
			c.name.SetText(cl.Name)
		}
	}

	c.source.Color = studiotheme.Text
	sameMedia := true
	for _, x := range clips {
		sameMedia = sameMedia && x.MediaID == cl.MediaID
	}
	if m, ok := p.MediaByID(cl.MediaID); ok && sameMedia {
		src := m.Name
		if m.Kind == model.KindVideo {
			src += "  ·  Ton aus Video"
		}
		if m.Duration > 0 {
			src += "  ·  " + model.Timecode(p.MediaFrames(*m), fps)
		}
		c.source.Text = fitText(src, 12, fyne.TextStyle{}, c.sourceWidth())
	} else if !sameMedia {
		c.source.Text = "Verschiedene Medien"
	} else {
		c.source.Text = "Medium fehlt. Im Medien-Bereich neu verknüpfen."
		c.source.Color = studiotheme.Muted
	}
	first, last, total := cl.Start, cl.End(), 0
	for _, x := range clips {
		first, last = min(first, x.Start), max(last, x.End())
		total += x.Length
	}
	if multi {
		c.srcRange.Text = mixed
		c.length.Text = model.Timecode(total, fps) + "  gesamt"
	} else {
		c.srcRange.Text = model.Timecode(cl.SourceIn, fps) + " – " + model.Timecode(cl.SourceIn+cl.Length, fps)
		c.length.Text = model.Timecode(cl.Length, fps)
	}
	c.place.Text = model.Timecode(first, fps) + " – " + model.Timecode(last, fps)
	for _, t := range []*canvas.Text{c.source, c.srcRange, c.place, c.length} {
		t.Refresh()
	}

	maxFade := cl.Length / 2
	sameVol, sameIn, sameOut := true, true, true
	for _, x := range clips {
		maxFade = min(maxFade, x.Length/2)
		sameVol = sameVol && x.Volume == cl.Volume
		sameIn = sameIn && x.FadeIn == cl.FadeIn
		sameOut = sameOut && x.FadeOut == cl.FadeOut
	}
	fadeMax := float64(max(maxFade, 1))
	setSlider(c.vol, MinDB, MaxDB, LinearToDB(cl.Volume))
	setSlider(c.fadeIn, 0, fadeMax, min(float64(cl.FadeIn), fadeMax))
	setSlider(c.fadeOut, 0, fadeMax, min(float64(cl.FadeOut), fadeMax))
	setText(c.volVal, pick(sameVol, FormatDB(LinearToDB(cl.Volume))))
	setText(c.inVal, pick(sameIn, formatSeconds(cl.FadeIn, fps)))
	setText(c.outVal, pick(sameOut, formatSeconds(cl.FadeOut, fps)))

	allLocked := locked == len(clips)
	for _, w := range []fyne.Disableable{c.name, c.vol, c.fadeIn, c.fadeOut} {
		if allLocked {
			w.Disable()
		} else {
			w.Enable()
		}
	}
	switch {
	case locked == 0:
		setText(c.hint, "")
	case !multi:
		setText(c.hint, "Spur "+lockedTrack+" ist gesperrt. Im Spurkopf entsperren, um den Clip zu ändern.")
	case allLocked:
		setText(c.hint, "Alle Clips liegen auf gesperrten Spuren. Im Spurkopf entsperren.")
	default:
		setText(c.hint, fmt.Sprintf("%d von %d Clips liegen auf gesperrten Spuren und bleiben unverändert.", locked, len(clips)))
	}
}

// sourceWidth is the room for the source line.
func (c *clipPanel) sourceWidth() float32 {
	if w := c.source.Size().Width; w > 40 {
		return w
	}
	return 240
}

// pick returns s, or the mixed-value dash when the selected clips differ.
func pick(same bool, s string) string {
	if same {
		return s
	}
	return mixed
}

// setSlider updates range and value without firing OnChanged.
func setSlider(s *kit.Slider, minV, maxV, v float64) {
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
