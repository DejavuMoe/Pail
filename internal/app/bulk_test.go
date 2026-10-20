package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// ---- helpers (bk prefix) -------------------------------------------------------------------------

// bkSeed stores objects whose content is "data:<key>" and creates the bucket
// when needed.
func bkSeed(e *s3Env, bucket string, versioned bool, keys ...string) {
	e.srv.CreateBucket(bucket, versioned)
	for _, k := range keys {
		e.srv.PutObject(bucket, k, []byte("data:"+k), "text/plain", nil)
	}
}

// bkWait waits for a task to finish and returns its final snapshot.
func bkWait(t *testing.T, e *s3Env, id string) Task {
	t.Helper()
	tk, err := e.s.waitTask(testCtx(t), id)
	if err != nil {
		t.Fatalf("wait %s: %v", id, err)
	}
	return tk
}

// bkItems indexes the reported items of a task by "key" or "key@version".
func bkItems(e *s3Env, id string) map[string]ItemResult {
	out := map[string]ItemResult{}
	for _, it := range e.s.TaskItems(id) {
		k := it.Key
		if it.VersionID != "" {
			k += "@" + it.VersionID
		}
		out[k] = it
	}
	return out
}

// bkLive lists the current keys of a bucket (flat).
func bkLive(t *testing.T, e *s3Env, bucket string) []string {
	t.Helper()
	var keys []string
	tok := ""
	for {
		p, err := e.s.List(testCtx(t), e.loc(bucket, ""), ListOptions{Flat: true, Token: tok})
		if err != nil {
			t.Fatal(err)
		}
		for _, en := range p.Entries {
			keys = append(keys, en.Key)
		}
		if p.NextToken == "" {
			break
		}
		tok = p.NextToken
	}
	sort.Strings(keys)
	return keys
}

func bkWantKeys(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("keys = %q, want %q", got, want)
	}
}

func bkWantData(t *testing.T, e *s3Env, bucket, key, want string) {
	t.Helper()
	d, ok := e.srv.Object(bucket, key)
	if !ok || string(d) != want {
		t.Fatalf("%s/%s = %q (exists %v), want %q", bucket, key, d, ok, want)
	}
}

func bkOK(t *testing.T, e *s3Env, id string) Task {
	t.Helper()
	tk := bkWait(t, e, id)
	if tk.Status != StatusSucceeded {
		t.Fatalf("task %q: status %v err %q items %+v", tk.Title, tk.Status, tk.Err, e.s.TaskItems(id))
	}
	return tk
}

// bkSetPage makes listings use small pages for the rest of the test.
func bkSetPage(t *testing.T, n int32) {
	old := bkPageSize
	bkPageSize = n
	t.Cleanup(func() { bkPageSize = old })
}

func bkWorkersSetting(t *testing.T, e *s3Env, n int) {
	t.Helper()
	st := e.s.Settings()
	st.BulkWorkers = n
	if err := e.s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
}

// ---- selection scope -------------------------------------------------------------------------------

