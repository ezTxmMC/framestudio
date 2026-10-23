package edit

import (
	"errors"
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

	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

// inspector shows the selected clip's properties, or the project settings when no clip
// is selected. Controls are rebuilt only when the target changes and otherwise synced
// from the model, so typing and dragging are never interrupted.
type inspector struct {
	pg      *Page
	s       *session.Session
	box     *fyne.Container
	kind    *canvas.Text // panel header: what is being inspected
	content fyne.CanvasObject

	sig     string // target signature; a change rebuilds the controls
	syncs   []func()
	syncing bool
}

var kindNames = map[model.ClipKind]string{
	model.KindColor: "Farbfläche", model.KindTitle: "Titel", model.KindImage: "Bild",
	model.KindVideo: "Video", model.KindAudio: "Audio",
}

var resolutions = []struct {
	Name string
	W, H int
}{
	{"1920 × 1080  Full HD", 1920, 1080},
	{"1280 × 720  HD", 1280, 720},
	{"3840 × 2160  UHD", 3840, 2160},
	{"1080 × 1920  Hochkant", 1080, 1920},
	{"1080 × 1080  Quadrat", 1080, 1080},
}

var frameRates = []int{24, 25, 30, 50, 60}

// Palettes as 0xRRGGBBAA.
var (
	textColors        = palette(0xFFFFFFFF, 0xC8C9C6FF, 0x000000FF, 0xF2C230FF, 0xE8903AFF, 0x5A8CEBFF)
	backColors        = palette(0x000000B4, 0x222327E6, 0xFFFFFFD9, 0x3F73B5E6, 0xB4527AE6)
	fillColors        = palette(0x000000FF, 0x222327FF, 0xFFFFFFFF, 0x3F73B5FF, 0x2F8F8BFF, 0xB8873FFF, 0xB4527AFF)
	projectBackColors = palette(0x000000FF, 0x222327FF, 0xFFFFFFFF, 0x101C30FF)
)

func palette(v ...uint32) []model.RGBA {
	out := make([]model.RGBA, len(v))
	for i, c := range v {
		out[i] = model.RGBA{R: uint8(c >> 24), G: uint8(c >> 16), B: uint8(c >> 8), A: uint8(c)}
	}
	return out
}

func labelColors() []model.RGBA {
	out := make([]model.RGBA, len(studiotheme.ClipColors))
	for i, c := range studiotheme.ClipColors {
		out[i] = rgba(c.Color)
	}
	return out
}

func newInspector(pg *Page) *inspector {
	in := &inspector{pg: pg, s: pg.ctx.Session, box: container.NewVBox()}
	in.kind = text("", studiotheme.Muted, 12)
	in.content = container.NewThemeOverride(container.NewVScroll(
		container.New(layout.NewCustomPaddedLayout(0, 6, 2, 6), in.box)), compactTheme{studiotheme.New()})
	in.update()
	return in
}

// update rebuilds or syncs after a session change.
func (in *inspector) update() {
	sig := "project"
	sel := in.pg.selection()
	if len(sel) > 1 {
		sig = "multi:" + strings.Join(sel, ",")
	} else if c, ok := in.s.Clip(); ok {
		sig = fmt.Sprintf("clip:%s:%s:%s", c.ID, c.Kind, c.MediaID)
	}
	if sig != in.sig {
		in.sig = sig
		in.syncs = nil
		var rows []fyne.CanvasObject
		if len(sel) > 1 {
			rows = in.buildMulti(sel)
			in.kind.Text = "Auswahl"
		} else if c, ok := in.s.Clip(); ok {
			rows = in.buildClip(c)
			in.kind.Text = kindNames[c.Kind]
		} else {
			rows = in.buildProject()
			in.kind.Text = "Projekt"
		}
		in.kind.Refresh()
		in.box.Objects = rows
		in.content.Refresh() // applies the small theme to the new controls
	}
	in.sync()
}

func (in *inspector) sync() {
	in.syncing = true
	defer func() { in.syncing = false }()
	for _, f := range in.syncs {
		f()
	}
}

func (in *inspector) onSync(f func()) { in.syncs = append(in.syncs, f) }

// clip returns the inspected clip from the live project, or nil if it is gone.
func (in *clipEditor) clip() *model.Clip {
	c, _, _ := in.s.Project.Clip(in.id)
	return c
}

func (in *inspector) fps() int { return in.s.FPS() }

func (in *inspector) tc(f int) string { return model.Timecode(f, in.fps()) }

// set changes one property of the inspected clip. Continuous controls coalesce into a
// single undo step per clip and field.
func (in *clipEditor) set(field, label string, coalesce bool, fn func(c *model.Clip)) error {
	if in.syncing {
		return nil
	}
	id := in.id
	edit := func(p *model.Project) error {
		c, _, _ := p.Clip(id)
		if c == nil {
			return model.ErrNotFound
		}
		next := *c
		fn(&next)
		return p.UpdateClip(next)
	}
	var err error
	if coalesce {
		err = in.s.EditCoalesced(id+":"+field, label, edit)
	} else {
		err = in.s.Edit(label, edit)
	}
	if err != nil {
		in.pg.showErr(explain(err))
	}
	return err
}

func (in *inspector) slider(min, max, step float64, change func(v float64)) *kit.Slider {
	s := kit.NewSlider(min, max)
	s.Step = step
	s.OnChanged = func(v float64) {
		if !in.syncing {
			change(v)
		}
	}
	return s
}

// clipEditor builds the controls of one clip; its edits stay bound to that clip even
// if a pending entry commits after the selection moved on.
type clipEditor struct {
	*inspector
	id string
}

func (in *inspector) buildClip(c *model.Clip) []fyne.CanvasObject {
	return (&clipEditor{inspector: in, id: c.ID}).build(c)
}

func (in *clipEditor) build(c *model.Clip) []fyne.CanvasObject {
	rows := []fyne.CanvasObject{in.clipHeader()}
	if c.Kind == model.KindColor {
		rows = append(rows, in.timingSection(c), section("Füllung", in.colorRows("Farbe", fillColors, true)...))
	} else {
		// the label colour sits with name and timing: one section less to scroll
		rows = append(rows, in.timingSection(c, in.colorRows("Clipfarbe", labelColors(), false)...))
	}
	if c.Kind == model.KindTitle {
		rows = append(rows, in.titleSection())
	}
	if c.Kind.IsVisual() {
		rows = append(rows, in.compositeSection(c))
	} else {
		rows = append(rows, in.audioSection())
	}
	rows = append(rows, in.fadeSection())
	return rows
}

// clipHeader shows colour, name, kind and track of the clip at the top.
func (in *clipEditor) clipHeader() fyne.CanvasObject {
	chip := canvas.NewRectangle(studiotheme.Steel)
	chip.CornerRadius = 2
	chip.SetMinSize(fyne.NewSize(4, 28))
	name, nameBox := truncLabel(true, false)
	sub, subBox := truncLabel(false, true)
	in.onSync(func() {
		c := in.clip()
		if c == nil {
			return
		}
		_, ti, _ := in.s.Project.Clip(c.ID)
		chip.FillColor = c.Color.NRGBA()
		chip.Refresh()
		name.SetText(c.Name)
		info := kindNames[c.Kind] + "  ·  Spur " + in.s.Project.Tracks[ti].Name
		if c.MediaID != "" {
			if m, ok := in.s.Project.MediaByID(c.MediaID); ok {
				info += "  ·  " + m.Name
			} else {
				info += "  ·  Medium fehlt"
			}
		}
		sub.SetText(info)
	})
	var link fyne.CanvasObject
	if c := in.clip(); c != nil && c.Kind.IsVisual() {
		// the colour page is one click away for every visual clip
		h := widget.NewHyperlink("Farbkorrektur", nil)
		h.OnTapped = func() {
			if in.pg.ctx.Navigate != nil {
				in.pg.ctx.Navigate(page.Color)
			}
		}
		link = container.NewCenter(h)
	}
	return container.New(layout.NewCustomPaddedLayout(0, 4, 0, 0), container.NewBorder(nil, nil,
		container.NewCenter(chip), link, container.New(layout.NewCustomPaddedVBoxLayout(0), nameBox, subBox)))
}

// truncLabel is a one-line label that shortens with an ellipsis instead of widening the
// inspector; box holds it with the label padding trimmed.
func truncLabel(bold, small bool) (*widget.Label, fyne.CanvasObject) {
	l := widget.NewLabel("")
	l.Truncation = fyne.TextTruncateEllipsis
	l.TextStyle.Bold = bold
	if small {
		l.SizeName = theme.SizeNameCaptionText
		l.Importance = widget.LowImportance
	}
	return l, container.New(layout.NewCustomPaddedLayout(-4, -4, -2, 0), l)
}

func (in *clipEditor) timingSection(c *model.Clip, extra ...fyne.CanvasObject) fyne.CanvasObject {
	nameNote, timeNote := newNote(), newNote()
	name := newCommitEntry(false, nil)
	name.OnCommit = func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			nameNote.show(errors.New("Der Name darf nicht leer sein"))
			return
		}
		nameNote.show(nil)
		if in.set("name", "Clip umbenennen", false, func(c *model.Clip) { c.Name = s }) == nil {
			name.force(s)
		}
	}
	start := newCommitEntry(true, nil)
	dur := newCommitEntry(true, nil)
	end := kit.MonoText("", studiotheme.Muted, 12)
	start.OnCommit = func(s string) {
		f, err := kit.ParseFrames(s, in.fps())
		if err == nil {
			err = in.timing("Clip verschieben", func(p *model.Project, c *model.Clip, track string) error {
				return p.MoveClip(c.ID, track, f)
			})
		}
		timeNote.show(err)
		if err == nil {
			start.force(in.tc(f))
		}
	}
	dur.OnCommit = func(s string) {
		f, err := kit.ParseFrames(s, in.fps())
		if err == nil && f < 1 {
			err = errors.New("Die Dauer muss mindestens 1 Frame betragen")
		}
		if err == nil {
			err = in.timing("Clipdauer ändern", func(p *model.Project, c *model.Clip, track string) error {
				if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
					if avail := p.MediaFrames(*m) - c.SourceIn; f > avail {
						return fmt.Errorf("Das Quellmaterial reicht nur für %s. Eine kürzere Dauer eingeben", p.Timecode(avail))
					}
				}
				// Like dragging the edge, the length stops at the next clip instead of
				// overwriting it.
				if next, ok := nextClipStart(p, track, c); ok && c.Start+f > next {
					return fmt.Errorf("Der nächste Clip beginnt bei %s. Höchstens %s eingeben oder den Clip zuerst verschieben",
						p.Timecode(next), p.Timecode(next-c.Start))
				}
				return p.TrimClip(c.ID, c.Start, f)
			})
		}
		timeNote.show(err)
		if err == nil {
			dur.force(in.tc(f))
		}
	}
	in.onSync(func() {
		if c := in.clip(); c != nil {
			name.set(c.Name)
			start.set(in.tc(c.Start))
			dur.set(in.tc(c.Length))
			end.Text = in.tc(c.End())
			end.Refresh()
		}
	})
	rows := []fyne.CanvasObject{
		field("Name", name), nameNote.box,
		field("Start", start), field("Dauer", dur), field("Ende", value(end)), timeNote.box,
	}
	return section("Clip", append(rows, extra...)...)
}

