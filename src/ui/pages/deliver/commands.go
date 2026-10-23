package deliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/page"
)

// Commands binds the deliver.* commands in the page scope (page.CommandProvider).
func (p *Page) Commands() []command.Binding {
	p.Content()
	return bindings(p.ctx, func() *Page { return p })
}

// GlobalCommands returns the deliver.* commands for the shell to bind in command.Global
// for the active project, so they work from every page. "Exportieren…" and "Zur
// Render-Warteschlange hinzufügen" open the deliver page through ctx.Navigate (which
// creates it); the queue commands work on the project's render queue directly, also
// when only the detached queue panel exists.
func GlobalCommands(ctx *page.Context) []command.Binding {
	return bindings(ctx, func() *Page {
		if ctx.Navigate != nil {
			ctx.Navigate(page.Deliver)
		}
		return pages[ctx.Session]
	})
}

// bindings builds the deliver commands; open shows the deliver page and returns it
// (nil when it does not exist).
func bindings(ctx *page.Context, open func() *Page) []command.Binding {
	q := func() *renderQueue { return existingQueue(ctx.Session) }
	return []command.Binding{
		{ID: "deliver.export", Icon: theme.UploadIcon(), Run: func() {
			if p := open(); p != nil {
				p.focusForm()
			}
		}},
		{ID: "deliver.addToQueue", Icon: theme.ContentAddIcon(), Run: func() {
			if p := open(); p != nil {
				p.Content()
				p.form.submit(false)
			}
		}},
		{ID: "deliver.renderAll", Icon: theme.MediaPlayIcon(),
			Run:     func() { q().renderAll() },
			Enabled: func() bool { return q() != nil && q().hasReady() }},
		{ID: "deliver.cancelAll", Icon: theme.CancelIcon(),
			Run:     func() { q().cancelAll() },
			Enabled: func() bool { return q() != nil && q().hasActive() }},
		{ID: "deliver.openFolder", Icon: theme.FolderOpenIcon(), Run: func() { openExportFolder(ctx) }},
	}
}

// focusForm puts the keyboard focus into the file name of the render settings.
func (p *Page) focusForm() {
	p.Content()
	if !p.form.content.Visible() {
		p.SetAreaVisible(areaSettings, true)
	}
	if p.ctx.Window != nil {
		p.ctx.Window.Canvas().Focus(p.form.name)
	}
}

// exportDir is the folder the form points at (or the default from the settings).
func (p *Page) exportDir() string {
	if p.form == nil {
		return exportFolder()
	}
	dir := strings.TrimSpace(p.form.folder.Text)
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	if dir == "" {
		return exportFolder()
	}
	return dir
}

// openExportFolder shows the export folder of the deliver form (or the default) in the
// file manager.
func openExportFolder(ctx *page.Context) {
	dir, open := exportFolder(), openWithSystem
	report := ctx.ShowErr
	if p := pages[ctx.Session]; p != nil {
		dir, open = p.exportDir(), p.openFolder
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if report != nil {
			report(fmt.Errorf("Der Exportordner %s existiert nicht. Wähle auf der Seite „Export“ einen anderen Ordner.", dir))
		}
		return
	}
	if err := open(dir); err != nil && report != nil {
		report(fmt.Errorf("Der Ordner %s kann nicht geöffnet werden. Ist xdg-open installiert? (%v)", dir, err))
	}
}
