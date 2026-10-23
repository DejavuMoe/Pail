package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

// ---- helpers ------------------------------------------------------------------------------

type sink struct {
	mu  sync.Mutex
	evs []Event
}

func (k *sink) fn(e Event) {
	k.mu.Lock()
	k.evs = append(k.evs, e)
	k.mu.Unlock()
}

func (k *sink) forTask(id string) []Event {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []Event
	for _, e := range k.evs {
		if e.Task.ID == id {
			out = append(out, e)
		}
	}
	return out
}

func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func okWork(context.Context, *run) error { return nil }

func wait(t testing.TB, s *Service, id string) Task {
	t.Helper()
	tk, err := s.waitTask(testCtx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func taskByID(s *Service, id string) (Task, bool) {
	for _, tk := range s.Tasks() {
		if tk.ID == id {
			return tk, true
		}
	}
	return Task{}, false
}

// gated returns a Work that reports its start on started and then waits for gate or ctx.
func gated(name string, started chan<- string, gate <-chan struct{}) func(context.Context, *run) error {
	return func(ctx context.Context, r *run) error {
		started <- name
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func setLimits(t testing.TB, s *Service, u, d, o int) {
	t.Helper()
	st := s.Settings()
	st.Limits = Limits{u, d, o}
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
}

// ---- status rules --------------------------------------------------------------------------

func TestTaskStatusRules(t *testing.T) {
	s := newTestService(t, nil)
	cases := []struct {
		name string
		work func(context.Context, *run) error
		want Status
	}{
		{"success", okWork, StatusSucceeded},
		{"success with skipped", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: ItemSucceeded})
			r.Item(ItemResult{Key: "b", Status: ItemSkipped})
			return nil
		}, StatusSucceeded},
		{"work error", func(context.Context, *run) error { return errors.New("boom") }, StatusFailed},
		{"failed item", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: ItemSucceeded})
			r.Item(ItemResult{Key: "b", Status: ItemFailed, Err: "denied"})
			return nil
		}, StatusPartiallyFailed},
		{"all items failed is still partial", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "b", Status: ItemFailed})
			return nil
		}, StatusPartiallyFailed},
		{"unknown item", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: ItemUnknown})
			return nil
		}, StatusPartiallyFailed},
		{"unprocessed item", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: ItemUnprocessed})
			return nil
		}, StatusPartiallyFailed},
		{"unrecognised status counts as failed", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: "weird"})
			return nil
		}, StatusPartiallyFailed},
		{"panic", func(context.Context, *run) error { panic("kaboom") }, StatusFailed},
		{"error after items", func(_ context.Context, r *run) error {
			r.Item(ItemResult{Key: "a", Status: ItemSucceeded})
			return errors.New("listing failed")
		}, StatusFailed},
		{"cancelled by own ctx", func(ctx context.Context, r *run) error {
			<-ctx.Done()
			return ctx.Err()
		}, StatusCanceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := s.submit(taskSpec{Kind: KindOther, Title: c.name, Work: c.work})
			if c.name == "cancelled by own ctx" {
				eventually(t, "running", func() bool { tk, _ := taskByID(s, id); return tk.Status == StatusRunning })
				if err := s.Cancel(id); err != nil {
					t.Fatal(err)
				}
			}
			tk := wait(t, s, id)
			if tk.Status != c.want {
				t.Fatalf("status %v (err %q), want %v", tk.Status, tk.Err, c.want)
			}
			if tk.Finished.IsZero() || tk.Created.IsZero() || (tk.Started.IsZero() && c.want != StatusCanceled) {
				t.Fatalf("timestamps %+v", tk)
			}
			if c.want != StatusSucceeded && tk.Err == "" {
				t.Fatal("non-success needs an Err message")
			}
		})
	}
}

