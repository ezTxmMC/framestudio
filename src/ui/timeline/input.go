package timeline

import (
	"math"
	"time"

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
	_ desktop.Mouseable      = (*Timeline)(nil)
)

type dragMode int

const (
	dragNone    dragMode = iota
	dragIgnore           // drag started somewhere without a drag action
	dragScrub            // ruler: move the playhead
	dragMarquee          // empty lane: rubber-band selection
	dragVScroll          // scroll indicator
	dragResize           // header bottom border: track height
	dragMove             // clip body: move the selection
	dragTrimL
	dragTrimR
	dragRippleL // ripple tool on a clip edge
	dragRippleR
	dragRoll // roll tool on a cut between two touching clips
)

// groupItem is one clip of a group being moved.
type groupItem struct {
	clip  model.Clip
	track int // track index when the drag started
}

// dragState tracks one mouse drag from press to release.
type dragState struct {
	mode  dragMode
	clip  model.Clip // grabbed clip as it was when the drag started
	from  int        // its track index
	grab  float64    // frame offset of the pointer from the clip start (trim right: end)
	lo    int        // trim limits: earliest start / latest end (ripple: delta, roll: cut)
	hi    int
	track int // preview: target track index of the grabbed clip
	start int // preview: start frame (trim)
	// length is the preview length (trim only).
	length int

	// group move
	group   []groupItem
	dFrames int
	dTrack  int // in tracks of the grabbed clip's kind

	// ripple / roll
	right   model.Clip     // roll: clip after the cut
	value   int            // ripple: delta, roll: cut frame
	edge    int            // frame of the edited edge, for the indicator
	preview *model.Project // result of the edit so far, drawn instead of the project

	// marquee
	press, cur fyne.Position
	base       []string // selection the marquee adds to (Ctrl/Shift), else empty
	orig       []string // selection before the marquee, restored by Esc

	// resize / scroll indicator
	trackID string
	y0, v0  float32
}

func (d *dragState) active() bool { return d.mode >= dragMove }

// stillValid reports whether a clip drag can go on after the project changed in the
// background (an import finishing, an edit on another page): every clip it works on must
// still be there, unchanged and on the same track. A ripple or roll preview is rebuilt on
// the next pointer move.
func (d *dragState) stillValid(p *model.Project) bool {
	same := func(c model.Clip, track int) bool {
		got, ti, _ := p.Clip(c.ID)
		return got != nil && ti == track && got.Start == c.Start && got.Length == c.Length && got.SourceIn == c.SourceIn
	}
	switch d.mode {
	case dragMove:
		for _, it := range d.group {
			if !same(it.clip, it.track) {
				return false
			}
		}
		return d.from < len(p.Tracks)
	case dragRoll:
		if !same(d.right, d.from) {
			return false
		}
	}
	if !same(d.clip, d.from) {
		return false
	}
	d.preview = nil
	return true
}

// grabbed is the clip start (trim right: end) that keeps the grab point under x.
func (d *dragState) grabbed(g geom, x float32) int {
	return int(math.Round(g.xFrame(x) - d.grab))
}

// doubleClick is the longest pause between the clicks of a double click.
const doubleClick = 450 * time.Millisecond

func (t *Timeline) ctrl() bool {
	return t.mods&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0
}
func (t *Timeline) shift() bool { return t.mods&fyne.KeyModifierShift != 0 }

// MouseDown records the modifiers of the press for the tap or drag that follows.
func (t *Timeline) MouseDown(ev *desktop.MouseEvent) { t.mods = ev.Modifier }
func (t *Timeline) MouseUp(*desktop.MouseEvent)      {}

// wheelMods returns the modifiers held right now: from the desktop driver when it knows
// them, else those of the last mouse event.
func (t *Timeline) wheelMods() fyne.KeyModifier {
	if app := fyne.CurrentApp(); app != nil {
		if d, ok := app.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return t.mods
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
		id := p.Tracks[h.track].ID
		if h.button != btnNone {
			t.toggle(id, h.button)
			return
		}
		now := time.Now()
		double := t.lastTap.track == id && now.Sub(t.lastTap.at) <= doubleClick
		t.lastTap.at, t.lastTap.track = now, id
		s.SelectTrack(id)
		if double && inNameArea(p.Tracks[h.track].Kind, g.rows[h.row], ev.Position) {
			t.lastTap.track = ""
			t.startRename(id)
		}
	case areaLane:
		if t.tool == ToolBlade {
			t.bladeAt(g, h, ev.Position.X)
			return
		}
		if h.clip >= 0 {
			t.clickClip(p.Tracks[h.track].Clips[h.clip].ID)
			return
		}
		if !t.ctrl() && !t.shift() {
			s.SelectClip("")
		}
		if h.track >= 0 {
			s.SelectTrack(p.Tracks[h.track].ID)
		}
		s.Seek(g.frameAt(ev.Position.X))
	}
}

