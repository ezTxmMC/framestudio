package menubar

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestShortcutText(t *testing.T) {
	if got := ShortcutText("Strg+Umschalt+S", "linux"); got != "Strg+Umschalt+S" {
		t.Fatal(got)
	}
	if got := ShortcutText("Strg+Alt+Umschalt+S", "darwin"); got != "⌥⇧⌘S" {
		t.Fatal(got)
	}
}

func TestMetricsGrid(t *testing.T) {
	st := menustyle
	for _, v := range []float32{st.RowH, st.PanePad, st.PaneRadius, st.ItemRadius, st.SepInset, st.PadSide, st.IconCol, st.IconGap, st.ArrowCol} {
		if int(v)%4 != 0 {
			t.Fatalf("%v is not a multiple of 4", v)
		}
	}
	if st.RowH != 28 || st.SepH != 9 || st.LabelX() != 36 {
		t.Fatal("row metrics")
	}
}

func TestContextMenu(t *testing.T) {
	h := newHarness(t)
	var ran []string
	items := []Item{
		{Label: "Eins", Action: func() { ran = append(ran, "1") }},
		{Label: "Aus", Disabled: true, Action: func() { ran = append(ran, "x") }},
		Sep(),
		{Label: "Mehr", Children: []Item{{Label: "Zwei", Action: func() { ran = append(ran, "2") }}}},
	}
	ShowContextMenu(h.c(), fyne.NewPos(10, 10), items)
	l := ctxLayer
	if l == nil || len(l.panes) != 1 {
		t.Fatal("not open")
	}
	p := l.panes[0]
	if p.size.Width < 200 || p.tops[0] != 4 || p.tops[3] != 4+28+28+9 {
		t.Fatalf("geometry %v %v", p.size, p.tops)
	}
	test.TapCanvas(h.c(), p.Position().Add(fyne.NewPos(60, p.tops[1]+10)))
	if ctxLayer == nil || len(ran) != 0 {
		t.Fatal("disabled row acted")
	}
	test.TapCanvas(h.c(), p.Position().Add(fyne.NewPos(60, p.tops[3]+10)))
	if len(l.panes) != 2 {
		t.Fatal("submenu not open")
	}
	l.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	l.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	l.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(ran) != 1 || ran[0] != "1" || ctxLayer != nil || len(h.c().Overlays().List()) != 0 {
		t.Fatalf("ran %v", ran)
	}
	// flips left/up at the corner
	ShowContextMenu(h.c(), fyne.NewPos(1270, 790), items)
	p = ctxLayer.panes[0]
	if p.Position().X+p.size.Width > 1280 || p.Position().Y+p.size.Height > 800 {
		t.Fatal("leaves the canvas")
	}
	CloseContextMenu()
}

func TestFromFyneMenu(t *testing.T) {
	m := fyne.NewMenu("", fyne.NewMenuItem("A", nil), fyne.NewMenuItemSeparator(), fyne.NewMenuItem("B", nil))
	m.Items[2].Disabled = true
	got := FromFyneMenu(m)
	if len(got) != 3 || !got[1].Separator || !got[2].Disabled || got[0].Label != "A" {
		t.Fatalf("%+v", got)
	}
}

func TestScrollingPane(t *testing.T) {
	h := newHarness(t)
	var items []Item
	for i := 0; i < 60; i++ {
		items = append(items, Item{Label: "Zeile"})
	}
	ShowContextMenu(h.c(), fyne.NewPos(10, 10), items)
	p := ctxLayer.panes[0]
	if !p.scrolls() || p.size.Height > 800-16 {
		t.Fatalf("pane %v scrolls %v", p.size, p.scrolls())
	}
	ctxLayer.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
	if p.hover != 59 || p.offset() <= 0 {
		t.Fatalf("end: hover %d offset %v", p.hover, p.offset())
	}
	CloseContextMenu()
}