func TestTaskCountersAndItems(t *testing.T) {
	s := newTestService(t, nil)
	id := s.submit(taskSpec{Kind: KindOther, Group: "grp", Title: "bulk", Detail: "d", Work: func(_ context.Context, r *run) error {
		r.SetTotal(1000, 5)
		r.SetDetail("working")
		r.AddBytes(100)
		r.AddBytes(-5) // ignored
		r.SetBytes(50) // never moves backwards
		r.SetBytes(300)
		r.Item(ItemResult{Key: "ok", Status: ItemSucceeded})
		r.Item(ItemResult{Key: "skip", Status: ItemSkipped})
		r.Item(ItemResult{Key: "bad", Status: ItemFailed, Err: "x"})
		r.Item(ItemResult{Key: "unk", Status: ItemUnknown})
		r.Item(ItemResult{Key: "todo", Status: ItemUnprocessed})
		return nil
	}})
	tk := wait(t, s, id)
	if tk.BytesTotal != 1000 || tk.BytesDone != 300 || tk.Items != 5 || tk.ItemsDone != 4 || tk.ItemsFailed != 2 ||
		tk.ItemsSkipped != 1 || tk.ItemsUnknown != 1 || tk.ItemsUnprocessed != 1 ||
		tk.Detail != "working" || tk.GroupID != "grp" || tk.Kind != KindOther || tk.Title != "bulk" {
		t.Fatalf("%+v", tk)
	}
	items := s.TaskItems(id)
	if len(items) != 5 || items[2].Key != "bad" || items[2].Err != "x" {
		t.Fatalf("items %+v", items)
	}
	// Mutating the returned slice must not affect the queue.
	items[0].Key = "changed"
	if s.TaskItems(id)[0].Key != "ok" {
		t.Fatal("TaskItems leaks internal slice")
	}
	if s.TaskItems("nope") != nil {
		t.Fatal("unknown task")
	}
	// Items beyond a declared total grow the total.
	id2 := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
		r.SetTotal(0, 1)
		r.Item(ItemResult{Status: ItemSucceeded})
		r.Item(ItemResult{Status: ItemSucceeded})
		r.AddTotal(10, 3)
		return nil
	}})
	if tk := wait(t, s, id2); tk.Items != 5 || tk.BytesTotal != 10 {
		t.Fatalf("%+v", tk)
	}
}

func TestSucceededItemsAreBoundedButCountersExact(t *testing.T) {
	s := newTestService(t, nil)
	n := maxSucceededItemsKept + 500
	id := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
		for i := 0; i < n; i++ {
			r.Item(ItemResult{Key: fmt.Sprint(i), Status: ItemSucceeded})
		}
		for i := 0; i < n; i++ {
			r.Item(ItemResult{Key: fmt.Sprint(i), Status: ItemSkipped})
		}
		r.Item(ItemResult{Key: "bad", Status: ItemFailed})
		return nil
	}})
	tk := wait(t, s, id)
	if tk.ItemsDone != 2*n+1 || tk.ItemsFailed != 1 || tk.ItemsSkipped != n {
		t.Fatalf("%+v", tk)
	}
	items := s.TaskItems(id)
	if len(items) != maxSucceededItemsKept+1 || items[len(items)-1].Key != "bad" {
		t.Fatalf("kept %d items", len(items))
	}
}

func TestGroupResultSurvivesEvictionAndActiveClear(t *testing.T) {
	s := newTestService(t, nil)
	s.q.mu.Lock()
	s.q.maxFinished = 2
	s.q.mu.Unlock()
	group := s.newGroupID()
	gate, started := make(chan struct{}), make(chan string, 1)
	scan := s.submit(taskSpec{Kind: KindOther, Group: group, Work: gated("scan", started, gate)})
	<-started
	failed := s.submit(taskSpec{Kind: KindUpload, Group: group, Work: func(context.Context, *run) error { return errors.New("failed") }})
	wait(t, s, failed)
	for _, status := range []string{ItemSucceeded, ItemSkipped, ItemSucceeded} {
		id := s.submit(taskSpec{Kind: KindUpload, Group: group, Work: func(_ context.Context, r *run) error {
			r.Item(ItemResult{Status: status})
			return nil
		}})
		wait(t, s, id)
	}
	if _, ok := taskByID(s, failed); ok {
		t.Fatal("failed task was not evicted")
	}
	check := func(pending int) {
		t.Helper()
		got, ok := s.TaskGroup(group)
		want := TaskGroupResult{Pending: pending, Succeeded: 2, Failed: 1}
		if !ok || got != want {
			t.Fatalf("group=%+v exists=%v want=%+v", got, ok, want)
		}
	}
	check(1)
	s.ClearFinished()
	check(1)
	close(gate)
	wait(t, s, scan)
	check(0)
	s.ClearFinished()
	if _, ok := s.TaskGroup(group); ok {
		t.Fatal("group outlived its last retained member")
	}
}

// ---- ordering and limits --------------------------------------------------------------------

func TestFIFOWithinKind(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	var mu sync.Mutex
	var order []string
	gate := make(chan struct{})
	first := s.submit(taskSpec{Kind: KindDownload, Work: func(ctx context.Context, _ *run) error { <-gate; return nil }})
	var ids []string
	for i := 0; i < 20; i++ {
		name := fmt.Sprint(i)
		ids = append(ids, s.submit(taskSpec{Kind: KindDownload, Title: name, Work: func(context.Context, *run) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}}))
	}
	close(gate)
	wait(t, s, first)
	for _, id := range ids {
		wait(t, s, id)
	}
	for i, name := range order {
		if name != fmt.Sprint(i) {
			t.Fatalf("order %v", order)
		}
	}
	// Tasks() lists in submission order.
	tasks := s.Tasks()
	if len(tasks) != 21 || tasks[0].ID != first || tasks[20].ID != ids[19] {
		t.Fatal("Tasks() order")
	}
}

