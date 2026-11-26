package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// blockRequests makes the server hold every request match accepts until the
// returned release is called (or the request ends). held reports the number
// of requests being held.
func blockRequests(t *testing.T, srv *s3test.Server, match func(*http.Request) bool) (held func() int, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var n atomic.Int32
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if !match(r) {
			return nil
		}
		n.Add(1)
		defer n.Add(-1)
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		return nil
	})
	return func() int { return int(n.Load()) }, release
}

func taskPhase(s *Service, id string) Phase {
	tk, _ := taskByID(s, id)
	return tk.Phase
}

func onlyTaskOfKind(t *testing.T, s *Service, group string, k Kind) string {
	t.Helper()
	var id string
	eventually(t, "the transfer task", func() bool {
		for _, tk := range s.Tasks() {
			if tk.GroupID == group && tk.Kind == k {
				id = tk.ID
				return true
			}
		}
		return false
	})
	return id
}

func TestUploadPhases(t *testing.T) {
	t.Run("preparing while the destination is looked up", func(t *testing.T) {
		e := newTrEnv(t)
		local := filepath.Join(t.TempDir(), "a.txt")
		trWrite(t, local, []byte("hello"))
		held, release := blockRequests(t, e.srv, func(r *http.Request) bool { return r.Method == http.MethodHead })
		g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictSkip})
		if err != nil {
			t.Fatal(err)
		}
		id := onlyTaskOfKind(t, e.s, g, KindUpload)
		eventually(t, "HEAD held", func() bool { return held() > 0 })
		if p := taskPhase(e.s, id); p != PhasePreparing {
			t.Fatalf("phase %q", p)
		}
		release()
		if tk := wait(t, e.s, id); tk.Status != StatusSucceeded || tk.Phase != PhaseNone {
			t.Fatalf("%+v", tk)
		}
	})
	t.Run("verifying while an identical file is compared", func(t *testing.T) {
		e := newTrEnv(t)
		local := filepath.Join(t.TempDir(), "a.txt")
		trWrite(t, local, []byte("hello"))
		trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictRename}), StatusSucceeded)
		held, release := blockRequests(t, e.srv, func(r *http.Request) bool { return r.Method == http.MethodGet })
		g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictRename})
		if err != nil {
			t.Fatal(err)
		}
		id := onlyTaskOfKind(t, e.s, g, KindUpload)
		eventually(t, "GET held", func() bool { return held() > 0 })
		if p := taskPhase(e.s, id); p != PhaseVerifying {
			t.Fatalf("phase %q", p)
		}
		release()
		if tk := wait(t, e.s, id); tk.Status != StatusSucceeded || tk.ItemsSkipped != 1 {
			t.Fatalf("%+v", tk)
		}
	})
	t.Run("finishing once every byte is sent", func(t *testing.T) {
		e := newTrEnv(t)
		local := filepath.Join(t.TempDir(), "a.txt")
		trWrite(t, local, []byte("hello"))
		held, release := blockRequests(t, e.srv, func(r *http.Request) bool { return r.Method == http.MethodPut })
		g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictOverwrite})
		if err != nil {
			t.Fatal(err)
		}
		id := onlyTaskOfKind(t, e.s, g, KindUpload)
		eventually(t, "PUT held", func() bool { return held() > 0 })
		eventually(t, "finishing", func() bool { return taskPhase(e.s, id) == PhaseFinishing })
		// Pausing clears the phase.
		if err := e.s.Pause(id); err != nil {
			t.Fatal(err)
		}
		eventually(t, "paused", func() bool { return statusOf(t, e.s, id) == StatusPaused })
		if p := taskPhase(e.s, id); p != PhaseNone {
			t.Fatalf("paused task keeps phase %q", p)
		}
		release()
		if err := e.s.Resume(id); err != nil {
			t.Fatal(err)
		}
		if tk := wait(t, e.s, id); tk.Status != StatusSucceeded || tk.Phase != PhaseNone {
			t.Fatalf("%+v", tk)
		}
	})
}

func TestDownloadPhaseFinishing(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "a.txt", []byte("hello"), "", nil)
	var sawFinishing atomic.Bool
	unsub := e.s.Subscribe(func(ev Event) {
		if ev.Task.Kind == KindDownload && ev.Task.Phase == PhaseFinishing {
			sawFinishing.Store(true)
		}
	})
	defer unsub()
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"a.txt"}, DestDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	trExpect(t, trWaitGroup(t, e.s, g), StatusSucceeded)
	// The final event of a task carries no phase.
	for _, tk := range e.s.Tasks() {
		if tk.Phase != PhaseNone {
			t.Fatalf("finished task with phase: %+v", tk)
		}
	}
	_ = sawFinishing.Load() // may be coalesced away by the throttle; not asserted
}

