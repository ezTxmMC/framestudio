package menubar

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"framestudio-proto/src/ui/command"
	"framestudio-proto/src/ui/kit"
	studiotheme "framestudio-proto/src/ui/theme"
)

// row is a resolved menu entry.
type row struct {
	sep      bool
	label    string
	shortcut string // display text
	enabled  bool
	toggle   bool
	checked  bool
	radio    bool // checked shows a dot instead of the check mark
	icon     fyne.Resource
	swatch   color.Color
	sub      func() []row // submenu rows (nil when the row is no submenu parent)
	run      func()
}

func (r row) selectable() bool { return !r.sep && r.enabled }

// buildRows resolves menu items against the registry. Dynamic generators are expanded
// in place, separators are tidied and an empty result becomes "Keine Einträge".
func buildRows(reg *command.Registry, items []command.Item) []row {
	var rows []row
	var add func(items []command.Item, depth int)
	add = func(items []command.Item, depth int) {
		for _, it := range items {
			switch {
			case it.Separator:
				rows = append(rows, row{sep: true})
			case it.Dynamic != "":
				if depth < 4 {
					add(reg.DynamicItems(it.Dynamic), depth+1)
				}
			case it.Submenu != nil:
				label := it.Label
				if label == "" {
					label = it.Submenu.Title
				}
				kids := it.Submenu.Items
				rows = append(rows, row{label: label, enabled: true,
					sub: func() []row { return buildRows(reg, kids) }})
			case it.Run != nil:
				r := row{label: it.Label, enabled: it.Enabled == nil || it.Enabled(), run: it.Run}
				if it.Command != "" {
					if c, ok := reg.Get(it.Command); ok {
						if r.label == "" {
							r.label = c.Label
						}
						r.shortcut = ShortcutText(command.Display(c.Shortcut), runtimeGOOS)
						r.icon = c.Icon
					}
				}
				r.toggle, r.checked = it.Checked != 0, it.Checked == 2
				rows = append(rows, r)
			case it.Command != "":
				id := it.Command
				r := row{label: it.Label}
				if c, ok := reg.Get(id); ok {
					if r.label == "" {
						r.label = c.MenuLabel
					}
					if r.label == "" {
						r.label = c.Label
					}
					r.shortcut = ShortcutText(command.Display(c.Shortcut), runtimeGOOS)
					r.enabled = c.Enabled
					r.toggle, r.checked = c.Toggle, c.Checked
					r.icon = c.Icon
				}
				if r.label == "" {
					r.label = id
				}
				r.run = func() { reg.Run(id) }
				rows = append(rows, r)
			default: // a plain label (e.g. a heading from a generator)
				rows = append(rows, row{label: it.Label, toggle: it.Checked != 0, checked: it.Checked == 2})
			}
		}
	}
	add(items, 0)
	out := tidy(rows)
	if len(out) == 0 {
		out = append(out, row{label: "Keine Einträge"})
	}
	return out
}

// vis are the canvas objects of one row.
type vis struct {
	lbl, sc *canvas.Text
	icon    *canvas.Image
	iconRes fyne.Resource
	mark    *glyph // check mark
	dot     *canvas.Circle
	sw      *canvas.Rectangle
	chev    *glyph
	line    *canvas.Rectangle
}

// pane is one dropdown, submenu or context menu box.
type pane struct {
	widget.BaseWidget
	src   func() []row
	scale float32
	maxW  float32  // available width (0: unlimited); long labels are shortened
	disp  []string // labels as shown (shortened to fit maxW)
	maxH  float32  // available height (0: unlimited); a taller pane scrolls
	rows  []row
	tops  []float32 // row tops in pane coordinates
	hover int       // current row (mouse or keyboard), -1 none
	sub   int       // row whose submenu is open, -1 none
	size  fyne.Size
	cont  float32 // content height

	shadow []*canvas.Rectangle
	bg, hl *canvas.Rectangle
	inner  *fyne.Container
	scroll *container.Scroll
	vis    []vis
}

func newPane(src func() []row, scale, maxH, maxW float32) *pane {
	if scale <= 0 {
		scale = 1
	}
	p := &pane{src: src, scale: scale, maxH: maxH, maxW: maxW, hover: -1, sub: -1}
	p.ExtendBaseWidget(p)
	p.reload()
	return p
}