func TestBulkScopeCollapsesNesting(t *testing.T) {
	sel := Selection{Location: Location{AccountID: "a", Bucket: "b", Prefix: "p/"},
		Keys:     []string{"p/dir/sub/", "p/dir/", "p/dir/file", "p/other", "p/other", "p/dir/sub/x"},
		Versions: []ObjectRef{{"p/dir/file", "v1"}, {"p/dir/file", "v1"}}}
	sc, err := bkBuildScope("t", sel, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Scans) != 1 || sc.Scans[0].Prefix != "p/dir/" {
		t.Fatalf("scans = %+v", sc.Scans)
	}
	var got []string
	for _, o := range sc.Objs {
		got = append(got, o.Key+"@"+o.VersionID)
	}
	if strings.Join(got, ",") != "p/other@,p/dir/file@v1" {
		t.Fatalf("objs = %v", got)
	}
	for name, bad := range map[string]Selection{
		"empty":         {Location: sel.Location},
		"outside":       {Location: sel.Location, Keys: []string{"q/x"}},
		"prefix itself": {Location: sel.Location, Keys: []string{"p/"}},
		"all+keys":      {Location: sel.Location, All: true, Keys: []string{"p/x"}},
		"no version":    {Location: sel.Location, Versions: []ObjectRef{{"p/x", ""}}},
	} {
		if _, err := bkBuildScope("t", bad, true, true); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := bkBuildScope("t", Selection{Location: sel.Location, Versions: []ObjectRef{{"p/x", "v"}}}, false, true); !IsKind(err, ErrInvalid) {
		t.Errorf("versions not allowed: %v", err)
	}
}

// ---- estimate ---------------------------------------------------------------------------------------

func TestEstimate(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/1", "a/2", "a/sub/3", "b/4", "top")
	loc := e.loc("bkt", "")
	size := func(k string) int64 { return int64(len("data:" + k)) }

	est, err := e.s.Estimate(testCtx(t), Selection{Location: loc, Keys: []string{"a/", "top", "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := size("a/1") + size("a/2") + size("a/sub/3") + size("top")
	if est.Objects != 4 || est.Bytes != wantBytes || est.Truncated {
		t.Fatalf("estimate = %+v, want 4 objects %d bytes", est, wantBytes)
	}
	all, err := e.s.Estimate(testCtx(t), Selection{Location: loc, All: true})
	if err != nil || all.Objects != 5 {
		t.Fatalf("all = %+v, %v", all, err)
	}
	if _, err := e.s.Estimate(testCtx(t), Selection{Location: e.loc("nope", ""), All: true}); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing bucket: %v", err)
	}
}

func TestEstimateTruncates(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8")
	old := bkEstimateLimit
	bkEstimateLimit = 5
	t.Cleanup(func() { bkEstimateLimit = old })
	bkSetPage(t, 3)
	est, err := e.s.Estimate(testCtx(t), Selection{Location: e.loc("bkt", ""), All: true})
	if err != nil {
		t.Fatal(err)
	}
	if est.Objects != 5 || !est.Truncated {
		t.Fatalf("estimate = %+v", est)
	}
	bkEstimateLimit = 8 // exactly the number of objects: not truncated
	est, _ = e.s.Estimate(testCtx(t), Selection{Location: e.loc("bkt", ""), All: true})
	if est.Objects != 8 || est.Truncated {
		t.Fatalf("estimate at limit = %+v", est)
	}
}

// ---- copy -----------------------------------------------------------------------------------------------

func TestCopyFoldersWithinBucket(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/x.txt", "a/sub/y.txt", "a/", "b/z.txt")
	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"a/"}}
	id, err := e.s.Copy(sel, e.loc("bkt", "c/"), CopyOptions{Conflict: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if tk.Kind != KindOther || tk.Items != 3 || tk.ItemsDone != 3 {
		t.Fatalf("task = %+v", tk)
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), "a/x.txt", "a/sub/y.txt", "a/", "b/z.txt", "c/a/x.txt", "c/a/sub/y.txt", "c/a/")
	bkWantData(t, e, "bkt", "c/a/sub/y.txt", "data:a/sub/y.txt")
	bkWantData(t, e, "bkt", "a/sub/y.txt", "data:a/sub/y.txt") // source untouched
}

func TestCopyAcrossBucketsRelativeToSelectionPrefix(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "src", false, "in/f.txt", "in/d/g.txt", "in/d/h/i.txt", "in/skipme")
	e.srv.CreateBucket("dst", false)
	sel := Selection{Location: e.loc("src", "in/"), Keys: []string{"in/f.txt", "in/d/"}}
	id, err := e.s.Copy(sel, e.loc("dst", "out/"), CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "dst"), "out/f.txt", "out/d/g.txt", "out/d/h/i.txt")
	bkWantData(t, e, "dst", "out/d/h/i.txt", "data:in/d/h/i.txt")

	// "All" copies the contents of the prefix without the prefix itself.
	e.srv.CreateBucket("dst2", false)
	id, _ = e.s.Copy(Selection{Location: e.loc("src", "in/"), All: true}, e.loc("dst2", ""), CopyOptions{})
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "dst2"), "f.txt", "d/g.txt", "d/h/i.txt", "skipme")
}

