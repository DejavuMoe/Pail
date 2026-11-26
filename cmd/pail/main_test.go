package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// NUL and /dev/null are character devices but not terminals: a script that
// redirects stdin from them must not be prompted or shown a live progress line.
func TestIsTerminalRejectsNullDevice(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatalf("%s counted as a terminal", os.DevNull)
	}
}

// The first interrupt cancels the command, restores default signal handling
// and tells the user once; a second signal is never read.
func TestOnInterruptRestoresDefaultAndTellsOnce(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restored := make(chan struct{}, 2)
	var w bytes.Buffer
	done := make(chan struct{})
	go func() {
		onInterrupt(sigs, cancel, func() { restored <- struct{}{} }, &w, false)
		close(done)
	}()
	sigs <- os.Interrupt
	sigs <- os.Interrupt
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("onInterrupt did not return after the first signal")
	}
	if ctx.Err() == nil {
		t.Error("the context was not canceled")
	}
	if len(restored) != 1 {
		t.Errorf("default handling restored %d times, want 1", len(restored))
	}
	if n := strings.Count(w.String(), "Ctrl-C again"); n != 1 {
		t.Errorf("notice printed %d times: %q", n, w.String())
	}
}

func TestWarnShutdown(t *testing.T) {
	var errb bytes.Buffer
	c := &cli{io: IO{Stderr: &errb}}
	c.warnShutdown(nil)
	if errb.Len() != 0 {
		t.Fatalf("warned without an error: %q", errb.String())
	}
	c.warnShutdown(errors.New("shutdown: tasks still running"))
	for _, want := range []string{"cleanup did not finish", "multipart", "pail multipart ls", "pail multipart abort"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("warning lacks %q: %q", want, errb.String())
		}
	}
}
