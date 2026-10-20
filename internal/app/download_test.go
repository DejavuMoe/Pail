package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func trSeed(e *trEnv) {
	e.srv.PutObject("b", "a/x.txt", []byte("x1"), "", nil)
	e.srv.PutObject("b", "a/sub/y.txt", []byte("y1"), "", nil)
	e.srv.PutObject("b", "a/sub/", nil, "", nil)
	e.srv.PutObject("b", "a/empty/", nil, "", nil)
	e.srv.PutObject("b", "z.bin", []byte("zz"), "", nil)
}

func TestDownloadSelections(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prefix  string
		keys    []string
		whole   bool
		want    []string
		wantDir []string
	}{
		{"single object below prefix", "a/", []string{"a/x.txt"}, false, []string{"x.txt"}, nil},
		{"object without prefix keeps key", "", []string{"a/x.txt", "z.bin"}, false, []string{"a/x.txt", "z.bin"}, nil},
		{"folder relative to prefix", "a/", []string{"a/sub/"}, false, []string{"sub/y.txt"}, []string{"sub"}},
		{"folder from the top", "", []string{"a/"}, false, []string{"a/sub/y.txt", "a/x.txt"}, []string{"a/empty", "a/sub"}},
		{"whole bucket ignores the prefix", "a/", nil, true, []string{"a/sub/y.txt", "a/x.txt", "z.bin"}, []string{"a/empty", "a/sub"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			trSeed(e)
			dest := t.TempDir()
			tasks := e.download(DownloadRequest{Source: e.loc("b", tc.prefix), Keys: tc.keys, WholeBucket: tc.whole, DestDir: dest})
			trExpect(t, tasks, StatusSucceeded)
			if got := trTree(t, dest); !trSameStrings(got, tc.want) {
				t.Fatalf("files %v, want %v", got, tc.want)
			}
			for _, d := range tc.wantDir {
				if st, err := os.Stat(filepath.Join(dest, filepath.FromSlash(d))); err != nil || !st.IsDir() {
					t.Errorf("directory %s missing: %v", d, err)
				}
			}
			if len(trKind(tasks, KindDownload)) != len(tc.want) {
				t.Errorf("%d download tasks for %d files", len(trKind(tasks, KindDownload)), len(tc.want))
			}
			// Content and mtime of one file.
			f := tc.want[0]
			key := f
			if tc.prefix != "" && !tc.whole {
				key = tc.prefix + f
			}
			want, _ := e.srv.Object("b", key)
			if got := trRead(t, filepath.Join(dest, filepath.FromSlash(f))); got != string(want) {
				t.Errorf("%s = %q, want %q", f, got, want)
			}
			info, err := e.s.Head(testCtx(t), e.loc("b", ""), key, "")
			if err != nil {
				t.Fatal(err)
			}
			st, _ := os.Stat(filepath.Join(dest, filepath.FromSlash(f)))
			if !st.ModTime().Equal(info.Modified) {
				t.Errorf("mtime %v, want the object's LastModified %v", st.ModTime(), info.Modified)
			}
		})
	}
}

