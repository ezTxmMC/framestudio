package timeline

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"framestudio-proto/src/model"
	studiotheme "framestudio-proto/src/ui/theme"
)

var (
	clipDark    = color.NRGBA{0x12, 0x13, 0x16, 0xFF}
	offlineFill = color.NRGBA{0x4A, 0x1A, 0x1C, 0xD8}
	offlineLine = color.NRGBA{0xC8, 0x52, 0x52, 0x70}
	offlineText = color.NRGBA{0xF0, 0x9A, 0x96, 0xFF}
	lockHatch   = color.NRGBA{0xFF, 0xFF, 0xFF, 0x12}
	white       = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// tlRenderer rebuilds the timeline's canvas objects on every refresh. Lanes, headers and
// ruler live in clipping containers so scrolled content never bleeds into neighbours.
type tlRenderer struct {
	t *Timeline

	bg                     *canvas.Rectangle
	lanes, header, ruler   *fyne.Container
	lanesC, headerC, rulsC *container.Clip
	corner                 *fyne.Container
	cornerTC               *canvas.Text

	phLine *canvas.Rectangle
	phHead *canvas.ArbitraryPolygon
	phStem *canvas.Rectangle

	objects []fyne.CanvasObject

	// generated images, reused while their key stays the same so textures are kept
	imgs, nextImgs map[string]*canvas.Image
	tiles          map[string]*image.NRGBA
	widths         map[textKey]float32
}

type textKey struct {
	s     string
	size  float32
	style fyne.TextStyle
}

func newRenderer(t *Timeline) *tlRenderer {
	r := &tlRenderer{
		t:        t,
		bg:       canvas.NewRectangle(studiotheme.Gap),
		lanes:    container.NewWithoutLayout(),
		header:   container.NewWithoutLayout(),
		ruler:    container.NewWithoutLayout(),
		cornerTC: canvas.NewText("", studiotheme.Text),
		phLine:   canvas.NewRectangle(studiotheme.Tally),
		phStem:   canvas.NewRectangle(studiotheme.Tally),
		phHead: &canvas.ArbitraryPolygon{FillColor: studiotheme.Tally, Points: []fyne.Position{
			{X: 0, Y: 0}, {X: 13, Y: 0}, {X: 13, Y: 8}, {X: 6.5, Y: 14}, {X: 0, Y: 8},
		}},
		imgs:   map[string]*canvas.Image{},
		tiles:  map[string]*image.NRGBA{},
		widths: map[textKey]float32{},
	}
	r.phHead.Resize(fyne.NewSize(13, 14))
	r.cornerTC.TextStyle = fyne.TextStyle{Monospace: true}
	r.cornerTC.TextSize = 12
	r.lanesC = container.NewClip(r.lanes)
	r.headerC = container.NewClip(r.header)
	r.rulsC = container.NewClip(r.ruler)
	r.corner = container.NewWithoutLayout()
	r.objects = []fyne.CanvasObject{r.bg, r.lanesC, r.headerC, r.rulsC, r.corner}
	return r
}

func (r *tlRenderer) Destroy()                     {}
func (r *tlRenderer) MinSize() fyne.Size           { return r.t.MinSize() }
func (r *tlRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *tlRenderer) Layout(fyne.Size)             { r.rebuild() }
func (r *tlRenderer) Refresh()                     { r.rebuild() }

func rect(c color.Color, x, y, w, h float32) *canvas.Rectangle {
	o := canvas.NewRectangle(c)
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(w, h))
	return o
}

func (r *tlRenderer) text(s string, c color.Color, size float32, style fyne.TextStyle, x, y float32) *canvas.Text {
	o := canvas.NewText(s, c)
	o.TextSize = size
	o.TextStyle = style
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(r.width(s, size, style), size*1.3))
	return o
}

func (r *tlRenderer) width(s string, size float32, style fyne.TextStyle) float32 {
	k := textKey{s, size, style}
	if w, ok := r.widths[k]; ok {
		return w
	}
	if len(r.widths) > 4000 {
		clear(r.widths)
	}
	w := fyne.MeasureText(s, size, style).Width
	r.widths[k] = w
	return w
}

