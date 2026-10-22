package timeline

import (
	"image"
	"image/color"
	"math"
	"slices"
	"testing"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
)

func geomProject() *model.Project {
	p := model.NewProject("g") // V1, V2, A1
	p.AddTrack(model.TrackAudio)
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "A", 0, 100))
	_ = p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "B", 100, 50))
	return p
}

func TestDisplayOrder(t *testing.T) {
	p := geomProject()
	p.AddTrack(model.TrackVideo) // V3
	got := displayOrder(p)
	var names []string
	for _, i := range got {
		names = append(names, p.Tracks[i].Name)
	}
	if want := []string{"V3", "V2", "V1", "A1", "A2"}; !slices.Equal(names, want) {
		t.Fatalf("order %v, want %v", names, want)
	}
}

func TestFrameXRoundTrip(t *testing.T) {
	p := geomProject()
	g := newGeom(p, 1000, 400, 4, 30, 0)
	if g.offset != 30 {
		t.Fatalf("offset %v", g.offset)
	}
	for _, f := range []float64{0, 30, 77.5, 240} {
		if got := g.xFrame(g.frameX(f)); math.Abs(got-f) > 1e-3 {
			t.Errorf("round trip %v -> %v", f, got)
		}
	}
	if x := g.frameX(30); x != headerW {
		t.Errorf("frame at offset should be at the lane edge, got %v", x)
	}
	if g.frameAt(headerW-500) != 0 {
		t.Error("frameAt must not go negative")
	}
}

func TestZoomOneFitsProgram(t *testing.T) {
	p := geomProject()
	g := newGeom(p, 1000, 400, 1, 500, 0)
	if g.offset != 0 {
		t.Fatalf("zoom 1 must not scroll, offset %v", g.offset)
	}
	if x := g.frameX(float64(p.Duration())); x > 1000 {
		t.Fatalf("program end at %v is off screen", x)
	}
	z := newGeom(p, 1000, 400, 60, 1e9, 0)
	if z.offset <= 0 || z.offset > float64(max(p.Duration(), 250)) {
		t.Fatalf("offset not clamped: %v", z.offset)
	}
}

func TestRowsAndVerticalScroll(t *testing.T) {
	p := geomProject()
	g := newGeom(p, 800, 200, 1, 0, 1000)
	if g.content != 2*videoRowH+2*audioRowH {
		t.Fatalf("content height %v", g.content)
	}
	if want := g.content - (200 - rulerH); g.vscroll() != want {
		t.Fatalf("vscroll %v, want clamped %v", g.vscroll(), want)
	}
	g = newGeom(p, 800, 600, 1, 0, 0)
	if g.rows[0].y != rulerH || g.rows[1].y != rulerH+videoRowH {
		t.Fatalf("rows %+v", g.rows)
	}
	if g.rowAt(rulerH+videoRowH+1) != 1 || g.rowAt(5) != -1 || g.rowAt(599) != -1 {
		t.Fatal("rowAt wrong")
	}
	if p.Tracks[g.rows[2].track].Kind != model.TrackAudio {
		t.Fatal("audio rows must follow video rows")
	}
}

func TestRulerStep(t *testing.T) {
	cases := []struct {
		ppf          float64
		fps          int
		major, minor int
	}{
		{ppf: 100, fps: 25, major: 1, minor: 1},
		{ppf: 10, fps: 25, major: 10, minor: 1},
		{ppf: 3.5, fps: 25, major: 50, minor: 5},
		{ppf: 0.05, fps: 25, major: 3000, minor: 300},
	}
	for _, c := range cases {
		major, minor := rulerStep(c.ppf, c.fps, labelGap)
		if major != c.major || minor != c.minor {
			t.Errorf("ppf %v: got %d/%d, want %d/%d", c.ppf, major, minor, c.major, c.minor)
		}
		if float64(major)*c.ppf < labelGap {
			t.Errorf("ppf %v: labels %v px apart", c.ppf, float64(major)*c.ppf)
		}
		if major%minor != 0 {
			t.Errorf("minor %d does not divide major %d", minor, major)
		}
	}
}

func TestSnapping(t *testing.T) {
	ts := []int{0, 50, 100, 140}
	if f, ok := snapFrame(97, ts, 4); !ok || f != 100 {
		t.Errorf("snapFrame 97 -> %d %v", f, ok)
	}
	if f, ok := snapFrame(120, ts, 4); ok || f != 120 {
		t.Errorf("snapFrame 120 -> %d %v", f, ok)
	}
	// a 30-frame clip starting at 108 ends at 138: its end snaps to 140
	if s, at, ok := snapSpan(108, 30, ts, 4); !ok || s != 110 || at != 140 {
		t.Errorf("snapSpan end -> %d %d %v", s, at, ok)
	}
	// start is closer than end
	if s, at, ok := snapSpan(99, 30, ts, 4); !ok || s != 100 || at != 100 {
		t.Errorf("snapSpan start -> %d %d %v", s, at, ok)
	}

	p := geomProject()
	p.AddMarker(70, "M")
	excl := p.Tracks[0].Clips[1].ID
	got := snapTargets(p, 33, 10, 20, excl)
	if want := []int{0, 10, 20, 33, 70, 100}; !slices.Equal(got, want) {
		t.Errorf("targets %v, want %v", got, want)
	}
	if got := snapTargets(p, -1, 0, 0, ""); !slices.Equal(got, []int{0, 70, 100, 150}) {
		t.Errorf("targets without playhead/range %v", got)
	}
}