func TestQueuesAreIndependentAndLimitsLive(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 2, 1, 1)
	started := make(chan string, 32)
	gates := map[string]chan struct{}{}
	submit := func(kind Kind, name string) string {
		g := make(chan struct{})
		gates[name] = g
		return s.submit(taskSpec{Kind: kind, Title: name, Work: gated(name, started, g)})
	}
	u1, u2, u3 := submit(KindUpload, "u1"), submit(KindUpload, "u2"), submit(KindUpload, "u3")
	d1, d2 := submit(KindDownload, "d1"), submit(KindDownload, "d2")
	o1 := submit(KindOther, "o1")
	got := map[string]bool{}
	for i := 0; i < 4; i++ {
		select {
		case n := <-started:
			got[n] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only %v started", got)
		}
	}
	if !(got["u1"] && got["u2"] && got["d1"] && got["o1"]) {
		t.Fatalf("started %v", got)
	}
	status := func(id string) Status { tk, _ := taskByID(s, id); return tk.Status }
	if status(u3) != StatusQueued || status(d2) != StatusQueued {
		t.Fatal("extra tasks must wait in their own queue")
	}

	// Lowering the upload limit never interrupts the two running uploads, and
	// the queued one waits until fewer than 1 is running.
	setLimits(t, s, 1, 1, 1)
	close(gates["u1"])
	wait(t, s, u1)
	time.Sleep(50 * time.Millisecond)
	if status(u2) != StatusRunning || status(u3) != StatusQueued {
		t.Fatalf("u2=%v u3=%v", status(u2), status(u3))
	}
	// Raising it starts the queued task at once.
	setLimits(t, s, 3, 1, 1)
	select {
	case n := <-started:
		if n != "u3" {
			t.Fatalf("started %s", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("raising the limit did not dispatch u3")
	}
	for _, g := range []string{"u2", "u3", "d1", "d2", "o1"} {
		close(gates[g])
	}
	for _, id := range []string{u2, u3, d1, d2, o1} {
		wait(t, s, id)
	}
}

// ---- cancel ----------------------------------------------------------------------------------------

func TestCancelQueuedRemovesImmediately(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	k := &sink{}
	s.Subscribe(k.fn)
	started := make(chan string, 4)
	gate := make(chan struct{})
	running := s.submit(taskSpec{Kind: KindUpload, Work: gated("run", started, gate)})
	<-started
	var ran atomic.Bool
	queued := s.submit(taskSpec{Kind: KindUpload, Title: "q", Work: func(context.Context, *run) error { ran.Store(true); return nil }})
	if err := s.Cancel(queued); err != nil {
		t.Fatal(err)
	}
	tk, _ := taskByID(s, queued)
	if tk.Status != StatusCanceled || tk.Finished.IsZero() || !tk.Started.IsZero() {
		t.Fatalf("%+v", tk)
	}
	// Cancelling again, or a finished task, is a harmless no-op.
	if err := s.Cancel(queued); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel("nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	close(gate)
	wait(t, s, running)
	time.Sleep(50 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a cancelled queued task must never run")
	}
	eventually(t, "final event of cancelled task", func() bool {
		evs := k.forTask(queued)
		return len(evs) > 0 && evs[len(evs)-1].Task.Status == StatusCanceled
	})
}

func TestCancelRunning(t *testing.T) {
	s := newTestService(t, nil)
	started := make(chan string, 1)
	id := s.submit(taskSpec{Kind: KindDownload, Work: func(ctx context.Context, r *run) error {
		r.Item(ItemResult{Key: "a", Status: ItemSucceeded})
		started <- "go"
		<-ctx.Done()
		r.Item(ItemResult{Key: "b", Status: ItemUnprocessed})
		return ctx.Err()
	}})
	<-started
	if err := s.Cancel(id); err != nil {
		t.Fatal(err)
	}
	tk := wait(t, s, id)
	if tk.Status != StatusCanceled {
		t.Fatalf("%+v", tk)
	}
	if err := s.Cancel(id); err != nil { // finished: no-op
		t.Fatal(err)
	}
}

func TestCancelRaces(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 4, 4, 4)
	const n = 300
	var executed sync.Map
	ids := make([]string, n)
	for i := range ids {
		i := i
		ids[i] = s.submit(taskSpec{Kind: Kind(i % 3), Work: func(ctx context.Context, r *run) error {
			executed.Store(i, true)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i%3) * time.Millisecond):
				return nil
			}
		}})
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < n; i += 4 {
				if i%2 == 0 {
					s.Cancel(ids[i])
				}
				s.Tasks()
			}
		}(g)
	}
	wg.Wait()
	for i, id := range ids {
		tk := wait(t, s, id)
		if !tk.Status.Finished() {
			t.Fatalf("task %d not final: %v", i, tk.Status)
		}
		if _, ran := executed.Load(i); !ran && tk.Status != StatusCanceled {
			t.Fatalf("task %d never ran but is %v", i, tk.Status)
		}
		if tk.Status == StatusCanceled && tk.Started.IsZero() {
			if _, ran := executed.Load(i); ran {
				t.Fatalf("task %d ran although it was cancelled while queued", i)
			}
		}
	}
	for _, tk := range s.Tasks() {
		if tk.Status == StatusQueued || tk.Status == StatusRunning {
			t.Fatalf("leftover %+v", tk)
		}
	}
}

