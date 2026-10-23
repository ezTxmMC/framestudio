package command

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func TestReservedCombinations(t *testing.T) {
	for sc, want := range map[string]string{
		"Alt+D": "Menü Datei", "Alt+R": "Menü Arbeitsbereich", "Alt+3": "Seite", "Ctrl+5": "Seite",
		"Ctrl+Y": "wiederholt", "F10": "Menüleiste",
	} {
		if got := Reserved(sc); !strings.Contains(got, want) {
			t.Errorf("Reserved(%s) = %q, want %q", sc, got, want)
		}
	}
	for _, sc := range []string{"Alt+X", "Ctrl+D", "Alt+6", "M", "Ctrl+Shift+D"} {
		if got := Reserved(sc); got != "" {
			t.Errorf("Reserved(%s) = %q, want free", sc, got)
		}
	}
	// No catalog default collides with a reserved key.
	for _, in := range Catalog {
		if in.Shortcut != "" && Reserved(in.Shortcut) != "" {
			t.Errorf("%s default %s is reserved", in.ID, in.Shortcut)
		}
	}
}

func TestComboOfDriverShortcuts(t *testing.T) {
	cases := []struct {
		sc   fyne.Shortcut
		want string
	}{
		{&fyne.ShortcutCut{Secondary: true}, "Shift+Delete"},
		{&fyne.ShortcutPaste{Secondary: true}, "Shift+Insert"},
		{&fyne.ShortcutCopy{Secondary: true}, "Ctrl+Insert"},
		{&fyne.ShortcutCut{}, "Ctrl+X"},
		{&fyne.ShortcutUndo{}, "Ctrl+Z"},
		{&desktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierControl}, "Ctrl+S"},
	}
	for _, c := range cases {
		k, m, ok := ComboOf(c.sc)
		if got := Format(k, m); !ok || got != c.want {
			t.Errorf("%T: %q, want %q", c.sc, got, c.want)
		}
	}
}

func TestDispatchAsUsesTheGivenPageScope(t *testing.T) {
	r := NewRegistry()
	var ran []string
	r.Bind(PageScope("edit"), Binding{ID: "edit.duplicate", Run: func() { ran = append(ran, "edit") }})
	r.Bind(PageScope("color"), Binding{ID: "edit.duplicate", Run: func() { ran = append(ran, "color") }})
	r.SetScopeActive(PageScope("color"), true)
	r.DispatchAs(PageScope("edit"), fyne.KeyD, fyne.KeyModifierControl|fyne.KeyModifierShift)
	r.Dispatch(fyne.KeyD, fyne.KeyModifierControl|fyne.KeyModifierShift)
	if len(ran) != 2 || ran[0] != "edit" || ran[1] != "color" {
		t.Fatalf("ran %v", ran)
	}
}

func TestMenuLabelLeavesCatalogLabel(t *testing.T) {
	r := NewRegistry()
	step := "Clip teilen"
	r.Bind(Global, Binding{ID: "edit.undo", Run: func() {}, MenuLabel: func() string { return "Rückgängig: " + step }})
	c, _ := r.Get("edit.undo")
	if c.Label != "Rückgängig" || c.MenuLabel != "Rückgängig: Clip teilen" {
		t.Fatalf("%q / %q", c.Label, c.MenuLabel)
	}
}

func TestCatalogLabelsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, in := range Catalog {
		if other, dup := seen[in.Label]; dup {
			t.Errorf("label %q used by %s and %s", in.Label, other, in.ID)
		}
		seen[in.Label] = in.ID
		if strings.Contains(in.Label, "Playhead") {
			t.Errorf("%s: %q says Playhead, the UI says Abspielkopf", in.ID, in.Label)
		}
	}
}
