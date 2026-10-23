package deliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/export"
	"framestudio-proto/src/model"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// resolution is one entry of the "Auflösung" chooser; zero size means custom.
type resolution struct {
	label string
	w, h  int
}

const (
	rangeAll  = "Gesamte Timeline"
	rangeIO   = "In-Out-Bereich"
	resCustom = kit.ResCustom
	resSame   = "Wie Projekt"
)

// settingsForm is the "Render-Einstellungen" panel.
type settingsForm struct {
	pg       *Page
	presets  []export.Preset
	rows     []*presetRow
	presetAt int // selected preset, -1 when the settings no longer match one

	name, folder      *widget.Entry
	size              *kit.ResolutionPicker
	width, height     *widget.Entry // the entries of size
	rate              *kit.RatePicker
	rateRow           fyne.CanvasObject
	rateNote          *widget.Label
	evenNote          *widget.Label
	ext               *canvas.Text
	format            *kit.Select
	res               *kit.Select
	resolutions       []resolution
	customRow         fyne.CanvasObject
	quality           *segmented
	rng               *kit.Select
	rangeInfo         *canvas.Text
	audio             *kit.Check
	audioWanted       bool
	sumPath, sumVideo *canvas.Text
	sumAudio, sumTime *canvas.Text
	sumFrames         *canvas.Text
	sumFramesRow      fyne.CanvasObject
	lastIn, lastOut   int
	lastW, lastH      int // size of the last non-custom resolution choice
	formBox, body     *fyne.Container
	advanced          *collapsible
	scroll            *container.Scroll
	errText           *widget.Label
	errBox            fyne.CanvasObject
	addBtn, renderBtn *widget.Button
	content           fyne.CanvasObject

	updating bool   // suppresses change handling while the form is filled in code
	onChange func() // called after every user change
}

