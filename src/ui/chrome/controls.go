package chrome

import (
	"image"
	"image/color"
	"math"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	studiotheme "framestudio-proto/src/ui/theme"
)

// Window button kinds, independent of the style.
type buttonKind int

const (
	kindNone buttonKind = iota
	kindClose
	kindMinimize
	kindZoom // mac: green (fullscreen / Alt: maximize); win: maximize
)

// Geometry of the mac traffic lights (points).
const (
	macDiameter = 12.5
	macGap      = 8.0
	macPadLeft  = 12.0
	macPadRight = 12.0
	winButtonW  = 46.0

	// linux (Adwaita / Breeze like): round hover discs with symbolic icons
	linDisc     = 24.0
	linGap      = 4.0
	linPadLeft  = 4.0
	linPadRight = 8.0
)

// styleSpec describes one button style; everything that differs per platform is data.
type styleSpec struct {
	right      bool          // controls at the right end of the bar (else left)
	centreText bool          // a plain title is centred in the free space
	zoomFull   bool          // the zoom button is fullscreen (Alt: maximize), else maximize
	perButton  bool          // hover/press is per button (else the group hovers as a whole)
	kinds      [3]buttonKind // buttons left to right
	width      float32
	slotStart  float64 // left edge of the first hit area
	slotStep   float64 // distance between hit areas
	slotW      float64 // width of a hit area (full bar height)
}

var styles = map[string]styleSpec{
	StyleMac: {right: false, centreText: true, zoomFull: true, perButton: false,
		kinds: [3]buttonKind{kindClose, kindMinimize, kindZoom},
		width: float32(macPadLeft + 3*macDiameter + 2*macGap + macPadRight)},
	StyleWin: {right: true, centreText: false, perButton: true,
		kinds: [3]buttonKind{kindMinimize, kindZoom, kindClose},
		width: 3 * winButtonW, slotStart: 0, slotStep: winButtonW, slotW: winButtonW},
	StyleLinux: {right: true, centreText: true, perButton: true,
		kinds:     [3]buttonKind{kindMinimize, kindZoom, kindClose},
		width:     linPadLeft + 3*linDisc + 2*linGap + linPadRight,
		slotStart: linPadLeft - linGap/2, slotStep: linDisc + linGap, slotW: linDisc + linGap},
}

func (c *Controls) spec() styleSpec { return styles[c.style] }

// Colours of the mac traffic lights (flat, with a thin darker rim like the system ones).
var (
	macRed      = color.NRGBA{0xFF, 0x5F, 0x57, 0xFF}
	macRedRim   = color.NRGBA{0xE0, 0x44, 0x3E, 0xFF}
	macYellow   = color.NRGBA{0xFE, 0xBC, 0x2E, 0xFF}
	macYelRim   = color.NRGBA{0xDE, 0xA1, 0x23, 0xFF}
	macGreen    = color.NRGBA{0x28, 0xC8, 0x40, 0xFF}
	macGreenRim = color.NRGBA{0x1A, 0xAB, 0x29, 0xFF}

	macGrey    = color.NRGBA{0x4A, 0x4D, 0x54, 0xFF}
	macGreyRim = color.NRGBA{0x3C, 0x3F, 0x45, 0xFF}

	glyphRed    = color.NRGBA{0x4C, 0x0A, 0x04, 0xFF}
	glyphYellow = color.NRGBA{0x5A, 0x38, 0x00, 0xFF}
	glyphGreen  = color.NRGBA{0x06, 0x48, 0x0C, 0xFF}
)

const glyphAlpha = 0.6

// Colours of the linux style buttons (the close disc uses the theme's error red).
var (
	linIcon    = color.NRGBA{0xE4, 0xE5, 0xE1, 0xFF}
	linHover   = color.NRGBA{0xFF, 0xFF, 0xFF, 0x1C}
	linPressed = color.NRGBA{0xFF, 0xFF, 0xFF, 0x36}
)