func TestDownloadVersions(t *testing.T) {
	e := newTrEnv(t)
	e.srv.CreateBucket("vb", true)
	v1 := e.srv.PutObject("vb", "d/doc.tar.gz", []byte("one"), "", nil)
	v2 := e.srv.PutObject("vb", "d/doc.tar.gz", []byte("two!"), "", nil)
	e.srv.PutObject("vb", "README", []byte("r"), "", nil)
	rv := e.srv.Versions("vb", "README")[0].VersionID
	dest := t.TempDir()
	tasks := e.download(DownloadRequest{Source: e.loc("vb", "d/"), DestDir: dest, Versions: []ObjectRef{
		{Key: "d/doc.tar.gz", VersionID: v1}, {Key: "d/doc.tar.gz", VersionID: v2}, {Key: "README", VersionID: rv}}})
	trExpect(t, tasks, StatusSucceeded)
	want := []string{
		"README (v" + rv[:8] + ")",
		"doc.tar (v" + v1[:8] + ").gz",
		"doc.tar (v" + v2[:8] + ").gz",
	}
	if got := trTree(t, dest); len(got) != 3 {
		t.Fatalf("%v", got)
	}
	for i, name := range want {
		if i == 0 { // README is not below the prefix "d/": full key
			continue
		}
		content := map[int]string{1: "one", 2: "two!"}[i]
		if got := trRead(t, filepath.Join(dest, name)); got != content {
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}
	if got := trRead(t, filepath.Join(dest, want[0])); got != "r" {
		t.Errorf("README version = %q", got)
	}
	items := trItems(e.s, trKind(tasks, KindDownload))
	if it := items["d/doc.tar.gz"]; it.VersionID == "" {
		t.Errorf("item should carry the version id: %+v", it)
	}
}

func TestDownloadConflictPolicies(t *testing.T) {
	hour := time.Hour
	for _, tc := range []struct {
		name     string
		policy   ConflictPolicy
		remote   string
		pre      map[string]string // local files before
		mtimeOff time.Duration
		want     map[string]string
		item     string
	}{
		{"skip", ConflictSkip, "remote!", map[string]string{"f.txt": "local-old"}, 0, map[string]string{"f.txt": "local-old"}, ItemSkipped},
		{"skip, free", ConflictSkip, "remote!", nil, 0, map[string]string{"f.txt": "remote!"}, ItemSucceeded},
		{"overwrite", ConflictOverwrite, "remote!", map[string]string{"f.txt": "local-old"}, 0, map[string]string{"f.txt": "remote!"}, ItemSucceeded},
		{"rename", ConflictRename, "remote!", map[string]string{"f.txt": "local-old"}, 0, map[string]string{"f.txt": "local-old", "f (1).txt": "remote!"}, ItemSucceeded},
		{"rename skips taken numbers", ConflictRename, "remote!", map[string]string{"f.txt": "a", "f (1).txt": "b"}, 0, map[string]string{"f.txt": "a", "f (1).txt": "b", "f (2).txt": "remote!"}, ItemSucceeded},
		{"newer, local older", ConflictNewer, "remote!", map[string]string{"f.txt": "local-old"}, -hour, map[string]string{"f.txt": "remote!"}, ItemSucceeded},
		{"newer, local newer", ConflictNewer, "remote!", map[string]string{"f.txt": "local-old"}, hour, map[string]string{"f.txt": "local-old"}, ItemSkipped},
		{"different_size, differs", ConflictDifferentSize, "remote!", map[string]string{"f.txt": "local-old"}, 0, map[string]string{"f.txt": "remote!"}, ItemSucceeded},
		{"different_size, same", ConflictDifferentSize, "remote123", map[string]string{"f.txt": "local-old"}, 0, map[string]string{"f.txt": "local-old"}, ItemSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "f.txt", []byte(tc.remote), "", nil)
			dest := t.TempDir()
			for name, content := range tc.pre {
				trWrite(t, filepath.Join(dest, name), []byte(content))
				trChtimes(t, filepath.Join(dest, name), time.Now().Add(tc.mtimeOff))
			}
			tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"f.txt"}, DestDir: dest, Conflict: tc.policy})
			trExpect(t, tasks, StatusSucceeded)
			got := trTree(t, dest)
			if len(got) != len(tc.want) {
				t.Fatalf("files %v, want %v", got, tc.want)
			}
			for name, want := range tc.want {
				if c := trRead(t, filepath.Join(dest, name)); c != want {
					t.Errorf("%s = %q, want %q", name, c, want)
				}
			}
			items := e.s.TaskItems(trKind(tasks, KindDownload)[0].ID)
			if len(items) != 1 || items[0].Status != tc.item {
				t.Errorf("items %+v, want %s", items, tc.item)
			}
		})
	}
}

