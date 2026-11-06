package main

import (
	"context"
	"testing"
	"time"
)

// Work that finishes after the window closed must not hang on a full queue.
func TestEnqueueAfterStopDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{ctx: ctx, pending: make(chan func(), 1)}
	a.enqueue(func() {}) // fills the queue
	cancel()

	done := make(chan struct{})
	go func() {
		a.enqueue(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocks on a full queue after the app stopped")
	}
}
