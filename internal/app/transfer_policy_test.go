package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

// ---- helpers --------------------------------------------------------------------------

func foldsCase() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// isObjectPut matches a single-part PUT of any object (not a part upload).
func isObjectPut(r *http.Request) bool {
	return r.Method == http.MethodPut && !r.URL.Query().Has("partNumber") && !r.URL.Query().Has("uploads")
}

// requestGate blocks the matching requests after the first `after` of them until it
// is opened (cleanup opens it). Blocked requests are then answered with a 500
// so that nothing is committed. The server cannot notice a client that went
// away while the handler is parked, hence a gate instead of a long Delay.
type requestGate struct {
	match   func(*http.Request) bool
	after   int32
	n       atomic.Int32
	hit     chan struct{}
	release chan struct{}
	once    sync.Once
	hitOnce sync.Once
}

func newRequestGate(t testing.TB, e *trEnv, after int32, match func(*http.Request) bool) *requestGate {
	g := &requestGate{match: match, after: after, hit: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.open)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if g.match(r) && g.n.Add(1) > g.after {
			g.hitOnce.Do(func() { close(g.hit) })
			<-g.release
			return &s3test.Fault{Status: 500, Code: "InternalError", Message: "gate released"}
		}
		return nil
	})
	return g
}

func (g *requestGate) open() { g.once.Do(func() { close(g.release) }) }

func countRequests(e *trEnv, match func(s3test.Request) bool) int {
	n := 0
	for _, r := range e.srv.Requests() {
		if match(r) {
			n++
		}
	}
	return n
}

// watchGroup waits (up to limit) until the group's scan and all its tasks are
// final and returns the largest number of QUEUED tasks seen meanwhile.
func watchGroup(t testing.TB, s *Service, group string, limit time.Duration) (maxQueued int) {
	t.Helper()
	deadline := time.Now().Add(limit)
	scan := false // sticky: finished tasks (the old scan first) are evicted after 1000
	for time.Now().Before(deadline) {
		queued, unfinished := 0, false
		for _, tk := range s.Tasks() {
			if tk.GroupID != group {
				continue
			}
			if tk.Status == StatusQueued {
				queued++
			}
			if !tk.Status.Finished() {
				unfinished = true
			}
			if tk.Kind == KindOther {
				scan = true
			}
		}
		if queued > maxQueued {
			maxQueued = queued
		}
		if scan && !unfinished {
			return maxQueued
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("group %s did not finish within %v", group, limit)
	return
}

func queuedCount(s *Service, group string) int {
	n := 0
	for _, tk := range s.Tasks() {
		if tk.GroupID == group && tk.Status == StatusQueued {
			n++
		}
	}
	return n
}

func mTimeMeta(t testing.TB, p string) string {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UTC().Format(time.RFC3339Nano)
}

// ---- case-insensitive collisions --------------------------------------------------

func TestDownloadCaseInsensitiveCollision(t *testing.T) {
	if !foldsCase() {
		t.Skip("case-insensitive collisions only exist on Windows and macOS")
	}
	for _, policy := range []ConflictPolicy{ConflictOverwrite, ConflictNewer, ConflictDifferentSize} {
		t.Run(string(policy), func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "dir/A.txt", []byte("UPPER"), "", nil)
			e.srv.PutObject("b", "dir/a.txt", []byte("lower!"), "", nil)
			dest := t.TempDir()
			tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"dir/"}, DestDir: dest, Conflict: policy})
			trExpect(t, tasks, StatusSucceeded)
			if got := trTree(t, dest); !trSameStrings(got, []string{"dir/A.txt", "dir/a (1).txt"}) {
				t.Fatalf("files %v", got)
			}
			if trRead(t, filepath.Join(dest, "dir", "A.txt")) != "UPPER" || trRead(t, filepath.Join(dest, "dir", "a (1).txt")) != "lower!" {
				t.Fatal("content mixed up")
			}
			items := trItems(e.s, trKind(tasks, KindDownload))
			if it := items["dir/a.txt"]; it.Status != ItemSucceeded || it.Err != "renamed: case-insensitive name collision" {
				t.Errorf("item for a.txt: %+v", it)
			}
			if it := items["dir/A.txt"]; it.Err != "" {
				t.Errorf("item for A.txt must carry no note: %+v", it)
			}
		})
	}
}