func newSettingsForm(pg *Page) *settingsForm {
	f := &settingsForm{pg: pg, presetAt: -1, audioWanted: true}
	proj := pg.ctx.Session.Project

	f.presets = export.Presets(proj)
	presetList := container.NewVBox()
	for i, ps := range f.presets {
		row := newPresetRow(ps, func() { f.applyPreset(i) })
		f.rows = append(f.rows, row)
		presetList.Add(row)
	}

	f.name = widget.NewEntry()
	f.name.SetText(fileName(proj.Name))
	f.name.OnChanged = func(string) { f.changed() }
	f.ext = kit.MonoText(".mp4", studiotheme.Muted, 12)
	nameRow := container.NewBorder(nil, nil, nil, container.NewPadded(vcenter(f.ext)), f.name)

	f.folder = widget.NewEntry()
	f.folder.SetText(exportFolder())
	f.folder.OnChanged = func(string) { f.changed() }
	browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), f.chooseFolder)
	folderRow := container.NewBorder(nil, nil, nil, browse, f.folder)

	var labels []string
	for _, ft := range export.Formats() {
		labels = append(labels, export.Label(ft))
	}
	f.format = kit.NewSelect(labels, func(string) { f.formatChanged() })

	f.res = kit.NewSelect(nil, func(string) { f.resChanged() })
	pr := proj.Settings
	f.size = kit.NewResolutionPicker(pr.Width, pr.Height, f.changed)
	f.width, f.height = f.size.Width, f.size.Height
	f.size.Relayout = func() { f.formBox.Refresh() }
	f.size.HidePreset()
	f.customRow = f.size.Object()
	f.customRow.Hide()
	f.evenNote = noteLabel(widget.LowImportance)
	f.rate = kit.NewRatePicker(model.Rate{}, kit.RateOptions{SameLabel: sameRateLabel(proj.Rate(), export.FormatMP4)}, f.rateChanged)
	f.rate.Relayout = func() { f.formBox.Refresh() }
	f.rateRow = f.rate.Details()
	f.rateNote = noteLabel(widget.LowImportance)

	f.quality = newSegmented([]string{"Hoch", "Mittel", "Niedrig"},
		[]string{string(export.QualityHigh), string(export.QualityMedium), string(export.QualityLow)},
		func(string) { f.changed() })

	f.rng = kit.NewSelect([]string{rangeAll}, func(string) { f.rangeChanged() })
	f.rangeInfo = kit.MonoText("", studiotheme.Faint, 11)

	f.audio = kit.NewCheck("Audio exportieren", func(on bool) {
		if !f.updating {
			f.audioWanted = on
		}
		f.changed()
	})

	f.errText = widget.NewLabel("")
	f.errText.Wrapping = fyne.TextWrapWord
	f.errText.Importance = widget.WarningImportance
	f.errText.SizeName = theme.SizeNameCaptionText
	warn := widget.NewIcon(theme.WarningIcon())
	f.errBox = container.NewBorder(nil, nil, container.NewVBox(warn), nil, f.errText)
	f.errBox.Hide()

	f.addBtn = widget.NewButtonWithIcon("Zur Render-Warteschlange hinzufügen", theme.ContentAddIcon(), func() { f.submit(false) })
	f.addBtn.Importance = widget.HighImportance
	f.renderBtn = widget.NewButtonWithIcon("Jetzt rendern", theme.MediaPlayIcon(), func() { f.submit(true) })

	sum := func() *canvas.Text { return kit.MonoText("", studiotheme.Text, 11) }
	f.sumPath, f.sumVideo, f.sumAudio, f.sumTime, f.sumFrames = sum(), sum(), sum(), sum(), sum()
	f.sumFramesRow = summaryRow("Bilder", f.sumFrames)
	f.sumFramesRow.Hide()

	// Auflösung and Qualität share one row; range and audio, rarely changed, sit in the
	// collapsed "Erweitert" section whose heading summarises them.
	formatRow := container.New(labelledColumns{ratio: 0.5},
		stacked("Format", f.format), kit.Labeled("Bildrate", f.rate.Select()))
	sizeRow := container.New(labelledColumns{ratio: 0.54},
		stacked("Auflösung", f.res), stacked("Qualität", f.quality.content))
	advanced := container.NewVBox(
		formRow("Bereich", f.rng),
		formRow("", f.rangeInfo),
		formRow("", f.audio),
	)
	f.advanced = newCollapsible("ERWEITERT", advanced, func() {
		if f.formBox != nil {
			f.formBox.Refresh()
		}
	})
	f.formBox = container.New(layout.NewCustomPaddedVBoxLayout(4),
		section("VOREINSTELLUNG"), presetList,
		section("DATEI"),
		formRow("Dateiname", nameRow),
		formRow("Ordner", folderRow),
		section("VIDEO"),
		formatRow,
		f.rateRow,
		sizeRow,
		f.customRow,
		f.evenNote,
		f.rateNote,
		f.advanced, advanced,
		section("AUSGABE"),
		container.New(layout.NewCustomPaddedVBoxLayout(1),
			summaryRow("Ziel", f.sumPath),
			summaryRow("Video", f.sumVideo),
			summaryRow("Audio", f.sumAudio),
			summaryRow("Dauer", f.sumTime),
			f.sumFramesRow),
	)
	f.scroll = container.NewVScroll(f.formBox)
	f.body = container.NewBorder(nil, container.NewVBox(f.errBox, f.addBtn, f.renderBtn), nil, nil, f.scroll)
	f.content = kit.Panel("Render-Einstellungen", nil, f.body)

	f.refreshProject()
	f.applyPreset(defaultPreset(f.presets))
	return f
}

// applyPreset fills the form from preset i.
func (f *settingsForm) applyPreset(i int) {
	o := f.presets[i].Options
	f.updating = true
	f.format.SetSelectedIndex(formatIndex(o.Format))
	f.selectSize(o.Width, o.Height)
	f.rate.Set(o.FPS, false)
	f.quality.Set(string(o.Quality))
	f.audioWanted = o.IncludeAudio || o.Format == export.FormatGIF || o.Format == export.FormatPNG
	f.updating = false
	f.formatChanged()
	f.changed()
}