// Colours of the win style buttons.
var (
	winIcon       = color.NRGBA{0xDA, 0xDB, 0xD7, 0xFF}
	winHover      = color.NRGBA{0xFF, 0xFF, 0xFF, 0x14}
	winPressed    = color.NRGBA{0xFF, 0xFF, 0xFF, 0x20}
	winCloseHover = color.NRGBA{0xC4, 0x2B, 0x1C, 0xFF}
	winClosePress = color.NRGBA{0xA3, 0x23, 0x17, 0xFF}
)

// actions are what the buttons do; the title bar wires them to the Backend.
type actions struct {
	close, minimize         func()
	fullscreen, maximize    func()
	drag, doubleTap         func() // empty area of the control box behaves like the bar
	secondary               func(*fyne.PointEvent)
	canMaximize, canFullscr bool
}

// Controls is the group of window buttons (mac traffic lights or win buttons). It handles
// all pointer input of the group itself, so hovering any part of the mac group shows the
// glyphs on all three lights. Empty space around the buttons is part of the drag region.
type Controls struct {
	widget.BaseWidget

	style   string
	compact bool
	act     actions
	state   State

	hover   bool // pointer over the group (mac) / any button (win)
	pos     fyne.Position
	pressed buttonKind
	alt     bool

	raster *canvas.Raster
}

// NewControls builds the button group. Exported for hosts that draw their own bar.
func newControls(style string, compact bool, act actions) *Controls {
	c := &Controls{style: normStyle(style), compact: compact, act: act, state: State{Focused: true}}
	c.ExtendBaseWidget(c)
	c.raster = canvas.NewRaster(c.draw)
	c.raster.ScaleMode = canvas.ImageScalePixels
	return c
}

// normStyle maps a style name to a known style; unknown names follow the running system.
func normStyle(s string) string {
	if _, ok := styles[s]; ok {
		return s
	}
	return ResolveStyle("auto", runtime.GOOS)
}

func (c *Controls) setStyle(s string) {
	s = normStyle(s)
	if s == c.style {
		return
	}
	c.style = s
	c.hover, c.pressed, c.alt = false, kindNone, false
	c.Refresh()
}

func (c *Controls) setState(s State) {
	if s == c.state {
		return
	}
	c.state = s
	c.Refresh()
}

// SetAlt tells the group that Alt/Option is held (shows the "+" glyph on the green light).
func (c *Controls) SetAlt(on bool) {
	if c.alt != on {
		c.alt = on
		c.Refresh()
	}
}

func barHeight(compact bool) float32 {
	if compact {
		return 24
	}
	return 28
}

func (c *Controls) height() float32 { return barHeight(c.compact) }

func (c *Controls) width() float32 {
	return c.spec().width
}

func (c *Controls) MinSize() fyne.Size { return fyne.NewSize(c.width(), c.height()) }

func (c *Controls) CreateRenderer() fyne.WidgetRenderer { return &controlsRenderer{c: c} }

type controlsRenderer struct{ c *Controls }

func (r *controlsRenderer) Layout(s fyne.Size) {
	r.c.raster.Move(fyne.NewPos(0, 0))
	r.c.raster.Resize(s)
}
func (r *controlsRenderer) MinSize() fyne.Size           { return r.c.MinSize() }
func (r *controlsRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.c.raster} }
func (r *controlsRenderer) Destroy()                     {}
func (r *controlsRenderer) Refresh() {
	r.c.raster.Refresh()
}

// enabled reports whether a button reacts.
func (c *Controls) enabled(k buttonKind) bool {
	if k == kindZoom {
		if c.spec().zoomFull {
			return c.act.canMaximize || c.act.canFullscr
		}
		return c.act.canMaximize
	}
	return k != kindNone
}

// order of the buttons left to right.
func (c *Controls) kinds() [3]buttonKind { return c.spec().kinds }

// slot returns the horizontal hit area of the i-th button (per button styles).
func (c *Controls) slot(i int) (x0, x1 float64) {
	sp := c.spec()
	x0 = sp.slotStart + float64(i)*sp.slotStep
	return x0, x0 + sp.slotW
}

// discCentre is the centre of the i-th linux disc.
func (c *Controls) discCentre(i int) (float64, float64) {
	return linPadLeft + linDisc/2 + float64(i)*(linDisc+linGap), float64(c.height()-1) / 2
}