func TestDownloadDefaultsFromSettings(t *testing.T) {
	e := newTrEnv(t) // default policy: rename
	e.srv.PutObject("b", "f.txt", []byte("r"), "", nil)
	dest := t.TempDir()
	if err := e.s.SaveSettings(Settings{DefaultDownloadDir: dest}); err != nil {
		t.Fatal(err)
	}
	trWrite(t, filepath.Join(dest, "f.txt"), []byte("local"))
	trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"f.txt"}}), StatusSucceeded)
	if trRead(t, filepath.Join(dest, "f.txt")) != "local" || trRead(t, filepath.Join(dest, "f (1).txt")) != "r" {
		t.Fatal("default dir/policy not applied")
	}
}

func TestDownloadValidation(t *testing.T) {
	e := newTrEnv(t)
	dest := t.TempDir()
	for name, req := range map[string]DownloadRequest{
		"nothing selected": {Source: e.loc("b", ""), DestDir: dest},
		"no bucket":        {Source: e.loc("", ""), Keys: []string{"k"}, DestDir: dest},
		"unknown account":  {Source: Location{AccountID: "x", Bucket: "b"}, Keys: []string{"k"}, DestDir: dest},
		"no dest":          {Source: e.loc("b", ""), Keys: []string{"k"}},
		"bad policy":       {Source: e.loc("b", ""), Keys: []string{"k"}, DestDir: dest, Conflict: "x"},
		"bad decode":       {Source: e.loc("b", ""), Keys: []string{"k"}, DestDir: dest, Decode: "rot13"},
		"negative max":     {Source: e.loc("b", ""), Keys: []string{"k"}, DestDir: dest, MaxDecoded: -1},
		"empty key":        {Source: e.loc("b", ""), Keys: []string{""}, DestDir: dest},
		"version no id":    {Source: e.loc("b", ""), Versions: []ObjectRef{{Key: "k"}}, DestDir: dest},
		"bad prefix":       {Source: e.loc("b", "p"), Keys: []string{"k"}, DestDir: dest},
	} {
		if _, err := e.s.Download(req); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestDownloadUnsafeKeys(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "good.txt", []byte("g"), "", nil)
	for _, k := range []string{"a//b.txt", "x/../up.txt", "/abs.txt", "x/./y.txt"} {
		e.srv.PutObject("b", k, []byte("evil"), "", nil)
	}
	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), WholeBucket: true, DestDir: dest})
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusPartiallyFailed {
		t.Fatalf("scan: %+v", scan)
	}
	failed := 0
	for _, it := range e.s.TaskItems(scan.ID) {
		if it.Status == ItemFailed {
			failed++
			if !strings.Contains(it.Err, "unsafe") {
				t.Errorf("%s: %s", it.Key, it.Err)
			}
		}
	}
	if failed != 4 {
		t.Errorf("%d failed items, want 4", failed)
	}
	if got := trTree(t, dest); !trSameStrings(got, []string{"good.txt"}) {
		t.Errorf("dest files %v", got)
	}
	if got := trTree(t, parent); !trSameStrings(got, []string{"dest/good.txt"}) {
		t.Errorf("something was written outside the destination: %v", got)
	}
}

func TestDownloadETagChangeAndRetry(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "c.bin", []byte("original"), "", nil)
	e.srv.Inject(trRaceOnce(func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/c.bin")
	}, func() { e.srv.PutObject("b", "c.bin", []byte("changed!!"), "", nil) }))
	dest := t.TempDir()
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"c.bin"}, DestDir: dest})
	dl := trKind(tasks, KindDownload)
	if len(dl) != 1 || dl[0].Status != StatusFailed || !strings.Contains(dl[0].Err, "changed") {
		t.Fatalf("%+v", dl)
	}
	if got := trTree(t, dest); len(got) != 0 {
		t.Fatalf("a file was published: %v", got)
	}
	// Retry looks the object up again and downloads the current version.
	id, err := e.s.Retry(dl[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, e.s, id); tk.Status != StatusSucceeded {
		t.Fatalf("retry: %+v", tk)
	}
	if got := trRead(t, filepath.Join(dest, "c.bin")); got != "changed!!" {
		t.Fatalf("retry content %q", got)
	}
}