// fit shortens s with an ellipsis until it is at most maxW wide; "" if nothing fits.
func (r *tlRenderer) fit(s string, size float32, style fyne.TextStyle, maxW float32) string {
	if maxW <= 0 {
		return ""
	}
	if r.width(s, size, style) <= maxW {
		return s
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if r.width(string(rs[:mid])+"…", size, style) <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ""
	}
	return strings.TrimRight(string(rs[:lo]), " ._-") + "…"
}

// image returns a canvas image for key, generating the pixels only when the key is new.
func (r *tlRenderer) image(key string, x, y, w, h float32, gen func(pw, ph int) image.Image) *canvas.Image {
	sc := r.t.scale()
	pw, ph := max(1, int(math.Ceil(float64(w*sc)))), max(1, int(math.Ceil(float64(h*sc))))
	key = fmt.Sprintf("%s|%dx%d", key, pw, ph)
	// the same pixels may appear twice in one build (two locked lanes); each needs its own object
	for n := 1; r.nextImgs[key] != nil; n++ {
		key = fmt.Sprintf("%s#%d", strings.Split(key, "#")[0], n)
	}
	o, ok := r.imgs[key]
	if !ok {
		o = canvas.NewImageFromImage(gen(pw, ph))
		o.FillMode = canvas.ImageFillStretch
		o.ScaleMode = canvas.ImageScaleFastest
	}
	r.nextImgs[key] = o
	o.Move(fyne.NewPos(x, y))
	o.Resize(fyne.NewSize(w, h))
	return o
}

func (r *tlRenderer) rebuild() {
	t := r.t
	sz := t.Size()
	if sz.Width <= 0 || sz.Height <= 0 {
		return
	}
	p := t.s.Project
	g := t.geom()
	r.nextImgs = map[string]*canvas.Image{}

	r.bg.Resize(sz)
	laneW, bodyH := g.laneW(), max(sz.Height-rulerH, 0)
	r.lanesC.Move(fyne.NewPos(headerW, rulerH))
	r.lanesC.Resize(fyne.NewSize(laneW, bodyH))
	r.headerC.Move(fyne.NewPos(0, rulerH))
	r.headerC.Resize(fyne.NewSize(headerW, bodyH))
	r.rulsC.Move(fyne.NewPos(headerW, 0))
	r.rulsC.Resize(fyne.NewSize(laneW, rulerH))

	r.lanes.Objects = r.buildLanes(p, g)
	r.header.Objects = r.buildHeaders(p, g)
	r.ruler.Objects = r.buildRuler(p, g)
	r.corner.Objects = r.buildCorner(g)
	r.placePlayhead(g)

	r.imgs = r.nextImgs
	for _, c := range []*fyne.Container{r.lanes, r.header, r.ruler, r.corner} {
		c.Refresh()
	}
	if len(r.tiles) > 64 {
		clear(r.tiles)
	}
}

// updatePlayhead moves only the playhead objects; used while playing.
func (r *tlRenderer) updatePlayhead() {
	r.placePlayhead(r.t.geom())
	canvas.Refresh(r.phLine)
	canvas.Refresh(r.phHead)
	canvas.Refresh(r.phStem)
	canvas.Refresh(r.cornerTC)
}

func (r *tlRenderer) placePlayhead(g geom) {
	s := r.t.s
	x := g.frameX(float64(s.Playhead)) - headerW
	visible := x >= -7 && x <= g.laneW()+7
	r.phLine.Move(fyne.NewPos(x, 0))
	r.phLine.Resize(fyne.NewSize(1, max(g.h-rulerH, 0)))
	r.phHead.Move(fyne.NewPos(x-6, rulerH-15))
	r.phStem.Move(fyne.NewPos(x, rulerH-3))
	r.phStem.Resize(fyne.NewSize(1, 3))
	for _, o := range []fyne.CanvasObject{r.phLine, r.phHead, r.phStem} {
		if visible {
			o.Show()
		} else {
			o.Hide()
		}
	}
	r.cornerTC.Text = s.Project.Timecode(s.Playhead)
	w := r.width(r.cornerTC.Text, r.cornerTC.TextSize, r.cornerTC.TextStyle)
	r.cornerTC.Move(fyne.NewPos((headerW-w)/2, (rulerH-r.cornerTC.TextSize*1.3)/2))
	r.cornerTC.Resize(fyne.NewSize(w, r.cornerTC.TextSize*1.3))
}

