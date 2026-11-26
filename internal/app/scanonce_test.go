package app

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Entries a scan handles without a task (skipped, failed, folder
// placeholders) move its position: after a pause, or in a restored session,
// the scan never reports them a second time.

// scanOnceTree builds a folder whose walk is: five files (with the upload
// limit 1 and a backlog of 4 they fill the queue), a symbolic link (skipped),
// a file whose key is too long (failed), then more files: the scan reports
// the link and the long name and then waits at the backlog.
func scanOnceTree(t *testing.T) (root, prefix, longName string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "tree")
	for i := 0; i < 5; i++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("a%d.txt", i)), []byte("a"))
	}
	if err := os.Symlink(filepath.Join(root, "a0.txt"), filepath.Join(root, "b-link")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	longName = "c-" + strings.Repeat("x", 100)
	trWrite(t, filepath.Join(root, longName), []byte("c"))
	for i := 0; i < 3; i++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("d%d.txt", i)), []byte("d"))
	}
	// The other keys stay below maxKeyBytes, the long name goes over it.
	prefix = strings.Repeat("p", maxKeyBytes-len("tree/")-len(longName)+1-len("/")) + "/"
	return root, prefix, longName
}

func scanTaskOf(t *testing.T, s *Service, group string) Task {
	t.Helper()
	for _, tk := range s.Tasks() {
		if tk.GroupID == group && tk.Kind == KindOther {
			return tk
		}
	}
	t.Fatalf("no scan in group %s", group)
	return Task{}
}

// parkedAfterSkipAndFail waits until the scan reported the link and the long
// name and waits at the backlog.
func parkedAfterSkipAndFail(t *testing.T, s *Service, group string, held func() int32) {
	t.Helper()
	eventually(t, "the scan parked after the link and the long name", func() bool {
		p, _ := s.GroupProgress(group)
		var sc Task
		for _, tk := range s.Tasks() {
			if tk.GroupID == group && tk.Kind == KindOther {
				sc = tk
			}
		}
		return held() == 1 && p.FilesQueued == 4 && sc.ItemsSkipped == 1 && sc.ItemsFailed == 1
	})
}

func TestScanReportsHandledEntriesOnceAcrossPause(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	root, prefix, _ := scanOnceTree(t)
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", prefix), Paths: []string{root}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	parkedAfterSkipAndFail(t, e.s, g, held)
	if err := e.s.PauseGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused", func() bool { p, _ := e.s.GroupProgress(g); return p.Paused && !p.Pausable })
	e.srv.Inject(nil)
	if err := e.s.ResumeGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "done", func() bool { return len(e.s.TaskGroups()) == 0 })
	sc := scanTaskOf(t, e.s, g)
	if sc.ItemsSkipped != 1 || sc.ItemsFailed != 1 || sc.ItemsDone != 2 || sc.Items != 2 || len(e.s.TaskItems(sc.ID)) != 2 {
		t.Fatalf("%+v %+v", sc, e.s.TaskItems(sc.ID))
	}
	if sc.Status != StatusPartiallyFailed || sc.Err != "1 of 2 item(s) did not complete" {
		t.Fatalf("%+v", sc)
	}
	if p, _ := e.s.GroupProgress(g); p.Files != 8 || p.FilesSucceeded != 8 {
		t.Fatalf("%+v", p)
	}
}

