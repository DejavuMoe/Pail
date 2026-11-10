package main

import (
	"os"
	"testing"
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
