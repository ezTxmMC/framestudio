package viewer

import (
	"image"
	"math/rand"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"framestudio-proto/src/model"
	"framestudio-proto/src/preview"
	"framestudio-proto/src/render"
)

// The still the viewer shows has exactly the pixel size fyne gives the picture widget,
// so the painter uploads it 1:1 without CPU scaling.
func TestShownStillMatchesWidgetTexture(t *testing.T) {
	s, v, _, q, w := setup(t)
	_ = s.Edit("4K", func(p *model.Project) error {
		p.Settings.Width, p.Settings.Height = 3840, 2160
		return nil
	})
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		w.Resize(fyne.NewSize(float32(500+rnd.Intn(900)), float32(320+rnd.Intn(500))))
		wantW, wantH := preview.TextureSize(v.pic.Size().Width, v.pic.Size().Height, 1)
		q.until(t, "frame of the new size", func() bool {
			img := v.ImageRGBA()
			wantW, wantH = preview.TextureSize(v.pic.Size().Width, v.pic.Size().Height, 1)
			return img != nil && img.Rect.Dx() == wantW && img.Rect.Dy() == wantH
		})
		if rw, rh := v.RenderSize(); rw != wantW || rh != wantH {
			t.Fatalf("window %d: RenderSize %dx%d, texture %dx%d", i, rw, rh, wantW, wantH)
		}
	}
}

// The UI thread work for a frame is a pointer swap: no pixel is touched there.
func TestShowReadyDoesNotTouchPixels(t *testing.T) {
	_, v, _, q, _ := setup(t)
	q.until(t, "first frame", func() bool { return v.ImageRGBA() != nil })
	f := v.sched.Take()
	if f != nil {
		f.Release()
	}
	// measure the closure itself with the next frame ready
	v.s.Seek(3)
	deadline := time.Now().Add(2 * time.Second)
	ready := func() bool { f := v.sched.Ready().Load(); return f != nil && f.Pos == 3 }
	for !ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !ready() {
		t.Fatal("no frame became ready")
	}
	q.mu.Lock()
	q.fns = nil
	q.mu.Unlock()
	t0 := time.Now()
	v.showReady()
	if d := time.Since(t0); d > 2*time.Millisecond {
		t.Fatalf("showReady took %v on the UI thread", d)
	}
	if v.ShownPos() != 3 {
		t.Fatalf("shown %d", v.ShownPos())
	}
}

// The image handed to fyne is an *image.RGBA with the cheap GPU scale mode, never the
// NRGBA + CatmullRom combination that cost 3-43 ms per frame.
func TestFynePathIsCheap(t *testing.T) {
	_, v, _, q, _ := setup(t)
	q.until(t, "first frame", func() bool { return v.ImageRGBA() != nil })
	if _, ok := v.img.Image.(*image.RGBA); !ok {
		t.Fatalf("canvas image is %T, want *image.RGBA", v.img.Image)
	}
	if v.img.ScaleMode != canvas.ImageScaleFastest {
		t.Fatalf("scale mode %v", v.img.ScaleMode)
	}
}

func TestStatsOverlayAndChip(t *testing.T) {
	_, v, _, q, _ := setup(t)
	q.until(t, "frame", func() bool { return v.ImageRGBA() != nil })
	if v.StatsShown() {
		t.Fatal("overlay must start hidden")
	}
	sn := preview.StatsSnapshot{ShownFPS: 29.8, ProjectFPS: 30, DroppedTotal: 12, DroppedPerSec: 0.4,
		Quality: preview.QHalf, RenderW: 640, RenderH: 360, RenderP50: 8.2, CachePct: 87, Decoder: "hw", UILatencyMS: 4}
	v.SetStats(true)
	now := time.Now()
	v.ShowSnapshot(sn, now)
	want := [3]string{
		"Bild 29.8/30 fps  verworfen 12 (0.4/s)",
		"Qualität: Halb (auto)  640x360  Render 8.2 ms",
		"Cache 87%  Decoder: hw  UI 4.0 ms",
	}
	for i, l := range v.overlay.lines {
		if l.Text != want[i] {
			t.Errorf("line %d = %q, want %q", i, l.Text, want[i])
		}
	}
	if v.ChipShown() {
		t.Fatal("chip without under-run")
	}
	sn.UnderRun = true
	v.ShowSnapshot(sn, now)
	if !v.ChipShown() || v.overlay.chipTx.Text != ChipOverloaded {
		t.Fatal("under-run must show the chip")
	}
	sn.UnderRun = false
	v.ShowSnapshot(sn, now.Add(2*time.Second))
	if !v.ChipShown() {
		t.Fatal("chip must stay for 3 s after recovery")
	}
	v.ShowSnapshot(sn, now.Add(4*time.Second))
	if v.ChipShown() {
		t.Fatal("chip must hide after 3 s of recovery")
	}
	sn.KFOnly = true
	v.ShowSnapshot(sn, now)
	if !v.overlay.kf.Visible() {
		t.Fatal("keyframe-only chip")
	}
	v.SetStats(false)
	if v.StatsShown() {
		t.Fatal("overlay must hide")
	}
}

func TestViewerCloseLeavesNoGoroutinesOrFrames(t *testing.T) {
	_, v, _, q, _ := setup(t)
	q.until(t, "frame", func() bool { return v.ImageRGBA() != nil })
	v.Close()
	if v.cur != nil || v.sched.Ready().Load() != nil {
		t.Fatal("frames not released")
	}
	select {
	case <-v.sched.Done():
	default:
		t.Fatal("scheduler not stopped")
	}
}

var _ = render.Options{}

// The warning chips must not outlive playback: the monitor hides them when it ends.
func TestChipsClearedWhenPlaybackStops(t *testing.T) {
	_, v, _, q, _ := setup(t)
	q.until(t, "frame", func() bool { return v.ImageRGBA() != nil })
	v.playingA.Store(true)
	v.startMonitor()
	v.ShowSnapshot(preview.StatsSnapshot{UnderRun: true, KFOnly: true}, time.Now())
	if !v.ChipShown() || !v.overlay.kf.Visible() {
		t.Fatal("chips not shown during playback")
	}
	v.playingA.Store(false)
	// chipOn is cleared last, after both chips were hidden
	q.until(t, "chips hidden after stop", func() bool { return !v.chipOn.Load() })
	if v.ChipShown() || v.overlay.kf.Visible() {
		t.Fatal("chip still visible after playback stopped")
	}
}
