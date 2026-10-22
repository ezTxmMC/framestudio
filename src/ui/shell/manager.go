package shell

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// Template is a preset for new projects.
type Template struct {
	Label              string
	Width, Height, FPS int
}

// Templates are offered in the "Neues Projekt" dialog; the first is the default.
var Templates = []Template{
	{"1080p HD · 25 fps", 1920, 1080, 25},
	{"720p HD · 25 fps", 1280, 720, 25},
	{"4K UHD · 25 fps", 3840, 2160, 25},
	{"1080p HD · 30 fps", 1920, 1080, 30},
	{"1080p HD · 60 fps", 1920, 1080, 60},
	{"Hochkant 1080×1920 · 30 fps (Social)", 1080, 1920, 30},
	{"Quadrat 1080×1080 · 30 fps", 1080, 1080, 30},
}

// manager is the full-window project manager: library grid plus recent projects.
type manager struct {
	sh   *Shell
	view fyne.CanvasObject

	grid      *fyne.Container
	empty     fyne.CanvasObject
	recent    *fyne.Container
	count     *canvas.Text
	dirText   *canvas.Text
	back      *widget.Button
	actions   []*widget.Button
	cards     []*projectCard
	selected  string // path of the selected card
	dir       string
	thumbsGen int
}

func newManager(sh *Shell) *manager {
	m := &manager{sh: sh}

	brand := text("Frame Studio", studiotheme.Text, 14, true)
	title := text("Projektmanager", studiotheme.Muted, 13, false)
	m.back = widget.NewButtonWithIcon("Zurück zu den Projekten", theme.NavigateBackIcon(), sh.leaveManager)
	m.back.Importance = widget.LowImportance
	header := bar(44, false, container.NewBorder(nil, nil,
		container.NewHBox(strut(8, 0), container.NewCenter(brand), strut(4, 0), vline(18), strut(4, 0), container.NewCenter(title)),
		container.NewHBox(container.NewCenter(m.back), strut(6, 0))))

	m.grid = container.NewGridWrap(cardSize)
	emptyIcon := canvas.NewImageFromResource(theme.NewThemedResource(theme.FolderOpenIcon()))
	emptyIcon.SetMinSize(fyne.NewSquareSize(40))
	emptyIcon.Translucency = 0.6
	m.empty = container.NewCenter(container.NewVBox(
		container.NewCenter(emptyIcon),
		container.NewCenter(text("Noch keine Projekte in der Bibliothek", studiotheme.Text, 14, true)),
		container.NewCenter(text("Mit „Neues Projekt“ anlegen oder eine .fsproj-Datei öffnen.", studiotheme.Muted, 12, false)),
	))
	m.count = text("", studiotheme.Faint, 11, false)
	library := kit.Panel("Projektbibliothek", container.NewHBox(m.count, strut(6, 0)),
		container.NewStack(container.NewVScroll(container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), m.grid)), m.empty))

	m.recent = container.NewVBox()
	recent := container.NewStack(strut(330, 0), kit.Panel("Zuletzt geöffnet", nil, container.NewVScroll(m.recent)))

	body := container.New(layout.NewCustomPaddedLayout(6, 6, 6, 6),
		container.NewBorder(nil, nil, nil, container.NewHBox(strut(0, 0), recent), library))

	act := func(label string, icon fyne.Resource, fn func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, fn)
		m.actions = append(m.actions, b)
		return b
	}
	openSel := act("Öffnen", theme.FolderOpenIcon(), func() { m.openSelected() })
	rename := act("Umbenennen", theme.DocumentCreateIcon(), func() { m.renameSelected() })
	dup := act("Duplizieren", theme.ContentCopyIcon(), func() { m.duplicateSelected() })
	del := act("Löschen", theme.DeleteIcon(), func() { m.deleteSelected() })
	openFile := widget.NewButton("Datei öffnen…", sh.openDialog)
	newProj := widget.NewButtonWithIcon("Neues Projekt", theme.ContentAddIcon(), sh.newProjectDialog)
	newProj.Importance = widget.HighImportance

	folder := canvas.NewImageFromResource(theme.NewThemedResource(theme.FolderIcon()))
	folder.SetMinSize(fyne.NewSquareSize(14))
	folder.Translucency = 0.5
	m.dirText = kit.MonoText("", studiotheme.Faint, 11)
	footer := bar(52, true, container.NewBorder(nil, nil,
		container.NewHBox(strut(8, 0), container.NewCenter(folder), container.NewCenter(m.dirText)),
		container.NewHBox(container.NewCenter(openSel), container.NewCenter(rename), container.NewCenter(dup), container.NewCenter(del),
			strut(4, 0), vline(22), strut(4, 0),
			container.NewCenter(openFile), container.NewCenter(newProj), strut(6, 0))))

	bg := canvas.NewRectangle(studiotheme.Gap)
	m.view = container.NewStack(bg, container.NewBorder(header, footer, nil, nil, body))
	return m
}

