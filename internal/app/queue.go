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

// This file implements the task queue (docs/dev/app-api.md section 4).
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
//	        r.SetPhase(PhaseVerifying)       // per-file transfers: what happens while no bytes move
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
//     Retry can resume exactly those items. (Scans report what they did not
//     scan through taskSpec.Unfinished instead, so that a pause is not a cancel.)
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
//
// Groups: every status change goes through setStatusLocked, which keeps the
// counters of the task's group exact (GroupProgress), so that group queries,
// group cancel and pause, and the scan back-pressure never walk the task map.

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
	// staleTick is how often running tasks are checked for a speed that went
	// stale since their last event.
	staleTick = time.Second
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
	// RetryEmpty opts scanners into Retry(nil), rebuilding their child group
	// even when they failed or were canceled before reporting an item.
	RetryEmpty bool
	// Saved describes a per-file transfer so that it can be paused, resumed
	// and kept across restarts. nil for every other task.
	Saved *savedTransfer
	// Scan marks the task that walks the selection of a transfer group and
	// submits its per-file tasks. A scan takes no Limits.Other slot, pauses
	// and resumes with its group (Work runs again and continues where it
	// stopped) and is not a file of the group.
	Scan bool
	// Unfinished, for a scan, returns the unprocessed items for what it has not
	// scanned yet; the queue adds them when the scan ends Canceled. nil items
	// when it never started. Called with q.mu held: it must not call the queue.
	Unfinished func() []ItemResult
	// Save, for a scan, describes how to continue it in a later session
	// (SaveTransfers). Called with q.mu held: it must not call the queue.
	Save func() *savedScan
	// SizeHint is the expected size of a per-file transfer, known when it is
	// submitted; its group counts it in BytesTotal while the task waits.
	SizeHint int64
	// Info describes the group of the task. The first member that carries it
	// sets it.
	Info *groupInfo
}

