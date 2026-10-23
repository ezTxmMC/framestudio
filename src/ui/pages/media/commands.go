package media

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/page"
)

// Commands binds the media.* commands in the page scope (page.CommandProvider). While
// the page is visible, "Alles auswählen", "Auswahl aufheben" and "Löschen" act on the
// pool instead of the timeline.
func (p *Page) Commands() []command.Binding {
	selected := func() bool { return len(p.selectedItems()) > 0 }
	return []command.Binding{
		{ID: "media.import", Icon: theme.ContentAddIcon(), Run: p.showImportDialog, Enabled: p.canImport},
		{ID: "media.importFolder", Icon: theme.FolderOpenIcon(), Run: p.showFolderDialog, Enabled: p.canImport},
		{ID: "media.relink", Run: func() {
			if items := p.selectedItems(); len(items) == 1 {
				p.showRelinkDialog(items[0])
			}
		}, Enabled: func() bool { return len(p.selectedItems()) == 1 }},
		{ID: "media.reveal", Icon: theme.FolderIcon(), Run: p.reveal, Enabled: selected},
		{ID: "media.removeUnused", Icon: theme.DeleteIcon(), Run: p.confirmRemoveUnused,
			Enabled: func() bool { return len(p.unused()) > 0 }},
		{ID: "media.insert", Run: func() { p.insert(p.selectedItems(), false) }, Enabled: selected},
		{ID: "media.append", Run: func() { p.insert(p.selectedItems(), true) }, Enabled: selected},
		{ID: "edit.selectAll", Run: p.selectAll, Enabled: func() bool { return len(p.view) > 0 }},
		{ID: "edit.deselect", Run: p.deselect, Enabled: selected},
		{ID: "edit.delete", Icon: theme.DeleteIcon(), Run: func() { p.confirmRemove(p.selectedItems()...) }, Enabled: selected},
	}
}

// GlobalCommands returns "Medien importieren…" and "Ordner importieren…" for the shell
// to bind in command.Global for the active project: they open the media page through
// ctx.Navigate (which creates it) and start the import there, from any page.
func GlobalCommands(ctx *page.Context) []command.Binding {
	open := func() *Page {
		if ctx.Navigate != nil {
			ctx.Navigate(page.Media)
		}
		return pages[ctx.Session]
	}
	enabled := func() bool {
		p := pages[ctx.Session]
		return p == nil || p.canImport()
	}
	return []command.Binding{
		{ID: "media.import", Icon: theme.ContentAddIcon(), Enabled: enabled, Run: func() {
			if p := open(); p != nil {
				p.showImportDialog()
			}
		}},
		{ID: "media.importFolder", Icon: theme.FolderOpenIcon(), Enabled: enabled, Run: func() {
			if p := open(); p != nil {
				p.showFolderDialog()
			}
		}},
	}
}

// canImport reports whether an import can start (none runs, no chooser is open).
func (p *Page) canImport() bool { return !p.importing && !p.picking && p.fileDlg == nil }

// deselect clears the pool selection.
func (p *Page) deselect() {
	p.sel, p.anchor = nil, ""
	p.ctx.Session.SelectMedia("")
}

// reveal opens the folder of the selected item in the file manager.
func (p *Page) reveal() {
	items := p.selectedItems()
	if len(items) == 0 {
		return
	}
	m := items[len(items)-1]
	dir := filepath.Dir(m.Path)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		p.showErr(fmt.Errorf("Der Ordner von „%s“ existiert nicht mehr (%s). Das Medium über „Neu verknüpfen…“ wiederfinden.", m.Name, dir))
		return
	}
	if err := p.openDir(dir); err != nil {
		p.showErr(fmt.Errorf("Der Ordner %s kann nicht geöffnet werden. Ist xdg-open installiert? (%v)", dir, err))
	}
}

// unused returns the media no timeline clip uses, in pool order.
func (p *Page) unused() []model.MediaItem {
	var out []model.MediaItem
	for _, m := range p.ctx.Session.Project.Media {
		if usage(p.ctx.Session.Project, m.ID) == 0 {
			out = append(out, m)
		}
	}
	return out
}

// confirmRemoveUnused asks, then removes every unused medium as one undo step.
func (p *Page) confirmRemoveUnused() {
	items := p.unused()
	if len(items) == 0 {
		return
	}
	ids := make([]string, len(items))
	for i, m := range items {
		ids[i] = m.ID
	}
	what := fmt.Sprintf("„%s“ wird", items[0].Name)
	if len(items) > 1 {
		what = fmt.Sprintf("%d Medien werden", len(items))
	}
	msg := what + " von keinem Clip verwendet und aus dem Projekt entfernt. Die Dateien bleiben auf dem Datenträger."
	d := dialog.NewConfirm("Unbenutzte Medien entfernen", msg, func(ok bool) {
		if !ok || p.closed {
			return
		}
		// only what is still unused: a clip may have been placed meanwhile
		var still []string
		for _, id := range ids {
			if _, ok := p.ctx.Session.Project.MediaByID(id); ok && usage(p.ctx.Session.Project, id) == 0 {
				still = append(still, id)
			}
		}
		if len(still) > 0 {
			p.removeAs("Unbenutzte Medien entfernen", still...)
		}
	}, p.ctx.Window)
	d.SetConfirmText("Entfernen")
	d.SetDismissText("Abbrechen")
	d.Show()
}
