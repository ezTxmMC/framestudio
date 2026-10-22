package shell

import (
	"errors"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

const ctrl = fyne.KeyModifierShortcutDefault

func key(k fyne.KeyName, mod fyne.KeyModifier) *desktop.CustomShortcut {
	return &desktop.CustomShortcut{KeyName: k, Modifier: mod}
}

var pageKeys = []fyne.KeyName{fyne.Key1, fyne.Key2, fyne.Key3, fyne.Key4, fyne.Key5}

// menus is the main menu; items are kept to update labels and enabled state.
type menus struct {
	sh   *Shell
	main *fyne.MainMenu

	save, saveAs, closeP, undo, redo, play, loop *fyne.MenuItem
	pages                                        []*fyne.MenuItem
	state                                        string
}

func newMenus(sh *Shell) *menus {
	m := &menus{sh: sh}
	item := func(label string, sc fyne.Shortcut, fn func()) *fyne.MenuItem {
		it := fyne.NewMenuItem(label, fn)
		it.Shortcut = sc
		return it
	}
	m.save = item("Speichern", key(fyne.KeyS, ctrl), sh.saveActive)
	m.saveAs = item("Speichern unter…", key(fyne.KeyS, ctrl|fyne.KeyModifierShift), sh.saveAs)
	m.closeP = item("Projekt schließen", key(fyne.KeyW, ctrl), sh.closeActive)
	quit := fyne.NewMenuItem("Beenden", sh.Quit)
	quit.IsQuit = true
	file := fyne.NewMenu("Datei",
		item("Neues Projekt…", key(fyne.KeyN, ctrl), sh.newProjectDialog),
		item("Projekt öffnen…", key(fyne.KeyO, ctrl), sh.openDialog),
		fyne.NewMenuItem("Projektmanager", sh.ShowManager),
		fyne.NewMenuItemSeparator(),
		m.save, m.saveAs,
		fyne.NewMenuItemSeparator(),
		m.closeP,
		fyne.NewMenuItemSeparator(),
		quit,
	)

	m.undo = item("Rückgängig", &fyne.ShortcutUndo{}, sh.undo)
	m.redo = item("Wiederholen", key(fyne.KeyZ, ctrl|fyne.KeyModifierShift), sh.redo)
	edit := fyne.NewMenu("Bearbeiten", m.undo, m.redo)

	view := fyne.NewMenu("Ansicht")
	for i, d := range sh.pageDefs {
		id := d.ID
		it := item(d.Label, key(pageKeys[i], fyne.KeyModifierShift), func() { sh.showPageID(id) })
		m.pages = append(m.pages, it)
		view.Items = append(view.Items, it)
	}

	m.play = item("Abspielen", key(fyne.KeySpace, 0), sh.togglePlay)
	m.loop = fyne.NewMenuItem("Schleife", sh.toggleLoop)
	playback := fyne.NewMenu("Wiedergabe", m.play, m.loop)

	help := fyne.NewMenu("Hilfe",
		fyne.NewMenuItem("Tastenkürzel", sh.showShortcuts),
		fyne.NewMenuItem("Über Frame Studio", sh.showAbout),
	)
	m.main = fyne.NewMainMenu(file, edit, view, playback, help)
	sh.win.SetMainMenu(m.main)
	return m
}

// refresh updates labels and enabled states; the menu is only rebuilt when they changed.
func (m *menus) refresh() {
	sh := m.sh
	p := sh.active
	open := p != nil && !sh.showManager
	m.undo.Label, m.redo.Label = "Rückgängig", "Wiederholen"
	m.play.Label = "Abspielen"
	m.loop.Checked = false
	if open {
		if l := p.sess.UndoLabel(); l != "" {
			m.undo.Label += ": " + l
		}
		if l := p.sess.RedoLabel(); l != "" {
			m.redo.Label += ": " + l
		}
		if p.sess.Playing {
			m.play.Label = "Pause"
		}
		m.loop.Checked = p.sess.Loop
	}
	m.undo.Disabled = !open || !p.sess.CanUndo()
	m.redo.Disabled = !open || !p.sess.CanRedo()
	for _, it := range []*fyne.MenuItem{m.save, m.saveAs, m.closeP, m.play, m.loop} {
		it.Disabled = !open
	}
	for i, it := range m.pages {
		it.Disabled = !open
		it.Checked = open && p.current == sh.pageDefs[i].ID
	}

	var b strings.Builder
	for _, menu := range m.main.Items {
		for _, it := range menu.Items {
			b.WriteString(it.Label)
			if it.Disabled {
				b.WriteByte('-')
			}
			if it.Checked {
				b.WriteByte('+')
			}
			b.WriteByte('|')
		}
	}
	if s := b.String(); s != m.state {
		m.state = s
		m.main.Refresh()
	}
}

// installKeys registers window shortcuts and routes plain keys to the active page.
func (sh *Shell) installKeys() {
	c := sh.win.Canvas()
	add := func(sc fyne.Shortcut, fn func()) { c.AddShortcut(sc, func(fyne.Shortcut) { fn() }) }
	add(key(fyne.KeyN, ctrl), sh.newProjectDialog)
	add(key(fyne.KeyO, ctrl), sh.openDialog)
	add(key(fyne.KeyS, ctrl), sh.saveActive)
	add(key(fyne.KeyS, ctrl|fyne.KeyModifierShift), sh.saveAs)
	add(key(fyne.KeyW, ctrl), sh.closeActive)
	add(&fyne.ShortcutUndo{}, sh.undo)
	add(&fyne.ShortcutRedo{}, sh.redo)
	add(key(fyne.KeyZ, ctrl), sh.undo)
	add(key(fyne.KeyY, ctrl), sh.redo)
	add(key(fyne.KeyZ, ctrl|fyne.KeyModifierShift), sh.redo)
	for i, d := range sh.pageDefs {
		id := d.ID
		add(key(pageKeys[i], fyne.KeyModifierShift), func() { sh.showPageID(id) })
	}
	c.SetOnTypedKey(sh.typedKey)
	if dc, ok := c.(desktop.Canvas); ok {
		dc.SetOnKeyDown(func(e *fyne.KeyEvent) { sh.trackShift(e.Name, true) })
		dc.SetOnKeyUp(func(e *fyne.KeyEvent) { sh.trackShift(e.Name, false) })
	}
}

// trackShift remembers the Shift state: Shift+digit is no shortcut for the driver, so
// page switching (Shift+1..5) is detected in typedKey.
func (sh *Shell) trackShift(k fyne.KeyName, down bool) {
	if k == desktop.KeyShiftLeft || k == desktop.KeyShiftRight {
		sh.shift = down
	}
}

// typedKey handles keys while no widget has focus.
func (sh *Shell) typedKey(k *fyne.KeyEvent) {
	if sh.showManager {
		sh.mgr.typedKey(k)
		return
	}
	p := sh.active
	if p == nil {
		return
	}
	if sh.shift {
		for i, pk := range pageKeys {
			if k.Name == pk && i < len(sh.pageDefs) {
				sh.showPage(p, sh.pageDefs[i].ID)
				return
			}
		}
	}
	if pg := p.pages[p.current]; pg != nil && pg.TypedKey(k) {
		return
	}
	if k.Name == fyne.KeySpace {
		p.sess.TogglePlaying()
	}
}

func (sh *Shell) workspaceProject() *project {
	if sh.showManager {
		return nil
	}
	return sh.active
}

func (sh *Shell) undo() {
	if p := sh.workspaceProject(); p != nil {
		p.sess.Undo()
	}
}

func (sh *Shell) redo() {
	if p := sh.workspaceProject(); p != nil {
		p.sess.Redo()
	}
}

func (sh *Shell) togglePlay() {
	if p := sh.workspaceProject(); p != nil {
		p.sess.TogglePlaying()
	}
}

func (sh *Shell) toggleLoop() {
	if p := sh.workspaceProject(); p != nil {
		p.sess.SetLoop(!p.sess.Loop)
	}
}

// dropped opens dropped project files and hands other files to the active page, or to
// the media page.
func (sh *Shell) dropped(uris []fyne.URI) {
	var files []fyne.URI
	for _, u := range uris {
		if strings.EqualFold(filepath.Ext(u.Path()), store.Extension) {
			sh.showErr(sh.OpenFile(u.Path()))
		} else {
			files = append(files, u)
		}
	}
	if len(files) == 0 {
		return
	}
	p := sh.active
	if p == nil {
		sh.showErr(errors.New("Medien lassen sich erst importieren, wenn ein Projekt geöffnet ist. Projekt anlegen oder öffnen und die Dateien erneut hineinziehen."))
		return
	}
	if !sh.showManager {
		if dt, ok := p.pages[p.current].(page.DropTarget); ok {
			dt.Dropped(files)
			return
		}
	}
	dt, ok := sh.ensurePage(p, page.Media).(page.DropTarget)
	if !ok {
		sh.showErr(errors.New("Diese Dateien lassen sich hier nicht ablegen. Über Medien › Importieren hinzufügen."))
		return
	}
	sh.showPage(p, page.Media)
	sh.showWorkspace()
	sh.refreshChrome()
	dt.Dropped(files)
}

// openDialog asks for a project file and opens it.
func (sh *Shell) openDialog() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			sh.showErr(err)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		_ = r.Close()
		sh.showErr(sh.OpenFile(path))
	}, sh.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{store.Extension}))
	sh.setDialogDir(d, "")
	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// shortcutGroups documents the shell and edit page shortcuts for the help dialog.
