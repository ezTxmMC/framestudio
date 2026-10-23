package workgate

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fast() Config {
	return Config{IdleDebounce: 20 * time.Millisecond, ScrubDebounce: 20 * time.Millisecond, TickTimeout: 100 * time.Millisecond}
}

func TestBackgroundBlockedWhilePlayingAndScrubbing(t *testing.T) {
	g := New(fast())
	g.SetState(Playing)
	if !g.ShouldYield(PrioBackground) || !g.ShouldYield(PrioVisible) {
		t.Fatal("background and visible work must yield while playing")
	}
	if g.ShouldYield(PrioPlayback) || g.ShouldYield(PrioScrub) || g.ShouldYield(PrioAudio) {
		t.Fatal("playback work never yields")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, PrioBackground); err == nil {
		t.Fatal("background acquired while playing")
	}
	g.SetState(Scrubbing)
	if !g.ShouldYield(PrioBackground) || g.ShouldYield(PrioVisible) {
		t.Fatal("scrubbing: background yields, visible may run")
	}
}

func TestAcquireWaitsForIdleDebounce(t *testing.T) {
	g := New(fast())
	g.SetState(Playing)
	got := make(chan time.Duration, 1)
	go func() {
		rel, err := g.Acquire(context.Background(), PrioBackground)
		if err != nil {
			return
		}
		got <- g.IdleFor()
		rel()
	}()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("acquired while playing")
	default:
	}
	g.SetState(Idle)
	select {
	case d := <-got:
		if d < 20*time.Millisecond {
			t.Fatalf("started after only %v idle", d)
		}
	case <-time.After(time.Second):
		t.Fatal("never acquired after stop")
	}
}

func TestSlotLimits(t *testing.T) {
	g := New(fast())
	time.Sleep(30 * time.Millisecond)
	r1, _ := g.Acquire(context.Background(), PrioBackground)
	r2, _ := g.Acquire(context.Background(), PrioBackground)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, PrioBackground); err == nil {
		t.Fatal("third background slot granted")
	}
	r1()
	r1() // double release is harmless
	r3, err := g.Acquire(context.Background(), PrioBackground)
	if err != nil {
		t.Fatal(err)
	}
	r2()
	r3()
	var vis []func()
	for i := 0; i < 3; i++ {
		r, err := g.Acquire(context.Background(), PrioVisible)
		if err != nil {
			t.Fatal(err)
		}
		vis = append(vis, r)
	}
	for _, r := range vis {
		r()
	}
}

func TestScrubDebounce(t *testing.T) {
	g := New(fast())
	g.SetState(Scrubbing)
	g.SetState(Idle)
	if g.State() != Scrubbing {
		t.Fatal("scrub ended immediately")
	}
	g.SetState(Scrubbing) // a new drag cancels the pending end
	time.Sleep(50 * time.Millisecond)
	if g.State() != Scrubbing {
		t.Fatal("pending idle was not cancelled")
	}
	g.SetState(Idle)
	time.Sleep(60 * time.Millisecond)
	if g.State() != Idle {
		t.Fatal("scrub did not end")
	}
}

func TestPlayingWithoutTicksRevertsToIdle(t *testing.T) {
	g := New(fast())
	g.Tick()
	g.SetState(Playing)
	deadline := time.Now().Add(time.Second)
	for g.State() == Playing && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.State() != Idle {
		t.Fatal("dead clock kept the gate in Playing")
	}
}

func TestSubscribeOrdered(t *testing.T) {
	g := New(fast())
	var seq atomic.Value
	seq.Store("")
	done := make(chan struct{})
	cancel := g.Subscribe(func(s State) {
		seq.Store(seq.Load().(string) + s.String()[:1])
		if len(seq.Load().(string)) == 3 {
			close(done)
		}
	})
	defer cancel()
	g.SetState(Playing)
	g.SetState(Idle)
	g.SetState(Playing)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("not notified")
	}
	if seq.Load().(string) != "pip" {
		t.Fatalf("order %q", seq.Load())
	}
}

// procState reads the state letter of a process from /proc.
func procState(pid int) string {
	b, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return ""
	}
	return s[i+2 : i+3]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestBackgroundProcessFrozenWhilePlaying(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	g := New(fast())
	cmd := g.Command(context.Background(), PrioBackground, "sleep", "60")
	done, err := Start(g, cmd)
	if err != nil {
		t.Skip("sleep not available:", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		done()
	}()
	g.SetState(Playing)
	waitFor(t, func() bool { return procState(cmd.Process.Pid) == "T" }, "process not stopped within 100 ms", 100*time.Millisecond)
	g.SetState(Idle)
	waitFor(t, func() bool { return procState(cmd.Process.Pid) != "T" }, "process not resumed", time.Second)
}

func TestPlaybackProcessNeverFrozen(t *testing.T) {
	g := New(fast())
	cmd := g.Command(context.Background(), PrioPlayback, "sleep", "60")
	done, err := Start(g, cmd)
	if err != nil {
		t.Skip("sleep not available")
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		done()
	}()
	g.SetState(Playing)
	time.Sleep(50 * time.Millisecond)
	if procState(cmd.Process.Pid) == "T" {
		t.Fatal("playback decoder was frozen")
	}
}

func waitFor(t *testing.T, ok func() bool, msg string, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !ok() {
		t.Fatal(msg)
	}
}

func TestNop(t *testing.T) {
	var g Gate = &Nop{}
	g.SetState(Playing)
	rel, err := g.Acquire(context.Background(), PrioBackground)
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if g.ShouldYield(PrioBackground) {
		t.Fatal("nop yields")
	}
}
