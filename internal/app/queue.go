package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// This file implements the task queue (APP_API.md section 4).
//
// Internal API for the files that implement operations (transfers, bulk,
// versions, buckets, sync ...):
//
//	spec := taskSpec{
//	    Kind:  KindOther,
//	    Group: groupID,                  // "" for a standalone task; s.newGroupID() for a user action
//	    Title: "Delete 3 objects", Detail: "s3://bucket/prefix/",
//	    Work: func(ctx context.Context, r *run) error {
//	        r.SetTotal(totalBytes, items)    // optional, may be refined later with AddTotal
//	        ...
//	        r.AddBytes(n) / r.SetBytes(cumulative)   // progress; both are cheap and goroutine-safe
//	        r.Item(ItemResult{Key: k, Status: ItemSucceeded})  // one per object, any goroutine
//	        return nil
//	    },
//	    Retry: func(failed []ItemResult) (taskSpec, bool) { ... }, // optional
//	}
//	id := s.submit(spec)
//
// Contract for Work:
//   - Honour ctx. When it is cancelled, stop, report every item not attempted as
//     ItemUnprocessed and return ctx.Err(); the task ends as Canceled and
//     Retry can resume exactly those items.
//   - A non-nil error means the task as a whole failed (Failed, or Canceled if
//     ctx was cancelled). Per-item problems are reported with Item and the task
//     returns nil; the queue then derives Succeeded / PartiallyFailed.
//   - Never replay an ItemUnknown write; Retry only receives failed and
//     unprocessed items.
//   - Panics are recovered and turn into a Failed task.
//
// Locking: Service.q.mu guards task bookkeeping and is never held across I/O or
// while calling subscribers. Events travel through an internal buffer drained
// by one goroutine, so slow subscribers cannot slow down Work.

const (
	// maxFinishedTasks bounds memory: older finished tasks are evicted (FIFO).
	maxFinishedTasks = 1000
	// emitInterval is the per-task event throttle (10 events/s).
	emitInterval = 100 * time.Millisecond
	// maxSucceededItemsKept bounds TaskItems for huge bulk tasks. Counters stay
	// exact; failed, unknown and unprocessed items are always kept.
	maxSucceededItemsKept = 10000
	// maxCanceledGroups bounds the remembered CancelGroup ids.
	maxCanceledGroups = 1000
)

// taskSpec describes work for the queue.
type taskSpec struct {
	Kind          Kind
	Group         string
	Title, Detail string
	// Work does the job. See the contract above.
	Work func(ctx context.Context, r *run) error
	// Retry builds the follow-up task from the failed and unprocessed items of
	// a finished task that reported items. Optional. Tasks that never reported
	// items are retried by running the same spec again.
	Retry func(failed []ItemResult) (taskSpec, bool)
}

// run is the handle Work uses to report progress. All methods are safe for
// concurrent use.
type run struct {
	s *Service
	t *task
}

// SetTotal sets the expected totals (0 = unknown).
func (r *run) SetTotal(bytes int64, items int) {
	r.t.total.Store(bytes)
	r.s.q.mu.Lock()
	r.t.pub.Items = items
	r.s.emitLocked(r.t, false)
	r.s.q.mu.Unlock()
}

// AddTotal grows the expected totals (used while a folder scan is discovering files).
func (r *run) AddTotal(bytes int64, items int) {
	r.t.total.Add(bytes)
	r.s.q.mu.Lock()
	r.t.pub.Items += items
	r.s.emitLocked(r.t, false)
	r.s.q.mu.Unlock()
}

// AddBytes records n more transferred bytes.
func (r *run) AddBytes(n int64) {
	if n <= 0 {
		return
	}
	total := r.t.bytes.Add(n)
	r.t.speed.sample(time.Now(), total)
	r.s.touch(r.t)
}

// SetBytes records the cumulative transferred bytes (as storage.TransferOptions.Progress
// reports them). It never moves backwards.
func (r *run) SetBytes(done int64) {
	for {
		cur := r.t.bytes.Load()
		if done <= cur {
			return
		}
		if r.t.bytes.CompareAndSwap(cur, done) {
			r.t.speed.sample(time.Now(), done)
			r.s.touch(r.t)
			return
		}
	}
}

