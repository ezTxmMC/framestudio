package shell

import (
	"errors"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"

	"framestudio-proto/src/store"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
)

// Key routing
//
// Keys without Ctrl/Alt/Super arrive in typedKey while no widget has focus: the active
// page's TypedKey gets them first (page-internal keys), then the registry dispatches
// them (page scope before global). Combinations with Ctrl, Alt or Super never reach
// typedKey: the driver turns them into shortcuts, so every such command shortcut is
// registered on the canvas (registerShortcuts) and re-registered when the keymap
// changes. Fyne hands shortcuts to a focused text field instead, so while one has focus
// only its own editing shortcuts work. Alt alone, F10 and Alt+menu letter open the menu
// bar. While a dialog is open no key reaches the pages or commands.

// pageKeys are the digit keys of the page switch alternatives Alt+1…5 and Ctrl+1…5.
var pageKeys = []fyne.KeyName{fyne.Key1, fyne.Key2, fyne.Key3, fyne.Key4, fyne.Key5}

// comboOf returns the key combination a shortcut stands for, including the driver's
// own shortcut types.
func comboOf(sc fyne.Shortcut) (fyne.KeyName, fyne.KeyModifier, bool) {
	const ctrl = fyne.KeyModifierShortcutDefault
	switch s := sc.(type) {
	case *desktop.CustomShortcut:
		return s.KeyName, s.Modifier, true
	case *fyne.ShortcutUndo:
		return fyne.KeyZ, ctrl, true
	case *fyne.ShortcutRedo:
		return fyne.KeyY, ctrl, true
	case *fyne.ShortcutSelectAll:
		return fyne.KeyA, ctrl, true
	case *fyne.ShortcutCopy:
		if s.Secondary {
			return fyne.KeyInsert, ctrl, true
		}
		return fyne.KeyC, ctrl, true
	case *fyne.ShortcutCut:
		if s.Secondary {
			return fyne.KeyDelete, fyne.KeyModifierShift, true
		}
		return fyne.KeyX, ctrl, true
	case *fyne.ShortcutPaste:
		if s.Secondary {
			return fyne.KeyInsert, fyne.KeyModifierShift, true
		}
		return fyne.KeyV, ctrl, true
	}
	return "", 0, false
}

// builtinShortcuts are the driver's own shortcut types, always registered: the desktop
// driver delivers Ctrl+Z/Y/A/C/X/V and Shift+Delete/Insert as these, never as
// CustomShortcut (which is registered for them as well, for other drivers).
func builtinShortcuts() []fyne.Shortcut {
	return []fyne.Shortcut{
		&fyne.ShortcutUndo{}, &fyne.ShortcutRedo{}, &fyne.ShortcutSelectAll{},
		&fyne.ShortcutCopy{}, &fyne.ShortcutCut{}, &fyne.ShortcutPaste{},
	}
}

// aliasKeys are combinations outside the registry: Ctrl+Y redoes, Alt+1…5 and Ctrl+1…5
// switch pages (keyboard-layout independent alternatives of Umschalt+1…5).
func (sh *Shell) aliasKey(key fyne.KeyName, mods fyne.KeyModifier) bool {
	if key == fyne.KeyY && mods == fyne.KeyModifierShortcutDefault {
		return sh.reg.Run("edit.redo")
	}
	if mods == fyne.KeyModifierAlt || mods == fyne.KeyModifierShortcutDefault {
		for i, pk := range pageKeys {
			if key == pk && i < len(sh.pageDefs) && sh.active != nil {
				sh.showPage(sh.active, sh.pageDefs[i].ID)
				return true
			}
		}
	}
	return false
}

// menuMnemonics assigns each top-level menu a letter for Alt+letter: the first letter of
// its title not taken by an earlier menu.
func menuMnemonics(menus []command.Menu) map[fyne.KeyName]int {
	out := map[fyne.KeyName]int{}
	for i, m := range menus {
		for _, r := range strings.ToUpper(m.Title) {
			k := fyne.KeyName(string(r))
			if r < 'A' || r > 'Z' {
				continue
			}
			if _, taken := out[k]; !taken {
				out[k] = i
				break
			}
		}
	}
	return out
}

