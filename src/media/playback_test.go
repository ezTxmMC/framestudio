package media

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"framestudio-proto/src/model"
)

func prefetcherCount(c *Cache) int {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	return len(c.prefetchers)
}

func TestPosterDistinctFramesAndCached(t *testing.T) {
	m := rateVideo(t, 25, 4)
	c := NewCache()
	defer c.Close()
	a, err := c.Poster(m, 0, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if b := a.Bounds(); b.Dx() != 64 || b.Dy() != 36 {
		t.Fatalf("poster size %v", b)
	}
	b, err := c.Poster(m, 2, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if sameImage(a, b) {
		t.Fatal("posters at 0 s and 2 s should differ")
	}
	// The frame at 2 s matches the decoded video frame 50.
	seq, err := OpenVideo(m, 0, 25, 64, 36)
	if err != nil {
		t.Fatal(err)
	}
	frames := readAll(t, seq)
	if !sameImage(b, frames[50]) {
		t.Error("poster at 2 s differs from frame 50")
	}
	// Times inside one 0.1 s slot share the cached image.
	b2, _ := c.Poster(m, 2.03, 64, 64)
	if b2 != b {
		t.Error("poster at 2.03 s should come from the cache")
	}
	// Past the end the last poster slot is used.
	if _, err := c.Poster(m, 99, 64, 64); err != nil {
		t.Fatalf("poster past the end: %v", err)
	}
	if n := prefetcherCount(c); n != 0 {
		t.Fatalf("posters must not start playback decoders, %d running", n)
	}
	if c.posters.len() != 3 {
		t.Fatalf("%d posters cached", c.posters.len())
	}
	// Stills return the scaled image.
	still := mustProbe(t, testPNG(t, 64, 48))
	img, err := c.Poster(still, 3, 32, 32)
	if err != nil || img.Bounds().Dx() != 32 || img.Bounds().Dy() != 24 {
		t.Fatalf("still poster %v %v", img, err)
	}
	c.Invalidate(m.ID)
	if c.posters.len() != 0 {
		t.Fatal("Invalidate should drop posters")
	}
}

func TestPostersAreLimited(t *testing.T) {
	c := NewCache()
	defer c.Close()
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.inSlot(c.posterSlots, "m", time.Minute, func(context.Context) (any, error) {
				n := running.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(15 * time.Millisecond)
				running.Add(-1)
				return nil, nil
			})
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > maxPosters {
		t.Fatalf("%d poster grabs at once, limit %d", p, maxPosters)
	}
}

func TestBackwardChunkDecode(t *testing.T) {
	m := rateVideo(t, 25, 6)
	r, err := OpenVideo(m, 0, 25, 64, 36)
	if err != nil {
		t.Fatal(err)
	}
	seq := readAll(t, r)
	c := NewCache()
	defer c.Close()

	// Playing backwards from frame 120: the first request decodes the chunk before it.
	img, err := c.PlaybackFrame(m, 120, 25, 64, 36, -1)
	if err != nil || !sameImage(img, seq[120]) {
		t.Fatalf("frame 120: %v", err)
	}
	if n := prefetcherCount(c); n != 0 {
		t.Fatalf("reverse playback started %d forward decoders", n)
	}
	hits, misses := c.hits.Load(), c.misses.Load()
	for n := 119; n > 120-2*25; n-- {
		img, err := c.PlaybackFrame(m, n, 25, 64, 36, -1)
		if err != nil {
			t.Fatal(err)
		}
		if !sameImage(img, seq[n]) {
			t.Fatalf("backward frame %d differs from sequential decoding", n)
		}
	}
	if c.misses.Load() != misses || c.hits.Load()-hits != 49 {
		t.Fatalf("backward requests inside the chunk should all hit: %d misses", c.misses.Load()-misses)
	}
	// The next chunk starts below the first one.
	if img, err := c.PlaybackFrame(m, 70, 25, 64, 36, -1); err != nil || !sameImage(img, seq[70]) {
		t.Fatalf("frame 70: %v", err)
	}
	if _, ok := c.frames.get(stream{id: m.ID, path: m.Path, fps: 25, w: 64, h: 36}.frameKey(21)); !ok {
		t.Fatal("second chunk should reach back two seconds")
	}

	// VideoFrame detects small backward steps by itself (e.g. skimming or a viewer
	// that does not pass the rate).
	d := NewCache()
	defer d.Close()
	for _, n := range []int{140, 139} {
		if img, err := d.VideoFrame(m, n, 25, 64, 36); err != nil || !sameImage(img, seq[n]) {
			t.Fatalf("frame %d: %v", n, err)
		}
	}
	misses = d.misses.Load()
	for n := 138; n > 100; n -= 3 {
		if img, err := d.VideoFrame(m, n, 25, 64, 36); err != nil || !sameImage(img, seq[n]) {
			t.Fatalf("frame %d: %v", n, err)
		}
	}
	if d.misses.Load() != misses {
		t.Fatalf("backward walk should be served from the chunk, %d misses", d.misses.Load()-misses)
	}
}

func TestBackwardRequestsShareChunk(t *testing.T) {
	m := rateVideo(t, 25, 4)
	c := NewCache()
	defer c.Close()
	var wg sync.WaitGroup
	for _, n := range []int{90, 89, 88, 87} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.PlaybackFrame(m, n, 25, 64, 36, -2); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.frames.len() > 2*2*25+4 {
		t.Fatalf("concurrent backward requests decoded %d frames", c.frames.len())
	}
}

func TestFastForwardServesNearFrame(t *testing.T) {
	m := rateVideo(t, 25, 2)
	c := NewCache()
	defer c.Close()
	s := stream{id: m.ID, path: m.Path, fps: 25, w: 64, h: 36}
	if _, err := c.VideoFrame(m, 10, 25, 64, 36); err != nil {
		t.Fatal(err)
	}
	ten, _ := c.frames.get(s.frameKey(10))
	c = NewCache() // only frame 10 cached
	defer c.Close()
	c.frames.put(s.frameKey(10), m.ID, ten, 64*36*4)
	img, err := c.PlaybackFrame(m, 11, 25, 64, 36, 8)
	if err != nil || img != ten {
		t.Fatal("fast forward should show the cached neighbour")
	}
	img, err = c.PlaybackFrame(m, 9, 25, 64, 36, 1)
	if err != nil || img == ten {
		t.Fatal("normal rate must return the exact frame")
	}
}

func TestOfflineMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := model.MediaItem{ID: "a", Path: path}
	gone := model.MediaItem{ID: "b", Path: filepath.Join(dir, "fehlt.png")}
	c := NewCache()
	defer c.Close()
	var calls atomic.Int32
	unsub := c.OnOfflineChange(func() { calls.Add(1) })
	if !c.CheckOffline([]model.MediaItem{m, gone}) || calls.Load() != 1 {
		t.Fatal("a missing file should change the offline map")
	}
	if c.Offline(m) || !c.Offline(gone) {
		t.Fatal("offline flags wrong")
	}
	if c.CheckOffline([]model.MediaItem{m, gone}) {
		t.Fatal("unchanged files should not report a change")
	}
	_ = os.Remove(path)
	_ = os.WriteFile(gone.Path, []byte("x"), 0o644)
	if !c.CheckOffline([]model.MediaItem{m, gone}) || !c.Offline(m) || c.Offline(gone) {
		t.Fatal("changes on disk should be picked up")
	}
	unsub()
	_ = os.Remove(gone.Path)
	c.CheckOffline([]model.MediaItem{gone})
	if calls.Load() != 2 {
		t.Fatalf("unsubscribed callback ran (%d calls)", calls.Load())
	}
	// Decoders mark files they cannot find.
	lost := model.MediaItem{ID: "c", Name: "weg", Kind: model.KindVideo, Path: filepath.Join(dir, "weg.mp4"), Width: 4, Height: 4}
	if _, err := c.VideoFrame(lost, 0, 25, 4, 4); err == nil || !c.Offline(lost) {
		t.Fatal("a failed decode of a missing file should mark it offline")
	}
}

func TestInvalidateDiscardsRunningWork(t *testing.T) {
	path := testPNG(t, 40, 30)
	m := mustProbe(t, path)
	c := NewCache()
	defer c.Close()
	var got []string
	unsub := c.OnInvalidate(func(id string) { got = append(got, id) })
	defer unsub()
	g := c.gen(m.ID)
	c.Invalidate(m.ID)
	c.putIf(c.thumbs, g, "stale", m.ID, 1, 1)
	if _, ok := c.thumbs.get("stale"); ok {
		t.Fatal("results of work started before Invalidate must not be cached")
	}
	if len(got) != 1 || got[0] != m.ID {
		t.Fatalf("invalidate subscribers got %v", got)
	}
	// Same path, new content: the next request decodes again.
	a, _ := c.Image(m, 40, 30)
	_ = os.WriteFile(path, mustReadFile(t, testPNG(t, 20, 10)), 0o644)
	c.Invalidate(m.ID)
	b, err := c.Image(m, 40, 30)
	if err != nil || a.Bounds() == b.Bounds() {
		t.Fatalf("image after relink still stale: %v %v", b.Bounds(), err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
