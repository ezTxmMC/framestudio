package audio

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/model"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

const (
	headerW = 176 // track header column
	rowH    = 76  // track row height
	rulerH  = 28
	stripH  = 15 // clip name strip
)

// trackView is the "Spuren" area: a ruler, one row per audio track with header and
// waveform lane, and a playhead spanning all rows. All rows share one time scale.
type trackView struct {
	pg     *Page
	rows   map[string]*trackRow
	order  []string
	list   *fyne.Container
	ruler  *ruler
	grid   *gridView
	head   *playhead
	tc     *canvas.Text
	empty  *fyne.Container
	scroll *container.Scroll
	obj    fyne.CanvasObject
}

func newTrackView(pg *Page) *trackView {
	tv := &trackView{pg: pg, rows: map[string]*trackRow{}, list: container.NewVBox()}
	tv.ruler = newRuler(pg)
	tv.grid = newGridView(pg)
	tv.head = newPlayhead(pg)
	tv.tc = kit.MonoText("00:00:00:00", studiotheme.Text, 14)

	hint := canvas.NewText("Keine Audiospuren. Im Mixer mit „Spur hinzufügen“ eine anlegen.", studiotheme.Muted)
	hint.TextSize = 12
	tv.empty = container.NewCenter(hint)

	cornerBg := canvas.NewRectangle(studiotheme.Panel)
	corner := container.NewStack(cornerBg, fixedSize(headerW, rulerH),
		container.New(layout.NewCustomPaddedLayout(0, 0, 10, 0), container.NewHBox(container.NewCenter(tv.tc))))
	top := container.NewBorder(nil, hline(), corner, nil, tv.ruler)

	headerCol := canvas.NewRectangle(studiotheme.Panel)
	headerCol.SetMinSize(fyne.NewSize(headerW, 0))
	backdrop := container.NewBorder(nil, nil, headerCol, nil, tv.grid)
	tv.scroll = container.NewVScroll(tv.list)
	body := container.NewStack(backdrop, tv.scroll, tv.empty)

	tv.obj = container.NewStack(canvas.NewRectangle(studiotheme.Lane),
		container.NewBorder(top, nil, nil, nil, body), tv.head)
	return tv
}

func hline() *canvas.Rectangle {
	l := canvas.NewRectangle(studiotheme.Border)
	l.SetMinSize(fyne.NewSize(1, 1))
	return l
}

// update reconciles rows with the project's audio tracks and redraws everything.
func (tv *trackView) update() {
	p := tv.pg.s.Project
	var ids []string
	for _, ti := range p.TrackIndices(model.TrackAudio) {
		ids = append(ids, p.Tracks[ti].ID)
	}
	if !slices.Equal(ids, tv.order) {
		objs := make([]fyne.CanvasObject, 0, len(ids))
		for _, id := range ids {
			r := tv.rows[id]
			if r == nil {
				r = newTrackRow(tv, id)
				tv.rows[id] = r
			}
			objs = append(objs, r.obj)
		}
		for id := range tv.rows {
			if !slices.Contains(ids, id) {
				delete(tv.rows, id)
			}
		}
		tv.order = ids
		tv.list.Objects = objs
		tv.list.Refresh()
	}
	for _, r := range tv.rows {
		r.update()
	}
	if len(ids) == 0 {
		tv.empty.Show()
	} else {
		tv.empty.Hide()
	}
	tv.ruler.Refresh()
	tv.grid.Refresh()
	tv.updatePlayhead()
}

func (tv *trackView) refreshLanes() {
	for _, r := range tv.rows {
		r.lane.Refresh()
	}
}

func (tv *trackView) updatePlayhead() {
	s := tv.pg.s
	if tc := model.Timecode(s.Playhead, s.FPS()); tv.tc.Text != tc {
		tv.tc.Text = tc
		tv.tc.Refresh()
	}
	tv.head.Refresh()
	tv.ruler.Refresh()
}

// trackRow is one audio track: header with name and M/S/lock, and its waveform lane.
type trackRow struct {
	tv               *trackView
	id               string
	name, info       *canvas.Text
	bg, accent       *canvas.Rectangle
	mute, solo, lock *toggle
	lane             *lane
	obj              fyne.CanvasObject
}