// SetDetail replaces the detail line ("scanning 1234 objects ...").
func (r *run) SetDetail(d string) {
	r.s.q.mu.Lock()
	r.t.pub.Detail = d
	r.s.emitLocked(r.t, false)
	r.s.q.mu.Unlock()
}

// Item records the result of one object. Succeeded and skipped items count
// as done; failed and unknown items also count as failed; unprocessed items
// count as neither.
func (r *run) Item(it ItemResult) {
	q := &r.s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	t := r.t
	t.reported++
	switch it.Status {
	case ItemSucceeded:
		t.pub.ItemsDone++
	case ItemSkipped:
		t.pub.ItemsDone++
		t.pub.ItemsSkipped++
	case ItemUnprocessed:
		t.pub.ItemsUnprocessed++
	case ItemUnknown:
		t.pub.ItemsDone++
		t.pub.ItemsFailed++
		t.pub.ItemsUnknown++
	default:
		it.Status = ItemFailed
		t.pub.ItemsDone++
		t.pub.ItemsFailed++
	}
	if it.Status == ItemSucceeded || it.Status == ItemSkipped {
		if t.keptOK < maxSucceededItemsKept {
			t.keptOK++
			t.items = append(t.items, it)
		}
	} else {
		t.items = append(t.items, it)
	}
	if t.reported > t.pub.Items {
		t.pub.Items = t.reported
	}
	r.s.emitLocked(t, false)
}

// task is the queue's record of one task.
type task struct {
	seq  uint64
	spec taskSpec
	ctx  context.Context
	// cancel releases ctx; calling it is how Cancel interrupts Work.
	cancel context.CancelFunc
	done   chan struct{} // closed when the task reached a final state

	// Guarded by queueState.mu.
	pub      Task
	items    []ItemResult
	keptOK   int
	reported int
	removed  bool
	err      error // error returned by Work, kept for the diagnostic log
	timer    *time.Timer
	// armed is true while a trailing throttled event is scheduled; it lets
	// progress callbacks skip the lock on the hot path.
	armed atomic.Bool

	bytes atomic.Int64
	total atomic.Int64
	speed speedMeter

	lastEmit time.Time
}

// queueState is the queue of one Service.
type queueState struct {
	mu          sync.Mutex
	tasks       map[string]*task
	groups      map[string]*taskGroup
	seq         uint64
	groupSeq    uint64
	queued      [3][]*task
	running     [3]int
	limits      [3]int
	finished    []*task // in finishing order, oldest first
	maxFinished int
	closed      bool
	wg          sync.WaitGroup

	canceledGroups map[string]struct{}
	canceledOrder  []string

	// stateCh is closed (and reset to nil) when a task starts or reaches a
	// final state; waitGroupBacklog parks on it. Created lazily by a waiter.
	stateCh chan struct{}

	bus *eventBus
}

type taskGroup struct {
	result   TaskGroupResult
	retained int
}

// notifyStateLocked wakes the callers parked in waitGroupBacklog. Caller holds q.mu.
func (s *Service) notifyStateLocked() {
	if ch := s.q.stateCh; ch != nil {
		close(ch)
		s.q.stateCh = nil
	}
}

// backlogMax is the number of queued tasks one scan may leave waiting in the
// queue of kind k: four times the category's concurrency limit, at least 64.
func (s *Service) backlogMax(k Kind) int {
	s.q.mu.Lock()
	l := s.q.limits[kindIndex(k)]
	s.q.mu.Unlock()
	if m := 4 * l; m > 64 {
		return m
	}
	return 64
}