// timing runs a timing edit on the inspected clip and returns a readable error.
func (in *clipEditor) timing(label string, fn func(p *model.Project, c *model.Clip, track string) error) error {
	id := in.id
	err := in.s.Edit(label, func(p *model.Project) error {
		c, ti, _ := p.Clip(id)
		if c == nil {
			return model.ErrNotFound
		}
		return fn(p, c, p.Tracks[ti].ID)
	})
	return explain(err)
}

// colorRows are the swatches (and optionally a hex entry) for the clip colour.
func (in *clipEditor) colorRows(label string, colors []model.RGBA, hex bool) []fyne.CanvasObject {
	sw := newSwatches(colors, func(c model.RGBA) {
		in.set("color", "Farbe ändern", false, func(cl *model.Clip) { cl.Color = c })
	})
	rows := []fyne.CanvasObject{field(label, sw.box)}
	var hexEntry *commitEntry
	if hex {
		n := newNote()
		hexEntry = newCommitEntry(true, nil)
		hexEntry.OnCommit = func(s string) {
			c, err := parseHex(s)
			n.show(err)
			if err == nil {
				in.set("color", "Farbe ändern", false, func(cl *model.Clip) { cl.Color = c })
			}
		}
		rows = append(rows, field("Hex", hexEntry), n.box)
	}
	in.onSync(func() {
		if c := in.clip(); c != nil {
			sw.set(c.Color)
			if hexEntry != nil {
				hexEntry.set(formatHex(c.Color))
			}
		}
	})
	return rows
}

