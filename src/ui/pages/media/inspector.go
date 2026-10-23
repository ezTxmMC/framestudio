package media

import (
	"fmt"
	"image"
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

const previewW, previewH = 640, 360

// inspector is the "Clip-Info" panel: preview with skimming, metadata and actions.
type inspector struct {
	p *Page

	shown model.MediaItem // item currently displayed
	uses  int

	preview  *canvas.Image
	noPrev   *canvas.Text
	scrub    *kit.Slider
	scrubTC  *canvas.Text
	scrubRow *fyne.Container
	name     *canvas.Text
	values   map[string]*widget.Label
	status   *fyne.Container

	insertBtn, appendBtn, relinkBtn, removeBtn *widget.Button

	body, placeholder *fyne.Container

	// skimming: one decode at a time, the newest pending request wins
	skimBusy    bool
	skimPending int
	skimGen     int
}

var metaKeys = []string{"Typ", "Auflösung", "Dauer", "Bildrate", "Audio", "Verwendung", "Pfad"}

func newInspector(p *Page) *inspector {
	in := &inspector{p: p, values: map[string]*widget.Label{}, skimPending: -1}
	in.preview = canvas.NewImageFromImage(nil)
	in.preview.FillMode = canvas.ImageFillContain
	in.preview.ScaleMode = canvas.ImageScaleSmooth
	in.noPrev = canvas.NewText("", studiotheme.Faint)
	in.noPrev.TextSize = 12
	in.noPrev.Alignment = fyne.TextAlignCenter
	well := canvas.NewRectangle(studiotheme.MonitorOut)
	sizer := canvas.NewRectangle(color.Transparent)
	sizer.SetMinSize(fyne.NewSize(0, 176))
	screen := container.NewStack(sizer, container.New(kit.AspectLayout{Ratio: 16.0 / 9},
		container.NewStack(well, in.preview, container.NewCenter(in.noPrev))))

	in.scrub = kit.NewSlider(0, 1)
	in.scrub.Step = 1
	in.scrub.OnChanged = func(v float64) { in.skimTo(int(v)) }
	in.scrubTC = kit.MonoText("00:00:00:00", studiotheme.Text, 12)
	in.scrubRow = container.NewBorder(nil, nil, nil, container.NewCenter(in.scrubTC), in.scrub)

	in.name = canvas.NewText("", studiotheme.Text)
	in.name.TextSize = 15
	in.name.TextStyle = fyne.TextStyle{Bold: true}

	form := container.New(layout.NewFormLayout())
	for _, k := range metaKeys {
		key := canvas.NewText(k, studiotheme.Muted)
		key.TextSize = 12
		v := widget.NewLabel("")
		v.Truncation = fyne.TextTruncateEllipsis
		if k == "Pfad" {
			v.Truncation = fyne.TextTruncateOff
			v.Wrapping = fyne.TextWrapBreak
		}
		in.values[k] = v
		form.Add(container.NewVBox(spacer(3), key))
		form.Add(v)
	}
	warn := canvas.NewText("Datei fehlt – neu verknüpfen", studiotheme.Text)
	warn.TextSize = 12
	badge := canvas.NewRectangle(warnColor)
	badge.CornerRadius = 3
	badge.SetMinSize(fyne.NewSize(4, 18))
	in.status = container.NewHBox(badge, warn)

	in.insertBtn = widget.NewButtonWithIcon("In Timeline einfügen", theme.ContentAddIcon(), func() { p.insert(p.selectedItems(), false) })
	in.insertBtn.Importance = widget.HighImportance
	in.appendBtn = widget.NewButtonWithIcon("Ans Ende anhängen", theme.MediaSkipNextIcon(), func() { p.insert(p.selectedItems(), true) })
	in.relinkBtn = widget.NewButtonWithIcon("Neu verknüpfen", theme.SearchReplaceIcon(), func() { in.act(p.showRelinkDialog) })
	in.removeBtn = widget.NewButtonWithIcon("Entfernen", theme.DeleteIcon(), func() { p.confirmRemove(p.selectedItems()...) })
	for _, b := range []*widget.Button{in.appendBtn, in.relinkBtn, in.removeBtn} {
		b.Alignment = widget.ButtonAlignLeading
	}

	sep := canvas.NewRectangle(studiotheme.Border)
	sep.SetMinSize(fyne.NewSize(1, 1))
	actions := container.NewVBox(in.insertBtn, container.NewGridWithColumns(2, in.appendBtn, in.relinkBtn), in.removeBtn)
	in.body = container.NewBorder(
		container.NewVBox(screen, in.scrubRow, spacer(2), in.name, in.status),
		container.NewVBox(sep, spacer(2), actions), nil, nil,
		container.NewVScroll(form))

	hint := canvas.NewText("Kein Medium ausgewählt", studiotheme.Muted)
	hint.TextSize = 13
	hint.Alignment = fyne.TextAlignCenter
	sub := canvas.NewText("Ein Medium im Pool anklicken", studiotheme.Faint)
	sub.TextSize = 12
	sub.Alignment = fyne.TextAlignCenter
	in.placeholder = container.NewCenter(container.NewVBox(hint, sub))
	return in
}

func (in *inspector) panel() fyne.CanvasObject {
	return kit.Panel("Clip-Info", nil, container.NewStack(in.body, in.placeholder))
}

func (in *inspector) act(fn func(model.MediaItem)) {
	if m, ok := in.p.selected(); ok {
		fn(m)
	}
}

// reset forgets the displayed item so the next show reloads the preview.
func (in *inspector) reset() { in.shown = model.MediaItem{} }

// show displays the selected media item; unchanged items keep their skim position.
func (in *inspector) show() {
	m, ok := in.p.selected()
	if !ok {
		in.shown = model.MediaItem{}
		in.body.Hide()
		in.placeholder.Show()
		return
	}
	in.placeholder.Hide()
	in.body.Show()
	uses := usage(in.p.ctx.Session.Project, m.ID)
	missing := in.p.missing(m.ID)
	in.status.Hidden = !missing
	in.status.Refresh()
	in.showCount()
	if m == in.shown && uses == in.uses {
		return
	}
	changed := m.ID != in.shown.ID || m.Path != in.shown.Path
	in.shown, in.uses = m, uses
	fps := in.p.fps()

	in.name.Text = ellipsize(m.Name, 290, in.name.TextSize, in.name.TextStyle)
	in.name.Refresh()
	set := func(k, v string) { in.values[k].SetText(v) }
	set("Typ", kindLabel(m.Kind))
	set("Auflösung", "–")
	if m.Width > 0 {
		set("Auflösung", fmt.Sprintf("%d × %d", m.Width, m.Height))
	}
	set("Dauer", "Standbild")
	if m.Kind != model.KindImage {
		set("Dauer", fmt.Sprintf("%s  (%s s)", model.Timecode(frames(m, fps), fps), trimFloat(m.Duration.Seconds(), 2)))
	}
	set("Bildrate", "–")
	if m.FPS > 0 {
		set("Bildrate", trimFloat(m.FPS, 3)+" fps")
	}
	set("Audio", "nein")
	if m.HasAudio {
		set("Audio", "ja")
	}
	set("Verwendung", "nicht in der Timeline")
	if uses > 0 {
		set("Verwendung", fmt.Sprintf("%d %s in der Timeline", uses, plural(uses, "Clip", "Clips")))
	}
	set("Pfad", m.Path)

	if !changed {
		return
	}
	in.skimGen++
	in.skimPending = -1
	in.preview.Image = nil
	in.preview.Refresh()
	in.noPrev.Text = "Vorschau wird geladen …"
	if missing {
		in.noPrev.Text = "Datei nicht gefunden"
	}
	in.noPrev.Refresh()
	n := frames(m, fps)
	if m.Kind == model.KindVideo && n > 1 && !missing {
		in.scrub.OnChanged = nil
		in.scrub.Max = float64(n - 1)
		in.scrub.SetValue(float64(n / 10))
		in.scrub.OnChanged = func(v float64) { in.skimTo(int(v)) }
		in.scrubTC.Text = model.Timecode(n/10, fps)
		in.scrubTC.Refresh()
		in.scrubRow.Show()
	} else {
		in.scrubRow.Hide()
	}
	if !missing {
		in.loadPoster(m)
	}
}

// showCount labels the insert, append and remove buttons with the number of selected
// items when several are selected (they act on the whole selection).
func (in *inspector) showCount() {
	suffix := ""
	if n := len(in.p.sel); n > 1 {
		suffix = fmt.Sprintf(" (%d)", n)
	}
	for b, label := range map[*widget.Button]string{in.insertBtn: "In Timeline einfügen", in.appendBtn: "Ans Ende anhängen", in.removeBtn: "Entfernen"} {
		if b.Text != label+suffix {
			b.SetText(label + suffix)
		}
	}
	if suffix != "" {
		in.relinkBtn.Disable()
	} else {
		in.relinkBtn.Enable()
	}
}

func frames(m model.MediaItem, fps int) int { return int(m.Duration.Seconds()*float64(fps) + 0.5) }

func trimFloat(v float64, prec int) string {
	s := fmt.Sprintf("%.*f", prec, v)
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func (in *inspector) setPreview(gen int, img image.Image, err error) {
	if in.p.closed || gen != in.skimGen {
		return
	}
	if err != nil {
		in.noPrev.Text = "Keine Vorschau verfügbar"
		in.noPrev.Refresh()
		return
	}
	in.noPrev.Text = ""
	in.noPrev.Refresh()
	in.preview.Image = img
	in.preview.Refresh()
}

func (in *inspector) loadPoster(m model.MediaItem) {
	cache := in.p.ctx.Media
	if cache == nil {
		return
	}
	gen := in.skimGen
	in.p.spawn(func() {
		img, err := cache.Thumbnail(m, previewW, previewH)
		kit.Do(func() { in.setPreview(gen, img, err) })
	})
}

// skimTo shows source frame n of the displayed video; drags coalesce to the newest frame.
func (in *inspector) skimTo(n int) {
	fps := in.p.fps()
	in.scrubTC.Text = model.Timecode(n, fps)
	in.scrubTC.Refresh()
	if in.skimBusy {
		in.skimPending = n
		return
	}
	cache := in.p.ctx.Media
	if cache == nil || in.shown.Kind != model.KindVideo {
		return
	}
	in.skimBusy = true
	m, gen := in.shown, in.skimGen
	in.p.spawn(func() {
		img, err := cache.VideoFrame(m, n, fps, previewW, previewH)
		kit.Do(func() {
			in.skimBusy = false
			in.setPreview(gen, img, err)
			if next := in.skimPending; next >= 0 && gen == in.skimGen {
				in.skimPending = -1
				in.skimTo(next)
			}
		})
	})
}
