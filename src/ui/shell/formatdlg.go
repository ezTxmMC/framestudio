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

// templateMatching finds the index of the template with exactly format f, or -1.
func templateMatching(list []Template, f kit.Format) int {
	for i, t := range list {
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
	tl     *templateList
	status *widget.Label

	create, saveTpl, renameBtn, deleteBtn *widget.Button

	cur    kit.Format
	curErr error
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
	n.tl = newTemplateList(func(t Template) {
		n.cur, n.curErr = templateFormat(t), nil
		n.picker.Set(n.cur)
		n.refresh()
	})

	n.saveTpl = widget.NewButtonWithIcon("Als Vorlage speichern…", theme.DocumentSaveIcon(), func() {
		if n.curErr == nil {
			n.askName("Als Vorlage speichern", "Speichern", "", n.saveTemplate)
		}
	})
	n.renameBtn = widget.NewButtonWithIcon("Umbenennen…", theme.DocumentCreateIcon(), func() {
		if t, ok := n.tl.current(); ok && t.User {
			n.askName("Vorlage umbenennen", "Umbenennen", t.Name, n.renameTemplate)
		}
	})
	n.deleteBtn = widget.NewButtonWithIcon("Löschen", theme.DeleteIcon(), func() {
		t, ok := n.tl.current()
		if !ok || !t.User {
			return
		}
		dialog.NewConfirm("Vorlage löschen?", "Die Vorlage „"+t.Name+"“ wird gelöscht. Projekte, die sie nutzen, bleiben unverändert.", func(yes bool) {
			if yes {
				n.deleteTemplate()
			}
		}, sh.win).Show()
	})
	for _, b := range []*widget.Button{n.renameBtn, n.deleteBtn, n.saveTpl} {
		b.Importance = widget.LowImportance
	}

	cancel := widget.NewButton("Abbrechen", func() { n.d.Hide() })
	n.create = widget.NewButton("Anlegen", n.submit)
	n.create.Importance = widget.HighImportance

	listBox := container.NewBorder(formatLabel("Vorlage"), container.NewHBox(n.saveTpl, n.renameBtn, n.deleteBtn), nil, nil, n.tl.list)
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

// refresh enables the buttons and shows what will be created.
func (n *newProject) refresh() {
	ok := n.curErr == nil && strings.TrimSpace(n.name.Text) != ""
	enable := func(b *widget.Button, on bool) {
		if on {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	enable(n.create, ok)
	enable(n.saveTpl, n.curErr == nil)
	t, isTpl := n.tl.current()
	enable(n.renameBtn, isTpl && t.User)
	enable(n.deleteBtn, isTpl && t.User)
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
func (n *newProject) saveTemplate(name string) error {
	if n.curErr != nil {
		return n.curErr
	}
	f := n.cur
	var failed error
	err := settings.Update(func(s *settings.Settings) {
		_, failed = settings.AddTemplate(s, name, f.Width, f.Height, f.FPS, f.DropFrame)
	})
	if failed != nil {
		return failed
	}
	if err != nil {
		n.sh.showErr(errSettingsSave(err))
	}
	n.tl.reload()
	n.tl.selectMatching(f)
	n.refresh()
	return nil
}

// renameTemplate renames the selected user template.
func (n *newProject) renameTemplate(name string) error {
	t, ok := n.tl.current()
	if !ok || !t.User {
		return errors.New("Es ist keine eigene Vorlage ausgewählt.")
	}
	var failed error
	err := settings.Update(func(s *settings.Settings) { failed = settings.RenameTemplate(s, t.ID, name) })
	if failed != nil {
		return failed
	}
	if err != nil {
		n.sh.showErr(errSettingsSave(err))
	}
	n.tl.reload()
	n.tl.selectMatching(n.cur)
	n.refresh()
	return nil
}

// deleteTemplate deletes the selected user template.
func (n *newProject) deleteTemplate() {
	t, ok := n.tl.current()
	if !ok || !t.User {
		return
	}
	if err := settings.Update(func(s *settings.Settings) { _ = settings.DeleteTemplate(s, t.ID) }); err != nil {
		n.sh.showErr(errSettingsSave(err))
	}
	n.tl.reload()
	n.tl.selectMatching(n.cur)
	n.refresh()
}

// askName shows a small dialog for a template name; ok returns the error to show.
func (n *newProject) askName(title, confirm, initial string, ok func(string) error) {
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
		container.NewVBox(widget.NewLabel("Name der Vorlage"), entry, errLabel)), n.sh.win)
	nd.SetButtons([]fyne.CanvasObject{widget.NewButton("Abbrechen", func() { nd.Hide() }), confirmBtn})
	nd.Show()
	n.sh.win.Canvas().Focus(entry)
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