func TestCopyPreservesMetadataAndClass(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", false)
	e.srv.PutObject("bkt", "m.bin", []byte("payload"), "application/x-custom", map[string]string{"owner": "me"})
	e.srv.SetStorageClass("bkt", "m.bin", "STANDARD_IA")
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"m.bin"}}, e.loc("bkt", "copy/"), CopyOptions{Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	info, err := e.s.Head(testCtx(t), e.loc("bkt", ""), "copy/m.bin", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentType != "application/x-custom" || info.Metadata["owner"] != "me" || info.StorageClass != "STANDARD_IA" {
		t.Fatalf("copy lost attributes: %+v", info)
	}
}

func TestCopyConflictPolicies(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "s/f.txt")
	e.srv.PutObject("bkt", "d/f.txt", []byte("OLD"), "text/plain", nil)
	sel := Selection{Location: e.loc("bkt", "s/"), Keys: []string{"s/f.txt"}}
	dest := e.loc("bkt", "d/")

	id, _ := e.s.Copy(sel, dest, CopyOptions{Conflict: ConflictSkip})
	bkOK(t, e, id)
	if it := bkItems(e, id)["s/f.txt"]; it.Status != ItemSkipped {
		t.Fatalf("skip: %+v", it)
	}
	bkWantData(t, e, "bkt", "d/f.txt", "OLD")

	id, _ = e.s.Copy(sel, dest, CopyOptions{Conflict: ConflictRename})
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "d/f (1).txt", "data:s/f.txt")
	bkWantData(t, e, "bkt", "d/f.txt", "OLD")

	id, _ = e.s.Copy(sel, dest, CopyOptions{Conflict: ConflictOverwrite})
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "d/f.txt", "data:s/f.txt")

	// different_size: now the sizes are equal, so it is skipped
	id, _ = e.s.Copy(sel, dest, CopyOptions{Conflict: ConflictDifferentSize})
	bkOK(t, e, id)
	if it := bkItems(e, id)["s/f.txt"]; it.Status != ItemSkipped {
		t.Fatalf("different_size: %+v", it)
	}
	// default policy (empty) is skip, never overwrite
	e.srv.PutObject("bkt", "d/f.txt", []byte("OLD"), "text/plain", nil)
	id, _ = e.s.Copy(sel, dest, CopyOptions{})
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "d/f.txt", "OLD")
}

func TestCopyRejectsSelfAndFolderIntoItself(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/x", "a/b/y")
	e.srv.CreateBucket("other", false)
	loc := e.loc("bkt", "")
	cases := map[string]struct {
		sel  Selection
		dest Location
		opt  CopyOptions
	}{
		"same folder":       {Selection{Location: e.loc("bkt", "a/"), Keys: []string{"a/x"}}, e.loc("bkt", "a/"), CopyOptions{}},
		"same folder root":  {Selection{Location: loc, Keys: []string{"a/"}}, loc, CopyOptions{Move: true}},
		"folder into self":  {Selection{Location: loc, Keys: []string{"a/"}}, e.loc("bkt", "a/b/"), CopyOptions{}},
		"folder into self2": {Selection{Location: loc, Keys: []string{"a/"}}, e.loc("bkt", "a/"), CopyOptions{Move: true}},
		"all into self":     {Selection{Location: e.loc("bkt", "a/"), All: true}, e.loc("bkt", "a/b/"), CopyOptions{}},
		"whole bucket":      {Selection{Location: loc, All: true}, e.loc("bkt", "z/"), CopyOptions{}},
		"bad policy":        {Selection{Location: loc, Keys: []string{"a/x"}}, e.loc("other", ""), CopyOptions{Conflict: "bogus"}},
		"other account":     {Selection{Location: loc, Keys: []string{"a/x"}}, Location{AccountID: "someone-else", Bucket: "other"}, CopyOptions{}},
		"no dest bucket":    {Selection{Location: loc, Keys: []string{"a/x"}}, e.loc("", ""), CopyOptions{}},
	}
	for name, c := range cases {
		if id, err := e.s.Copy(c.sel, c.dest, c.opt); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: id %q err %v, want Invalid", name, id, err)
		}
	}
	// A sibling folder with a common name prefix is fine.
	bkSeed(e, "bkt", false, "ab/z")
	id, err := e.s.Copy(Selection{Location: loc, Keys: []string{"a/"}}, e.loc("bkt", "ab/"), CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "ab/a/b/y", "data:a/b/y")
}

