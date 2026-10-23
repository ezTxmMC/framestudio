package chrome

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

func TestResolveStyle(t *testing.T) {
	goses := map[string]string{"darwin": StyleMac, "windows": StyleWin, "linux": StyleLinux,
		"freebsd": StyleLinux, "openbsd": StyleLinux, "netbsd": StyleLinux, "solaris": StyleLinux, "": StyleLinux}
	for goos, auto := range goses {
		for _, set := range []string{"auto", "", "bogus", "MAC"} {
			if got := ResolveStyle(set, goos); got != auto {
				t.Errorf("%q on %q: %s, want %s", set, goos, got, auto)
			}
		}
		for _, set := range []string{StyleMac, StyleWin, StyleLinux} {
			if got := ResolveStyle(set, goos); got != set {
				t.Errorf("explicit %q on %q: %s", set, goos, got)
			}
		}
	}
}

func styleBar(t *testing.T, style string, st State) (*Titlebar, *fakeBackend) {
	fb := newFake()
	fb.st = st
	o := full
	o.Style = style
	tb, _ := newBar(t, o, fb)
	return tb, fb
}

func TestStyleLayouts(t *testing.T) {
	cases := []struct {
		style string
		right bool
		order [3]buttonKind
		width float32
	}{
		{StyleMac, false, [3]buttonKind{kindClose, kindMinimize, kindZoom}, 76},
		{StyleWin, true, [3]buttonKind{kindMinimize, kindZoom, kindClose}, 138},
		{StyleLinux, true, [3]buttonKind{kindMinimize, kindZoom, kindClose}, 82},
	}
	for _, c := range cases {
		tb, _ := styleBar(t, c.style, State{Focused: true})
		ctl := tb.Controls()
		if ctl.kinds() != c.order || ctl.MinSize().Width != c.width {
			t.Fatalf("%s: order %v width %v", c.style, ctl.kinds(), ctl.MinSize().Width)
		}
		if c.right != (ctl.Position().X > 100) || (c.right && ctl.Position().X+ctl.Size().Width != 600) {
			t.Fatalf("%s: controls at %v", c.style, ctl.Position())
		}
		// every button has a hit area spanning the whole bar height, in order
		for i, k := range c.order {
			var x float32
			if c.style == StyleMac {
				cx, _ := ctl.macCentre(i)
				x = float32(cx)
			} else {
				x0, x1 := ctl.slot(i)
				x = float32(x0+x1) / 2
			}
			for _, y := range []float32{1, 14, 26} {
				if c.style != StyleMac || y == 14 {
					if got := ctl.hit(pt(x, y)); got != k {
						t.Fatalf("%s: button %d at (%v,%v) hit %v want %v", c.style, i, x, y, got, k)
					}
				}
			}
		}
		if c.style == StyleWin && (ctl.hit(pt(45.9, 5)) != kindMinimize || ctl.hit(pt(46, 5)) != kindZoom) {
			t.Fatal("win buttons are 46 wide")
		}
		if c.style == StyleLinux {
			if ctl.hit(pt(1, 14)) != kindNone || ctl.hit(pt(81, 14)) != kindNone {
				t.Fatal("linux padding is not a button")
			}
			x0, x1 := ctl.slot(0)
			if x1-x0 != 24 {
				t.Fatalf("linux hit width %v", x1-x0)
			}
		}
	}
}