// reload resolves the rows again (labels, enabled, checked, dynamic lists).
func (p *pane) reload() {
	p.rows = p.src()
	if p.hover >= len(p.rows) || (p.hover >= 0 && !p.rows[p.hover].selectable()) {
		p.hover = -1
	}
	if p.sub >= len(p.rows) || (p.sub >= 0 && p.rows[p.sub].sub == nil) {
		p.sub = -1
	}
	p.measure()
	p.build()
}

// setMaxH sets the available height; the pane scrolls when its content is taller.
func (p *pane) setMaxH(h float32) {
	if h == p.maxH {
		return
	}
	p.maxH = h
	p.measure()
	p.build()
}

func (p *pane) snap(v float32) float32 { return kit.Snap(v, p.scale) }

func (p *pane) measure() {
	st := menustyle
	var lw, sw float32
	p.tops = make([]float32, len(p.rows))
	y := st.PanePad
	for i, r := range p.rows {
		p.tops[i] = y
		if r.sep {
			y += st.SepH
			continue
		}
		y += st.RowH
		if w := fyne.MeasureText(r.label, st.LabelSize, fyne.TextStyle{}).Width; w > lw {
			lw = w
		}
		if r.shortcut != "" {
			if w := fyne.MeasureText(r.shortcut, st.ShortcutSize, fyne.TextStyle{}).Width; w > sw {
				sw = w
			}
		}
	}
	fixed := st.PanePad + st.PadSide + st.IconCol + st.IconGap + st.LabelGap + sw +
		st.ShortcutGap + st.ArrowCol + st.PadSide + st.PanePad
	w := fixed + lw
	if w < st.MinPaneW {
		w = st.MinPaneW
	}
	p.disp = make([]string, len(p.rows))
	for i, r := range p.rows {
		p.disp[i] = r.label
	}
	if p.maxW > 0 && w > p.maxW {
		w = p.maxW
		avail := w - fixed
		for i, r := range p.rows {
			if !r.sep {
				p.disp[i] = ellipsize(r.label, st.LabelSize, avail)
			}
		}
	}
	w = float32(math.Ceil(float64(w*p.scale)-1e-3)) / p.scale
	p.cont = y + st.PanePad
	h := p.cont
	if p.maxH > 0 && h > p.maxH {
		h = p.snap(p.maxH)
	}
	p.size = fyne.NewSize(w, h)
}

func (p *pane) scrolls() bool { return p.scroll != nil }

func (p *pane) offset() float32 {
	if p.scroll != nil {
		return p.scroll.Offset.Y
	}
	return 0
}

func (p *pane) rowHeightAt(i int) float32 {
	if p.rows[i].sep {
		return menustyle.SepH
	}
	return menustyle.RowH
}

// rowAt returns the row index at y (pane coordinates), -1 outside.
func (p *pane) rowAt(y float32) int {
	y += p.offset()
	for i := range p.rows {
		if y >= p.tops[i] && y < p.tops[i]+p.rowHeightAt(i) {
			return i
		}
	}
	return -1
}

func (p *pane) contains(pos fyne.Position) bool {
	o := p.Position()
	return pos.X >= o.X && pos.Y >= o.Y && pos.X < o.X+p.size.Width && pos.Y < o.Y+p.size.Height
}

// reveal scrolls row i into view.
func (p *pane) reveal(i int) {
	if p.scroll == nil || i < 0 || i >= len(p.rows) {
		return
	}
	top, bottom := p.tops[i], p.tops[i]+p.rowHeightAt(i)
	if i == 0 || (i > 0 && p.rows[i-1].sep && i == 1) {
		top = 0
	}
	if i == len(p.rows)-1 {
		bottom = p.cont
	}
	off, h := p.scroll.Offset.Y, p.size.Height
	switch {
	case top < off:
		p.scroll.Offset.Y = top
	case bottom > off+h:
		p.scroll.Offset.Y = bottom - h
	default:
		return
	}
	p.scroll.Refresh()
}