func (in *clipEditor) titleSection() fyne.CanvasObject {
	body := widget.NewMultiLineEntry()
	body.SetMinRowsVisible(3)
	body.Wrapping = fyne.TextWrapWord
	body.OnChanged = func(s string) {
		in.set("text", "Titeltext ändern", true, func(c *model.Clip) { c.Title.Text = s })
	}
	sizeVal, yVal := readout(), readout()
	size := in.slider(2, 30, 0.5, func(v float64) {
		in.set("size", "Schriftgröße ändern", true, func(c *model.Clip) { c.Title.Size = v / 100 })
	})
	y := in.slider(0, 100, 1, func(v float64) {
		in.set("y", "Position ändern", true, func(c *model.Clip) { c.Title.Y = v / 100 })
	})
	fg := newSwatches(textColors, func(col model.RGBA) {
		in.set("textcolor", "Schriftfarbe ändern", false, func(c *model.Clip) { c.Title.Color = col })
	})
	bold := widget.NewCheck("Fett", func(on bool) {
		in.set("bold", "Schriftstil ändern", false, func(c *model.Clip) { c.Title.Bold = on })
	})
	aligns := []model.Align{model.AlignLeft, model.AlignCenter, model.AlignRight}
	alignNames := []string{"Links", "Mitte", "Rechts"}
	align := widget.NewRadioGroup(alignNames, func(s string) {
		for i, n := range alignNames {
			if n == s {
				in.set("align", "Ausrichtung ändern", false, func(c *model.Clip) { c.Title.Align = aligns[i] })
			}
		}
	})
	align.Horizontal = true
	align.Required = true
	bgOn := widget.NewCheck("Anzeigen", func(on bool) {
		in.set("background", "Hintergrund ändern", false, func(c *model.Clip) {
			switch {
			case on && c.Title.Background.A == 0:
				c.Title.Background = backColors[0]
			case !on:
				c.Title.Background.A = 0
			}
		})
	})
	bg := newSwatches(backColors, func(col model.RGBA) {
		in.set("background", "Hintergrund ändern", false, func(c *model.Clip) { c.Title.Background = col })
	})
	in.onSync(func() {
		c := in.clip()
		if c == nil {
			return
		}
		t := c.Title
		if !focused(body) && body.Text != t.Text {
			body.SetText(t.Text)
		}
		size.SetValue(t.Size * 100)
		sizeVal.Text = percent(t.Size * 100)
		sizeVal.Refresh()
		y.SetValue(t.Y * 100)
		yVal.Text = percent(t.Y * 100)
		yVal.Refresh()
		fg.set(t.Color)
		bold.SetChecked(t.Bold)
		for i, a := range aligns {
			if a == t.Align || (t.Align == "" && a == model.AlignCenter) {
				align.SetSelected(alignNames[i])
			}
		}
		bgOn.SetChecked(t.Background.A > 0)
		bg.set(t.Background)
	})
	return section("Text",
		field("Inhalt", body),
		sliderField("Größe", size, sizeVal),
		field("Schriftfarbe", fg.box),
		field("Stil", bold),
		field("Ausrichtung", align),
		sliderField("Vertikal", y, yVal),
		field("Hintergrund", bgOn),
		field("", bg.box),
	)
}