// reload re-reads the library and the recent list.
func (m *manager) reload() {
	sh := m.sh
	if len(sh.projects) > 0 {
		m.back.Show()
	} else {
		m.back.Hide()
	}
	dir, err := store.ProjectsDir()
	var list []store.Details
	if err == nil {
		list, err = store.ListDetails(dir)
	}
	m.dir = dir
	m.dirText.Text = dir
	if err != nil {
		m.dirText.Text = err.Error()
	}
	m.dirText.Refresh()

	m.cards = m.cards[:0]
	objs := make([]fyne.CanvasObject, 0, len(list))
	for _, d := range list {
		c := newProjectCard(d, sh.findOpen(d.Path) != nil)
		path := d.Path
		c.onSelect = func() { m.selectPath(path) }
		c.onOpen = func() { m.open(path) }
		c.onMenu = func(at fyne.Position) { m.showMenu(path, at) }
		m.cards = append(m.cards, c)
		objs = append(objs, c)
	}
	m.grid.Objects = objs
	m.grid.Refresh()
	m.count.Text = countLabel(len(list))
	m.count.Refresh()
	if len(list) == 0 {
		m.empty.Show()
	} else {
		m.empty.Hide()
	}
	if m.card(m.selected) == nil {
		m.selected = ""
		if len(list) > 0 {
			m.selected = list[0].Path
		}
	}
	m.selectPath(m.selected)
	m.reloadRecent()
	if sh.thumbnails {
		m.loadThumbnails()
	}
}

func countLabel(n int) string {
	if n == 1 {
		return "1 Projekt"
	}
	return fmt.Sprintf("%d Projekte", n)
}

func (m *manager) reloadRecent() {
	m.recent.Objects = nil
	for _, path := range store.Recent() {
		d, err := store.Describe(path)
		if err != nil {
			continue
		}
		where := filepath.Dir(path)
		if m.dir != "" && samePath(where, m.dir) {
			where = "Bibliothek"
		} else if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(where, home) {
			where = "~" + strings.TrimPrefix(where, home)
		}
		name := text(ellipsize(d.Name, 32), studiotheme.Text, 12, true)
		date := text(d.Modified.Format("02.01. 15:04"), studiotheme.Muted, 11, false)
		loc := text(ellipsize(where, 44), studiotheme.Faint, 11, false)
		p := path
		m.recent.Add(newTapRow(container.NewVBox(container.NewBorder(nil, nil, nil, date, name), loc), func() { m.open(p) }))
	}
	if len(m.recent.Objects) == 0 {
		m.recent.Add(container.New(layout.NewCustomPaddedLayout(6, 6, 8, 8),
			text("Noch keine Projekte geöffnet.", studiotheme.Muted, 12, false)))
	}
	m.recent.Refresh()
}

func (m *manager) card(path string) *projectCard {
	for _, c := range m.cards {
		if c.d.Path == path {
			return c
		}
	}
	return nil
}

func (m *manager) selectPath(path string) {
	m.selected = path
	for _, c := range m.cards {
		if c.selected != (c.d.Path == path) {
			c.setSelected(c.d.Path == path)
		}
	}
	for _, b := range m.actions {
		setEnabled(b, path != "")
	}
}

// loadThumbnails renders a frame of every card's project in the background.
func (m *manager) loadThumbnails() {
	m.thumbsGen++
	gen := m.thumbsGen
	cards := slices.Clone(m.cards)
	r := m.sh.renderer
	go func() {
		for _, c := range cards {
			p, err := store.Load(c.d.Path)
			if err != nil || p.Duration() == 0 {
				continue
			}
			w, h := 384, 384*p.Settings.Height/p.Settings.Width
			if h > 216 {
				h, w = 216, 216*p.Settings.Width/p.Settings.Height
			}
			img := r.Frame(p, p.Duration()/3, max(w, 1), max(h, 1))
			fyne.Do(func() {
				if m.thumbsGen == gen {
					c.thumb.Image = image.Image(img)
					c.thumb.Refresh()
				}
			})
		}
	}()
}

func (m *manager) open(path string) {
	if err := m.sh.OpenFile(path); err != nil {
		m.sh.showErr(err)
		m.reload()
	}
}

func (m *manager) openSelected() {
	if m.selected != "" {
		m.open(m.selected)
	}
}

func (m *manager) showMenu(path string, at fyne.Position) {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Öffnen", func() { m.open(path) }),
		fyne.NewMenuItem("Umbenennen…", m.renameSelected),
		fyne.NewMenuItem("Duplizieren", m.duplicateSelected),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Löschen…", m.deleteSelected),
	)
	widget.ShowPopUpMenuAtPosition(menu, m.sh.win.Canvas(), at)
}

func (m *manager) renameSelected() {
	c := m.card(m.selected)
	if c == nil {
		return
	}
	entry := widget.NewEntry()
	entry.SetText(c.d.Name)
	entry.Validator = nonEmpty
	d := dialog.NewForm("Projekt umbenennen", "Umbenennen", "Abbrechen",
		[]*widget.FormItem{widget.NewFormItem("Name", entry)}, func(ok bool) {
			if ok {
				m.sh.showErr(m.rename(c.d.Path, strings.TrimSpace(entry.Text)))
				m.reload()
			}
		}, m.sh.win)
	d.Resize(fyne.NewSize(420, 160))
	d.Show()
	m.sh.win.Canvas().Focus(entry)
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("Namen eingeben")
	}
	return nil
}

