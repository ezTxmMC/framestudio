package timeline

import (
	"math"
	"sort"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
)

// Fixed metrics of the timeline in device-independent pixels.
const (
	headerW    float32 = 132
	rulerH     float32 = 26
	videoRowH  float32 = 58 // default heights: V1, V2, A1, A2 fit a 300 px panel
	audioRowH  float32 = 48
	minRowH    float32 = 30 // track height limits when resizing by the header border
	maxRowH    float32 = 220
	tailH      float32 = 30 // empty strip below the last track: drop target for a new track
	borderPx   float32 = 3  // grab distance of a header's bottom border (track resize)
	scrollW    float32 = 5  // vertical scroll indicator width
	scrollHitW float32 = 10 // grab width of the scroll indicator at the right edge
	clipPadY   float32 = 2  // gap between a lane's edge and its clips
	nameBarH   float32 = 16 // coloured name strip at the top of a clip
	edgeGrab   float32 = 7  // trim handle width at each clip edge
	snapPx     float32 = 8  // snapping distance
	labelGap           = 96 // minimum distance between ruler labels

	minZoom float32 = 1
	maxZoom float32 = 60
)

// row is one track lane in display order.
type row struct {
	track int     // index into Project.Tracks
	y, h  float32 // widget coordinates, vertical scroll applied
}

// geom maps between frames, pixels and tracks for one widget size and view state.
type geom struct {
	w, h    float32
	ppf     float64 // pixels per frame
	offset  float64 // frame at the left edge of the lanes
	rows    []row
	content float32 // height of all rows together
	scroll  float32 // vertical scroll
}

// displayOrder lists track indices top to bottom: video from the top layer down to V1,
// then audio A1, A2... like Premiere.
func displayOrder(p *model.Project) []int {
	video := p.TrackIndices(model.TrackVideo)
	out := make([]int, 0, len(p.Tracks))
	for i := len(video) - 1; i >= 0; i-- {
		out = append(out, video[i])
	}
	return append(out, p.TrackIndices(model.TrackAudio)...)
}

// rowDefaults are the lane heights of tracks the user did not resize (settings
// Timeline.VideoTrackH / AudioTrackH).
type rowDefaults struct{ video, audio float32 }

var defaultRows = rowDefaults{videoRowH, audioRowH}

func (d rowDefaults) of(k model.TrackKind) float32 {
	h := d.video
	if k == model.TrackAudio {
		h = d.audio
	}
	if h <= 0 {
		return defaultRows.of(k)
	}
	return max(minRowH, min(h, maxRowH))
}

// fitFrames is the number of frames visible at zoom 1: the whole program plus some air,
// at least ten seconds.
func fitFrames(p *model.Project) float64 {
	return float64(max(p.Duration(), 10*p.SecondFrames())) * 1.05
}

// trackHeight is a track's lane height: the user's choice from heights, else the default.
func trackHeight(tr *model.Track, heights map[string]float32, d rowDefaults) float32 {
	if h, ok := heights[tr.ID]; ok {
		return max(minRowH, min(h, maxRowH))
	}
	return d.of(tr.Kind)
}

// newGeom computes the geometry for a widget of size w x h with default track heights.
// offset and vscroll are clamped.
func newGeom(p *model.Project, w, h, zoom float32, offset float64, vscroll float32) geom {
	return layoutGeom(p, w, h, zoom, offset, vscroll, nil, defaultRows)
}

// layoutGeom is newGeom with per-track heights (track ID -> height).
func layoutGeom(p *model.Project, w, h, zoom float32, offset float64, vscroll float32, heights map[string]float32, d rowDefaults) geom {
	g := geom{w: w, h: h}
	laneW := float64(max(w-headerW, 1))
	g.ppf = laneW / fitFrames(p) * float64(clampZoom(zoom))
	g.offset = g.clampOffset(p, offset, zoom)

	order := displayOrder(p)
	for _, ti := range order {
		g.content += trackHeight(&p.Tracks[ti], heights, d)
	}
	vscroll = max(0, min(vscroll, g.maxScroll()))
	y := rulerH - vscroll
	for _, ti := range order {
		rh := trackHeight(&p.Tracks[ti], heights, d)
		g.rows = append(g.rows, row{track: ti, y: y, h: rh})
		y += rh
	}
	g.scroll = vscroll
	return g
}