func TestCancelGroup(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	started := make(chan string, 8)
	gate := make(chan struct{})
	g := s.newGroupID()
	a := s.submit(taskSpec{Kind: KindUpload, Group: g, Work: gated("a", started, gate)})
	<-started
	b := s.submit(taskSpec{Kind: KindUpload, Group: g, Work: gated("b", started, gate)})
	other := s.submit(taskSpec{Kind: KindUpload, Group: s.newGroupID(), Work: gated("other", started, gate)})
	if err := s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, s, a); tk.Status != StatusCanceled {
		t.Fatalf("a: %v", tk.Status)
	}
	if tk := wait(t, s, b); tk.Status != StatusCanceled || !tk.Started.IsZero() {
		t.Fatalf("b: %+v", tk)
	}
	// A task of the group that is submitted afterwards (a folder scan still
	// expanding) is cancelled on arrival.
	late := s.submit(taskSpec{Kind: KindUpload, Group: g, Work: gated("late", started, gate)})
	if tk, _ := taskByID(s, late); tk.Status != StatusCanceled {
		t.Fatalf("late: %+v", tk)
	}
	// The other group is unaffected.
	select {
	case n := <-started:
		if n != "other" {
			t.Fatalf("started %s", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("other group's task did not start")
	}
	close(gate)
	if tk := wait(t, s, other); tk.Status != StatusSucceeded {
		t.Fatalf("other: %v", tk.Status)
	}
	if err := s.CancelGroup("unknown"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	if err := s.CancelGroup(""); !IsKind(err, ErrInvalid) {
		t.Fatalf("empty group: %v", err)
	}
}

// ---- retry ---------------------------------------------------------------------------------------------

func TestRetryItemsOnlyFailedAndUnprocessed(t *testing.T) {
	s := newTestService(t, nil)
	var retried atomic.Value
	var spec taskSpec
	spec = taskSpec{Kind: KindOther, Group: "G", Title: "Delete 4", Work: func(_ context.Context, r *run) error {
		r.Item(ItemResult{Key: "ok", Status: ItemSucceeded})
		r.Item(ItemResult{Key: "bad", Status: ItemFailed, Err: "denied"})
		r.Item(ItemResult{Key: "unk", Status: ItemUnknown})
		r.Item(ItemResult{Key: "todo", Status: ItemUnprocessed})
		r.Item(ItemResult{Key: "skip", Status: ItemSkipped})
		return nil
	}, Retry: func(failed []ItemResult) (taskSpec, bool) {
		retried.Store(failed)
		return taskSpec{Work: func(_ context.Context, r *run) error {
			for _, it := range failed {
				r.Item(ItemResult{Key: it.Key, Status: ItemSucceeded})
			}
			return nil
		}}, true
	}}
	id := s.submit(spec)
	if tk := wait(t, s, id); tk.Status != StatusPartiallyFailed {
		t.Fatal(tk.Status)
	}
	nid, err := s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := retried.Load().([]ItemResult)
	if len(got) != 2 || got[0].Key != "bad" || got[1].Key != "todo" {
		t.Fatalf("Retry received %+v; unknown/succeeded/skipped must not be replayed", got)
	}
	tk := wait(t, s, nid)
	if tk.Status != StatusSucceeded || tk.GroupID != "G" || tk.Title != "Delete 4" || tk.Kind != KindOther {
		t.Fatalf("retry task %+v (group/title/kind inherited)", tk)
	}
}

func TestRetryRules(t *testing.T) {
	s := newTestService(t, nil)
	// Per-file task without items: same spec runs again.
	var runs atomic.Int32
	id := s.submit(taskSpec{Kind: KindDownload, Title: "file", Work: func(context.Context, *run) error {
		if runs.Add(1) == 1 {
			return errors.New("network down")
		}
		return nil
	}})
	if tk := wait(t, s, id); tk.Status != StatusFailed {
		t.Fatal(tk.Status)
	}
	nid, err := s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, s, nid); tk.Status != StatusSucceeded || runs.Load() != 2 {
		t.Fatalf("%+v runs=%d", tk, runs.Load())
	}
	// Succeeded tasks cannot be retried; unknown ids are NotFound.
	if _, err := s.Retry(nid); !IsKind(err, ErrInvalid) {
		t.Fatalf("retry succeeded: %v", err)
	}
	if _, err := s.Retry("nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// Only unknown results: nothing replayed.
	unk := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
		r.Item(ItemResult{Key: "k", Status: ItemUnknown})
		return nil
	}, Retry: func([]ItemResult) (taskSpec, bool) { t.Error("Retry must not be called"); return taskSpec{}, false }})
	wait(t, s, unk)
	if _, err := s.Retry(unk); !IsKind(err, ErrInvalid) {
		t.Fatalf("unknown-only retry: %v", err)
	}
	// Items but no Retry function.
	noRetry := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
		r.Item(ItemResult{Key: "k", Status: ItemFailed})
		return nil
	}})
	wait(t, s, noRetry)
	if _, err := s.Retry(noRetry); !IsKind(err, ErrInvalid) {
		t.Fatalf("no Retry func: %v", err)
	}
	// Retry func declines.
	decline := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
		r.Item(ItemResult{Key: "k", Status: ItemFailed})
		return nil
	}, Retry: func([]ItemResult) (taskSpec, bool) { return taskSpec{}, false }})
	wait(t, s, decline)
	if _, err := s.Retry(decline); !IsKind(err, ErrInvalid) {
		t.Fatalf("declined: %v", err)
	}
}

