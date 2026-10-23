package application

import (
	"bufio"
	"bytes"
	"math"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"framestudio-proto/src/media"
	"framestudio-proto/src/settings"
)

// Memory tuning (R9). GC pauses of the preview are far below a frame (measured under
// 2 ms), so GOGC stays at the Go default; what protects the machine is the memory limit.
// The frame and buffer pools are big and long-lived, the explicit caches count towards
// the limit, and the MemoryWatcher shrinks them when the heap gets close to it.

const (
	gib = int64(1) << 30

	minMemLimit = 3 * gib / 2 // 1.5 GiB
	maxMemLimit = 3 * gib
	fallbackRAM = 8 * gib
)

// MemoryLimit is the soft Go memory limit for a machine with total bytes of RAM:
// half of it, between 1.5 and 3 GiB.
func MemoryLimit(total int64) int64 {
	if total <= 0 {
		total = fallbackRAM
	}
	return min(max(total/2, minMemLimit), maxMemLimit)
}

// ParseMemTotal reads MemTotal from /proc/meminfo content; 0 if it is missing.
func ParseMemTotal(meminfo []byte) int64 {
	sc := bufio.NewScanner(bytes.NewReader(meminfo))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil || kb <= 0 {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}

// TuneGC sets the soft memory limit unless the user set GOMEMLIMIT, and leaves GOGC at
// the Go default unless the user set it. It returns the limit now in force (-1 when the
// user's environment decides).
func TuneGC(getenv func(string) string, totalRAM int64) int64 {
	if getenv("GOGC") == "" {
		debug.SetGCPercent(100)
	}
	if getenv("GOMEMLIMIT") != "" {
		return -1
	}
	limit := MemoryLimit(totalRAM)
	debug.SetMemoryLimit(limit)
	return limit
}

// AutoCacheMB is the preview cache budget for Playback.CacheMB: 0 = 10 % of the RAM
// between 256 and 2048 MB, -1 = off (0 bytes), otherwise the value.
func AutoCacheMB(setting int, totalRAM int64) int {
	switch {
	case setting < 0:
		return 0
	case setting > 0:
		return setting
	}
	if totalRAM <= 0 {
		totalRAM = fallbackRAM
	}
	return int(min(max(totalRAM/10>>20, 256), 2048))
}

// Budgeter is implemented by explicit caches that can give memory back under pressure.
type Budgeter interface {
	// ScaleBudget sets the share (1 = configured size, 0.5 = half) the cache may use.
	ScaleBudget(share float64)
}

// MemoryWatcher polls the live heap once a second and, above 80 % of the limit, halves
// the registered caches; below 60 % they get their full budget back.
type MemoryWatcher struct {
	limit int64
	read  func() int64

	mu        sync.Mutex
	budgeters []Budgeter
	pressure  atomic.Bool
	stop      chan struct{}
	once      sync.Once
}

// NewMemoryWatcher watches against limit; read returns the live heap bytes (nil = the
// runtime metric).
func NewMemoryWatcher(limit int64, read func() int64) *MemoryWatcher {
	if read == nil {
		read = heapObjects
	}
	return &MemoryWatcher{limit: limit, read: read, stop: make(chan struct{})}
}

// Add registers a cache.
func (w *MemoryWatcher) Add(b Budgeter) {
	w.mu.Lock()
	w.budgeters = append(w.budgeters, b)
	w.mu.Unlock()
}

// Pressure reports whether the caches are currently shrunk.
func (w *MemoryWatcher) Pressure() bool { return w.pressure.Load() }

// Check runs one poll; Start calls it every second.
func (w *MemoryWatcher) Check() {
	if w.limit <= 0 {
		return
	}
	heap := w.read()
	was := w.pressure.Load()
	switch {
	case !was && heap > w.limit*8/10:
		w.pressure.Store(true)
	case was && heap < w.limit*6/10:
		w.pressure.Store(false)
	default:
		return
	}
	share := 1.0
	if w.pressure.Load() {
		share = 0.5
	}
	w.mu.Lock()
	bs := append([]Budgeter(nil), w.budgeters...)
	w.mu.Unlock()
	for _, b := range bs {
		b.ScaleBudget(share)
	}
}

// Start polls in the background until Stop.
func (w *MemoryWatcher) Start() {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				w.Check()
			case <-w.stop:
				return
			}
		}
	}()
}

// Stop ends the polling.
func (w *MemoryWatcher) Stop() { w.once.Do(func() { close(w.stop) }) }

func heapObjects() int64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(s[0].Value.Uint64())
}

// mediaBudget is the media frame cache as a Budgeter: the configured size scaled.
type mediaBudget struct {
	mu    sync.Mutex
	base  media.Config
	share float64
}

func newMediaBudget() *mediaBudget { return &mediaBudget{share: 1} }

func (m *mediaBudget) apply() {
	c := m.base
	if m.share < 1 && c.FrameCacheMB > 0 {
		c.FrameCacheMB = max(int(float64(c.FrameCacheMB)*m.share), 16)
	}
	if m.share < 1 && c.StillCacheMB > 0 {
		c.StillCacheMB = max(int(float64(c.StillCacheMB)*m.share), 16)
	}
	media.Configure(c)
}

// Set takes the configured limits.
func (m *mediaBudget) Set(c media.Config) {
	m.mu.Lock()
	m.base = c
	m.apply()
	m.mu.Unlock()
}

// ScaleBudget implements Budgeter.
func (m *mediaBudget) ScaleBudget(share float64) {
	m.mu.Lock()
	m.share = share
	m.apply()
	m.mu.Unlock()
}

// SystemRAM reads the machine's memory from the operating system (0 if unknown).
func SystemRAM(read func(string) ([]byte, error)) int64 {
	b, err := read("/proc/meminfo")
	if err != nil {
		return 0
	}
	total := ParseMemTotal(b)
	// a container or cgroup may allow far less than the host has
	for _, f := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		if cb, err := read(f); err == nil {
			if lim := ParseCgroupLimit(cb); lim > 0 && (total <= 0 || lim < total) {
				total = lim
			}
		}
	}
	return total
}

// ParseCgroupLimit reads a cgroup memory limit file ("max" or an unlimited-looking
// value gives 0).
func ParseCgroupLimit(b []byte) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || v <= 0 || v >= 1<<60 {
		return 0
	}
	return v
}

// EffectiveLimit is the soft memory limit in force after TuneGC: its result, or the
// limit the user's GOMEMLIMIT set (total-derived default if none is in force).
func EffectiveLimit(tuned, totalRAM int64) int64 {
	if tuned > 0 {
		return tuned
	}
	if cur := debug.SetMemoryLimit(-1); cur > 0 && cur < math.MaxInt64 {
		return cur
	}
	return MemoryLimit(totalRAM)
}

// PreviewCacheBytes is the byte budget of the preview frame cache from the settings.
func PreviewCacheBytes(s settings.Settings, totalRAM int64) int64 {
	return int64(AutoCacheMB(s.Playback.CacheMB, totalRAM)) << 20
}