// clickClip applies a click on a clip to the selection: plain click selects only it,
// Ctrl toggles it, Shift adds the clips between the primary clip and it on its track.
func (t *Timeline) clickClip(id string) {
	s := t.s
	switch {
	case t.ctrl():
		s.ToggleClip(id)
	case t.shift():
		s.SelectClips(append(append([]string{}, s.Selected...), rangeOnTrack(s.Project, s.SelectedClip, id)...))
	default:
		s.SelectClip(id)
	}
}

// rangeOnTrack lists the clips from clip a to clip b (inclusive, ending with b) when both
// lie on the same track; otherwise just b.
func rangeOnTrack(p *model.Project, a, b string) []string {
	ca, ta, ia := p.Clip(a)
	cb, tb, ib := p.Clip(b)
	if cb == nil {
		return nil
	}
	if ca == nil || ta != tb {
		return []string{b}
	}
	var out []string
	clips := p.Tracks[tb].Clips
	if ia <= ib {
		for i := ia; i <= ib; i++ {
			out = append(out, clips[i].ID)
		}
	} else {
		for i := ia; i >= ib; i-- {
			out = append(out, clips[i].ID)
		}
	}
	return out
}

// bladeAt handles a blade click: it splits the clip under x, or with "blade all tracks"
// every unlocked track at that frame.
func (t *Timeline) bladeAt(g geom, h hit, x float32) {
	p := t.s.Project
	if t.bladeAll {
		t.bladeAllAt(g, x)
		return
	}
	if h.clip >= 0 {
		t.blade(g, p.Tracks[h.track].Clips[h.clip], x)
	}
}

// blade splits c at the (snapped) frame under x. A snap onto the clip's own edges (for
// example the start of an abutting clip) is ignored: there is nothing to cut there.
func (t *Timeline) blade(g geom, c model.Clip, x float32) {
	raw := g.frameAt(x)
	f := t.snap(g, raw, true, c.ID)
	if f <= c.Start || f >= c.End() {
		f, t.snapAt = raw, -1
	}
	if f <= c.Start || f >= c.End() {
		return // on the clip's first frame: nothing to cut
	}
	t.edit("Clip teilen", func(p *model.Project) error {
		_, err := p.SplitClip(c.ID, f)
		return err
	})
}

// cuts counts the clips strictly containing frame f on unlocked and on locked tracks.
func cuts(p *model.Project, f int) (unlocked, locked int) {
	for _, tr := range p.Tracks {
		for _, c := range tr.Clips {
			if f > c.Start && f < c.End() {
				if tr.Locked {
					locked++
				} else {
					unlocked++
				}
			}
		}
	}
	return unlocked, locked
}

// bladeAllAt cuts every unlocked track at the (snapped) frame under x.
func (t *Timeline) bladeAllAt(g geom, x float32) {
	raw := g.frameAt(x)
	f := t.snap(g, raw, true)
	if n, _ := cuts(t.s.Project, f); n == 0 {
		f, t.snapAt = raw, -1
	}
	n, locked := cuts(t.s.Project, f)
	if n == 0 {
		if locked > 0 {
			t.fail(editError("Alle Spuren teilen", model.ErrLocked))
		}
		return
	}
	t.edit("Alle Spuren teilen", func(p *model.Project) error {
		p.SplitAll(f)
		return nil
	})
}

