// Package workgate decides when background work may run. Playback has priority: while
// the user plays, nothing of lower priority consumes CPU. Every background worker
// (thumbnails, posters, waveforms, scopes, proxies, cache warming, queue renders)
// asks the Gate before it starts (Acquire) and polls it during long loops
// (ShouldYield); background child processes are created through Command/Start so the
// gate can lower their priority and freeze them (SIGSTOP) while playing.
//
// Order of priority: audio > playback frames > scrub > visible > everything else.
// The package depends on the standard library only.
package workgate

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Prio is a work priority; lower values win.
type Prio int

const (
	PrioAudio      Prio = iota // audio engine, never gated
	PrioPlayback               // frames of the running playback
	PrioScrub                  // frames while the playhead is dragged
	PrioVisible                // thumbnails the user is looking at
	PrioBackground             // waveforms, posters off-screen, proxies, warming, queue
)

// State is what the user is doing right now.
type State int

const (
	Idle State = iota
	Scrubbing
	Playing
)

func (s State) String() string {
	switch s {
	case Scrubbing:
		return "scrubbing"
	case Playing:
		return "playing"
	}
	return "idle"
}

// Gate is the work gate. All methods are safe for concurrent use.
type Gate interface {
	// SetState is called by the owner of the clock (UI thread is fine, never blocks).
	SetState(State)
	State() State
	// Acquire blocks until work of priority p is allowed and a slot is free. The
	// returned release must be called once.
	Acquire(ctx context.Context, p Prio) (release func(), err error)
	// ShouldYield is a cheap atomic poll for long loops: true when work of priority p
	// should stop (or pause) right now.
	ShouldYield(p Prio) bool
	// Command builds a child process; for PrioVisible and lower it runs with low CPU
	// and I/O priority in its own process group.
	Command(ctx context.Context, p Prio, name string, args ...string) *exec.Cmd
	// Subscribe reports state changes (in order, from a separate goroutine).
	Subscribe(func(State)) (cancel func())
}

// Tracker is implemented by gates that freeze child processes. Call Track after
// cmd.Start and the returned func after cmd.Wait.
type Tracker interface {
	Track(cmd *exec.Cmd) (untrack func())
}

// Start starts cmd and registers it with g when g can freeze processes. The returned
// done must be called after cmd.Wait returned.
func Start(g Gate, cmd *exec.Cmd) (done func(), err error) {
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	if t, ok := g.(Tracker); ok {
		return t.Track(cmd), nil
	}
	return func() {}, nil
}

// Run is Start + Wait.
func Run(g Gate, cmd *exec.Cmd) error {
	done, err := Start(g, cmd)
	if err != nil {
		return err
	}
	defer done()
	return cmd.Wait()
}

