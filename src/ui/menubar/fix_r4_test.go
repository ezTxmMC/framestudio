package menubar

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"framestudio-proto/src/ui/command"
	studiotheme "framestudio-proto/src/ui/theme"
)

// paletteRowOf returns the rendered palette row of command id.
func paletteRowOf(t *testing.T, p *palette, id string) *paletteRowRenderer {
	t.Helper()
	for _, o := range p.list.Objects {
		if r, ok := o.(*paletteRow); ok && r.e.kind == kindCommand && r.e.cmd.ID == id {
			return test.WidgetRenderer(r).(*paletteRowRenderer)
		}
	}
	t.Fatalf("palette has no row %s", id)
	return nil
}

// Toggle commands show their state in the palette, like the menus.
func TestPaletteShowsToggleState(t *testing.T) {
	h := newHarness(t)
	p := showPalette(h.w, h.reg)
	r := paletteRowOf(t, p, "timeline.snapping")
	if r.check != nil {
		t.Fatal("snapping is off: expected no mark")
	}
	p.close()
	h.snap = true
	p = showPalette(h.w, h.reg)
	if r := paletteRowOf(t, p, "timeline.snapping"); r.check == nil {
		t.Fatal("snapping is on: expected a check mark")
	}
	if r := paletteRowOf(t, p, "file.save"); r.check != nil {
		t.Fatal("plain actions show no state")
	}
	p.close()
}

// Shortcuts typed while a menu is open close it and go to the window (OnCombo), also
// the driver's own types such as Strg+Z; Alt+letter still switches menus.
func TestOpenMenuPassesShortcutsOn(t *testing.T) {
	h := newHarness(t)
	var got []string
	h.bar.OnCombo = func(k fyne.KeyName, m fyne.KeyModifier) { got = append(got, command.Format(k, m)) }
	h.openTitle("Bearbeiten")
	h.bar.layer.TypedShortcut(&fyne.ShortcutUndo{})
	if h.bar.IsOpen() || len(got) != 1 || got[0] != "Ctrl+Z" {
		t.Fatalf("Strg+Z: open %v, got %v", h.bar.IsOpen(), got)
	}
	h.openTitle("Bearbeiten")
	h.bar.layer.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.Key3, Modifier: fyne.KeyModifierAlt})
	h.openTitle("Bearbeiten")
	h.bar.layer.TypedShortcut(&fyne.ShortcutCut{Secondary: true})
	if len(got) != 3 || got[1] != "Alt+3" || got[2] != "Shift+Delete" {
		t.Fatalf("got %v", got)
	}
	h.openTitle("Bearbeiten")
	h.bar.layer.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyD, Modifier: fyne.KeyModifierAlt})
	if !h.bar.IsOpen() || h.bar.active != h.menuIndex("Datei") {
		t.Fatal("Alt+D did not switch to Datei")
	}
	h.bar.Close()
}

// After the window lost focus a held Alt is forgotten.
func TestFocusLossForgetsAlt(t *testing.T) {
	h := newHarness(t)
	alt := &fyne.KeyEvent{Name: desktop.KeyAltLeft}
	h.bar.HandleKeyDown(alt)
	h.bar.WindowFocusLost()
	if h.bar.HandleKeyUp(alt) || h.bar.IsOpen() {
		t.Fatal("synthetic Alt release focused the bar")
	}
}

// The current-row fill follows the accent.
func TestCurrentRowFollowsAccent(t *testing.T) {
	old := studiotheme.Steel
	t.Cleanup(func() { studiotheme.Steel = old })
	before := studiotheme.MenuAccentRow()
	studiotheme.Configure("amber", false)
	if studiotheme.MenuAccentRow() == before {
		t.Fatal("current row did not follow the accent")
	}
}

// Submenus show short labels; the palette keeps the full ones.
func TestSubmenuShortLabels(t *testing.T) {
	h := newHarness(t)
	h.reg.Bind(command.Global, command.Binding{ID: "timeline.follow.page", Run: func() {}})
	h.openTitle("Timeline")
	h.bar.layer.openSub(0, h.rowIndex(0, "Timeline folgt dem Abspielkopf"), false)
	if h.pane(1).rows[0].label != "Seitenweise" {
		t.Fatalf("submenu row %q", h.pane(1).rows[0].label)
	}
	h.bar.Close()
	if c, _ := h.reg.Get("timeline.follow.page"); c.Label != "Timeline folgt dem Abspielkopf: seitenweise" {
		t.Fatalf("catalog label %q", c.Label)
	}
}