// snap applies snapping to frame f if enabled, ignoring the edges of the excluded clips.
func (t *Timeline) snap(g geom, f int, withPlayhead bool, exclude ...string) int {
	t.snapAt = -1
	if !t.snapping {
		return f
	}
	ph := t.s.Playhead
	if !withPlayhead {
		ph = -1
	}
	ts := snapTargets(t.s.Project, ph, t.s.In, t.s.Out, exclude...)
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
	d := &dragState{mode: dragIgnore, press: press, cur: press}
	if h.area == areaLane && press.X >= g.w-scrollHitW {
		if y, sh, ok := g.scrollThumb(); ok {
			d.mode, d.y0, d.v0 = dragVScroll, press.Y, g.scroll
			if ly := press.Y - rulerH; ly < y || ly > y+sh { // jump to the click first
				t.vscroll = max(0, min(g.maxScroll(), (ly-sh/2)/max(g.bodyH()-sh, 1)*g.maxScroll()))
				d.v0 = t.vscroll
			}
			return d
		}
	}
	if h.area == areaHeader {
		if ri := g.rowBorderAt(press.Y); ri >= 0 {
			r := g.rows[ri]
			d.mode, d.trackID, d.y0, d.v0 = dragResize, p.Tracks[r.track].ID, press.Y, r.h
		}
		return d
	}
	switch {
	case h.area == areaRuler:
		d.mode = dragScrub
		t.scrubbing = true
	case h.area == areaLane && h.clip < 0 && t.tool != ToolBlade:
		d.mode = dragMarquee
		d.orig = append([]string(nil), t.s.Selected...)
		if t.ctrl() || t.shift() {
			d.base = append([]string(nil), t.s.Selected...)
		}
	case h.area == areaLane && h.clip >= 0 && t.tool != ToolBlade:
		t.beginClipDrag(d, g, h, press)
	}
	return d
}

// beginClipDrag starts a move, trim, ripple or roll on the clip under the press.
func (t *Timeline) beginClipDrag(d *dragState, g geom, h hit, press fyne.Position) {
	p := t.s.Project
	tr := &p.Tracks[h.track]
	c := tr.Clips[h.clip]
	d.clip, d.from, d.track, d.start, d.length = c, h.track, h.track, c.Start, c.Length
	d.grab = g.xFrame(press.X) - float64(c.Start)

	if h.part == partBody {
		switch {
		case t.ctrl() && !t.s.IsSelected(c.ID):
			t.s.ToggleClip(c.ID)
		case t.shift():
			t.clickClip(c.ID)
		case !t.s.IsSelected(c.ID):
			t.s.SelectClip(c.ID)
		}
		ids := t.s.Selected
		if !t.s.IsSelected(c.ID) { // Ctrl/Shift may have removed it: move just the grabbed clip
			ids = []string{c.ID}
		}
		label := moveLabel(len(ids))
		for _, id := range ids {
			gc, ti, _ := p.Clip(id)
			if gc == nil {
				continue
			}
			if p.Tracks[ti].Locked {
				t.fail(editError(label, model.ErrLocked))
				return
			}
			d.group = append(d.group, groupItem{clip: *gc, track: ti})
		}
		d.mode = dragMove
		return
	}

	if !t.s.IsSelected(c.ID) {
		t.s.SelectClip(c.ID)
	}
	left := h.part == partLeft
	label := "Clip trimmen"
	if tr.Locked {
		t.fail(editError(label, model.ErrLocked))
		return
	}
	switch t.tool {
	case ToolRipple:
		d.mode = dragRippleR
		if left {
			d.mode = dragRippleL
		}
		d.lo, d.hi = rippleLimits(p, c.ID, left)
		d.edge = c.Start
		if !left {
			d.edge = c.End()
			d.grab -= float64(c.Length)
		}
		return
	case ToolRoll:
		if a, b, ok := rollNeighbours(tr, h.clip, left); ok {
			d.mode = dragRoll
			d.clip, d.right = tr.Clips[a], tr.Clips[b]
			d.value, d.edge = d.right.Start, d.right.Start
			d.grab = g.xFrame(press.X) - float64(d.right.Start)
			d.lo, d.hi = rollLimits(p, d.clip.ID, d.right.ID)
			return
		}
	}
	d.lo, d.hi = trimLimits(p, tr, h.clip)
	if left {
		d.mode = dragTrimL
	} else {
		d.mode = dragTrimR
		d.grab -= float64(c.Length)
	}
}

