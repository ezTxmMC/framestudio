package command

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestCatalogConsistent(t *testing.T) {
	seen := map[string]bool{}
	shortcuts := map[string]string{}
	for _, in := range Catalog {
		if seen[in.ID] {
			t.Errorf("duplicate id %s", in.ID)
		}
		seen[in.ID] = true
		if in.Shortcut != "" {
			n := Normalize(in.Shortcut)
			if n == "" {
				t.Errorf("%s: invalid shortcut %q", in.ID, in.Shortcut)
			}
			if other, dup := shortcuts[n]; dup {
				t.Errorf("shortcut %s used by %s and %s", n, other, in.ID)
			}
			shortcuts[n] = in.ID
		}
	}
	var walk func(items []Item)
	walk = func(items []Item) {
		for _, it := range items {
			if it.Command != "" && !seen[it.Command] {
				t.Errorf("menu references unknown command %s", it.Command)
			}
			if it.Submenu != nil {
				walk(it.Submenu.Items)
			}
		}
	}
	for _, m := range MenuBar {
		walk(m.Items)
	}
}

func TestShortcutParsing(t *testing.T) {
	cases := map[string]string{"ctrl+shift+z": "Ctrl+Shift+Z", "Strg+,": "Ctrl+,", "Shift+Entf": "Shift+Delete",
		"space": "Space", "Ctrl++": "Ctrl++", "f11": "F11", "Alt+1": "Alt+1", "Shift+Ctrl+S": "Ctrl+Shift+S"}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Display("Ctrl+Shift+Delete") != "Strg+Umschalt+Entf" {
		t.Error(Display("Ctrl+Shift+Delete"))
	}
}

func TestScopesAndDispatch(t *testing.T) {
	r := NewRegistry()
	var got []string
	r.Bind(Global, Binding{ID: "play.toggle", Run: func() { got = append(got, "global") }})
	r.Bind(PageScope("edit"), Binding{ID: "play.toggle", Run: func() { got = append(got, "edit") }})
	if !r.Dispatch(fyne.KeySpace, 0) || got[0] != "global" {
		t.Fatal("inactive page scope must not win")
	}
	r.SetScopeActive(PageScope("edit"), true)
	r.Dispatch(fyne.KeySpace, 0)
	if got[1] != "edit" {
		t.Fatal("active page scope should win")
	}
	r.SetShortcut("play.toggle", "Ctrl+P")
	if r.Dispatch(fyne.KeySpace, 0) || !r.Dispatch("P", fyne.KeyModifierControl) {
		t.Fatal("keymap override not honoured")
	}
	if c := r.Conflicts("Ctrl+S", ""); len(c) != 1 || c[0] != "file.save" {
		t.Fatalf("conflicts %v", c)
	}
	on := false
	r.Bind(Global, Binding{ID: "play.loop", Run: func() { on = !on }, Checked: func() bool { return on }, Enabled: func() bool { return true }})
	r.Run("play.loop")
	if c, _ := r.Get("play.loop"); !c.Toggle || !c.Checked || !c.Enabled {
		t.Fatalf("toggle state wrong %+v", c)
	}
	if c, _ := r.Get("file.save"); c.Available || c.Enabled {
		t.Fatal("unbound command must be unavailable")
	}
	r.Unbind(PageScope("edit"))
	r.SetKeymap(nil)
	r.Dispatch(fyne.KeySpace, 0)
	if got[len(got)-1] != "global" {
		t.Fatal("unbind failed")
	}
}
