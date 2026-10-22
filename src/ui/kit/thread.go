package kit

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
)

// All background work hands results to the UI thread through Do / DoAndWait.
// In the app they are fyne.Do / fyne.DoAndWait. Fyne's test driver runs those
// inline on the calling goroutine, which races with the test goroutine; tests
// install a UIQueue instead, which runs callbacks on the test goroutine.

var queue atomic.Pointer[UIQueue]

// Do runs f on the UI thread without waiting.
func Do(f func()) {
	if q := queue.Load(); q != nil {
		q.post(f, false)
		return
	}
	fyne.Do(f)
}

// DoAndWait runs f on the UI thread and returns when it has finished.
func DoAndWait(f func()) {
	if q := queue.Load(); q != nil {
		q.post(f, true)
		return
	}
	fyne.DoAndWait(f)
}

// UIQueue makes the calling test goroutine the UI thread: Do calls from that
// goroutine run inline, calls from other goroutines wait for Drain or Until.
type UIQueue struct {
	owner uint64
	mu    sync.Mutex
	fns   []func()
	wake  chan struct{}
}

// UseUIQueue installs a queue owned by the calling goroutine until cleanup runs.
func UseUIQueue(t interface{ Cleanup(func()) }) *UIQueue {
	q := &UIQueue{owner: goid(), wake: make(chan struct{}, 1)}
	queue.Store(q)
	t.Cleanup(func() {
		queue.CompareAndSwap(q, nil)
		q.Drain()
	})
	return q
}

func (q *UIQueue) post(f func(), wait bool) {
	if goid() == q.owner {
		f()
		return
	}
	var done chan struct{}
	if wait {
		done = make(chan struct{})
		g := f
		f = func() { defer close(done); g() }
	}
	q.mu.Lock()
	q.fns = append(q.fns, f)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	if wait {
		<-done
	}
}

// Drain runs all queued callbacks on the caller and reports how many ran.
func (q *UIQueue) Drain() int {
	n := 0
	for {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		if len(fns) == 0 {
			return n
		}
		for _, f := range fns {
			f()
		}
		n += len(fns)
	}
}

// Until drains callbacks until cond holds or the timeout passes.
func (q *UIQueue) Until(cond func() bool, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		q.Drain()
		if cond() {
			return true
		}
		select {
		case <-q.wake:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			q.Drain()
			return cond()
		}
	}
}

// Settle drains until no callback arrived for the quiet period (or timeout).
func (q *UIQueue) Settle(quiet, timeout time.Duration) {
	end := time.Now().Add(timeout)
	last := time.Now()
	for time.Now().Before(end) {
		if q.Drain() > 0 {
			last = time.Now()
		} else if time.Since(last) >= quiet {
			return
		}
		select {
		case <-q.wake:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// goid returns the current goroutine's id (only used on the test path).
func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