var shortcutGroups = []struct {
	Title string
	Keys  [][2]string
}{
	{"Projekt", [][2]string{
		{"Strg+N", "Neues Projekt"},
		{"Strg+O", "Projekt öffnen"},
		{"Strg+S", "Speichern"},
		{"Strg+Umschalt+S", "Speichern unter"},
		{"Strg+W", "Projekt schließen"},
		{"Strg+Z", "Rückgängig"},
		{"Strg+Umschalt+Z, Strg+Y", "Wiederholen"},
	}},
	{"Seiten", [][2]string{
		{"Umschalt+1", "Medien"},
		{"Umschalt+2", "Schnitt"},
		{"Umschalt+3", "Farbe"},
		{"Umschalt+4", "Ton"},
		{"Umschalt+5", "Export"},
	}},
	{"Wiedergabe", [][2]string{
		{"Leertaste", "Abspielen / Pause"},
		{"J", "Vorheriger Schnittpunkt"},
		{"K / L", "Stopp / Abspielen"},
		{"← / →", "Ein Frame zurück / vor"},
		{"↑ / ↓", "Vorheriger / nächster Schnittpunkt"},
		{"I / O", "In- / Out-Punkt setzen"},
	}},
	{"Schnitt", [][2]string{
		{"A", "Auswahlwerkzeug"},
		{"B", "Klingenwerkzeug"},
		{"S", "Clip am Playhead teilen"},
		{"M", "Marker setzen"},
		{"Entf", "Ausgewählten Clip löschen"},
		{"+ / −", "Timeline vergrößern / verkleinern"},
	}},
}

