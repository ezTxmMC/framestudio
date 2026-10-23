package osscale

import (
	"errors"
	"math"
	"testing"
)

func TestParseXftDPI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"125", "Xft.antialias:\t1\nXft.dpi:\t120\nXft.hinting:\t1\n", 1.25, true},
		{"96", "Xft.dpi:\t96", 1, true},
		{"192", "Xft.dpi: 192", 2, true},
		{"105", "Xft.dpi:\t105", 1.09375, true},
		{"missing", "Xft.hinting: 1", 1, false},
		{"empty", "", 1, false},
		{"garbage", "Xft.dpi:\tabc", 1, false},
		{"zero", "Xft.dpi:\t0", 1, false},
		{"negative", "Xft.dpi:\t-1", 1, false},
		{"nan", "Xft.dpi:\tNaN", 1, false},
		{"too low", "Xft.dpi:\t20", 1, false},
		{"too high", "Xft.dpi:\t5000", 1, false},
		{"xsettingsd", "Xft.dpi:\t98304", 1, false},
	}
	for _, c := range cases {
		_, got, ok := ParseXftDPI(c.in)
		if ok != c.ok || (ok && math.Abs(got-c.want) > 1e-9) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestClamp(t *testing.T) {
	for in, want := range map[float64]float64{0.5: 1, 1.01: 1, 1.02: 1, 1.25: 1.25, 9: 4, math.NaN(): 1, math.Inf(1): 1} {
		if got := Clamp(in); got != want {
			t.Errorf("Clamp(%v)=%v want %v", in, got, want)
		}
	}
}

// Trimmed from kscreen-doctor -j on the development machine: three outputs at 1.25.
const kscreenThree = `{"outputs":[
 {"name":"HDMI-A-1","enabled":true,"connected":true,"scale":1.25},
 {"name":"DP-3","enabled":true,"connected":true,"scale":1.25},
 {"name":"DP-2","enabled":true,"connected":true,"scale":1.25}]}`

func TestParseKScreenMax(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{kscreenThree, 1.25, true},
		{`{"outputs":[{"enabled":true,"scale":1},{"enabled":true,"scale":2},{"enabled":false,"scale":3}]}`, 2, true},
		{`{"outputs":[{"enabled":false,"scale":2}]}`, 1, false},
		{`{"outputs":[]}`, 1, false},
		{`not json`, 1, false},
		{``, 1, false},
	}
	for _, c := range cases {
		got, ok := ParseKScreenMax([]byte(c.in))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q: got %v %v", c.in, got, ok)
		}
	}
}

func TestXwaylandClientsScaleOff(t *testing.T) {
	if !XwaylandClientsScaleOff("[General]\nx=1\n[KScreen]\nXwaylandClientsScale=false\n") {
		t.Error("false not detected")
	}
	if XwaylandClientsScaleOff("[KScreen]\nXwaylandClientsScale=true\n") || XwaylandClientsScaleOff("[Other]\nXwaylandClientsScale=false\n") || XwaylandClientsScaleOff("") {
		t.Error("false positive")
	}
}

func fakeEnv(vars map[string]string, cmds map[string]string, files map[string]string) (Env, *[]string) {
	var calls []string
	return Env{
		Getenv: func(k string) string { return vars[k] },
		Run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name)
			if out, ok := cmds[name]; ok {
				return []byte(out), nil
			}
			return nil, errors.New("not found")
		},
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("missing")
		},
	}, &calls
}

func TestDetectChain(t *testing.T) {
	wl := map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0", "XDG_SESSION_TYPE": "wayland", "HOME": "/h"}

	e, _ := fakeEnv(wl, map[string]string{"xrdb": "Xft.dpi:\t120\n", "kscreen-doctor": kscreenThree}, nil)
	if r := Detect(e); r.Scale != 1.25 || r.Source != SourceXft || r.DPI != 120 {
		t.Errorf("xft: %+v", r)
	}

	// xrdb missing, xprop works
	e, _ = fakeEnv(wl, map[string]string{"xprop": `RESOURCE_MANAGER(STRING) = "Xft.dpi:\t144\nXft.hinting:\t1\n"`}, nil)
	if r := Detect(e); r.Scale != 1.5 || r.Source != SourceXft {
		t.Errorf("xprop: %+v", r)
	}

	// no Xft: kscreen-doctor
	e, _ = fakeEnv(wl, map[string]string{"kscreen-doctor": kscreenThree}, nil)
	if r := Detect(e); r.Scale != 1.25 || r.Source != SourceKScreen {
		t.Errorf("kscreen: %+v", r)
	}

	// Xwayland not scaled by the client: 1, hint set
	e, _ = fakeEnv(wl, map[string]string{"kscreen-doctor": kscreenThree}, map[string]string{"/h/.config/kdeglobals": "[KScreen]\nXwaylandClientsScale=false\n"})
	if r := Detect(e); r.Scale != 1 || !r.XwaylandUnscaled {
		t.Errorf("unscaled: %+v", r)
	}

	// kscreen-doctor is not asked on a plain X11 session
	x := map[string]string{"DISPLAY": ":0", "GDK_SCALE": "2", "GDK_DPI_SCALE": "0.75"}
	e, calls := fakeEnv(x, map[string]string{"kscreen-doctor": kscreenThree}, nil)
	if r := Detect(e); r.Scale != 1.5 || r.Source != SourceGDK {
		t.Errorf("gdk: %+v", r)
	}
	for _, c := range *calls {
		if c == "kscreen-doctor" {
			t.Error("kscreen-doctor asked on X11 session")
		}
	}

	e, _ = fakeEnv(map[string]string{"QT_SCALE_FACTOR": "1.75"}, nil, nil)
	if r := Detect(e); r.Scale != 1.75 || r.Source != SourceQt {
		t.Errorf("qt: %+v", r)
	}

	// nothing, garbage: 1, no panic
	e, _ = fakeEnv(map[string]string{"DISPLAY": ":0", "GDK_SCALE": "x"}, map[string]string{"xrdb": "garbage"}, nil)
	if r := Detect(e); r.Scale != 1 || r.Detected || r.Source != SourceNone {
		t.Errorf("none: %+v", r)
	}
	if r := Detect(Env{}); r.Scale != 1 {
		t.Errorf("zero env: %+v", r)
	}
}

func TestFyneRound(t *testing.T) {
	if FyneRound(1.25) != 1.3 || FyneRound(1.5) != 1.5 || FyneRound(1.75) != 1.8 {
		t.Error("rounding")
	}
	if Format(1.25) != "1.25" || Format(2) != "2" {
		t.Error("format")
	}
}
