package shell

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/chrome"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/menubar"
	studiotheme "framestudio-proto/src/ui/theme"
	"framestudio-proto/src/ui/winchrome"
)

const (
	topBarHeight    = 32
	pageBarHeight   = 34
	statusBarHeight = 24
)

// menuOpener is the part of the menu bar the key routing uses.
type menuOpener interface {
	Open(i int)
	Close()
	IsOpen() bool
	HandleKey(*fyne.KeyEvent) bool
	HandleKeyDown(*fyne.KeyEvent) bool
	HandleKeyUp(*fyne.KeyEvent) bool
	HandleShortcut(fyne.Shortcut) bool
}

// bars holds the two top rows (menu bar with wordmark, which is the title bar in the
// custom chrome; project tabs with save status and undo/redo) and the bottom bar (page buttons and the status line with format and
// duration). Display.PageBar and Display.StatusBar switch the parts of the bottom bar.
type bars struct {
	sh          *Shell
	top, bottom *fyne.Container // project tabs; page bar and status line
	menu        *menubar.MenuBar
	project     *chrome.TitleText // active project name (right of the menu row, centred in the title bar)
	tabs        *fyne.Container
	status      *canvas.Text
	undo, redo  *widget.Button
	pages       []*pageButton
	format      *canvas.Text
	length      *canvas.Text

	pageRow, info, home fyne.CanvasObject
}

func newBars(sh *Shell) *bars {
	b := &bars{sh: sh, tabs: container.NewHBox()}

	// Row 1: wordmark and menu bar.
	b.menu = menubar.New(sh.win, sh.reg, command.MenuBar)
	b.menu.OnCombo = func(k fyne.KeyName, m fyne.KeyModifier) {
		if !sh.modal() { // the menu closed; another dialog may still be open
			sh.combo(k, m)
		}
	}
	menubar.ContextCombo = b.menu.OnCombo
	b.menu.SetHeight(chrome.BarHeight(false))
	sh.menuBar = b.menu
	b.project = chrome.NewTitleText(12)

	// Row 2: project tabs, save status, undo/redo.
	plus := newIconTap(theme.ContentAddIcon(), 14, sh.ShowManager)
	center := container.NewHBox(strut(4, 0), b.tabs, container.NewCenter(plus))
	b.status = text("", studiotheme.Muted, 11, false)
	b.undo = kit.ToolButton(theme.ContentUndoIcon(), func() { sh.reg.Run("edit.undo") })
	b.redo = kit.ToolButton(theme.ContentRedoIcon(), func() { sh.reg.Run("edit.redo") })
	right := container.NewHBox(container.NewCenter(b.status), strut(10, 0), vline(18), strut(4, 0),
		container.NewCenter(b.undo), container.NewCenter(b.redo), strut(2, 0))
	tabRow := bar(topBarHeight, studiotheme.Lane, false, container.NewBorder(nil, nil, nil, right, center))
	b.top = container.New(layout.NewCustomPaddedVBoxLayout(0), tabRow)

	// Bottom: page buttons, status line, project manager.
	row := container.NewHBox()
	for _, d := range sh.pageDefs {
		id := d.ID
		pb := newPageButton(id, d.Label, d.Icon, func() { sh.reg.Run("view.page." + id) })
		b.pages = append(b.pages, pb)
		row.Add(pb)
	}
	b.pageRow = container.NewCenter(row)
	b.format = kit.MonoText("", studiotheme.Faint, 11)
	b.length = kit.MonoText("", studiotheme.Faint, 11)
	b.info = container.NewHBox(strut(10, 0), container.NewCenter(b.format), strut(14, 0), container.NewCenter(b.length))
	b.home = container.NewHBox(container.NewCenter(kit.ToolButton(theme.HomeIcon(), sh.ShowManager)), strut(4, 0))
	b.bottom = container.NewStack()
	b.applyDisplay(settings.Get().Display)
	return b
}

// systemRow is the menu row below the system title bar: wordmark, menu bar and the
// project name at the right. It is the same menu bar widget the title bar holds in the
// custom chrome, so only one of the two is shown at a time. It uses the same surface
// (Panel) and rhythm as the custom title bar.
func (b *bars) systemRow() fyne.CanvasObject {
	b.project.SetColor(studiotheme.Muted)
	return bar(chrome.BarHeight(false), studiotheme.Panel, false, container.NewBorder(nil, nil,
		b.titleLead(), container.NewHBox(container.NewCenter(b.project), strut(12, 0)), nil))
}

// titleParts are the areas of the custom title bar: app glyph, wordmark, divider and menu
// bar after the window buttons, the project name centred on the window, nothing at the
// right.
func (b *bars) titleParts() winchrome.Parts {
	b.project.SetColor(studiotheme.Muted)
	return winchrome.Parts{Leading: b.titleLead(), Center: b.project}
}

// titleLead is [8 pad][glyph 16][8][wordmark][12][divider 1x14][8][menu titles]. Every
// child is centred in the bar height with the same helper (kit.CenterY), so the text
// baselines of wordmark, titles and project name match.
func (b *bars) titleLead() fyne.CanvasObject {
	word := text("Frame Studio", studiotheme.Text, 13, true)
	div := canvas.NewRectangle(studiotheme.MenuSep)
	div.SetMinSize(fyne.NewSize(1, 14))
	return container.New(&leadLayout{pad: leadPad}, newMarkGlyph(), word, div, b.menu)
}

