package media

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynelayout "fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func init() {
	page.RegisterPanel(page.PanelInfo{
		ID: page.PanelMediaPool, Title: "Medienpool", Icon: page.LazyIcon(theme.FolderIcon),
		New: func(ctx *page.Context) page.Panel { return newPoolPanel(ctx) },
	})
}

// poolPanel is the media pool with its import buttons in its own window. It is a media
// page in pool-only mode: the same grid, search, type filter, multi-selection, context
// menu, keys and imports. Folder imports include subfolders as set in the settings
// (Media.ImportRecursive). Dialogs and menus open in ctx.Window, so the workspace passes
// a context whose Window is the panel's window.
type poolPanel struct{ p *Page }

func newPoolPanel(ctx *page.Context) *poolPanel {
	p := newPage(ctx, true)
	p.Activate()
	return &poolPanel{p: p}
}

// buildPoolPanel is the pool-only content: the pool with the import buttons in its
// header and the import progress below.
func (p *Page) buildPoolPanel() fyne.CanvasObject {
	p.folderBtn.SetText("Ordner")
	tools := container.NewHBox(spacer(4), p.folderBtn, p.importBtn)
	pool := p.buildPool(tools)
	// The VBox has no height while the progress box is hidden.
	progress := container.NewVBox(p.progressBox)
	return container.NewStack(canvas.NewRectangle(studiotheme.Gap),
		container.New(fynelayout.NewCustomPaddedLayout(4, 4, 4, 4), container.NewBorder(nil, progress, nil, nil, pool)))
}

func (pp *poolPanel) Content() fyne.CanvasObject { return pp.p.content }

func (pp *poolPanel) TypedKey(k *fyne.KeyEvent) bool { return pp.p.TypedKey(k) }

func (pp *poolPanel) Close() { pp.p.Close() }

// Dropped imports files dropped onto the panel's window.
func (pp *poolPanel) Dropped(uris []fyne.URI) { pp.p.Dropped(uris) }

// SelectAll selects every item the pool shows (Ctrl+A in the panel's window).
func (pp *poolPanel) SelectAll() bool { return pp.p.SelectAll() }
