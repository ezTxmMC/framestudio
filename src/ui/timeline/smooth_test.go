package timeline

import (
	"context"
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

func TestLoadersWaitWhilePlaying(t *testing.T) {
	g := workgate.New(workgate.Config{IdleDebounce: time.Millisecond})
	g.SetState(workgate.Playing)
	ld := newLoader(nil, nil)
	ld.gate = g
	done := make(chan struct{})
	go func() {
		rel, err := ld.gt().Acquire(ld.ctx, workgate.PrioVisible)
		if err == nil {
			rel()
			close(done)
		}
	}()
	select {
	case <-done:
		t.Fatal("thumbnail loading must wait while playing")
	case <-time.After(40 * time.Millisecond):
	}
	g.SetState(workgate.Idle)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("never resumed")
	}
	g.SetState(workgate.Playing)
	ld.close()
	if _, err := ld.gt().Acquire(ld.ctx, workgate.PrioBackground); err != context.Canceled {
		t.Fatalf("closed loader: %v", err)
	}
}
