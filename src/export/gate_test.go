package export

import (
	"context"
	"testing"
	"time"

	"framestudio-proto/src/workgate"
)

func TestExportPausesWhilePlaying(t *testing.T) {
	old := workgate.Default()
	g := workgate.New()
	workgate.SetDefault(g)
	defer workgate.SetDefault(old)

	g.SetState(workgate.Playing)
	done := make(chan struct{})
	go func() {
		waitForPlayback(context.Background())
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("export rendered a frame while the user was playing")
	case <-time.After(80 * time.Millisecond):
	}
	g.SetState(workgate.Idle)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("export did not continue after playback stopped")
	}
}

func TestExportWaitEndsOnCancel(t *testing.T) {
	old := workgate.Default()
	g := workgate.New()
	workgate.SetDefault(g)
	defer workgate.SetDefault(old)
	g.SetState(workgate.Playing)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { waitForPlayback(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a cancelled export stayed blocked")
	}
}
