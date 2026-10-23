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
}

func newLRU(budget int64) *lru {
	return &lru{budget: budget, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *lru) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*lruEntry).val, true
	}
	return nil, false
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if size > c.budget {
		return
	}
	if e, ok := c.items[key]; ok {
		c.remove(e)
	}
	c.items[key] = c.ll.PushFront(&lruEntry{key: key, owner: owner, val: val, size: size})
	c.used += size
	for c.used > c.budget {
		c.remove(c.ll.Back())
	}
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
