// Package page defines the contract between the app shell and the workspace pages.
package page

import (
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
