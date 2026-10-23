package audio

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// nameLabel shows a track name; a double-click turns it into an entry. Enter or leaving
// the field commits, Escape cancels.
type nameLabel struct {
	widget.BaseWidget
	text     *canvas.Text
	entry    *renameEntry
	align    fyne.TextAlign
	onTap    func()
	onRename func(name string)
	fullName func() string // untruncated name for editing
	editing  bool
}

func newNameLabel(size float32, align fyne.TextAlign, tap func(), rename func(string)) *nameLabel {
	n := &nameLabel{onTap: tap, onRename: rename, align: align}
	n.text = canvas.NewText("", studiotheme.Text)
	n.text.TextSize = size
	n.text.TextStyle = fyne.TextStyle{Bold: true}
	n.text.Alignment = align
	n.entry = newRenameEntry(n)
	n.entry.Hide()
	n.ExtendBaseWidget(n)
	return n
}

// SetText sets the shown name (already fitted to the width by the caller).
func (n *nameLabel) SetText(s string) {
	if n.text.Text != s {
		n.text.Text = s
		n.text.Refresh()
	}
}

func (n *nameLabel) Tapped(*fyne.PointEvent) {
	if n.onTap != nil {
		n.onTap()
	}
}

func (n *nameLabel) DoubleTapped(*fyne.PointEvent) { n.startEdit() }

// startEdit opens the entry with the full name.
func (n *nameLabel) startEdit() {
	if n.editing || n.onRename == nil {
		return
	}
	full := n.text.Text
	if n.fullName != nil {
		full = n.fullName()
	}
	n.editing = true
	n.entry.SetText(full)
	n.entry.Show()
	n.text.Hide()
	n.Refresh()
	if c := fyne.CurrentApp().Driver().CanvasForObject(n); c != nil {
		c.Focus(n.entry)
	}
	n.entry.TypedShortcut(&fyne.ShortcutSelectAll{}) // typing replaces the name
}

// finish closes the entry and reports the new name if commit is set and it changed.
func (n *nameLabel) finish(commit bool) {
	if !n.editing {
		return
	}
	n.editing = false
	name := strings.TrimSpace(n.entry.Text)
	n.entry.Hide()
	n.text.Show()
	if c := fyne.CurrentApp().Driver().CanvasForObject(n); c != nil && c.Focused() == n.entry {
		c.Unfocus()
	}
	n.Refresh()
	if commit && name != "" && n.onRename != nil {
		n.onRename(name)
	}
}

func (n *nameLabel) MinSize() fyne.Size {
	return fyne.NewSize(10, n.text.MinSize().Height)
}

func (n *nameLabel) CreateRenderer() fyne.WidgetRenderer { return &nameRenderer{n: n} }

type nameRenderer struct{ n *nameLabel }

func (r *nameRenderer) Layout(s fyne.Size) {
	n := r.n
	n.text.Move(fyne.NewPos(0, 0))
	n.text.Resize(s)
	eh := n.entry.MinSize().Height
	n.entry.Move(fyne.NewPos(-4, (s.Height-eh)/2))
	n.entry.Resize(fyne.NewSize(s.Width+8, eh))
}

func (r *nameRenderer) MinSize() fyne.Size { return r.n.MinSize() }
func (r *nameRenderer) Refresh() {
	r.Layout(r.n.Size())
	r.n.text.Refresh()
}
func (r *nameRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.n.text, r.n.entry} }
func (r *nameRenderer) Destroy()                     {}

// renameEntry is an entry that reports Escape and focus loss to its label.
type renameEntry struct {
	widget.Entry
	label *nameLabel
}

func newRenameEntry(l *nameLabel) *renameEntry {
	e := &renameEntry{label: l}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { l.finish(true) }
	return e
}

func (e *renameEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.label.finish(false)
		return
	}
	e.Entry.TypedKey(k)
}

func (e *renameEntry) FocusLost() {
	e.Entry.FocusLost()
	e.label.finish(true)
}