func (r *tlRenderer) buildCorner(g geom) []fyne.CanvasObject {
	return []fyne.CanvasObject{
		rect(studiotheme.Panel, 0, 0, headerW, rulerH),
		r.cornerTC,
		rect(studiotheme.Border, 0, rulerH-1, headerW, 1),
		rect(studiotheme.Border, headerW-1, 0, 1, g.h),
	}
}

// trackDim reports whether a track's content is switched off (hidden, muted, soloed out).
func trackDim(p *model.Project, tr *model.Track) bool {
	if tr.Kind == model.TrackVideo {
		return tr.Hidden
	}
	if tr.Muted {
		return true
	}
	for _, o := range p.Tracks {
		if o.Kind == model.TrackAudio && o.Solo {
			return !tr.Solo
		}
	}
	return false
}

func (r *tlRenderer) buildLanes(p *model.Project, g geom) []fyne.CanvasObject {
	t, s := r.t, r.t.s
	laneW, laneH := g.laneW(), max(g.h-rulerH, 0)
	var objs []fyne.CanvasObject

	// lane backgrounds
	lastVideo := -1
	for i, rw := range g.rows {
		y := rw.y - rulerH
		if y+rw.h < 0 || y > laneH {
			continue
		}
		tr := &p.Tracks[rw.track]
		bg := studiotheme.Lane
		if tr.ID == s.SelectedTrack {
			bg = mix(bg, studiotheme.Steel, 0.05)
		}
		objs = append(objs, rect(bg, 0, y, laneW, rw.h-1), rect(studiotheme.Gap, 0, y+rw.h-1, laneW, 1))
		if tr.Kind == model.TrackVideo {
			lastVideo = i
		}
	}

	// In/Out range and markers behind the clips
	if s.HasRange() {
		x0, x1 := g.frameX(float64(s.In))-headerW, g.frameX(float64(s.Out))-headerW
		objs = append(objs, rect(alpha(studiotheme.Steel, 0x16), x0, 0, x1-x0, min(laneH, g.content)))
	}
	for _, m := range p.Markers {
		x := g.frameX(float64(m.Frame)) - headerW
		if x >= -1 && x <= laneW {
			objs = append(objs, rect(alpha(m.Color.NRGBA(), 0x50), x, 0, 1, min(laneH, g.content)))
		}
	}

	d := t.drag
	for _, rw := range g.rows {
		y := rw.y - rulerH
		if y+rw.h < 0 || y > laneH {
			continue
		}
		tr := &p.Tracks[rw.track]
		for _, c := range tr.Clips {
			st := clipState{selected: c.ID == s.SelectedClip}
			if d != nil && d.clip.ID == c.ID && d.active() {
				if d.mode == dragMove {
					st.faded = true
				} else {
					c.Start, c.Length = d.start, d.length
					st.trim = d.mode
				}
			}
			objs = r.drawClip(objs, p, g, c, y, rw.h, st)
		}
		if tr.Locked {
			objs = append(objs,
				rect(color.NRGBA{0, 0, 0, 0x38}, 0, y, laneW, rw.h-1),
				r.image("hatch", 0, y, laneW, rw.h-1, func(w, h int) image.Image {
					return hatchImage(w, h, lockHatch, int(9*r.t.scale()))
				}))
		} else if trackDim(p, tr) {
			objs = append(objs, rect(alpha(studiotheme.Lane, 0x9C), 0, y, laneW, rw.h-1))
		}
	}

	// ghost of a clip being moved
	if d != nil && d.mode == dragMove && d.active() {
		if ri := g.rowOfTrack(d.track); ri >= 0 {
			rw := g.rows[ri]
			c := d.clip
			c.Start = d.start
			objs = r.drawClip(objs, p, g, c, rw.y-rulerH, rw.h, clipState{ghost: true})
		}
	}

	if lastVideo >= 0 && lastVideo+1 < len(g.rows) {
		rw := g.rows[lastVideo]
		objs = append(objs, rect(studiotheme.Border, 0, rw.y-rulerH+rw.h-1, laneW, 2))
	}

	if t.snapAt >= 0 && d != nil {
		x := g.frameX(float64(t.snapAt)) - headerW
		objs = append(objs, rect(studiotheme.Steel, x, 0, 1, laneH))
	}

	if hint := emptyHint(p); hint != "" {
		size := float32(12)
		w := r.width(hint, size, fyne.TextStyle{})
		y := min(g.content, laneH)/2 - 8
		if len(g.rows) == 0 {
			y = laneH/2 - 8
		}
		objs = append(objs, r.text(r.fit(hint, size, fyne.TextStyle{}, laneW-24), studiotheme.Muted, size, fyne.TextStyle{}, max(12, (laneW-w)/2), y))
	}

	return append(objs, r.phLine)
}

