package workgate

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// burners starts n gate-aware background workers that count work units. They behave
// like the real ones: Acquire before a unit, a short unit, release.
func burners(g Gate, n int, p Prio, stop <-chan struct{}) (*atomic.Int64, *sync.WaitGroup) {
	var units atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { <-stop; cancel() }()
			for {
				rel, err := g.Acquire(ctx, p)
				if err != nil {
					return
				}
				// one small unit of work (a thumbnail, a waveform block)
				t := time.Now()
				for time.Since(t) < time.Millisecond {
				}
				units.Add(1)
				rel()
			}
		}()
	}
	return &units, &wg
}

// Six concurrent poster-like jobs requested while playing must not run at all, and run
// to completion once the user stops (R8).
func TestBackgroundWorkersDoNothingWhilePlaying(t *testing.T) {
	g := New(Config{IdleDebounce: 10 * time.Millisecond})
	g.SetState(Playing)
	stop := make(chan struct{})
	units, wg := burners(g, 6, PrioBackground, stop)
	vis, wgv := burners(g, 6, PrioVisible, stop)
	time.Sleep(200 * time.Millisecond)
	if n := units.Load() + vis.Load(); n != 0 {
		t.Fatalf("%d work units ran while playing", n)
	}
	g.SetState(Idle)
	deadline := time.Now().Add(2 * time.Second)
	for (units.Load() < 20 || vis.Load() < 20) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if units.Load() < 20 || vis.Load() < 20 {
		t.Fatalf("workers did not resume after stop: bg %d visible %d", units.Load(), vis.Load())
	}
	g.SetState(Playing)
	time.Sleep(30 * time.Millisecond) // a unit in flight may finish (< 20 ms)
	before := units.Load() + vis.Load()
	time.Sleep(150 * time.Millisecond)
	if after := units.Load() + vis.Load(); after != before {
		t.Fatalf("workers kept running after play: %d more units", after-before)
	}
	close(stop)
	wg.Wait()
	wgv.Wait()
}

// cpuTicks reads utime+stime (clock ticks) of a process from /proc.
func cpuTicks(pid int) (int64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	f := strings.Fields(s[i+2:])
	if len(f) < 13 {
		return 0, false
	}
	u, _ := strconv.ParseInt(f[11], 10, 64)
	st, _ := strconv.ParseInt(f[12], 10, 64)
	return u + st, true
}

// A background child that burns CPU consumes (almost) none while playing: stopped
// within 100 ms, CPU delta over the following second below 50 ms (5 ticks of 10 ms).
func TestFrozenChildUsesNoCPU(t *testing.T) {
	if _, ok := cpuTicks(os.Getpid()); !ok {
		t.Skip("no /proc")
	}
	g := New(fast())
	cmd := g.Command(context.Background(), PrioBackground, "sh", "-c", "while :; do :; done")
	done, err := Start(g, cmd)
	if err != nil {
		t.Skip("sh not available")
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		done()
	}()
	time.Sleep(150 * time.Millisecond)
	run0, _ := cpuTicks(cmd.Process.Pid)
	time.Sleep(100 * time.Millisecond)
	run1, _ := cpuTicks(cmd.Process.Pid)
	if run1 <= run0 {
		t.Skip("busy child does not accumulate CPU here")
	}
	g.SetState(Playing)
	waitFor(t, func() bool { return procState(cmd.Process.Pid) == "T" }, "child not stopped within 100 ms", 100*time.Millisecond)
	c0, _ := cpuTicks(cmd.Process.Pid)
	time.Sleep(time.Second)
	c1, _ := cpuTicks(cmd.Process.Pid)
	if c1-c0 > 5 {
		t.Fatalf("frozen child used %d ticks (%d ms) in one second", c1-c0, (c1-c0)*10)
	}
}
