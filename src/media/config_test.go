package media

import (
	"testing"
)

func TestLRUSetBudgetEvicts(t *testing.T) {
	l := newLRU(100)
	for i, k := range []string{"a", "b", "c"} {
		l.put(k, "m", i, 30)
	}
	l.setBudget(50)
	if l.len() != 1 {
		t.Fatalf("shrinking to 50 bytes should keep one 30-byte entry, have %d", l.len())
	}
	if _, ok := l.get("c"); !ok {
		t.Fatal("the newest entry should survive")
	}
	l.put("big", "m", 0, 80)
	if _, ok := l.get("big"); ok {
		t.Fatal("entries larger than the budget are not stored")
	}
	l.setBudget(200)
	l.put("big", "m", 0, 80)
	if _, ok := l.get("big"); !ok {
		t.Fatal("after growing the budget the entry fits")
	}
}

func TestConfigureAppliesToOpenAndNewCaches(t *testing.T) {
	t.Cleanup(func() { Configure(Config{}) })
	c := NewCache()
	defer c.Close()
	if c.FrameCacheBytes() != frameBudget || c.StillCacheBytes() != stillBudget || c.MaxDecoders() != maxPrefetchers {
		t.Fatal("defaults expected before Configure")
	}
	Configure(Config{FrameCacheMB: 1024, StillCacheMB: 64, MaxDecoders: 2})
	if c.FrameCacheBytes() != 1024<<20 || c.StillCacheBytes() != 64<<20 || c.MaxDecoders() != 2 {
		t.Fatalf("open cache not updated: %d %d %d", c.FrameCacheBytes(), c.StillCacheBytes(), c.MaxDecoders())
	}
	d := NewCache()
	if d.FrameCacheBytes() != 1024<<20 || d.MaxDecoders() != 2 {
		t.Fatal("new cache should start with the configured limits")
	}
	d.Close()
	cfgMu.Lock()
	_, still := caches[d]
	cfgMu.Unlock()
	if still {
		t.Fatal("closed caches must be forgotten")
	}
	Configure(Config{MaxDecoders: 1000, FrameCacheMB: -5})
	if c.MaxDecoders() != maxDecoders || c.FrameCacheBytes() != frameBudget {
		t.Fatal("limits should be clamped and invalid budgets fall back to the default")
	}
}

func TestSetMaxDecodersStopsExtraDecoders(t *testing.T) {
	m := mustProbe(t, testVideo(t))
	c := NewCache()
	defer c.Close()
	for i := 0; i < 4; i++ {
		if _, err := c.VideoFrame(m, i*11%50, 25, 40+i*8, 40+i*8); err != nil {
			t.Fatal(err)
		}
	}
	c.SetMaxDecoders(1)
	c.pmu.Lock()
	n := len(c.prefetchers)
	c.pmu.Unlock()
	if n > 1 {
		t.Fatalf("%d decoders still running after limiting to 1", n)
	}
	if _, err := c.VideoFrame(m, 3, 25, 96, 96); err != nil {
		t.Fatal(err)
	}
	c.pmu.Lock()
	n = len(c.prefetchers)
	c.pmu.Unlock()
	if n > 1 {
		t.Fatalf("%d decoders after a new request with a limit of 1", n)
	}
}
