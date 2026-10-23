// Package page defines the contract between the app shell and the workspace pages.
package page

import (
	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/layout"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/media"
	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
)

// IDs of the workspace pages, in page bar order.
const (
	Media   = "media"
	Edit    = "edit"
	Color   = "color"
	Audio   = "audio"
	Deliver = "deliver"
)

// Context is what every page of one open project gets. Session notifications arrive on the
// UI thread; pages must call kit.Do (or fyne.Do) for updates from their own goroutines.
type Context struct {
	Window   fyne.Window
	Session  *session.Session
	Renderer *render.Renderer
	Media    *media.Cache
	Navigate func(pageID string)
	ShowErr  func(err error)
	// Commands is the app-wide command registry (run commands from buttons, read state).
	Commands *command.Registry
}

type Page interface {
	// Content builds the page UI. Called once.
	Content() fyne.CanvasObject
	// Activate / Deactivate are called when the page becomes visible or hidden.
	Activate()
	Deactivate()
	// TypedKey handles page shortcuts while no widget has focus. Return true if handled.
	TypedKey(k *fyne.KeyEvent) bool
	// Close releases subscriptions and goroutines when the project tab closes.
	Close()
}

// Busy is implemented by pages that run work the user would lose by closing the project,
// such as exports. Busy returns a short German description ("Export läuft (17 %)"), or ""
// when idle. The shell asks before closing a busy project.
type Busy interface {
	Busy() string
}

// Waiter is implemented by pages whose background goroutines must finish cleanup (temp
// files, child processes) after Close. Wait blocks until they have ended or the timeout
// passed, and reports whether they ended.
type Waiter interface {
	Wait(timeout time.Duration) bool
}

// DropTarget is implemented by pages that accept files dropped onto the window.
// The shell routes drops to the active page, or to the media page otherwise.
type DropTarget interface {
	Dropped(uris []fyne.URI)
}

// CommandProvider is implemented by pages that bind commands (see src/ui/command). The
// shell binds them in the page's scope (command.PageScope(id)) once the page exists and
// activates the scope while the page is visible.
type CommandProvider interface {
	Commands() []command.Binding
}

// LayoutAware pages save and restore their split positions and hidden areas.
type LayoutAware interface {
	LayoutState() layout.Page
	ApplyLayout(l layout.Page)
}

// Panel IDs: areas that can be detached into their own window (any monitor).
const (
	PanelProgram   = "program"   // program monitor with transport
	PanelCleanFeed = "cleanfeed" // picture only, black surround, no controls (client/fullscreen output)
	PanelTimeline  = "timeline"
	PanelInspector = "inspector"
	PanelLibrary   = "library"   // edit page media/generator library
	PanelMediaPool = "mediapool" // media page pool with import
	PanelScopes    = "scopes"
	PanelMixer     = "mixer"
	PanelQueue     = "queue" // render queue
)

// Panel is a detachable area instantiated for one project's Context.
type Panel interface {
	Content() fyne.CanvasObject
	// TypedKey handles keys while the panel's window has focus and no widget does.
	TypedKey(k *fyne.KeyEvent) bool
	Close()
}

// PanelInfo registers a detachable panel. Owners register in their package init().
type PanelInfo struct {
	ID    string
	Title string
	Icon  fyne.Resource
	New   func(ctx *Context) Panel
}

var panels []PanelInfo

// RegisterPanel adds or replaces a panel factory.
func RegisterPanel(p PanelInfo) {
	for i := range panels {
		if panels[i].ID == p.ID {
			panels[i] = p
			return
		}
	}
	panels = append(panels, p)
}

// Panels lists the registered panels in registration order.
func Panels() []PanelInfo { return append([]PanelInfo(nil), panels...) }

func PanelByID(id string) (PanelInfo, bool) {
	for _, p := range panels {
		if p.ID == id {
			return p, true
		}
	}
	return PanelInfo{}, false
}
