# Desktop memory sampling

Run the desktop with `--memory-profile` and a new output directory to record memory during normal use. The parent directory must exist. An existing output directory is rejected.

```sh
go run ./cmd/pail-desktop --demo --memory-profile build/memory-demo
go run ./cmd/pail-desktop --memory-profile build/memory-normal
```

The second command uses the normal account configuration. For an isolated fixture, also supply `--config-dir` with its configuration directory. Keep window size, display scaling, theme, build flags and the operations being compared the same. Compare the desktop process itself, not the `go run` compiler or launcher.

## Capture sequence

1. Open an empty or small directory and leave the window untouched for 30 seconds.
2. Open the directory used in the report and wait for loading to finish.
3. Return to the empty directory. Leave it untouched for at least 120 seconds.
4. Repeat steps 2 and 3 to check whether memory keeps increasing.
5. If previewing or transferring triggers the increase, repeat that operation separately.
6. Close the window normally to finish the capture.

The sampler writes `memory.jsonl` at startup, at application stages and every five seconds. It records service initialization, bucket loading, directory clearing/loading, preview completion, vault unlock and task state changes. Bucket loading includes credential lookup and client initialization; it does not separately time those operations. Failed requests have a fixed failure label, without the server's error message.

Each stage carries counts captured on the UI thread. `event_ms` is when that stage was marked; `sample_ms` is when the background worker measured memory. Short adjacent stages can share the same allocation peak. Periodic rows repeat the last stage and its counts; they do not assert that the application is idle. The worker does not wake the UI. A full diagnostic queue drops marks and increments `dropped_marks` instead of blocking the application.

## Metrics and files

The first JSON line identifies the schema, application version, Go version, platform, process ID and whether demo mode was used. Later lines contain byte counts:

| Field | Meaning |
| --- | --- |
| `heap_alloc_bytes` | Go heap objects, including garbage not yet collected |
| `heap_inuse_bytes` | Heap spans in use, including unused space within those spans |
| `heap_idle_bytes` | Idle heap spans, including spans returned to the OS |
| `heap_released_bytes` | Idle heap memory returned to the OS |
| `go_managed_bytes` | Go runtime memory, `Sys - HeapReleased`; excludes native allocations |
| `total_alloc_bytes` | Cumulative Go heap allocations; compare deltas |
| `stack_inuse_bytes` | Go stack spans in use |
| `gc_count` | Completed Go garbage collections |
| `goroutines` | Current goroutines, including the sampler |
| `process.resident_bytes` | Windows working set; Linux and macOS resident size |
| `process.private_commit_bytes` | Windows private commit, not private working set |
| `process.footprint_bytes` | macOS physical footprint from `proc_pid_rusage` |
| `process.cpu_time_ns` | Cumulative process user and kernel CPU time |
| `process.threads` | Current OS thread count |

Unsupported platform-specific fields are omitted. Native sampling failures appear in `process.error`. Do not subtract Go-managed memory from resident size and label the difference GPU memory: these are different accounting measures. macOS footprint, Windows private commit and resident size are not interchangeable.

Normal shutdown also writes `heap.pprof`. This is Go's sampled allocation profile as of its last GC, not a forced collection or an exact shutdown heap census. Inspect it with:

```sh
go tool pprof -top build/memory-normal/heap.pprof
go tool pprof -top -alloc_space build/memory-normal/heap.pprof
```

Sampling is off unless the flag is supplied. It adds one worker and periodic runtime/native measurements. It does not change GC settings, memory limits, concurrency or thread limits, and does not force a collection. The JSON contains fixed stage labels and counts, not account names, endpoints, object keys or credentials. The heap profile contains allocation stacks and build/source information; it does not dump object contents. Files remain local, with no profiling HTTP listener.

For native graphics allocations on macOS, use Instruments Allocations/VM Tracker or `vmmap` alongside the stage timestamps. The macOS sampling implementation requires native runtime validation; cross-compilation alone does not validate the system calls.