// selectSize picks the matching resolution entry, or custom with the given size.
func (f *settingsForm) selectSize(w, h int) {
	f.lastW, f.lastH = w, h
	for i, r := range f.resolutions {
		if r.w == w && r.h == h {
			f.res.SetSelectedIndex(i)
			return
		}
	}
	f.size.Set(w, h)
	f.res.SetSelected(resCustom)
}

// refreshProject rebuilds the project-dependent choices (resolutions, range).
func (f *settingsForm) refreshProject() {
	pw, ph := f.pg.ctx.Session.Project.Settings.Width, f.pg.ctx.Session.Project.Settings.Height
	if f.lastW == 0 {
		f.lastW, f.lastH = pw, ph
	}
	f.resolutions = []resolution{{fmt.Sprintf("%s  %d × %d", resSame, pw, ph), pw, ph}}
	for _, pct := range []int{50, 25} {
		w, h := scaleSize(pw, ph, pct)
		f.resolutions = append(f.resolutions, resolution{fmt.Sprintf("%d %%  %d × %d", pct, w, h), w, h})
	}
	for _, h := range []int{2160, 1080, 720, 480} {
		w, hh := export.SizeForHeight(pw, ph, h)
		f.resolutions = append(f.resolutions, resolution{fmt.Sprintf("%dp  %d × %d", h, w, hh), w, hh})
	}
	f.resolutions = append(f.resolutions, resolution{label: resCustom})
	var labels []string
	for _, r := range f.resolutions {
		labels = append(labels, r.label)
	}
	sel := f.res.SelectedIndex()
	f.updating = true
	f.res.SetOptions(labels)
	if sel >= 0 && sel < len(labels) {
		f.res.SetSelectedIndex(sel)
	}
	f.updating = false
	f.rate.SetSameLabel(sameRateLabel(f.pg.ctx.Session.Project.Rate(), f.currentFormat()))
	f.refreshRange()
}

// refreshRange offers the In-Out range only while the session has one.
func (f *settingsForm) refreshRange() {
	s := f.pg.ctx.Session
	f.lastIn, f.lastOut = s.In, s.Out
	opts := []string{rangeAll}
	if s.HasRange() {
		opts = append(opts, rangeIO)
	}
	f.updating = true
	cur := f.rng.Selected
	if len(opts) != len(f.rng.Options) {
		f.rng.SetOptions(opts)
	}
	if cur == rangeIO && s.HasRange() {
		f.rng.SetSelected(rangeIO)
	} else {
		f.rng.SetSelected(rangeAll)
	}
	f.updating = false
	f.updateRangeInfo()
}

func (f *settingsForm) updateRangeInfo() {
	p := f.pg.ctx.Session.Project
	from, to := 0, p.Duration()
	if f.rng.Selected == rangeIO {
		from, to = f.pg.ctx.Session.In, f.pg.ctx.Session.Out
	}
	text := fmt.Sprintf("%s – %s", p.Timecode(from), p.Timecode(to))
	if to <= from {
		text = "Timeline ist leer"
	}
	if text != f.rangeInfo.Text {
		f.rangeInfo.Text = text
		f.rangeInfo.Refresh()
	}
}

// rangeMoved reports whether In or Out changed since the last refreshRange.
func (f *settingsForm) rangeMoved() bool {
	s := f.pg.ctx.Session
	return s.In != f.lastIn || s.Out != f.lastOut
}