// ---- move --------------------------------------------------------------------------------------------------

func TestMoveUnversioned(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/x", "a/y", "keep")
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a/"}}, e.loc("bkt", "m/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if !strings.HasPrefix(tk.Title, "Move") {
		t.Fatalf("title %q", tk.Title)
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), "keep", "m/a/x", "m/a/y")
	bkWantData(t, e, "bkt", "m/a/y", "data:a/y")
}

func TestMoveAcrossBuckets(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "one", false, "f1", "f2")
	e.srv.CreateBucket("two", false)
	id, _ := e.s.Copy(Selection{Location: e.loc("one", ""), Keys: []string{"f1", "f2"}}, e.loc("two", "in/"), CopyOptions{Move: true})
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "one"))
	bkWantKeys(t, bkLive(t, e, "two"), "in/f1", "in/f2")
}

// The copy source is percent-encoded so "+" (a space to AWS-like servers) and
// other sub-delimiters survive copy, move and rename.
func TestCopySpecialCharacterKeys(t *testing.T) {
	keys := []string{"plus+only.txt", "a b+c d.txt", "plus+/nested+dir/x+y.txt", "amp&eq=semi;.txt", "pct%20lit.txt"}
	e := newS3Env(t)
	bkSeed(e, "bkt", false, keys...)
	id, _ := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: keys}, e.loc("bkt", "moved/"), CopyOptions{Move: true})
	bkOK(t, e, id)
	want := make([]string, len(keys))
	for i, k := range keys {
		want[i] = "moved/" + k
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), want...)
	id, err := e.s.Rename(testCtx(t), e.loc("bkt", "moved/"), "moved/plus+only.txt", "still+plus.txt")
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	if _, ok := e.srv.Object("bkt", "moved/still+plus.txt"); !ok {
		t.Fatal("rename of a key with + failed")
	}
}

// The destination key is dest.Prefix plus the key relative to the selection's
// folder, so moving pfx/src/a.txt (selected from pfx/) to pfx/src/ lands on
// pfx/src/src/a.txt; only a destination equal to the source is refused.
func TestMoveIntoSubfolderKeepsRelativePath(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "pfx/src/a.txt")
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", "pfx/"), Keys: []string{"pfx/src/a.txt"}}, e.loc("bkt", "pfx/src/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "bkt"), "pfx/src/src/a.txt")
	if _, err := e.s.Copy(Selection{Location: e.loc("bkt", "pfx/src/src/"), Keys: []string{"pfx/src/src/a.txt"}}, e.loc("bkt", "pfx/src/src/"), CopyOptions{Move: true}); !IsKind(err, ErrInvalid) {
		t.Fatalf("move onto itself: want Invalid, got %v", err)
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), "pfx/src/src/a.txt")
}

func TestMoveNeverDeletesSourceWhenCopyFails(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "src/a.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "" {
			return &s3test.Fault{Status: 503, Code: "SlowDown", Message: "injected"}
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"src/a.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true, Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	if tk.Status != StatusPartiallyFailed {
		t.Fatalf("status %v items %+v", tk.Status, e.s.TaskItems(id))
	}
	if it := bkItems(e, id)["src/a.txt"]; it.Status != ItemFailed {
		t.Fatalf("item %+v", it)
	}
	e.srv.Inject(nil)
	bkWantData(t, e, "bkt", "src/a.txt", "data:src/a.txt")
	if _, ok := e.srv.Object("bkt", "dst/src/a.txt"); ok {
		t.Fatal("destination must not exist")
	}
	// the same with create-only (multipart) copies
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "" {
			return &s3test.Fault{Status: 503, Code: "SlowDown", Message: "injected"}
		}
		return nil
	})
	id, _ = e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"src/a.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true, Conflict: ConflictSkip})
	if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
		t.Fatalf("multipart status %v", tk.Status)
	}
	e.srv.Inject(nil)
	bkWantData(t, e, "bkt", "src/a.txt", "data:src/a.txt")
	// and Retry moves it once the fault is gone
	rid, err := e.s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, rid)
	bkWantData(t, e, "bkt", "dst/src/a.txt", "data:src/a.txt")
	if _, ok := e.srv.Object("bkt", "src/a.txt"); ok {
		t.Fatal("source must be gone after the retried move")
	}
}

