package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func memorySamples(t *testing.T, dir string) []memorySample {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	var samples []memorySample
	for {
		var s memorySample
		if err := d.Decode(&s); err == io.EOF {
			return samples
		} else if err != nil {
			t.Fatal(err)
		}
		if s.Stage != "" {
			samples = append(samples, s)
		}
	}
}

func TestMemorySampler(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	m, err := startMemorySampler(dir, true, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.close() })
	m.mark("listing-ready", memoryState{Objects: 42})
	deadline := time.Now().Add(5 * time.Second)
	for {
		samples := memorySamples(t, dir)
		if len(samples) > 0 && samples[len(samples)-1].Periodic && samples[len(samples)-1].State.Objects == 42 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no periodic sample")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
	m.mark("after-close", memoryState{})
	samples := memorySamples(t, dir)
	for _, s := range samples {
		if s.Stage == "after-close" || s.HeapAlloc == 0 || s.GoManaged < s.HeapAlloc || s.Goroutines < 1 {
			t.Fatalf("bad sample: %+v", s)
		}
		if s.Process.Error != "" || s.Process.ResidentBytes == 0 || s.Process.Threads == 0 {
			t.Fatalf("native process sampling failed: %+v", s.Process)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "heap.pprof")); err != nil || st.Size() == 0 {
		t.Fatalf("missing profile: %v", err)
	}
	if _, err := startMemorySampler(dir, true, time.Second); err == nil {
		t.Fatal("overwrote an existing capture")
	}
	if m, err := startMemorySampler("", false, 0); m != nil || err != nil {
		t.Fatal("disabled sampler did work")
	}
	if _, err := startMemorySampler(filepath.Join(t.TempDir(), "bad"), false, 0); err == nil {
		t.Fatal("accepted invalid interval")
	}
}

func TestMemoryMarksNeverBlock(t *testing.T) {
	m := &memorySampler{start: time.Now(), marks: make(chan memoryMark, 1), stop: make(chan struct{})}
	for i := 0; i < 100; i++ {
		m.mark("listing-ready", memoryState{})
	}
	if got := m.dropped.Load(); got != 99 {
		t.Fatalf("dropped=%d", got)
	}
}

func TestMemoryListingStages(t *testing.T) {
	h := newHarness(t, "light")
	dir := filepath.Join(t.TempDir(), "capture")
	m, err := startMemorySampler(dir, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.close() })
	h.a.opts.memory = m
	h.a.reload()
	h.settle()
	want := len(h.a.raw)
	h.a.openPrefix("private-object-name-not-in-diagnostics/")
	h.settle()
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
	seenReady, seenEmpty := false, false
	for _, s := range memorySamples(t, dir) {
		if s.Stage == "listing-ready" && !s.State.Loading {
			seenReady = seenReady || s.State.Objects == want && want > 0
			seenEmpty = seenEmpty || s.State.Objects == 0
		}
	}
	if !seenReady || !seenEmpty {
		t.Fatalf("ready=%v empty=%v", seenReady, seenEmpty)
	}
	b, err := os.ReadFile(filepath.Join(dir, "memory.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-object-name", bkt, demoAccountName, "demo-secret", h.env.Srv.URL} {
		if strings.Contains(string(b), private) {
			t.Fatalf("diagnostics contain %q", private)
		}
	}
}