func TestDownloadSameKeyTwiceIsNotACollision(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "dir/a.txt", []byte("one"), "", nil)
	dest := t.TempDir()
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"dir/", "dir/a.txt"}, DestDir: dest, Conflict: ConflictSkip})
	trExpect(t, tasks, StatusSucceeded)
	if got := trTree(t, dest); !trSameStrings(got, []string{"dir/a.txt"}) {
		t.Fatalf("files %v", got)
	}
}

// The target is absent when the decision is made and appears during the
// transfer: the decision must be made again, never an overwrite of the racer.
func TestDownloadAbsentThenAppears(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  ConflictPolicy
		racer   string
		want    string
		item    string
		reason  string
		mtimeFn func() time.Time
	}{
		{"newer keeps a newer racer", ConflictNewer, "racer", "racer", ItemSkipped, "local file is not older", nil},
		{"different_size keeps a same-size racer", ConflictDifferentSize, "racer!", "racer!", ItemSkipped, "same size", nil},
		{"different_size replaces a different-size racer", ConflictDifferentSize, "rac", "remote", ItemSucceeded, "", nil},
		{"overwrite replaces the racer", ConflictOverwrite, "racer", "remote", ItemSucceeded, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "f.txt", []byte("remote"), "", nil)
			dest := t.TempDir()
			e.srv.Inject(trRaceOnce(func(r *http.Request) bool {
				return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/f.txt")
			}, func() { os.WriteFile(filepath.Join(dest, "f.txt"), []byte(tc.racer), 0o644) }))
			tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"f.txt"}, DestDir: dest, Conflict: tc.policy})
			trExpect(t, tasks, StatusSucceeded)
			if got := trRead(t, filepath.Join(dest, "f.txt")); got != tc.want {
				t.Errorf("f.txt = %q, want %q", got, tc.want)
			}
			it := e.s.TaskItems(trKind(tasks, KindDownload)[0].ID)
			if len(it) != 1 || it[0].Status != tc.item || it[0].Err != tc.reason {
				t.Errorf("items %+v, want %s %q", it, tc.item, tc.reason)
			}
		})
	}
}

// ---- backpressure -----------------------------------------------------------------

func TestUploadScanBackpressure(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 2, 2, 2)
	const n = 2000
	root := filepath.Join(t.TempDir(), "many")
	for i := 0; i < n; i++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("f%04d.txt", i)), []byte("x"))
	}
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{root}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	maxQueued := watchGroup(t, e.s, g, 90*time.Second)
	if bound := 64; maxQueued > bound {
		t.Errorf("up to %d queued tasks of one scan, bound is %d", maxQueued, bound)
	}
	for i := 0; i < n; i++ {
		if _, ok := e.srv.Object("b", fmt.Sprintf("many/f%04d.txt", i)); !ok {
			t.Fatalf("object %d missing", i)
		}
	}
}

func TestDownloadScanBackpressure(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 2, 8, 2)
	const n = 2000
	for i := 0; i < n; i++ {
		e.srv.PutObject("b", fmt.Sprintf("many/f%04d.txt", i), []byte("x"), "", nil)
	}
	dest := t.TempDir()
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"many/"}, DestDir: dest, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	// 4 x the download limit (8) is below the 64 floor.
	maxQueued := watchGroup(t, e.s, g, 120*time.Second)
	if bound := 64; maxQueued > bound {
		t.Errorf("up to %d queued tasks of one scan, bound is %d", maxQueued, bound)
	}
	if got := trTree(t, dest); len(got) != n {
		t.Fatalf("%d files downloaded, want %d", len(got), n)
	}
}

