package app

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func pausableSpec(group string, work func(context.Context, *run) error) taskSpec {
	return taskSpec{Kind: KindUpload, Group: group, Work: work, Saved: &savedTransfer{Upload: &uploadJob{Key: "k"}}}
}

func statusOf(t *testing.T, s *Service, id string) Status {
	t.Helper()
	tk, ok := taskByID(s, id)
	if !ok {
		t.Fatalf("no task %s", id)
	}
	return tk.Status
}

func TestPauseAndResumeGroup(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	group := s.newGroupID()
	var runs atomic.Int32
	started := make(chan struct{}, 4)
	gate := make(chan struct{})
	work := func(ctx context.Context, r *run) error {
		runs.Add(1)
		r.AddBytes(10)
		started <- struct{}{}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a := s.submit(pausableSpec(group, work))
	b := s.submit(pausableSpec(group, work))
	<-started
	if !s.Pausable(group) {
		t.Fatal("group with a running transfer is not pausable")
	}
	if err := s.Pause(group); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both paused", func() bool { return statusOf(t, s, a) == StatusPaused && statusOf(t, s, b) == StatusPaused })
	if tk, _ := taskByID(s, a); tk.BytesDone != 0 || tk.Err != "" {
		t.Fatalf("paused task keeps progress or error: %+v", tk)
	}
	// A file the group's scan submits now starts out paused.
	c := s.submit(pausableSpec(group, work))
	if statusOf(t, s, c) != StatusPaused {
		t.Fatal("later member of a paused group is not paused")
	}
	if g, _ := s.TaskGroup(group); g.Pending != 3 {
		t.Fatalf("paused tasks must stay pending: %+v", g)
	}
	close(gate)
	if err := s.Resume(group); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b, c} {
		if tk := wait(t, s, id); tk.Status != StatusSucceeded {
			t.Fatalf("%s: %+v", id, tk)
		}
	}
	if runs.Load() != 4 { // a twice (interrupted by the pause), b and c once
		t.Fatalf("runs = %d", runs.Load())
	}
}

func TestPauseLeavesOtherTasksAlone(t *testing.T) {
	s := newTestService(t, nil)
	started := make(chan struct{})
	gate := make(chan struct{})
	id := s.submit(taskSpec{Kind: KindOther, Work: func(ctx context.Context, r *run) error {
		close(started)
		<-gate
		return nil
	}})
	<-started
	if s.Pausable(id) {
		t.Fatal("a non-transfer task is pausable")
	}
	if err := s.Pause(id); err != nil {
		t.Fatal(err)
	}
	if statusOf(t, s, id) != StatusRunning {
		t.Fatal("Pause stopped a non-transfer task")
	}
	close(gate)
	wait(t, s, id)
}

func TestCancelPausedAndResumeWhilePausing(t *testing.T) {
	s := newTestService(t, nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	work := func(ctx context.Context, r *run) error {
		started <- struct{}{}
		<-ctx.Done()
		<-release // still cleaning up when Resume arrives
		return ctx.Err()
	}
	id := s.submit(pausableSpec("", work))
	<-started
	if err := s.Pause(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(id); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-started // queued again and running
	if err := s.Pause(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused", func() bool { return statusOf(t, s, id) == StatusPaused })
	if err := s.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, s, id); tk.Status != StatusCanceled {
		t.Fatalf("%+v", tk)
	}
}

func TestPauseAllResumeAll(t *testing.T) {
	s := newTestService(t, nil)
	setLimits(t, s, 1, 1, 1)
	gate := make(chan struct{})
	work := func(ctx context.Context, r *run) error {
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ids := []string{s.submit(pausableSpec("", work)), s.submit(pausableSpec("", work))}
	s.PauseAll()
	eventually(t, "all paused", func() bool {
		return statusOf(t, s, ids[0]) == StatusPaused && statusOf(t, s, ids[1]) == StatusPaused
	})
	close(gate)
	s.ResumeAll()
	for _, id := range ids {
		if tk := wait(t, s, id); tk.Status != StatusSucceeded {
			t.Fatalf("%+v", tk)
		}
	}
}

func TestSaveAndRestoreTransfers(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	src := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(src, n), []byte("hello "+n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	group, err := e.s.Upload(UploadRequest{Dest: e.loc("b", "in/"), Paths: []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Pause(group); err != nil {
		t.Fatal(err)
	}
	paused := func(s *Service) int {
		n := 0
		for _, tk := range s.Tasks() {
			if tk.Status == StatusPaused {
				n++
			}
		}
		return n
	}
	eventually(t, "two paused files", func() bool { return paused(e.s) == 2 })
	if err := e.s.SaveTransfers(); err != nil {
		t.Fatal(err)
	}

	// A new session on the same configuration.
	s2, _ := newTestServiceDir(t, e.s.dir, e.s.osKr)
	n, err := s2.RestoreTransfers()
	if err != nil || n != 2 {
		t.Fatalf("restored %d: %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(e.s.dir, transfersFile)); !os.IsNotExist(err) {
		t.Fatal("transfers.json kept after restore")
	}
	if paused(s2) != 2 {
		t.Fatal("restored transfers are not paused")
	}
	s2.ResumeAll()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tk := range s2.Tasks() {
		if got, err := s2.waitTask(ctx, tk.ID); err != nil || got.Status != StatusSucceeded {
			t.Fatalf("%+v %v", got, err)
		}
	}
	for _, k := range []string{"in/a.txt", "in/b.txt"} {
		if _, ok := e.srv.Object("b", k); !ok {
			t.Errorf("%s not uploaded", k)
		}
	}
	// Nothing left: the file is removed rather than written empty.
	if err := s2.SaveTransfers(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.s.dir, transfersFile)); !os.IsNotExist(err) {
		t.Fatal("empty transfers.json written")
	}
}