// rename changes the project name; library files are renamed to match.
func (m *manager) rename(path, name string) error {
	if name == "" {
		return errors.New("Der Name darf nicht leer sein")
	}
	if p := m.sh.findOpen(path); p != nil {
		p.sess.Rename(name)
		return m.sh.Save(p)
	}
	proj, err := store.Load(path)
	if err != nil {
		return err
	}
	proj.Name = name
	proj.Modified = m.sh.now()
	newPath := path
	if m.dir != "" && samePath(filepath.Dir(path), m.dir) &&
		store.SafeName(name) != strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) {
		newPath = store.SuggestPath(m.dir, name)
	}
	if err := store.Save(proj, newPath); err != nil {
		return err
	}
	if newPath != path {
		if slices.Contains(store.Recent(), path) {
			store.RemoveRecent(path)
			store.AddRecent(newPath)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("Die alte Datei %s konnte nicht entfernt werden: %w", path, err)
		}
		m.selected = newPath
	}
	return nil
}

func (m *manager) duplicateSelected() {
	if m.selected == "" {
		return
	}
	path, err := m.duplicate(m.selected)
	if err != nil {
		m.sh.showErr(err)
		return
	}
	m.selected = path
	m.reload()
}

// duplicate copies the project under a new ID into the library.
func (m *manager) duplicate(path string) (string, error) {
	proj, err := store.Load(path)
	if err != nil {
		return "", err
	}
	if p := m.sh.findOpen(path); p != nil {
		proj = p.sess.Project.Clone()
	}
	if m.dir == "" {
		return "", errors.New("Der Projektordner ist nicht verfügbar")
	}
	now := m.sh.now()
	proj.ID = model.NewID()
	proj.Name += " Kopie"
	proj.Created, proj.Modified = now, now
	dst := store.SuggestPath(m.dir, proj.Name)
	return dst, store.Save(proj, dst)
}

func (m *manager) deleteSelected() {
	c := m.card(m.selected)
	if c == nil {
		return
	}
	if m.sh.findOpen(c.d.Path) != nil {
		m.sh.showErr(fmt.Errorf("„%s“ ist geöffnet. Erst den Projekt-Tab schließen, dann löschen", c.d.Name))
		return
	}
	msg := widget.NewLabel(fmt.Sprintf("„%s“ wird endgültig gelöscht.\nDie verwendeten Mediendateien bleiben erhalten.", c.d.Name))
	dialog.NewCustomConfirm("Projekt löschen?", "Löschen", "Abbrechen", msg, func(ok bool) {
		if !ok {
			return
		}
		if err := os.Remove(c.d.Path); err != nil {
			m.sh.showErr(fmt.Errorf("Löschen fehlgeschlagen: %w", err))
		}
		store.RemoveRecent(c.d.Path)
		m.selected = ""
		m.reload()
	}, m.sh.win).Show()
}

// typedKey handles keys while the manager is shown.
func (m *manager) typedKey(k *fyne.KeyEvent) {
	i := slices.IndexFunc(m.cards, func(c *projectCard) bool { return c.d.Path == m.selected })
	switch k.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		m.openSelected()
	case fyne.KeyDelete:
		m.deleteSelected()
	case fyne.KeyF2:
		m.renameSelected()
	case fyne.KeyEscape:
		m.sh.leaveManager()
	case fyne.KeyLeft, fyne.KeyRight:
		if len(m.cards) == 0 {
			return
		}
		if k.Name == fyne.KeyLeft {
			i--
		} else {
			i++
		}
		m.selectPath(m.cards[max(0, min(i, len(m.cards)-1))].d.Path)
	}
}

// newProjectDialog asks for name and preset and opens the new project.
func (sh *Shell) newProjectDialog() {
	name := widget.NewEntry()
	name.SetText(defaultProjectName())
	name.Validator = nonEmpty
	labels := make([]string, len(Templates))
	for i, t := range Templates {
		labels[i] = t.Label
	}
	preset := widget.NewSelect(labels, nil)
	preset.SetSelectedIndex(0)
	d := dialog.NewForm("Neues Projekt", "Anlegen", "Abbrechen", []*widget.FormItem{
		widget.NewFormItem("Name", name),
		widget.NewFormItem("Vorlage", preset),
	}, func(ok bool) {
		if ok {
			sh.newProject(strings.TrimSpace(name.Text), Templates[max(0, preset.SelectedIndex())])
		}
	}, sh.win)
	d.Resize(fyne.NewSize(480, 200))
	d.Show()
	sh.win.Canvas().Focus(name)
}

// defaultProjectName proposes a name that is not yet used in the library.
func defaultProjectName() string {
	name := "Projekt " + time.Now().Format("02.01.2006")
	if dir, err := store.ProjectsDir(); err == nil {
		return strings.TrimSuffix(filepath.Base(store.SuggestPath(dir, name)), store.Extension)
	}
	return name
}