func TestUploadScanCancelDuringBackpressure(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 1, 1, 2)
	newRequestGate(t, e, 0, isObjectPut)
	base := t.TempDir()
	r1, r2, r3 := filepath.Join(base, "r1"), filepath.Join(base, "r2"), filepath.Join(base, "r3")
	for i := 0; i < 200; i++ {
		trWrite(t, filepath.Join(r1, fmt.Sprintf("f%03d.txt", i)), []byte("x"))
	}
	for i := 0; i < 3; i++ {
		trWrite(t, filepath.Join(r2, fmt.Sprintf("f%d.txt", i)), []byte("x"))
		trWrite(t, filepath.Join(r3, fmt.Sprintf("f%d.txt", i)), []byte("x"))
	}
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{r1, r2, r3}, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the backlog to fill", func() bool { return queuedCount(e.s, g) >= 60 })
	time.Sleep(150 * time.Millisecond) // the scan must now be parked, not racing ahead
	if q := queuedCount(e.s, g); q > 64 {
		t.Fatalf("%d queued tasks, bound is 64", q)
	}
	start := time.Now()
	if err := e.s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	var scan Task
	eventually(t, "the scan to end", func() bool {
		for _, tk := range e.s.Tasks() {
			if tk.GroupID == g && tk.Kind == KindOther {
				scan = tk
				return tk.Status.Finished()
			}
		}
		return false
	})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("scan needed %v to notice the cancel", d)
	}
	if scan.Status != StatusCanceled {
		t.Fatalf("scan: %+v", scan)
	}
	unprocessed := map[string]bool{}
	for _, it := range e.s.TaskItems(scan.ID) {
		if it.Status == ItemUnprocessed {
			unprocessed[it.Key] = true
		}
	}
	if !unprocessed[r2] || !unprocessed[r3] {
		t.Errorf("remaining roots must be unprocessed: %v", unprocessed)
	}
}

// ---- retry must not duplicate under the rename policy ------------------------------

func TestUploadRetryAfterCancelNoDuplicates(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 2, 2, 2)
	const n = 120
	root := filepath.Join(t.TempDir(), "data")
	for i := 0; i < n; i++ {
		trWrite(t, filepath.Join(root, fmt.Sprintf("f%03d.txt", i)), []byte(fmt.Sprintf("content %d", i)))
	}
	gate := newRequestGate(t, e, 10, isObjectPut)
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{root}, Conflict: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.hit:
	case <-time.After(20 * time.Second):
		t.Fatal("uploads never reached the blocking point")
	}
	if err := e.s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	tasks := trWaitGroup(t, e.s, g)
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusCanceled {
		t.Fatalf("the scan must be canceled mid-folder: %+v", scan)
	}
	gate.open()
	e.srv.Inject(nil)
	id, err := e.s.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, e.s, id)
	tk, _ := taskByID(e.s, id)
	watchGroup(t, e.s, tk.GroupID, 60*time.Second)
	skipped := 0
	for _, x := range e.s.Tasks() {
		if x.GroupID == g && x.Kind == KindUpload {
			for _, it := range e.s.TaskItems(x.ID) {
				if it.Status == ItemSkipped && it.Err == "identical file exists" {
					skipped++
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("data/f%03d.txt", i)
		if got := trObject(t, e, key); got != fmt.Sprintf("content %d", i) {
			t.Errorf("%s = %q", key, got)
		}
		trMissing(t, e, fmt.Sprintf("data/f%03d (1).txt", i))
	}
	if skipped == 0 {
		t.Error("no file was reported as an identical, skipped file")
	}
}

func TestDownloadRetryAfterCancelNoDuplicates(t *testing.T) {
	e := newTrEnv(t)
	setLimits(t, e.s, 2, 2, 2)
	const n = 120
	for i := 0; i < n; i++ {
		e.srv.PutObject("b", fmt.Sprintf("d/f%03d.txt", i), []byte(fmt.Sprintf("content %d", i)), "", nil)
	}
	gate := newRequestGate(t, e, 10, func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ".txt")
	})
	dest := t.TempDir()
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"d/"}, DestDir: dest, Conflict: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.hit:
	case <-time.After(20 * time.Second):
		t.Fatal("downloads never reached the blocking point")
	}
	if err := e.s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	tasks := trWaitGroup(t, e.s, g)
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusCanceled {
		t.Fatalf("the scan must be canceled mid-folder: %+v", scan)
	}
	gate.open()
	e.srv.Inject(nil)
	id, err := e.s.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, e.s, id)
	tk, _ := taskByID(e.s, id)
	watchGroup(t, e.s, tk.GroupID, 60*time.Second)
	got := trTree(t, dest)
	if len(got) != n {
		t.Fatalf("%d files, want %d: %v", len(got), n, got)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("d/f%03d.txt", i)
		if c := trRead(t, filepath.Join(dest, filepath.FromSlash(name))); c != fmt.Sprintf("content %d", i) {
			t.Errorf("%s = %q", name, c)
		}
	}
}

