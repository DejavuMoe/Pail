package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// gateAfter lets the first n requests that match through and holds the
// others until release; a held request whose client gave up is dropped.
func gateAfter(t *testing.T, srv *s3test.Server, n int32, match func(*http.Request) bool) (held func() int32, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var passed, waiting atomic.Int32
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if !match(r) || passed.Add(1) <= n {
			return nil
		}
		waiting.Add(1)
		defer waiting.Add(-1)
		select {
		case <-gate:
			return nil
		case <-r.Context().Done():
			return &s3test.Fault{DropConnection: true}
		}
	})
	return waiting.Load, release
}

func isObjectGet(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" && strings.Count(strings.Trim(r.URL.Path, "/"), "/") >= 1
}

func setBacklogFloor(s *Service, n int) {
	s.q.mu.Lock()
	s.q.backlogFloor = n
	s.q.mu.Unlock()
}

// listKeys returns every key of bucket.
func listKeys(t *testing.T, e *trEnv, bucket string) []string {
	t.Helper()
	var keys []string
	p := s3.NewListObjectsV2Paginator(e.sdk(), &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	for p.HasMorePages() {
		out, err := p.NextPage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	sort.Strings(keys)
	return keys
}

// interrupt cancels what is left of group in s (as quitting does: nothing
// further is transferred) after SaveTransfers, and waits until it stopped.
func interrupt(t *testing.T, s *Service, group string) {
	t.Helper()
	if err := s.SaveTransfers(); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelGroup(group); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the group to stop", func() bool { return len(s.TaskGroups()) == 0 })
}

// restoreAndRun restores the saved transfers into a fresh Service on the same
// configuration, resumes them and waits until they are done.
func restoreAndRun(t *testing.T, e *trEnv) *Service {
	t.Helper()
	s2, _ := newTestServiceDir(t, e.s.dir, e.s.osKr)
	n, err := s2.RestoreTransfers()
	if err != nil || n == 0 {
		t.Fatalf("restored %d: %v", n, err)
	}
	gs := s2.TaskGroups()
	if len(gs) != 1 || !gs[0].Paused || !gs[0].Scanning {
		t.Fatalf("restored groups: %+v", gs)
	}
	s2.ResumeAll()
	eventually(t, "restored group done", func() bool { return len(s2.TaskGroups()) == 0 })
	p, _ := s2.GroupProgress(gs[0].ID)
	if p.FilesFailed != 0 || p.FilesCanceled != 0 || p.Result.Failed != 0 {
		t.Fatalf("restored group: %+v", p)
	}
	return s2
}

func TestFolderUploadInterruptedMidScanResumesAfterRestart(t *testing.T) {
	e := newTrEnv(t)
	e.srv.CreateBucket("v", true)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 2)
	root := filepath.Join(t.TempDir(), "tree")
	want := map[string]string{"tree/top.txt": "top"}
	trWrite(t, filepath.Join(root, "top.txt"), []byte("top"))
	for _, d := range []string{"a", "b", "c"} {
		for i := 0; i < 8; i++ {
			k := fmt.Sprintf("tree/%s/f%02d.txt", d, i)
			want[k] = k
			trWrite(t, filepath.Join(root, d, fmt.Sprintf("f%02d.txt", i)), []byte(k))
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	want["tree/empty/"] = ""
	held, _ := gateAfter(t, e.srv, 5, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("v", ""), Paths: []string{root}, Conflict: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	// 5 done, 1 held, 4 queued (the backlog), the scan parked in the middle.
	eventually(t, "the scan parked mid-way", func() bool {
		p, _ := e.s.GroupProgress(g)
		return held() == 1 && p.FilesSucceeded == 5 && p.FilesQueued == 4 && p.Scanning
	})
	interrupt(t, e.s, g)
	e.srv.Inject(nil)
	before := map[string]bool{}
	for _, k := range listKeys(t, e, "v") {
		before[k] = true
	}
	if len(before) != 5 {
		t.Fatalf("%d objects before the restart", len(before))
	}
	e.srv.ResetRequests()

	restoreAndRun(t, e)
	keys := listKeys(t, e, "v")
	if len(keys) != len(want) {
		t.Fatalf("keys after restart: %v", keys)
	}
	for k, data := range want {
		if got := trObjectIn(t, e, "v", k); got != data {
			t.Errorf("%s = %q", k, got)
		}
		if vs := e.srv.Versions("v", k); len(vs) != 1 {
			t.Errorf("%s uploaded %d times", k, len(vs))
		}
	}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodGet && !strings.Contains(r.RawQuery, "list-type") {
			t.Errorf("object read after the restart: %s %s?%s", r.Method, r.Path, r.RawQuery)
		}
		if r.Method == http.MethodHead && before[strings.TrimPrefix(r.Path, "/v/")] {
			t.Errorf("completed file examined again: %s", r.Path)
		}
	}
}

func trObjectIn(t *testing.T, e *trEnv, bucket, key string) string {
	t.Helper()
	b, ok := e.srv.Object(bucket, key)
	if !ok {
		t.Fatalf("object %q does not exist", key)
	}
	return string(b)
}

func TestPrefixDownloadInterruptedMidScanResumesAfterRestart(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 2)
	defer func(n int32) { bkPageSize = n }(bkPageSize)
	bkPageSize = 4 // the listing resumes on a later page
	keys := []string{"dir/top.txt"}
	for _, d := range []string{"a", "b", "c"} {
		for i := 0; i < 8; i++ {
			keys = append(keys, fmt.Sprintf("dir/%s/f%02d.txt", d, i))
		}
	}
	for _, k := range keys {
		e.srv.PutObject("b", k, []byte(k), "", nil)
	}
	dest := t.TempDir()
	held, _ := gateAfter(t, e.srv, 5, isObjectGet)
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"dir/"}, DestDir: dest})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the scan parked mid-way", func() bool {
		p, _ := e.s.GroupProgress(g)
		return held() == 1 && p.FilesSucceeded == 5 && p.FilesQueued == 4 && p.Scanning
	})
	interrupt(t, e.s, g)
	e.srv.Inject(nil)
	done := map[string]bool{}
	for _, f := range trTree(t, dest) {
		done[f] = true
	}
	if len(done) != 5 {
		t.Fatalf("%d files before the restart: %v", len(done), done)
	}
	e.srv.ResetRequests()

	restoreAndRun(t, e)
	got := trTree(t, dest)
	if len(got) != len(keys) {
		t.Fatalf("files after restart: %v", got)
	}
	for _, k := range keys {
		if s := trRead(t, filepath.Join(dest, filepath.FromSlash(k))); s != k {
			t.Errorf("%s = %q", k, s)
		}
	}
	gets := map[string]int{}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodGet && !strings.Contains(r.RawQuery, "list-type") {
			gets[strings.TrimPrefix(r.Path, "/b/")]++
		}
	}
	for _, k := range keys {
		switch {
		case done[k] && gets[k] != 0:
			t.Errorf("%s downloaded again", k)
		case !done[k] && gets[k] != 1:
			t.Errorf("%s downloaded %d times after the restart", k, gets[k])
		}
	}
}