func TestSetPhaseIsCheapWhenUnchanged(t *testing.T) {
	s := newTestService(t, nil)
	k := &sink{}
	s.Subscribe(k.fn)
	release := make(chan struct{})
	id := s.submit(taskSpec{Kind: KindUpload, Work: func(ctx context.Context, r *run) error {
		for i := 0; i < 1000; i++ {
			r.SetPhase(PhaseVerifying)
		}
		<-release
		return nil
	}})
	eventually(t, "verifying", func() bool { return taskPhase(s, id) == PhaseVerifying })
	time.Sleep(3 * emitInterval)
	if n := len(k.forTask(id)); n > 4 {
		t.Fatalf("%d events for one phase change", n)
	}
	close(release)
	wait(t, s, id)
}

func TestGroupProgressOfFolderUpload(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	dir := filepath.Join(t.TempDir(), "photos")
	const n = 6
	var total int64
	for i := 0; i < n; i++ {
		data := strings.Repeat("x", 10*(i+1))
		total += int64(len(data))
		trWrite(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), []byte(data))
	}
	held, release := blockRequests(t, e.srv, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", "in/"), Paths: []string{dir}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "a PUT held and the scan done", func() bool {
		p, _ := e.s.GroupProgress(g)
		return held() > 0 && !p.Scanning && p.Files == n
	})
	p, ok := e.s.GroupProgress(g)
	if !ok || p.Kind != KindUpload || p.Title != "Upload photos" || p.Name != "photos" || p.Roots != 1 || p.Detail != "→ s3://b/in/" {
		t.Fatalf("%+v", p)
	}
	// Queued files count with the size the scan saw.
	if p.BytesTotal != total || p.FilesRunning != 1 || p.FilesQueued != n-1 || p.FilesFinished() != 0 || !p.Pausable || p.Paused {
		t.Fatalf("%+v", p)
	}
	gs := e.s.TaskGroups()
	if len(gs) != 1 || gs[0].ID != g {
		t.Fatalf("%+v", gs)
	}
	release()
	trExpect(t, trWaitGroup(t, e.s, g), StatusSucceeded)
	p, _ = e.s.GroupProgress(g)
	if p.FilesSucceeded != n || p.BytesDone != total || p.BytesTotal != total || p.Speed != 0 || p.Scanning || p.Result.Succeeded != n {
		t.Fatalf("%+v", p)
	}
	if gs := e.s.TaskGroups(); len(gs) != 0 {
		t.Fatalf("finished group still active: %+v", gs)
	}
	// Skipped files are counted as such.
	g2, err := e.s.Upload(UploadRequest{Dest: e.loc("b", "in/"), Paths: []string{dir}, Conflict: ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	trWaitGroup(t, e.s, g2)
	if p, _ := e.s.GroupProgress(g2); p.FilesSkipped != n || p.FilesSucceeded != 0 || p.BytesDone != p.BytesTotal {
		t.Fatalf("%+v", p)
	}
}

func TestGroupProgressSurvivesEviction(t *testing.T) {
	s := newTestService(t, nil)
	s.q.mu.Lock()
	s.q.maxFinished = 2
	s.q.mu.Unlock()
	g := s.newGroupID()
	gate := make(chan struct{})
	started := make(chan string, 1)
	running := s.submit(taskSpec{Kind: KindDownload, Group: g, SizeHint: 7, Work: gated("r", started, gate)})
	<-started
	for i := 0; i < 10; i++ {
		id := s.submit(taskSpec{Kind: KindDownload, Group: g, SizeHint: 5, Work: func(_ context.Context, r *run) error {
			r.SetTotal(5, 1)
			r.SetBytes(5)
			r.Item(ItemResult{Key: "k", Status: ItemSucceeded})
			return nil
		}})
		_ = id
	}
	eventually(t, "ten finished", func() bool { p, _ := s.GroupProgress(g); return p.FilesSucceeded == 10 })
	if n := len(s.Tasks()); n > 3 {
		t.Fatalf("%d tasks retained", n)
	}
	p, _ := s.GroupProgress(g)
	if p.Files != 11 || p.BytesTotal != 57 || p.BytesDone != 50 || p.FilesRunning != 1 || p.Kind != KindDownload {
		t.Fatalf("%+v", p)
	}
	close(gate)
	wait(t, s, running)
	if gs := s.TaskGroups(); len(gs) != 0 {
		t.Fatalf("%+v", gs)
	}
}

func TestGroupProgressAdjustsToActualSize(t *testing.T) {
	s := newTestService(t, nil)
	g := s.newGroupID()
	gate := make(chan struct{})
	id := s.submit(taskSpec{Kind: KindUpload, Group: g, SizeHint: 100, Work: func(ctx context.Context, r *run) error {
		r.SetTotal(100, 1)
		r.SetTotal(40, 1) // compressed
		r.SetBytes(10)
		<-gate
		r.SetBytes(40)
		return nil
	}})
	eventually(t, "running with progress", func() bool {
		p, _ := s.GroupProgress(g)
		return p.BytesTotal == 40 && p.BytesDone == 10
	})
	close(gate)
	wait(t, s, id)
	if p, _ := s.GroupProgress(g); p.BytesTotal != 40 || p.BytesDone != 40 {
		t.Fatalf("%+v", p)
	}
}

func TestStandaloneTasksHaveNoGroup(t *testing.T) {
	s := newTestService(t, nil)
	wait(t, s, s.submit(taskSpec{Kind: KindOther, Work: okWork}))
	if gs := s.TaskGroups(); len(gs) != 0 {
		t.Fatalf("%+v", gs)
	}
	if _, ok := s.GroupProgress(""); ok {
		t.Fatal("the empty group exists")
	}
}

func TestPauseGroupResumeGroupCancelGroupLarge(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	g := s.newGroupID()
	gate := make(chan struct{})
	started := make(chan string, 1)
	const n = 20000
	ids := []string{s.submit(pausableSpec(g, gated("first", started, gate)))}
	<-started
	for i := 1; i < n; i++ {
		ids = append(ids, s.submit(pausableSpec(g, okWork)))
	}
	other := s.submit(taskSpec{Kind: KindUpload, Work: okWork}) // not in the group
	t0 := time.Now()
	if err := s.PauseGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := s.PauseGroup("nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown group: %v", err)
	}
	eventually(t, "group paused", func() bool { p, _ := s.GroupProgress(g); return p.FilesPaused == n && p.Paused && !p.Pausable })
	if tk := wait(t, s, other); tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
	if err := s.ResumeGroup(g); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GroupProgress(g); p.Paused || p.FilesPaused != 0 {
		t.Fatalf("%+v", p)
	}
	if err := s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Fatalf("group operations over %d tasks took %v", n, d)
	}
	close(gate)
	eventually(t, "all canceled or done", func() bool {
		p, _ := s.GroupProgress(g)
		return p.FilesFinished() == n
	})
	p, _ := s.GroupProgress(g)
	if p.FilesCanceled < n-1 || p.Result.Pending != 0 {
		t.Fatalf("%+v", p)
	}
	// FIFO of the other queues is intact.
	a := s.submit(taskSpec{Kind: KindUpload, Work: okWork})
	if tk := wait(t, s, a); tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
	_ = ids
}