// updateSummary describes what "Jetzt rendern" would produce.
func (f *settingsForm) updateSummary() {
	if f.sumPath == nil {
		return
	}
	f.updateNotes()
	set := func(t *canvas.Text, s string) {
		if t.Text != s {
			t.Text = s
			t.Refresh()
		}
	}
	if f.advanced != nil {
		f.advanced.setSummary(f.advancedSummary())
	}
	o, err := f.options()
	if err != nil {
		set(f.sumPath, "Angaben unvollständig")
		set(f.sumVideo, "–")
		set(f.sumAudio, "–")
		set(f.sumTime, "–")
		return
	}
	s := f.pg.ctx.Session
	n := frameCount(s.Project, o)
	set(f.sumPath, ellipsize(shortPath(o.Path), 40))
	shown := o // the size that is written; the note above explains a rounding
	shown.Width, shown.Height = export.EvenSize(o)
	set(f.sumVideo, videoSummary(shown, s.FPS()))
	set(f.sumAudio, audioSummary(o))
	set(f.sumTime, fmt.Sprintf("%s · %d Frames", s.Project.Timecode(n), n))
	conv := f.convertedFrames(s.Project, o, n)
	set(f.sumFrames, conv)
	setVisible(f.sumFramesRow, conv != "", f.formBox)
}

// convertedFrames is "90 Bilder bei 29,97 fps" when the export converts the frame rate,
// else "".
func (f *settingsForm) convertedFrames(p *model.Project, o export.Options, n int) string {
	out := export.OutputRate(o, p.Rate())
	if o.Format == export.FormatWAV || o.Format == export.FormatPNG || out.Equal(p.Rate()) {
		return ""
	}
	return fmt.Sprintf("%d Bilder bei %s", model.ConvertFrames(n, p.Rate(), out), out.Label())
}

// updateNotes shows the frame rate conversion and even-size notes of the current options.
func (f *settingsForm) updateNotes() {
	o, err := f.options()
	if err != nil {
		setNote(f.rateNote, "", f.formBox)
		setNote(f.evenNote, "", f.formBox)
		return
	}
	proj := f.pg.ctx.Session.Project.Rate()
	out := export.OutputRate(o, proj)
	switch {
	case o.Format == export.FormatWAV || o.Format == export.FormatPNG || out.Equal(proj):
		setNote(f.rateNote, "", f.formBox)
	default:
		setNote(f.rateNote, fmt.Sprintf("Bilder werden beim Export umgerechnet: %s → %s. Bilder werden ausgelassen oder wiederholt, nicht überblendet.",
			strings.TrimSuffix(proj.Label(), " fps"), out.Label()), f.formBox)
	}
	setNote(f.evenNote, export.Notes(o), f.formBox)
}

// advancedSummary describes the collapsed "Erweitert" settings, e.g.
// "Gesamte Timeline · mit Audio".
func (f *settingsForm) advancedSummary() string {
	rng := f.rng.Selected
	if rng == "" {
		rng = rangeAll
	}
	audio := "ohne Audio"
	if f.audio.Checked {
		audio = "mit Audio"
	}
	return rng + " · " + audio
}

func (f *settingsForm) currentFormat() export.Format {
	i := f.format.SelectedIndex()
	if i < 0 {
		return export.FormatMP4
	}
	return export.Formats()[i]
}

func formatIndex(ft export.Format) int {
	for i, x := range export.Formats() {
		if x == ft {
			return i
		}
	}
	return 0
}

// formatChanged adapts dependent controls: extension, size and audio availability.
func (f *settingsForm) formatChanged() {
	if f.updating {
		return
	}
	ft := f.currentFormat()
	f.ext.Text = export.Extension(ft)
	if ft == export.FormatPNG {
		f.ext.Text = "/"
	}
	f.ext.Refresh()
	f.updating = true
	switch ft {
	case export.FormatWAV:
		f.res.Disable()
		f.audio.SetChecked(true)
		f.audio.Disable()
	case export.FormatGIF, export.FormatPNG:
		f.res.Enable()
		f.audio.SetChecked(false)
		f.audio.Disable()
	default:
		f.res.Enable()
		f.audio.SetChecked(f.audioWanted)
		f.audio.Enable()
	}
	setVisible(f.customRow, ft != export.FormatWAV && f.res.Selected == resCustom, f.scroll)
	rateApplies := ft != export.FormatWAV && ft != export.FormatPNG
	f.rate.SetDisabled(!rateApplies)
	f.rate.SetSameLabel(sameRateLabel(f.pg.ctx.Session.Project.Rate(), ft))
	f.updating = false
	f.changed()
}

