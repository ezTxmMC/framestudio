// Package page defines the contract between the app shell and the workspace pages.
package page

import (
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
// UI thread; pages must call fyne.Do for updates from their own goroutines.
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

// DropTarget is implemented by pages that accept files dropped onto the window.
// The shell routes drops to the active page, or to the media page otherwise.
type DropTarget interface {
	Dropped(uris []fyne.URI)
}