func emptyHint(p *model.Project) string {
	switch {
	case len(p.Tracks) == 0:
		return "Keine Spuren. Rechtsklick in die Timeline, um eine Video- oder Audiospur hinzuzufügen."
	case p.Duration() == 0:
		return "Noch keine Clips. Medien im Media-Pool importieren und auf eine Spur ziehen."
	}
	return ""
}

type clipState struct {
	selected bool
	faded    bool     // original position of a clip being moved
	ghost    bool     // target preview of a move
	trim     dragMode // edge being trimmed
}

// drawClip appends the canvas objects of one clip in lane coordinates.
func (r *tlRenderer) drawClip(objs []fyne.CanvasObject, p *model.Project, g geom, c model.Clip, rowY, rowH float32, st clipState) []fyne.CanvasObject {
	laneW := g.laneW()
	x0, x1 := g.clipSpan(c)
	x0, x1 = x0-headerW, x1-headerW
	if x1 < 0 || x0 > laneW {
		return objs
	}
	if x1-x0 < 1 {
		x1 = x0 + 1
	}
	// clamp to the visible range (plus a margin for rounded corners) to keep geometry small
	vx0, vx1 := max(x0, -4), min(x1, laneW+4)
	top, h := rowY+clipPadY, rowH-2*clipPadY-1
	bar := min(nameBarH, h)
	bodyTop, bodyH := top+bar, h-bar

	base := c.Color.NRGBA()
	base.A = 0xFF
	body := mix(base, clipDark, 0.62)
	switch c.Kind {
	case model.KindColor:
		body = mix(base, clipDark, 0.3)
	case model.KindAudio:
		body = mix(base, clipDark, 0.68)
	}

	bg := rect(body, vx0, top, vx1-vx0, h)
	bg.CornerRadius = 3
	bg.StrokeColor = color.NRGBA{0, 0, 0, 0x90}
	bg.StrokeWidth = 1
	nb := rect(base, vx0, top, vx1-vx0, bar)
	nb.TopLeftCornerRadius, nb.TopRightCornerRadius = 3, 3
	objs = append(objs, bg, nb)

	tx := max(x0, 0) + 5 // labels stay visible when the clip starts off screen
	avail := min(x1, laneW) - 4 - tx

	// visible body area for generated images
	ix0, ix1 := max(x0, 0)+1, min(x1, laneW)-1
	var m *model.MediaItem
	offline := false
	if c.MediaID != "" {
		var ok bool
		if m, ok = p.MediaByID(c.MediaID); !ok {
			offline = true
		}
	}
	if m != nil && !offline {
		wave := c.Kind == model.KindAudio
		a := r.t.ld.snapshot(r.t.ld.get(*m, wave, waveBuckets(p, *m)))
		offline = a.offline
		if !offline && ix1-ix0 >= 1 && bodyH > 4 {
			if wave {
				objs = append(objs, r.waveform(p, g, c, *m, a, ix0, ix1, bodyTop, bodyH-1))
			} else if a.thumb != nil {
				objs = append(objs, r.filmstrip(g, c, *m, a, x0, ix0, ix1, bodyTop, bodyH-1, body))
			}
		}
	}

	if offline && bodyH > 2 {
		objs = append(objs,
			rect(offlineFill, vx0+1, bodyTop, vx1-vx0-2, bodyH-1),
			r.image("offline", ix0, bodyTop, ix1-ix0, bodyH-1, func(w, h int) image.Image {
				return hatchImage(w, h, offlineLine, int(7*r.t.scale()))
			}))
		if avail > 50 && bodyH > 16 {
			objs = append(objs, r.text(r.fit("Medien offline", 11, fyne.TextStyle{Bold: true}, avail), offlineText, 11, fyne.TextStyle{Bold: true}, tx, bodyTop+(bodyH-14.3)/2))
		}
	}

	objs = r.drawFades(objs, g, c, x0, x1, top, h)

	// labels
	if avail > 8 && bar >= 12 {
		dur := clipLabel(c.Length, p.Settings.FPS)
		mono := fyne.TextStyle{Monospace: true}
		dw := r.width(dur, 9.5, mono)
		nameW := avail
		if avail > dw+40 {
			nameW = avail - dw - 8
			objs = append(objs, r.text(dur, alpha(white, 0xB0), 9.5, mono, tx+avail-dw, top+2))
		}
		if name := r.fit(c.Name, 11.5, fyne.TextStyle{}, nameW); name != "" {
			objs = append(objs, r.text(name, white, 11.5, fyne.TextStyle{}, tx, top+0.5))
		}
	}
	if c.Kind == model.KindTitle && avail > 12 && bodyH > 16 {
		it := fyne.TextStyle{Italic: true}
		if s := r.fit(c.Title.Text, 12, it, avail); s != "" {
			objs = append(objs, r.text(s, alpha(studiotheme.Text, 0xC8), 12, it, tx, bodyTop+(bodyH-15.6)/2))
		}
	}

	switch {
	case st.faded:
		objs = append(objs, rect(alpha(studiotheme.Lane, 0xB0), vx0, top, vx1-vx0, h))
	case st.ghost:
		o := rect(color.Transparent, vx0, top, vx1-vx0, h)
		o.StrokeColor, o.StrokeWidth, o.CornerRadius = studiotheme.Steel, 2, 3
		objs = append(objs, o)
	case st.selected:
		o := rect(color.Transparent, vx0, top, vx1-vx0, h)
		o.StrokeColor, o.StrokeWidth, o.CornerRadius = white, 1.5, 3
		objs = append(objs, o)
	}
	switch st.trim {
	case dragTrimL:
		objs = append(objs, rect(studiotheme.Steel, x0, top, 3, h))
	case dragTrimR:
		objs = append(objs, rect(studiotheme.Steel, x1-3, top, 3, h))
	}
	return objs
}

