package audio

import (
	"os/exec"
	"testing"
	"time"
)

func TestOpenPlayerNone(t *testing.T) {
	saved := lookPath
	defer func() { lookPath = saved }()
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, err := OpenPlayer(); err != ErrNoPlayer {
		t.Fatalf("got %v", err)
	}
}

// TestPlayerProcess streams into a stand-in player (cat) and closes it cleanly.
func TestPlayerProcess(t *testing.T) {
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("no cat")
	}
	out, err := startPlayer(cat, player{name: "cat", args: []string{}, latency: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	// One page of pipe adds ~21 ms on Linux.
	if l := out.Latency(); l < 10*time.Millisecond || l > 10*time.Millisecond+400*time.Millisecond {
		t.Fatalf("latency %v", l)
	}
	t.Logf("latency estimate with the pipe: %v", out.Latency())
	block := make([]byte, BlockFrames*bytesPerFrame)
	for range 5 {
		if _, err := out.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("close must not hang")
	}
	if _, err := out.Write(block); err == nil {
		t.Fatal("write after close")
	}
}

func TestNullOutputPaces(t *testing.T) {
	o := newNullOutput()
	start := time.Now()
	block := make([]byte, BlockFrames*bytesPerFrame)
	for range 5 {
		_, _ = o.Write(block)
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("5 blocks took %v, want ~50 ms", d)
	}
	_ = o.Close()
	if _, err := o.Write(block); err == nil {
		t.Fatal("closed")
	}
}