// groupInfo describes a transfer group for its row (GroupProgress). It is
// kept in transfers.json.
type groupInfo struct {
	Kind   Kind   `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Roots  int    `json:"roots,omitempty"`
	Name   string `json:"name,omitempty"`
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
	r.s.recountBytesLocked(r.t, bytes)
	r.s.emitLocked(r.t, false)
	r.s.q.mu.Unlock()
}

// AddTotal grows the expected totals (used while a folder scan is discovering files).
func (r *run) AddTotal(bytes int64, items int) {
	total := r.t.total.Add(bytes)
	r.s.q.mu.Lock()
	r.t.pub.Items += items
	r.s.recountBytesLocked(r.t, total)
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

// SetPhase records what the task is doing while no bytes move (PhaseNone
// while they do). It costs one atomic load when the phase does not change;
// a change is published through the event throttle.
func (r *run) SetPhase(p Phase) {
	if r.t.getPhase() == p {
		return
	}
	r.t.phase.Store(p)
	r.s.q.mu.Lock()
	r.s.emitLocked(r.t, false)
	r.s.q.mu.Unlock()
}

// withPhase runs fn in phase p and restores the phase that was set before.
func (r *run) withPhase(p Phase, fn func() error) error {
	prev := r.t.getPhase()
	r.SetPhase(p)
	defer r.SetPhase(prev)
	return fn()
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
	r.s.q.mu.Lock()
	defer r.s.q.mu.Unlock()
	r.s.itemLocked(r.t, it)
}

func (s *Service) itemLocked(t *task, it ItemResult) {
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
	s.emitLocked(t, false)
}

// task is the queue's record of one task.
type task struct {
	seq  uint64
	spec taskSpec
	ctx  context.Context
	// cancel releases ctx; calling it is how Cancel interrupts Work.
	cancel context.CancelFunc
	done   chan struct{} // closed when the task reached a final state
	// pausing is set when Pause interrupts the running Work; the task then
	// becomes Paused instead of Canceled. Guarded by queueState.mu.
	pausing bool
	// resumeAfter queues the task again as soon as its pause completes.
	resumeAfter bool

	// Guarded by queueState.mu.
	pub      Task
	items    []ItemResult
	keptOK   int
	reported int
	removed  bool
	err      error // error returned by Work, kept for the diagnostic log
	timer    *time.Timer
	inQueue  bool  // waiting in q.queued or q.scans
	grouped  bool  // counted in q.groups[pub.GroupID]
	file     bool  // a per-file transfer (KindUpload or KindDownload)
	scan     bool  // spec.Scan
	counted  int64 // bytes this file contributes to its group's BytesTotal
	// emittedSpeed is the speed of the last event, so that a speed gone
	// stale since then is published once (staleWatch).
	emittedSpeed float64
	// armed is true while a trailing throttled event is scheduled; it lets
	// progress callbacks skip the lock on the hot path.
	armed atomic.Bool

	bytes atomic.Int64
	total atomic.Int64
	speed speedMeter
	phase atomic.Value // Phase

	lastEmit time.Time
}

func (t *task) getPhase() Phase {
	p, _ := t.phase.Load().(Phase)
	return p
}

// queueState is the queue of one Service.
type queueState struct {
	mu       sync.Mutex
	tasks    map[string]*task
	groups   map[string]*taskGroup
	active   map[string]*taskGroup // groups with Pending > 0
	seq      uint64
	groupSeq uint64
	queued   [3][]*task
	running  [3]int
	limits   [3]int
	scans    []*task // queued scans: they start without waiting for a slot
	scanning int     // running scans
	// backlogFloor is the least backlogMax (a variable for tests).
	backlogFloor int
	finished     []*task // in finishing order, oldest first
	maxFinished  int
	closed       bool
	wg           sync.WaitGroup

	// runningSet holds every running task, for staleWatch.
	runningSet map[*task]struct{}
	// staleWatching is true while the staleWatch goroutine runs.
	staleWatching bool
	staleAfter    time.Duration // speed with no sample for this long is 0
	staleTick     time.Duration

	canceledGroups map[string]struct{}
	canceledOrder  []string
	// pausedGroups holds the groups paused as a whole: their tasks submitted
	// later (by a scan that is still running) start out paused too.
	pausedGroups map[string]struct{}

	// stateCh is closed (and reset to nil) when a task starts or reaches a
	// final state; waitGroupBacklog parks on it. Created lazily by a waiter.
	stateCh chan struct{}

	bus *eventBus
}

// taskGroup is the bookkeeping of one group. The counters cover the group's
// whole life; finished members stay counted after they are evicted.
type taskGroup struct {
	result   TaskGroupResult
	retained int
	seq      uint64
	created  time.Time
	info     *groupInfo
	kind     Kind // of the first file, when info is nil
	hasKind  bool
	members  []*task // in submission order; removed ones are compacted lazily
	dead     int     // removed tasks still in members

	files, queued, running, paused, pausable int
	succeeded, skipped, failed, canceled     int
	scans, scansPaused                       int // unfinished scans, and the paused ones among them

	bytesTotal, bytesFinished int64
	runningSet                map[*task]struct{} // running files
}

// count adds d to the counters of status st for member t.
func (g *taskGroup) count(t *task, st Status, d int) {
	switch {
	case t.scan:
		switch st {
		case StatusQueued, StatusRunning:
			g.scans += d
		case StatusPaused:
			g.scans += d
			g.scansPaused += d
		}
	case t.file:
		switch st {
		case StatusQueued:
			g.queued += d
		case StatusRunning:
			g.running += d
			if d > 0 {
				g.runningSet[t] = struct{}{}
			} else {
				delete(g.runningSet, t)
			}
		case StatusPaused:
			g.paused += d
		case StatusSucceeded:
			if t.pub.ItemsSkipped > 0 && t.pub.ItemsSkipped == t.pub.ItemsDone {
				g.skipped += d
			} else {
				g.succeeded += d
			}
		case StatusCanceled:
			g.canceled += d
		default:
			g.failed += d
		}
		if t.spec.Saved != nil && (st == StatusQueued || st == StatusRunning) {
			g.pausable += d
		}
	}
}

// setStatusLocked moves t to st, keeping its group's counters exact.
func (s *Service) setStatusLocked(t *task, st Status) {
	old := t.pub.Status
	if old == st {
		return
	}
	t.pub.Status = st
	if old == StatusRunning {
		delete(s.q.runningSet, t)
	}
	if st == StatusRunning {
		s.q.runningSet[t] = struct{}{}
	}
	if !t.grouped {
		return
	}
	g := s.q.groups[t.pub.GroupID]
	if g == nil {
		return
	}
	g.count(t, old, -1)
	g.count(t, st, 1)
	if t.file && st.Finished() {
		fin := max(t.counted, t.bytes.Load())
		g.bytesTotal += fin - t.counted
		t.counted = fin
		g.bytesFinished += fin
	}
}

// recountBytesLocked makes the group of a running file count total bytes for
// it: the size the file actually transfers replaces the hint given at submit.
func (s *Service) recountBytesLocked(t *task, total int64) {
	if !t.file || !t.grouped || t.pub.Status.Finished() {
		return
	}
	if g := s.q.groups[t.pub.GroupID]; g != nil {
		g.bytesTotal += total - t.counted
		t.counted = total
	}
}

// notifyStateLocked wakes the callers parked in waitGroupBacklog. Caller holds q.mu.
func (s *Service) notifyStateLocked() {
	if ch := s.q.stateCh; ch != nil {
		close(ch)
		s.q.stateCh = nil
	}
}

// backlogMax is the number of queued tasks one scan may leave waiting in the
// queue of kind k: four times the category's concurrency limit, at least
// backlogFloor (64).
func (s *Service) backlogMax(k Kind) int {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	return max(4*s.q.limits[kindIndex(k)], s.q.backlogFloor)
}

// waitGroupBacklog blocks until fewer than max files of group are queued or
// paused (so a submit that follows keeps the backlog at or below max), or ctx
// ends. Scans call it before every submit so that a huge folder does not
// materialise one queued task per file up front. State changes of any task
// wake it; it returns ctx.Err() promptly when the scan is cancelled or paused.
func (s *Service) waitGroupBacklog(ctx context.Context, group string, max int) error {
	q := &s.q
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q.mu.Lock()
		n := 0
		if g := q.groups[group]; g != nil {
			n = g.queued + g.paused
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
	s.q.active = map[string]*taskGroup{}
	s.q.runningSet = map[*task]struct{}{}
	s.q.staleAfter = speedStale
	s.q.staleTick = staleTick
	s.q.limits = [3]int{l.Upload, l.Download, l.Other}
	s.q.maxFinished = maxFinishedTasks
	s.q.backlogFloor = 64
	s.q.canceledGroups = map[string]struct{}{}
	s.q.pausedGroups = map[string]struct{}{}
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
	return s.submitWith(spec, nil)
}

// submitWith is submit that also runs registered (if non-nil) with q.mu held,
// in the same critical section that registers the task: a scan advances its
// saved position there, so that SaveTransfers sees a file either as a task or
// as not yet scanned, never both or neither.
func (s *Service) submitWith(spec taskSpec, registered func()) string {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	spec.Kind = Kind(kindIndex(spec.Kind))
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{
		seq: q.seq, spec: spec, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		scan: spec.Scan,
		pub: Task{ID: fmt.Sprintf("t%d", q.seq), GroupID: spec.Group, Kind: spec.Kind, Title: spec.Title,
			Detail: spec.Detail, Status: StatusQueued, Created: s.now(), Pausable: canPause(spec)},
	}
	q.tasks[t.pub.ID] = t
	if spec.Group != "" {
		g := q.groups[spec.Group]
		if g == nil {
			g = &taskGroup{seq: q.seq, created: t.pub.Created, runningSet: map[*task]struct{}{}}
			q.groups[spec.Group] = g
		}
		if g.info == nil && spec.Info != nil {
			g.info = spec.Info
		}
		g.retained++
		g.result.Pending++
		q.active[spec.Group] = g
		g.members = append(g.members, t)
		t.grouped = true
		if t.file = spec.Kind != KindOther; t.file {
			if !g.hasKind {
				g.kind, g.hasKind = spec.Kind, true
			}
			g.files++
			t.counted = max(spec.SizeHint, 0)
			g.bytesTotal += t.counted
		}
		g.count(t, StatusQueued, 1)
	}
	if registered != nil {
		registered()
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
	if _, paused := q.pausedGroups[spec.Group]; paused && spec.Group != "" && canPause(spec) {
		s.setStatusLocked(t, StatusPaused)
		s.emitLocked(t, false)
		return t.pub.ID
	}
	s.enqueueLocked(t)
	s.emitLocked(t, false)
	s.dispatchLocked()
	return t.pub.ID
}

// canPause reports whether tasks of spec can be paused.
func canPause(spec taskSpec) bool { return spec.Saved != nil || spec.Scan }

// queueOf returns the queue t waits in.
func (s *Service) queueOf(t *task) *[]*task {
	if t.scan {
		return &s.q.scans
	}
	return &s.q.queued[kindIndex(t.pub.Kind)]
}

// enqueueLocked appends t to the end of its queue.
func (s *Service) enqueueLocked(t *task) {
	p := s.queueOf(t)
	*p = append(*p, t)
	t.inQueue = true
}

// unqueueLocked removes t from its queue, in place.
func (s *Service) unqueueLocked(t *task) {
	if !t.inQueue {
		return
	}
	t.inQueue = false
	p := s.queueOf(t)
	qq := *p
	for j, x := range qq {
		if x == t {
			copy(qq[j:], qq[j+1:])
			qq[len(qq)-1] = nil
			*p = qq[:len(qq)-1]
			return
		}
	}
}

// unqueueWhereLocked removes every queued task for which drop is true in one
// pass over the queues, so that group operations stay linear.
func (s *Service) unqueueWhereLocked(drop func(*task) bool) {
	filter := func(p *[]*task) {
		qq := *p
		kept := qq[:0]
		for _, t := range qq {
			if drop(t) {
				t.inQueue = false
			} else {
				kept = append(kept, t)
			}
		}
		clear(qq[len(kept):])
		*p = kept
	}
	for k := range s.q.queued {
		filter(&s.q.queued[k])
	}
	filter(&s.q.scans)
}

// tasksActive reports whether any task is running or waiting in a queue.
func (s *Service) tasksActive() bool {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.scanning > 0 || len(q.scans) > 0 {
		return true
	}
	for k := 0; k < 3; k++ {
		if q.running[k] > 0 || len(q.queued[k]) > 0 {
			return true
		}
	}
	return false
}

// dispatchLocked starts queued tasks while slots are free. Scans start at once.
func (s *Service) dispatchLocked() {
	q := &s.q
	if q.closed {
		return
	}
	for len(q.scans) > 0 {
		t := q.scans[0]
		q.scans[0] = nil
		q.scans = q.scans[1:]
		q.scanning++
		s.startLocked(t)
	}
	for k := 0; k < 3; k++ {
		for q.running[k] < q.limits[k] && len(q.queued[k]) > 0 {
			t := q.queued[k][0]
			q.queued[k][0] = nil
			q.queued[k] = q.queued[k][1:]
			q.running[k]++
			s.startLocked(t)
		}
	}
}

func (s *Service) startLocked(t *task) {
	q := &s.q
	t.inQueue = false
	s.notifyStateLocked()
	s.setStatusLocked(t, StatusRunning)
	t.pub.Started = s.now()
	t.phase.Store(PhaseNone)
	t.speed.start(time.Now())
	t.speed.setStale(q.staleAfter)
	s.emitLocked(t, false)
	q.wg.Add(1)
	s.lgInfo("task started", "task", t.pub.ID, "kind", t.pub.Kind.String(), "title", t.pub.Title)
	if !q.staleWatching {
		q.staleWatching = true
		go s.staleWatch()
	}
	go s.runTask(t)
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
	if t.scan {
		q.scanning--
	} else {
		q.running[kindIndex(t.pub.Kind)]--
	}
	t.pub.Finished = s.now()
	t.err = err
	t.phase.Store(PhaseNone)
	canceled := t.ctx.Err() != nil
	if t.pausing && canceled && !q.closed && (len(t.items) == 0 || t.scan) {
		// Paused: a file starts again from the beginning on Resume, a scan
		// continues where it stopped.
		t.pausing = false
		s.setStatusLocked(t, StatusPaused)
		t.pub.Err, t.pub.Finished, t.pub.Started = "", time.Time{}, time.Time{}
		t.bytes.Store(0)
		t.speed.stop()
		s.notifyStateLocked()
		if t.resumeAfter {
			t.resumeAfter = false
			s.resumeLocked(t)
		} else {
			s.emitLocked(t, false)
		}
		s.dispatchLocked()
		return
	}
	t.pausing, t.resumeAfter = false, false
	var st Status
	switch {
	case err != nil && canceled:
		st, t.pub.Err = StatusCanceled, "canceled"
		s.addUnfinishedLocked(t)
	case err != nil:
		st, t.pub.Err = StatusFailed, err.Error()
	case t.pub.ItemsFailed > 0 || t.pub.ItemsUnprocessed > 0:
		st = StatusPartiallyFailed
		t.pub.Err = fmt.Sprintf("%d of %d item(s) did not complete", t.pub.ItemsFailed+t.pub.ItemsUnprocessed, t.pub.Items)
	default:
		st = StatusSucceeded
	}
	s.setStatusLocked(t, st)
	if st == StatusSucceeded {
		t.spec = taskSpec{} // nothing to retry; release captured data
	}
	s.completeLocked(t)
	s.dispatchLocked()
}

// addUnfinishedLocked reports what a canceled scan did not scan.
func (s *Service) addUnfinishedLocked(t *task) {
	if t.spec.Unfinished == nil {
		return
	}
	for _, it := range t.spec.Unfinished() {
		s.itemLocked(t, it)
	}
}

// finishQueuedLocked ends a task that is not running (queued or paused) as Canceled.
func (s *Service) finishQueuedLocked(t *task, msg string) {
	s.unqueueLocked(t)
	s.addUnfinishedLocked(t)
	s.setStatusLocked(t, StatusCanceled)
	t.pub.Err = msg
	t.pub.Finished = s.now()
	s.completeLocked(t)
}

// completeLocked performs the bookkeeping common to every final state. The
// status is already set (setStatusLocked).
func (s *Service) completeLocked(t *task) {
	q := &s.q
	if g := q.groups[t.pub.GroupID]; g != nil {
		g.result.Pending--
		if g.result.Pending == 0 {
			delete(q.active, t.pub.GroupID)
		}
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

// removeLocked drops a finished task and tells subscribers.
func (s *Service) removeLocked(t *task) {
	if t.removed {
		return
	}
	t.removed = true
	delete(s.q.tasks, t.pub.ID)
	if g := s.q.groups[t.pub.GroupID]; g != nil && t.grouped {
		g.retained--
		if g.retained == 0 {
			delete(s.q.groups, t.pub.GroupID)
			delete(s.q.active, t.pub.GroupID)
		} else if g.dead++; g.dead > 32 && 2*g.dead > len(g.members) {
			kept := g.members[:0]
			for _, m := range g.members {
				if !m.removed {
					kept = append(kept, m)
				}
			}
			clear(g.members[len(kept):])
			g.members, g.dead = kept, 0
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
		p.Phase = t.getPhase()
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

// sendLocked publishes the current state of t as a progress event.
func (s *Service) sendLocked(t *task) {
	t.lastEmit = time.Now()
	ev := Event{Task: t.snapshot()}
	t.emittedSpeed = ev.Task.Speed
	s.q.bus.send(ev, false)
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
		s.sendLocked(t)
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
		s.sendLocked(t)
	})
}

// staleWatch publishes once, with Speed 0, every running task whose last
// event showed a speed that has since gone stale (a stalled transfer emits no
// progress, so nothing else would). It runs while tasks are running.
func (s *Service) staleWatch() {
	q := &s.q
	q.mu.Lock()
	every := q.staleTick
	q.mu.Unlock()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for range tick.C {
		q.mu.Lock()
		if q.closed || len(q.runningSet) == 0 {
			q.staleWatching = false
			q.mu.Unlock()
			return
		}
		now := time.Now()
		for t := range q.runningSet {
			if t.emittedSpeed > 0 && t.speed.value(now) == 0 {
				s.emitLocked(t, false)
			}
		}
		q.mu.Unlock()
	}
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

// GroupProgress returns the progress of a group while any member is running
// or retained (like TaskGroup).
func (s *Service) GroupProgress(id string) (GroupProgress, bool) {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	g := s.q.groups[id]
	if g == nil {
		return GroupProgress{}, false
	}
	return s.groupProgressLocked(id, g, time.Now()), true
}

// TaskGroups returns the progress of every group that still has an unfinished
// member (queued, running or paused), oldest first. Its cost does not depend
// on the number of tasks: call it on every event.
func (s *Service) TaskGroups() []GroupProgress {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	type entry struct {
		id string
		g  *taskGroup
	}
	es := make([]entry, 0, len(s.q.active))
	for id, g := range s.q.active {
		es = append(es, entry{id, g})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].g.seq < es[j].g.seq })
	now := time.Now()
	out := make([]GroupProgress, len(es))
	for i, e := range es {
		out[i] = s.groupProgressLocked(e.id, e.g, now)
	}
	return out
}

func (s *Service) groupProgressLocked(id string, g *taskGroup, now time.Time) GroupProgress {
	p := GroupProgress{
		ID: id, Kind: KindOther, Created: g.created,
		Files: g.files, FilesQueued: g.queued, FilesRunning: g.running, FilesPaused: g.paused,
		FilesSucceeded: g.succeeded, FilesSkipped: g.skipped, FilesFailed: g.failed, FilesCanceled: g.canceled,
		BytesTotal: g.bytesTotal, BytesDone: g.bytesFinished,
		Scanning: g.scans > 0,
		Result:   g.result,
	}
	if g.hasKind {
		p.Kind = g.kind
	}
	if g.info != nil {
		p.Kind, p.Title, p.Detail, p.Roots, p.Name = g.info.Kind, g.info.Title, g.info.Detail, g.info.Roots, g.info.Name
	}
	for t := range g.runningSet {
		p.BytesDone += t.bytes.Load()
		p.Speed += t.speed.value(now)
	}
	scansActive := g.scans - g.scansPaused
	p.Pausable = g.pausable > 0 || scansActive > 0
	_, paused := s.q.pausedGroups[id]
	p.Paused = paused || (g.paused+g.scansPaused > 0 && g.queued == 0 && g.running == 0 && scansActive == 0)
	return p
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
	case StatusQueued, StatusPaused:
		s.finishQueuedLocked(t, "canceled")
	case StatusRunning:
		t.pausing, t.resumeAfter = false, false
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
	members := s.groupMembersLocked(groupID)
	if len(members) == 0 {
		return opErr("CancelGroup", ErrNotFound, "no tasks in group %q", groupID)
	}
	delete(q.pausedGroups, groupID)
	if _, ok := q.canceledGroups[groupID]; !ok {
		q.canceledGroups[groupID] = struct{}{}
		q.canceledOrder = append(q.canceledOrder, groupID)
		if len(q.canceledOrder) > maxCanceledGroups {
			delete(q.canceledGroups, q.canceledOrder[0])
			q.canceledOrder = q.canceledOrder[1:]
		}
	}
	s.unqueueWhereLocked(func(t *task) bool { return t.pub.GroupID == groupID })
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
	}
	if hadItems || spec.RetryEmpty {
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
	next.Kind = kind
	if next.Group == "" {
		next.Group = group
	}
	if next.Title == "" {
		next.Title = title
	}
	if next.Detail == "" {
		next.Detail = detail
	}
	if next.Group != "" { // an explicit retry lifts cancellation of its own group
		q.mu.Lock()
		delete(q.canceledGroups, next.Group)
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

// shutdownQueue implements Service.Shutdown.
func (s *Service) shutdownQueue(ctx context.Context) error {
	q := &s.q
	q.mu.Lock()
	q.closed = true
	var waiting []*task
	for k := 0; k < 3; k++ {
		waiting = append(waiting, q.queued[k]...)
	}
	waiting = append(waiting, q.scans...)
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].seq < waiting[j].seq })
	s.unqueueWhereLocked(func(*task) bool { return true })
	for _, t := range waiting {
		s.finishQueuedLocked(t, "canceled: the application is shutting down")
	}
	for _, t := range q.tasks {
		switch t.pub.Status {
		case StatusRunning:
			t.cancel()
		case StatusPaused:
			s.finishQueuedLocked(t, "canceled: the application is shutting down")
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
	stale  time.Duration // 0 = speedStale
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

func (m *speedMeter) setStale(d time.Duration) {
	m.mu.Lock()
	m.stale = d
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
	stale := m.stale
	if stale <= 0 {
		stale = speedStale
	}
	if !m.primed || now.Sub(m.lastT) > stale {
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
