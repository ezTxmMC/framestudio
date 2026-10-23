package timeline

import (
	"errors"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
)

// editErr is a rejected edit with a message that says what failed and how to fix it.
type editErr struct {
	msg string
	err error
}

func (e *editErr) Error() string { return e.msg }
func (e *editErr) Unwrap() error { return e.err }

// editError turns a model error into a user-facing German message; nil stays nil.
func editError(action string, err error) error {
	if err == nil {
		return nil
	}
	var hint string
	var lt *model.LockedTracksError
	switch {
	case errors.As(err, &lt):
		hint = lt.Error() + "."
	case errors.Is(err, model.ErrLocked):
		hint = "Die Spur ist gesperrt. Schloss im Spurkopf lösen und erneut versuchen."
	case errors.Is(err, model.ErrWrongTrack):
		hint = "Video-, Bild- und Titelclips gehören auf V-Spuren, Audioclips auf A-Spuren."
	case errors.Is(err, model.ErrInvalidEdit):
		hint = "Der Schnittpunkt muss innerhalb des Clips liegen und die Quelle lang genug sein."
	case errors.Is(err, model.ErrNotFound):
		hint = "Clip oder Spur existiert nicht mehr."
	default:
		hint = err.Error()
	}
	return &editErr{msg: fmt.Sprintf("%s nicht möglich: %s", action, hint), err: err}
}

func (t *Timeline) TappedSecondary(ev *fyne.PointEvent) {
	p := t.s.Project
	h := t.geom().hitTest(p, ev.Position)
	var menu *fyne.Menu
	switch {
	case h.area == areaLane && h.clip >= 0:
		c := p.Tracks[h.track].Clips[h.clip]
		if !t.s.IsSelected(c.ID) {
			t.s.SelectClip(c.ID)
		}
		menu = t.clipMenu(c)
	case h.area == areaHeader && h.track >= 0:
		t.s.SelectTrack(p.Tracks[h.track].ID)
		menu = t.trackMenu(p.Tracks[h.track])
	case h.area == areaLane || h.area == areaHeader:
		menu = fyne.NewMenu("", t.addTrackItems()...)
	default:
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		widget.ShowPopUpMenuAtPosition(menu, c, ev.AbsolutePosition)
	}
}

// edit runs one undoable edit and reports a failure through OnError.
func (t *Timeline) edit(label string, fn func(p *model.Project) error) {
	t.fail(editError(label, t.s.Edit(label, fn)))
}

func (t *Timeline) clipMenu(c model.Clip) *fyne.Menu {
	id := c.ID
	split := fyne.NewMenuItem("Teilen am Playhead", func() {
		ph := t.s.Playhead
		t.edit("Clip teilen", func(p *model.Project) error {
			_, err := p.SplitClip(id, ph)
			return err
		})
	})
	split.Disabled = !(t.s.Playhead > c.Start && t.s.Playhead < c.End())
	// delete acts on the whole selection when the clicked clip is part of it
	ids := []string{id}
	if t.s.IsSelected(id) && len(t.s.Selected) > 1 {
		ids = append([]string(nil), t.s.Selected...)
	}
	delLabel := "Clip löschen"
	if len(ids) > 1 {
		delLabel = fmt.Sprintf("%d Clips löschen", len(ids))
	}
	del := fyne.NewMenuItem("Löschen", func() {
		t.edit(delLabel, func(p *model.Project) error { return p.DeleteClips(ids, false) })
	})
	ripple := fyne.NewMenuItem("Lücke schließen", func() {
		t.edit("Löschen und Lücke schließen", func(p *model.Project) error { return p.DeleteClips(ids, true) })
	})
	if len(ids) > 1 {
		del.Label = fmt.Sprintf("%d Clips löschen", len(ids))
	}
	colors := fyne.NewMenuItem("Clipfarbe", nil)
	var items []*fyne.MenuItem
	for _, cc := range studiotheme.ClipColors {
		col := model.FromNRGBA(cc.Color)
		it := fyne.NewMenuItem(cc.Name, func() {
			t.edit("Clipfarbe ändern", func(p *model.Project) error {
				cur, _, _ := p.Clip(id)
				if cur == nil {
					return model.ErrNotFound
				}
				upd := *cur
				upd.Color = col
				return p.UpdateClip(upd)
			})
		})
		it.Icon = icon("swatch", cc.Color)
		it.Checked = c.Color == col
		items = append(items, it)
	}
	colors.ChildMenu = fyne.NewMenu("", items...)
	return fyne.NewMenu("", split, fyne.NewMenuItemSeparator(), del, ripple, fyne.NewMenuItemSeparator(), colors)
}

func (t *Timeline) trackMenu(tr model.Track) *fyne.Menu {
	id, name, n := tr.ID, tr.Name, len(tr.Clips)
	remove := fyne.NewMenuItem("Spur löschen", func() {
		do := func() { t.edit("Spur löschen", func(p *model.Project) error { return p.RemoveTrack(id) }) }
		if n == 0 {
			do()
			return
		}
		w := t.window()
		if w == nil {
			do() // no window to ask in; the step can be undone
			return
		}
		msg := fmt.Sprintf("Spur %s enthält %d Clips. Die Spur mit allen Clips löschen?", name, n)
		if n == 1 {
			msg = fmt.Sprintf("Spur %s enthält einen Clip. Die Spur mit dem Clip löschen?", name)
		}
		dialog.ShowConfirm("Spur löschen", msg, func(ok bool) {
			if ok {
				do()
			}
		}, w)
	})
	rename := fyne.NewMenuItem("Umbenennen", func() { t.startRename(id) })
	near := func(label string, above bool) *fyne.MenuItem {
		return fyne.NewMenuItem(label, func() {
			t.edit("Spur hinzufügen", func(p *model.Project) error {
				_, err := insertTrackNear(p, id, above)
				return err
			})
		})
	}
	items := []*fyne.MenuItem{
		rename, fyne.NewMenuItemSeparator(),
		near("Spur darüber hinzufügen", true), near("Spur darunter hinzufügen", false),
		remove, fyne.NewMenuItemSeparator(),
	}
	if _, ok := t.heights[id]; ok {
		items = append(items, fyne.NewMenuItem("Standardhöhe", func() {
			delete(t.heights, id)
			t.Refresh()
		}), fyne.NewMenuItemSeparator())
	}
	items = append(items, t.addTrackItems()...)
	return fyne.NewMenu("", items...)
}

func (t *Timeline) addTrackItems() []*fyne.MenuItem {
	add := func(label string, k model.TrackKind) *fyne.MenuItem {
		return fyne.NewMenuItem(label, func() {
			t.edit(label, func(p *model.Project) error {
				p.AddTrack(k)
				return nil
			})
		})
	}
	return []*fyne.MenuItem{add("Videospur hinzufügen", model.TrackVideo), add("Audiospur hinzufügen", model.TrackAudio)}
}

// window finds the window showing the timeline, for dialogs.
func (t *Timeline) window() fyne.Window {
	app := fyne.CurrentApp()
	if app == nil {
		return nil
	}
	c := app.Driver().CanvasForObject(t)
	for _, w := range app.Driver().AllWindows() {
		if w.Canvas() == c {
			return w
		}
	}
	return nil
}
