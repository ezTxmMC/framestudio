package shell

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

func templateFormat(t Template) kit.Format {
	return kit.Format{Width: t.Width, Height: t.Height, FPS: t.FPS, DropFrame: t.DropFrame && t.FPS.CanDropFrame()}
}

// templateMatching finds the index of the template with exactly format f, or -1. A user template wins over a
// built-in one with the same values.
func templateMatching(list []Template, f kit.Format) int {
	// Search from the end: user templates come after the built-in ones and win on equal values.
	for i := len(list) - 1; i >= 0; i-- {
		t := list[i]
		if t.Width == f.Width && t.Height == f.Height && t.FPS.Equal(f.FPS) && (t.DropFrame && t.FPS.CanDropFrame()) == f.DropFrame {
			return i
		}
	}
	return -1
}

// rememberFormat stores f as the format "Neues Projekt" proposes next time: the ID of the
// template with the same values, else the format itself.
func rememberFormat(f kit.Format) {
	_ = settings.Update(func(s *settings.Settings) {
		list := s.AllTemplates()
		if i := templateMatching(list, f); i >= 0 {
			s.General.DefaultTemplate = list[i].ID
			return
		}
		s.General.DefaultTemplate = settings.FormatID(f.Width, f.Height, f.FPS, f.DropFrame)
	})
}

// templateList is the list of templates of the new-project dialog.
type templateList struct {
	list     *widget.List
	items    []Template
	selected int
	onPick   func(Template)
	updating bool
}

func newTemplateList(onPick func(Template)) *templateList {
	tl := &templateList{selected: -1, onPick: onPick}
	tl.reload()
	tl.list = widget.NewList(
		func() int { return len(tl.items) },
		func() fyne.CanvasObject {
			name := canvas.NewText("Vorlage", studiotheme.Text)
			name.TextSize = 13
			spec := kit.MonoText("1920×1080 · 25 fps", studiotheme.Muted, 11)
			spec.Alignment = fyne.TextAlignTrailing
			return container.NewBorder(nil, nil, name, spec)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(tl.items) {
				return
			}
			t := tl.items[i]
			b := o.(*fyne.Container)
			name := b.Objects[0].(*canvas.Text)
			spec := b.Objects[1].(*canvas.Text)
			label := t.Name
			if t.User {
				label += "  (eigene)"
			}
			name.Text = label
			s := fmt.Sprintf("%d×%d · %s", t.Width, t.Height, strings.TrimSuffix(t.FPS.Label(), " fps"))
			if t.DropFrame && t.FPS.CanDropFrame() {
				s += " DF"
			}
			spec.Text = s + " fps "
			name.Refresh()
			spec.Refresh()
		})
	tl.list.OnSelected = func(i widget.ListItemID) {
		tl.selected = i
		if tl.updating || i >= len(tl.items) {
			return
		}
		if tl.onPick != nil {
			tl.onPick(tl.items[i])
		}
	}
	return tl
}

func (tl *templateList) reload() {
	tl.items = settings.Get().AllTemplates()
	if tl.list != nil {
		tl.list.Refresh()
	}
}

// selectMatching highlights the template that has format f, or nothing.
func (tl *templateList) selectMatching(f kit.Format) {
	tl.updating = true
	defer func() { tl.updating = false }()
	if i := templateMatching(tl.items, f); i >= 0 {
		tl.selected = i
		tl.list.Select(i)
		tl.list.ScrollTo(i)
		return
	}
	tl.selected = -1
	tl.list.UnselectAll()
}

func (tl *templateList) current() (Template, bool) {
	if tl.selected < 0 || tl.selected >= len(tl.items) {
		return Template{}, false
	}
	return tl.items[tl.selected], true
}

// formatNote is a short muted/warning line under a dialog section.
func formatNote(text string, imp widget.Importance) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = imp
	l.SizeName = theme.SizeNameCaptionText
	return l
}

// newProject is the state of the "Neues Projekt" dialog; the buttons call its methods, so
// tests drive the same code without tapping.
type newProject struct {
	sh     *Shell
	d      dialog.Dialog
	name   *widget.Entry
	picker *kit.FormatPicker
	status *widget.Label
	*templateOps

	create *widget.Button

	cur    kit.Format
	curErr error
}

// templateOps is the template list with its save, rename and delete buttons; the new-project
// and the project settings dialog both use it.
type templateOps struct {
	sh *Shell
	tl *templateList
	// cur is the format the dialog currently holds (or why it is invalid); refresh runs
	// after the templates changed.
	cur     func() (kit.Format, error)
	refresh func()

	saveTpl, renameBtn, deleteBtn *widget.Button
}