// macCentre returns the centre of the i-th traffic light.
func (c *Controls) macCentre(i int) (float64, float64) {
	return macPadLeft + macDiameter/2 + float64(i)*(macDiameter+macGap), float64(c.height()) / 2
}

// hit returns the button under p (kindNone for empty space).
func (c *Controls) hit(p fyne.Position) buttonKind {
	ks := c.kinds()
	x, y := float64(p.X), float64(p.Y)
	if y < 0 || y > float64(c.height()) {
		return kindNone
	}
	if c.spec().perButton {
		for i := range ks {
			if x0, x1 := c.slot(i); x >= x0 && x < x1 {
				return ks[i]
			}
		}
		return kindNone
	}
	for i := range ks {
		cx, cy := c.macCentre(i)
		if math.Abs(x-cx) <= macDiameter/2+macGap/2 && math.Abs(y-cy) <= macDiameter/2+3 &&
			x >= macPadLeft-macGap/2 && x <= macPadLeft+3*macDiameter+2*macGap+macGap/2 {
			return ks[i]
		}
	}
	return kindNone
}

// inGroup reports whether p is over the mac group (circles plus a small margin).
func (c *Controls) inGroup(p fyne.Position) bool {
	x, y := float64(p.X), float64(p.Y)
	_, cy := c.macCentre(0)
	return x >= macPadLeft-6 && x <= macPadLeft+3*macDiameter+2*macGap+6 && math.Abs(y-cy) <= macDiameter/2+6
}

// desktop.Hoverable

func (c *Controls) MouseIn(e *desktop.MouseEvent) { c.track(e) }
func (c *Controls) MouseMoved(e *desktop.MouseEvent) {
	c.track(e)
}

// isDown reports whether button k is drawn pressed: held and still under the pointer.
func (c *Controls) isDown(k buttonKind) bool { return c.pressed == k && c.hit(c.pos) == k }

func (c *Controls) MouseOut() {
	c.hover = false
	c.alt = false
	c.pos = fyne.NewPos(-1, -1) // the pointer is away: a pressed button un-presses visually
	c.Refresh()
}

func (c *Controls) track(e *desktop.MouseEvent) {
	c.pos = e.Position
	h := c.inGroup(e.Position)
	perButton := c.spec().perButton
	if perButton {
		h = c.hit(e.Position) != kindNone
	}
	alt := e.Modifier&fyne.KeyModifierAlt != 0
	if h != c.hover || alt != c.alt || perButton {
		c.hover, c.alt = h, alt
		c.Refresh()
	}
}

// desktop.Mouseable

func (c *Controls) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	c.pos = e.Position
	k := c.hit(e.Position)
	if k == kindNone || !c.enabled(k) {
		c.pressed = kindNone
		if k == kindNone && c.act.drag != nil {
			c.act.drag()
		}
		return
	}
	c.pressed = k
	c.alt = e.Modifier&fyne.KeyModifierAlt != 0
	c.Refresh()
}

func (c *Controls) MouseUp(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary {
		return
	}
	k := c.pressed
	c.pressed = kindNone
	c.pos = e.Position
	c.Refresh()
	if k == kindNone || c.hit(e.Position) != k {
		return
	}
	alt := e.Modifier&fyne.KeyModifierAlt != 0
	c.fire(k, alt)
}

// fire runs the action of button k.
func (c *Controls) fire(k buttonKind, alt bool) {
	switch k {
	case kindClose:
		call(c.act.close)
	case kindMinimize:
		call(c.act.minimize)
	case kindZoom:
		switch {
		case !c.spec().zoomFull:
			if c.act.canMaximize {
				call(c.act.maximize)
			}
		case alt && c.act.canMaximize:
			call(c.act.maximize)
		case c.act.canFullscr:
			call(c.act.fullscreen)
		case c.act.canMaximize:
			call(c.act.maximize)
		}
	}
}

func call(f func()) {
	if f != nil {
		f()
	}
}

// Empty space of the group acts like the bar itself.

func (c *Controls) Tapped(*fyne.PointEvent) {}
func (c *Controls) DoubleTapped(e *fyne.PointEvent) {
	if c.hit(e.Position) == kindNone {
		call(c.act.doubleTap)
	}
}
func (c *Controls) TappedSecondary(e *fyne.PointEvent) {
	if c.act.secondary != nil {
		c.act.secondary(e)
	}
}