func moveLabel(n int) string {
	if n > 1 {
		return "Clips verschieben"
	}
	return "Clip verschieben"
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

// kindPos returns the position of track ti among the tracks of its kind, or -1.
func kindPos(p *model.Project, ti int) int {
	for i, x := range p.TrackIndices(p.Tracks[ti].Kind) {
		if x == ti {
			return i
		}
	}
	return -1
}

// shiftTrack returns the track dTrack tracks of the same kind away from ti, or -1.
func shiftTrack(p *model.Project, ti, dTrack int) int {
	same := p.TrackIndices(p.Tracks[ti].Kind)
	i := kindPos(p, ti) + dTrack
	if i < 0 || i >= len(same) {
		return -1
	}
	return same[i]
}

// groupTarget is the track a group member lands on: members of the grabbed clip's track
// kind follow the vertical move, the others stay on their tracks.
func (d *dragState) groupTarget(p *model.Project, it groupItem) int {
	if p.Tracks[it.track].Kind != p.Tracks[d.from].Kind {
		return it.track
	}
	return shiftTrack(p, it.track, d.dTrack)
}

// validShift reports whether every member of the group can move dTrack tracks.
func (d *dragState) validShift(p *model.Project, dTrack int) bool {
	kind := p.Tracks[d.from].Kind
	for _, it := range d.group {
		if p.Tracks[it.track].Kind != kind {
			continue
		}
		ti := shiftTrack(p, it.track, dTrack)
		if ti < 0 || p.Tracks[ti].Locked {
			return false
		}
	}
	return true
}

func (t *Timeline) updateDrag(pos fyne.Position) {
	d := t.drag
	p := t.s.Project
	g := t.geom()
	d.cur = pos
	switch d.mode {
	case dragScrub:
		t.s.Seek(t.snap(g, g.frameAt(pos.X), false))
		return
	case dragMarquee:
		ids := append(append([]string{}, d.base...), g.clipsInRect(p, d.press, pos)...)
		t.s.SelectGroup(ids) // keeps a picture clip primary when the band reaches audio
	case dragVScroll:
		_, sh, _ := g.scrollThumb()
		span := max(g.bodyH()-sh, 1)
		t.vscroll = max(0, min(g.maxScroll(), d.v0+(pos.Y-d.y0)/span*g.maxScroll()))
	case dragResize:
		t.heights[d.trackID] = max(minRowH, min(maxRowH, d.v0+pos.Y-d.y0))
	case dragMove:
		t.updateMove(g, pos)
	case dragTrimL:
		end := d.clip.End()
		f := t.snap(g, d.grabbed(g, pos.X), true, d.clip.ID)
		f = max(d.lo, min(f, end-1))
		d.start, d.length = f, end-f
	case dragTrimR:
		f := t.snap(g, d.grabbed(g, pos.X), true, d.clip.ID)
		f = min(d.hi, max(f, d.clip.Start+1))
		d.start, d.length = d.clip.Start, f-d.clip.Start
	case dragRippleL, dragRippleR:
		f := t.snap(g, d.grabbed(g, pos.X), true, d.clip.ID)
		left := d.mode == dragRippleL
		delta := f - d.clip.End()
		if left {
			delta = f - d.clip.Start
		}
		delta = max(d.lo, min(delta, d.hi))
		if delta == d.value && d.preview != nil {
			return
		}
		d.value = delta
		d.edge = d.clip.End() + delta
		if left {
			d.edge = d.clip.Start + delta
		}
		d.preview = p.Clone()
		if rippleTrimSync(d.preview, d.clip.ID, left, delta, t.RippleSync()) != nil {
			d.preview = nil
		}
	case dragRoll:
		f := t.snap(g, d.grabbed(g, pos.X), true, d.clip.ID, d.right.ID)
		cut := max(d.lo, min(f, d.hi))
		if cut == d.value && d.preview != nil {
			return
		}
		d.value, d.edge = cut, cut
		d.preview = p.Clone()
		if rollEdit(d.preview, d.clip.ID, d.right.ID, cut) != nil {
			d.preview = nil
		}
	default:
		return
	}
	t.Refresh()
}

// updateMove follows the pointer with the group: horizontal offset from the grabbed clip
// (snapped by its edges), vertical offset in tracks of its kind.
func (t *Timeline) updateMove(g geom, pos fyne.Position) {
	d := t.drag
	p := t.s.Project
	minStart := d.clip.Start
	ids := make([]string, 0, len(d.group))
	for _, it := range d.group {
		minStart = min(minStart, it.clip.Start)
		ids = append(ids, it.clip.ID)
	}
	start := d.grabbed(g, pos.X)
	t.snapAt = -1
	if t.snapping {
		ts := snapTargets(p, t.s.Playhead, t.s.In, t.s.Out, ids...)
		if s, at, ok := snapSpan(start, d.clip.Length, ts, float64(snapPx)/g.ppf); ok {
			start, t.snapAt = s, at
		}
	}
	d.dFrames = max(start-d.clip.Start, -minStart)
	if d.dFrames != start-d.clip.Start {
		t.snapAt = -1
	}
	d.start = d.clip.Start + d.dFrames
	if ri := g.rowAt(pos.Y); ri >= 0 {
		ti := g.rows[ri].track
		if p.Tracks[ti].Kind == p.Tracks[d.from].Kind {
			if dt := kindPos(p, ti) - kindPos(p, d.from); d.validShift(p, dt) {
				d.dTrack = dt
			}
		}
	}
	if ti := shiftTrack(p, d.from, d.dTrack); ti >= 0 {
		d.track = ti
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
		if d.dFrames == 0 && d.dTrack == 0 {
			break
		}
		p := t.s.Project
		kind := p.Tracks[d.from].Kind
		var vert, rest []string
		for _, it := range d.group {
			if p.Tracks[it.track].Kind == kind {
				vert = append(vert, it.clip.ID)
			} else {
				rest = append(rest, it.clip.ID)
			}
		}
		dF, dT := d.dFrames, d.dTrack
		t.edit(moveLabel(len(d.group)), func(p *model.Project) error {
			if err := p.MoveClips(vert, dF, dT); err != nil {
				return err
			}
			if len(rest) > 0 {
				return p.MoveClips(rest, dF, 0)
			}
			return nil
		})
	case dragTrimL, dragTrimR:
		if d.start != d.clip.Start || d.length != d.clip.Length {
			t.edit("Clip trimmen", func(p *model.Project) error {
				return p.TrimClip(d.clip.ID, d.start, d.length)
			})
		}
	case dragRippleL, dragRippleR:
		if d.value != 0 {
			left, delta := d.mode == dragRippleL, d.value
			t.edit("Ripple-Trimmen", func(p *model.Project) error {
				return rippleTrimSync(p, d.clip.ID, left, delta, t.RippleSync())
			})
		}
	case dragRoll:
		if d.value != d.right.Start {
			t.edit("Schnitt verschieben", func(p *model.Project) error {
				return rollEdit(p, d.clip.ID, d.right.ID, d.value)
			})
		}
	case dragScrub:
		t.follow()
	}
	t.Refresh()
}

// Scrolled pans the lanes horizontally; over the header column (or with Shift) it scrolls
// the tracks vertically, and with Ctrl it zooms around the pointer.
func (t *Timeline) Scrolled(ev *fyne.ScrollEvent) {
	g := t.geom()
	mods := t.wheelMods()
	if mods&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0 {
		d := ev.Scrolled.DY
		if d == 0 {
			d = ev.Scrolled.DX
		}
		t.zoomAt(t.zoom*float32(math.Exp(float64(d)*0.02)), ev.Position.X)
		return
	}
	if (ev.Position.X < headerW && ev.Position.Y >= rulerH) || mods&fyne.KeyModifierShift != 0 {
		if g.maxScroll() <= 0 {
			return
		}
		t.vscroll = max(0, min(t.vscroll-ev.Scrolled.DY-ev.Scrolled.DX, g.maxScroll()))
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
	t.mods = ev.Modifier
	t.hover = t.geom().hitTest(t.s.Project, ev.Position)
	t.hoverPos = ev.Position
}
func (t *Timeline) MouseOut() { t.hover = hit{} }

func (t *Timeline) Cursor() desktop.Cursor {
	if d := t.drag; d != nil {
		switch d.mode {
		case dragTrimL, dragTrimR, dragRippleL, dragRippleR, dragRoll:
			return desktop.HResizeCursor
		case dragResize:
			return desktop.VResizeCursor
		case dragMarquee:
			return desktop.CrosshairCursor
		}
		return desktop.DefaultCursor
	}
	h := t.hover
	switch {
	case h.area == areaHeader && h.button == btnNone && t.geom().rowBorderAt(t.hoverPos.Y) >= 0:
		return desktop.VResizeCursor
	case h.area == areaLane && t.tool == ToolBlade:
		return desktop.CrosshairCursor
	case h.area == areaLane && (h.part == partLeft || h.part == partRight):
		// select, ripple and roll all grab edges; Fyne offers a single resize cursor
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
