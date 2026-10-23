package media

import "sync"

// Config holds the user-adjustable cache limits (settings.Media). Zero or negative
// fields keep the built-in defaults.
type Config struct {
	FrameCacheMB int // decoded video frames for playback and scrubbing
	StillCacheMB int // decoded still images
	MaxDecoders  int // concurrent playback decoders (ffmpeg processes)
}

// Upper bounds that keep a typo in the settings from exhausting memory or processes.
const (
	maxCacheMB  = 64 << 10 // 64 GiB
	maxDecoders = 32
)

var (
	cfgMu  sync.Mutex
	cfg    Config
	caches = map[*Cache]struct{}{}
)

// Configure sets the limits for every open cache, live, and for caches created later.
// Shrinking a budget evicts the oldest entries at once; fewer decoders stop the least
// recently used ones the next time a decoder starts. Safe from any goroutine.
func Configure(c Config) {
	cfgMu.Lock()
	cfg = c
	open := make([]*Cache, 0, len(caches))
	for k := range caches {
		open = append(open, k)
	}
	cfgMu.Unlock()
	for _, k := range open {
		k.apply(c)
	}
}

func register(c *Cache) {
	cfgMu.Lock()
	caches[c] = struct{}{}
	now := cfg
	cfgMu.Unlock()
	c.apply(now)
}

func unregister(c *Cache) {
	cfgMu.Lock()
	delete(caches, c)
	cfgMu.Unlock()
}

func (c *Cache) apply(cf Config) {
	c.SetFrameCacheMB(cf.FrameCacheMB)
	c.SetStillCacheMB(cf.StillCacheMB)
	c.SetMaxDecoders(cf.MaxDecoders)
}

func clampMB(mb int, def int64) int64 {
	if mb <= 0 {
		return def
	}
	return int64(min(mb, maxCacheMB)) << 20
}

// SetFrameCacheMB sets the budget of decoded video frames (<= 0 = default 512 MB).
func (c *Cache) SetFrameCacheMB(mb int) { c.frames.setBudget(clampMB(mb, frameBudget)) }

// SetStillCacheMB sets the budget of decoded still images (<= 0 = default 256 MB).
func (c *Cache) SetStillCacheMB(mb int) { c.stills.setBudget(clampMB(mb, stillBudget)) }

// FrameCacheBytes and StillCacheBytes report the current budgets.
func (c *Cache) FrameCacheBytes() int64 { return c.frames.getBudget() }
func (c *Cache) StillCacheBytes() int64 { return c.stills.getBudget() }

// SetMaxDecoders limits the concurrent playback decoders (<= 0 = default 4). When more
// are running, the least recently used ones stop right away.
func (c *Cache) SetMaxDecoders(n int) {
	if n <= 0 {
		n = maxPrefetchers
	}
	n = min(n, maxDecoders)
	c.decoders.Store(int32(n))
	c.pmu.Lock()
	defer c.pmu.Unlock()
	for len(c.prefetchers) > n {
		all := make([]int, len(c.prefetchers))
		for i := range all {
			all[i] = i
		}
		c.evict(all)
	}
}

// MaxDecoders reports the playback decoder limit.
func (c *Cache) MaxDecoders() int { return c.maxDecoders() }

func (c *Cache) maxDecoders() int { return int(c.decoders.Load()) }
