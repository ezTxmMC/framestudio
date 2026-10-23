package media

import (
	"container/list"
	"sync"
)

// lru is a mutex-guarded least-recently-used cache with a byte budget.
type lru struct {
	mu     sync.Mutex
	budget int64
	used   int64
	ll     *list.List
	items  map[string]*list.Element
}

type lruEntry struct {
	key, owner string // owner is the media ID, for Invalidate
	val        any
	size       int64
	pf         *pframe    // pooled buffer behind val, nil when the GC owns it
	src        SourceKind // how the frame was produced
}

func newLRU(budget int64) *lru {
	return &lru{budget: budget, ll: list.New(), items: map[string]*list.Element{}}
}

// get returns the value. The caller keeps it for as long as it likes, so a pooled buffer
// behind it is never recycled afterwards (see getRef for the managed variant).
func (c *lru) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		en := e.Value.(*lruEntry)
		if en.pf != nil {
			en.pf.escaped.Store(true)
		}
		return en.val, true
	}
	return nil, false
}

// getRef returns the value with an extra reference on its pooled buffer, which the
// caller drops with the returned release func when it is done reading.
func (c *lru) getRef(key string) (val any, src SourceKind, release func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.items[key]
	if !found {
		return nil, 0, nil, false
	}
	c.ll.MoveToFront(e)
	en := e.Value.(*lruEntry)
	if en.pf == nil {
		return en.val, en.src, func() {}, true
	}
	en.pf.refs.Add(1)
	pf := en.pf
	var once sync.Once
	return en.val, en.src, func() { once.Do(pf.release) }, true
}

// peekSrc is peek that also reports how the frame was produced.
func (c *lru) peekSrc(key string) (any, SourceKind, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		en := e.Value.(*lruEntry)
		return en.val, en.src, true
	}
	return nil, 0, false
}

// peek is get without touching the recency order.
func (c *lru) peek(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		return e.Value.(*lruEntry).val, true
	}
	return nil, false
}

// put stores val, evicting the oldest entries to stay within budget. Values larger
// than the whole budget are not stored.
func (c *lru) put(key, owner string, val any, size int64) {
	c.putPF(key, owner, val, size, nil, SrcSW)
}

// putPF is put for a frame in a pooled buffer: the cache takes its own reference and
// drops it on eviction. It reports whether the value was stored.
func (c *lru) putPF(key, owner string, val any, size int64, pf *pframe, src SourceKind) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if size > c.budget {
		return false
	}
	if e, ok := c.items[key]; ok {
		c.remove(e)
	}
	if pf != nil {
		pf.refs.Add(1)
	}
	c.items[key] = c.ll.PushFront(&lruEntry{key: key, owner: owner, val: val, size: size, pf: pf, src: src})
	c.used += size
	for c.used > c.budget {
		c.remove(c.ll.Back())
	}
	return true
}

// setBudget changes the byte budget, evicting the oldest entries when it shrinks.
func (c *lru) setBudget(budget int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budget = budget
	for c.used > c.budget && c.ll.Len() > 0 {
		c.remove(c.ll.Back())
	}
}

func (c *lru) getBudget() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.budget
}

func (c *lru) remove(e *list.Element) {
	en := e.Value.(*lruEntry)
	c.ll.Remove(e)
	delete(c.items, en.key)
	c.used -= en.size
	if en.pf != nil {
		en.pf.release()
	}
}

// dropOwner removes every entry belonging to one media ID.
func (c *lru) dropOwner(owner string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for e := c.ll.Front(); e != nil; {
		next := e.Next()
		if e.Value.(*lruEntry).owner == owner {
			c.remove(e)
		}
		e = next
	}
}

func (c *lru) usedBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func (c *lru) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// flight collapses concurrent calls for the same key into one (like x/sync/singleflight).
type flight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

func (f *flight) do(key string, fn func() (any, error)) (any, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]*flightCall{}
	}
	if c, ok := f.calls[key]; ok {
		f.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &flightCall{}
	c.wg.Add(1)
	f.calls[key] = c
	f.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()
	f.mu.Lock()
	delete(f.calls, key)
	f.mu.Unlock()
	return c.val, c.err
}
