package workgate

import (
	"context"
	"testing"
	"time"
)

// A fresh gate (no transition ever happened) lets background work start once the idle
// debounce has passed; nothing has to wake the waiter.
func TestAcquireOnFreshGateWakesByTimer(t *testing.T) {
	g := New(Config{IdleDebounce: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t0 := time.Now()
	rel, err := g.Acquire(ctx, PrioBackground)
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if d := time.Since(t0); d > 500*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}