func TestMoveSourceDeleteFailureIsReported(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodDelete {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no delete"}
		}
		return nil
	})
	id, _ := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true})
	tk := bkWait(t, e, id)
	it := bkItems(e, id)["a.txt"]
	if tk.Status != StatusPartiallyFailed || it.Status != ItemFailed || !strings.Contains(it.Err, "copied, source not deleted") {
		t.Fatalf("task %v item %+v", tk.Status, it)
	}
	bkWantData(t, e, "bkt", "d/a.txt", "data:a.txt")
}

// A move in a versioned bucket must not resurrect older data: the source gets
// a delete marker (plain DeleteObject), the whole history stays.
func TestMoveVersionedLeavesDeleteMarkerAndKeepsHistory(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "m.txt", []byte("old"), "text/plain", nil)
	v2 := e.srv.PutObject("bkt", "m.txt", []byte("new"), "text/plain", nil)
	if v1 == v2 || v1 == "" {
		t.Fatalf("versions %q %q", v1, v2)
	}
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"m.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "dst/m.txt", "new")
	if d, ok := e.srv.Object("bkt", "m.txt"); ok {
		t.Fatalf("source key is still readable after the move: %q (the old version was resurrected)", d)
	}
	vs := e.srv.Versions("bkt", "m.txt")
	if len(vs) != 3 || !vs[0].DeleteMarker || vs[1].VersionID != v2 || vs[2].VersionID != v1 {
		t.Fatalf("source versions = %+v, want [marker, %s, %s]", vs, v2, v1)
	}
	page, err := e.s.ListVersions(testCtx(t), e.loc("bkt", ""), "", "")
	if err != nil {
		t.Fatal(err)
	}
	var hist []string
	for _, v := range page.Versions {
		if v.Key == "m.txt" {
			hist = append(hist, v.VersionID)
		}
	}
	if len(hist) != 3 || hist[0] != vs[0].VersionID || hist[1] != v2 || hist[2] != v1 {
		t.Fatalf("history = %v", hist)
	}
}

// If the source changes between the copy and the delete, the source is kept
// and the item reports it.
func TestMoveKeepsSourceChangedAfterCopy(t *testing.T) {
	for _, versioned := range []bool{false, true} {
		name := "unversioned"
		if versioned {
			name = "versioned"
		}
		t.Run(name, func(t *testing.T) {
			e := newS3Env(t)
			bkSeed(e, "bkt", versioned, "m.txt")
			var destHeads, deletes atomic.Int32
			e.srv.Inject(func(r *http.Request) *s3test.Fault {
				switch {
				case r.Method == http.MethodHead && r.URL.Path == "/bkt/dst/m.txt":
					// HEAD 1 is the conflict check; HEAD 2 verifies the copy,
					// right before the source is re-checked: a concurrent
					// writer replaces the source in between.
					if destHeads.Add(1) == 2 {
						e.srv.PutObject("bkt", "m.txt", []byte("replaced meanwhile"), "text/plain", nil)
					}
				case r.Method == http.MethodDelete && r.URL.Path == "/bkt/m.txt":
					deletes.Add(1)
				}
				return nil
			})
			id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"m.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true})
			if err != nil {
				t.Fatal(err)
			}
			tk := bkWait(t, e, id)
			it := bkItems(e, id)["m.txt"]
			if tk.Status != StatusPartiallyFailed || it.Status != ItemFailed || !strings.Contains(it.Err, "copied, but source changed since; source kept") {
				t.Fatalf("task %v item %+v", tk.Status, it)
			}
			if deletes.Load() != 0 {
				t.Fatal("the source was deleted although it changed after the copy")
			}
			bkWantData(t, e, "bkt", "m.txt", "replaced meanwhile")
			bkWantData(t, e, "bkt", "dst/m.txt", "data:m.txt")
		})
	}
}

