// Package studiotheme is the dark editing-suite look of Frame Studio: graphite panels,
// steel blue for selection (Premiere) and a tally red reserved for the playhead (Resolve).
package studiotheme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"framestudio-proto/src/assets"
)

var (
	Gap    = color.NRGBA{0x16, 0x17, 0x1A, 0xFF} // window background between panels
	Panel  = color.NRGBA{0x22, 0x23, 0x27, 0xFF}
	Raised = color.NRGBA{0x2C, 0x2E, 0x33, 0xFF}
	Lane   = color.NRGBA{0x1B, 0x1C, 0x20, 0xFF}
	Border = color.NRGBA{0x38, 0x3A, 0x41, 0xFF}
	Text   = color.NRGBA{0xDA, 0xDB, 0xD7, 0xFF}
	Muted  = color.NRGBA{0x8B, 0x8E, 0x96, 0xFF}
	Faint  = color.NRGBA{0x55, 0x58, 0x60, 0xFF}
	// Steel is the accent colour (selection, focus, active controls). It follows
	// settings.Display.Accent: Configure updates it in place.
	Steel      = color.NRGBA{0x5A, 0x8C, 0xEB, 0xFF}
	Tally      = color.NRGBA{0xE8, 0x38, 0x3D, 0xFF}
	MonitorOut = color.NRGBA{0x00, 0x00, 0x00, 0xFF}
)

// Menu tokens (menus, context menus, command palette). Derived from the graphite
// scale above, no new hues. MenuAccentRow follows the accent: see MenuAccentRow().
var (
	MenuSurface  = color.NRGBA{0x2A, 0x2C, 0x31, 0xFF} // pane fill, one step above Panel
	MenuEdge     = color.NRGBA{0xFF, 0xFF, 0xFF, 0x29} // 1 px stroke of a pane
	MenuHover    = color.NRGBA{0xFF, 0xFF, 0xFF, 0x17}
	MenuHoverBar = color.NRGBA{0xFF, 0xFF, 0xFF, 0x1A} // bar title under the pointer
	MenuOpen     = color.NRGBA{0xFF, 0xFF, 0xFF, 0x24} // bar title whose menu is open
	MenuText     = Text
	MenuShortcut = Muted
	MenuDisabled = color.NRGBA{0xDA, 0xDB, 0xD7, 0x6B} // Text at 42 %
	MenuSep      = color.NRGBA{0xFF, 0xFF, 0xFF, 0x1F}
)

// MenuAccentRow is the current menu row: the accent at 30 % alpha.
func MenuAccentRow() color.NRGBA { return withAlpha(Steel, 0x4D) }

// ClipColors mirrors the clip colour labels of an NLE, muted so white text stays readable.
var ClipColors = []struct {
	Name  string
	Color color.NRGBA
}{
	{"Teal", color.NRGBA{0x2F, 0x8F, 0x8B, 0xFF}},
	{"Violett", color.NRGBA{0x74, 0x66, 0xC2, 0xFF}},
	{"Oliv", color.NRGBA{0x86, 0x96, 0x38, 0xFF}},
	{"Rosé", color.NRGBA{0xB4, 0x52, 0x7A, 0xFF}},
	{"Ocker", color.NRGBA{0xB8, 0x87, 0x3F, 0xFF}},
	{"Blau", color.NRGBA{0x3F, 0x73, 0xB5, 0xFF}},
	{"Mandarine", color.NRGBA{0xC8, 0x64, 0x3A, 0xFF}},
}

var (
	resRegular = fyne.NewStaticResource("RobotoCondensed-Regular.ttf", assets.FontRegular)
	resBold    = fyne.NewStaticResource("RobotoCondensed-Bold.ttf", assets.FontBold)
	resItalic  = fyne.NewStaticResource("RobotoCondensed-Italic.ttf", assets.FontItalic)
	resMono    = fyne.NewStaticResource("JetBrainsMono-Medium.ttf", assets.FontMono)
)

type Theme struct{}

func New() fyne.Theme { return Theme{} }

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

func (Theme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return Gap
	case theme.ColorNameButton:
		return Raised
	case theme.ColorNameDisabledButton:
		return Panel
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		return Faint
	case theme.ColorNameForeground:
		return Text
	case theme.ColorNameForegroundOnPrimary:
		return color.White
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return Steel
	case theme.ColorNameHover:
		return color.NRGBA{0xFF, 0xFF, 0xFF, 0x10}
	case theme.ColorNamePressed:
		return color.NRGBA{0xFF, 0xFF, 0xFF, 0x1C}
	case theme.ColorNameSelection:
		return withAlpha(Steel, 0x48)
	case theme.ColorNameInputBackground:
		return Lane
	case theme.ColorNameInputBorder, theme.ColorNameSeparator:
		return Border
	case theme.ColorNameHeaderBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return Raised
	case theme.ColorNameScrollBar:
		return withAlpha(Muted, 0x80)
	case theme.ColorNameShadow:
		return color.NRGBA{0, 0, 0, 0x66}
	case theme.ColorNameError:
		return Tally
	case ColorNameMuted:
		return Muted
	case ColorNameAccent:
		return Steel
	case ColorNameMenuDisabled:
		return MenuDisabled
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (Theme) Font(s fyne.TextStyle) fyne.Resource {
	switch {
	case s.Monospace:
		return resMono
	case s.Symbol:
		return theme.DefaultTheme().Font(s)
	case s.Bold:
		return resBold
	case s.Italic:
		return resItalic
	}
	return resRegular
}

func (Theme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (Theme) Size(n fyne.ThemeSizeName) float32 {
	if compact.Load() {
		if v, ok := compactSize(n); ok {
			return v
		}
	}
	switch n {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameHeadingText:
		return 18
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 6
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 3
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 3
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}