// drawFades shades fade-in/out ramps as triangles with a bright ramp line.
func (r *tlRenderer) drawFades(objs []fyne.CanvasObject, g geom, c model.Clip, x0, x1, top, h float32) []fyne.CanvasObject {
	shade := color.NRGBA{0, 0, 0, 0x78}
	line := alpha(white, 0xA0)
	if c.FadeIn > 0 {
		fw := min(float32(float64(c.FadeIn)*g.ppf), x1-x0)
		if fw >= 2 {
			tri := &canvas.ArbitraryPolygon{FillColor: shade, Points: []fyne.Position{{X: 0, Y: 0}, {X: fw, Y: 0}, {X: 0, Y: h}}}
			tri.Move(fyne.NewPos(x0, top))
			tri.Resize(fyne.NewSize(fw, h))
			ln := canvas.NewLine(line)
			ln.Position1, ln.Position2 = fyne.NewPos(x0, top+h), fyne.NewPos(x0+fw, top)
			objs = append(objs, tri, ln)
		}
	}
	if c.FadeOut > 0 {
		fw := min(float32(float64(c.FadeOut)*g.ppf), x1-x0)
		if fw >= 2 {
			tri := &canvas.ArbitraryPolygon{FillColor: shade, Points: []fyne.Position{{X: 0, Y: 0}, {X: fw, Y: 0}, {X: fw, Y: h}}}
			tri.Move(fyne.NewPos(x1-fw, top))
			tri.Resize(fyne.NewSize(fw, h))
			ln := canvas.NewLine(line)
			ln.Position1, ln.Position2 = fyne.NewPos(x1-fw, top), fyne.NewPos(x1, top+h)
			objs = append(objs, tri, ln)
		}
	}
	return objs
}