func (in *clipEditor) compositeSection(c *model.Clip) fyne.CanvasObject {
	opVal := readout()
	opacity := in.slider(0, 100, 1, func(v float64) {
		in.set("opacity", "Deckkraft ändern", true, func(c *model.Clip) { c.Opacity = v / 100 })
	})
	rows := []fyne.CanvasObject{sliderField("Deckkraft", opacity, opVal)}
	fits := []model.Fit{model.FitContain, model.FitCover, model.FitStretch}
	fitNames := []string{"Einpassen", "Füllen", "Strecken"}
	var fit *widget.Select
	if c.Kind == model.KindImage || c.Kind == model.KindVideo {
		fit = newSelect(fitNames, func(s string) {
			for i, n := range fitNames {
				if n == s {
					in.set("fit", "Einpassung ändern", false, func(c *model.Clip) { c.Fit = fits[i] })
				}
			}
		})
		rows = append(rows, field("Einpassen", fit))
	}
	in.onSync(func() {
		c := in.clip()
		if c == nil {
			return
		}
		opacity.SetValue(c.Opacity * 100)
		opVal.Text = percent(c.Opacity * 100)
		opVal.Refresh()
		if fit != nil {
			idx := 0
			for i, f := range fits {
				if f == c.Fit {
					idx = i
				}
			}
			fit.SetSelected(fitNames[idx])
		}
	})
	return section("Komposition", rows...)
}