func TestDownloadRenameKeepsDifferentAndSkipsIdentical(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "f.txt", []byte("remote"), "", nil)
	info, err := e.s.Head(testCtx(t), e.loc("b", ""), "f.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	// Same size but another time, and the same time but another size: not identical.
	trWrite(t, filepath.Join(dest, "f.txt"), []byte("local!"))
	trChtimes(t, filepath.Join(dest, "f.txt"), info.Modified.Add(-time.Hour))
	trWrite(t, filepath.Join(dest, "f (1).txt"), []byte("other size"))
	trChtimes(t, filepath.Join(dest, "f (1).txt"), info.Modified)
	// An identical copy under a numbered name: found while walking the names.
	trWrite(t, filepath.Join(dest, "f (2).txt"), []byte("remote"))
	trChtimes(t, filepath.Join(dest, "f (2).txt"), info.Modified.Add(time.Second))
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"f.txt"}, DestDir: dest, Conflict: ConflictRename})
	trExpect(t, tasks, StatusSucceeded)
	if got := trTree(t, dest); !trSameStrings(got, []string{"f (1).txt", "f (2).txt", "f.txt"}) {
		t.Fatalf("files %v", got)
	}
	it := e.s.TaskItems(trKind(tasks, KindDownload)[0].ID)
	if len(it) != 1 || it[0].Status != ItemSkipped || it[0].Err != "identical file exists" {
		t.Fatalf("items %+v", it)
	}
}

// ---- truncated listing without a token ----------------------------------------------

func TestScanListTruncatedWithoutTokenFails(t *testing.T) {
	srv := s3test.New()
	t.Cleanup(srv.Close)
	srv.CreateBucket("b", false)
	srv.PutObject("b", "a/x.txt", []byte("x"), "", nil)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, srv.URL+r.RequestURI, r.Body)
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if r.URL.Query().Get("list-type") == "2" {
			body = bytes.ReplaceAll(body, []byte("<IsTruncated>false</IsTruncated>"), []byte("<IsTruncated>true</IsTruncated>"))
		}
		for k, v := range resp.Header {
			if k != "Content-Length" {
				w.Header()[k] = v
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}))
	t.Cleanup(proxy.Close)
	s := newTestService(t, nil)
	acc, err := s.SaveAccount(staticAccount("mem", proxy.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Download(DownloadRequest{Source: Location{AccountID: acc.ID, Bucket: "b"}, Keys: []string{"a/"}, DestDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tasks := trWaitGroup(t, s, g)
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusPartiallyFailed {
		t.Fatalf("scan: %+v", scan)
	}
	var failed bool
	for _, it := range s.TaskItems(scan.ID) {
		if it.Status == ItemFailed && strings.Contains(it.Err, "truncated listing without a continuation token") {
			failed = true
		}
	}
	if !failed {
		t.Errorf("items %+v", s.TaskItems(scan.ID))
	}
}

// ---- write-only credentials ----------------------------------------------------------

func denyHead(e *trEnv) {
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodHead {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
		}
		return nil
	})
}

func uploadOne(t *testing.T, e *trEnv, name string, data []byte, policy ConflictPolicy) []Task {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	trWrite(t, src, data)
	return e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: policy})
}

