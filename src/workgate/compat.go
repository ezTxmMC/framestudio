package workgate

import (
	"context"
	"os/exec"
	"sync/atomic"
	"time"
)

var global atomic.Pointer[Gate]

func init() {
	var g Gate = New()
	global.Store(&g)
}

// Default is the process-wide gate.
func Default() Gate { return *global.Load() }

// SetDefault replaces the process-wide gate (tests install a NopGate).
func SetDefault(g Gate) { global.Store(&g) }

var uiBusy atomic.Bool

// SetUIBusy is set by the preview's load governor when the UI thread is saturated.
// UI code reacts by doing less per frame (timeline pages instead of following
// smoothly, scopes slow down).
func SetUIBusy(b bool) { uiBusy.Store(b) }

// UIBusy reports the flag of SetUIBusy.
func UIBusy() bool { return uiBusy.Load() }

// Start starts cmd as background work (PrioBackground) through g; done must be called
// after cmd.Wait.
func Start(g Gate, cmd *exec.Cmd) (done func(), err error) {
	return StartCmd(g, cmd, PrioBackground)
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

// WaitIdle blocks until the gate has been Idle for at least d (or ctx ends).
func WaitIdle(ctx context.Context, g Gate, d time.Duration) error {
	start := time.Time{}
	for {
		if g.State() == Idle {
			if start.IsZero() {
				start = time.Now()
			}
			if time.Since(start) >= d {
				return nil
			}
		} else {
			start = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
