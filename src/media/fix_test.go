package media

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Fast shuttle must never block the viewer on sequential decoding: after the first
// picture every request returns at once while one keyframe grab at a time follows the
// playhead in the background.
func TestShuttleNeverBlocks(t *testing.T) {
	m := rateVideo(t, 25, 8)
	c := NewCache()
	defer c.Close()
	for _, rate := range []float64{8, -8} {
		start := 0
		if rate < 0 {
			start = 190
		}
		if img, err := c.PlaybackFrame(m, start, 25, 64, 36, rate); err != nil || img == nil {
			t.Fatalf("first shuttle frame: %v", err)
		}
		slowest := time.Duration(0)
		n := start
		for range 20 {
			n += int(rate)
			t0 := time.Now()
			img, err := c.PlaybackFrame(m, n, 25, 64, 36, rate)
			slowest = max(slowest, time.Since(t0))
			if err != nil || img == nil {
				t.Fatalf("rate %v frame %d: %v", rate, n, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if slowest > 50*time.Millisecond {
			t.Fatalf("rate %v: a request blocked %v", rate, slowest)
		}
		if p := prefetcherCount(c); p != 0 {
			t.Fatalf("shuttle started %d sequential decoders", p)
		}
		s := stream{id: m.ID, path: m.Path, fps: 25, w: 64, h: 36}
		deadline := time.Now().Add(5 * time.Second)
		for {
			c.rev.mu.Lock()
			at := c.rev.fast[s].n
			c.rev.mu.Unlock()
			if at != start {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("rate %v: the picture never followed the playhead", rate)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// Reverse play decodes the next older chunk in the background once the playhead is in
// the lower half of the current one, so it does not stall at the boundary.
func TestReverseReadsAhead(t *testing.T) {
	m := rateVideo(t, 25, 6)
	c := NewCache()
	defer c.Close()
	s := stream{id: m.ID, path: m.Path, fps: 25, w: 64, h: 36}
	if _, err := c.PlaybackFrame(m, 120, 25, 64, 36, -2); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.frames.peek(s.frameKey(60)); ok {
		t.Fatal("only one chunk expected so far")
	}
	if _, err := c.PlaybackFrame(m, 92, 25, 64, 36, -2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, first := c.frames.peek(s.frameKey(21))
		if _, last := c.frames.peek(s.frameKey(70)); first && last { // decoded in ascending order
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the older chunk was not decoded ahead")
		}
		time.Sleep(5 * time.Millisecond)
	}
	misses := c.misses.Load()
	for n := 70; n > 25; n -= 2 {
		before := c.misses.Load()
		if _, err := c.PlaybackFrame(m, n, 25, 64, 36, -2); err != nil {
			t.Fatal(err)
		}
		if c.misses.Load() != before {
			t.Logf("miss at %d", n)
		}
	}
	if c.misses.Load() != misses {
		t.Fatalf("%d reverse requests missed after the read-ahead", c.misses.Load()-misses)
	}
}

// Cancel(mediaID), called when a project closes, also stops its playback decoders.
func TestCancelStopsPrefetchers(t *testing.T) {
	m := rateVideo(t, 25, 4)
	c := NewCache()
	defer c.Close()
	if _, err := c.VideoFrame(m, 5, 25, 64, 36); err != nil {
		t.Fatal(err)
	}
	c.pmu.Lock()
	ps := append([]*prefetcher(nil), c.prefetchers...)
	c.pmu.Unlock()
	if len(ps) == 0 {
		t.Fatal("no prefetcher running")
	}
	c.Cancel(m.ID)
	if n := prefetcherCount(c); n != 0 {
		t.Fatalf("%d prefetchers left after Cancel", n)
	}
	deadline := time.Now().Add(2 * time.Second)
	for _, p := range ps {
		for !p.isDone() {
			if time.Now().After(deadline) {
				t.Fatal("prefetcher still decoding after Cancel")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// Waiting for a slot does not count against a job's timeout.
func TestSlotWaitDoesNotTimeOut(t *testing.T) {
	c := NewCache()
	defer c.Close()
	var wg sync.WaitGroup
	for range maxPosters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.inSlot(c.posterSlots, "busy", time.Minute, func(context.Context) (any, error) {
				time.Sleep(300 * time.Millisecond)
				return nil, nil
			})
		}()
	}
	time.Sleep(50 * time.Millisecond)
	_, err := c.inSlot(c.posterSlots, "queued", 100*time.Millisecond, func(ctx context.Context) (any, error) {
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("queued job timed out while waiting for a slot: %v", err)
	}
	wg.Wait()
}