func TestUploadWriteOnlyCredentials(t *testing.T) {
	big := trBigData(6 << 20)
	small := []byte("ours")
	t.Run("skip, absent", func(t *testing.T) {
		e := newTrEnv(t)
		denyHead(e)
		trExpect(t, uploadOne(t, e, "f.txt", small, ConflictSkip), StatusSucceeded)
		if trObject(t, e, "f.txt") != "ours" {
			t.Fatal("not uploaded")
		}
		var cond bool
		for _, r := range e.srv.Requests() {
			if r.Method == http.MethodPut && r.Header.Get("If-None-Match") == "*" {
				cond = true
			}
		}
		if !cond {
			t.Error("the blind write must be create-only")
		}
	})
	for _, tc := range []struct {
		name string
		data []byte
	}{{"single", small}, {"multipart", big}} {
		t.Run("skip, exists, "+tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
			denyHead(e)
			tasks := uploadOne(t, e, "f.txt", tc.data, ConflictSkip)
			trExpect(t, tasks, StatusSucceeded)
			if trObject(t, e, "f.txt") != "old" {
				t.Fatal("existing object was overwritten")
			}
			it := e.s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(it) != 1 || it[0].Status != ItemSkipped || it[0].Err != "destination exists" {
				t.Fatalf("items %+v", it)
			}
		})
		t.Run("rename, exists, "+tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
			e.srv.PutObject("b", "f (1).txt", []byte("old1"), "", nil)
			denyHead(e)
			tasks := uploadOne(t, e, "f.txt", tc.data, ConflictRename)
			trExpect(t, tasks, StatusSucceeded)
			if trObject(t, e, "f.txt") != "old" || trObject(t, e, "f (1).txt") != "old1" {
				t.Fatal("existing object was overwritten")
			}
			if trObject(t, e, "f (2).txt") != string(tc.data) {
				t.Fatal("the numbered upload is missing")
			}
		})
	}
	t.Run("rename gives up after 5 attempts", func(t *testing.T) {
		e := newTrEnv(t)
		e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
		for i := 1; i <= 4; i++ {
			e.srv.PutObject("b", fmt.Sprintf("f (%d).txt", i), []byte("old"), "", nil)
		}
		denyHead(e)
		tasks := uploadOne(t, e, "f.txt", small, ConflictRename)
		up := trKind(tasks, KindUpload)[0]
		if up.Status != StatusFailed {
			t.Fatalf("%+v", up)
		}
		trMissing(t, e, "f (5).txt")
		puts := countRequests(e, func(r s3test.Request) bool { return r.Method == http.MethodPut })
		if puts != 5 {
			t.Errorf("%d PUT requests, want 5", puts)
		}
	})
	t.Run("known unsupported conditional writes fail with the denial", func(t *testing.T) {
		e := newTrEnv(t)
		e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
		e.s.markCondWritesUnsupported(e.acc.ID)
		denyHead(e)
		up := trKind(uploadOne(t, e, "f.txt", small, ConflictSkip), KindUpload)[0]
		if up.Status != StatusFailed || !strings.Contains(up.Err, "HeadObject") {
			t.Fatalf("%+v", up)
		}
		if trObject(t, e, "f.txt") != "old" {
			t.Fatal("overwritten")
		}
		if n := countRequests(e, func(r s3test.Request) bool { return r.Method == http.MethodPut }); n != 0 {
			t.Errorf("%d PUT requests", n)
		}
	})
	t.Run("provider rejecting the header never gets an unconditional write", func(t *testing.T) {
		e := newTrEnv(t)
		e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
		e.srv.Inject(func(r *http.Request) *s3test.Fault {
			if r.Method == http.MethodHead {
				return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
			}
			if isObjectPut(r) {
				return &s3test.Fault{Status: 501, Code: "NotImplemented", Message: "not implemented"}
			}
			return nil
		})
		up := trKind(uploadOne(t, e, "f.txt", small, ConflictSkip), KindUpload)[0]
		if up.Status != StatusFailed || !strings.Contains(up.Err, "HeadObject") {
			t.Fatalf("%+v", up)
		}
		if trObject(t, e, "f.txt") != "old" {
			t.Fatal("overwritten")
		}
		if n := countRequests(e, func(r s3test.Request) bool { return r.Method == http.MethodPut }); n != 1 {
			t.Errorf("%d PUT requests, want 1", n)
		}
		if !e.s.condWritesUnsupported(e.acc.ID) {
			t.Error("the rejection was not recorded")
		}
	})
	t.Run("newer still fails when HEAD is denied", func(t *testing.T) {
		e := newTrEnv(t)
		e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
		denyHead(e)
		up := trKind(uploadOne(t, e, "f.txt", small, ConflictNewer), KindUpload)[0]
		if up.Status != StatusFailed {
			t.Fatalf("%+v", up)
		}
		if trObject(t, e, "f.txt") != "old" {
			t.Fatal("overwritten")
		}
	})
}