func TestMoveRejectsExplicitVersions(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "m.txt", []byte("one"), "text/plain", nil)
	e.srv.PutObject("bkt", "m.txt", []byte("two"), "text/plain", nil)
	sel := Selection{Location: e.loc("bkt", ""), Versions: []ObjectRef{{"m.txt", v1}}}
	if id, err := e.s.Copy(sel, e.loc("bkt", "dst/"), CopyOptions{Move: true}); !IsKind(err, ErrInvalid) {
		t.Fatalf("id %q err %v, want Invalid", id, err)
	}
	if len(e.srv.Versions("bkt", "m.txt")) != 2 {
		t.Fatal("history changed")
	}
}

func TestCopyVersionsSelectionCopiesThatVersion(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "m.txt", []byte("one"), "text/plain", nil)
	e.srv.PutObject("bkt", "m.txt", []byte("two"), "text/plain", nil)
	sel := Selection{Location: e.loc("bkt", ""), Versions: []ObjectRef{{"m.txt", v1}}}
	id, err := e.s.Copy(sel, e.loc("bkt", "old/"), CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "old/m.txt", "one")
	bkWantData(t, e, "bkt", "m.txt", "two")
}

// ---- rename ---------------------------------------------------------------------------------------------------

func TestRename(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "docs/old.txt", "docs/dir/a", "docs/dir/b/c", "docs/dir/", "docs/other")
	loc := e.loc("bkt", "docs/")

	id, err := e.s.Rename(testCtx(t), loc, "docs/old.txt", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "docs/new.txt", "data:docs/old.txt")
	if _, ok := e.srv.Object("bkt", "docs/old.txt"); ok {
		t.Fatal("old name still exists")
	}

	id, err = e.s.Rename(testCtx(t), loc, "docs/dir/", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "bkt"), "docs/new.txt", "docs/other", "docs/renamed/a", "docs/renamed/b/c", "docs/renamed/")

	// onto an existing name: fails, nothing overwritten
	id, _ = e.s.Rename(testCtx(t), loc, "docs/other", "new.txt")
	tk := bkWait(t, e, id)
	if tk.Status != StatusPartiallyFailed {
		t.Fatalf("status %v", tk.Status)
	}
	bkWantData(t, e, "bkt", "docs/other", "data:docs/other")
	bkWantData(t, e, "bkt", "docs/new.txt", "data:docs/old.txt")
}

func TestRenameValidation(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/b.txt")
	loc := e.loc("bkt", "a/")
	for name, newName := range map[string]string{"empty": "", "slash": "x/y", "dot": ".", "dotdot": "..", "same": "b.txt", "ctrl": "a\nb"} {
		if _, err := e.s.Rename(testCtx(t), loc, "a/b.txt", newName); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.s.Rename(testCtx(t), loc, "", "x"); !IsKind(err, ErrInvalid) {
		t.Errorf("empty key: %v", err)
	}
	if _, err := e.s.Rename(testCtx(t), Location{AccountID: "nobody", Bucket: "bkt"}, "k", "x"); err == nil {
		t.Error("unknown account must fail")
	}
}

// ---- delete -----------------------------------------------------------------------------------------------------

func TestDeleteFolderAndKeys(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a/1", "a/2", "a/sub/3", "keep", "gone")
	id, err := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"a/", "gone", "not-there"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if tk.Items != 5 {
		t.Fatalf("items = %d", tk.Items)
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), "keep")
}

func TestDeleteMoreThanOneChunk(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", false)
	const n = 2300
	for i := 0; i < n; i++ {
		e.srv.PutObject("bkt", fmt.Sprintf("big/%05d", i), []byte("x"), "text/plain", nil)
	}
	e.srv.PutObject("bkt", "other", []byte("x"), "text/plain", nil)
	e.srv.ResetRequests()
	id, err := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"big/"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if tk.Items != n || tk.ItemsDone != n {
		t.Fatalf("task = %+v", tk)
	}
	bkWantKeys(t, bkLive(t, e, "bkt"), "other")
	posts := 0
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPost && strings.Contains(r.RawQuery, "delete") {
			posts++
		}
	}
	if posts != 3 {
		t.Fatalf("DeleteObjects requests = %d, want 3", posts)
	}
}