// draw renders the group (called by the raster with pixel dimensions).
func (c *Controls) draw(pw, ph int) image.Image {
	p := newPainter(pw, ph, c.Size().Width)
	if c.Size().Width <= 0 {
		return p.img
	}
	switch c.style {
	case StyleWin:
		c.drawWin(p)
	case StyleLinux:
		c.drawLinux(p)
	default:
		c.drawMac(p)
	}
	return p.img
}

// macLook returns fill, rim and glyph colours of a traffic light.
func macLook(k buttonKind) (fill, rim, glyph color.NRGBA) {
	switch k {
	case kindClose:
		return macRed, macRedRim, glyphRed
	case kindMinimize:
		return macYellow, macYelRim, glyphYellow
	}
	return macGreen, macGreenRim, glyphGreen
}

func (c *Controls) drawMac(p *painter) {
	r := macDiameter / 2
	colourful := c.state.Focused || c.hover
	for i, k := range c.kinds() {
		cx, cy := c.macCentre(i)
		fill, rim, glyph := macLook(k)
		enabled := c.enabled(k)
		switch {
		case !enabled:
			// disabled: an empty grey ring
			p.ring(cx, cy, r, 1, macGrey)
			continue
		case !colourful:
			fill, rim = macGrey, macGreyRim
		case c.pressed == k:
			fill, rim = darken(fill, 0.8), darken(rim, 0.8)
		}
		p.disc(cx, cy, r, rim)
		p.disc(cx, cy, r-0.55, fill)
		if c.hover && colourful {
			g := withAlpha(glyph, glyphAlpha)
			if c.pressed == k {
				g = withAlpha(glyph, glyphAlpha+0.15)
			}
			c.drawMacGlyph(p, k, cx, cy, g)
		}
	}
}

func (c *Controls) drawMacGlyph(p *painter, k buttonKind, cx, cy float64, g color.NRGBA) {
	const sw = 1.3
	switch k {
	case kindClose:
		const a = 2.1
		p.line(cx-a, cy-a, cx+a, cy+a, sw, g)
		p.line(cx-a, cy+a, cx+a, cy-a, sw, g)
	case kindMinimize:
		p.line(cx-3.0, cy, cx+3.0, cy, sw, g)
	case kindZoom:
		if c.alt && c.act.canMaximize {
			const a = 2.9
			p.line(cx-a, cy, cx+a, cy, sw, g)
			p.line(cx, cy-a, cx, cy+a, sw, g)
			return
		}
		const l, o = 4.0, 0.5 // leg length, offset of the right angle from the centre
		if !c.state.Fullscreen {
			// two triangles pointing outwards: right angles at the outer corners
			p.tri(cx+o+l, cy-o-l, cx+o+l, cy-o, cx+o, cy-o-l, g)
			p.tri(cx-o-l, cy+o+l, cx-o-l, cy+o, cx-o, cy+o+l, g)
		} else {
			// inwards: right angles close to the centre
			p.tri(cx+o, cy-o, cx+o+l, cy-o, cx+o, cy-o-l, g)
			p.tri(cx-o, cy+o, cx-o-l, cy+o, cx-o, cy+o+l, g)
		}
	}
}

func (c *Controls) drawWin(p *painter) {
	h := float64(c.height() - 1) // the bar's bottom border line stays visible
	dim := 1.0
	if !c.state.Focused {
		dim = 0.5
	}
	for i, k := range c.kinds() {
		x0 := float64(i) * winButtonW
		over := c.hover && c.hit(c.pos) == k
		down := c.isDown(k)
		ic := withAlpha(winIcon, dim)
		if c.enabled(k) {
			if k == kindClose && (over || down) {
				bg := winCloseHover
				if down {
					bg = winClosePress
				}
				p.rect(x0, 0, x0+winButtonW, h, bg)
				ic = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
			} else if down {
				p.rect(x0, 0, x0+winButtonW, h, winPressed)
			} else if over {
				p.rect(x0, 0, x0+winButtonW, h, winHover)
			}
		} else {
			ic = withAlpha(winIcon, 0.3)
		}
		cx := x0 + winButtonW/2
		cy := math.Floor(h / 2)
		c.drawWinIcon(p, k, cx, cy, ic)
	}
}