// waitGroupBacklog blocks until fewer than max tasks of group are QUEUED (so a
// submit that follows keeps the backlog at or below max), or ctx ends. Scans
// call it before every submit so that a huge folder does not materialise one
// queued task per file up front. State changes of any task wake it; it returns
// ctx.Err() promptly when the scan is cancelled.
func (s *Service) waitGroupBacklog(ctx context.Context, group string, max int) error {
	q := &s.q
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		n := 0
		for k := range q.queued {
			for _, t := range q.queued[k] {
				if t.pub.GroupID == group {
					n++
				}
			}
		}
		if n < max {
			q.mu.Unlock()
			return nil
		}
		if q.stateCh == nil {
			q.stateCh = make(chan struct{})
		}
		ch := q.stateCh
		q.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) initQueue(l Limits) {
	s.q.tasks = map[string]*task{}
	s.q.groups = map[string]*taskGroup{}
	s.q.limits = [3]int{l.Upload, l.Download, l.Other}
	s.q.maxFinished = maxFinishedTasks
	s.q.canceledGroups = map[string]struct{}{}
	s.q.bus = newEventBus()
}

// newGroupID returns a fresh GroupID for the per-file tasks of one user action.
func (s *Service) newGroupID() string {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	s.q.groupSeq++
	return fmt.Sprintf("g%d", s.q.groupSeq)
}

// setLimits applies new concurrency limits; running tasks are never interrupted.
func (s *Service) setLimits(l Limits) {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	s.q.limits = [3]int{l.Upload, l.Download, l.Other}
	s.dispatchLocked()
}

func kindIndex(k Kind) int {
	if k < KindUpload || k > KindOther {
		return int(KindOther)
	}
	return int(k)
}

// submit registers spec and returns its task ID immediately. The order of
// submit calls is the FIFO order within a kind. After Shutdown, or for a group
// that was cancelled, the task is registered as already Canceled so that
// callers never lose track of it.
func (s *Service) submit(spec taskSpec) string {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	spec.Kind = Kind(kindIndex(spec.Kind))
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{
		seq: q.seq, spec: spec, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		pub: Task{ID: fmt.Sprintf("t%d", q.seq), GroupID: spec.Group, Kind: spec.Kind, Title: spec.Title,
			Detail: spec.Detail, Status: StatusQueued, Created: s.now()},
	}
	q.tasks[t.pub.ID] = t
	if spec.Group != "" {
		g := q.groups[spec.Group]
		if g == nil {
			g = &taskGroup{}
			q.groups[spec.Group] = g
		}
		g.retained++
		g.result.Pending++
	}
	_, groupCanceled := q.canceledGroups[spec.Group]
	if q.closed || (spec.Group != "" && groupCanceled) {
		msg := "canceled"
		if q.closed {
			msg = "canceled: the application is shutting down"
		}
		s.finishQueuedLocked(t, msg)
		return t.pub.ID
	}
	i := kindIndex(spec.Kind)
	q.queued[i] = append(q.queued[i], t)
	s.emitLocked(t, false)
	s.dispatchLocked()
	return t.pub.ID
}

// dispatchLocked starts queued tasks while slots are free.
func (s *Service) dispatchLocked() {
	q := &s.q
	if q.closed {
		return
	}
	for k := 0; k < 3; k++ {
		for q.running[k] < q.limits[k] && len(q.queued[k]) > 0 {
			t := q.queued[k][0]
			q.queued[k][0] = nil
			q.queued[k] = q.queued[k][1:]
			q.running[k]++
			s.notifyStateLocked()
			t.pub.Status = StatusRunning
			t.pub.Started = s.now()
			t.speed.start(time.Now())
			s.emitLocked(t, false)
			q.wg.Add(1)
			s.lgInfo("task started", "task", t.pub.ID, "kind", t.pub.Kind.String(), "title", t.pub.Title)
			go s.runTask(t)
		}
	}
}

func (s *Service) runTask(t *task) {
	defer s.q.wg.Done()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error: %v", r)
			}
		}()
		if t.spec.Work != nil {
			err = t.spec.Work(t.ctx, &run{s: s, t: t})
		}
	}()
	s.finishRunning(t, err)
}