// installKeys routes typed keys and registers the window shortcuts.
func (sh *Shell) installKeys() {
	c := sh.win.Canvas()
	c.SetOnTypedKey(sh.typedKey)
	if dc, ok := c.(desktop.Canvas); ok {
		dc.SetOnKeyDown(sh.keyDown)
		dc.SetOnKeyUp(sh.keyUp)
	}
	sh.registerShortcuts()
}

// shortcutSignature identifies the current keymap; shortcuts are re-registered when it
// changes.
func (sh *Shell) shortcutSignature() string {
	var b strings.Builder
	for _, c := range sh.reg.All() {
		if c.Shortcut != "" {
			b.WriteString(c.ID)
			b.WriteByte('=')
			b.WriteString(c.Shortcut)
			b.WriteByte(';')
		}
	}
	return b.String()
}

// registerShortcuts (re-)registers a canvas shortcut for every command shortcut with
// Ctrl, Alt or Super, the page and redo aliases, the menu letters and the driver's
// built-in shortcut types. Unchanged keymaps are left alone.
func (sh *Shell) registerShortcuts() {
	sig := sh.shortcutSignature()
	if sig == sh.keySig && sh.shortcuts != nil {
		return
	}
	sh.keySig = sig
	c := sh.win.Canvas()
	for _, sc := range sh.registered {
		c.RemoveShortcut(sc)
	}
	sh.registered = nil
	sh.shortcuts = map[string]func(fyne.Shortcut){}
	seen := map[string]bool{}
	add := func(sc fyne.Shortcut) {
		name := sc.ShortcutName()
		if seen[name] {
			return
		}
		seen[name] = true
		sh.registered = append(sh.registered, sc)
		sh.shortcuts[name] = sh.shortcutTyped
		c.AddShortcut(sc, sh.shortcutTyped)
	}
	for _, sc := range builtinShortcuts() {
		add(sc)
	}
	const mod = fyne.KeyModifierControl | fyne.KeyModifierAlt | fyne.KeyModifierSuper
	for _, cmd := range sh.reg.All() {
		k, m, ok := command.Parse(cmd.Shortcut)
		if !ok || m&mod == 0 {
			continue
		}
		add(&desktop.CustomShortcut{KeyName: k, Modifier: m})
	}
	for _, k := range pageKeys {
		add(&desktop.CustomShortcut{KeyName: k, Modifier: fyne.KeyModifierAlt})
		add(&desktop.CustomShortcut{KeyName: k, Modifier: fyne.KeyModifierShortcutDefault})
	}
	for k := range menuMnemonics(command.MenuBar) {
		add(&desktop.CustomShortcut{KeyName: k, Modifier: fyne.KeyModifierAlt})
	}
}

// shortcutTyped handles every registered canvas shortcut.
func (sh *Shell) shortcutTyped(sc fyne.Shortcut) {
	k, m, ok := comboOf(sc)
	if !ok || sh.modal() { // a dialog is open: keys must not edit what is behind it
		return
	}
	sh.altArmed = false
	sh.combo(k, m)
}

// combo runs the command bound to key+mods, then the aliases, then the menu letters.
func (sh *Shell) combo(k fyne.KeyName, m fyne.KeyModifier) bool {
	if sh.dispatch(k, m) || sh.aliasKey(k, m) {
		return true
	}
	if m == fyne.KeyModifierAlt {
		if i, ok := menuMnemonics(command.MenuBar)[k]; ok {
			sh.openMenu(i)
			return true
		}
	}
	return false
}

// dispatch runs the registry command for a key, marking it as keyboard-triggered.
func (sh *Shell) dispatch(k fyne.KeyName, m fyne.KeyModifier) bool {
	sh.dispatchingKey = true
	defer func() { sh.dispatchingKey = false }()
	return sh.reg.Dispatch(k, m)
}

// openMenu opens top-level menu i of the menu bar.
func (sh *Shell) openMenu(i int) {
	if sh.menuBar != nil {
		sh.menuBar.Open(i)
	}
	sh.menuOpened = append(sh.menuOpened, i)
}

