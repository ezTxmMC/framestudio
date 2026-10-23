package timeline

import (
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
)

// renameState is an inline rename of a track name in its header.
type renameState struct {
	track string
	entry *renameEntry
	done  bool
}

// renameEntry is a single-line entry that reports Esc and focus loss.
type renameEntry struct {
	widget.Entry
	onCancel func()
	onBlur   func()
}

func newRenameEntry() *renameEntry {
	e := &renameEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *renameEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && e.onCancel != nil {
		e.onCancel()
		return
	}
	e.Entry.TypedKey(k)
}

func (e *renameEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onBlur != nil {
		e.onBlur()
	}
}

// inNameArea reports whether pos lies on the name part of a track header (left of the
// switches), where a double click starts renaming.
func inNameArea(k model.TrackKind, r row, pos fyne.Position) bool {
	btns := headerButtons(k, r)
	return pos.X >= 4 && pos.X < btns[0].rect.X-2 && pos.Y >= r.y && pos.Y < r.y+r.h-borderPx
}

// startRename opens an inline entry over the header of track id.
func (t *Timeline) startRename(id string) {
	tr, _ := t.s.Project.Track(id)
	if tr == nil {
		return
	}
	if t.rename != nil {
		t.cancelRename()
	}
	e := newRenameEntry()
	e.SetText(tr.Name)
	st := &renameState{track: id, entry: e}
	e.OnSubmitted = func(string) { t.commitRename(st) }
	e.onCancel = func() { t.endRename(st) }
	e.onBlur = func() { t.commitRename(st) }
	t.rename = st
	t.s.SelectTrack(id)
	t.Refresh()
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(e)
	}
	e.TypedShortcut(&fyne.ShortcutSelectAll{}) // select the whole name, ready to type over
}

// commitRename applies the entered name as one undo step.
func (t *Timeline) commitRename(st *renameState) {
	if st.done {
		return
	}
	name := strings.TrimSpace(st.entry.Text)
	t.endRename(st)
	tr, _ := t.s.Project.Track(st.track)
	if tr == nil || name == tr.Name {
		return
	}
	if name == "" {
		t.fail(&editErr{msg: "Spur umbenennen nicht möglich: Der Name ist leer. Einen Namen eingeben, zum Beispiel „Musik“.", err: errors.New("leerer Name")})
		return
	}
	id := st.track
	t.edit("Spur umbenennen", func(p *model.Project) error {
		tr, _ := p.Track(id)
		if tr == nil {
			return model.ErrNotFound
		}
		tr.Name = name
		return nil
	})
}

func (t *Timeline) cancelRename() {
	if t.rename != nil {
		t.endRename(t.rename)
	}
}

// endRename removes the entry without applying it.
func (t *Timeline) endRename(st *renameState) {
	if st.done {
		return
	}
	st.done = true
	if t.rename == st {
		t.rename = nil
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil && c.Focused() == st.entry {
		c.Unfocus()
	}
	t.Refresh()
}
