package media

import (
	"errors"
	"fmt"

	"fyne.io/fyne/v2/dialog"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
)

// formatFromMedia is the project settings that match m: its size, and its frame rate when
// it is a video with a valid rate. Background and the rest stay.
func formatFromMedia(p *model.Project, m model.MediaItem) (model.Settings, error) {
	st := p.Settings
	st.FPS = p.Rate()
	if m.Kind != model.KindVideo && m.Kind != model.KindImage || m.Width <= 0 || m.Height <= 0 {
		return st, errors.New("Dieses Medium hat keine Bildgröße. Ein Video oder Bild auswählen, dessen Format das Projekt übernehmen soll.")
	}
	st.Width, st.Height = m.Width, m.Height
	if m.Kind == model.KindVideo && m.FPS.Valid() {
		st.FPS = m.FPS.Reduced()
		if !st.FPS.CanDropFrame() {
			st.DropFrame = false
		}
	}
	if err := model.ValidateSettings(st); err != nil {
		return st, fmt.Errorf("„%s“ lässt sich nicht als Projektformat verwenden. %w", m.Name, err)
	}
	return st, nil
}

// useAsProjectFormat sets resolution and frame rate of the project from m after a
// confirmation. It is one undoable edit.
func (p *Page) useAsProjectFormat(m model.MediaItem) {
	proj := p.ctx.Session.Project
	st, err := formatFromMedia(proj, m)
	if err != nil {
		p.showErr(err)
		return
	}
	sum := kit.SummarizeChange(proj, st)
	if !sum.Changed {
		p.confirmFormat("Projektformat", "Das Projekt hat schon das Format von „"+m.Name+"“ ("+formatText(st)+").", false, nil)
		return
	}
	msg := fmt.Sprintf("Das Projekt wird auf das Format von „%s“ gestellt: %s (bisher %s).\n%s",
		m.Name, formatText(st), formatText(proj.Settings), sum.Text())
	p.confirmFormat("Als Projektformat verwenden", msg, true, func() {
		if err := p.ctx.Session.SetSettings(st); err != nil {
			p.showErr(err)
		}
	})
}

func formatText(st model.Settings) string {
	return fmt.Sprintf("%d × %d, %s", st.Width, st.Height, st.FPS.Label())
}

// confirmFormat shows a confirmation (or, without ask, just a message). Tests replace it.
func (p *Page) confirmFormat(title, msg string, ask bool, yes func()) {
	if p.formatDialog != nil {
		p.formatDialog(title, msg, ask, yes)
		return
	}
	if !ask {
		dialog.NewInformation(title, msg, p.ctx.Window).Show()
		return
	}
	d := dialog.NewConfirm(title, msg, func(ok bool) {
		if ok && !p.closed {
			yes()
		}
	}, p.ctx.Window)
	d.SetConfirmText("Übernehmen")
	d.SetDismissText("Abbrechen")
	d.Show()
}