// rateChanged reacts to the frame rate picker.
func (f *settingsForm) rateChanged() {
	if f.updating {
		return
	}
	f.changed()
}

// sameRateLabel is the "no rate of its own" entry of the export frame rate chooser.
func sameRateLabel(project model.Rate, ft export.Format) string {
	if ft == export.FormatGIF && project.Float() > 15 {
		return "Wie Projekt (GIF höchstens 15 fps)"
	}
	return "Wie Projekt (" + project.Label() + ")"
}

// scaleSize is w × h scaled to pct percent, at least the smallest allowed size.
func scaleSize(w, h, pct int) (int, int) {
	sc := func(n int) int { return max(model.MinSize, (n*pct+50)/100) }
	return sc(w), sc(h)
}

// noteLabel is a wrapping caption under a form row.
func noteLabel(imp widget.Importance) *widget.Label {
	l := widget.NewLabel("")
	l.Wrapping = fyne.TextWrapWord
	l.Importance = imp
	l.SizeName = theme.SizeNameCaptionText
	l.Hide()
	return l
}

func setNote(l *widget.Label, text string, parent fyne.CanvasObject) {
	if l.Text != text {
		l.SetText(text)
	}
	if parent != nil && l.Visible() != (text != "") {
		setVisible(l, text != "", parent)
	}
}

func (f *settingsForm) resChanged() {
	if f.updating {
		return
	}
	i := f.res.SelectedIndex()
	if f.res.Selected == resCustom {
		// start from the size that was chosen before, so "Eigene…" edits it
		f.size.Set(f.lastW, f.lastH)
	} else if i >= 0 && i < len(f.resolutions) {
		f.lastW, f.lastH = f.resolutions[i].w, f.resolutions[i].h
	}
	setVisible(f.customRow, f.res.Selected == resCustom, f.scroll)
	f.changed()
}

func (f *settingsForm) rangeChanged() {
	if f.updating {
		return
	}
	f.updateRangeInfo()
	f.changed()
}

// changed reacts to any edit: clears the error and re-matches the preset highlight.
func (f *settingsForm) changed() {
	if f.updating {
		return
	}
	f.setError(nil)
	f.presetAt = -1
	if o, err := f.options(); err == nil {
		for i, ps := range f.presets {
			po := ps.Options
			if po.Format == o.Format && po.Quality == o.Quality && po.FPS.Equal(o.FPS) &&
				(o.Format == export.FormatWAV || po.Width == o.Width && po.Height == o.Height) &&
				(po.IncludeAudio == o.IncludeAudio || !audioApplies(o.Format)) {
				f.presetAt = i
				break
			}
		}
	}
	for i, r := range f.rows {
		r.SetSelected(i == f.presetAt)
	}
	if f.onChange != nil {
		f.onChange()
	} else {
		f.updateSummary()
	}
}

func audioApplies(ft export.Format) bool { return ft == export.FormatMP4 || ft == export.FormatWebM }