// build creates the canvas objects for the current rows.
func (p *pane) build() {
	st := menustyle
	s := p.scale
	w, h := p.size.Width, p.size.Height
	hair := kit.Hair(s)

	p.shadow = p.shadow[:0]
	for i := st.ShadowLayers; i >= 1; i-- {
		g := float32(i) * 2
		sh := canvas.NewRectangle(color.NRGBA{0, 0, 0, st.ShadowAlpha[i-1]})
		sh.CornerRadius = st.PaneRadius + g
		sh.Move(fyne.NewPos(p.snap(-g), p.snap(-g+float32(i)*1.5)))
		sh.Resize(fyne.NewSize(p.snap(w+2*g), p.snap(h+2*g)))
		p.shadow = append(p.shadow, sh)
	}
	p.bg = canvas.NewRectangle(studiotheme.MenuSurface)
	p.bg.CornerRadius = st.PaneRadius
	p.bg.StrokeColor = studiotheme.MenuEdge
	p.bg.StrokeWidth = hair
	p.bg.Resize(p.size)

	p.hl = canvas.NewRectangle(studiotheme.MenuAccentRow())
	p.hl.CornerRadius = st.ItemRadius
	p.hl.Hide()
	objs := []fyne.CanvasObject{p.hl}
	p.vis = make([]vis, len(p.rows))
	for i, r := range p.rows {
		y := p.tops[i]
		v := &p.vis[i]
		if r.sep {
			ln := canvas.NewRectangle(studiotheme.MenuSep)
			ln.Move(fyne.NewPos(st.SepInset, p.snap(y+4)))
			ln.Resize(fyne.NewSize(w-2*st.SepInset, hair))
			v.line = ln
			objs = append(objs, ln)
			continue
		}
		cy := y + st.RowH/2
		iconCx := st.PanePad + st.PadSide + st.IconCol/2
		switch {
		case r.swatch != nil: // a checked colour gets a ring instead of a check mark
			sz := st.SwatchSize
			v.sw = canvas.NewRectangle(r.swatch)
			v.sw.CornerRadius = 3
			if r.toggle && r.checked {
				v.sw.StrokeColor = studiotheme.MenuText
				v.sw.StrokeWidth = 1.5
			}
			v.sw.Resize(fyne.NewSquareSize(sz))
			v.sw.Move(fyne.NewPos(p.snap(iconCx-sz/2), p.snap(cy-sz/2)))
			objs = append(objs, v.sw)
		case r.toggle && r.checked && r.radio:
			v.dot = canvas.NewCircle(studiotheme.MenuText)
			v.dot.Resize(fyne.NewSquareSize(6))
			v.dot.Move(fyne.NewPos(p.snap(iconCx-3), p.snap(cy-3)))
			objs = append(objs, v.dot)
		case r.toggle && r.checked:
			v.mark = newGlyph(10, 8, 1.5, [][2]float64{{0.8, 4.4}, {3.8, 7.2}, {9.2, 0.8}})
			v.mark.obj.Resize(fyne.NewSize(10, 8))
			v.mark.obj.Move(fyne.NewPos(p.snap(iconCx-5), p.snap(cy-4)))
			objs = append(objs, v.mark.obj)
		case !r.toggle && r.icon != nil:
			v.iconRes = r.icon
			v.icon = canvas.NewImageFromResource(r.icon)
			v.icon.FillMode = canvas.ImageFillContain
			v.icon.Resize(fyne.NewSquareSize(st.IconSize))
			v.icon.Move(fyne.NewPos(p.snap(iconCx-st.IconSize/2), p.snap(cy-st.IconSize/2)))
			objs = append(objs, v.icon)
		}
		v.lbl = canvas.NewText(p.disp[i], studiotheme.MenuText)
		v.lbl.TextSize = st.LabelSize
		ls := v.lbl.MinSize()
		v.lbl.Resize(ls)
		ly := y + kit.CenterY(st.RowH, ls.Height, s)
		v.lbl.Move(fyne.NewPos(st.PanePad+st.LabelX(), ly))
		objs = append(objs, v.lbl)
		if r.shortcut != "" {
			v.sc = canvas.NewText(r.shortcut, studiotheme.MenuShortcut)
			v.sc.TextSize = st.ShortcutSize
			ss := v.sc.MinSize()
			v.sc.Resize(ss)
			right := w - st.PanePad - st.PadSide - st.ArrowCol - st.ShortcutGap
			// shortcut and label share one baseline
			sy := ly + textBaseline(r.label, st.LabelSize) - textBaseline(r.shortcut, st.ShortcutSize)
			v.sc.Move(fyne.NewPos(p.snap(right-ss.Width), p.snap(sy)))
			objs = append(objs, v.sc)
		}
		if r.sub != nil {
			v.chev = newGlyph(5, 8, 1.5, [][2]float64{{0.8, 0.8}, {4.2, 4}, {0.8, 7.2}})
			v.chev.obj.Resize(fyne.NewSize(5, 8))
			cx := w - st.PanePad - st.PadSide - st.ArrowCol/2
			v.chev.obj.Move(fyne.NewPos(p.snap(cx-2.5), p.snap(cy-4)))
			objs = append(objs, v.chev.obj)
		}
	}
	p.inner = container.New(fixedLayout{fyne.NewSize(w, p.cont)}, objs...)
	p.inner.Resize(fyne.NewSize(w, p.cont))
	p.scroll = nil
	if p.cont > h+0.01 {
		p.scroll = container.NewVScroll(p.inner)
		p.scroll.Resize(p.size)
	}
	p.paintHover()
	p.Refresh()
}

