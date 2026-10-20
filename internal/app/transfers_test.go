package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// trEnv is an s3Env with one bucket "b" and helpers for the transfer tests.
type trEnv struct {
	*s3Env
}

func newTrEnv(t *testing.T) *trEnv {
	t.Helper()
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	return &trEnv{e}
}

// trWaitGroup waits until the scan task of group and every task it submitted
// are final, and returns them in submission order.
func trWaitGroup(t testing.TB, s *Service, group string) []Task {
	t.Helper()
	var out []Task
	eventually(t, "group "+group+" to finish", func() bool {
		out = out[:0]
		scanDone := false
		for _, tk := range s.Tasks() {
			if tk.GroupID != group {
				continue
			}
			out = append(out, tk)
			if !tk.Status.Finished() {
				return false
			}
			if tk.Kind == KindOther {
				scanDone = true
			}
		}
		return scanDone
	})
	return out
}

// trItems collects the items of all tasks of a group, keyed by Key.
func trItems(s *Service, tasks []Task) map[string]ItemResult {
	m := map[string]ItemResult{}
	for _, tk := range tasks {
		for _, it := range s.TaskItems(tk.ID) {
			m[it.Key] = it
		}
	}
	return m
}

// trKind returns the tasks of one kind.
func trKind(tasks []Task, k Kind) []Task {
	var out []Task
	for _, tk := range tasks {
		if tk.Kind == k {
			out = append(out, tk)
		}
	}
	return out
}

// trExpect fails unless every task has status want.
func trExpect(t testing.TB, tasks []Task, want Status) {
	t.Helper()
	for _, tk := range tasks {
		if tk.Status != want {
			t.Fatalf("task %s %q is %s (%s), want %s", tk.ID, tk.Title, tk.Status, tk.Err, want)
		}
	}
}

func trWrite(t testing.TB, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func trRead(t testing.TB, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// trUpload runs Upload and waits for the whole group.
func (e *trEnv) upload(req UploadRequest) []Task {
	e.t.Helper()
	g, err := e.s.Upload(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return trWaitGroup(e.t, e.s, g)
}

// download runs Download and waits for the whole group.
func (e *trEnv) download(req DownloadRequest) []Task {
	e.t.Helper()
	g, err := e.s.Download(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return trWaitGroup(e.t, e.s, g)
}

// trTree lists the regular files below dir (slash separated, sorted) and
// fails the test when it finds a hidden temporary file.
func trTree(t testing.TB, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	for _, o := range out {
		if strings.HasSuffix(o, ".tmp") && strings.Contains(o, ".pail-") {
			t.Fatalf("temporary file left behind: %s", o)
		}
	}
	return out
}

func trSameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func trObject(t testing.TB, e *trEnv, key string) string {
	t.Helper()
	b, ok := e.srv.Object("b", key)
	if !ok {
		t.Fatalf("object %q does not exist", key)
	}
	return string(b)
}

func trMissing(t testing.TB, e *trEnv, key string) {
	t.Helper()
	if _, ok := e.srv.Object("b", key); ok {
		t.Fatalf("object %q exists", key)
	}
}

func trChtimes(t testing.TB, p string, mt time.Time) {
	t.Helper()
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
}