// waveform draws the visible part [ix0,ix1) of an audio clip's waveform.
func (r *tlRenderer) waveform(p *model.Project, g geom, c model.Clip, m model.MediaItem, a asset, ix0, ix1, y, h float32) fyne.CanvasObject {
	srcFrames := max(p.MediaFrames(m), c.SourceIn+c.Length)
	srcStart := float64(c.SourceIn) + g.xFrame(ix0+headerW) - float64(c.Start)
	sc := float64(r.t.scale())
	base := c.Color.NRGBA()
	spec := waveSpec{
		peaks: a.peaks, srcFrames: srcFrames, srcStart: srcStart, fpp: 1 / (g.ppf * sc),
		gain: c.Volume, col: mix(base, white, 0.45),
	}
	key := fmt.Sprintf("wave|%s|%p|%d|%.2f|%.4f|%.3f|%v", m.ID, a.peaks, len(a.peaks), srcStart, spec.fpp, c.Volume, base)
	return r.image(key, ix0, y, ix1-ix0, h, func(w, h int) image.Image { return waveImage(w, h, spec) })
}

// filmstrip repeats the media thumbnail across the visible body of a video or image clip.
func (r *tlRenderer) filmstrip(g geom, c model.Clip, m model.MediaItem, a asset, x0, ix0, ix1, y, h float32, bg color.NRGBA) fyne.CanvasObject {
	sc := r.t.scale()
	ph := max(1, int(math.Ceil(float64(h*sc))))
	tk := fmt.Sprintf("%s|%p|%d", m.ID, a.thumb, ph)
	tile, ok := r.tiles[tk]
	if !ok {
		tile = scaleToHeight(a.thumb, ph)
		r.tiles[tk] = tile
	}
	from := int(math.Round(float64((ix0 - x0) * sc)))
	key := fmt.Sprintf("film|%s|%d", tk, from)
	return r.image(key, ix0, y, ix1-ix0, h, func(w, h int) image.Image { return filmstripImage(w, h, tile, from, bg) })
}

func (r *tlRenderer) buildHeaders(p *model.Project, g geom) []fyne.CanvasObject {
	s := r.t.s
	bodyH := max(g.h-rulerH, 0)
	objs := []fyne.CanvasObject{rect(studiotheme.Panel, 0, 0, headerW, bodyH)}
	lastVideo := -1
	for i, rw := range g.rows {
		y := rw.y - rulerH
		tr := &p.Tracks[rw.track]
		if tr.Kind == model.TrackVideo {
			lastVideo = i
		}
		if y+rw.h < 0 || y > bodyH {
			continue
		}
		sel := tr.ID == s.SelectedTrack
		bg := studiotheme.Panel
		if sel {
			bg = studiotheme.Raised
		}
		objs = append(objs, rect(bg, 0, y, headerW, rw.h-1), rect(studiotheme.Gap, 0, y+rw.h-1, headerW, 1))
		if sel {
			objs = append(objs, rect(studiotheme.Steel, 0, y, 2, rw.h-1))
		}
		cy := y + (rw.h-1)/2
		glyph, nameCol := "film", studiotheme.Text
		if tr.Kind == model.TrackAudio {
			glyph = "wave"
		}
		if trackDim(p, tr) {
			nameCol = studiotheme.Muted
		}
		ic := canvas.NewImageFromResource(icon(glyph, studiotheme.Muted))
		ic.FillMode = canvas.ImageFillContain
		ic.Move(fyne.NewPos(10, cy-7))
		ic.Resize(fyne.NewSize(14, 14))
		bold := fyne.TextStyle{Bold: true}
		btns := headerButtons(tr.Kind, row{y: y, h: rw.h - 1})
		nameMax := btns[0].rect.X - 32 - 4
		objs = append(objs, ic, r.text(r.fit(tr.Name, 13, bold, nameMax), nameCol, 13, bold, 30, cy-8.5))
		for _, b := range btns {
			objs = r.drawButton(objs, b, tr)
		}
	}
	if lastVideo >= 0 && lastVideo+1 < len(g.rows) {
		rw := g.rows[lastVideo]
		objs = append(objs, rect(studiotheme.Border, 0, rw.y-rulerH+rw.h-1, headerW, 2))
	}
	return append(objs, rect(studiotheme.Border, headerW-1, 0, 1, bodyH))
}

