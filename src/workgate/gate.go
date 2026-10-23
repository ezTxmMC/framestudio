// Package workgate decides when background work may run. Playback frames and audio come
// first; everything else (thumbnails, waveforms, proxies, cache warming, queue renders)
// is delayed or paused while the user plays or scrubs.
//
// Priorities: P0 audio > P1 playback frames > P2 scrub > P3 visible background work >
// P4 background work. P0..P2 are never gated. The package uses only the standard library.
package workgate

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Prio is the priority class of a piece of work.
type Prio int

const (
	PrioAudio Prio = iota
	PrioPlayback
	PrioScrub
	PrioVisible    // thumbnails of items the user is looking at
	PrioBackground // waveforms, off-screen posters, proxies, cache warming, queue renders
)

// State is what the user does with the playhead.
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

// Gate is the interface every background worker uses.
type Gate interface {
	// SetState is called by the session or clock owner; it never blocks.
	SetState(State)
	State() State
	// Acquire blocks until work of priority p may run and returns the release func.
	Acquire(ctx context.Context, p Prio) (release func(), err error)
	// ShouldYield is a cheap atomic poll for long loops: true when work of priority p
	// should stop what it does and call Acquire again.
	ShouldYield(p Prio) bool
	// Command builds an ffmpeg-like command for work of priority p. Background priorities
	// get their own process group so they can be suspended as a whole.
	Command(ctx context.Context, p Prio, name string, args ...string) *exec.Cmd
	// Subscribe reports state changes (in order, from one goroutine).
	Subscribe(func(State)) (cancel func())
}

// Starter is implemented by gates that manage child processes. Start starts cmd, lowers
// its CPU priority for background work and registers it for SIGSTOP/SIGCONT; done must
// be called after cmd.Wait.
type Starter interface {
	Start(cmd *exec.Cmd, p Prio) (done func(), err error)
}

// StartCmd starts cmd through g when it manages processes, else plainly.
func StartCmd(g Gate, cmd *exec.Cmd, p Prio) (done func(), err error) {
	if s, ok := g.(Starter); ok {
		return s.Start(cmd, p)
	}
	return func() {}, cmd.Start()
}

// Limits and debounce times of the default gate.
const (
	IdleSlotsVisible  = 3
	IdleSlotsBack     = 2
	ScrubSlotsVisible = 1
	BackgroundDelay   = 300 * time.Millisecond // idle time before P4 work starts
	ScrubIdleDelay    = 120 * time.Millisecond // scrubbing ends this long after the last drag
	HeartbeatTimeout  = time.Second            // "playing" without clock ticks reverts to idle
)

// Controller is the real Gate.
type Controller struct {
	state     atomic.Int32
	idleSince atomic.Int64 // unix nanos of the last switch to Idle
	beat      atomic.Int64 // unix nanos of the last Heartbeat while playing
	allowVis  atomic.Bool  // visible thumbnails may use one slot while playing

	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state or slot change
	vis     int
	back    int
	pending *time.Timer // delayed Scrubbing -> Idle
	genSet  uint64

	subs     map[int]func(State)
	nextSub  int
	queue    []State
	wake     chan struct{}
	closed   chan struct{}
	closeOne sync.Once

	procs procSet
}

