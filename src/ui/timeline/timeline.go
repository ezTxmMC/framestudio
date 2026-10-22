// Package timeline is the multi-track timeline widget of the edit page.
//
// OWNER: timeline agent. Exported signatures in this file are a contract; do not change them.
package timeline

import (
	"math"

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

	tool     Tool
	zoom     float32
	snapping bool
	offset   float64 // frame at the left edge of the lanes
	vscroll  float32

	unsub  func()
	ld     *loader
	r      *tlRenderer
	closed bool

	drag  *dragState
	hover hit
	// snapAt is the frame of the active snap indicator, -1 when none.
	snapAt int
	// scrubbing suppresses view paging while the user drags the playhead.
	scrubbing bool
}

// New creates the widget and subscribes to session changes. Call Close to unsubscribe.
func New(s *session.Session, cache *media.Cache) *Timeline {
	t := &Timeline{s: s, cache: cache, zoom: 1, snapping: true, snapAt: -1}
	var src mediaSource
	if cache != nil {
		src = cache
	}
	t.ld = newLoader(src, t.Refresh)
	t.ExtendBaseWidget(t)
	t.unsub = s.Subscribe(t.changed)
	return t
}

func (t *Timeline) SetTool(tool Tool) {
	t.tool = tool
	t.Refresh()
}

func (t *Timeline) Tool() Tool { return t.tool }

// SetZoom sets the zoom factor (1 = whole program fits) keeping the playhead in place.
func (t *Timeline) SetZoom(z float32) {
	z = clampZoom(z)
	if z == t.zoom {
		return
	}
	old := t.geom()
	ph := float64(t.s.Playhead)
	px := old.frameX(ph)
	t.zoom = z
	g := t.geom()
	if px < headerW || px > old.w {
		px = headerW + g.laneW()/2 // playhead off screen: centre it
	}
	t.offset = g.clampOffset(t.s.Project, ph-float64(px-headerW)/g.ppf, t.zoom)
	t.Refresh()
}

func (t *Timeline) Zoom() float32 { return t.zoom }

func (t *Timeline) SetSnapping(on bool) { t.snapping = on }
func (t *Timeline) Snapping() bool      { return t.snapping }

// Close unsubscribes from the session and stops background loading.
func (t *Timeline) Close() {
	if t.closed {
		return
	}
	t.closed = true
	if t.unsub != nil {
		t.unsub()
	}
	t.ld.close()
}

func (t *Timeline) CreateRenderer() fyne.WidgetRenderer {
	t.r = newRenderer(t)
	return t.r
}

func (t *Timeline) MinSize() fyne.Size {
	return fyne.NewSize(headerW+200, rulerH+videoRowH)
}

// geom returns the geometry for the current size and view, clamping the view state.
func (t *Timeline) geom() geom {
	sz := t.Size()
	g := newGeom(t.s.Project, sz.Width, sz.Height, t.zoom, t.offset, t.vscroll)
	t.offset = g.offset
	t.vscroll = g.vscroll()
	return g
}

// changed reacts to session notifications (UI thread).
func (t *Timeline) changed(c session.Change) {
	if t.closed {
		return
	}
	if c&(session.ChangeProject|session.ChangeSelection) != 0 {
		if t.drag != nil && c&session.ChangeProject != 0 {
			t.drag = nil // the dragged clip may be gone (undo)
		}
		t.follow()
		t.Refresh()
		return
	}
	if c&session.ChangePlayhead != 0 {
		if t.follow() || t.r == nil {
			t.Refresh()
			return
		}
		t.r.updatePlayhead() // cheap path during playback
	}
}

// follow pages the view so the playhead stays visible. It reports whether the view moved.
func (t *Timeline) follow() bool {
	if t.scrubbing || t.zoom <= minZoom || t.Size().Width <= headerW {
		return false
	}
	g := t.geom()
	x := g.frameX(float64(t.s.Playhead))
	if x >= headerW && x <= g.w-12 {
		return false
	}
	// Page like an NLE: the playhead lands near the left edge.
	vis := g.visibleFrames()
	off := g.clampOffset(t.s.Project, float64(t.s.Playhead)-vis*0.05, t.zoom)
	if math.Abs(off-t.offset) < 1e-9 {
		return false
	}
	t.offset = off
	return true
}

// scale returns the canvas pixel scale used to size generated images.
func (t *Timeline) scale() float32 {
	if app := fyne.CurrentApp(); app != nil {
		if c := app.Driver().CanvasForObject(t); c != nil && c.Scale() > 0 {
			return c.Scale()
		}
	}
	return 1
}

func (t *Timeline) fail(err error) {
	if err != nil && t.OnError != nil {
		t.OnError(err)
	}
}