// finishRunning derives the final status of a task whose Work returned.
func (s *Service) finishRunning(t *task, err error) {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	q.running[kindIndex(t.pub.Kind)]--
	t.pub.Finished = s.now()
	t.err = err
	canceled := t.ctx.Err() != nil
	switch {
	case err != nil && canceled:
		t.pub.Status, t.pub.Err = StatusCanceled, "canceled"
	case err != nil:
		t.pub.Status, t.pub.Err = StatusFailed, err.Error()
	case t.pub.ItemsFailed > 0 || t.hasUnprocessed():
		t.pub.Status = StatusPartiallyFailed
		t.pub.Err = fmt.Sprintf("%d of %d item(s) did not complete", t.countNotOK(), t.pub.Items)
	default:
		t.pub.Status = StatusSucceeded
		t.spec = taskSpec{} // nothing to retry; release captured data
	}
	s.completeLocked(t)
	s.dispatchLocked()
}

// finishQueuedLocked ends a task that never ran as Canceled.
func (s *Service) finishQueuedLocked(t *task, msg string) {
	t.pub.Status = StatusCanceled
	t.pub.Err = msg
	t.pub.Finished = s.now()
	s.completeLocked(t)
}

// completeLocked performs the bookkeeping common to every final state.
func (s *Service) completeLocked(t *task) {
	q := &s.q
	if g := q.groups[t.pub.GroupID]; g != nil {
		g.result.Pending--
		switch t.pub.Status {
		case StatusSucceeded:
			if t.pub.Kind != KindOther {
				g.result.Succeeded += t.pub.ItemsDone - t.pub.ItemsFailed - t.pub.ItemsSkipped
			}
		case StatusCanceled:
			g.result.Canceled++
		default:
			g.result.Failed++
		}
	}
	t.cancel()
	t.pub.Speed = 0
	t.speed.stop()
	s.lgTaskDone(t)
	q.finished = append(q.finished, t)
	s.notifyStateLocked()
	s.emitLocked(t, true)
	close(t.done)
	for len(q.finished) > q.maxFinished {
		s.removeLocked(q.finished[0])
		q.finished[0] = nil // the sliced-off slot must not retain the task's results
		q.finished = q.finished[1:]
	}
}

func (t *task) hasUnprocessed() bool { return t.countStatus(ItemUnprocessed) > 0 }
func (t *task) countStatus(st string) int {
	n := 0
	for _, it := range t.items {
		if it.Status == st {
			n++
		}
	}
	return n
}
func (t *task) countNotOK() int {
	n := 0
	for _, it := range t.items {
		if it.Status != ItemSucceeded && it.Status != ItemSkipped {
			n++
		}
	}
	return n
}

// removeLocked drops a finished task and tells subscribers.
func (s *Service) removeLocked(t *task) {
	if t.removed {
		return
	}
	t.removed = true
	delete(s.q.tasks, t.pub.ID)
	if g := s.q.groups[t.pub.GroupID]; g != nil {
		g.retained--
		if g.retained == 0 {
			delete(s.q.groups, t.pub.GroupID)
		}
	}
	s.q.bus.send(Event{Task: t.snapshot(), Removed: true}, true)
}

// snapshot returns the public view of t. Caller holds q.mu.
func (t *task) snapshot() Task {
	p := t.pub
	p.BytesDone = t.bytes.Load()
	p.BytesTotal = t.total.Load()
	if p.Status == StatusRunning {
		p.Speed = t.speed.value(time.Now())
	}
	return p
}

// touch is the hot path of progress callbacks: if a trailing event is already
// scheduled nothing is left to do.
func (s *Service) touch(t *task) {
	if t.armed.Load() {
		return
	}
	s.q.mu.Lock()
	s.emitLocked(t, false)
	s.q.mu.Unlock()
}