// New creates a gate in the Idle state. Close stops its goroutines.
func New() *Controller {
	c := &Controller{
		changed: make(chan struct{}),
		subs:    map[int]func(State){},
		wake:    make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	c.idleSince.Store(time.Now().Add(-time.Hour).UnixNano())
	go c.dispatch()
	return c
}

// Close stops the dispatcher and resumes every suspended process.
func (c *Controller) Close() {
	c.closeOne.Do(func() {
		close(c.closed)
		c.procs.resumeAll()
	})
}

// AllowVisibleWhilePlaying lets one P3 job run while playing (the UI is not busy and the
// preview quality is not stepped down); the viewer's load governor toggles it.
func (c *Controller) AllowVisibleWhilePlaying(ok bool) {
	c.allowVis.Store(ok)
	c.procs.update(c.State(), ok)
	c.broadcast()
}

// broadcast wakes every Acquire waiter.
func (c *Controller) broadcast() {
	c.mu.Lock()
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

// State reports the current state.
func (c *Controller) State() State { return State(c.state.Load()) }

// Heartbeat tells the gate the clock is alive. Playing without a heartbeat for a second
// after one was seen reverts to Idle (a crashed clock must not park all background work).
func (c *Controller) Heartbeat() { c.beat.Store(time.Now().UnixNano()) }

// SetState changes the state. Leaving Scrubbing for Idle takes effect after
// ScrubIdleDelay unless the scrubbing continues.
func (c *Controller) SetState(s State) {
	c.mu.Lock()
	c.genSet++
	gen := c.genSet
	if c.pending != nil {
		c.pending.Stop()
		c.pending = nil
	}
	if s == Idle && State(c.state.Load()) == Scrubbing {
		c.pending = time.AfterFunc(ScrubIdleDelay, func() {
			c.mu.Lock()
			ok := c.genSet == gen
			c.mu.Unlock()
			if ok {
				c.apply(Idle)
			}
		})
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.apply(s)
}

func (c *Controller) apply(s State) {
	old := State(c.state.Swap(int32(s)))
	if old == s {
		return
	}
	if s == Idle {
		c.idleSince.Store(time.Now().UnixNano())
	}
	if s == Playing {
		c.beat.Store(0)
		go c.watchdog()
	}
	// Suspension happens right here, not in the subscriber goroutine, so the 50 ms
	// guarantee does not depend on slow subscribers.
	c.procs.update(s, c.allowVis.Load())
	c.mu.Lock()
	c.queue = append(c.queue, s)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Controller) watchdog() {
	t := time.NewTicker(HeartbeatTimeout / 4)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
		}
		if c.State() != Playing {
			return
		}
		if b := c.beat.Load(); b != 0 && time.Since(time.Unix(0, b)) > HeartbeatTimeout {
			c.apply(Idle)
			return
		}
	}
}

func (c *Controller) dispatch() {
	for {
		select {
		case <-c.closed:
			return
		case <-c.wake:
		}
		for {
			c.mu.Lock()
			if len(c.queue) == 0 {
				c.mu.Unlock()
				break
			}
			s := c.queue[0]
			c.queue = c.queue[1:]
			subs := make([]func(State), 0, len(c.subs))
			for _, f := range c.subs {
				subs = append(subs, f)
			}
			c.mu.Unlock()
			for _, f := range subs {
				f(s)
			}
		}
	}
}

// Subscribe registers fn for state changes.
func (c *Controller) Subscribe(fn func(State)) (cancel func()) {
	c.mu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

func (c *Controller) idleLong() bool {
	return time.Since(time.Unix(0, c.idleSince.Load())) >= BackgroundDelay
}

// allowed reports whether work of priority p may take a slot now (c.mu held for the slot
// counters). wait is how long until the answer may change by time alone.
func (c *Controller) allowed(p Prio) (ok bool, wait time.Duration) {
	switch p {
	case PrioVisible:
		switch c.State() {
		case Idle:
			return c.vis < IdleSlotsVisible, 0
		case Scrubbing:
			return c.vis < ScrubSlotsVisible, 0
		default:
			return c.allowVis.Load() && c.vis < 1, 0
		}
	case PrioBackground:
		if c.State() != Idle {
			return false, 0
		}
		since := time.Since(time.Unix(0, c.idleSince.Load()))
		if since < BackgroundDelay {
			return false, BackgroundDelay - since
		}
		return c.back < IdleSlotsBack, 0
	}
	return true, 0
}

// Acquire implements Gate.
func (c *Controller) Acquire(ctx context.Context, p Prio) (func(), error) {
	if p < PrioVisible {
		return func() {}, ctx.Err()
	}
	for {
		c.mu.Lock()
		ok, wait := c.allowed(p)
		if ok {
			if p == PrioVisible {
				c.vis++
			} else {
				c.back++
			}
			c.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					c.mu.Lock()
					if p == PrioVisible {
						c.vis--
					} else {
						c.back--
					}
					close(c.changed)
					c.changed = make(chan struct{})
					c.mu.Unlock()
				})
			}, nil
		}
		ch := c.changed
		c.mu.Unlock()
		var timer <-chan time.Time
		var t *time.Timer
		if wait > 0 {
			t = time.NewTimer(wait + time.Millisecond)
			timer = t.C
		}
		select {
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			return nil, ctx.Err()
		case <-ch:
		case <-timer:
		case <-c.closed:
			return func() {}, nil
		}
		if t != nil {
			t.Stop()
		}
	}
}

// ShouldYield implements Gate.
func (c *Controller) ShouldYield(p Prio) bool {
	switch p {
	case PrioVisible:
		return c.State() == Playing && !c.allowVis.Load()
	case PrioBackground:
		return c.State() != Idle || !c.idleLong()
	}
	return false
}

// Command implements Gate.
func (c *Controller) Command(ctx context.Context, p Prio, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if p >= PrioVisible {
		prepare(cmd)
	}
	return cmd
}

// Start implements Starter.
func (c *Controller) Start(cmd *exec.Cmd, p Prio) (func(), error) {
	if p >= PrioVisible {
		prepare(cmd)
	}
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	if p < PrioVisible {
		return func() {}, nil
	}
	lower(cmd.Process.Pid)
	pr := c.procs.add(cmd.Process.Pid, p)
	// The state may have changed while the process started.
	c.procs.update(c.State(), c.allowVis.Load())
	return func() { c.procs.remove(pr) }, nil
}

// Suspended reports how many registered processes are currently stopped (for tests).
func (c *Controller) Suspended() int { return c.procs.stopped() }

// NopGate never blocks and never suspends: the default for tests and tools.
type NopGate struct{}

func (NopGate) SetState(State)        {}
func (NopGate) State() State          { return Idle }
func (NopGate) ShouldYield(Prio) bool { return false }
func (NopGate) Acquire(ctx context.Context, _ Prio) (func(), error) {
	return func() {}, ctx.Err()
}
func (NopGate) Command(ctx context.Context, _ Prio, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
func (NopGate) Subscribe(func(State)) func() { return func() {} }

var (
	_ Gate    = (*Controller)(nil)
	_ Starter = (*Controller)(nil)
	_ Gate    = NopGate{}
)
