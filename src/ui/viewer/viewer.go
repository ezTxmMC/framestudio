// Package viewer is the program monitor: an async-rendered 16:9 (project aspect) view of
// the frame under the playhead, plus a transport bar.
//
// OWNER: edit page agent. Exported signatures in this file are a contract; do not change them.
package viewer

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/render"
	"framestudio-proto/src/session"
)

type Viewer struct {
	widget.BaseWidget
	s *session.Session
	r *render.Renderer

	// OnFrame is called on the UI thread with every newly rendered preview frame
	// (the colour page uses it for scopes). The image must not be modified.
	OnFrame func(img *image.NRGBA)
}

func New(s *session.Session, r *render.Renderer) *Viewer {
	v := &Viewer{s: s, r: r}
	v.ExtendBaseWidget(v)
	return v
}

// SetCompare renders the ungraded frame when on (colour page before/after).
func (v *Viewer) SetCompare(on bool) {}

// Close stops rendering and unsubscribes.
func (v *Viewer) Close() {}

func (v *Viewer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewLabel("viewer TODO"))
}

// Transport returns the bar under the monitor: timecode, transport buttons, loop, duration.
func Transport(s *session.Session) fyne.CanvasObject { return widget.NewLabel("transport TODO") }
