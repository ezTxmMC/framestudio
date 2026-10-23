package edit

import (
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
)

// markerColors are the marker colours offered in the edit dialog: the default blue
// first, then the clip label colours.
func markerColors() []model.RGBA {
	return append([]model.RGBA{model.FromNRGBA(studiotheme.Steel)}, labelColors()...)
}

func (p *Page) addMarker() {
	f := p.ctx.Session.Playhead
	p.edit("Marker setzen", func(pr *model.Project) error {
		pr.AddMarker(f, fmt.Sprintf("Marker %d", len(pr.Markers)+1))
		return nil
	})
}

// markerAtPlayhead returns the index of the marker on the playhead frame, or -1.
func (p *Page) markerAtPlayhead() int {
	s := p.ctx.Session
	for i, m := range s.Project.Markers {
		if m.Frame == s.Playhead {
			return i
		}
	}
	return -1
}

// markerFrom returns the index of the nearest marker after (dir > 0) or before the
// playhead, or -1.
func (p *Page) markerFrom(dir int) int {
	s := p.ctx.Session
	best := -1
	for i, m := range s.Project.Markers {
		switch {
		case dir > 0 && m.Frame > s.Playhead && (best < 0 || m.Frame < s.Project.Markers[best].Frame):
			best = i
		case dir < 0 && m.Frame < s.Playhead && (best < 0 || m.Frame > s.Project.Markers[best].Frame):
			best = i
		}
	}
	return best
}

func (p *Page) seekMarker(dir int) {
	if i := p.markerFrom(dir); i >= 0 {
		p.ctx.Session.Seek(p.ctx.Session.Project.Markers[i].Frame)
	}
}

func (p *Page) deleteMarker() {
	i := p.markerAtPlayhead()
	if i < 0 {
		return
	}
	id := p.ctx.Session.Project.Markers[i].ID
	p.edit("Marker löschen", func(pr *model.Project) error {
		pr.RemoveMarker(id)
		return nil
	})
}

// deleteAllMarkers asks first, then removes every marker in one undo step.
func (p *Page) deleteAllMarkers() {
	n := len(p.ctx.Session.Project.Markers)
	if n == 0 {
		return
	}
	do := func() {
		p.edit("Alle Marker löschen", func(pr *model.Project) error {
			pr.Markers = nil
			return nil
		})
	}
	if p.ctx.Window == nil {
		do()
		return
	}
	d := dialog.NewConfirm("Alle Marker löschen",
		fmt.Sprintf("%s aus dem Projekt löschen? Rückgängig stellt sie wieder her.", plural(n, "Marker", "Marker")),
		func(ok bool) {
			if ok {
				do()
			}
		}, p.ctx.Window)
	d.SetConfirmText("Löschen")
	d.SetDismissText("Abbrechen")
	p.dlg = d
	d.Show()
}

// editMarker opens a small dialog for the name and colour of the marker at the playhead.
func (p *Page) editMarker() {
	i := p.markerAtPlayhead()
	if i < 0 || p.ctx.Window == nil {
		return
	}
	m := p.ctx.Session.Project.Markers[i]
	name := widget.NewEntry()
	name.SetText(m.Name)
	col := m.Color
	sw := newSwatches(markerColors(), nil)
	pick := func(c model.RGBA) {
		col = c
		sw.set(c)
	}
	for k, chip := range sw.chips {
		c := sw.colors[k]
		chip.OnTapped = func() { pick(c) }
	}
	sw.set(col)
	note := newNote()
	at := text(p.ctx.Session.Project.Timecode(m.Frame), studiotheme.Muted, 12)
	at.TextStyle.Monospace = true
	form := container.NewVBox(
		field("Position", value(at)),
		field("Name", name),
		field("Farbe", sw.box),
		note.box,
		fixed(0, 8), // air above the buttons
	)
	id := m.ID
	var d *dialog.ConfirmDialog
	apply := func() bool {
		n := strings.TrimSpace(name.Text)
		if n == "" {
			note.show(errors.New("Der Name darf nicht leer sein"))
			return false
		}
		p.edit("Marker bearbeiten", func(pr *model.Project) error {
			for k := range pr.Markers {
				if pr.Markers[k].ID == id {
					pr.Markers[k].Name, pr.Markers[k].Color = n, col
					return nil
				}
			}
			return model.ErrNotFound
		})
		return true
	}
	d = dialog.NewCustomConfirm("Marker bearbeiten", "Übernehmen", "Abbrechen",
		container.NewThemeOverride(form, compactTheme{studiotheme.New()}),
		func(ok bool) {
			if ok && !apply() {
				d.Show() // keep the dialog open with the hint
			}
		}, p.ctx.Window)
	name.OnSubmitted = func(string) {
		if apply() {
			d.Hide()
		}
	}
	d.Resize(fyne.NewSize(420, d.MinSize().Height))
	p.dlg = d
	d.Show()
	p.ctx.Window.Canvas().Focus(name)
}

// renameClip asks for a new name for the selected clip.
func (p *Page) renameClip() {
	s := p.ctx.Session
	c, ok := s.Clip()
	if !ok || p.ctx.Window == nil {
		return
	}
	id := c.ID
	e := widget.NewEntry()
	e.SetText(c.Name)
	e.Validator = func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("Der Name darf nicht leer sein")
		}
		return nil
	}
	d := dialog.NewForm("Clip umbenennen", "Umbenennen", "Abbrechen",
		[]*widget.FormItem{widget.NewFormItem("Name", e)}, func(ok bool) {
			v := strings.TrimSpace(e.Text)
			if !ok || v == "" {
				return
			}
			p.edit("Clip umbenennen", func(pr *model.Project) error {
				cur, _, _ := pr.Clip(id)
				if cur == nil {
					return model.ErrNotFound
				}
				next := *cur
				next.Name = v
				return pr.UpdateClip(next)
			})
		}, p.ctx.Window)
	e.OnSubmitted = func(string) { d.Submit() }
	d.Resize(fyne.NewSize(380, d.MinSize().Height))
	p.dlg = d
	d.Show()
	p.ctx.Window.Canvas().Focus(e)
	e.TypedShortcut(&fyne.ShortcutSelectAll{})
}
