package main

import (
	"testing"
	"time"
)

// The work after the window has gone is bounded: mygo holds the single
// instance lock until the process exits, and a second launch waits for it
// without a word. Work that ends returns at once; work that hangs is left
// behind once the deadline passes, so main returns and the process exits.
func TestFinishWithinBoundsTheWorkAfterQuit(t *testing.T) {
	t.Parallel()
	ran := false
	start := time.Now()
	if !finishWithin(time.Minute, func() { ran = true }) || !ran {
		t.Fatal("work that ends was not waited for")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("work that ends took %v to return", d)
	}

	hang := make(chan struct{})
	defer close(hang)
	start = time.Now()
	if finishWithin(50*time.Millisecond, func() { <-hang }) {
		t.Fatal("work that hangs reported finished")
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 10*time.Second {
		t.Errorf("work that hangs returned after %v, want the 50ms deadline", d)
	}
}

// The deadline leaves the shutdown its own timeout to stop transfers
// cleanly, and does not keep a second launch waiting much longer than that.
func TestQuitDeadlineCoversTheShutdown(t *testing.T) {
	t.Parallel()
	if quitDeadline <= shutdownTimeout {
		t.Errorf("quitDeadline %v cuts the shutdown's %v short", quitDeadline, shutdownTimeout)
	}
	if quitDeadline > 15*time.Second {
		t.Errorf("quitDeadline %v keeps a second launch waiting too long", quitDeadline)
	}
}