func TestRetryAfterCancelGroupLiftsTheCancellation(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	g := s.newGroupID()
	started := make(chan string, 2)
	gate := make(chan struct{})
	a := s.submit(taskSpec{Kind: KindDownload, Group: g, Work: gated("a", started, gate)})
	<-started
	if err := s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	wait(t, s, a)
	nid, err := s.Retry(a)
	if err != nil {
		t.Fatal(err)
	}
	<-started // the retried task actually runs
	close(gate)
	if tk := wait(t, s, nid); tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
}

// ---- retention, clear -------------------------------------------------------------------------------------

//go:noinline
func prepareEvictedTask(s *Service) weak.Pointer[task] {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	s.q.maxFinished = 1
	s.q.finished = make([]*task, 0, 8)
	var oldest weak.Pointer[task]
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		tk := &task{ctx: ctx, cancel: cancel, done: make(chan struct{}), pub: Task{ID: fmt.Sprint(i), Status: StatusSucceeded}}
		if i == 0 {
			oldest = weak.Make(tk)
		}
		s.q.tasks[tk.pub.ID] = tk
		s.completeLocked(tk)
	}
	return oldest
}

func TestRetentionReleasesEvictedTask(t *testing.T) {
	s := newTestService(t, nil)
	old := prepareEvictedTask(s)
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	if old.Value() != nil {
		t.Error("evicted task is still reachable")
	}
	runtime.KeepAlive(s)
}

func TestRetentionEvictsOldestFinished(t *testing.T) {
	s := newTestService(t, nil)
	s.q.mu.Lock()
	s.q.maxFinished = 5
	s.q.mu.Unlock()
	k := &sink{}
	s.Subscribe(k.fn)
	gate := make(chan struct{})
	started := make(chan string, 1)
	setLimits(t, s, 1, 1, 1)
	// A running task must never be evicted however many finish.
	runningID := s.submit(taskSpec{Kind: KindUpload, Work: gated("r", started, gate)})
	<-started
	var ids []string
	for i := 0; i < 12; i++ {
		ids = append(ids, s.submit(taskSpec{Kind: KindOther, Title: fmt.Sprint(i), Work: okWork}))
		wait(t, s, ids[i])
	}
	tasks := s.Tasks()
	if len(tasks) != 6 { // 5 finished + 1 running
		t.Fatalf("%d tasks retained", len(tasks))
	}
	if tasks[0].ID != runningID || tasks[1].Title != "7" || tasks[5].Title != "11" {
		t.Fatalf("wrong survivors: %+v", tasks)
	}
	eventually(t, "removal events", func() bool {
		n := 0
		for _, id := range ids[:7] {
			for _, e := range k.forTask(id) {
				if e.Removed {
					n++
				}
			}
		}
		return n == 7
	})
	close(gate)
	wait(t, s, runningID)
}