func TestHitTest(t *testing.T) {
	p := geomProject()
	g := newGeom(p, 1000, 400, 1, 0, 0)
	v1 := g.rows[g.rowOfTrack(0)]
	mid := v1.y + v1.h/2
	x0, x1 := g.clipSpan(p.Tracks[0].Clips[0])

	if h := g.hitTest(p, fyne.NewPos(500, 5)); h.area != areaRuler {
		t.Errorf("ruler: %+v", h)
	}
	if h := g.hitTest(p, fyne.NewPos(5, 5)); h.area != areaCorner {
		t.Errorf("corner: %+v", h)
	}
	h := g.hitTest(p, fyne.NewPos((x0+x1)/2, mid))
	if h.area != areaLane || h.track != 0 || h.clip != 0 || h.part != partBody {
		t.Errorf("body: %+v", h)
	}
	if h := g.hitTest(p, fyne.NewPos(x0+2, mid)); h.part != partLeft {
		t.Errorf("left edge: %+v", h)
	}
	if h := g.hitTest(p, fyne.NewPos(x1-2, mid)); h.part != partRight || h.clip != 0 {
		t.Errorf("right edge: %+v", h)
	}
	if h := g.hitTest(p, fyne.NewPos(x1+2, mid)); h.clip != 1 || h.part != partLeft {
		t.Errorf("next clip's left edge: %+v", h)
	}
	if h := g.hitTest(p, fyne.NewPos(990, mid)); h.clip != -1 || h.track != 0 {
		t.Errorf("empty lane: %+v", h)
	}
	a1 := g.rows[g.rowOfTrack(2)]
	for _, b := range headerButtons(model.TrackAudio, a1) {
		c := fyne.NewPos(b.rect.X+b.size.Width/2, b.rect.Y+b.size.Height/2)
		if h := g.hitTest(p, c); h.area != areaHeader || h.button != b.b || h.track != 2 {
			t.Errorf("button %v: %+v", b.b, h)
		}
	}
	if h := g.hitTest(p, fyne.NewPos(20, a1.y+a1.h/2)); h.button != btnNone || h.track != 2 {
		t.Errorf("header name: %+v", h)
	}
}

func TestGrabWidthShortClips(t *testing.T) {
	if grabWidth(100) != edgeGrab || grabWidth(9) != 3 {
		t.Fatal("grab width")
	}
}

func TestTrimLimits(t *testing.T) {
	p := geomProject()
	m := model.MediaItem{ID: "m", Kind: model.KindVideo, Duration: 8e9} // 200 frames
	p.AddMedia(m)
	c := p.ClipFromMedia(m, 300, 0)
	c.SourceIn, c.Length = 40, 100
	_ = p.PlaceClip(p.Tracks[0].ID, c)
	tr := &p.Tracks[0]
	lo, hi := trimLimits(p, tr, 2)
	if lo != 260 || hi != 460 {
		t.Errorf("media limits %d..%d, want 260..460", lo, hi)
	}
	lo, hi = trimLimits(p, tr, 1)
	if lo != 100 || hi != 300 {
		t.Errorf("neighbour limits %d..%d, want 100..300", lo, hi)
	}
}

func TestWaveImage(t *testing.T) {
	col := color.NRGBA{200, 200, 200, 255}
	img := waveImage(40, 21, waveSpec{peaks: []float32{0, 1}, srcFrames: 40, fpp: 1, gain: 1, col: col})
	if img.NRGBAAt(5, 0) != (color.NRGBA{}) || img.NRGBAAt(35, 1) != col {
		t.Fatal("silent half must be empty and loud half full height")
	}
	empty := waveImage(10, 10, waveSpec{col: col})
	if empty.NRGBAAt(3, 5).A == 0 {
		t.Fatal("missing peaks still draw a centre line")
	}
}

func TestFilmstripTilesStayAnchored(t *testing.T) {
	tile := image.NewNRGBA(image.Rect(0, 0, 9, 4))
	for i := range tile.Pix {
		tile.Pix[i] = 0xFF
	}
	a := filmstripImage(30, 4, tile, 0, color.NRGBA{})
	b := filmstripImage(30, 4, tile, 5, color.NRGBA{})
	// with a 10 px period, pixel 9 of a is the gap, which is pixel 4 of b
	if a.NRGBAAt(9, 0).A != 0 || b.NRGBAAt(4, 0).A != 0 || b.NRGBAAt(5, 0).A == 0 {
		t.Fatal("filmstrip not anchored to the clip start")
	}
}

func TestClipLabel(t *testing.T) {
	if got := clipLabel(62, 25); got != "00:02:12" {
		t.Errorf("got %q", got)
	}
	if got := clipLabel(25*3600, 25); got != "01:00:00:00" {
		t.Errorf("got %q", got)
	}
}
