package menubar

import (
	"runtime"
	"strings"
	"time"
)

// Metrics are the geometry of every menu surface: bar, dropdown, submenu, context menu and
// palette. All values are Fyne units at scale 1; the multiples of 4 are whole device
// pixels at 1.25 and 1.5 as well. Positions are snapped with kit.Snap on top.
type Metrics struct {
	RowH, SepH   float32 // row 28, separator slot 9 (4 above, line, 4 below)
	PanePad      float32 // inner padding of a pane (row highlight inset), 4
	PaneRadius   float32 // 8 (concentric with ItemRadius + PanePad)
	ItemRadius   float32 // highlight radius, 4
	SepInset     float32 // separator line inset from the pane edge, 8
	PadSide      float32 // row padding before the icon column and after the arrow, 8
	IconCol      float32 // icon / check column, 20
	IconGap      float32 // gap between icon column and label, 8
	LabelGap     float32 // minimum gap between label and shortcut, 28
	ShortcutGap  float32 // gap between shortcut and arrow column, 8
	ArrowCol     float32 // submenu chevron column, 16
	MinPaneW     float32 // 200
	ScreenMargin float32 // keep panes this far from the canvas edge, 4
	LabelSize    float32 // 13
	ShortcutSize float32 // 12
	IconSize     float32 // 16
	SwatchSize   float32 // 12
	SubOverlap   float32 // a submenu overlaps its parent by 6
	SubDrop      float32 // and starts 4 above the parent row (aligns the first rows)
	BarOverlap   float32 // a dropdown overlaps the bar line by 1
	ShadowLayers int
	ShadowAlpha  [4]uint8
	PageRows     int // PageUp/PageDown step in a scrolling pane
	TitleSize    float32
	TitlePadX    float32
	PillInsetY   float32 // 3
	PillRadius   float32 // 4
	CompactTitle float32 // title text size in the 24 px bar
	CompactPadX  float32

	PaletteW       float32
	PaletteSearchH float32
}

// menustyle is the one metrics instance.
var menustyle = Metrics{
	RowH: 28, SepH: 9, PanePad: 4, PaneRadius: 8, ItemRadius: 4, SepInset: 8,
	PadSide: 8, IconCol: 20, IconGap: 8, LabelGap: 28, ShortcutGap: 8, ArrowCol: 16,
	MinPaneW: 200, ScreenMargin: 4, LabelSize: 13, ShortcutSize: 12, IconSize: 16, SwatchSize: 12,
	SubOverlap: 6, SubDrop: 4, BarOverlap: 1,
	ShadowLayers: 4, ShadowAlpha: [4]uint8{0x1C, 0x14, 0x0D, 0x07},
	PageRows: 8, TitleSize: 13, TitlePadX: 10, PillInsetY: 3, PillRadius: 4,
	CompactTitle: 12, CompactPadX: 8,
	PaletteW: 620, PaletteSearchH: 40,
}

// LabelX is the x of the label from the left edge of a row.
func (m Metrics) LabelX() float32 { return m.PadSide + m.IconCol + m.IconGap }

// compactBar is the bar height up to which the compact title metrics apply.
const compactBar = 24

// Timing of hover actions; tests set them to 0 to act synchronously.
var (
	openDelay      = 250 * time.Millisecond // hover on a submenu row before it opens
	safeDelay      = 300 * time.Millisecond // close delay while the pointer heads for the open submenu or leaves it
	ignoreUpWithin = 100 * time.Millisecond // a mouse-up this soon after opening does not activate
)

var runtimeGOOS = runtime.GOOS

// ShortcutText converts a German shortcut display text ("Strg+Umschalt+S") for the
// operating system goos: unchanged everywhere except darwin, where it becomes the glyph
// string in the order Control, Option, Shift, Command ("⌥⇧⌘S", no separators).
func ShortcutText(display, goos string) string {
	if goos != "darwin" || display == "" {
		return display
	}
	parts := strings.Split(display, "+")
	if n := len(parts); n > 1 && parts[n-1] == "" { // the key itself is "+"
		parts = append(parts[:n-2], "+")
	}
	var opt, shift, cmd bool
	var key []string
	for i, p := range parts {
		if i < len(parts)-1 {
			switch p {
			case "Strg", "Super":
				cmd = true
				continue
			case "Alt":
				opt = true
				continue
			case "Umschalt":
				shift = true
				continue
			}
		}
		key = append(key, p)
	}
	var b strings.Builder
	if opt {
		b.WriteString("⌥")
	}
	if shift {
		b.WriteString("⇧")
	}
	if cmd {
		b.WriteString("⌘")
	}
	b.WriteString(strings.Join(key, "+"))
	return b.String()
}