// drawButton draws one header toggle in its on or off state.
func (r *tlRenderer) drawButton(objs []fyne.CanvasObject, b headerButton, tr *model.Track) []fyne.CanvasObject {
	var on bool
	var glyph, label string
	switch b.b {
	case btnEye:
		on, glyph = tr.Hidden, "eye"
		if on {
			glyph = "eyeoff"
		}
	case btnLock:
		on, glyph = tr.Locked, "unlock"
		if on {
			glyph = "lock"
		}
	case btnMute:
		on, label = tr.Muted, "M"
	case btnSolo:
		on, label = tr.Solo, "S"
	}
	fill, stroke, fg := studiotheme.Lane, studiotheme.Border, studiotheme.Muted
	if on {
		fill, stroke, fg = studiotheme.Steel, studiotheme.Steel, white
	}
	bg := rect(fill, b.rect.X, b.rect.Y, b.size.Width, b.size.Height)
	bg.CornerRadius, bg.StrokeColor, bg.StrokeWidth = 3, stroke, 1
	objs = append(objs, bg)
	if label != "" {
		st := fyne.TextStyle{Bold: true}
		w := r.width(label, 11, st)
		return append(objs, r.text(label, fg, 11, st, b.rect.X+(b.size.Width-w)/2, b.rect.Y+(b.size.Height-14.3)/2))
	}
	ic := canvas.NewImageFromResource(icon(glyph, fg))
	ic.FillMode = canvas.ImageFillContain
	ic.Move(fyne.NewPos(b.rect.X+(b.size.Width-12)/2, b.rect.Y+(b.size.Height-12)/2))
	ic.Resize(fyne.NewSize(12, 12))
	return append(objs, ic)
}

func (r *tlRenderer) buildRuler(p *model.Project, g geom) []fyne.CanvasObject {
	s := r.t.s
	laneW := g.laneW()
	objs := []fyne.CanvasObject{rect(studiotheme.Panel, 0, 0, laneW, rulerH)}
	if s.HasRange() {
		x0, x1 := g.frameX(float64(s.In))-headerW, g.frameX(float64(s.Out))-headerW
		objs = append(objs,
			rect(alpha(studiotheme.Steel, 0x38), x0, 0, x1-x0, rulerH-1),
			rect(studiotheme.Steel, x0, 0, 1, rulerH-1), rect(studiotheme.Steel, x0, 0, 5, 1),
			rect(studiotheme.Steel, x1-1, 0, 1, rulerH-1), rect(studiotheme.Steel, x1-5, 0, 5, 1))
	}

	fps := max(p.Settings.FPS, 1)
	major, minor := rulerStep(g.ppf, fps, labelGap)
	first := int(math.Floor(g.offset/float64(minor))) * minor
	last := g.offset + g.visibleFrames()
	mono := fyne.TextStyle{Monospace: true}
	for f := first; float64(f) <= last; f += minor {
		if f < 0 {
			continue
		}
		x := float32(math.Round(float64(g.frameX(float64(f)) - headerW)))
		if f%major == 0 {
			objs = append(objs, rect(studiotheme.Muted, x, rulerH-9, 1, 8),
				r.text(model.Timecode(f, fps), studiotheme.Muted, 10, mono, x+4, 3))
		} else {
			objs = append(objs, rect(studiotheme.Faint, x, rulerH-5, 1, 4))
		}
	}

	for _, m := range p.Markers {
		x := g.frameX(float64(m.Frame)) - headerW
		if x < -6 || x > laneW+6 {
			continue
		}
		flag := &canvas.ArbitraryPolygon{FillColor: m.Color.NRGBA(), Points: []fyne.Position{
			{X: 0, Y: 0}, {X: 9, Y: 0}, {X: 9, Y: 6}, {X: 4.5, Y: 10}, {X: 0, Y: 6},
		}}
		flag.Move(fyne.NewPos(x-4.5, rulerH-12))
		flag.Resize(fyne.NewSize(9, 10))
		objs = append(objs, flag)
	}
	return append(objs, rect(studiotheme.Border, 0, rulerH-1, laneW, 1), r.phStem, r.phHead)
}