// emitLocked publishes the current state of t, throttled to one event per
// emitInterval. A throttled update arms a timer that publishes the then-current
// state, so the last change is never lost. final bypasses the throttle and
// cancels the timer: the final state is always delivered.
func (s *Service) emitLocked(t *task, final bool) {
	if final {
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		t.armed.Store(false)
		s.q.bus.send(Event{Task: t.snapshot()}, true)
		return
	}
	if t.pub.Status.Finished() || t.removed || t.armed.Load() {
		return
	}
	since := time.Since(t.lastEmit)
	if t.lastEmit.IsZero() || since >= emitInterval {
		t.lastEmit = time.Now()
		s.q.bus.send(Event{Task: t.snapshot()}, false)
		return
	}
	t.armed.Store(true)
	t.timer = time.AfterFunc(emitInterval-since, func() {
		s.q.mu.Lock()
		defer s.q.mu.Unlock()
		t.armed.Store(false)
		t.timer = nil
		if t.pub.Status.Finished() || t.removed {
			return
		}
		t.lastEmit = time.Now()
		s.q.bus.send(Event{Task: t.snapshot()}, false)
	})
}

// ---- public API ---------------------------------------------------------------------------

// TaskGroup returns the full result while any member is running or retained.
// Group bookkeeping is released with the last member; task retention is unchanged.
func (s *Service) TaskGroup(id string) (TaskGroupResult, bool) {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	if g := s.q.groups[id]; g != nil {
		return g.result, true
	}
	return TaskGroupResult{}, false
}

// Subscribe registers fn for task events and returns the unsubscribe function.
// fn is called from a dedicated goroutine, never while the Service holds a
// lock, in per-task order, at most ~10 times per second per task; the final
// state of every task is always delivered. Intermediate progress events may be
// coalesced when fn is slow. Subscribers do not receive a replay: call Tasks
// first. A call that was already dispatched may still arrive after unsubscribe.
func (s *Service) Subscribe(fn func(Event)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	return s.q.bus.subscribe(fn)
}

// Tasks returns snapshots of all known tasks (queued, running and the retained
// finished ones) in submission order.
func (s *Service) Tasks() []Task {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	ts := make([]*task, 0, len(q.tasks))
	for _, t := range q.tasks {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	out := make([]Task, len(ts))
	for i, t := range ts {
		out[i] = t.snapshot()
	}
	return out
}

// TaskItems returns the per-item results reported so far (nil for an unknown
// task). For very large tasks only the first 10000 succeeded or skipped items
// are retained; counters in Task stay exact and failures are always kept.
func (s *Service) TaskItems(id string) []ItemResult {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.tasks[id]
	if t == nil {
		return nil
	}
	return append([]ItemResult(nil), t.items...)
}

// Cancel cancels a task. A queued task is removed from its queue at once and
// becomes Canceled; a running task has its context cancelled and ends as soon
// as its Work returns. Cancelling a finished task is a no-op.
func (s *Service) Cancel(id string) error {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.tasks[id]
	if t == nil {
		return opErr("Cancel", ErrNotFound, "no task %q", id)
	}
	s.cancelLocked(t)
	return nil
}

func (s *Service) cancelLocked(t *task) {
	switch t.pub.Status {
	case StatusQueued:
		i := kindIndex(t.pub.Kind)
		qq := s.q.queued[i]
		for j, x := range qq {
			if x == t {
				s.q.queued[i] = append(qq[:j:j], qq[j+1:]...)
				break
			}
		}
		s.finishQueuedLocked(t, "canceled")
	case StatusRunning:
		t.cancel()
	}
}

// CancelGroup cancels every unfinished task of a group, including tasks of the
// group that are submitted afterwards (a folder scan may still be expanding
// into per-file tasks). Unknown groups are an error.
func (s *Service) CancelGroup(groupID string) error {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if groupID == "" {
		return invalid("CancelGroup", "group id is required")
	}
	var members []*task
	for _, t := range q.tasks {
		if t.pub.GroupID == groupID {
			members = append(members, t)
		}
	}
	if len(members) == 0 {
		return opErr("CancelGroup", ErrNotFound, "no tasks in group %q", groupID)
	}
	if _, ok := q.canceledGroups[groupID]; !ok {
		q.canceledGroups[groupID] = struct{}{}
		q.canceledOrder = append(q.canceledOrder, groupID)
		if len(q.canceledOrder) > maxCanceledGroups {
			delete(q.canceledGroups, q.canceledOrder[0])
			q.canceledOrder = q.canceledOrder[1:]
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].seq < members[j].seq })
	for _, t := range members {
		s.cancelLocked(t)
	}
	return nil
}