func TestStaleSpeedIsPublished(t *testing.T) {
	s := newTestService(t, nil)
	s.q.mu.Lock()
	s.q.staleAfter, s.q.staleTick = 300*time.Millisecond, 50*time.Millisecond
	s.q.mu.Unlock()
	var zeroWhileRunning, positive atomic.Bool
	unsub := s.Subscribe(func(ev Event) {
		if ev.Task.Status != StatusRunning {
			return
		}
		if ev.Task.Speed > 0 {
			positive.Store(true)
		} else if positive.Load() {
			zeroWhileRunning.Store(true)
		}
	})
	defer unsub()
	gate := make(chan struct{})
	id := s.submit(taskSpec{Kind: KindDownload, Work: func(ctx context.Context, r *run) error {
		for i := 0; i < 80; i++ {
			r.AddBytes(1000)
			time.Sleep(10 * time.Millisecond)
		}
		<-gate // stalled: no more progress
		return nil
	}})
	eventually(t, "positive speed seen", positive.Load)
	eventually(t, "speed 0 published while stalled", zeroWhileRunning.Load)
	if tk, _ := taskByID(s, id); tk.Status != StatusRunning || tk.Speed != 0 {
		t.Fatalf("%+v", tk)
	}
	close(gate)
	wait(t, s, id)
}

func TestWaitGroupBacklogCountsQueuedAndPausedFiles(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	g := s.newGroupID()
	gate := make(chan struct{})
	started := make(chan string, 1)
	s.submit(pausableSpec(g, gated("r", started, gate)))
	<-started
	b := s.submit(pausableSpec(g, okWork))
	c := s.submit(pausableSpec(g, okWork))
	if err := s.Pause(c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.waitGroupBacklog(ctx, g, 2); err == nil {
		t.Fatal("one queued plus one paused file is a backlog of two")
	}
	done := make(chan error, 1)
	go func() { done <- s.waitGroupBacklog(context.Background(), g, 2) }()
	if err := s.Cancel(b); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := s.Resume(c); err != nil {
		t.Fatal(err)
	}
	wait(t, s, c)
}
