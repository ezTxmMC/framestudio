//go:build !windows

package workgate

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func eventually(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func procState(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "?"
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return "?"
	}
	return s[i+2 : i+3]
}

func TestHighPrioNeverBlocks(t *testing.T) {
	g := New()
	defer g.Close()
	g.SetState(Playing)
	for _, p := range []Prio{PrioAudio, PrioPlayback, PrioScrub} {
		rel, err := g.Acquire(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		rel()
		if g.ShouldYield(p) {
			t.Fatalf("prio %d must never yield", p)
		}
	}
}

func TestBackgroundBlockedWhilePlaying(t *testing.T) {
	g := New()
	defer g.Close()
	g.SetState(Playing)
	if !g.ShouldYield(PrioBackground) || !g.ShouldYield(PrioVisible) {
		t.Fatal("background work must yield while playing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, PrioBackground); err == nil {
		t.Fatal("P4 acquired while playing")
	}
	got := make(chan time.Time, 1)
	go func() {
		rel, err := g.Acquire(context.Background(), PrioBackground)
		if err == nil {
			rel()
		}
		got <- time.Now()
	}()
	time.Sleep(30 * time.Millisecond)
	stop := time.Now()
	g.SetState(Idle)
	at := <-got
	if d := at.Sub(stop); d < BackgroundDelay-20*time.Millisecond {
		t.Fatalf("P4 started %v after idle, want >= %v debounce", d, BackgroundDelay)
	}
}

func TestSlotsAndScrubbing(t *testing.T) {
	g := New()
	defer g.Close()
	var rels []func()
	for i := 0; i < IdleSlotsVisible; i++ {
		r, _ := g.Acquire(context.Background(), PrioVisible)
		rels = append(rels, r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, PrioVisible); err == nil {
		t.Fatal("fourth visible slot granted")
	}
	for _, r := range rels {
		r()
	}
	g.SetState(Scrubbing)
	r, err := g.Acquire(context.Background(), PrioVisible)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel2()
	if _, err := g.Acquire(ctx2, PrioVisible); err == nil {
		t.Fatal("second visible slot while scrubbing")
	}
	r()
	if !g.ShouldYield(PrioBackground) {
		t.Fatal("P4 must yield while scrubbing")
	}
}

func TestScrubIdleDebounce(t *testing.T) {
	g := New()
	defer g.Close()
	g.SetState(Scrubbing)
	g.SetState(Idle)
	if g.State() != Scrubbing {
		t.Fatal("scrubbing ended without debounce")
	}
	g.SetState(Scrubbing) // drag continues
	time.Sleep(ScrubIdleDelay + 40*time.Millisecond)
	if g.State() != Scrubbing {
		t.Fatal("continued scrub was ended by the old timer")
	}
	g.SetState(Idle)
	eventually(t, time.Second, "idle after debounce", func() bool { return g.State() == Idle })
}

func TestSubscribeOrder(t *testing.T) {
	g := New()
	defer g.Close()
	ch := make(chan State, 8)
	cancel := g.Subscribe(func(s State) { ch <- s })
	g.SetState(Playing)
	g.SetState(Idle)
	if a, b := <-ch, <-ch; a != Playing || b != Idle {
		t.Fatalf("order %v %v", a, b)
	}
	cancel()
	g.SetState(Playing)
	select {
	case s := <-ch:
		t.Fatalf("event after cancel: %v", s)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSuspendsProcessGroup(t *testing.T) {
	g := New()
	defer g.Close()
	cmd := g.Command(context.Background(), PrioBackground, "sh", "-c", "sleep 60 & sleep 60; wait")
	done, err := g.Start(cmd, PrioBackground)
	if err != nil {
		t.Skip("cannot start sh:", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		done()
	}()
	pid := cmd.Process.Pid
	g.SetState(Playing)
	t0 := time.Now()
	eventually(t, 100*time.Millisecond, "process stopped", func() bool { return procState(pid) == "T" })
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Fatalf("stop took %v", d)
	}
	if g.Suspended() != 1 {
		t.Fatalf("suspended %d", g.Suspended())
	}
	g.SetState(Idle)
	eventually(t, 200*time.Millisecond, "process resumed", func() bool { return procState(pid) != "T" })
}

func TestStartWhilePlayingStopsImmediately(t *testing.T) {
	g := New()
	defer g.Close()
	g.SetState(Playing)
	cmd := g.Command(context.Background(), PrioBackground, "sleep", "60")
	done, err := g.Start(cmd, PrioBackground)
	if err != nil {
		t.Skip(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		done()
	}()
	eventually(t, 100*time.Millisecond, "stopped at start", func() bool { return procState(cmd.Process.Pid) == "T" })
}

func TestHeartbeatWatchdog(t *testing.T) {
	g := New()
	defer g.Close()
	g.SetState(Playing)
	g.Heartbeat()
	eventually(t, 2*time.Second, "revert to idle without heartbeat", func() bool { return g.State() == Idle })
}

func TestNopGate(t *testing.T) {
	var g Gate = NopGate{}
	rel, err := g.Acquire(context.Background(), PrioBackground)
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if g.ShouldYield(PrioBackground) {
		t.Fatal("nop gate yields")
	}
}