// Retry starts a new task for a Failed, PartiallyFailed or Canceled task and
// returns its ID.
//   - A task that reported no items (one file per task) runs the same work again.
//   - Otherwise only the failed and unprocessed items are retried, through the
//     task's Retry function; items with unknown outcome are never replayed.
func (s *Service) Retry(id string) (string, error) {
	const op = "Retry"
	q := &s.q
	q.mu.Lock()
	t := q.tasks[id]
	if t == nil {
		q.mu.Unlock()
		return "", opErr(op, ErrNotFound, "no task %q", id)
	}
	switch t.pub.Status {
	case StatusFailed, StatusPartiallyFailed, StatusCanceled:
	default:
		q.mu.Unlock()
		return "", invalid(op, "task is %s; only failed, partially failed or canceled tasks can be retried", t.pub.Status)
	}
	spec := t.spec
	var retry []ItemResult
	unknown := 0
	for _, it := range t.items {
		switch it.Status {
		case ItemFailed, ItemUnprocessed:
			retry = append(retry, it)
		case ItemUnknown:
			unknown++
		}
	}
	hadItems := len(t.items) > 0
	group, title, detail, kind := t.pub.GroupID, t.pub.Title, t.pub.Detail, t.pub.Kind
	q.mu.Unlock()

	if spec.Work == nil {
		return "", invalid(op, "task cannot be retried")
	}
	next := spec
	if hadItems {
		if len(retry) == 0 {
			if unknown > 0 {
				return "", invalid(op, "nothing to retry: %d item(s) have an unknown outcome and are not replayed automatically; check them first", unknown)
			}
			return "", invalid(op, "no failed or unprocessed items to retry")
		}
		if spec.Retry == nil {
			return "", invalid(op, "this kind of task cannot be retried")
		}
		var ok bool
		if next, ok = spec.Retry(retry); !ok {
			return "", invalid(op, "the remaining items cannot be retried")
		}
	}
	if next.Work == nil {
		return "", invalid(op, "task cannot be retried")
	}
	next.Kind, next.Group = kind, group
	if next.Title == "" {
		next.Title = title
	}
	if next.Detail == "" {
		next.Detail = detail
	}
	if group != "" { // an explicit retry lifts an earlier CancelGroup
		q.mu.Lock()
		delete(q.canceledGroups, group)
		q.mu.Unlock()
	}
	return s.submit(next), nil
}

// ClearFinished removes every finished task from the list.
func (s *Service) ClearFinished() {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.finished {
		s.removeLocked(t)
	}
	q.finished = nil
}