// fixedLayout gives a container a fixed minimum size and leaves the children where they are.
type fixedLayout struct{ size fyne.Size }

func (fixedLayout) Layout([]fyne.CanvasObject, fyne.Size)   {}
func (f fixedLayout) MinSize([]fyne.CanvasObject) fyne.Size { return f.size }

func (p *pane) setHover(i int) {
	if i >= 0 && (i >= len(p.rows) || !p.rows[i].selectable()) {
		i = -1
	}
	if i == p.hover {
		return
	}
	p.hover = i
	p.paintHover()
	p.reveal(i)
	p.Refresh()
}

// paintHover moves the highlight and colours the texts, icons and chevrons.
func (p *pane) paintHover() {
	for i, r := range p.rows {
		if r.sep {
			continue
		}
		v := &p.vis[i]
		cur := i == p.hover
		lc, sc, cc := color.Color(studiotheme.MenuText), color.Color(studiotheme.MenuShortcut), color.Color(studiotheme.Muted)
		iconName := fyne.ThemeColorName(studiotheme.ColorNameMuted)
		if cur {
			cc = studiotheme.MenuText
			iconName = theme.ColorNameForeground
		}
		if !r.enabled {
			lc, sc, cc = studiotheme.MenuDisabled, studiotheme.MenuDisabled, studiotheme.MenuDisabled
			iconName = studiotheme.ColorNameMenuDisabled
		}
		if v.lbl != nil {
			v.lbl.Color = lc
			v.lbl.Refresh()
		}
		if v.sc != nil {
			v.sc.Color = sc
			v.sc.Refresh()
		}
		if v.mark != nil {
			v.mark.setColor(lc)
		}
		if v.dot != nil {
			v.dot.FillColor = lc
			v.dot.Refresh()
		}
		if v.chev != nil {
			v.chev.setColor(cc)
		}
		if v.icon != nil {
			v.icon.Resource = theme.NewColoredResource(v.iconRes, iconName)
			v.icon.Refresh()
		}
	}
	if p.hover < 0 {
		p.hl.Hide()
		return
	}
	p.hl.FillColor = studiotheme.MenuAccentRow()
	p.hl.Move(fyne.NewPos(menustyle.PanePad, p.tops[p.hover]))
	p.hl.Resize(fyne.NewSize(p.size.Width-2*menustyle.PanePad, menustyle.RowH))
	p.hl.Show()
	p.hl.Refresh()
}

// step moves the highlight to the next selectable row in direction dir (wrapping).
func (p *pane) step(dir int) {
	n := len(p.rows)
	i := p.hover
	for k := 0; k < n; k++ {
		if i < 0 {
			if dir > 0 {
				i = 0
			} else {
				i = n - 1
			}
		} else {
			i = (i + dir + n) % n
		}
		if p.rows[i].selectable() {
			p.setHover(i)
			return
		}
	}
}

func (p *pane) first() { p.hover = -1; p.step(1) }
func (p *pane) last()  { p.hover = -1; p.step(-1) }