// options reads the form. Errors describe unreadable input; Validate checks the rest.
func (f *settingsForm) options() (export.Options, error) {
	s := f.pg.ctx.Session
	ft := f.currentFormat()
	o := export.Options{Format: ft, Quality: export.Quality(f.quality.value)}
	name := strings.TrimSpace(f.name.Text)
	name = strings.TrimSuffix(name, export.Extension(ft))
	if name == "" {
		return o, errors.New("Der Dateiname fehlt. Gib einen Namen für die Ausgabe an.")
	}
	if strings.ContainsAny(name, `/\`) {
		return o, errors.New("Der Dateiname darf keinen Schrägstrich enthalten. Wähle den Ordner über „Ordner“.")
	}
	dir := strings.TrimSpace(f.folder.Text)
	if dir == "" {
		return o, errors.New("Der Ordner fehlt. Wähle einen Zielordner.")
	}
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	o.Path = filepath.Join(dir, name+export.Extension(ft))
	if ft != export.FormatWAV {
		i := f.res.SelectedIndex()
		if i < 0 || i >= len(f.resolutions) {
			return o, errors.New("Wähle eine Auflösung.")
		}
		r := f.resolutions[i]
		if r.w == 0 {
			w, h, err := f.size.Value()
			if err != nil {
				return o, fmt.Errorf("Die eigene Auflösung ist ungültig. %w", err)
			}
			r.w, r.h = w, h
		}
		o.Width, o.Height = r.w, r.h
	}
	if ft != export.FormatWAV && ft != export.FormatPNG {
		r, _, same, err := f.rate.Value()
		if err != nil {
			return o, fmt.Errorf("Die Export-Bildrate ist ungültig. %w", err)
		}
		if !same && !r.Equal(s.FPS()) {
			o.FPS = r
		}
	}
	o.IncludeAudio = ft == export.FormatWAV || (audioApplies(ft) && f.audio.Checked)
	if f.rng.Selected == rangeIO && s.HasRange() {
		o.From, o.To = s.In, s.Out
	}
	return o, nil
}

// submit validates the form and adds a job; start renders it right away.
func (f *settingsForm) submit(start bool) {
	o, err := f.options()
	if err == nil {
		o.Path = f.pg.rq.uniquePath(o.Path)
		err = export.Validate(f.pg.ctx.Session.Project, o)
	}
	var off *export.OfflineError
	if errors.As(err, &off) && f.pg.ctx.Window != nil {
		f.setError(err)
		d := dialog.NewConfirm("Medien offline", err.Error(), func(ok bool) {
			if !ok {
				return
			}
			o.AllowOffline = true
			if err := export.Validate(f.pg.ctx.Session.Project, o); err != nil {
				f.setError(err)
				return
			}
			f.enqueue(o, start)
		}, f.pg.ctx.Window)
		d.SetConfirmText("Trotzdem exportieren")
		d.SetDismissText("Abbrechen")
		d.Show()
		return
	}
	if err != nil {
		f.setError(err)
		return
	}
	f.enqueue(o, start)
}

func (f *settingsForm) enqueue(o export.Options, start bool) {
	f.setError(nil)
	f.pg.rq.add(o, f.pg.ctx.Session.Project.Clone(), start)
	f.pg.updateSummary()
}

func (f *settingsForm) setError(err error) {
	if err != nil {
		f.errText.SetText(err.Error())
	}
	setVisible(f.errBox, err != nil, f.body)
}

func (f *settingsForm) chooseFolder() {
	w := f.pg.ctx.Window
	if w == nil {
		return
	}
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil {
			f.setError(fmt.Errorf("Der Ordner kann nicht geöffnet werden: %w", err))
			return
		}
		if u != nil {
			f.folder.SetText(u.Path())
		}
	}, w)
	if l, err := storage.ListerForURI(storage.NewFileURI(f.folder.Text)); err == nil {
		d.SetLocation(l)
	}
	d.Resize(fyne.NewSize(820, 560))
	d.Show()
}

// fileName turns a project name into a safe default file name.
func fileName(project string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(project))
	if name == "" {
		return "Export"
	}
	return name
}

// exportFolder is the folder from the settings (Export.Folder) or defaultFolder.
func exportFolder() string {
	if d := strings.TrimSpace(settings.Get().Export.Folder); d != "" {
		if strings.HasPrefix(d, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				d = filepath.Join(home, d[1:])
			}
		}
		return d
	}
	return defaultFolder()
}

// defaultPreset is the index of the preset named in the settings (Export.Preset), or 0.
func defaultPreset(presets []export.Preset) int {
	name := settings.Get().Export.Preset
	for i, ps := range presets {
		if ps.Name == name {
			return i
		}
	}
	return 0
}

// defaultFolder is ~/Videos when it exists, otherwise the home directory.
func defaultFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	for _, d := range []string{"Videos", "Video"} {
		if st, err := os.Stat(filepath.Join(home, d)); err == nil && st.IsDir() {
			return filepath.Join(home, d)
		}
	}
	return home
}
