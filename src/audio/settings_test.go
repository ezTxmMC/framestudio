package audio

import (
	"errors"
	"math"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMasterGainAndMuteActOnOutputOnly(t *testing.T) {
	r := newRig(t)
	r.play(50, 1)
	r.step(3)
	r.tick()
	prev := r.pos(BlockFrames - 1)

	r.e.SetMasterGain(0.5)
	r.tick()
	r.step(1)
	want := float64((prev + 1) % 16384)
	if got := float64(r.out.last()[0]); math.Abs(got-want/2) > 2 {
		t.Fatalf("half gain: sample %v, want ~%v", got, want/2)
	}

	r.e.SetMuted(true)
	r.tick()
	r.step(2)
	if !silent(r.out.last()) {
		t.Fatal("muted output must be silent")
	}
	if m, _ := r.v.Levels(r.clk.Now()); m.Peak[0] == 0 {
		t.Fatal("meters keep showing the mix while the monitor is muted")
	}
	r.e.SetMuted(false)
	r.e.SetMasterGain(1)
	r.tick()
	r.step(2)
	if silent(r.out.last()) {
		t.Fatal("unmuted output sounds again")
	}
	r.e.SetMasterGain(100)
	if r.e.MasterGain() != MaxMasterGain {
		t.Fatalf("gain clamps to %v, got %v", MaxMasterGain, r.e.MasterGain())
	}
	r.e.SetMasterGain(math.NaN())
	if r.e.MasterGain() != 0 {
		t.Fatal("invalid gain mutes")
	}
}

func TestLatencyPriority(t *testing.T) {
	t.Setenv("FS_AUDIO_LATENCY_MS", "")
	out := &fakeOut{}
	e := New(Config{Open: func() (Output, error) { return out, nil }, Cache: NewCache(newFakeMedia(0).decode, newFakeMedia(0).stat, 0)})
	defer e.Close()
	if e.Latency() != 0 {
		t.Fatal("no output yet, no estimate")
	}
	e.SetLatency(80 * time.Millisecond)
	if e.Latency() != 80*time.Millisecond {
		t.Fatalf("user latency: %v", e.Latency())
	}
	e.SetLatency(0)
	if e.Latency() != 0 {
		t.Fatal("0 goes back to the estimate")
	}

	t.Setenv("FS_AUDIO_LATENCY_MS", "120")
	e2 := New(Config{Open: func() (Output, error) { return out, nil }, Cache: NewCache(newFakeMedia(0).decode, newFakeMedia(0).stat, 0)})
	defer e2.Close()
	e2.SetLatency(80 * time.Millisecond)
	if e2.Latency() != 120*time.Millisecond {
		t.Fatalf("the environment overrides the setting, got %v", e2.Latency())
	}
}

func TestSetOpenerSwitchesPlayer(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	a, b := &fakeOut{clk: clk}, &fakeOut{clk: clk}
	e := New(Config{Clock: clk, Open: func() (Output, error) { return a, nil }, Cache: NewCache(newFakeMedia(0).decode, newFakeMedia(0).stat, 0)})
	defer e.Close()
	if err := e.Step(); err != nil {
		t.Fatal(err)
	}
	e.SetOpener(func() (Output, error) { return b, nil })
	if !a.closed {
		t.Fatal("switching the player closes the old output at once")
	}
	if err := e.Step(); err != nil {
		t.Fatal(err)
	}
	if len(b.blocks) != 1 || len(a.blocks) != 1 {
		t.Fatalf("blocks: old %d new %d", len(a.blocks), len(b.blocks))
	}
	// A failing player: the engine falls back to the null output and reports the reason.
	e.SetOpener(func() (Output, error) { return nil, errors.New("kaputt") })
	if !b.closed {
		t.Fatal("old output closed")
	}
	if err := e.Step(); err != nil {
		t.Fatal(err)
	}
}

func TestSetOpenerWhileRunning(t *testing.T) {
	a, b := &fakeOut{}, &fakeOut{}
	e := New(Config{Open: func() (Output, error) { return slowOut{a}, nil }, Cache: NewCache(newFakeMedia(0).decode, newFakeMedia(0).stat, 0)})
	e.Start()
	time.Sleep(30 * time.Millisecond)
	e.SetOpener(func() (Output, error) { return slowOut{b}, nil })
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		n := len(b.blocks)
		b.mu.Unlock()
		if n > 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new player gets no audio")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.Close()
	if !b.closed {
		t.Fatal("Close closes the new output")
	}
}

func TestOpenerChoice(t *testing.T) {
	saved := lookPath
	defer func() { lookPath = saved }()
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if PlayerInstalled("pw-play") {
		t.Fatal("not installed")
	}
	_, err := Opener("paplay")()
	if err == nil || !strings.Contains(err.Error(), "paplay ist nicht installiert") {
		t.Fatalf("missing chosen player: %v", err)
	}
	if _, err := Opener("auto")(); err != ErrNoPlayer {
		t.Fatalf("auto without players: %v", err)
	}
	if _, err := Opener("winamp")(); err != ErrNoPlayer {
		t.Fatal("unknown names fall back to automatic")
	}
	if n := PlayerNames(); len(n) != 3 || n[0] != "pw-play" {
		t.Fatalf("players %v", n)
	}
	cat, err := saved("cat")
	if err != nil {
		t.Skip("no cat")
	}
	lookPath = func(name string) (string, error) {
		if name == "ffplay" {
			return cat, nil
		}
		return "", exec.ErrNotFound
	}
	out, err := Opener("ffplay")()
	if err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
}

func TestCacheSetBudgetEvicts(t *testing.T) {
	fm := newFakeMedia(SampleRate) // 1 s per file
	c := NewCache(fm.decode, fm.stat, 0)
	defer c.Close()
	for _, p := range []string{"/a", "/b", "/c"} {
		pcm, err := c.Get(p)
		if err != nil {
			t.Fatal(err)
		}
		<-pcm.Done()
	}
	per := c.Used() / 3
	c.SetBudget(per + per/2)
	if got := len(c.Keys()); got != 1 {
		t.Fatalf("%d files kept after shrinking to 1.5 files, want 1", got)
	}
	if c.Budget() != per+per/2 {
		t.Fatal("budget")
	}
	c.SetBudget(0)
	if c.Budget() != DefaultBudget {
		t.Fatal("0 restores the default")
	}
}