func TestRestoredScanReportsHandledEntriesOnce(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	root, prefix, longName := scanOnceTree(t)
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", prefix), Paths: []string{root}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	parkedAfterSkipAndFail(t, e.s, g, held)
	interrupt(t, e.s, g)
	e.srv.Inject(nil)
	var saved savedTransfers
	if ok, err := readJSON(e.s.path(transfersFile), &saved); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if sc := saved.Groups[0].Scan.Upload; len(sc.Failed) != 1 || sc.Pos.Path[0] != longName {
		t.Fatalf("saved scan: %+v", sc)
	}

	s2, _ := newTestServiceDir(t, e.s.dir, e.s.osKr)
	if _, err := s2.RestoreTransfers(); err != nil {
		t.Fatal(err)
	}
	g2 := s2.TaskGroups()[0].ID
	s2.ResumeAll()
	eventually(t, "done", func() bool { return len(s2.TaskGroups()) == 0 })
	sc := scanTaskOf(t, s2, g2)
	// The link is not reported again; the long name once, by the restored scan.
	items := s2.TaskItems(sc.ID)
	if sc.ItemsSkipped != 0 || sc.ItemsFailed != 1 || len(items) != 1 || items[0].Key != filepath.Join(root, longName) {
		t.Fatalf("%+v %+v", sc, items)
	}
	if p, _ := s2.GroupProgress(g2); p.FilesSucceeded != 8 || p.FilesFailed != 0 {
		t.Fatalf("%+v", p)
	}
	// Retry rescans it (and it fails again for the same reason).
	rid, err := s2.Retry(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := wait(t, s2, rid)
	if got := s2.TaskItems(rid); r.ItemsFailed != 1 || len(got) != 1 || got[0].Key != filepath.Join(root, longName) {
		t.Fatalf("%+v %+v", r, got)
	}
}

func TestSkippedRootThenPausedSecondRoot(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	base := t.TempDir()
	target := filepath.Join(base, "target")
	trWrite(t, filepath.Join(target, "x"), []byte("x"))
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	second := filepath.Join(base, "second")
	for i := 0; i < 8; i++ {
		trWrite(t, filepath.Join(second, fmt.Sprintf("%d", i)), []byte("s"))
	}
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{link, second}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "parked in the second root", func() bool {
		p, _ := e.s.GroupProgress(g)
		return held() == 1 && p.FilesQueued == 4
	})
	if err := e.s.PauseGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused", func() bool { p, _ := e.s.GroupProgress(g); return p.Paused && !p.Pausable })
	e.srv.Inject(nil)
	if err := e.s.ResumeGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "done", func() bool { return len(e.s.TaskGroups()) == 0 })
	sc := scanTaskOf(t, e.s, g)
	if sc.ItemsSkipped != 1 || sc.Items != 1 || sc.Status != StatusSucceeded {
		t.Fatalf("%+v %+v", sc, e.s.TaskItems(sc.ID))
	}
	if p, _ := e.s.GroupProgress(g); p.FilesSucceeded != 8 {
		t.Fatalf("%+v", p)
	}
}

func TestDownloadPlaceholderReportedOnceAcrossPause(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	for i := 0; i < 5; i++ {
		e.srv.PutObject("b", fmt.Sprintf("d/f%d", i), []byte("f"), "", nil)
	}
	e.srv.PutObject("b", "d/g/", nil, "", nil) // placeholder: a directory, reported as succeeded
	for i := 0; i < 3; i++ {
		e.srv.PutObject("b", fmt.Sprintf("d/h%d", i), []byte("h"), "", nil)
	}
	held, _ := gateAfter(t, e.srv, 0, isObjectGet)
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"d/"}, DestDir: t.TempDir(), Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "parked after the placeholder", func() bool {
		p, _ := e.s.GroupProgress(g)
		return held() == 1 && p.FilesQueued == 4 && scanTaskOf(t, e.s, g).ItemsDone == 1
	})
	if err := e.s.PauseGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused", func() bool { p, _ := e.s.GroupProgress(g); return p.Paused && !p.Pausable })
	e.srv.Inject(nil)
	if err := e.s.ResumeGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "done", func() bool { return len(e.s.TaskGroups()) == 0 })
	if sc := scanTaskOf(t, e.s, g); sc.ItemsDone != 1 || sc.Items != 1 {
		t.Fatalf("%+v %+v", sc, e.s.TaskItems(sc.ID))
	}
	if p, _ := e.s.GroupProgress(g); p.FilesSucceeded != 8 {
		t.Fatalf("%+v", p)
	}
}