func TestDeleteVersionedBucketCreatesMarkers(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("x"), "text/plain", nil)
	id, _ := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}})
	bkOK(t, e, id)
	vs := e.srv.Versions("bkt", "k")
	if len(vs) != 2 || !vs[0].DeleteMarker || vs[1].VersionID != v1 {
		t.Fatalf("versions = %+v", vs)
	}
}

func TestDeleteUnknownOutcomeIsNotRetried(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "ok.txt", "drop.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "delete") {
			return &s3test.Fault{DropDeleteAcks: []string{"drop.txt"}}
		}
		return nil
	})
	id, err := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"ok.txt", "drop.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	items := bkItems(e, id)
	if tk.Status != StatusPartiallyFailed || items["ok.txt"].Status != ItemSucceeded || items["drop.txt"].Status != ItemUnknown {
		t.Fatalf("task %v items %+v", tk.Status, items)
	}
	e.srv.ResetRequests()
	if _, err := e.s.Retry(id); !IsKind(err, ErrInvalid) {
		t.Fatalf("Retry err = %v, want Invalid (unknown items are not replayed)", err)
	}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPost {
			t.Fatalf("a retry sent %+v", r)
		}
	}
}

// A 4xx answer to the whole DeleteObjects request is a definitive refusal:
// nothing was deleted, so the items are failed (and Retry replays them).
func TestDeleteWholeRequestRefusalIsFailedAndRetryable(t *testing.T) {
	for _, f := range []s3test.Fault{
		{Status: 400, Code: "MalformedXML", Message: "injected"},
		{Status: 403, Code: "AccessDenied", Message: "injected"},
	} {
		t.Run(f.Code, func(t *testing.T) {
			e := newS3Env(t)
			bkSeed(e, "bkt", false, "a", "b")
			fault := f
			e.srv.Inject(func(r *http.Request) *s3test.Fault {
				if r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "delete") {
					return &fault
				}
				return nil
			})
			id, _ := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"a", "b"}})
			tk := bkWait(t, e, id)
			items := bkItems(e, id)
			if tk.Status != StatusPartiallyFailed || items["a"].Status != ItemFailed || items["b"].Status != ItemFailed {
				t.Fatalf("task %v items %+v", tk.Status, items)
			}
			if !strings.Contains(items["a"].Err, f.Code) {
				t.Fatalf("item error %q does not name %s", items["a"].Err, f.Code)
			}
			bkWantKeys(t, bkLive(t, e, "bkt"), "a", "b")
			e.srv.Inject(nil)
			rid, err := e.s.Retry(id)
			if err != nil {
				t.Fatalf("Retry err = %v, want the failed items to be replayed", err)
			}
			bkOK(t, e, rid)
			bkWantKeys(t, bkLive(t, e, "bkt"))
		})
	}
}

// ---- cancel and retry --------------------------------------------------------------------------------------------

// bkCancelDuring cancels the task named by *id when the n-th request matching
// match arrives at the server.
func bkCancelDuring(e *s3Env, id *atomic.Value, n int32, match func(*http.Request) bool) {
	var seen atomic.Int32
	var once sync.Once
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if match(r) && seen.Add(1) == n {
			once.Do(func() {
				for i := 0; i < 500; i++ {
					if v, ok := id.Load().(string); ok {
						_ = e.s.Cancel(v)
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
			})
		}
		return nil
	})
}

