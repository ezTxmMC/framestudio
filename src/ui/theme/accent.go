package studiotheme

import (
	"image/color"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Extra colour names for rich text and themed widgets: secondary text (Muted) and the
// current accent (Steel).
const (
	ColorNameMuted  fyne.ThemeColorName = "fsMuted"
	ColorNameAccent fyne.ThemeColorName = "fsAccent"
	// ColorNameMenuDisabled tints the icons of disabled menu rows (MenuDisabled).
	ColorNameMenuDisabled fyne.ThemeColorName = "fsMenuDisabled"
)

// Accent is a selectable accent colour.
type Accent struct {
	ID    string // settings value: "steel", "teal", "amber", "violet"
	Label string // German name for the settings window
	Color color.NRGBA
}

// Accents lists the accent colours in display order; the first is the default. All of
// them keep white text readable and stay clear of the tally red of the playhead.
var Accents = []Accent{
	{"steel", "Stahlblau", color.NRGBA{0x5A, 0x8C, 0xEB, 0xFF}},
	{"teal", "Petrol", color.NRGBA{0x2E, 0xA8, 0x9E, 0xFF}},
	{"amber", "Bernstein", color.NRGBA{0xD6, 0x9A, 0x3C, 0xFF}},
	{"violet", "Violett", color.NRGBA{0x8E, 0x78, 0xE0, 0xFF}},
}

// AccentColor returns the colour of accent id (the default for unknown IDs).
func AccentColor(id string) color.NRGBA {
	for _, a := range Accents {
		if a.ID == id {
			return a.Color
		}
	}
	return Accents[0].Color
}

var compact atomic.Bool

// Compact reports whether the compact density is active.
func Compact() bool { return compact.Load() }

// Configure sets the accent colour and density. It updates Steel in place, so widgets
// built afterwards (and the theme's Primary, Focus and Selection colours) use the new
// accent. UI thread only. It reports whether anything changed; callers then repaint
// with Refresh.
func Configure(accent string, dense bool) (changed bool) {
	c := AccentColor(accent)
	if c != Steel {
		Steel = c
		changed = true
	}
	if compact.Swap(dense) != dense {
		changed = true
	}
	return changed
}

// Refresh re-applies the studio theme to the running app so every themed widget repaints
// with the current accent and density. UI thread only; a no-op without an app.
func Refresh() {
	if a := fyne.CurrentApp(); a != nil {
		a.Settings().SetTheme(New())
	}
}

// compactSize is the dense variant of the sizes: less padding, slightly smaller text.
func compactSize(n fyne.ThemeSizeName) (float32, bool) {
	switch n {
	case theme.SizeNameText:
		return 12, true
	case theme.SizeNameCaptionText:
		return 10, true
	case theme.SizeNameHeadingText:
		return 16, true
	case theme.SizeNamePadding:
		return 3, true
	case theme.SizeNameInnerPadding:
		return 4, true
	case theme.SizeNameInlineIcon:
		return 16, true
	}
	return 0, false
}