// ---- a replayed create-only write ------------------------------------------------------

func TestUploadReplayedCreateOnlyIsSuccess(t *testing.T) {
	big := trBigData(6 << 20)
	small := []byte("ours")
	for _, tc := range []struct {
		name   string
		data   []byte
		match  func(*http.Request) bool
		policy ConflictPolicy
	}{
		{"single skip", small, isPut("r.bin"), ConflictSkip},
		{"single rename", small, isPut("r.bin"), ConflictRename},
		{"multipart skip", big, isComplete, ConflictSkip},
		{"multipart rename", big, isComplete, ConflictRename},
	} {
		t.Run(tc.name+"/same metadata", func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "r.bin")
			trWrite(t, src, tc.data)
			mt := time.Date(2024, 1, 2, 3, 4, 5, 600000000, time.UTC)
			trChtimes(t, src, mt)
			meta := map[string]string{mtimeMetaKey: mTimeMeta(t, src)}
			e.srv.Inject(trRaceOnce(tc.match, func() { e.srv.PutObject("b", "r.bin", tc.data, "", meta) }))
			tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: tc.policy})
			trExpect(t, tasks, StatusSucceeded)
			it := e.s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(it) != 1 || it[0].Status != ItemSucceeded || it[0].Key != "r.bin" || it[0].Err != "committed; response was retried" {
				t.Fatalf("items %+v", it)
			}
			trMissing(t, e, "r (1).bin")
		})
	}
	for name, meta := range map[string]map[string]string{
		"other mtime": {mtimeMetaKey: "2001-01-01T00:00:00Z"},
		"no mtime":    nil,
		"other size":  {mtimeMetaKey: "", syMetaSize: "1"},
	} {
		t.Run("single skip/"+name, func(t *testing.T) {
			e := newTrEnv(t)
			src := filepath.Join(t.TempDir(), "r.bin")
			trWrite(t, src, small)
			if _, ok := meta[mtimeMetaKey]; ok && meta[mtimeMetaKey] == "" {
				meta[mtimeMetaKey] = mTimeMeta(t, src)
			}
			e.srv.Inject(trRaceOnce(isPut("r.bin"), func() { e.srv.PutObject("b", "r.bin", small, "", meta) }))
			up := trKind(e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: ConflictSkip}), KindUpload)
			if len(up) != 1 || up[0].Status != StatusFailed || !strings.Contains(up[0].Err, "nothing was overwritten") {
				t.Fatalf("%+v", up)
			}
		})
	}
	t.Run("different size, same mtime", func(t *testing.T) {
		e := newTrEnv(t)
		src := filepath.Join(t.TempDir(), "r.bin")
		trWrite(t, src, small)
		meta := map[string]string{mtimeMetaKey: mTimeMeta(t, src)}
		e.srv.Inject(trRaceOnce(isPut("r.bin"), func() { e.srv.PutObject("b", "r.bin", []byte("longer racer"), "", meta) }))
		up := trKind(e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: ConflictSkip}), KindUpload)
		if len(up) != 1 || up[0].Status != StatusFailed {
			t.Fatalf("%+v", up)
		}
	})
}

// ---- auto decode only touches Pail's own compression -----------------------------------