func (sh *Shell) showShortcuts() {
	cols := container.NewGridWithColumns(2)
	for _, g := range shortcutGroups {
		rows := container.NewVBox(text(g.Title, studiotheme.Muted, 12, true), strut(0, 2))
		for _, k := range g.Keys {
			keyText := kit.MonoText(k[0], studiotheme.Text, 11)
			rows.Add(container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(170, 22), container.NewHBox(container.NewCenter(keyText))), nil,
				container.NewHBox(container.NewCenter(text(k[1], studiotheme.Text, 12, false)))))
		}
		cols.Add(container.NewPadded(rows))
	}
	d := dialog.NewCustom("Tastenkürzel", "Schließen", container.NewVScroll(cols), sh.win)
	d.Resize(fyne.NewSize(760, 520))
	d.Show()
}

func (sh *Shell) showAbout() {
	ff := "ffmpeg gefunden: Video und Ton werden unterstützt."
	if !media.FFmpegAvailable() {
		ff = "ffmpeg nicht gefunden: Video und Ton lassen sich erst nach Installation von ffmpeg importieren."
	}
	info := widget.NewLabel("Videoschnitt mit Medien-, Schnitt-, Farb-, Ton- und Export-Seite.\n" + ff)
	info.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(text("Frame Studio", studiotheme.Text, 20, true),
		text("Prototyp", studiotheme.Muted, 12, false), container.NewGridWrap(fyne.NewSize(420, 70), info))
	dialog.NewCustom("Über Frame Studio", "Schließen", content, sh.win).Show()
}
