package media

import (
	"os"
	"sync"

	"framestudio-proto/src/model"
)

// Missing files: pages must not stat files on the UI thread, so the cache keeps a map of
// offline paths. CheckOffline (run off the UI thread) refreshes it; Offline reads it.
// Decoders that find a file missing mark it offline as well.

type offline struct {
	mu      sync.Mutex
	missing map[string]bool // path -> not found at the last check
	next    int
	onOff   map[int]func()
	onInv   map[int]func(string)
}

// Offline reports whether the file of m was missing at the last check. Files that were
// never checked count as online. Cheap and safe on the UI thread.
func (c *Cache) Offline(m model.MediaItem) bool {
	c.off.mu.Lock()
	defer c.off.mu.Unlock()
	return c.off.missing[m.Path]
}

// CheckOffline looks up the files of items and updates the offline map. It blocks on the
// file system, so call it from a background goroutine. It reports whether any state
// changed; when it did, OnOfflineChange subscribers are called on this goroutine.
func (c *Cache) CheckOffline(items []model.MediaItem) (changed bool) {
	found := map[string]bool{}
	for _, m := range items {
		if m.Path == "" {
			continue
		}
		if _, ok := found[m.Path]; ok {
			continue
		}
		_, err := os.Stat(m.Path)
		found[m.Path] = err == nil
	}
	c.off.mu.Lock()
	if c.off.missing == nil {
		c.off.missing = map[string]bool{}
	}
	for path, ok := range found {
		if c.off.missing[path] == ok { // missing flips (true->online or false->offline)
			changed = true
		}
		if ok {
			delete(c.off.missing, path)
		} else {
			c.off.missing[path] = true
		}
	}
	c.off.mu.Unlock()
	if changed {
		c.off.notifyOffline()
	}
	return changed
}

// noteMissing marks a path offline after a decoder failed to find it.
func (c *Cache) noteMissing(path string) {
	c.off.mu.Lock()
	if c.off.missing == nil {
		c.off.missing = map[string]bool{}
	}
	changed := !c.off.missing[path]
	c.off.missing[path] = true
	c.off.mu.Unlock()
	if changed {
		c.off.notifyOffline()
	}
}

// OnOfflineChange registers fn to be called whenever a file goes offline or comes back.
// fn runs on a background goroutine; hop to the UI thread (kit.Do) before touching
// widgets. The returned func unregisters it.
func (c *Cache) OnOfflineChange(fn func()) (cancel func()) {
	c.off.mu.Lock()
	defer c.off.mu.Unlock()
	if c.off.onOff == nil {
		c.off.onOff = map[int]func(){}
	}
	id := c.off.next
	c.off.next++
	c.off.onOff[id] = fn
	return func() {
		c.off.mu.Lock()
		delete(c.off.onOff, id)
		c.off.mu.Unlock()
	}
}

func (o *offline) notifyOffline() {
	o.mu.Lock()
	fns := make([]func(), 0, len(o.onOff))
	for _, f := range o.onOff {
		fns = append(fns, f)
	}
	o.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func (o *offline) subscribeInvalidate(fn func(string)) func() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.onInv == nil {
		o.onInv = map[int]func(string){}
	}
	id := o.next
	o.next++
	o.onInv[id] = fn
	return func() {
		o.mu.Lock()
		delete(o.onInv, id)
		o.mu.Unlock()
	}
}

func (o *offline) notifyInvalidate(id string) {
	o.mu.Lock()
	fns := make([]func(string), 0, len(o.onInv))
	for _, f := range o.onInv {
		fns = append(fns, f)
	}
	o.mu.Unlock()
	for _, f := range fns {
		f(id)
	}
}