func (in *clipEditor) audioSection() fyne.CanvasObject {
	val := readout()
	vol := in.slider(-60, 12, 0.5, func(db float64) {
		in.set("volume", "Lautstärke ändern", true, func(c *model.Clip) { c.Volume = fromDB(db) })
	})
	in.onSync(func() {
		if c := in.clip(); c != nil {
			db := toDB(c.Volume)
			vol.SetValue(db)
			val.Text = formatDB(db)
			val.Refresh()
		}
	})
	return section("Audio", sliderField("Lautstärke", vol, val))
}

func (in *clipEditor) fadeSection() fyne.CanvasObject {
	n := newNote()
	entry := func(label string, set func(c *model.Clip, f int)) *commitEntry {
		var e *commitEntry
		e = newCommitEntry(true, func(s string) {
			c := in.clip()
			if c == nil {
				return
			}
			f, err := kit.ParseFrames(s, in.fps())
			if err == nil && f > c.Length {
				err = fmt.Errorf("Die Blende ist länger als der Clip (%s)", in.tc(c.Length))
			}
			n.show(err)
			if err == nil && in.set(label, label+" ändern", false, func(c *model.Clip) { set(c, f) }) == nil {
				e.force(in.tc(f))
			}
		})
		return e
	}
	fadeIn := entry("Einblendung", func(c *model.Clip, f int) { c.FadeIn = f })
	fadeOut := entry("Ausblendung", func(c *model.Clip, f int) { c.FadeOut = f })
	in.onSync(func() {
		if c := in.clip(); c != nil {
			fadeIn.set(in.tc(c.FadeIn))
			fadeOut.set(in.tc(c.FadeOut))
		}
	})
	return section("Blenden", field("Einblenden", fadeIn), field("Ausblenden", fadeOut), n.box)
}