func newTemplateOps(sh *Shell, tl *templateList, cur func() (kit.Format, error), refresh func()) *templateOps {
	o := &templateOps{sh: sh, tl: tl, cur: cur, refresh: refresh}
	o.saveTpl = widget.NewButtonWithIcon("Als Vorlage speichern…", theme.DocumentSaveIcon(), func() {
		if _, err := o.cur(); err == nil {
			o.askName("Als Vorlage speichern", "Speichern", "", o.saveTemplate)
		}
	})
	o.renameBtn = widget.NewButtonWithIcon("Umbenennen…", theme.DocumentCreateIcon(), func() {
		if t, ok := o.tl.current(); ok && t.User {
			o.askName("Vorlage umbenennen", "Umbenennen", t.Name, o.renameTemplate)
		}
	})
	o.deleteBtn = widget.NewButtonWithIcon("Löschen", theme.DeleteIcon(), func() {
		t, ok := o.tl.current()
		if !ok || !t.User {
			return
		}
		dialog.NewConfirm("Vorlage löschen?", "Die Vorlage „"+t.Name+"“ wird gelöscht. Projekte, die sie nutzen, bleiben unverändert.", func(yes bool) {
			if yes {
				o.deleteTemplate()
			}
		}, sh.win).Show()
	})
	for _, b := range []*widget.Button{o.renameBtn, o.deleteBtn, o.saveTpl} {
		b.Importance = widget.LowImportance
	}
	return o
}

// newProjectDialog asks for name and format and opens the new project. The format comes
// from a template (built-in or the user's own) or from the picker for custom values.
func (sh *Shell) newProjectDialog() {
	n := sh.buildNewProject()
	sh.npd = n
	n.d.Show()
	sh.win.Canvas().Focus(n.name)
}

func (sh *Shell) buildNewProject() *newProject {
	st := settings.Get()
	start := st.TemplateOrDefault(st.General.DefaultTemplate)
	n := &newProject{sh: sh, cur: templateFormat(start)}

	n.name = widget.NewEntry()
	n.name.SetText(defaultProjectName())
	n.name.Validator = nonEmpty
	n.name.OnChanged = func(string) { n.refresh() }
	n.status = formatNote("", widget.LowImportance)

	n.picker = kit.NewFormatPicker(n.cur, kit.FormatOptions{}, func(f kit.Format, err error) {
		n.cur, n.curErr = f, err
		if err == nil {
			n.tl.selectMatching(f)
		} else {
			n.tl.selectMatching(kit.Format{})
		}
		n.refresh()
	})
	n.templateOps = newTemplateOps(sh, newTemplateList(func(t Template) {
		n.cur, n.curErr = templateFormat(t), nil
		n.picker.Set(n.cur)
		n.refresh()
	}), func() (kit.Format, error) { return n.cur, n.curErr }, n.refresh)
	n.name.OnSubmitted = func(string) { n.submit() }
	n.picker.SetOnSubmitted(n.submit)

	cancel := widget.NewButton("Abbrechen", func() { n.d.Hide() })
	n.create = widget.NewButton("Anlegen", n.submit)
	n.create.Importance = widget.HighImportance

	listBox := container.NewBorder(formatLabel("Vorlage"), n.buttons(), nil, nil, n.tl.list)
	left := container.NewStack(sizeRect(300, 380), listBox)
	pick := container.NewVScroll(container.NewPadded(n.picker.Object()))
	n.picker.Relayout = pick.Refresh
	right := container.NewStack(sizeRect(340, 0), pick)
	body := container.NewVBox(
		kit.Labeled("Name", n.name),
		container.NewBorder(nil, nil, left, nil, right),
		n.status,
	)
	d := dialog.NewCustomWithoutButtons("Neues Projekt", body, sh.win)
	d.SetButtons([]fyne.CanvasObject{cancel, n.create})
	n.d = d
	n.tl.selectMatching(n.cur)
	n.refresh()
	return n
}