func TestRestoreReadsFormat1(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "remote.txt", []byte("remote"), "", nil)
	src := filepath.Join(t.TempDir(), "local.txt")
	trWrite(t, src, []byte("local"))
	dest := t.TempDir()
	// As written by v0.7.x: per-file jobs, no group description, no scan.
	old := fmt.Sprintf(`{"format":1,"groups":[{"jobs":[{"upload":{"Dest":{"accountId":%q,"bucket":"b","prefix":"in/"},"Local":%q,"Key":"in/local.txt","Policy":"rename","NoRules":false,"Folder":false,"Sync":null}},`+
		`{"download":{"Src":{"accountId":%q,"bucket":"b","prefix":""},"Key":"remote.txt","VersionID":"","ETag":"\"x\"","Size":6,"Modified":"2024-01-02T03:04:05Z","DestDir":%q,"Rel":"remote.txt","Policy":"rename","Decode":"","PasswordRef":"","MaxDecoded":0,"Note":"","Sync":null}}]}]}`,
		e.acc.ID, src, e.acc.ID, dest)
	if err := os.WriteFile(filepath.Join(e.s.dir, transfersFile), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, _ := newTestServiceDir(t, e.s.dir, e.s.osKr)
	n, err := s2.RestoreTransfers()
	if err != nil || n != 2 {
		t.Fatalf("restored %d: %v", n, err)
	}
	gs := s2.TaskGroups()
	if len(gs) != 1 || !gs[0].Paused || gs[0].Scanning || gs[0].Files != 2 || gs[0].Title != "Upload 2 files" {
		t.Fatalf("%+v", gs)
	}
	s2.ResumeAll()
	eventually(t, "done", func() bool { return len(s2.TaskGroups()) == 0 })
	if got := trObject(t, e, "in/local.txt"); got != "local" {
		t.Fatal(got)
	}
	if got := trRead(t, filepath.Join(dest, "remote.txt")); got != "remote" {
		t.Fatal(got)
	}
}

