package layout

import "testing"

func TestSaveLoadList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	l := Default()
	l.Name = "Zwei Monitore"
	l.Windows = append(l.Windows, Window{ID: "w1", Panel: "cleanfeed", Monitor: "HDMI-A-1", Mode: ModeFullscreen})
	l.Pages["edit"] = Page{Splits: map[string]float64{"main": 0.7}, Hidden: map[string]bool{"viewer": true}}
	if err := Save(l, false); err != nil {
		t.Fatal(err)
	}
	if err := Save(l, true); err != nil {
		t.Fatal(err)
	}
	got, err := Load("Zwei Monitore", false)
	if err != nil || len(got.Windows) != 2 || got.Pages["edit"].Splits["main"] != 0.7 || got.Main().Panel != "" {
		t.Fatalf("round trip failed: %v %+v", err, got)
	}
	if names := List(); len(names) != 1 || names[0] != "Zwei Monitore" {
		t.Fatalf("list %v (current must not be listed)", names)
	}
	if _, err := Load("", true); err != nil {
		t.Fatal(err)
	}
	_ = Delete("Zwei Monitore")
	if len(List()) != 0 {
		t.Fatal("delete failed")
	}
}