func TestClearFinished(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	k := &sink{}
	s.Subscribe(k.fn)
	done := s.submit(taskSpec{Kind: KindOther, Work: okWork})
	failed := s.submit(taskSpec{Kind: KindOther, Work: func(context.Context, *run) error { return errors.New("x") }})
	wait(t, s, done)
	wait(t, s, failed)
	gate := make(chan struct{})
	started := make(chan string, 1)
	running := s.submit(taskSpec{Kind: KindOther, Work: gated("r", started, gate)})
	<-started
	queued := s.submit(taskSpec{Kind: KindOther, Work: okWork})
	s.ClearFinished()
	tasks := s.Tasks()
	if len(tasks) != 2 || tasks[0].ID != running || tasks[1].ID != queued {
		t.Fatalf("after clear: %+v", tasks)
	}
	eventually(t, "removed events", func() bool {
		rm := func(id string) bool {
			for _, e := range k.forTask(id) {
				if e.Removed {
					return true
				}
			}
			return false
		}
		return rm(done) && rm(failed) && !rm(running) && !rm(queued)
	})
	if s.TaskItems(done) != nil {
		t.Fatal("cleared task still known")
	}
	if err := s.Cancel(done); !IsKind(err, ErrNotFound) {
		t.Fatalf("cleared task: %v", err)
	}
	close(gate)
	wait(t, s, queued)
	s.ClearFinished()
	if len(s.Tasks()) != 0 {
		t.Fatal("everything should be gone")
	}
}

// ---- events ----------------------------------------------------------------------------------------------

func TestEventsThrottledButFinalAlwaysDelivered(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	s.Subscribe(k.fn)
	start := time.Now()
	id := s.submit(taskSpec{Kind: KindUpload, Title: "hammer", Work: func(ctx context.Context, r *run) error {
		r.SetTotal(1<<30, 0)
		for time.Since(start) < 1200*time.Millisecond {
			r.AddBytes(1)
		}
		return nil
	}})
	tk := wait(t, s, id)
	if tk.Status != StatusSucceeded {
		t.Fatal(tk.Status)
	}
	eventually(t, "final event", func() bool {
		evs := k.forTask(id)
		return len(evs) > 0 && evs[len(evs)-1].Task.Status == StatusSucceeded
	})
	evs := k.forTask(id)
	// 1.2 s of hammering at <=10/s: at most ~13 progress events plus first/final.
	if len(evs) > 18 {
		t.Fatalf("%d events in ~1.2 s; throttle should keep this near 12", len(evs))
	}
	if len(evs) < 3 {
		t.Fatalf("only %d events", len(evs))
	}
	last := evs[len(evs)-1].Task
	if last.BytesDone != tk.BytesDone || last.Status != StatusSucceeded || last.Finished.IsZero() {
		t.Fatalf("final event %+v vs task %+v", last, tk)
	}
	// The final event is the last one and only one terminal event exists.
	terminal := 0
	for i, e := range evs {
		if e.Task.Status.Finished() {
			terminal++
			if i != len(evs)-1 {
				t.Fatal("event after the final one")
			}
		}
		if i > 0 && e.Task.BytesDone < evs[i-1].Task.BytesDone {
			t.Fatal("progress went backwards")
		}
	}
	if terminal != 1 {
		t.Fatalf("%d terminal events", terminal)
	}
}

func TestFinalEventDeliveredEvenRightAfterThrottledUpdate(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	s.Subscribe(k.fn)
	for i := 0; i < 20; i++ {
		id := s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
			r.SetDetail("a")
			r.AddBytes(10) // throttled: arms a trailing timer
			r.AddBytes(10)
			return errors.New("fail fast")
		}})
		wait(t, s, id)
		eventually(t, "final", func() bool {
			evs := k.forTask(id)
			return len(evs) > 0 && evs[len(evs)-1].Task.Status == StatusFailed
		})
		evs := k.forTask(id)
		if evs[len(evs)-1].Task.BytesDone != 20 || evs[len(evs)-1].Task.Err != "fail fast" {
			t.Fatalf("final event content %+v", evs[len(evs)-1].Task)
		}
	}
}