func newTrackRow(tv *trackView, id string) *trackRow {
	r := &trackRow{tv: tv, id: id}
	s := tv.pg.s
	r.name = canvas.NewText("", studiotheme.Text)
	r.name.TextSize = 13
	r.name.TextStyle = fyne.TextStyle{Bold: true}
	r.info = canvas.NewText("", studiotheme.Muted)
	r.info.TextSize = 11
	r.mute = newToggle("M", studiotheme.Steel, func() { tv.pg.toggleMute(id) })
	r.solo = newToggle("S", amber, func() { tv.pg.toggleSolo(id) })
	r.lock = newIconToggle(lockOff, lockOn, studiotheme.Steel, func() { tv.pg.toggleLock(id) })
	r.bg = canvas.NewRectangle(studiotheme.Panel)
	r.accent = canvas.NewRectangle(studiotheme.Steel)
	r.accent.SetMinSize(fyne.NewSize(3, 0))

	text := container.New(layout.NewCustomPaddedLayout(0, 0, 0, 0), container.NewVBox(r.name, r.info))
	toggles := container.NewHBox(r.mute, r.solo, r.lock)
	inner := container.NewBorder(text, toggles, nil, nil)
	vline := canvas.NewRectangle(studiotheme.Border)
	vline.SetMinSize(fyne.NewSize(1, 0))
	header := newTapArea(container.NewStack(r.bg, fixedSize(headerW, rowH),
		container.NewBorder(nil, nil, r.accent, vline,
			container.New(layout.NewCustomPaddedLayout(7, 8, 9, 6), inner))),
		func() { s.SelectTrack(id) })

	r.lane = newLane(r)
	r.obj = container.NewBorder(nil, hline(), header, nil, r.lane)
	return r
}

func (r *trackRow) update() {
	s := r.tv.pg.s
	t, _ := s.Project.Track(r.id)
	if t == nil {
		return
	}
	r.name.Text = fitText(t.Name, 13, r.name.TextStyle, headerW-24)
	clips := "Keine Clips"
	switch n := len(t.Clips); n {
	case 0:
	case 1:
		clips = "1 Clip"
	default:
		clips = fmt.Sprintf("%d Clips", n)
	}
	r.info.Text = clips + "  ·  " + FormatDB(LinearToDB(t.Volume))
	r.mute.SetOn(t.Muted)
	r.solo.SetOn(t.Solo)
	r.lock.SetOn(t.Locked)
	if s.SelectedTrack == r.id {
		r.accent.FillColor, r.bg.FillColor = studiotheme.Steel, studiotheme.Raised
	} else {
		r.accent.FillColor, r.bg.FillColor = color.Transparent, studiotheme.Panel
	}
	r.name.Refresh()
	r.info.Refresh()
	r.accent.Refresh()
	r.bg.Refresh()
	r.lane.Refresh()
}

// audible reports whether a track is heard given mute and solo states of all tracks.
func audible(p *model.Project, t *model.Track) bool {
	if t.Muted {
		return false
	}
	for _, ti := range p.TrackIndices(model.TrackAudio) {
		if p.Tracks[ti].Solo {
			return t.Solo
		}
	}
	return true
}

// lane draws the clips of one track with their waveforms.
type lane struct {
	widget.BaseWidget
	row *trackRow
}

func newLane(r *trackRow) *lane {
	l := &lane{row: r}
	l.ExtendBaseWidget(l)
	return l
}

func (l *lane) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(l, fyne.NewSize(80, rowH), l.build)
}

// Tapped selects the clip under the pointer, or seeks there if the lane is empty.
func (l *lane) Tapped(e *fyne.PointEvent) {
	pg := l.row.tv.pg
	t, _ := pg.s.Project.Track(l.row.id)
	if t == nil {
		return
	}
	f := pg.frameAt(e.Position.X, l.Size().Width)
	if ci := t.ClipAt(f); ci >= 0 {
		pg.s.SelectClip(t.Clips[ci].ID)
		return
	}
	pg.s.SelectClip("")
	pg.s.SelectTrack(t.ID)
	pg.s.Seek(f)
}

