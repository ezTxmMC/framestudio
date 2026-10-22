// Package timeline is the multi-track timeline widget of the edit page.
//
// OWNER: timeline agent. Exported signatures in this file are a contract; do not change them.
package timeline

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/session"
)

type Tool int

const (
	ToolSelect Tool = iota // click selects/seeks, drag moves clips, drag edges trims
	ToolBlade              // click splits the clip under the cursor
)

// Timeline renders every track of the session's project and edits it through session.Edit.
type Timeline struct {
	widget.BaseWidget
	s     *session.Session
	cache *media.Cache

	// OnError reports rejected edits (locked track, wrong track kind...).
	OnError func(error)
}

// New creates the widget and subscribes to session changes. Call Close to unsubscribe.
func New(s *session.Session, cache *media.Cache) *Timeline {
	t := &Timeline{s: s, cache: cache}
	t.ExtendBaseWidget(t)
	return t
}

func (t *Timeline) SetTool(Tool)     {}
func (t *Timeline) Tool() Tool       { return ToolSelect }
func (t *Timeline) SetZoom(float32)  {} // 1 = whole program fits; up to ~50
func (t *Timeline) Zoom() float32    { return 1 }
func (t *Timeline) SetSnapping(bool) {}
func (t *Timeline) Snapping() bool   { return true }
func (t *Timeline) Close()           {}

func (t *Timeline) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewLabel("timeline TODO"))
}