// leadPad is the space before the app glyph: 8, or nothing after the mac traffic lights
// (their zone already ends 12 after the last light).
func leadPad() float32 {
	if winchrome.Style(settings.Get().Display) == chrome.StyleMac {
		return 0
	}
	return 8
}

// leadLayout places glyph, wordmark, divider and menu bar (in this order) left to right.
type leadLayout struct{ pad func() float32 }

const (
	glyphSize = 16
	gapGlyph  = 8
	gapWord   = 12
	gapDiv    = 8
)

func (l *leadLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) != 4 {
		return
	}
	s := kit.ScaleOf(objs[0])
	x := l.pad()
	put := func(o fyne.CanvasObject, w float32) {
		h := o.MinSize().Height
		if h > size.Height {
			h = size.Height
		}
		o.Resize(fyne.NewSize(w, h))
		o.Move(fyne.NewPos(kit.Snap(x, s), kit.CenterY(size.Height, h, s)))
		x += w
	}
	put(objs[0], glyphSize)
	x += gapGlyph
	put(objs[1], objs[1].MinSize().Width)
	x += gapWord
	put(objs[2], 1)
	x += gapDiv
	put(objs[3], objs[3].MinSize().Width)
}

func (l *leadLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) != 4 {
		return fyne.Size{}
	}
	w := l.pad() + glyphSize + gapGlyph + objs[1].MinSize().Width + gapWord + 1 + gapDiv + objs[3].MinSize().Width
	return fyne.NewSize(w, chrome.BarHeight(false))
}

// markGlyph is the wordmark's frame glyph. It reads the accent when painting, so a
// changed accent (Einstellungen › Darstellung) shows without a restart.
type markGlyph struct{ widget.BaseWidget }

func newMarkGlyph() *markGlyph {
	g := &markGlyph{}
	g.ExtendBaseWidget(g)
	return g
}

func (g *markGlyph) MinSize() fyne.Size { return fyne.NewSize(glyphSize, glyphSize) }

func (g *markGlyph) CreateRenderer() fyne.WidgetRenderer {
	frame := canvas.NewRectangle(color.Transparent)
	frame.StrokeWidth = 2
	frame.CornerRadius = 2
	dot := canvas.NewRectangle(color.Transparent)
	dot.CornerRadius = 1
	r := &markRenderer{frame: frame, dot: dot}
	r.Refresh()
	return r
}

type markRenderer struct{ frame, dot *canvas.Rectangle }

func (r *markRenderer) Layout(s fyne.Size) {
	fh := s.Height * 12 / 16 // frame 16x12 in the 16x16 box
	r.frame.Move(fyne.NewPos(0, (s.Height-fh)/2))
	r.frame.Resize(fyne.NewSize(s.Width, fh))
	r.dot.Move(fyne.NewPos((s.Width-5)/2, (s.Height-4)/2))
	r.dot.Resize(fyne.NewSize(5, 4))
}
func (r *markRenderer) MinSize() fyne.Size { return fyne.NewSize(glyphSize, glyphSize) }
func (r *markRenderer) Refresh() {
	r.frame.StrokeColor, r.dot.FillColor = studiotheme.Steel, studiotheme.Steel
	r.frame.Refresh()
	r.dot.Refresh()
}
func (r *markRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.frame, r.dot} }
func (r *markRenderer) Destroy()                     {}

// applyDisplay shows the parts of the bottom bar the settings ask for: page buttons
// (Display.PageBar) and the status line (Display.StatusBar); with neither it is hidden.
func (b *bars) applyDisplay(d settings.Display) {
	var content fyne.CanvasObject
	switch {
	case d.PageBar:
		var info fyne.CanvasObject = strut(0, 0)
		if d.StatusBar {
			info = b.info
		}
		content = bar(pageBarHeight, studiotheme.Lane, true,
			container.NewStack(container.NewBorder(nil, nil, info, b.home), b.pageRow))
	case d.StatusBar:
		content = bar(statusBarHeight, studiotheme.Lane, true, container.NewBorder(nil, nil, b.info, nil))
	}
	if content == nil {
		b.bottom.Objects = nil
		b.bottom.Hide()
	} else {
		b.bottom.Objects = []fyne.CanvasObject{content}
		b.bottom.Show()
	}
	b.bottom.Refresh()
	if f := b.sh.frame; f != nil {
		f.Refresh() // the bottom bar changed height: lay the frame out again
	}
	if t := b.sh.toast; t != nil {
		t.setBottom(bottomHeight(d) + 14)
	}
}

// bottomHeight is the height of the bottom bar for d.
func bottomHeight(d settings.Display) float32 {
	switch {
	case d.PageBar:
		return pageBarHeight
	case d.StatusBar:
		return statusBarHeight
	}
	return 0
}

// bar is a thin graphite strip with a hairline towards the workspace.
func bar(height float32, fill color.Color, lineOnTop bool, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(fill)
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
	name := ""
	if p != nil && !sh.showManager {
		name = p.sess.Project.Name // the title shortens itself in the middle
	}
	b.project.SetText(name)
	if p == nil {
		b.undo.Disable()
		b.redo.Disable()
		b.format.Text, b.length.Text = "", ""
	} else {
		setEnabled(b.undo, p.sess.CanUndo())
		setEnabled(b.redo, p.sess.CanRedo())
		st := p.sess.Project.Settings
		b.format.Text = fmt.Sprintf("%d × %d  ·  %s", st.Width, st.Height, st.FPS.Label())
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