func (l *lane) build(size fyne.Size) []fyne.CanvasObject {
	pg := l.row.tv.pg
	p := pg.s.Project
	t, _ := p.Track(l.row.id)
	if t == nil || size.Width <= 0 {
		return nil
	}
	on := audible(p, t)
	var objs []fyne.CanvasObject
	top, h := float32(4), size.Height-8
	for _, c := range t.Clips {
		x0, x1 := pg.xOf(c.Start, size.Width), pg.xOf(c.End(), size.Width)
		if x0 >= size.Width {
			break
		}
		w := max(x1-x0, 3)
		base := c.Color.NRGBA()
		base.A = 0xFF
		if !on {
			base = mix(base, studiotheme.Faint, 0.75)
		}
		body := canvas.NewRectangle(mix(studiotheme.Lane, base, 0.3))
		strip := canvas.NewRectangle(mix(studiotheme.Lane, base, 0.85))
		objs = append(objs, place(body, x0, top, w, h), place(strip, x0, top, w, stripH))

		waveTop := top + stripH
		entry, m := pg.waveFor(c)
		wave := canvas.NewRaster(waveRaster(c, entry, p, m, mix(base, color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}, 0.45)))
		objs = append(objs, place(wave, x0+1, waveTop+2, max(w-2, 1), h-stripH-4))
		if m == nil && c.MediaID != "" && w > 60 {
			miss := canvas.NewText(fitText("Medium fehlt", 11, fyne.TextStyle{}, w-12), studiotheme.Muted)
			miss.TextSize = 11
			objs = append(objs, place(miss, x0+6, waveTop+4, w-12, 14))
		}

		if label := fitText(c.Name, 11, fyne.TextStyle{}, w-10); label != "" {
			name := canvas.NewText(label, studiotheme.Text)
			name.TextSize = 11
			objs = append(objs, place(name, x0+5, top+1, w-10, stripH-2))
		}
		frame := canvas.NewRectangle(color.Transparent)
		frame.StrokeWidth = 1
		frame.StrokeColor = mix(base, color.NRGBA{A: 0xFF}, 0.35)
		if pg.s.SelectedClip == c.ID {
			frame.StrokeWidth = 2
			frame.StrokeColor = studiotheme.Steel
		}
		objs = append(objs, place(frame, x0, top, w, h))
	}
	return objs
}

// waveRaster draws the clip's source range of the media waveform, shaped by fades and gain.
func waveRaster(c model.Clip, e *waveEntry, p *model.Project, m *model.MediaItem, col color.NRGBA) func(w, h int) image.Image {
	var peaks []float32
	if e != nil {
		peaks = e.peaks
	}
	mf := c.SourceIn + c.Length
	if m != nil {
		if n := p.MediaFrames(*m); n > 0 {
			mf = n
		}
	}
	gain := math.Min(c.Volume, 4)
	fades := c.FadeIn > 0 || c.FadeOut > 0
	centre := color.NRGBA{col.R, col.G, col.B, 0x50}
	env := color.NRGBA{0xFF, 0xFF, 0xFF, 0xB0}
	return func(w, h int) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		if w <= 0 || h <= 0 {
			return img
		}
		mid := float64(h-1) / 2
		for x := range w {
			t0 := float64(x) / float64(w) * float64(c.Length)
			t1 := float64(x+1) / float64(w) * float64(c.Length)
			g := c.FadeFactor(c.Start + int(t0))
			img.SetNRGBA(x, int(mid), centre)
			if peaks != nil {
				a := float64(peakIn(peaks, (float64(c.SourceIn)+t0)/float64(mf), (float64(c.SourceIn)+t1)/float64(mf)))
				a = math.Min(1, a*g*gain)
				y0, y1 := int(math.Round(mid-a*mid)), int(math.Round(mid+a*mid))
				for y := y0; y <= y1; y++ {
					img.SetNRGBA(x, y, col)
				}
			}
			if fades && g < 1 {
				y := int(math.Round(mid - g*mid))
				img.SetNRGBA(x, y, env)
				img.SetNRGBA(x, h-1-y, env)
			}
		}
		return img
	}
}

// peakIn returns the largest peak in the fraction range [a, b) of the waveform.
func peakIn(peaks []float32, a, b float64) float32 {
	n := len(peaks)
	if n == 0 {
		return 0
	}
	i0 := int(a * float64(n))
	i1 := int(math.Ceil(b * float64(n)))
	i0 = max(0, min(i0, n-1))
	i1 = max(i0+1, min(i1, n))
	var p float32
	for _, v := range peaks[i0:i1] {
		p = max(p, v)
	}
	return p
}

// tickStep returns the frame distance between labelled ruler ticks so that labels are at
// least minPx apart on a lane of width w showing span frames.
func tickStep(span int, w float32, fps int, minPx float32) int {
	if fps <= 0 {
		fps = 25
	}
	secs := []int{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600}
	for _, s := range secs {
		if span <= 0 || float32(s*fps)/float32(span)*w >= minPx {
			return s * fps
		}
	}
	return secs[len(secs)-1] * fps
}