// waitTask blocks until task id is final or ctx ends and returns its last
// snapshot. For tests and for operations that expand into follow-up tasks.
func (s *Service) waitTask(ctx context.Context, id string) (Task, error) {
	q := &s.q
	q.mu.Lock()
	t := q.tasks[id]
	q.mu.Unlock()
	if t == nil {
		return Task{}, opErr("Wait", ErrNotFound, "no task %q", id)
	}
	select {
	case <-t.done:
	case <-ctx.Done():
		return Task{}, ctx.Err()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return t.snapshot(), nil
}

// shutdownQueue implements Service.Shutdown.
func (s *Service) shutdownQueue(ctx context.Context) error {
	q := &s.q
	q.mu.Lock()
	q.closed = true
	for k := 0; k < 3; k++ {
		for _, t := range q.queued[k] {
			s.finishQueuedLocked(t, "canceled: the application is shutting down")
		}
		q.queued[k] = nil
	}
	for _, t := range q.tasks {
		if t.pub.Status == StatusRunning {
			t.cancel()
		}
	}
	q.mu.Unlock()

	idle := make(chan struct{})
	go func() { q.wg.Wait(); close(idle) }()
	select {
	case <-idle:
	case <-ctx.Done():
		return fmt.Errorf("shutdown: tasks still running: %w", ctx.Err())
	}
	if err := q.bus.close(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// ---- speed smoothing --------------------------------------------------------------------------------

// speedMeter keeps an exponential moving average of the transfer rate,
// sampled at most every speedWindow so that bursts of tiny writes do not
// dominate.
type speedMeter struct {
	mu     sync.Mutex
	ema    float64
	lastT  time.Time
	lastB  int64
	primed bool
}

const (
	speedWindow = 250 * time.Millisecond
	speedAlpha  = 0.3
	speedStale  = 5 * time.Second // no sample for this long: report 0
)

func (m *speedMeter) start(now time.Time) {
	m.mu.Lock()
	m.lastT, m.lastB, m.ema, m.primed = now, 0, 0, false
	m.mu.Unlock()
}

func (m *speedMeter) stop() {
	m.mu.Lock()
	m.ema = 0
	m.mu.Unlock()
}

func (m *speedMeter) sample(now time.Time, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastT.IsZero() {
		m.lastT = now
	}
	dt := now.Sub(m.lastT)
	if dt < speedWindow {
		return
	}
	inst := float64(total-m.lastB) / dt.Seconds()
	if !m.primed {
		m.ema, m.primed = inst, true
	} else {
		m.ema = speedAlpha*inst + (1-speedAlpha)*m.ema
	}
	m.lastT, m.lastB = now, total
}

func (m *speedMeter) value(now time.Time) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.primed || now.Sub(m.lastT) > speedStale {
		return 0
	}
	return m.ema
}

// ---- event bus ----------------------------------------------------------------------------------------------

type pendingEvent struct{ ev Event }

type subscriber struct {
	id int
	fn func(Event)
}

// eventBus delivers events to subscribers from one goroutine, in order, with
// coalescing of undelivered progress events per task so that a slow subscriber
// costs bounded memory.
type eventBus struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []*pendingEvent
	pend   map[string]*pendingEvent // undelivered, coalescable progress event per task
	subs   []subscriber
	nextID int
	closed bool
	done   chan struct{}
}

func newEventBus() *eventBus {
	b := &eventBus{pend: map[string]*pendingEvent{}, done: make(chan struct{})}
	b.cond = sync.NewCond(&b.mu)
	go b.loop()
	return b
}

// send queues ev. Non-terminal progress events (terminal=false) replace an
// undelivered earlier progress event of the same task. A terminal or removal
// event is never lost; it takes over the slot of an undelivered progress event.
func (b *eventBus) send(ev Event, terminal bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	id := ev.Task.ID
	p := b.pend[id]
	switch {
	case !terminal && p != nil:
		p.ev = ev
		return
	case !terminal:
		p = &pendingEvent{ev: ev}
		b.pend[id] = p
		b.buf = append(b.buf, p)
	case p != nil && !ev.Removed:
		p.ev = ev
		delete(b.pend, id)
	default:
		delete(b.pend, id)
		b.buf = append(b.buf, &pendingEvent{ev: ev})
	}
	b.cond.Signal()
}

func (b *eventBus) subscribe(fn func(Event)) func() {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.subs = append(b.subs, subscriber{id, fn})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subs {
			if s.id == id {
				b.subs = append(b.subs[:i:i], b.subs[i+1:]...)
				return
			}
		}
	}
}

func (b *eventBus) loop() {
	defer close(b.done)
	for {
		b.mu.Lock()
		for len(b.buf) == 0 && !b.closed {
			b.cond.Wait()
		}
		if len(b.buf) == 0 {
			b.mu.Unlock()
			return
		}
		p := b.buf[0]
		b.buf[0] = nil
		b.buf = b.buf[1:]
		if b.pend[p.ev.Task.ID] == p {
			delete(b.pend, p.ev.Task.ID)
		}
		subs := append([]subscriber(nil), b.subs...)
		ev := p.ev
		b.mu.Unlock()
		for _, s := range subs {
			deliver(s.fn, ev)
		}
	}
}

func deliver(fn func(Event), ev Event) {
	defer func() { recover() }() // a faulty subscriber must not kill the dispatcher
	fn(ev)
}

// close stops accepting events, drains what is queued and waits for the
// dispatcher (bounded by ctx).
func (b *eventBus) close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return errors.New("event delivery did not finish: " + ctx.Err().Error())
	}
}