func TestSlowSubscriberDoesNotBlockWorkAndSeesFinal(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	var block atomic.Bool
	block.Store(true)
	s.Subscribe(func(e Event) {
		for block.Load() {
			time.Sleep(5 * time.Millisecond)
		}
		k.fn(e)
	})
	ids := make([]string, 30)
	begin := time.Now()
	for i := range ids {
		ids[i] = s.submit(taskSpec{Kind: KindOther, Work: func(_ context.Context, r *run) error {
			for j := 0; j < 200; j++ {
				r.AddBytes(1)
				r.Item(ItemResult{Status: ItemSucceeded})
			}
			return nil
		}})
	}
	for _, id := range ids {
		wait(t, s, id)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatal("a blocked subscriber slowed the tasks down")
	}
	block.Store(false)
	eventually(t, "all final events", func() bool {
		for _, id := range ids {
			evs := k.forTask(id)
			if len(evs) == 0 || evs[len(evs)-1].Task.Status != StatusSucceeded {
				return false
			}
		}
		return true
	})
}

func TestSubscribersAreCalledWithoutLocks(t *testing.T) {
	s := newTestService(t, nil)
	var called atomic.Int32
	var unsub func()
	unsub = s.Subscribe(func(e Event) {
		// Re-entering the Service from a callback must not deadlock.
		s.Tasks()
		s.TaskItems(e.Task.ID)
		s.Settings()
		s.Cancel(e.Task.ID)
		s.submit(taskSpec{Kind: KindOther, Work: okWork}) // even submitting from a callback
		if called.Add(1) > 3 {
			unsub()
		}
	})
	id := s.submit(taskSpec{Kind: KindOther, Work: okWork})
	wait(t, s, id)
	eventually(t, "callback", func() bool { return called.Load() > 0 })
}

func TestUnsubscribe(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	unsub := s.Subscribe(k.fn)
	a := s.submit(taskSpec{Kind: KindOther, Work: okWork})
	wait(t, s, a)
	eventually(t, "event for a", func() bool { return len(k.forTask(a)) > 0 })
	unsub()
	time.Sleep(50 * time.Millisecond)
	b := s.submit(taskSpec{Kind: KindOther, Work: okWork})
	wait(t, s, b)
	time.Sleep(100 * time.Millisecond)
	if len(k.forTask(b)) != 0 {
		t.Fatal("events after unsubscribe")
	}
	s.Subscribe(nil)() // nil callback is tolerated
	s.Subscribe(func(Event) { panic("bad subscriber") })
	c := s.submit(taskSpec{Kind: KindOther, Work: okWork}) // a panicking subscriber must not break delivery
	wait(t, s, c)
}

func TestFirstEventIsQueuedThenRunningThenFinal(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	s.Subscribe(k.fn)
	gate := make(chan struct{})
	started := make(chan string, 1)
	id := s.submit(taskSpec{Kind: KindOther, Title: "x", Work: gated("x", started, gate)})
	<-started
	time.Sleep(250 * time.Millisecond)
	close(gate)
	wait(t, s, id)
	eventually(t, "final", func() bool {
		evs := k.forTask(id)
		return len(evs) > 0 && evs[len(evs)-1].Task.Status.Finished()
	})
	var seen []Status
	for _, e := range k.forTask(id) {
		if len(seen) == 0 || seen[len(seen)-1] != e.Task.Status {
			seen = append(seen, e.Task.Status)
		}
	}
	if len(seen) != 3 || seen[0] != StatusQueued || seen[1] != StatusRunning || seen[2] != StatusSucceeded {
		t.Fatalf("status sequence %v", seen)
	}
}

// ---- shutdown ------------------------------------------------------------------------------------------------

