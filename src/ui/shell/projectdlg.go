package shell

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

var projectBackColors = []model.RGBA{
	{R: 0, G: 0, B: 0, A: 255}, {R: 0x22, G: 0x23, B: 0x27, A: 255}, {R: 255, G: 255, B: 255, A: 255}, {R: 0x10, G: 0x1C, B: 0x30, A: 255},
}

// projectSettingsUI is the state of the "Projekteinstellungen" dialog.
type projectSettingsUI struct {
	sh *Shell
	p  *project
	d  dialog.Dialog

	picker   *kit.FormatPicker
	tpl      *templateOps
	swatches []*kit.Swatch
	summary  *widget.Label
	warn     *widget.Label
	warnBox  fyne.CanvasObject
	sumBox   *fyne.Container
	apply    *widget.Button

	base   model.Settings // the settings the dialog started from
	cur    kit.Format
	curErr error
	bg     model.RGBA
	sum    kit.ChangeSummary
}

// projectSettings opens the dialog for resolution, frame rate, drop-frame and background
// of the open project. Applying is one undoable edit.
func (sh *Shell) projectSettings() {
	if sh.active == nil {
		return
	}
	u := sh.buildProjectSettings(sh.active)
	sh.psd = u
	u.d.Show()
}

func (sh *Shell) buildProjectSettings(p *project) *projectSettingsUI {
	sess := p.sess
	st := sess.Project.Settings
	st.FPS = sess.Project.Rate()
	u := &projectSettingsUI{sh: sh, p: p, base: st, bg: st.Background,
		cur: kit.Format{Width: st.Width, Height: st.Height, FPS: st.FPS, DropFrame: st.DropFrame && st.FPS.CanDropFrame()}}
	u.summary = formatNote("", widget.LowImportance)
	u.warn = formatNote("", widget.WarningImportance)
	u.warnBox = container.NewBorder(nil, nil, container.NewVBox(widget.NewIcon(theme.WarningIcon())), nil, u.warn)
	u.warnBox.Hide()

	var match func() (kit.Format, error)
	if len(sess.Project.Media) > 0 {
		match = func() (kit.Format, error) {
			m, err := sess.Project.MatchFirstMedia()
			if err != nil {
				return kit.Format{}, err
			}
			return kit.Format{Width: m.Width, Height: m.Height, FPS: m.FPS, DropFrame: u.cur.DropFrame && m.FPS.CanDropFrame()}, nil
		}
	}
	u.picker = kit.NewFormatPicker(u.cur, kit.FormatOptions{MatchMedia: match}, func(f kit.Format, err error) {
		u.cur, u.curErr = f, err
		u.refresh()
	})

	u.picker.SetOnSubmitted(u.Apply)
	u.tpl = newTemplateOps(sh, newTemplateList(func(t Template) {
		u.cur, u.curErr = templateFormat(t), nil
		u.picker.Set(u.cur)
		u.refresh()
	}), func() (kit.Format, error) { return u.cur, u.curErr }, u.refresh)
	tplBox := container.NewVBox(formatLabel("Vorlage"), container.NewStack(sizeRect(0, 120), u.tpl.tl.list), u.tpl.buttons())

	row := container.NewHBox()
	for _, c := range projectBackColors {
		sw := kit.NewSwatch(c.NRGBA(), func() { u.pickBackground(c) })
		u.swatches = append(u.swatches, sw)
		row.Add(container.NewGridWrap(fyne.NewSize(24, 24), sw))
	}

	cancel := widget.NewButton("Abbrechen", func() { u.d.Hide() })
	u.apply = widget.NewButton("Anwenden", u.Apply)
	u.apply.Importance = widget.HighImportance

	// The dialog keeps its size while hints appear and vanish: the form scrolls in a fixed
	// frame and the summary has reserved room below it.
	form := container.NewVScroll(container.NewPadded(container.NewVBox(tplBox, u.picker.Object(), kit.Labeled("Hintergrundfarbe", row))))
	u.picker.Relayout = form.Refresh
	u.sumBox = container.NewVBox(u.summary, u.warnBox)
	summary := container.NewStack(sizeRect(0, 116), u.sumBox)
	body := container.NewStack(sizeRect(430, 600), container.NewBorder(nil, summary, nil, nil, form))
	d := dialog.NewCustomWithoutButtons("Projekteinstellungen", body, sh.win)
	d.SetButtons([]fyne.CanvasObject{cancel, u.apply})
	u.d = d
	u.pickBackground(u.bg)
	return u
}

func (u *projectSettingsUI) pickBackground(c model.RGBA) {
	u.bg = c
	for i, sw := range u.swatches {
		sw.SetSelected(projectBackColors[i] == c)
	}
	u.refresh()
}

// settings is the project settings the dialog would apply.
func (u *projectSettingsUI) settings() model.Settings {
	next := u.base
	next.Width, next.Height, next.FPS, next.DropFrame = u.cur.Width, u.cur.Height, u.cur.FPS, u.cur.DropFrame
	next.Background = u.bg
	return next
}

// refresh recomputes the summary of what applying would change.
func (u *projectSettingsUI) refresh() {
	if u.apply == nil {
		return
	}
	if u.tpl != nil {
		if u.curErr == nil {
			u.tpl.tl.selectMatching(u.cur)
		} else {
			u.tpl.tl.selectMatching(kit.Format{})
		}
		u.tpl.updateButtons()
	}
	if u.curErr != nil {
		u.sum = kit.ChangeSummary{}
		u.summary.SetText("")
		u.warnBox.Hide()
		u.apply.Disable()
		u.sumBox.Refresh()
		return
	}
	u.sum = kit.SummarizeChange(u.p.sess.Project, u.settings())
	u.summary.SetText(strings.Join(u.sum.Lines, "\n"))
	if u.sum.Warning != "" {
		u.warn.SetText(u.sum.Warning)
		u.warnBox.Show()
	} else {
		u.warnBox.Hide()
	}
	if u.sum.Changed {
		u.apply.Enable()
	} else {
		u.apply.Disable()
	}
	u.sumBox.Refresh()
}

// Apply changes the project in one undoable edit and closes the dialog.
func (u *projectSettingsUI) Apply() {
	if u.curErr != nil || !u.sum.Changed {
		return
	}
	next := u.settings()
	if err := u.p.sess.SetSettings(next); err != nil {
		u.sh.showErr(err)
		return
	}
	u.d.Hide()
	u.sh.toast.show("Projekteinstellungen", fmt.Sprintf("%d×%d · %s", next.Width, next.Height, next.FPS.Label()), false)
}