func TestAutoDecodeOnlyOwnCompression(t *testing.T) {
	const plain = "decoded payload, decoded payload, decoded payload\n"
	gz := trGzip(t, plain)
	zp := trZip(t, "inner.txt", plain)
	aes := trAESZip(t, "inner.txt", plain, "right")
	for _, tc := range []struct {
		name     string
		object   []byte
		meta     map[string]string
		encoding string
		mode     DecodeMode
		want     []byte
	}{
		{"user's gzip file stays compressed", gz, nil, "", DecodeAuto, gz},
		{"user's zip file stays compressed", zp, nil, "", DecodeAuto, zp},
		{"user's aes zip stays compressed", aes, nil, "", DecodeAuto, aes},
		{"gzip marker", gz, map[string]string{"pail-compression": "gzip"}, "", DecodeAuto, []byte(plain)},
		{"gzip Content-Encoding", gz, nil, "gzip", DecodeAuto, []byte(plain)},
		{"zip marker", zp, map[string]string{"pail-compression": "zip"}, "", DecodeAuto, []byte(plain)},
		{"aes-zip marker", aes, map[string]string{"pail-compression": "aes-zip"}, "", DecodeAuto, []byte(plain)},
		{"zip marker does not decode gzip data", gz, map[string]string{"pail-compression": "zip"}, "", DecodeAuto, gz},
		{"gzip marker does not decode zip data", zp, map[string]string{"pail-compression": "gzip"}, "", DecodeAuto, zp},
		{"explicit gzip ignores markers", gz, nil, "", DecodeGzip, []byte(plain)},
		{"explicit zip ignores markers", zp, nil, "", DecodeZip, []byte(plain)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SetSecret("zp", "right"); err != nil {
				t.Fatal(err)
			}
			in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("obj.bin"), Body: bytes.NewReader(tc.object), Metadata: tc.meta}
			if tc.encoding != "" {
				in.ContentEncoding = aws.String(tc.encoding)
			}
			if _, err := e.sdk().PutObject(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"obj.bin"}, DestDir: dest, Decode: tc.mode, PasswordRef: "zp"}), StatusSucceeded)
			if got := trRead(t, filepath.Join(dest, "obj.bin")); got != string(tc.want) {
				t.Fatalf("content %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUploadMarksOwnCompression(t *testing.T) {
	for _, tc := range []struct {
		name string
		comp rules.Compression
		key  string
		want string
	}{
		{"gzip", rules.Compression{Method: "gzip", Level: -1}, "d.txt", "gzip"},
		{"zip", rules.Compression{Method: "zip", Level: -1}, "d.txt.zip", "zip"},
		{"aes-zip", rules.Compression{Method: "zip", Level: -1, PasswordRef: "zpw"}, "d.txt.zip", "aes-zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SetSecret("zpw", "pa55word"); err != nil {
				t.Fatal(err)
			}
			if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "c", Compression: &tc.comp}}); err != nil {
				t.Fatal(err)
			}
			tasks := uploadOne(t, e, "d.txt", []byte("hello hello hello"), ConflictSkip)
			trExpect(t, tasks, StatusSucceeded)
			info, err := e.s.Head(testCtx(t), e.loc("b", ""), tc.key, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Metadata["pail-compression"]; got != tc.want {
				t.Errorf("pail-compression = %q, want %q", got, tc.want)
			}
		})
	}
	e := newTrEnv(t)
	trExpect(t, uploadOne(t, e, "plain.txt", []byte("x"), ConflictSkip), StatusSucceeded)
	info, _ := e.s.Head(testCtx(t), e.loc("b", ""), "plain.txt", "")
	if v, ok := info.Metadata["pail-compression"]; ok {
		t.Errorf("an uncompressed upload carries pail-compression=%q", v)
	}
}