func TestShutdownCancelsAndWaits(t *testing.T) {
	s, err := New(Options{ConfigDir: t.TempDir(), Keyring: NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Settings()
	st.Limits.Upload = 1
	s.SaveSettings(st)
	k := &sink{}
	s.Subscribe(k.fn)
	started := make(chan string, 1)
	var cleanedUp atomic.Bool
	running := s.submit(taskSpec{Kind: KindUpload, Work: func(ctx context.Context, r *run) error {
		started <- "r"
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // cleanup that Shutdown must wait for
		cleanedUp.Store(true)
		return ctx.Err()
	}})
	<-started
	var queuedRan atomic.Bool
	queued := s.submit(taskSpec{Kind: KindUpload, Work: func(context.Context, *run) error { queuedRan.Store(true); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !cleanedUp.Load() {
		t.Fatal("Shutdown returned before the running task finished")
	}
	for _, id := range []string{running, queued} {
		tk, _ := taskByID(s, id)
		if tk.Status != StatusCanceled {
			t.Fatalf("%s: %v", id, tk.Status)
		}
	}
	if queuedRan.Load() {
		t.Fatal("queued task ran during shutdown")
	}
	// Final events were flushed before Shutdown returned.
	for _, id := range []string{running, queued} {
		evs := k.forTask(id)
		if len(evs) == 0 || evs[len(evs)-1].Task.Status != StatusCanceled {
			t.Fatalf("%s: final event missing after Shutdown: %+v", id, evs)
		}
	}
	// New work after Shutdown is registered as cancelled, never run.
	var late atomic.Bool
	id := s.submit(taskSpec{Kind: KindOther, Work: func(context.Context, *run) error { late.Store(true); return nil }})
	if tk, _ := taskByID(s, id); tk.Status != StatusCanceled {
		t.Fatalf("late task: %v", tk.Status)
	}
	time.Sleep(20 * time.Millisecond)
	if late.Load() {
		t.Fatal("task ran after Shutdown")
	}
}

func TestShutdownIsBoundedByContext(t *testing.T) {
	s, err := New(Options{ConfigDir: t.TempDir(), Keyring: NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan string, 1)
	id := s.submit(taskSpec{Kind: KindOther, Work: func(ctx context.Context, r *run) error {
		started <- "r"
		<-release // ignores ctx
		return nil
	}})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(begin) > 3*time.Second {
		t.Fatal("Shutdown did not honour its context")
	}
	close(release)
	wait(t, s, id)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := s.Shutdown(ctx2); err != nil { // second call completes cleanly
		t.Fatal(err)
	}
}

// ---- speed ---------------------------------------------------------------------------------------------------

func TestSpeedMeterSmoothing(t *testing.T) {
	var m speedMeter
	t0 := time.Unix(1000, 0)
	m.start(t0)
	if m.value(t0) != 0 {
		t.Fatal("no samples yet")
	}
	// Bursts closer than the window are ignored.
	m.sample(t0.Add(100*time.Millisecond), 1000)
	if m.value(t0.Add(100*time.Millisecond)) != 0 {
		t.Fatal("sample inside the window must be ignored")
	}
	// First real sample: 1 MB in 1 s.
	m.sample(t0.Add(time.Second), 1_000_000)
	if v := m.value(t0.Add(time.Second)); v < 999_000 || v > 1_001_000 {
		t.Fatalf("first sample %v", v)
	}
	// A sudden slowdown to 0 B/s is blended in, not applied at once.
	m.sample(t0.Add(2*time.Second), 1_000_000)
	v := m.value(t0.Add(2 * time.Second))
	if v <= 0 || v >= 1_000_000 || v < 600_000 || v > 800_000 {
		t.Fatalf("smoothed value %v (alpha %v)", v, speedAlpha)
	}
	// Stale after no samples for a while.
	if m.value(t0.Add(2*time.Second+speedStale+time.Second)) != 0 {
		t.Fatal("stale meter must report 0")
	}
	m.stop()
	if m.value(t0.Add(2*time.Second)) != 0 {
		t.Fatal("stopped meter")
	}
}

func TestRunningTaskReportsSpeed(t *testing.T) {
	s := newTestService(t, nil)
	gate := make(chan struct{})
	started := make(chan string, 1)
	id := s.submit(taskSpec{Kind: KindDownload, Work: func(ctx context.Context, r *run) error {
		started <- "go"
		deadline := time.Now().Add(800 * time.Millisecond)
		for time.Now().Before(deadline) {
			r.AddBytes(1000)
			time.Sleep(5 * time.Millisecond)
		}
		<-gate
		return nil
	}})
	<-started
	eventually(t, "speed > 0", func() bool { tk, _ := taskByID(s, id); return tk.Speed > 0 })
	close(gate)
	if tk := wait(t, s, id); tk.Speed != 0 {
		t.Fatalf("finished tasks report no speed, got %v", tk.Speed)
	}
}

func TestStatusAndKindStrings(t *testing.T) {
	for st, want := range map[Status]string{StatusQueued: "queued", StatusRunning: "running", StatusSucceeded: "succeeded",
		StatusFailed: "failed", StatusCanceled: "canceled", StatusPartiallyFailed: "partially failed", Status(99): "unknown"} {
		if st.String() != want {
			t.Errorf("%d: %q", st, st.String())
		}
	}
	if StatusQueued.Finished() || StatusRunning.Finished() || !StatusSucceeded.Finished() || !StatusPartiallyFailed.Finished() {
		t.Fatal("Finished()")
	}
	if KindUpload.String() != "upload" || KindDownload.String() != "download" || KindOther.String() != "other" {
		t.Fatal("Kind strings")
	}
	// An out-of-range kind is coerced to Other rather than panicking.
	s := newTestService(t, nil)
	id := s.submit(taskSpec{Kind: Kind(42), Work: okWork})
	if tk := wait(t, s, id); tk.Kind != KindOther || tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
}
