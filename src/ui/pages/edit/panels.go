package edit

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
	"framestudio-proto/src/ui/page"
	studiotheme "framestudio-proto/src/ui/theme"
)

func init() {
	for _, p := range []struct {
		id, title string
		icon      func() fyne.Resource
	}{
		{page.PanelProgram, "Programm-Monitor", theme.MediaVideoIcon},
		{page.PanelCleanFeed, "Clean-Feed", theme.ComputerIcon},
		{page.PanelTimeline, "Timeline", theme.ViewRestoreIcon},
		{page.PanelInspector, "Inspector", theme.InfoIcon},
		{page.PanelLibrary, "Bibliothek", theme.FolderIcon},
	} {
		id := p.id
		page.RegisterPanel(page.PanelInfo{ID: id, Title: p.title, Icon: page.LazyIcon(p.icon),
			New: func(ctx *page.Context) page.Panel { return NewPanel(ctx, id) }})
	}
}

// Panel is a detachable part of the edit page in its own window: program monitor,
// clean feed, timeline, inspector or library. Every instance builds its own widgets
// on the project's session, so edits, selection and playhead stay in sync with the
// page and other panels; the tool follows the page's timeline.
type Panel struct {
	pg      *Page
	content fyne.CanvasObject
}

var _ page.Panel = (*Panel)(nil)

// NewPanel builds panel id (page.PanelProgram, PanelCleanFeed, PanelTimeline,
// PanelInspector or PanelLibrary) for ctx. ctx.Window should be the panel's window
// (dialogs, drops); Close releases everything.
func NewPanel(ctx *page.Context, id string) *Panel {
	p := newPage(ctx, id)
	p.start()
	var content fyne.CanvasObject
	switch id {
	case page.PanelCleanFeed:
		p.buildViewer(true)
		bg := canvas.NewRectangle(studiotheme.MonitorOut)
		content = container.NewStack(bg, p.viewer)
	case page.PanelTimeline:
		p.buildTimeline()
		content = kit.Panel("Timeline", p.bar.content, p.timelineHost)
	case page.PanelInspector:
		p.buildInspector()
		content = kit.Panel("Inspector", container.NewHBox(p.insp.kind, fixed(6, 0)), p.insp.content)
	case page.PanelLibrary:
		p.buildLibrary()
		content = kit.Panel("Bibliothek", nil, p.lib.content)
	default: // page.PanelProgram
		p.panel = page.PanelProgram
		p.buildViewer(false)
		info := container.NewHBox(container.NewCenter(p.progName), container.NewCenter(p.progMeta), fixed(4, 0))
		content = kit.Panel("Programm", info, p.progBox)
	}
	p.root = content
	p.unsub = ctx.Session.Subscribe(p.changed)
	p.changed(session.ChangeProject | session.ChangeSelection | session.ChangeMeta)
	p.attachWindow()
	p.Activate()
	return &Panel{pg: p, content: content}
}

func (x *Panel) Content() fyne.CanvasObject { return x.content }

// TypedKey runs the edit commands bound to the key (with the user's key map), so a
// detached timeline cuts, trims and zooms like the page.
func (x *Panel) TypedKey(k *fyne.KeyEvent) bool { return x.pg.TypedKey(k) }

// Close stops rendering, meters and loaders and releases the subscriptions.
func (x *Panel) Close() { x.pg.Close() }

// ID returns the panel ID.
func (x *Panel) ID() string { return x.pg.panel }