// ruler shows timecodes and the In/Out range; click or drag to seek.
type ruler struct {
	widget.BaseWidget
	pg *Page
}

func newRuler(pg *Page) *ruler {
	r := &ruler{pg: pg}
	r.ExtendBaseWidget(r)
	return r
}

func (r *ruler) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(r, fyne.NewSize(80, rulerH), r.build)
}

func (r *ruler) Tapped(e *fyne.PointEvent) { r.pg.s.Seek(r.pg.frameAt(e.Position.X, r.Size().Width)) }
func (r *ruler) Dragged(e *fyne.DragEvent) { r.pg.s.Seek(r.pg.frameAt(e.Position.X, r.Size().Width)) }
func (r *ruler) DragEnd()                  {}

func (r *ruler) build(size fyne.Size) []fyne.CanvasObject {
	pg := r.pg
	s := pg.s
	objs := []fyne.CanvasObject{place(canvas.NewRectangle(studiotheme.Panel), 0, 0, size.Width, size.Height)}
	if s.HasRange() {
		x0, x1 := pg.xOf(s.In, size.Width), pg.xOf(s.Out, size.Width)
		band := canvas.NewRectangle(color.NRGBA{studiotheme.Steel.R, studiotheme.Steel.G, studiotheme.Steel.B, 0x38})
		edge := canvas.NewRectangle(studiotheme.Steel)
		objs = append(objs, place(band, x0, 0, x1-x0, size.Height), place(edge, x0, size.Height-3, x1-x0, 3))
	}
	fps := s.FPS()
	span := pg.span()
	step := tickStep(span, size.Width, fps, 104)
	minor := step / 5
	if minor < 1 || step%5 != 0 {
		minor = max(step/2, 1)
	}
	for f := 0; f <= span; f += minor {
		x := pg.xOf(f, size.Width)
		if x > size.Width {
			break
		}
		if f%step == 0 {
			objs = append(objs, place(canvas.NewRectangle(studiotheme.Muted), x, size.Height-9, 1, 9))
			lbl := kit.MonoText(model.Timecode(f, fps), studiotheme.Muted, 10)
			objs = append(objs, place(lbl, x+4, 3, 90, 13))
		} else {
			objs = append(objs, place(canvas.NewRectangle(studiotheme.Faint), x, size.Height-5, 1, 5))
		}
	}
	return objs
}

// gridView draws faint second lines behind the lanes.
type gridView struct {
	widget.BaseWidget
	pg *Page
}

func newGridView(pg *Page) *gridView {
	g := &gridView{pg: pg}
	g.ExtendBaseWidget(g)
	return g
}

func (g *gridView) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(g, fyne.NewSize(0, 0), func(size fyne.Size) []fyne.CanvasObject {
		span := g.pg.span()
		step := tickStep(span, size.Width, g.pg.s.FPS(), 104)
		line := color.NRGBA{0xFF, 0xFF, 0xFF, 0x0A}
		var objs []fyne.CanvasObject
		for f := step; f <= span; f += step {
			objs = append(objs, place(canvas.NewRectangle(line), g.pg.xOf(f, size.Width), 0, 1, size.Height))
		}
		return objs
	})
}

// playhead is the tally-red line across ruler and lanes.
type playhead struct {
	widget.BaseWidget
	pg *Page
}

func newPlayhead(pg *Page) *playhead {
	h := &playhead{pg: pg}
	h.ExtendBaseWidget(h)
	return h
}

func (h *playhead) CreateRenderer() fyne.WidgetRenderer {
	return newDrawnRenderer(h, fyne.NewSize(0, 0), func(size fyne.Size) []fyne.CanvasObject {
		w := size.Width - headerW
		x := headerW + h.pg.xOf(h.pg.s.Playhead, w)
		if w <= 0 || x > size.Width {
			return nil
		}
		line := canvas.NewRectangle(studiotheme.Tally)
		tip := canvas.NewArbitraryPolygon([]fyne.Position{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 0.55}, {X: 0.5, Y: 1}, {X: 0, Y: 0.55}}, studiotheme.Tally)
		tip.NormalizedPoints = true
		return []fyne.CanvasObject{
			place(line, x-0.5, rulerH-12, 1.5, size.Height-rulerH+12),
			place(tip, x-6, rulerH-14, 12, 13),
		}
	})
}