func clampZoom(z float32) float32 { return max(minZoom, min(z, maxZoom)) }

func (g geom) laneW() float32 { return max(g.w-headerW, 1) }

// visibleFrames is how many frames fit into the lane area.
func (g geom) visibleFrames() float64 { return float64(g.laneW()) / g.ppf }

// clampOffset keeps the view inside the program: at zoom 1 everything fits, otherwise the
// view may scroll until the end of the program reaches the middle of the lanes.
func (g geom) clampOffset(p *model.Project, offset float64, zoom float32) float64 {
	if clampZoom(zoom) <= minZoom || math.IsNaN(offset) {
		return 0
	}
	vis := g.visibleFrames()
	end := math.Max(float64(p.Duration()), fitFrames(p)/1.05)
	limit := math.Max(0, end-vis/2)
	return math.Max(0, math.Min(offset, limit))
}

// maxScroll is how far the tracks scroll vertically; the empty tail strip below the last
// track stays reachable as a drop target.
func (g geom) maxScroll() float32 { return max(0, g.content+tailH-(g.h-rulerH)) }

// vscroll returns the current (clamped) vertical scroll.
func (g geom) vscroll() float32 { return g.scroll }

// bodyH is the height of the lane area below the ruler.
func (g geom) bodyH() float32 { return max(g.h-rulerH, 0) }

// tailY is the widget y where the area below the last track starts.
func (g geom) tailY() float32 { return rulerH - g.scroll + g.content }

// scrollThumb returns the vertical scroll indicator's position and height in lane
// coordinates; ok is false when every track fits.
func (g geom) scrollThumb() (y, h float32, ok bool) {
	total := g.content + tailH
	body := g.bodyH()
	if g.maxScroll() <= 0 || total <= 0 || body <= 0 {
		return 0, 0, false
	}
	h = max(18, body*body/total)
	y = (body - h) * g.scroll / g.maxScroll()
	return y, h, true
}

// rowBorderAt returns the index of the row whose bottom border lies within borderPx of y
// (the grab zone for resizing a track), or -1.
func (g geom) rowBorderAt(y float32) int {
	if y < rulerH {
		return -1
	}
	for i, r := range g.rows {
		b := r.y + r.h
		if y >= b-borderPx && y <= b+borderPx {
			return i
		}
	}
	return -1
}

// clipRect returns the widget rectangle of clip c on row r.
func (g geom) clipRect(c model.Clip, r row) (x0, y0, x1, y1 float32) {
	x0, x1 = g.clipSpan(c)
	return x0, r.y + clipPadY, x1, r.y + r.h - clipPadY - 1
}