func TestSaveTransfersWritesFormat2WithScan(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	root := filepath.Join(t.TempDir(), "photos")
	for i := 0; i < 6; i++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("%d.jpg", i)), []byte("x"))
	}
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", "p/"), Paths: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "parked", func() bool { p, _ := e.s.GroupProgress(g); return held() == 1 && p.FilesQueued == 4 })
	interrupt(t, e.s, g)
	var saved savedTransfers
	if ok, err := readJSON(e.s.path(transfersFile), &saved); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if saved.Format != 2 || len(saved.Groups) != 1 {
		t.Fatalf("%+v", saved)
	}
	sg := saved.Groups[0]
	if len(sg.Jobs) != 5 || sg.Info == nil || sg.Info.Title != "Upload photos" || sg.Scan == nil || sg.Scan.Upload == nil {
		b, _ := json.Marshal(sg)
		t.Fatalf("%s", b)
	}
	if pos := sg.Scan.Upload.Pos; pos.Next != 0 || len(pos.Path) != 1 || pos.Path[0] != "4.jpg" || pos.Files != 5 {
		t.Fatalf("%+v", pos)
	}
}

func TestUploadScanResumesAtPosition(t *testing.T) {
	e := newTrEnv(t)
	root := filepath.Join(t.TempDir(), "r")
	for _, p := range []string{"a/1", "b/1", "b/3", "c/1", "d"} {
		trWrite(t, filepath.Join(root, filepath.FromSlash(p)), []byte(p))
	}
	// Changed since the position (b/2) was saved: b/0 appeared behind it, b/3
	// ahead of it; b/2 itself is gone.
	trWrite(t, filepath.Join(root, "b", "0"), []byte("new behind"))
	st := newScanState()
	st.restore(savedScanPos{Next: 0, Path: []string{"b", "2"}, Files: 3}, nil)
	g := e.s.newGroupID()
	id := e.s.submit(e.s.uploadScanSpecFrom(g, e.loc("b", ""), []scanRoot{{Local: root, Key: "r"}}, ConflictOverwrite, true, "Scan", st))
	tasks := trWaitGroup(t, e.s, g)
	trExpect(t, tasks, StatusSucceeded)
	var got []string
	for _, tk := range trKind(tasks, KindUpload) {
		got = append(got, tk.Title)
	}
	if want := []string{"Upload 3", "Upload 1", "Upload d"}; !trSameStrings(got, want) {
		t.Fatalf("submitted %v, want %v", got, want)
	}
	_ = id
}

func TestPauseAllWhileOnlyTheScanRuns(t *testing.T) {
	e := newTrEnv(t)
	for i := 0; i < 3; i++ {
		e.srv.PutObject("b", fmt.Sprintf("d/%d", i), []byte("x"), "", nil)
	}
	held, release := blockRequests(t, e.srv, func(r *http.Request) bool { return r.URL.Query().Get("list-type") == "2" })
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"d/"}, DestDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "listing held", func() bool { return held() > 0 })
	if !e.s.Pausable(g) {
		t.Fatal("a group whose scan runs is not pausable")
	}
	e.s.PauseAll()
	eventually(t, "scan paused", func() bool { p, _ := e.s.GroupProgress(g); return p.Paused && !p.Pausable })
	release()
	time.Sleep(200 * time.Millisecond)
	if p, _ := e.s.GroupProgress(g); p.Files != 0 {
		t.Fatalf("files started in a paused group: %+v", p)
	}
	e.s.ResumeAll()
	tasks := trWaitGroup(t, e.s, g)
	trExpect(t, tasks, StatusSucceeded)
	if p, _ := e.s.GroupProgress(g); p.FilesSucceeded != 3 || p.Files != 3 {
		t.Fatalf("%+v", p)
	}
}

