package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
)

// watcher collects task events (Service.Subscribe) and waits for a task or a
// whole group to finish, printing one line per finished item.
type watcher struct {
	c     *cli
	mu    sync.Mutex
	recs  map[string]*rec
	wake  chan struct{}
	unsub func()
}

type rec struct {
	task    app.Task
	items   []app.ItemResult
	done    bool
	printed bool
}

// outcome aggregates the results of everything a command started.
type outcome struct {
	Tasks, Succeeded, Skipped, Failed, Unknown, Unprocessed int
	Bytes                                                   int64
	FailedTasks, PartialTasks, CanceledTasks                int
	Interrupted, TimedOut                                   bool
}

// watch must be called before the task or group is started: subscribers get no replay.
func (c *cli) watch() *watcher {
	w := &watcher{c: c, recs: map[string]*rec{}, wake: make(chan struct{}, 1)}
	w.unsub = c.svc.Subscribe(w.onEvent)
	return w
}

func (w *watcher) close() { w.unsub() }

func (w *watcher) onEvent(ev app.Event) {
	t := ev.Task
	w.mu.Lock()
	r := w.recs[t.ID]
	if r == nil {
		r = &rec{}
		w.recs[t.ID] = r
	}
	r.task = t
	if (t.Status.Finished() || ev.Removed) && !r.done {
		r.items = w.c.svc.TaskItems(t.ID)
		r.done = true
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func member(t app.Task, id string) bool { return t.ID == id || t.GroupID == id }

// wait blocks until every task of id (a task ID or a group ID) has finished.
// When ctx ends first the tasks are cancelled and given 10 s to wind down.
func (w *watcher) wait(ctx context.Context, id string) outcome {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	live := w.c.io.StderrTTY && !w.c.g.quiet
	defer func() {
		if live {
			fmt.Fprint(w.c.io.Stderr, "\r\x1b[K")
		}
	}()
	interrupted := false
	var deadline <-chan time.Time
	stop := ctx.Done()
	for {
		listed := w.scan(id)
		if w.drain(id, listed) {
			break
		}
		if live {
			w.liveLine(listed)
		}
		select {
		case <-w.wake:
		case <-tick.C:
		case <-stop:
			interrupted = true
			stop = nil
			_ = w.c.svc.CancelGroup(id)
			_ = w.c.svc.Cancel(id)
			t := time.NewTimer(10 * time.Second)
			defer t.Stop()
			deadline = t.C
		case <-deadline:
			w.drain(id, nil)
			return w.collect(id, true)
		}
	}
	return w.collect(id, interrupted)
}

// scan returns the members of id that the Service still lists.
func (w *watcher) scan(id string) []app.Task {
	out := []app.Task{}
	for _, t := range w.c.svc.Tasks() {
		if member(t, id) {
			out = append(out, t)
		}
	}
	return out
}

// drain prints finished, unprinted members and reports whether all are finished.
func (w *watcher) drain(id string, listed []app.Task) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	var fresh []*rec
	for _, r := range w.recs {
		if r.done && !r.printed && member(r.task, id) {
			r.printed = true
			fresh = append(fresh, r)
		}
	}
	sort.Slice(fresh, func(i, j int) bool {
		a, b := fresh[i].task, fresh[j].task
		if !a.Finished.Equal(b.Finished) {
			return a.Finished.Before(b.Finished)
		}
		return a.ID < b.ID
	})
	for _, r := range fresh {
		w.c.printRec(r)
	}
	if listed == nil {
		return false
	}
	for _, t := range listed {
		if r := w.recs[t.ID]; r == nil || !r.done {
			return false
		}
	}
	return true
}

func (w *watcher) liveLine(listed []app.Task) {
	var done, total, bd, bt int64
	var speed float64
	for _, t := range listed {
		done += int64(t.ItemsDone)
		total += int64(t.Items)
		bd += t.BytesDone
		bt += t.BytesTotal
		if t.Status == app.StatusRunning {
			speed += t.Speed
		}
	}
	fmt.Fprintf(w.c.io.Stderr, "\r\x1b[K%d/%d items, %s / %s, %s/s", done, total, humanSize(bd), humanSize(bt), humanSize(int64(speed)))
}

func (w *watcher) collect(id string, interrupted bool) outcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	o := outcome{Interrupted: interrupted}
	for _, r := range w.recs {
		if !member(r.task, id) {
			continue
		}
		o.Tasks++
		o.Bytes += r.task.BytesDone
		switch r.task.Status {
		case app.StatusFailed:
			o.FailedTasks++
		case app.StatusPartiallyFailed:
			o.PartialTasks++
		case app.StatusCanceled:
			o.CanceledTasks++
		}
		for _, it := range r.items {
			switch it.Status {
			case app.ItemSucceeded:
				o.Succeeded++
			case app.ItemSkipped:
				o.Skipped++
			case app.ItemFailed:
				o.Failed++
			case app.ItemUnknown:
				o.Unknown++
			case app.ItemUnprocessed:
				o.Unprocessed++
			}
		}
	}
	return o
}

