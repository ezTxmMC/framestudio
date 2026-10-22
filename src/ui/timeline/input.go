package timeline

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"framestudio-proto/src/model"
)

var (
	_ fyne.Tappable          = (*Timeline)(nil)
	_ fyne.SecondaryTappable = (*Timeline)(nil)
	_ fyne.Draggable         = (*Timeline)(nil)
	_ fyne.Scrollable        = (*Timeline)(nil)
	_ desktop.Hoverable      = (*Timeline)(nil)
	_ desktop.Cursorable     = (*Timeline)(nil)
)

type dragMode int

const (
	dragNone   dragMode = iota
	dragIgnore          // drag started somewhere without a drag action
	dragScrub
	dragMove
	dragTrimL
	dragTrimR
)

// dragState tracks one mouse drag from press to release.
type dragState struct {
	mode  dragMode
	clip  model.Clip // clip as it was when the drag started
	from  int        // its track index
	grab  float64    // frame offset of the pointer from the clip start (trim right: end)
	lo    int        // trim limits: earliest start / latest end
	hi    int
	track int // preview: target track index
	start int // preview: start frame
	// length is the preview length (trim only).
	length int
}

func (d *dragState) active() bool { return d.mode >= dragMove }

// grabbed is the clip start (trim right: end) that keeps the grab point under x.
func (d *dragState) grabbed(g geom, x float32) int {
	return int(math.Round(g.xFrame(x) - d.grab))
}

func (t *Timeline) Tapped(ev *fyne.PointEvent) {
	p, s := t.s.Project, t.s
	g := t.geom()
	h := g.hitTest(p, ev.Position)
	switch h.area {
	case areaRuler:
		s.Seek(g.frameAt(ev.Position.X))
	case areaHeader:
		if h.track < 0 {
			return
		}
		if h.button != btnNone {
			t.toggle(p.Tracks[h.track].ID, h.button)
			return
		}
		s.SelectTrack(p.Tracks[h.track].ID)
	case areaLane:
		if h.clip >= 0 {
			c := p.Tracks[h.track].Clips[h.clip]
			if t.tool == ToolBlade {
				t.blade(g, c, ev.Position.X)
				return
			}
			s.SelectClip(c.ID)
			return
		}
		s.SelectClip("")
		if h.track >= 0 {
			s.SelectTrack(p.Tracks[h.track].ID)
		}
		s.Seek(g.frameAt(ev.Position.X))
	}
}

// blade splits c at the (snapped) frame under x.
func (t *Timeline) blade(g geom, c model.Clip, x float32) {
	f := t.snap(g, g.frameAt(x), c.ID, true)
	t.edit("Clip teilen", func(p *model.Project) error {
		_, err := p.SplitClip(c.ID, f)
		return err
	})
}

// snap applies snapping to frame f if enabled, ignoring the edges of clip exclude.
func (t *Timeline) snap(g geom, f int, exclude string, withPlayhead bool) int {
	t.snapAt = -1
	if !t.snapping {
		return f
	}
	ph := t.s.Playhead
	if !withPlayhead {
		ph = -1
	}
	ts := snapTargets(t.s.Project, ph, t.s.In, t.s.Out, exclude)
	if s, ok := snapFrame(f, ts, float64(snapPx)/g.ppf); ok {
		t.snapAt = s
		return s
	}
	return f
}

// toggle flips a track switch as one undo step.
func (t *Timeline) toggle(trackID string, b button) {
	tr, _ := t.s.Project.Track(trackID)
	if tr == nil {
		return
	}
	t.edit(toggleLabel(tr, b), func(p *model.Project) error {
		tr, _ := p.Track(trackID)
		if tr == nil {
			return model.ErrNotFound
		}
		switch b {
		case btnEye:
			tr.Hidden = !tr.Hidden
		case btnLock:
			tr.Locked = !tr.Locked
		case btnMute:
			tr.Muted = !tr.Muted
		case btnSolo:
			tr.Solo = !tr.Solo
		}
		return nil
	})
}

// toggleLabel names the undo step for flipping switch b of tr.
func toggleLabel(tr *model.Track, b button) string {
	pick := func(on bool, off, onLabel string) string {
		if on {
			return onLabel
		}
		return off
	}
	switch b {
	case btnEye:
		return pick(tr.Hidden, "Spur ausblenden", "Spur einblenden")
	case btnLock:
		return pick(tr.Locked, "Spur sperren", "Spur entsperren")
	case btnMute:
		return pick(tr.Muted, "Spur stummschalten", "Stummschaltung aufheben")
	}
	return pick(tr.Solo, "Spur solo schalten", "Solo aufheben")
}

func (t *Timeline) Dragged(ev *fyne.DragEvent) {
	if t.drag == nil {
		press := fyne.NewPos(ev.Position.X-ev.Dragged.DX, ev.Position.Y-ev.Dragged.DY)
		t.drag = t.beginDrag(press)
	}
	t.updateDrag(ev.Position)
}

// beginDrag decides what a drag starting at press does.
func (t *Timeline) beginDrag(press fyne.Position) *dragState {
	p := t.s.Project
	g := t.geom()
	h := g.hitTest(p, press)
	d := &dragState{mode: dragIgnore}
	switch {
	case h.area == areaRuler:
		d.mode = dragScrub
		t.scrubbing = true
	case h.area == areaLane && h.clip >= 0 && t.tool == ToolSelect:
		tr := &p.Tracks[h.track]
		c := tr.Clips[h.clip]
		t.s.SelectClip(c.ID)
		label := "Clip verschieben"
		if h.part != partBody {
			label = "Clip trimmen"
		}
		if tr.Locked {
			t.fail(editError(label, model.ErrLocked))
			return d
		}
		d.clip, d.from, d.track, d.start, d.length = c, h.track, h.track, c.Start, c.Length
		d.grab = g.xFrame(press.X) - float64(c.Start)
		d.lo, d.hi = trimLimits(p, tr, h.clip)
		switch h.part {
		case partLeft:
			d.mode = dragTrimL
		case partRight:
			d.mode = dragTrimR
			d.grab -= float64(c.Length)
		default:
			d.mode = dragMove
		}
	}
	return d
}

