// Package timeline is the multi-track timeline widget of the edit page.
//
// OWNER: timeline agent. Exported signatures in this file are a contract; do not change them.
package timeline

import (
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/media"
	"framestudio-proto/src/model"
	"framestudio-proto/src/session"
	"framestudio-proto/src/ui/kit"
)

type Tool int

const (
	ToolSelect Tool = iota // click selects/seeks, drag moves clips, drag edges trims
	ToolBlade              // click splits the clip under the cursor
	ToolRipple             // edge drags trim and shift later clips on the track
	ToolRoll               // dragging a cut between two adjacent clips moves the edit point
)

// Timeline renders every track of the session's project and edits it through session.Edit.
type Timeline struct {
	widget.BaseWidget
	s     *session.Session
	cache *media.Cache

	// OnError reports rejected edits (locked track, wrong track kind...).
	OnError func(error)
	// OnZoomChanged reports zoom changes the widget made itself (e.g. Ctrl+wheel), so
	// toolbars can follow.
	OnZoomChanged func(float32)

	bladeAll bool

	tool     Tool
	zoom     float32
	snapping bool
	offset   float64 // frame at the left edge of the lanes
	vscroll  float32

	unsub      func()
	unsubMedia []func() // media cache subscriptions (offline map, relink invalidation)
	ld         *loader
	posters    *posterLoader
	known      map[string]model.MediaItem // media as last drawn, to spot relinks
	r          *tlRenderer
	closed     bool

	// heights holds track heights the user dragged, by track ID (view state, not saved).
	heights map[string]float32

	win        fyne.Window
	unregister func() // drop zone registration

	drag     *dragState
	drop     *dropPlan // media drag hovering over the timeline
	hover    hit
	hoverPos fyne.Position
	// mods are the keyboard modifiers of the last mouse press or move (Tapped and Scrolled
	// carry none).
	mods fyne.KeyModifier
	// lastTap remembers the previous header click to detect double clicks (rename).
	lastTap struct {
		at    time.Time
		track string
	}
	rename *renameState
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
	var psrc posterSource
	if cache != nil {
		psrc = cache
	}
	t.posters = newPosterLoader(psrc, t.Refresh)
	t.heights = map[string]float32{}
	t.ExtendBaseWidget(t)
	t.unsub = s.Subscribe(t.changed)
	if cache != nil {
		// The cache's offline map and relink invalidations run on background goroutines;
		// hop to the UI thread before touching the loaders.
		t.unsubMedia = append(t.unsubMedia,
			cache.OnOfflineChange(func() {
				kit.Do(func() {
					if !t.closed && t.ld.recheckOffline(func(path string) bool {
						return cache.Offline(model.MediaItem{Path: path})
					}) {
						t.Refresh()
					}
				})
			}),
			cache.OnInvalidate(func(id string) {
				kit.Do(func() {
					if t.closed {
						return
					}
					t.ld.forget(id)
					t.posters.forget(id)
					t.Refresh()
				})
			}))
	}
	return t
}

func (t *Timeline) SetTool(tool Tool) {
	t.tool = tool
	t.Refresh()
}

func (t *Timeline) Tool() Tool { return t.tool }

// SetBladeAllTracks makes the blade cut every unlocked track at the clicked frame.
func (t *Timeline) SetBladeAllTracks(on bool) { t.bladeAll = on }
func (t *Timeline) BladeAllTracks() bool      { return t.bladeAll }

// AttachWindow registers the timeline as a drop target for media dragged inside w (media
// pool, library). Call it once the widget is in the window; Close unregisters.
func (t *Timeline) AttachWindow(w fyne.Window) {
	if t.unregister != nil {
		t.unregister()
		t.unregister = nil
	}
	t.win = w
	if w != nil && !t.closed {
		t.unregister = kit.RegisterDropZone(w, t)
	}
}

// ClearSelection deselects all clips.
func (t *Timeline) ClearSelection() { t.s.SelectClip("") }

// Escape cancels what the pointer is doing (marquee, move, trim, rename) or, when idle,
// clears the clip selection. It reports whether anything changed. The edit page calls it
// for the Esc key.
func (t *Timeline) Escape() bool {
	switch {
	case t.rename != nil:
		t.cancelRename()
	case t.drag != nil && t.drag.mode != dragNone && t.drag.mode != dragIgnore:
		if t.drag.mode == dragMarquee {
			t.s.SelectClips(t.drag.orig)
		}
		t.drag = &dragState{mode: dragIgnore} // swallow the rest of this drag
		t.snapAt, t.scrubbing = -1, false
		t.Refresh()
	case len(t.s.Selected) > 0 || t.s.SelectedClip != "":
		t.ClearSelection()
	default:
		return false
	}
	return true
}

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

// zoomAt zooms to z keeping the frame under widget x in place, and reports the change
// through OnZoomChanged.
func (t *Timeline) zoomAt(z float32, x float32) {
	z = clampZoom(z)
	if z == t.zoom {
		return
	}
	old := t.geom()
	x = max(headerW, min(x, old.w))
	f := old.xFrame(x)
	t.zoom = z
	g := t.geom()
	t.offset = g.clampOffset(t.s.Project, f-float64(x-headerW)/g.ppf, t.zoom)
	t.Refresh()
	if t.OnZoomChanged != nil {
		t.OnZoomChanged(z)
	}
}

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
	if t.unregister != nil {
		t.unregister()
		t.unregister = nil
	}
	for _, u := range t.unsubMedia {
		u()
	}
	t.unsubMedia = nil
	t.ld.close()
	t.posters.close()
}

func (t *Timeline) CreateRenderer() fyne.WidgetRenderer {
	t.r = newRenderer(t)
	return t.r
}

func (t *Timeline) MinSize() fyne.Size {
	return fyne.NewSize(headerW+200, rulerH+audioRowH)
}

// geom returns the geometry for the current size and view, clamping the view state.
func (t *Timeline) geom() geom {
	sz := t.Size()
	g := layoutGeom(t.s.Project, sz.Width, sz.Height, t.zoom, t.offset, t.vscroll, t.heights)
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
		if c&session.ChangeProject != 0 {
			if t.drag != nil && t.drag.mode != dragScrub && t.drag.mode != dragMarquee {
				t.drag = nil // the dragged clip may be gone (undo)
			}
			t.forgetChangedMedia()
			if t.rename != nil {
				if tr, _ := t.s.Project.Track(t.rename.track); tr == nil {
					t.cancelRename()
				}
			}
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

// forgetChangedMedia drops loaded filmstrips and waveforms of media items whose file or
// length changed (relink, replaced file), so they load again.
func (t *Timeline) forgetChangedMedia() {
	known := make(map[string]model.MediaItem, len(t.s.Project.Media))
	for _, m := range t.s.Project.Media {
		if old, ok := t.known[m.ID]; ok && (old.Path != m.Path || old.Duration != m.Duration) {
			t.ld.forget(m.ID)
			t.posters.forget(m.ID)
		}
		known[m.ID] = m
	}
	t.known = known
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
	// Page like an NLE: the playhead lands near the edge it enters from, the left one when
	// playing forwards and the right one when playing backwards (J/K/L shuttle).
	vis := g.visibleFrames()
	at := 0.05
	if t.s.Playing && t.s.Rate < 0 {
		at = 0.9
	}
	off := g.clampOffset(t.s.Project, float64(t.s.Playhead)-vis*at, t.zoom)
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