// updateButtons enables the buttons for the current format and selection.
func (o *templateOps) updateButtons() {
	enable := func(b *widget.Button, on bool) {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	_, err := o.cur()
	enable(o.saveTpl, err == nil)
	t, isTpl := o.tl.current()
	enable(o.renameBtn, isTpl && t.User)
	enable(o.deleteBtn, isTpl && t.User)
}

func (o *templateOps) buttons() fyne.CanvasObject {
	return container.NewHBox(o.saveTpl, o.renameBtn, o.deleteBtn)
}

// refresh enables the buttons and shows what will be created.
func (n *newProject) refresh() {
	ok := n.curErr == nil && strings.TrimSpace(n.name.Text) != ""
	if ok {
		n.create.Enable()
	} else {
		n.create.Disable()
	}
	n.updateButtons()
	t, isTpl := n.tl.current()
	switch {
	case n.curErr != nil:
		n.status.SetText("")
	case isTpl:
		n.status.SetText("Vorlage „" + t.Name + "“")
	default:
		n.status.SetText("Eigene Werte: " + kit.SizeReadout(n.cur.Width, n.cur.Height))
	}
}

// submit creates the project and remembers the format as the next default.
func (n *newProject) submit() {
	name := strings.TrimSpace(n.name.Text)
	if n.curErr != nil || name == "" {
		return
	}
	f := n.cur
	n.d.Hide()
	rememberFormat(f)
	n.sh.newProject(name, Template{Name: "Eigene Werte", Width: f.Width, Height: f.Height, FPS: f.FPS, DropFrame: f.DropFrame})
}

// saveTemplate stores the current format as a user template.
func (o *templateOps) saveTemplate(name string) error {
	f, err0 := o.cur()
	if err0 != nil {
		return err0
	}
	var failed error
	err := settings.Update(func(s *settings.Settings) {
		_, failed = settings.AddTemplate(s, name, f.Width, f.Height, f.FPS, f.DropFrame)
	})
	if failed != nil {
		return failed
	}
	if err != nil {
		o.sh.showErr(errSettingsSave(err))
	}
	o.tl.reload()
	o.tl.selectMatching(f)
	o.refresh()
	return nil
}

// renameTemplate renames the selected user template.
func (o *templateOps) renameTemplate(name string) error {
	t, ok := o.tl.current()
	if !ok || !t.User {
		return errors.New("Es ist keine eigene Vorlage ausgewählt.")
	}
	var failed error
	err := settings.Update(func(s *settings.Settings) { failed = settings.RenameTemplate(s, t.ID, name) })
	if failed != nil {
		return failed
	}
	if err != nil {
		o.sh.showErr(errSettingsSave(err))
	}
	o.tl.reload()
	f, _ := o.cur()
	o.tl.selectMatching(f)
	o.refresh()
	return nil
}

// deleteTemplate deletes the selected user template.
func (o *templateOps) deleteTemplate() {
	t, ok := o.tl.current()
	if !ok || !t.User {
		return
	}
	if err := settings.Update(func(s *settings.Settings) { _ = settings.DeleteTemplate(s, t.ID) }); err != nil {
		o.sh.showErr(errSettingsSave(err))
	}
	o.tl.reload()
	f, _ := o.cur()
	o.tl.selectMatching(f)
	o.refresh()
}

// askName shows a small dialog for a template name; ok returns the error to show.
func (o *templateOps) askName(title, confirm, initial string, ok func(string) error) {
	entry := widget.NewEntry()
	entry.SetText(initial)
	entry.SetPlaceHolder("z. B. Kino 24p")
	errLabel := formatNote("", widget.WarningImportance)
	errLabel.Hide()
	var nd *dialog.CustomDialog
	submit := func() {
		if err := ok(entry.Text); err != nil {
			errLabel.SetText(err.Error())
			errLabel.Show()
			return
		}
		nd.Hide()
	}
	entry.OnSubmitted = func(string) { submit() }
	confirmBtn := widget.NewButton(confirm, submit)
	confirmBtn.Importance = widget.HighImportance
	nd = dialog.NewCustomWithoutButtons(title, container.NewGridWrap(fyne.NewSize(380, 0),
		container.NewVBox(widget.NewLabel("Name der Vorlage"), entry, errLabel)), o.sh.win)
	nd.SetButtons([]fyne.CanvasObject{widget.NewButton("Abbrechen", func() { nd.Hide() }), confirmBtn})
	nd.Show()
	o.sh.win.Canvas().Focus(entry)
}

func formatLabel(s string) fyne.CanvasObject {
	t := canvas.NewText(s, studiotheme.Muted)
	t.TextSize = 11
	return container.New(layout.NewCustomPaddedLayout(0, 4, 0, 0), t)
}

func sizeRect(w, h float32) *canvas.Rectangle {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}
