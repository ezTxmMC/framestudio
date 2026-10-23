package timeline

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"framestudio-proto/src/model"
	"framestudio-proto/src/workgate"
)

func smoothFixture(t *testing.T) *fixture {
	f := setup(t, 1000, 300)
	_ = f.s.Edit("x", func(p *model.Project) error {
		for i := 0; i < 20; i++ {
			if err := p.PlaceClip(p.Tracks[0].ID, model.NewClip(model.KindColor, "C", i*100+1000, 90)); err != nil {
				return err
			}
		}
		return nil
	})
	f.tl.SetZoom(10)
	f.tl.SetFollow(FollowSmooth)
	f.s.Seek(990)
	f.s.SetPlaying(true)
	f.s.Advance(400 * time.Millisecond)
	return f
}

// absolute x of the playhead line on the timeline widget.
func phX(f *fixture) float32 {
	r := f.tl.r
	return headerW + r.lanes.Position().X + r.phLine.Position().X
}

func TestSmoothFollowSlidesInsteadOfRebuilding(t *testing.T) {
	f := smoothFixture(t)
	r := f.tl.r
	if r.margin <= 0 {
		t.Fatal("playing with smooth follow builds with a margin")
	}
	first := r.lanes.Objects[0]
	n := len(r.lanes.Objects)
	builtAt := r.buildOff
	for i := 0; i < 15; i++ {
		f.s.Advance(40 * time.Millisecond)
		g := f.tl.geom()
		if want := g.frameX(float64(f.s.Playhead)); abs(int(phX(f)-want)) > 1 {
			t.Fatalf("step %d: playhead drawn at %v, want %v", i, phX(f), want)
		}
	}
	if r.buildOff != builtAt || r.lanes.Objects[0] != first || len(r.lanes.Objects) != n {
		t.Fatal("small scroll steps must not rebuild the objects")
	}
	if r.lanes.Position().X == -r.margin {
		t.Fatal("lanes did not slide")
	}
	// scrolling past the margin rebuilds once and stays consistent
	for i := 0; i < 400; i++ {
		f.s.Advance(40 * time.Millisecond)
	}
	if r.buildOff == builtAt {
		t.Fatal("the margin is used up: a rebuild is due")
	}
	g := f.tl.geom()
	if want := g.frameX(float64(f.s.Playhead)); abs(int(phX(f)-want)) > 1 {
		t.Fatalf("after rebuild playhead drawn at %v, want %v", phX(f), want)
	}
	f.s.SetPlaying(false)
	f.tl.Refresh()
	if r.margin != 0 || r.lanes.Position() != (fyne.Position{}) {
		t.Fatal("stopped: the view is built exactly again")
	}
}

func TestSmoothFollowFallsBackToPagingWhenUIIsBusy(t *testing.T) {
	f := smoothFixture(t)
	workgate.SetUIBusy(true)
	defer workgate.SetUIBusy(false)
	f.s.Advance(40 * time.Millisecond)
	if f.tl.smoothFollow() {
		t.Fatal("smooth follow must be off while the UI is busy")
	}
	if f.tl.r.margin != 0 {
		t.Fatal("rebuilt without margin")
	}
}

// gatedSource behaves like media.Cache: it takes the work-gate slot itself.
type gatedSource struct {
	g    workgate.Gate
	done chan struct{}
}

func (s gatedSource) job(p workgate.Prio) error {
	rel, err := s.g.Acquire(context.Background(), p)
	if err != nil {
		return err
	}
	defer rel()
	time.Sleep(20 * time.Millisecond)
	s.done <- struct{}{}
	return nil
}

func (s gatedSource) Thumbnail(model.MediaItem, int, int) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), s.job(workgate.PrioVisible)
}

func (s gatedSource) Waveform(model.MediaItem, int) ([]float32, error) {
	return []float32{1}, s.job(workgate.PrioBackground)
}

// The loader must not hold a gate slot around the cache's own slot: with more loads
// than slots the nested acquisition used to deadlock every one of them.
func TestLoadersDoNotNestGateSlots(t *testing.T) {
	g := workgate.New()
	defer g.Close()
	g.SetState(workgate.Idle)
	time.Sleep(workgate.BackgroundDelay + 50*time.Millisecond)
	dir := t.TempDir()
	src := gatedSource{g, make(chan struct{}, 64)}
	ld := newLoader(src, func() {})
	defer ld.close()
	const n = 12
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, fmt.Sprint("x", i))
		os.WriteFile(path, []byte("x"), 0o644)
		m := model.MediaItem{ID: path, Path: path, Kind: model.KindVideo}
		ld.get(m, true, 512)
		ld.get(m, false, 0)
	}
	for i := 0; i < 2*n; i++ {
		select {
		case <-src.done:
		case <-time.After(10 * time.Second):
			t.Fatalf("deadlock after %d of %d loads", i, 2*n)
		}
	}
}