// printRec prints the items of one finished task (w.mu is held).
func (c *cli) printRec(r *rec) {
	t := r.task
	if c.g.json {
		for _, it := range r.items {
			c.emit(map[string]any{"type": "item", "task": t.ID, "key": it.Key, "versionId": it.VersionID, "status": it.Status, "error": it.Err})
		}
		if t.Status != app.StatusSucceeded || t.Err != "" {
			c.emit(map[string]any{"type": "task", "task": t.ID, "title": t.Title, "status": t.Status.String(), "error": t.Err})
		}
		return
	}
	if c.g.quiet {
		return
	}
	for _, it := range r.items {
		label := map[string]string{
			app.ItemSucceeded: "done", app.ItemSkipped: "skipped", app.ItemFailed: "FAILED",
			app.ItemUnknown: "UNKNOWN", app.ItemUnprocessed: "unprocessed",
		}[it.Status]
		line := fmt.Sprintf("%-11s %s", label, it.Key)
		if it.VersionID != "" {
			line += " (version " + it.VersionID + ")"
		}
		if it.Err != "" {
			line += "  -- " + it.Err
		}
		fmt.Fprintln(c.io.Stderr, line)
	}
	bad := t.Status == app.StatusFailed || t.Status == app.StatusPartiallyFailed || t.Status == app.StatusCanceled
	if (bad && t.Err != "") || (bad && len(r.items) == 0) {
		fmt.Fprintf(c.io.Stderr, "%-11s %s  -- %s\n", strings.ToUpper(t.Status.String()), t.Title, t.Err)
	}
}

// finish prints the summary and maps the outcome to an exit error.
func (c *cli) finish(o outcome, what string, ctx context.Context) error {
	bad := o.FailedTasks+o.PartialTasks+o.CanceledTasks+o.Failed+o.Unknown+o.Unprocessed > 0
	code := exitOK
	switch {
	case o.Interrupted && !errors.Is(ctx.Err(), context.DeadlineExceeded):
		code = exitInterrupt
	case o.Interrupted:
		code = exitFailure
		o.TimedOut = true
	case !bad:
	case o.Succeeded == 0:
		code = exitFailure
	default:
		code = exitPartial
	}
	if c.g.json {
		c.emit(map[string]any{
			"type": "summary", "command": what, "ok": code == exitOK, "exitCode": code,
			"succeeded": o.Succeeded, "skipped": o.Skipped, "failed": o.Failed, "unknown": o.Unknown,
			"unprocessed": o.Unprocessed, "bytes": o.Bytes, "interrupted": o.Interrupted, "timedOut": o.TimedOut,
		})
	} else {
		c.infof("%s: %d succeeded, %d skipped, %d failed, %d unknown, %d unprocessed, %s", what,
			o.Succeeded, o.Skipped, o.Failed, o.Unknown, o.Unprocessed, humanSize(o.Bytes))
	}
	switch {
	case code == exitInterrupt:
		return &exitErr{code, "interrupted: running tasks were canceled"}
	case o.TimedOut:
		return &exitErr{code, "timed out: running tasks were canceled"}
	case code != exitOK:
		return &exitErr{code, ""}
	}
	return nil
}