// WaitIdle blocks until the gate has been Idle for at least d (or ctx ends). Proxies
// wait 1.5 s, cache warming 750 ms.
func WaitIdle(ctx context.Context, g Gate, d time.Duration) error {
	for {
		wait := 50 * time.Millisecond
		if g.State() == Idle {
			ig, ok := g.(interface{ IdleFor() time.Duration })
			if !ok {
				return nil
			}
			left := d - ig.IdleFor()
			if left <= 0 {
				return nil
			}
			wait = min(left, 200*time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Config tunes a Gate; zero values mean the defaults of the design.
type Config struct {
	IdleDebounce  time.Duration // Idle must last this long before PrioBackground starts (300 ms)
	ScrubDebounce time.Duration // Scrubbing -> Idle delay (120 ms)
	VisibleSlots  int           // concurrent PrioVisible slots when Idle (3)
	BackSlots     int           // concurrent PrioBackground slots when Idle (2)
	TickTimeout   time.Duration // Playing without Tick for this long reverts to Idle (1 s)
}

func (c Config) withDefaults() Config {
	if c.IdleDebounce == 0 {
		c.IdleDebounce = 300 * time.Millisecond
	}
	if c.ScrubDebounce == 0 {
		c.ScrubDebounce = 120 * time.Millisecond
	}
	if c.VisibleSlots == 0 {
		c.VisibleSlots = 3
	}
	if c.BackSlots == 0 {
		c.BackSlots = 2
	}
	if c.TickTimeout == 0 {
		c.TickTimeout = time.Second
	}
	return c
}

// New returns the real gate.
func New(cfg Config) *Impl {
	g := &Impl{cfg: cfg.withDefaults(), wake: make(chan struct{}), procs: map[*exec.Cmd]*proc{},
		subs: map[int]func(State){}}
	g.since.Store(time.Now().UnixNano())
	return g
}

// Impl is the default Gate.
type Impl struct {
	cfg Config

	state atomic.Int32
	since atomic.Int64 // UnixNano of the last transition
	gen   atomic.Uint64
	visOK atomic.Bool // visible thumbnails allowed while Playing (UI not busy)
	tick  atomic.Int64
	ticks atomic.Bool

	mu    sync.Mutex
	wake  chan struct{} // closed and replaced whenever the rules may have changed
	vis   int
	back  int
	procs map[*exec.Cmd]*proc

	subMu   sync.Mutex
	subs    map[int]func(State)
	subNext int
	queue   []State
	running bool
}

type proc struct {
	prio    Prio
	stopped bool
}

func (g *Impl) State() State { return State(g.state.Load()) }

// IdleFor is how long the gate has been Idle (0 when it is not).
func (g *Impl) IdleFor() time.Duration {
	if g.State() != Idle {
		return 0
	}
	return time.Since(time.Unix(0, g.since.Load()))
}

// Tick tells the gate the playback clock is alive. Once used, Playing without a tick
// for TickTimeout reverts to Idle (a clock that died must not freeze background work).
func (g *Impl) Tick() {
	g.tick.Store(time.Now().UnixNano())
	g.ticks.Store(true)
}

// AllowVisibleWhilePlaying lets one visible-viewport thumbnail load run during
// playback (when the UI is not busy and the preview is not stepped down).
func (g *Impl) AllowVisibleWhilePlaying(on bool) {
	g.visOK.Store(on)
	g.broadcast()
}

func (g *Impl) broadcast() {
	g.mu.Lock()
	close(g.wake)
	g.wake = make(chan struct{})
	g.mu.Unlock()
}

// SetState switches the state. Scrubbing -> Idle is delayed by ScrubDebounce so a drag
// that pauses for a moment does not release background work.
func (g *Impl) SetState(s State) {
	id := g.gen.Add(1)
	if s == Idle && g.State() == Scrubbing && g.cfg.ScrubDebounce > 0 {
		time.AfterFunc(g.cfg.ScrubDebounce, func() {
			if g.gen.Load() == id {
				g.apply(Idle)
			}
		})
		return
	}
	g.apply(s)
	if s == Playing {
		g.tick.Store(time.Now().UnixNano())
		if g.ticks.Load() {
			g.watch(id)
		}
	}
}

// watch reverts a Playing state whose clock stopped ticking.
func (g *Impl) watch(id uint64) {
	time.AfterFunc(g.cfg.TickTimeout/2, func() {
		if g.gen.Load() != id || g.State() != Playing {
			return
		}
		if time.Since(time.Unix(0, g.tick.Load())) > g.cfg.TickTimeout {
			g.gen.Add(1)
			g.apply(Idle)
			return
		}
		g.watch(id)
	})
}

func (g *Impl) apply(s State) {
	old := State(g.state.Swap(int32(s)))
	if old == s {
		return
	}
	g.since.Store(time.Now().UnixNano())
	g.syncProcs() // freeze right here, not via the notifier goroutine
	g.broadcast()
	if s == Idle {
		// Acquire waiters re-check once the idle debounce has passed
		time.AfterFunc(g.cfg.IdleDebounce+time.Millisecond, g.broadcast)
	}
	g.notify(s)
}

// allowed must be called with g.mu held.
func (g *Impl) allowed(p Prio) bool {
	st := g.State()
	switch p {
	case PrioVisible:
		switch st {
		case Idle:
			return g.vis < g.cfg.VisibleSlots
		case Scrubbing:
			return g.vis < 1
		}
		return g.visOK.Load() && g.vis < 1
	case PrioBackground:
		return st == Idle && g.IdleFor() >= g.cfg.IdleDebounce && g.back < g.cfg.BackSlots
	}
	return true
}

// ShouldYield implements Gate.
func (g *Impl) ShouldYield(p Prio) bool {
	st := g.State()
	switch p {
	case PrioVisible:
		return st == Playing && !g.visOK.Load()
	case PrioBackground:
		return st != Idle
	}
	return false
}

// Acquire implements Gate.
func (g *Impl) Acquire(ctx context.Context, p Prio) (func(), error) {
	for {
		g.mu.Lock()
		if g.allowed(p) {
			switch p {
			case PrioVisible:
				g.vis++
			case PrioBackground:
				g.back++
			}
			g.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					g.mu.Lock()
					switch p {
					case PrioVisible:
						g.vis--
					case PrioBackground:
						g.back--
					}
					close(g.wake)
					g.wake = make(chan struct{})
					g.mu.Unlock()
				})
			}, nil
		}
		w := g.wake
		g.mu.Unlock()
		// waiting only for the idle debounce to pass: wake up by timer
		var timer <-chan time.Time
		if p == PrioBackground && g.State() == Idle {
			if left := g.cfg.IdleDebounce - g.IdleFor(); left > 0 {
				timer = time.After(left + time.Millisecond)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w:
		case <-timer:
		}
	}
}

// Subscribe implements Gate.
func (g *Impl) Subscribe(fn func(State)) func() {
	g.subMu.Lock()
	defer g.subMu.Unlock()
	id := g.subNext
	g.subNext++
	g.subs[id] = fn
	return func() {
		g.subMu.Lock()
		delete(g.subs, id)
		g.subMu.Unlock()
	}
}

func (g *Impl) notify(s State) {
	g.subMu.Lock()
	g.queue = append(g.queue, s)
	if g.running {
		g.subMu.Unlock()
		return
	}
	g.running = true
	g.subMu.Unlock()
	go func() {
		for {
			g.subMu.Lock()
			if len(g.queue) == 0 {
				g.running = false
				g.subMu.Unlock()
				return
			}
			st := g.queue[0]
			g.queue = g.queue[1:]
			fns := make([]func(State), 0, len(g.subs))
			for _, f := range g.subs {
				fns = append(fns, f)
			}
			g.subMu.Unlock()
			for _, f := range fns {
				f(st)
			}
		}
	}()
}

// Nop is a gate that never blocks and never freezes anything (tests, headless tools).
type Nop struct{ st atomic.Int32 }

func (n *Nop) SetState(s State) { n.st.Store(int32(s)) }
func (n *Nop) State() State     { return State(n.st.Load()) }
func (n *Nop) Acquire(context.Context, Prio) (func(), error) {
	return func() {}, nil
}
func (n *Nop) ShouldYield(Prio) bool { return false }
func (n *Nop) Command(ctx context.Context, _ Prio, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
func (n *Nop) Subscribe(func(State)) func() { return func() {} }

var global atomic.Pointer[Gate]

func init() {
	var g Gate = New(Config{})
	global.Store(&g)
}

// Default is the process-wide gate.
func Default() Gate { return *global.Load() }

// SetDefault replaces the process-wide gate (tests install a Nop).
func SetDefault(g Gate) { global.Store(&g) }

var uiBusy atomic.Bool

// SetUIBusy is set by the preview's load governor when the UI thread is saturated
// (kit.Do latency p95 above its budget). UI code reacts by doing less per frame: the
// timeline stops following smoothly, scopes slow down.
func SetUIBusy(b bool) { uiBusy.Store(b) }

// UIBusy reports the flag of SetUIBusy.
func UIBusy() bool { return uiBusy.Load() }
