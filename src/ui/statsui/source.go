package statsui

import "sync"

// The preview pipeline registers what it offers here, so the UI needs no import of it.
var reg struct {
	mu          sync.RWMutex
	source      Source
	clearCache  func()
	proxyClear  func()
	proxyCreate func(path string)
}

// SetSource installs the statistics of the active preview (nil = none).
func SetSource(s Source) {
	reg.mu.Lock()
	reg.source = s
	reg.mu.Unlock()
}

// Current is a Source that reads whatever SetSource installed last.
var Current Source = SourceFunc(func() Snapshot {
	reg.mu.RLock()
	s := reg.source
	reg.mu.RUnlock()
	if s == nil {
		return Snapshot{}
	}
	return s.Snapshot()
})

// Hooks are the actions behind the commands "Vorschau-Cache leeren" (cache.clear),
// "Proxy-Cache leeren" (media.proxyClear) and "Proxy erzeugen" (media.proxyCreate). The
// preview pipeline and the proxy manager install them; a command without a hook is
// disabled.
type Hooks struct {
	ClearCache  func()
	ProxyClear  func()
	ProxyCreate func(path string)
}

// SetHooks installs the actions; nil fields keep their commands disabled.
func SetHooks(h Hooks) {
	reg.mu.Lock()
	reg.clearCache, reg.proxyClear, reg.proxyCreate = h.ClearCache, h.ProxyClear, h.ProxyCreate
	reg.mu.Unlock()
}

// GetHooks returns the installed actions.
func GetHooks() Hooks {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return Hooks{ClearCache: reg.clearCache, ProxyClear: reg.proxyClear, ProxyCreate: reg.proxyCreate}
}