func TestDownloadCancel(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "c.bin", trBigData(1<<20), "", nil)
	started := make(chan struct{}, 8)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/c.bin") {
			select {
			case started <- struct{}{}:
			default:
			}
			return &s3test.Fault{Delay: 30 * time.Second}
		}
		return nil
	})
	dest := t.TempDir()
	g, err := e.s.Download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"c.bin"}, DestDir: dest})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("download never reached the server")
	}
	if err := e.s.CancelGroup(g); err != nil {
		t.Fatal(err)
	}
	tasks := trWaitGroup(t, e.s, g)
	dl := trKind(tasks, KindDownload)
	if len(dl) != 1 || dl[0].Status != StatusCanceled {
		t.Fatalf("%+v", dl)
	}
	if got := trTree(t, dest); len(got) != 0 {
		t.Fatalf("files after cancel: %v", got)
	}
	ents, _ := os.ReadDir(dest)
	if len(ents) != 0 {
		t.Fatalf("temporary file left in the destination: %v", ents)
	}
}

func TestDownloadEmptyObject(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "empty.txt", nil, "", nil)
	dest := t.TempDir()
	trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"empty.txt"}, DestDir: dest}), StatusSucceeded)
	st, err := os.Stat(filepath.Join(dest, "empty.txt"))
	if err != nil || st.Size() != 0 {
		t.Fatalf("%v %v", st, err)
	}
}

func TestDownloadLocalFileAppearsDuringTransfer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy ConflictPolicy
		want   map[string]string
		item   string
	}{
		{"skip", ConflictSkip, map[string]string{"f.txt": "racer"}, ItemSkipped},
		{"rename", ConflictRename, map[string]string{"f.txt": "racer", "f (1).txt": "remote"}, ItemSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "f.txt", []byte("remote"), "", nil)
			dest := t.TempDir()
			e.srv.Inject(trRaceOnce(func(r *http.Request) bool {
				return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/f.txt")
			},
				func() { os.WriteFile(filepath.Join(dest, "f.txt"), []byte("racer"), 0o644) }))
			tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"f.txt"}, DestDir: dest, Conflict: tc.policy})
			trExpect(t, tasks, StatusSucceeded)
			if got := trTree(t, dest); len(got) != len(tc.want) {
				t.Fatalf("%v", got)
			}
			for n, c := range tc.want {
				if trRead(t, filepath.Join(dest, n)) != c {
					t.Errorf("%s = %q, want %q", n, trRead(t, filepath.Join(dest, n)), c)
				}
			}
			if it := e.s.TaskItems(trKind(tasks, KindDownload)[0].ID); len(it) != 1 || it[0].Status != tc.item {
				t.Errorf("%+v", it)
			}
		})
	}
}

func TestDownloadScanListingFailureIsRetryable(t *testing.T) {
	e := newTrEnv(t)
	trSeed(e)
	var failing atomic.Bool
	failing.Store(true)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if failing.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			return &s3test.Fault{Status: 500, Code: "InternalError", Message: "boom"}
		}
		return nil
	})
	dest := t.TempDir()
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"a/"}, DestDir: dest})
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusPartiallyFailed || len(trKind(tasks, KindDownload)) != 0 {
		t.Fatalf("%+v", tasks)
	}
	failing.Store(false)
	id, err := e.s.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, e.s, id)
	tk, _ := taskByID(e.s, id)
	for _, x := range trWaitGroup(t, e.s, tk.GroupID) {
		if x.ID != scan.ID && x.Status != StatusSucceeded {
			t.Fatalf("%+v", x)
		}
	}
	if got := trTree(t, dest); !trSameStrings(got, []string{"a/sub/y.txt", "a/x.txt"}) {
		t.Fatalf("%v", got)
	}
}
