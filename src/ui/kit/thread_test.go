package kit

import (
	"testing"
	"time"
)

func TestUIQueueRunsOnOwner(t *testing.T) {
	q := UseUIQueue(t)
	ran := false
	Do(func() { ran = true })
	if !ran {
		t.Fatal("Do on the owner goroutine must run inline")
	}

	got := 0
	go Do(func() { got++ })
	if !q.Until(func() bool { return got == 1 }, time.Second) {
		t.Fatal("queued callback did not run")
	}

	done := make(chan struct{})
	go func() {
		DoAndWait(func() { got++ })
		close(done)
	}()
	if !q.Until(func() bool { return got == 2 }, time.Second) {
		t.Fatal("DoAndWait callback did not run")
	}
	<-done
}