func TestBulkTaskStartsWhileScansAreParked(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	e.srv.PutObject("b", "gone.txt", []byte("x"), "", nil)
	var groups []string
	held, release := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	for i := 0; i < 2; i++ {
		root := filepath.Join(t.TempDir(), fmt.Sprintf("f%d", i))
		for j := 0; j < 8; j++ {
			trWrite(t, filepath.Join(root, fmt.Sprintf("%d", j)), []byte("x"))
		}
		g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", fmt.Sprintf("u%d/", i)), Paths: []string{root}})
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, g)
	}
	eventually(t, "both scans parked", func() bool {
		for _, g := range groups {
			if p, _ := e.s.GroupProgress(g); !p.Scanning || p.FilesQueued < 4 {
				return false
			}
		}
		return held() == 1
	})
	id, err := e.s.Delete(Selection{Location: e.loc("b", ""), Keys: []string{"gone.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, e.s, id); tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
	// A paused scan holds nothing either.
	if err := e.s.PauseGroup(groups[0]); err != nil {
		t.Fatal(err)
	}
	release()
	if err := e.s.ResumeGroup(groups[0]); err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		trExpect(t, trWaitGroup(t, e.s, g), StatusSucceeded)
		// The paused file may have been committed just before its pause; it
		// is then found identical and skipped.
		if p, _ := e.s.GroupProgress(g); p.FilesSucceeded+p.FilesSkipped != 8 || p.Files != 8 {
			t.Fatalf("%+v", p)
		}
	}
}

func TestCancelPausedScanReportsUnprocessedRoots(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	roots := []string{filepath.Join(t.TempDir(), "x"), filepath.Join(t.TempDir(), "y")}
	for _, r := range roots {
		for j := 0; j < 6; j++ {
			trWrite(t, filepath.Join(r, fmt.Sprintf("%d", j)), []byte("x"))
		}
	}
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: roots})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "parked", func() bool { p, _ := e.s.GroupProgress(g); return held() == 1 && p.FilesQueued == 4 })
	if err := e.s.PauseGroup(g); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused", func() bool { p, _ := e.s.GroupProgress(g); return p.Paused && !p.Pausable })
	if err := e.s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	var scan Task
	eventually(t, "scan canceled", func() bool {
		for _, tk := range e.s.Tasks() {
			if tk.GroupID == g && tk.Kind == KindOther {
				scan = tk
				return tk.Status == StatusCanceled
			}
		}
		return false
	})
	if scan.ItemsUnprocessed != 2 {
		t.Fatalf("%+v %+v", scan, e.s.TaskItems(scan.ID))
	}
	e.srv.Inject(nil)
	rid, err := e.s.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := wait(t, e.s, rid)
	tasks := trWaitGroup(t, e.s, r.GroupID)
	trExpect(t, tasks, StatusSucceeded)
	if n := len(trKind(tasks, KindUpload)); n != 12 {
		t.Fatalf("retry uploaded %d files", n)
	}
}

func TestRestoredScanReportsEarlierFailures(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 1)
	setBacklogFloor(e.s, 1)
	missing := filepath.Join(t.TempDir(), "missing")
	root := filepath.Join(t.TempDir(), "r")
	for j := 0; j < 8; j++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("%d", j)), []byte("x"))
	}
	held, _ := gateAfter(t, e.srv, 0, func(r *http.Request) bool { return r.Method == http.MethodPut })
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{missing, root}})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "parked", func() bool { p, _ := e.s.GroupProgress(g); return held() == 1 && p.FilesQueued == 4 })
	interrupt(t, e.s, g)
	e.srv.Inject(nil)
	trWrite(t, filepath.Join(missing, "late.txt"), []byte("late")) // the retry finds it now

	s2, _ := newTestServiceDir(t, e.s.dir, e.s.osKr)
	if _, err := s2.RestoreTransfers(); err != nil {
		t.Fatal(err)
	}
	s2.ResumeAll()
	eventually(t, "done", func() bool { return len(s2.TaskGroups()) == 0 })
	var scan Task
	for _, tk := range s2.Tasks() {
		if tk.Kind == KindOther {
			scan = tk
		}
	}
	if scan.Status != StatusPartiallyFailed || scan.ItemsFailed != 1 {
		t.Fatalf("%+v %+v", scan, s2.TaskItems(scan.ID))
	}
	rid, err := s2.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := wait(t, s2, rid)
	trExpect(t, trWaitGroup(t, s2, r.GroupID), StatusSucceeded)
	if got := trObject(t, e, "missing/late.txt"); got != "late" {
		t.Fatal(got)
	}
	for j := 0; j < 8; j++ {
		trObject(t, e, fmt.Sprintf("r/%d", j))
	}
}