// clipsInRect returns the IDs of clips on unlocked tracks touched by the rectangle with
// corners a and b (widget coordinates), top to bottom and left to right.
func (g geom) clipsInRect(p *model.Project, a, b fyne.Position) []string {
	rx0, rx1 := min(a.X, b.X), max(a.X, b.X)
	ry0, ry1 := min(a.Y, b.Y), max(a.Y, b.Y)
	var ids []string
	for _, r := range g.rows {
		tr := &p.Tracks[r.track]
		if tr.Locked || r.y+r.h-clipPadY-1 < ry0 || r.y+clipPadY > ry1 {
			continue
		}
		for _, c := range tr.Clips {
			x0, y0, x1, y1 := g.clipRect(c, r)
			if x1 >= rx0 && x0 <= rx1 && y1 >= ry0 && y0 <= ry1 {
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

// frameX returns the widget x coordinate of frame f.
func (g geom) frameX(f float64) float32 {
	return headerW + float32((f-g.offset)*g.ppf)
}

// xFrame returns the (fractional) frame at widget x coordinate x.
func (g geom) xFrame(x float32) float64 {
	return g.offset + float64(x-headerW)/g.ppf
}

// frameAt returns the nearest whole frame at x, never negative.
func (g geom) frameAt(x float32) int {
	return max(0, int(math.Round(g.xFrame(x))))
}

// rowAt returns the index into g.rows at widget y, or -1.
func (g geom) rowAt(y float32) int {
	if y < rulerH {
		return -1
	}
	for i, r := range g.rows {
		if y >= r.y && y < r.y+r.h {
			return i
		}
	}
	return -1
}

// rowOfTrack returns the index into g.rows of track ti, or -1.
func (g geom) rowOfTrack(ti int) int {
	for i, r := range g.rows {
		if r.track == ti {
			return i
		}
	}
	return -1
}

// rulerStep chooses the labelled interval (major) and tick interval (minor) in frames so
// labels are at least minPx apart.
func rulerStep(ppf float64, fps int, minPx float64) (major, minor int) {
	fps = max(fps, 1)
	var cands []int
	for _, n := range []int{1, 2, 5, 10} {
		if n < fps {
			cands = append(cands, n)
		}
	}
	for _, s := range []int{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 18000} {
		cands = append(cands, s*fps)
	}
	major = cands[len(cands)-1]
	for _, c := range cands {
		if float64(c)*ppf >= minPx {
			major = c
			break
		}
	}
	minor = major
	for _, n := range []int{10, 5, 4, 2} {
		if major%n == 0 && float64(major/n)*ppf >= 7 {
			minor = major / n
			break
		}
	}
	return major, minor
}

type area int

const (
	areaNone area = iota
	areaCorner
	areaRuler
	areaHeader
	areaLane
)

type part int

const (
	partNone part = iota
	partBody
	partLeft
	partRight
)

type button int

const (
	btnNone button = iota
	btnEye
	btnLock
	btnMute
	btnSolo
)

// hit describes what lies under a pointer position.
type hit struct {
	area   area
	row    int // index into geom.rows or -1
	track  int // index into Project.Tracks or -1
	clip   int // index into the track's clips or -1
	part   part
	button button
	frame  float64
}

// headerButton is a toggle drawn in a track header.
type headerButton struct {
	b    button
	rect fyne.Position // top-left
	size fyne.Size
}

const (
	btnW float32 = 22
	btnH float32 = 18
	btnG float32 = 3
)

// headerButtons lays out the toggles of a track header, right aligned and vertically centred.
func headerButtons(k model.TrackKind, r row) []headerButton {
	bs := []button{btnEye, btnLock}
	if k == model.TrackAudio {
		bs = []button{btnMute, btnSolo, btnLock}
	}
	out := make([]headerButton, len(bs))
	x := headerW - 8 - float32(len(bs))*btnW - float32(len(bs)-1)*btnG
	y := r.y + (r.h-btnH)/2
	for i, b := range bs {
		out[i] = headerButton{b: b, rect: fyne.NewPos(x, y), size: fyne.NewSize(btnW, btnH)}
		x += btnW + btnG
	}
	return out
}

func (b headerButton) contains(p fyne.Position) bool {
	return p.X >= b.rect.X && p.X < b.rect.X+b.size.Width && p.Y >= b.rect.Y && p.Y < b.rect.Y+b.size.Height
}

// clipSpan returns the widget x range of a clip.
func (g geom) clipSpan(c model.Clip) (x0, x1 float32) {
	return g.frameX(float64(c.Start)), g.frameX(float64(c.End()))
}

// grabWidth is the trim handle width for a clip of the given pixel width, so short clips
// keep a movable middle.
func grabWidth(w float32) float32 { return min(edgeGrab, w/3) }

// hitTest finds what lies under pos.
func (g geom) hitTest(p *model.Project, pos fyne.Position) hit {
	h := hit{row: -1, track: -1, clip: -1, frame: g.xFrame(pos.X)}
	if pos.X < 0 || pos.Y < 0 || pos.X >= g.w || pos.Y >= g.h {
		return h
	}
	if pos.Y < rulerH {
		if pos.X < headerW {
			h.area = areaCorner
		} else {
			h.area = areaRuler
		}
		return h
	}
	h.row = g.rowAt(pos.Y)
	if h.row >= 0 {
		h.track = g.rows[h.row].track
	}
	if pos.X < headerW {
		h.area = areaHeader
		if h.track >= 0 {
			for _, b := range headerButtons(p.Tracks[h.track].Kind, g.rows[h.row]) {
				if b.contains(pos) {
					h.button = b.b
				}
			}
		}
		return h
	}
	h.area = areaLane
	if h.track < 0 {
		return h
	}
	for i, c := range p.Tracks[h.track].Clips {
		x0, x1 := g.clipSpan(c)
		if pos.X < x0 || pos.X >= x1 {
			continue
		}
		h.clip = i
		gw := grabWidth(x1 - x0)
		switch {
		case pos.X < x0+gw:
			h.part = partLeft
		case pos.X >= x1-gw:
			h.part = partRight
		default:
			h.part = partBody
		}
		break
	}
	return h
}

// snapTargets collects the frames edits snap to: playhead, clip edges (except those of
// the clips being edited), markers and the In/Out range.
func snapTargets(p *model.Project, playhead int, in, out int, exclude ...string) []int {
	skip := map[string]bool{}
	for _, id := range exclude {
		skip[id] = true
	}
	seen := map[int]bool{}
	var ts []int
	add := func(f int) {
		if f >= 0 && !seen[f] {
			seen[f] = true
			ts = append(ts, f)
		}
	}
	if playhead >= 0 {
		add(playhead)
	}
	add(0)
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			if !skip[c.ID] {
				add(c.Start)
				add(c.End())
			}
		}
	}
	for _, m := range p.Markers {
		add(m.Frame)
	}
	if out > in {
		add(in)
		add(out)
	}
	sort.Ints(ts)
	return ts
}

// snapFrame returns the target closest to f if it lies within thr frames.
func snapFrame(f int, targets []int, thr float64) (int, bool) {
	best, bestD := f, math.Inf(1)
	for _, t := range targets {
		if d := math.Abs(float64(t - f)); d <= thr && d < bestD {
			best, bestD = t, d
		}
	}
	return best, !math.IsInf(bestD, 1)
}

// snapSpan snaps a clip being moved by its start or its end, whichever is closer to a
// target. It returns the new start and the frame that was snapped to.
func snapSpan(start, length int, targets []int, thr float64) (newStart, at int, ok bool) {
	s, okS := snapFrame(start, targets, thr)
	e, okE := snapFrame(start+length, targets, thr)
	switch {
	case okS && (!okE || abs(s-start) <= abs(e-start-length)):
		return s, s, true
	case okE:
		return e - length, e, true
	}
	return start, 0, false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ContentHeight is the height the widget needs to show every track without vertical
// scrolling: ruler, all lanes at their current heights and the drop strip below them.
// Pages use it to size the timeline panel.
func (t *Timeline) ContentHeight() float32 {
	h := rulerH + tailH
	for i := range t.s.Project.Tracks {
		h += trackHeight(&t.s.Project.Tracks[i], t.heights, t.rows)
	}
	return h
}

// LanePoint is the widget position of frame on the middle of the lane of track trackID
// at the current zoom, scroll and track heights. ok is false for unknown tracks. Used by
// integration tests to drive drags and drops.
func (t *Timeline) LanePoint(frame float64, trackID string) (pos fyne.Position, ok bool) {
	g := t.geom()
	for _, r := range g.rows {
		if t.s.Project.Tracks[r.track].ID == trackID {
			return fyne.NewPos(g.frameX(frame), r.y+r.h/2), true
		}
	}
	return fyne.Position{}, false
}