func TestStyleActions(t *testing.T) {
	for _, style := range []string{StyleWin, StyleLinux} {
		tb, fb := styleBar(t, style, State{Focused: true})
		c := tb.Controls()
		for i := 0; i < 3; i++ {
			x0, x1 := c.slot(i)
			click(c, pt(float32(x0+x1)/2, 14), 0)
		}
		want := []string{"min", "max", "close"}
		if len(fb.calls) != 3 {
			t.Fatalf("%s calls %v", style, fb.calls)
		}
		for i := range want {
			if fb.calls[i] != want[i] {
				t.Fatalf("%s calls %v", style, fb.calls)
			}
		}
		// Alt does not turn maximize into anything else, no fullscreen
		x0, x1 := c.slot(1)
		fb.calls = nil
		click(c, pt(float32(x0+x1)/2, 14), fyne.KeyModifierAlt)
		if len(fb.calls) != 1 || fb.calls[0] != "max" {
			t.Fatalf("%s alt: %v", style, fb.calls)
		}
	}
	tb, fb := styleBar(t, StyleMac, State{Focused: true})
	click(tb.Controls(), posZoom, 0)
	click(tb.Controls(), posZoom, fyne.KeyModifierAlt)
	if len(fb.calls) != 2 || fb.calls[0] != "fs-on" || fb.calls[1] != "max" {
		t.Fatalf("mac zoom: %v", fb.calls)
	}
	// without maximize the zoom button of win/linux does nothing
	for _, style := range []string{StyleWin, StyleLinux} {
		fb := newFake()
		tb, _ := newBar(t, Options{Style: style, CanFullscreen: true}, fb)
		x0, x1 := tb.Controls().slot(1)
		click(tb.Controls(), pt(float32(x0+x1)/2, 14), 0)
		if len(fb.calls) != 0 {
			t.Fatalf("%s: %v", style, fb.calls)
		}
	}
}

func TestLinuxTitleCentredAndHover(t *testing.T) {
	tb, _ := styleBar(t, StyleLinux, State{Focused: true})
	title := widget.NewLabel("Projekt")
	tb.center = title
	tb.Refresh()
	m := title.MinSize().Width
	mid := title.Position().X + m/2
	free := 600 - tb.Controls().MinSize().Width
	if title.Size().Width != m || mid < free/2-1 || mid > free/2+1 {
		t.Fatalf("title at %v size %v, free %v", title.Position(), title.Size(), free)
	}
	c := tb.Controls()
	c.MouseMoved(down(0, 0, pt(18, 14)))
	if !c.hover || c.hit(c.pos) != kindMinimize {
		t.Fatal("hover on first disc")
	}
	c.MouseOut()
	if c.hover {
		t.Fatal("hover stays")
	}
}

func TestLiveSwitchAllStyles(t *testing.T) {
	tb, _ := styleBar(t, StyleMac, State{Focused: true})
	for _, st := range []string{StyleLinux, StyleWin, StyleMac, StyleWin, StyleLinux, StyleMac} {
		tb.SetStyle(st)
		right := st != StyleMac
		if tb.Style() != st || (tb.Controls().Position().X > 0) != right {
			t.Fatalf("%s: at %v", st, tb.Controls().Position())
		}
		if tb.Controls().Size().Width != tb.Controls().MinSize().Width {
			t.Fatalf("%s: size %v", st, tb.Controls().Size())
		}
	}
}

func TestStylesDrawInAllStates(t *testing.T) {
	for _, style := range []string{StyleMac, StyleWin, StyleLinux} {
		for _, st := range []State{{Focused: true}, {}, {Focused: true, Maximized: true}, {Fullscreen: true}} {
			tb, _ := styleBar(t, style, st)
			c := tb.Controls()
			c.MouseIn(down(0, 0, pt(10, 14)))
			c.draw(300, 56)
		}
	}
}

func TestPressedLookFollowsPointer(t *testing.T) {
	for _, style := range []string{StyleWin, StyleLinux} {
		tb, _ := styleBar(t, style, State{Focused: true})
		c := tb.Controls()
		p := c.centreOf(2)
		c.MouseIn(down(0, 0, p))
		c.MouseDown(down(desktop.MouseButtonPrimary, 0, p))
		if !c.isDown(kindClose) {
			t.Fatalf("%s: not pressed over the button", style)
		}
		c.MouseOut()
		if c.isDown(kindClose) {
			t.Fatalf("%s: pressed look stays after leaving", style)
		}
		c.MouseMoved(down(0, 0, p))
		if !c.isDown(kindClose) {
			t.Fatalf("%s: pressed look not restored on return", style)
		}
		c.MouseUp(down(desktop.MouseButtonPrimary, 0, pt(-1, -1)))
	}
}
