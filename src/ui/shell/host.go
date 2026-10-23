package shell

import (
	"fyne.io/fyne/v2"

	wslayout "framestudio-proto/src/ui/layout"
	"framestudio-proto/src/ui/page"
	"framestudio-proto/src/ui/workspace"
)

// The shell is the workspace.Host of the workspace manager.
var _ workspace.Host = (*Shell)(nil)

func (sh *Shell) App() fyne.App           { return sh.app }
func (sh *Shell) MainWindow() fyne.Window { return sh.win }

// ActiveContext returns the page context of the active project tab (also while the
// manager is shown), nil when no project is open.
func (sh *Shell) ActiveContext() *page.Context {
	if sh.active == nil {
		return nil
	}
	return sh.active.ctx
}

// OnActiveChange registers fn to run on the UI thread whenever the active project tab
// changes (another tab, the last tab closed, or the first one opened).
func (sh *Shell) OnActiveChange(fn func()) { sh.activeFns = append(sh.activeFns, fn) }

// notifyActive runs the OnActiveChange callbacks once per change of the active tab.
func (sh *Shell) notifyActive() {
	if sh.active == sh.notified {
		return
	}
	sh.notified = sh.active
	for _, fn := range sh.activeFns {
		fn()
	}
}

// PageLayouts returns the layout state of the active project's created pages that save
// one (page.LayoutAware), plus the remembered state of pages not created yet.
func (sh *Shell) PageLayouts() map[string]wslayout.Page {
	out := map[string]wslayout.Page{}
	for id, l := range sh.pageLayouts {
		out[id] = l
	}
	if p := sh.active; p != nil {
		for id, pg := range p.pages {
			if la, ok := pg.(page.LayoutAware); ok {
				out[id] = la.LayoutState()
			}
		}
	}
	return out
}

// captureLayouts records the layout of p's created pages as the global page layout
// (one arrangement for all tabs; the user changes it in the shown tab).
func (sh *Shell) captureLayouts(p *project) {
	for id, pg := range p.pages {
		if la, ok := pg.(page.LayoutAware); ok {
			sh.pageLayouts[id] = la.LayoutState()
		}
	}
}

// applyLayouts gives p's created pages the global page layout.
func (sh *Shell) applyLayouts(p *project) {
	for id, pg := range p.pages {
		if l, ok := sh.pageLayouts[id]; ok {
			if la, ok := pg.(page.LayoutAware); ok {
				la.ApplyLayout(l)
			}
		}
	}
}

// ApplyPageLayouts restores page layouts in every open project; pages created later
// (also of projects opened later) start with them.
func (sh *Shell) ApplyPageLayouts(ls map[string]wslayout.Page) {
	for id, l := range ls {
		sh.pageLayouts[id] = l
	}
	for _, p := range sh.projects {
		for id, pg := range p.pages {
			if l, ok := ls[id]; ok {
				if la, ok := pg.(page.LayoutAware); ok {
					la.ApplyLayout(l)
				}
			}
		}
	}
}

// ActivePage returns the page shown for the active project ("" when none is open).
func (sh *Shell) ActivePage() string {
	if sh.active == nil {
		return ""
	}
	return sh.active.current
}

// ShowPage shows page id of the active project (leaving the manager).
func (sh *Shell) ShowPage(id string) {
	if sh.active != nil {
		sh.showPage(sh.active, id)
	}
}