// drawWinIcon draws a 10 point line icon; coordinates sit on pixel centres at 1x.
func (c *Controls) drawWinIcon(p *painter, k buttonKind, cx, cy float64, ic color.NRGBA) {
	switch k {
	case kindMinimize:
		p.rect(cx-5, cy, cx+5, cy+1, ic)
	case kindClose:
		// corner pixels of the 10x10 box: pixel centres run from -4.5 to +4.5
		p.line(cx-4.5, cy-4.5, cx+4.5, cy+4.5, 1, ic)
		p.line(cx-4.5, cy+4.5, cx+4.5, cy-4.5, 1, ic)
	case kindZoom:
		if c.state.Maximized || c.state.Fullscreen {
			// front square 8x8, back square peeking out at the top right
			box := func(x0, y0, x1, y1 float64) {
				p.rect(x0, y0, x1, y0+1, ic)
				p.rect(x0, y1-1, x1, y1, ic)
				p.rect(x0, y0, x0+1, y1, ic)
				p.rect(x1-1, y0, x1, y1, ic)
			}
			box(cx-5, cy-3, cx+3, cy+5)
			p.rect(cx-3, cy-5, cx+5, cy-4, ic)
			p.rect(cx+4, cy-5, cx+5, cy+3, ic)
			p.rect(cx-3, cy-5, cx-2, cy-3, ic)
			p.rect(cx+3, cy+2, cx+5, cy+3, ic)
			return
		}
		p.rect(cx-5, cy-5, cx+5, cy-4, ic)
		p.rect(cx-5, cy+4, cx+5, cy+5, ic)
		p.rect(cx-5, cy-5, cx-4, cy+5, ic)
		p.rect(cx+4, cy-5, cx+5, cy+5, ic)
	}
}

// drawLinux draws round hover discs with symbolic 1.5 px icons (Adwaita / Breeze like).
func (c *Controls) drawLinux(p *painter) {
	dim := 1.0
	if !c.state.Focused {
		dim = 0.5
	}
	r := math.Min(linDisc, float64(c.height())-2) / 2
	for i, k := range c.kinds() {
		cx, cy := c.discCentre(i)
		ic := withAlpha(linIcon, dim)
		if c.enabled(k) {
			over := c.hover && c.hit(c.pos) == k
			down := c.isDown(k)
			switch {
			case k == kindClose && (over || down):
				bg := studiotheme.Tally
				if down {
					bg = darken(bg, 0.8)
				}
				p.disc(cx, cy, r, bg)
				ic = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
			case down:
				p.disc(cx, cy, r, linPressed)
			case over:
				p.disc(cx, cy, r, linHover)
			}
		} else {
			ic = withAlpha(linIcon, 0.3)
		}
		c.drawLinuxIcon(p, k, cx, cy, ic)
	}
}

// drawLinuxIcon draws the symbolic icon: stroke 1.5, 8 px box.
func (c *Controls) drawLinuxIcon(p *painter, k buttonKind, cx, cy float64, ic color.NRGBA) {
	const sw, a = 1.5, 3.5 // stroke width, half size of the glyph
	switch k {
	case kindMinimize:
		p.line(cx-a, cy, cx+a, cy, sw, ic)
	case kindClose:
		p.line(cx-a, cy-a, cx+a, cy+a, sw, ic)
		p.line(cx-a, cy+a, cx+a, cy-a, sw, ic)
	case kindZoom:
		if c.state.Maximized || c.state.Fullscreen {
			// front square at the bottom left, the back one shows an L at the top right
			p.frame(cx-a-0.5, cy-a+2, cx+a-2.5, cy+a+0.5, sw, 1, ic)
			p.line(cx-a+1.5, cy-a-0.5, cx+a+0.5, cy-a-0.5, sw, ic)
			p.line(cx+a+0.5, cy-a-0.5, cx+a+0.5, cy+a-2.5, sw, ic)
			return
		}
		p.frame(cx-a, cy-a, cx+a, cy+a, sw, 1, ic)
	}
}
