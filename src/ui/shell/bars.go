package shell

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	topBarHeight  = 34
	pageBarHeight = 52
)

// bars holds the top bar (tabs, status, undo) and the bottom page bar.
type bars struct {
	sh          *Shell
	top, bottom fyne.CanvasObject
	tabs        *fyne.Container
	status      *canvas.Text
	undo, redo  *widget.Button
	pages       []*pageButton
	format      *canvas.Text
	length      *canvas.Text
}

func newBars(sh *Shell) *bars {
	b := &bars{sh: sh, tabs: container.NewHBox()}

	brand := text("Frame Studio", studiotheme.Text, 13, true)
	left := container.NewHBox(strut(6, 0), container.NewCenter(brand), strut(6, 0), vline(18))
	plus := newIconTap(theme.ContentAddIcon(), 14, sh.ShowManager)
	center := container.NewHBox(b.tabs, container.NewCenter(plus))

	b.status = text("", studiotheme.Muted, 11, false)
	b.undo = kit.ToolButton(theme.ContentUndoIcon(), sh.undo)
	b.redo = kit.ToolButton(theme.ContentRedoIcon(), sh.redo)
	right := container.NewHBox(container.NewCenter(b.status), strut(10, 0), vline(18), strut(4, 0),
		container.NewCenter(b.undo), container.NewCenter(b.redo), strut(2, 0))

	b.top = bar(topBarHeight, false, container.NewBorder(nil, nil, left, right, center))

	row := container.NewHBox()
	for _, d := range sh.pageDefs {
		id := d.ID
		pb := newPageButton(id, d.Label, d.Icon, func() { sh.showPageID(id) })
		b.pages = append(b.pages, pb)
		row.Add(pb)
	}
	b.format = kit.MonoText("", studiotheme.Faint, 11)
	b.length = kit.MonoText("", studiotheme.Faint, 11)
	info := container.NewHBox(strut(10, 0), container.NewCenter(b.format), strut(14, 0), container.NewCenter(b.length))
	home := kit.ToolButton(theme.HomeIcon(), sh.ShowManager)
	ends := container.NewBorder(nil, nil, info, container.NewHBox(container.NewCenter(home), strut(4, 0)))
	b.bottom = bar(pageBarHeight, true, container.NewStack(ends, container.NewCenter(row)))
	return b
}

// bar is a thin graphite strip with a hairline towards the workspace.
func bar(height float32, lineOnTop bool, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(studiotheme.Lane)
	line := canvas.NewRectangle(studiotheme.Border)
	line.SetMinSize(fyne.NewSize(1, 1))
	body := container.NewStack(strut(0, height), content)
	if lineOnTop {
		return container.NewStack(bg, container.NewBorder(line, nil, nil, nil, body))
	}
	return container.NewStack(bg, container.NewBorder(nil, line, nil, nil, body))
}

func (b *bars) refresh() {
	sh := b.sh
	objs := make([]fyne.CanvasObject, 0, len(sh.projects))
	for _, p := range sh.projects {
		p.tab.set(p.sess.Project.Name, p.sess.Dirty, p == sh.active)
		objs = append(objs, p.tab)
	}
	b.tabs.Objects = objs

	p := sh.active
	b.status.Text = statusText(p, sh.now())
	if p == nil {
		b.undo.Disable()
		b.redo.Disable()
		b.format.Text, b.length.Text = "", ""
	} else {
		setEnabled(b.undo, p.sess.CanUndo())
		setEnabled(b.redo, p.sess.CanRedo())
		st := p.sess.Project.Settings
		b.format.Text = fmt.Sprintf("%d × %d  ·  %d fps", st.Width, st.Height, st.FPS)
		b.length.Text = "Dauer " + p.sess.Project.Timecode(p.sess.Project.Duration())
	}
	// Texts changed width: lay the bars out again.
	b.top.Refresh()
	b.bottom.Refresh()
	for _, pb := range b.pages {
		on := p != nil && pb.id == p.current
		if on != pb.active {
			pb.setActive(on)
		}
	}
}

func setEnabled(w fyne.Disableable, on bool) {
	if on && w.Disabled() {
		w.Enable()
	} else if !on && !w.Disabled() {
		w.Disable()
	}
}

// statusText describes the save state of p for the top bar.
func statusText(p *project, now time.Time) string {
	switch {
	case p == nil:
		return ""
	case p.saveErr != "":
		return p.saveErr
	case p.sess.Dirty:
		return "Ungespeichert"
	case p.savedAt.IsZero() && p.sess.Path == "":
		return "Noch nicht gespeichert"
	case p.savedAt.IsZero():
		return "Gespeichert"
	}
	stamp := p.savedAt.Format("15:04")
	if y, m, d := p.savedAt.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		stamp = p.savedAt.Format("02.01.2006 15:04")
	}
	if p.autosaved {
		return "Automatisch gespeichert " + stamp
	}
	return "Gespeichert " + stamp
}