// keyDown / keyUp detect Alt pressed and released alone, which opens the menu bar.
func (sh *Shell) keyDown(k *fyne.KeyEvent) {
	sh.altArmed = k.Name == desktop.KeyAltLeft || k.Name == desktop.KeyAltRight
}

func (sh *Shell) keyUp(k *fyne.KeyEvent) {
	if (k.Name == desktop.KeyAltLeft || k.Name == desktop.KeyAltRight) && sh.altArmed && !sh.modal() {
		sh.openMenu(0)
	}
	sh.altArmed = false
}

// heldModifiers returns the modifier keys the driver reported with the current key
// event. The desktop driver takes them from the key event itself, so they cannot get
// stuck when the window loses focus while a modifier is held.
func heldModifiers() fyne.KeyModifier {
	if a := fyne.CurrentApp(); a != nil {
		if d, ok := a.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return 0
}

// modal reports whether a dialog or pop-up covers the window. Keys then belong to it;
// the media-drag ghost (also a pop-up) does not count.
func (sh *Shell) modal() bool {
	return sh.win.Canvas().Overlays().Top() != nil && !kit.MediaDragActive()
}

// typedKey handles keys while no widget has focus: F10 opens the menu bar; in the
// manager its own keys come first; otherwise the active page's TypedKey, then the
// command registry. Umschalt+digit switches pages before the page sees the digit.
func (sh *Shell) typedKey(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyAltLeft || k.Name == desktop.KeyAltRight {
		return // Alt alone opens the menu on release (keyUp)
	}
	sh.altArmed = false
	if sh.modal() {
		return
	}
	mods := sh.modifiers()
	if k.Name == fyne.KeyF10 && mods == 0 {
		sh.openMenu(0)
		return
	}
	if sh.showManager {
		if sh.mgr.typedKey(k) {
			return
		}
		sh.dispatch(k.Name, mods)
		return
	}
	p := sh.active
	if p == nil {
		sh.dispatch(k.Name, mods)
		return
	}
	if mods == fyne.KeyModifierShift && isDigit(k.Name) && sh.dispatch(k.Name, mods) {
		return
	}
	if pg := p.pages[p.current]; pg != nil && pg.TypedKey(k) {
		return
	}
	sh.dispatch(k.Name, mods)
}

func isDigit(k fyne.KeyName) bool { return len(k) == 1 && k[0] >= '0' && k[0] <= '9' }

// selectAller is implemented by pages with their own "select all" (the media pool).
type selectAller interface {
	SelectAll() bool
}

// selectAll is Ctrl+A: the active page selects all of what it shows (the media pool on
// Medien); on the timeline pages every clip of the project is selected. In the manager
// or while a text field has focus (the field gets the shortcut itself) it does nothing.
func (sh *Shell) selectAll() {
	p := sh.workspaceProject()
	if p == nil {
		return
	}
	if sa, ok := p.pages[p.current].(selectAller); ok && sa.SelectAll() {
		return
	}
	switch p.current {
	case page.Edit, page.Color, page.Audio:
		p.sess.SelectAll()
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

func (sh *Shell) toggleLoop() {
	if p := sh.workspaceProject(); p != nil {
		p.sess.SetLoop(!p.sess.Loop)
	}
}

// positionedDrop is implemented by pages that place dropped files where they land (the
// edit page puts them on the timeline under the pointer).
type positionedDrop interface {
	DroppedAt(pos fyne.Position, uris []fyne.URI)
}

// dropped is droppedAt without a known position.
func (sh *Shell) dropped(uris []fyne.URI) { sh.droppedAt(fyne.NewPos(-1, -1), uris) }

// droppedAt opens dropped project files and hands other files to the active page (at
// window position pos), or to the media page when the active page takes no files.
func (sh *Shell) droppedAt(pos fyne.Position, uris []fyne.URI) {
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
		if dt, ok := p.pages[p.current].(positionedDrop); ok {
			dt.DroppedAt(pos, files)
			return
		}
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