// Round trip with Auto: our own uploads decode, without any explicit mode.
func TestAutoDecodeRoundTripOfOwnUploads(t *testing.T) {
	const content = "the quick brown fox jumps over the lazy dog\n"
	for _, tc := range []struct {
		name string
		comp rules.Compression
		key  string
	}{
		{"gzip", rules.Compression{Method: "gzip", Level: -1}, "d.txt"},
		{"zip", rules.Compression{Method: "zip", Level: -1}, "d.txt.zip"},
		{"aes-zip", rules.Compression{Method: "zip", Level: -1, PasswordRef: "zpw"}, "d.txt.zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SetSecret("zpw", "pa55word"); err != nil {
				t.Fatal(err)
			}
			if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "c", Compression: &tc.comp}}); err != nil {
				t.Fatal(err)
			}
			trExpect(t, uploadOne(t, e, "d.txt", []byte(content), ConflictSkip), StatusSucceeded)
			out := t.TempDir()
			trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{tc.key}, DestDir: out, Decode: DecodeAuto, PasswordRef: "zpw"}), StatusSucceeded)
			if got := trRead(t, filepath.Join(out, tc.key)); got != content {
				t.Fatalf("content %q", got)
			}
		})
	}
}

// ---- orphaned spool files ---------------------------------------------------------------------

func TestNewSweepsOldSpoolFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	if os.TempDir() != tmp && filepath.Clean(os.TempDir()) != filepath.Clean(tmp) {
		t.Skipf("cannot redirect os.TempDir (%s)", os.TempDir())
	}
	old := filepath.Join(tmp, "pail-upload-111.tmp")
	fresh := filepath.Join(tmp, "pail-upload-222.tmp")
	foreignOld := filepath.Join(tmp, "somebody-else-333.tmp")
	oldDir := filepath.Join(tmp, "pail-upload-444.tmp")
	for _, p := range []string{old, fresh, foreignOld} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-25 * time.Hour)
	for _, p := range []string{old, foreignOld, oldDir} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	// A download target directory with an old temp file must be left alone.
	dl := t.TempDir()
	dlTmp := filepath.Join(dl, ".pail-123.tmp")
	if err := os.WriteFile(dlTmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(dlTmp, past, past)

	newTestService(t, nil)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old spool file survived: %v", err)
	}
	for _, p := range []string{fresh, foreignOld, oldDir, dlTmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must be kept: %v", p, err)
		}
	}
}

func TestLocalNameFolder(t *testing.T) {
	for _, tc := range []struct {
		goos string
		fold bool
	}{{"windows", true}, {"darwin", true}, {"linux", false}} {
		f := newNameClaims(tc.goos)
		if got := f.claim("a.txt", "k1"); got != "a.txt" {
			t.Errorf("%s: first claim = %q", tc.goos, got)
		}
		if got := f.claim("A.txt", "k2"); (got != "A.txt") != tc.fold {
			t.Errorf("%s: second claim = %q, fold=%v", tc.goos, got, tc.fold)
		}
		if got := f.claim("a.txt", "k1"); got != "a.txt" {
			t.Errorf("%s: the same owner re-claiming must keep its name, got %q", tc.goos, got)
		}
	}
	f := newNameClaims("windows")
	f.claim("d/a.txt", "k1")
	if got := f.claim("D/A.TXT", "k2"); got != "D/A.TXT (1)" && got != "D/A (1).TXT" {
		t.Errorf("numbered name = %q", got)
	}
	// A later real key that equals an already handed-out numbered name is itself a collision.
	if got := f.claim("d/a (1).txt", "k3"); got == "d/a (1).txt" {
		t.Errorf("real key took a numbered name that was already handed out")
	}
}

func TestUploadEmptyFolderWriteOnly(t *testing.T) {
	e := newTrEnv(t)
	denyHead(e)
	root := filepath.Join(t.TempDir(), "top")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{root}, Conflict: ConflictSkip}), StatusSucceeded)
	if _, ok := e.srv.Object("b", "top/empty/"); !ok {
		t.Fatal("folder object missing")
	}
	// Again: the create-only write meets the existing folder object and reports it as existing.
	again := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{root}, Conflict: ConflictSkip})
	trExpect(t, again, StatusSucceeded)
	for _, it := range trItems(e.s, trKind(again, KindUpload)) {
		if it.Status != ItemSkipped {
			t.Errorf("second run: %+v", it)
		}
	}
}