// page moves the highlight by n rows without wrapping, to the nearest selectable row.
func (p *pane) page(n int) {
	if len(p.rows) == 0 {
		return
	}
	i := p.hover + n
	if p.hover < 0 {
		i = 0
		if n > 0 {
			i = n - 1
		}
	}
	if i < 0 {
		i = 0
	}
	if i >= len(p.rows) {
		i = len(p.rows) - 1
	}
	dir := 1
	if n < 0 {
		dir = -1
	}
	for j := i; j >= 0 && j < len(p.rows); j += dir {
		if p.rows[j].selectable() {
			p.setHover(j)
			return
		}
	}
	for j := i; j >= 0 && j < len(p.rows); j -= dir {
		if p.rows[j].selectable() {
			p.setHover(j)
			return
		}
	}
}

func (p *pane) MinSize() fyne.Size { return p.size }

func (p *pane) CreateRenderer() fyne.WidgetRenderer { return &paneRenderer{p: p} }

type paneRenderer struct{ p *pane }

func (r *paneRenderer) Layout(fyne.Size) {
	if r.p.scroll != nil {
		r.p.scroll.Move(fyne.NewPos(0, 0))
		r.p.scroll.Resize(r.p.size)
	}
}
func (r *paneRenderer) MinSize() fyne.Size { return r.p.size }
func (r *paneRenderer) Refresh()           { canvas.Refresh(r.p) }
func (r *paneRenderer) Destroy()           {}
func (r *paneRenderer) Objects() []fyne.CanvasObject {
	p := r.p
	objs := make([]fyne.CanvasObject, 0, 8)
	for _, s := range p.shadow {
		objs = append(objs, s)
	}
	objs = append(objs, p.bg)
	if p.scroll != nil {
		return append(objs, p.scroll)
	}
	return append(objs, p.inner)
}

// glyph is a small anti-aliased stroked polyline (check mark, chevron) drawn at the
// device resolution with round caps and joins, so it stays crisp at every scale.
type glyph struct {
	obj    *canvas.Raster
	col    color.Color
	w, h   float64 // box in units
	stroke float64 // stroke width in units
	pts    [][2]float64
}

func newGlyph(w, h, stroke float64, pts [][2]float64) *glyph {
	g := &glyph{col: studiotheme.MenuText, w: w, h: h, stroke: stroke, pts: pts}
	g.obj = canvas.NewRaster(g.draw)
	g.obj.ScaleMode = canvas.ImageScalePixels
	return g
}

func (g *glyph) setColor(c color.Color) {
	g.col = c
	g.obj.Refresh()
}

func (g *glyph) draw(pw, ph int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, pw, ph))
	if pw <= 0 || ph <= 0 {
		return img
	}
	sx, sy := float64(pw)/g.w, float64(ph)/g.h
	r, gg, b, a := g.col.RGBA()
	cr, cg, cb, ca := float64(r>>8), float64(gg>>8), float64(b>>8), float64(a>>8)/255
	half := g.stroke * sx / 2
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.MaxFloat64
			for i := 0; i+1 < len(g.pts); i++ {
				ax, ay := g.pts[i][0]*sx, g.pts[i][1]*sy
				bx, by := g.pts[i+1][0]*sx, g.pts[i+1][1]*sy
				if v := segDist(px, py, ax, ay, bx, by); v < d {
					d = v
				}
			}
			cov := half + 0.5 - d
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(cr), uint8(cg), uint8(cb), uint8(cov * ca * 255)})
		}
	}
	return img
}

func segDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = ((px-ax)*dx + (py-ay)*dy) / l2
		t = math.Max(0, math.Min(1, t))
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// textBaseline is the distance from the top of a text box to its baseline.
func textBaseline(text string, size float32) float32 {
	_, b := fyne.CurrentApp().Driver().RenderedTextSize(text, size, fyne.TextStyle{}, nil)
	return b
}

// ellipsize shortens s with a trailing ellipsis until it is at most w wide.
func ellipsize(s string, size, w float32) string {
	if fyne.MeasureText(s, size, fyne.TextStyle{}).Width <= w {
		return s
	}
	rs := []rune(s)
	for n := len(rs) - 1; n > 0; n-- {
		t := string(rs[:n]) + "…"
		if fyne.MeasureText(t, size, fyne.TextStyle{}).Width <= w {
			return t
		}
	}
	return "…"
}