// trimLimits returns how far clip ci may be extended without overwriting its neighbours or
// running past the ends of its source media.
func trimLimits(p *model.Project, tr *model.Track, ci int) (lo, hi int) {
	c := tr.Clips[ci]
	lo, hi = 0, math.MaxInt32
	if ci > 0 {
		lo = tr.Clips[ci-1].End()
	}
	if ci+1 < len(tr.Clips) {
		hi = tr.Clips[ci+1].Start
	}
	if c.MediaID != "" {
		if m, ok := p.MediaByID(c.MediaID); ok && m.Duration > 0 {
			lo = max(lo, c.Start-c.SourceIn)
			hi = min(hi, c.Start+p.MediaFrames(*m)-c.SourceIn)
		}
	}
	return lo, hi
}

func (t *Timeline) updateDrag(pos fyne.Position) {
	d := t.drag
	p := t.s.Project
	g := t.geom()
	switch d.mode {
	case dragScrub:
		t.s.Seek(t.snap(g, g.frameAt(pos.X), "", false))
	case dragMove:
		start := max(0, d.grabbed(g, pos.X))
		t.snapAt = -1
		if t.snapping {
			ts := snapTargets(p, t.s.Playhead, t.s.In, t.s.Out, d.clip.ID)
			if s, at, ok := snapSpan(start, d.clip.Length, ts, float64(snapPx)/g.ppf); ok && s >= 0 {
				start, t.snapAt = s, at
			}
		}
		d.start = start
		if ri := g.rowAt(pos.Y); ri >= 0 {
			ti := g.rows[ri].track
			if tr := &p.Tracks[ti]; tr.Kind.Accepts(d.clip.Kind) && !tr.Locked {
				d.track = ti
			} else {
				d.track = d.from
			}
		}
	case dragTrimL:
		end := d.clip.End()
		f := t.snap(g, d.grabbed(g, pos.X), d.clip.ID, true)
		f = max(d.lo, min(f, end-1))
		d.start, d.length = f, end-f
	case dragTrimR:
		f := t.snap(g, d.grabbed(g, pos.X), d.clip.ID, true)
		f = min(d.hi, max(f, d.clip.Start+1))
		d.start, d.length = d.clip.Start, f-d.clip.Start
	default:
		return
	}
	if d.mode != dragScrub {
		t.Refresh()
	}
}

func (t *Timeline) DragEnd() {
	d := t.drag
	t.drag, t.snapAt, t.scrubbing = nil, -1, false
	if d == nil {
		return
	}
	switch d.mode {
	case dragMove:
		if d.track != d.from || d.start != d.clip.Start {
			to := t.s.Project.Tracks[d.track].ID
			t.edit("Clip verschieben", func(p *model.Project) error {
				return p.MoveClip(d.clip.ID, to, d.start)
			})
		}
	case dragTrimL, dragTrimR:
		if d.start != d.clip.Start || d.length != d.clip.Length {
			t.edit("Clip trimmen", func(p *model.Project) error {
				return p.TrimClip(d.clip.ID, d.start, d.length)
			})
		}
	case dragScrub:
		t.follow()
	}
	t.Refresh()
}

// Scrolled pans the lanes horizontally, or scrolls the tracks vertically over the header column.
func (t *Timeline) Scrolled(ev *fyne.ScrollEvent) {
	g := t.geom()
	if ev.Position.X < headerW && ev.Position.Y >= rulerH {
		if g.maxScroll() <= 0 {
			return
		}
		t.vscroll = max(0, min(t.vscroll-ev.Scrolled.DY, g.maxScroll()))
		t.Refresh()
		return
	}
	delta := float64(ev.Scrolled.DX + ev.Scrolled.DY)
	off := g.clampOffset(t.s.Project, t.offset-delta*1.5/g.ppf, t.zoom)
	if off != t.offset {
		t.offset = off
		t.Refresh()
	}
}

func (t *Timeline) MouseIn(ev *desktop.MouseEvent) { t.MouseMoved(ev) }
func (t *Timeline) MouseMoved(ev *desktop.MouseEvent) {
	t.hover = t.geom().hitTest(t.s.Project, ev.Position)
}
func (t *Timeline) MouseOut() { t.hover = hit{} }

func (t *Timeline) Cursor() desktop.Cursor {
	if d := t.drag; d != nil {
		if d.mode == dragTrimL || d.mode == dragTrimR {
			return desktop.HResizeCursor
		}
		return desktop.DefaultCursor
	}
	h := t.hover
	switch {
	case h.area == areaLane && t.tool == ToolBlade:
		return desktop.CrosshairCursor
	case h.area == areaLane && (h.part == partLeft || h.part == partRight):
		if tr := t.trackAt(h.track); tr != nil && !tr.Locked {
			return desktop.HResizeCursor
		}
	case h.area == areaHeader && h.button != btnNone:
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (t *Timeline) trackAt(i int) *model.Track {
	if i < 0 || i >= len(t.s.Project.Tracks) {
		return nil
	}
	return &t.s.Project.Tracks[i]
}
