package shell

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/media/hwaccel"
	"framestudio-proto/src/media/proxy"
	"framestudio-proto/src/settings"
	"framestudio-proto/src/ui/statsui"
	"framestudio-proto/src/ui/viewer"
	"framestudio-proto/src/workgate"
)

// perfServices are the process-wide helpers of the preview pipeline: the proxy manager
// (background low-resolution intra copies of heavy sources) and the hardware decoding
// probe. They are created once, on the first shell, and shared by all media caches.
var perfServices struct {
	once  sync.Once
	proxy *proxy.Manager
	hw    *hwaccel.Manager
}

// startPerfServices wires cache into the work gate, the proxies and the hardware
// decoder, and installs the commands' hooks. Tests (which must not touch the user's
// cache folder or start probes) only get the gate.
func startPerfServices(cache *media.Cache) {
	cache.SetGate(workgate.Default())
	if testing.Testing() {
		return
	}
	s := &perfServices
	s.once.Do(func() {
		g := workgate.Default()
		cur := settings.Get().Playback
		s.proxy = proxy.New(proxy.Config{Gate: g, Mode: cur.ProxyMode, CapGB: cur.ProxyCapGB})
		s.proxy.StartCleanup(30 * time.Second)
		s.hw = hwaccel.NewManager(&hwaccel.Prober{CacheDir: filepath.Dir(proxy.DefaultDir())})
		s.hw.SetMode(cur.HwDecode)
		s.hw.Start(5 * time.Second)
		settings.Subscribe(func(n settings.Settings) {
			s.proxy.SetMode(n.Playback.ProxyMode)
			s.proxy.SetCapGB(n.Playback.ProxyCapGB)
			s.hw.SetMode(n.Playback.HwDecode)
		})
		statsui.SetHooks(statsui.Hooks{
			ClearCache: viewer.ClearCaches,
			ProxyClear: s.proxy.Clear,
			ProxyCreate: func(path string) {
				if _, ok := s.proxy.Lookup(path); !ok {
					s.proxy.Regenerate(path)
				}
			},
		})
	})
	cache.SetProxies(s.proxy)
	cache.SetHW(s.hw)
}
