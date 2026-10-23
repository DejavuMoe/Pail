package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// Only counts and fixed labels cross into the sampler; never account names,
// keys, endpoints, errors from servers, or credentials.
type memoryState struct {
	Accounts      int    `json:"accounts"`
	Buckets       int    `json:"buckets"`
	Objects       int    `json:"objects"`
	Rows          int    `json:"rows"`
	Tasks         int    `json:"tasks"`
	ActiveTasks   int    `json:"active_tasks"`
	Loading       bool   `json:"loading"`
	SecretBackend string `json:"secret_backend,omitempty"`
}

type memoryMark struct {
	Stage string      `json:"stage"`
	AtMS  int64       `json:"event_ms"`
	State memoryState `json:"state"`
}

type processMemory struct {
	ResidentBytes  uint64 `json:"resident_bytes,omitempty"`
	PrivateBytes   uint64 `json:"private_commit_bytes,omitempty"`
	FootprintBytes uint64 `json:"footprint_bytes,omitempty"`
	CPUTimeNS      uint64 `json:"cpu_time_ns"`
	Threads        uint32 `json:"threads,omitempty"`
	Error          string `json:"error,omitempty"`
}

type memorySample struct {
	memoryMark
	SampleMS     int64         `json:"sample_ms"`
	Periodic     bool          `json:"periodic"`
	Dropped      uint64        `json:"dropped_marks"`
	HeapAlloc    uint64        `json:"heap_alloc_bytes"`
	HeapInuse    uint64        `json:"heap_inuse_bytes"`
	HeapIdle     uint64        `json:"heap_idle_bytes"`
	HeapReleased uint64        `json:"heap_released_bytes"`
	GoManaged    uint64        `json:"go_managed_bytes"`
	TotalAlloc   uint64        `json:"total_alloc_bytes"`
	StackInuse   uint64        `json:"stack_inuse_bytes"`
	GCCount      uint32        `json:"gc_count"`
	Goroutines   int           `json:"goroutines"`
	Process      processMemory `json:"process"`
}

type memorySampler struct {
	start   time.Time
	marks   chan memoryMark
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
	err     error // read only after done closes
}

// A fresh directory prevents overwriting a previous capture or a user file.
// The worker samples without waking the UI and never forces GC.
func startMemorySampler(dir string, demo bool, interval time.Duration) (*memorySampler, error) {
	if dir == "" {
		return nil, nil
	}
	if interval <= 0 {
		return nil, errors.New("memory sampling interval must be positive")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "memory.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(map[string]any{"schema": 1, "pid": os.Getpid(), "version": version,
		"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "demo": demo,
		"started": time.Now().UTC(), "interval_ms": interval.Milliseconds()}); err != nil {
		f.Close()
		return nil, err
	}
	m := &memorySampler{start: time.Now(), marks: make(chan memoryMark, 64), stop: make(chan struct{}), done: make(chan struct{})}
	go m.run(f, enc, dir, interval)
	return m, nil
}

func (m *memorySampler) mark(stage string, state memoryState) {
	if m == nil {
		return
	}
	select {
	case <-m.stop:
		return
	default:
	}
	select {
	case m.marks <- memoryMark{stage, time.Since(m.start).Milliseconds(), state}:
	default:
		m.dropped.Add(1) // diagnostics must never block the UI
	}
}

func (m *memorySampler) close() error {
	if m == nil {
		return nil
	}
	m.once.Do(func() { close(m.stop) })
	<-m.done
	return m.err
}

func (m *memorySampler) run(f *os.File, enc *json.Encoder, dir string, interval time.Duration) {
	defer close(m.done)
	defer func() { m.err = errors.Join(m.err, f.Close()) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := memoryMark{Stage: "startup"}
	write := func(periodic bool) {
		if m.err != nil {
			return
		}
		var s runtime.MemStats
		runtime.ReadMemStats(&s)
		m.err = enc.Encode(memorySample{memoryMark: last, SampleMS: time.Since(m.start).Milliseconds(),
			Periodic: periodic, Dropped: m.dropped.Load(), HeapAlloc: s.HeapAlloc, HeapInuse: s.HeapInuse,
			HeapIdle: s.HeapIdle, HeapReleased: s.HeapReleased, GoManaged: s.Sys - s.HeapReleased,
			TotalAlloc: s.TotalAlloc, StackInuse: s.StackInuse, GCCount: s.NumGC,
			Goroutines: runtime.NumGoroutine(), Process: readProcessMemory()})
	}
	write(false)
	for {
		select {
		case last = <-m.marks:
			write(false)
		case <-ticker.C:
			write(true)
		case <-m.stop:
			for {
				select {
				case last = <-m.marks:
					write(false)
				default:
					write(true)
					// This is the runtime's sampled heap, as of its last GC.
					p, err := os.OpenFile(filepath.Join(dir, "heap.pprof"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err == nil {
						err = errors.Join(pprof.Lookup("heap").WriteTo(p, 0), p.Close())
					}
					m.err = errors.Join(m.err, err)
					return
				}
			}
		}
	}
}

func (a *app) markMemory(stage string) {
	if a.opts.memory == nil {
		return
	}
	counts := countTasks(a.tasks)
	a.opts.memory.mark(stage, memoryState{Accounts: len(a.accounts), Buckets: len(a.buckets),
		Objects: len(a.raw), Rows: len(a.rows), Tasks: len(a.tasks), ActiveTasks: counts[0],
		Loading: a.loading || a.bucketsLoad, SecretBackend: a.svc.SecretBackend()})
}
