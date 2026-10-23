package preview

import (
	"container/list"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// CacheKey identifies a rendered picture. ProjHash comes from Hasher.FrameHash, so a
// project edit changes the key of exactly the frames it touches and everything else
// stays warm without explicit invalidation.
type CacheKey struct {
	ProjHash uint64
	Frame    int
	W, H     uint16
	Q        Quality
}

// FrameRange is a half-open run of cached frames.
type FrameRange struct{ From, To int }

type cacheEntry struct {
	key   CacheKey
	f     *Frame
	elem  *list.Element
	bytes int64
}

// FrameCache is a byte-budgeted LRU of rendered frames. Entries hold a reference of
// their frame; Get hands out a new reference the caller must Release. Safe for
// concurrent use.
type FrameCache struct {
	mu      sync.Mutex
	budget  int64
	used    int64
	items   map[CacheKey]*cacheEntry
	lru     *list.List // front = most recently used
	pinLo   int
	pinHi   int
	hits    uint64
	misses  uint64
	evicted uint64
}

// NewFrameCache makes a cache with the given budget; 0 or less disables it.
func NewFrameCache(budgetBytes int64) *FrameCache {
	return &FrameCache{budget: budgetBytes, items: map[CacheKey]*cacheEntry{}, lru: list.New(), pinHi: -1}
}

// DefaultCacheBytes is clamp(10% of RAM, 256, 2048) MB.
func DefaultCacheBytes() int64 {
	ram := physicalRAM()
	return min(max(ram/10, 256<<20), 2048<<20)
}

func physicalRAM() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 8 << 30
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 8 << 30
}

// Get returns the frame for k with an extra reference.
func (c *FrameCache) Get(k CacheKey) (*Frame, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[k]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.lru.MoveToFront(e.elem)
	return e.f.Retain(), true
}

// Has reports whether k is cached without touching LRU order or counters.
func (c *FrameCache) Has(k CacheKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[k]
	return ok
}

// GetNearest looks for a cached frame within maxDist frames of frame (closest
// first, earlier before later), using hashAt to compute each candidate's hash.
// It returns the frame with a reference and its distance.
func (c *FrameCache) GetNearest(hashAt func(frame int) uint64, frame, maxDist int, w, h int, q Quality) (*Frame, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for d := 1; d <= maxDist; d++ {
		for _, fr := range [2]int{frame - d, frame + d} {
			k := CacheKey{hashAt(fr), fr, uint16(w), uint16(h), q}
			if e, ok := c.items[k]; ok {
				c.lru.MoveToFront(e.elem)
				return e.f.Retain(), d, true
			}
		}
	}
	return nil, 0, false
}

// Put stores f under k, taking its own reference. Frames inside the pinned window
// (see Pin) are never evicted to make room for frames outside of it.
func (c *FrameCache) Put(k CacheKey, f *Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budget <= 0 || f.Bytes() > c.budget {
		return
	}
	if old, ok := c.items[k]; ok {
		c.lru.MoveToFront(old.elem)
		return
	}
	e := &cacheEntry{key: k, f: f.Retain(), bytes: f.Bytes()}
	e.elem = c.lru.PushFront(e)
	c.items[k] = e
	c.used += e.bytes
	c.shrinkLocked()
}

// Pin protects frames in [lo, hi] from eviction by outsiders (the second around the
// playhead); pass hi < lo to clear.
func (c *FrameCache) Pin(lo, hi int) {
	c.mu.Lock()
	c.pinLo, c.pinHi = lo, hi
	c.mu.Unlock()
}

func (c *FrameCache) pinned(k CacheKey) bool { return k.Frame >= c.pinLo && k.Frame <= c.pinHi }

func (c *FrameCache) shrinkLocked() {
	for el := c.lru.Back(); c.used > c.budget && el != nil; {
		prev := el.Prev()
		e := el.Value.(*cacheEntry)
		if !c.pinned(e.key) {
			c.removeLocked(e)
			c.evicted++
		}
		el = prev
	}
	// everything left is pinned: drop the oldest regardless to honour the budget
	for c.used > c.budget && c.lru.Len() > 0 {
		c.removeLocked(c.lru.Back().Value.(*cacheEntry))
		c.evicted++
	}
}

func (c *FrameCache) removeLocked(e *cacheEntry) {
	c.lru.Remove(e.elem)
	delete(c.items, e.key)
	c.used -= e.bytes
	e.f.Release()
}

// Invalidate eagerly frees the frames in [from, to] (a project edit's range). Entries
// outside stay; stale ones inside would never be hit anyway, this only returns memory.
func (c *FrameCache) Invalidate(from, to int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.items {
		if e.key.Frame >= from && e.key.Frame <= to {
			c.removeLocked(e)
		}
	}
}

// Clear drops everything.
func (c *FrameCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.items {
		c.removeLocked(e)
	}
}

// SetBudget changes the byte budget, evicting as needed (0 disables and clears).
func (c *FrameCache) SetBudget(b int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budget = b
	c.shrinkLocked()
}

// Ranges lists runs of cached frames for which valid(frame, hash) holds (nil = all);
// the timeline draws them as the cache bar.
func (c *FrameCache) Ranges(valid func(frame int, hash uint64) bool) []FrameRange {
	c.mu.Lock()
	frames := make([]int, 0, len(c.items))
	for k := range c.items {
		if valid == nil || valid(k.Frame, k.ProjHash) {
			frames = append(frames, k.Frame)
		}
	}
	c.mu.Unlock()
	slices.Sort(frames)
	frames = slices.Compact(frames)
	var out []FrameRange
	for _, f := range frames {
		if n := len(out); n > 0 && out[n-1].To == f {
			out[n-1].To = f + 1
			continue
		}
		out = append(out, FrameRange{f, f + 1})
	}
	return out
}

// CacheStats is a reading of the cache counters.
type CacheStats struct {
	Entries       int
	Bytes, Budget int64
	Hits, Misses  uint64
	Evicted       uint64
}

// Stats returns the counters.
func (c *FrameCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{len(c.items), c.used, c.budget, c.hits, c.misses, c.evicted}
}