func TestBulkCancelThenRetryProcessesOnlyUnprocessed(t *testing.T) {
	e := newS3Env(t)
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf("src/%02d", i)
	}
	bkSeed(e, "bkt", false, keys...)
	bkWorkersSetting(t, e, 1)
	bkSetPage(t, 5) // the listing spans several pages: part of it is never listed when we cancel

	var id atomic.Value
	bkCancelDuring(e, &id, 3, func(r *http.Request) bool {
		return r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != ""
	})
	tid, err := e.s.Copy(Selection{Location: e.loc("bkt", "src/"), All: true}, e.loc("bkt", "dst/"), CopyOptions{Conflict: ConflictOverwrite})
	if err != nil {
		t.Fatal(err)
	}
	id.Store(tid)
	tk := bkWait(t, e, tid)
	e.srv.Inject(nil)
	if tk.Status != StatusCanceled {
		t.Fatalf("status = %v, err %q", tk.Status, tk.Err)
	}
	items := e.s.TaskItems(tid)
	var done, unproc int
	marker := false
	for _, it := range items {
		switch it.Status {
		case ItemSucceeded:
			done++
		case ItemUnprocessed:
			unproc++
			if it.Key == bkRemainderKey {
				marker = true
			}
		default:
			t.Fatalf("unexpected item %+v", it)
		}
	}
	if done == 0 || done >= 20 || unproc == 0 || !marker {
		t.Fatalf("done %d unprocessed %d marker %v (items %d)", done, unproc, marker, len(items))
	}
	before := len(bkLive(t, e, "bkt"))

	rid, err := e.s.Retry(tid)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, rid)
	// The retry listed and copied the rest: every source object now has a copy.
	live := bkLive(t, e, "bkt")
	if len(live) != 40 || before >= 40 {
		t.Fatalf("live %d (before retry %d)", len(live), before)
	}
	for _, k := range keys {
		bkWantData(t, e, "bkt", "dst/"+strings.TrimPrefix(k, "src/"), "data:"+k)
	}
	// The retry did not touch what had already been copied.
	retried := e.s.TaskItems(rid)
	if len(retried) > unproc-1+20 { // sanity: never more than the unprocessed + remaining listing
		t.Fatalf("retry items = %d", len(retried))
	}
	copiedBefore := map[string]bool{}
	for _, it := range items {
		if it.Status == ItemSucceeded {
			copiedBefore[it.Key] = true
		}
	}
	for _, it := range retried {
		if copiedBefore[it.Key] {
			t.Fatalf("retry reprocessed %q", it.Key)
		}
	}
}

func TestBulkListingFailureLeavesRemainderForRetry(t *testing.T) {
	e := newS3Env(t)
	keys := make([]string, 9)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}
	bkSeed(e, "bkt", false, keys...)
	bkSetPage(t, 3)
	var lists atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if failing.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" && lists.Add(1) == 2 {
			return &s3test.Fault{Status: 400, Code: "InvalidArgument", Message: "injected list failure"}
		}
		return nil
	})
	id, _ := e.s.Delete(Selection{Location: e.loc("bkt", ""), All: true})
	tk := bkWait(t, e, id)
	if tk.Status != StatusPartiallyFailed {
		t.Fatalf("status %v err %q", tk.Status, tk.Err)
	}
	if it := bkItems(e, id)[bkRemainderKey]; it.Status != ItemFailed || !strings.Contains(it.Err, "listing stopped") {
		t.Fatalf("remainder item %+v", it)
	}
	failing.Store(false)
	rid, err := e.s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, rid)
	bkWantKeys(t, bkLive(t, e, "bkt"))
}

func TestBulkRetryOfSucceededTaskIsRejected(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a")
	id, _ := e.s.Delete(Selection{Location: e.loc("bkt", ""), Keys: []string{"a"}})
	bkOK(t, e, id)
	if _, err := e.s.Retry(id); !IsKind(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// Tags that cannot be read must fail a create-only (multipart) copy before any
// upload exists, so a Move never deletes a source whose tags were not carried over.
func TestMoveFailsWhenSourceTagsAreUnreadable(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "src/a.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Query().Has("tagging") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no tagging"}
		}
		return nil
	})
	e.srv.ResetRequests()
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"src/a.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	it := bkItems(e, id)["src/a.txt"]
	if tk.Status != StatusPartiallyFailed || it.Status != ItemFailed || !strings.Contains(it.Err, "cannot read source tags") || !strings.Contains(it.Err, "AccessDenied") {
		t.Fatalf("task %v item %+v", tk.Status, it)
	}
	e.srv.Inject(nil)
	bkWantData(t, e, "bkt", "src/a.txt", "data:src/a.txt")
	if _, ok := e.srv.Object("bkt", "dst/a.txt"); ok {
		t.Fatal("destination must not exist")
	}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPost && strings.Contains(r.RawQuery, "uploads") {
			t.Fatalf("a multipart upload was created before the tags were read: %+v", r)
		}
	}
}
