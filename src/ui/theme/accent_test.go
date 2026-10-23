package studiotheme

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func TestConfigureSwitchesAccentAndDensity(t *testing.T) {
	t.Cleanup(func() { Configure("steel", false) })
	th := New()
	if !Configure("teal", false) {
		t.Fatal("switching to teal should report a change")
	}
	teal := AccentColor("teal")
	if Steel != teal {
		t.Fatalf("Steel = %v, want %v", Steel, teal)
	}
	if got := th.Color(theme.ColorNamePrimary, theme.VariantDark); got != teal {
		t.Fatalf("primary = %v, want teal", got)
	}
	if got := th.Color(theme.ColorNameFocus, theme.VariantDark); got != teal {
		t.Fatalf("focus = %v, want teal", got)
	}
	sel := th.Color(theme.ColorNameSelection, theme.VariantDark).(color.NRGBA)
	if sel.R != teal.R || sel.G != teal.G || sel.B != teal.B || sel.A == 0xFF {
		t.Fatalf("selection = %v, want translucent teal", sel)
	}
	if Configure("teal", false) {
		t.Fatal("same accent again must not report a change")
	}
	if Configure("unknown", false); Steel != Accents[0].Color {
		t.Fatalf("unknown accent should fall back to steel, got %v", Steel)
	}

	normal := th.Size(theme.SizeNamePadding)
	if !Configure("steel", true) || !Compact() {
		t.Fatal("compact should switch on")
	}
	if th.Size(theme.SizeNamePadding) >= normal || th.Size(theme.SizeNameText) >= 13 {
		t.Fatal("compact density should lower padding and text size")
	}
	Configure("steel", false)
	if th.Size(theme.SizeNamePadding) != normal {
		t.Fatal("normal density should restore the padding")
	}
}

func TestRefreshSetsTheme(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	t.Cleanup(func() { Configure("steel", false) })
	Configure("amber", false)
	Refresh()
	if got := a.Settings().Theme().Color(theme.ColorNamePrimary, theme.VariantDark); got != AccentColor("amber") {
		t.Fatalf("app theme primary = %v, want amber", got)
	}
}

func TestAccentsDistinct(t *testing.T) {
	seen := map[color.NRGBA]bool{}
	for _, a := range Accents {
		if seen[a.Color] || a.Color == Tally {
			t.Fatalf("accent %s clashes", a.ID)
		}
		seen[a.Color] = true
	}
}