func (in *inspector) buildProject() []fyne.CanvasObject {
	s := in.s
	name := newCommitEntry(false, nil)
	nameNote := newNote()
	name.OnCommit = func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			nameNote.show(errors.New("Der Projektname darf nicht leer sein"))
			return
		}
		nameNote.show(nil)
		s.Rename(v)
		name.force(v)
	}

	res := newSelect(nil, nil)
	res.OnChanged = func(v string) {
		if in.syncing {
			return
		}
		for _, r := range resolutions {
			if r.Name == v {
				in.pg.edit("Auflösung ändern", func(p *model.Project) error {
					p.Settings.Width, p.Settings.Height = r.W, r.H
					return nil
				})
			}
		}
	}
	rateNames := make([]string, len(frameRates))
	for i, r := range frameRates {
		rateNames[i] = fmt.Sprintf("%d fps", r)
	}
	rate := newSelect(rateNames, nil)
	rate.OnChanged = func(v string) {
		if in.syncing {
			return
		}
		for i, n := range rateNames {
			if n == v {
				in.pg.setFPS(frameRates[i])
			}
		}
	}
	bg := newSwatches(projectBackColors, func(c model.RGBA) {
		in.pg.edit("Hintergrundfarbe ändern", func(p *model.Project) error {
			p.Settings.Background = c
			return nil
		})
	})
	rateHint := text("23,976 fps wird nicht unterstützt", studiotheme.Faint, 11)

	stat := func() *canvas.Text { return text("", studiotheme.Text, 12) }
	tracks, clips, media, markers := stat(), stat(), stat(), stat()
	length := kit.MonoText("", studiotheme.Text, 12)

	in.onSync(func() {
		p := s.Project
		name.set(p.Name)
		opts := make([]string, 0, len(resolutions)+1)
		sel := ""
		for _, r := range resolutions {
			opts = append(opts, r.Name)
			if r.W == p.Settings.Width && r.H == p.Settings.Height {
				sel = r.Name
			}
		}
		if sel == "" {
			sel = fmt.Sprintf("%d × %d  Eigene", p.Settings.Width, p.Settings.Height)
			opts = append(opts, sel)
		}
		res.SetOptions(opts)
		res.SetSelected(sel)
		cur := fmt.Sprintf("%d fps", p.Settings.FPS)
		if !contains(rateNames, cur) {
			rate.SetOptions(append(append([]string{}, rateNames...), cur))
		} else {
			rate.SetOptions(rateNames)
		}
		rate.SetSelected(cur)
		bg.set(p.Settings.Background)

		n := 0
		for _, t := range p.Tracks {
			n += len(t.Clips)
		}
		setText(tracks, fmt.Sprintf("%d Video, %d Audio", len(p.TrackIndices(model.TrackVideo)), len(p.TrackIndices(model.TrackAudio))))
		setText(clips, plural(n, "Clip", "Clips"))
		setText(media, plural(len(p.Media), "Datei", "Dateien"))
		setText(markers, plural(len(p.Markers), "Marker", "Marker"))
		setText(length, p.Timecode(p.Duration()))
	})

	hint := widget.NewLabel("Clip in der Timeline auswählen, um Name, Zeit und Aussehen zu bearbeiten.")
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	hint.SizeName = theme.SizeNameCaptionText

	return []fyne.CanvasObject{
		firstSection("Projekt",
			field("Name", name), nameNote.box,
			field("Auflösung", res),
			field("Bildrate", rate),
			field("", rateHint),
			field("Hintergrund", bg.box),
		),
		section("Übersicht",
			field("Spuren", value(tracks)),
			field("Clips", value(clips)),
			field("Medien", value(media)),
			field("Marker", value(markers)),
			field("Laufzeit", value(length)),
		),
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), hint),
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func setText(t *canvas.Text, s string) {
	if t.Text != s {
		t.Text = s
		t.Refresh()
	}
}

func focused(o fyne.Focusable) bool {
	c := fyne.CurrentApp().Driver().CanvasForObject(o.(fyne.CanvasObject))
	return c != nil && c.Focused() == o
}

func percent(v float64) string { return strconv.Itoa(int(math.Round(v))) + " %" }

func toDB(gain float64) float64 {
	if gain <= 0.001 {
		return -60
	}
	return math.Max(-60, math.Min(12, 20*math.Log10(gain)))
}

func fromDB(db float64) float64 {
	if db <= -60 {
		return 0
	}
	return math.Pow(10, db/20)
}

func formatDB(db float64) string {
	if db <= -60 {
		return "−∞ dB"
	}
	s := strings.Replace(strconv.FormatFloat(db, 'f', 1, 64), ".", ",", 1)
	s = strings.Replace(s, "-", "−", 1)
	if db > 0 {
		s = "+" + s
	}
	return s + " dB"
}

func formatHex(c model.RGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func parseHex(s string) (model.RGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return model.RGBA{}, errors.New("Farbe als Hexwert angeben, z.B. #3F73B5")
	}
	return model.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

// nextClipStart returns the start of the first clip after c on the track with ID track.
func nextClipStart(p *model.Project, track string, c *model.Clip) (int, bool) {
	t, _ := p.Track(track)
	if t == nil {
		return 0, false
	}
	next, ok := 0, false
	for _, o := range t.Clips {
		if o.ID != c.ID && o.Start >= c.End() && (!ok || o.Start < next) {
			next, ok = o.Start, true
		}
	}
	return next, ok
}
