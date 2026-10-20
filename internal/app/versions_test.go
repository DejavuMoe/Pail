package app

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func bkVersionIDs(vs []s3test.VersionInfo) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.VersionID
	}
	return out
}

func TestVersionsListingOrderWithMarkers(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	put := func(k, data string) string { return e.srv.PutObject("bkt", k, []byte(data), "text/plain", nil) }
	a1, a2 := put("a", "a1"), put("a", "a2")
	b1 := put("b", "b1")
	bm := e.srv.DeleteObject("bkt", "b") // delete marker on top of b
	put("sub/x", "x1")
	d1 := put("d", "d1")
	time.Sleep(1100 * time.Millisecond)
	dm := e.srv.DeleteMarker("bkt", "d")
	time.Sleep(1100 * time.Millisecond)
	d2 := put("d", "d2")

	page, err := e.s.ListVersions(testCtx(t), e.loc("bkt", ""), "", "")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		key, id       string
		latest, isDel bool
	}
	var got []row
	for _, v := range page.Versions {
		got = append(got, row{v.Key, v.VersionID, v.Latest, v.DeleteMarker})
	}
	want := []row{
		{"a", a2, true, false}, {"a", a1, false, false},
		{"b", bm, true, true}, {"b", b1, false, false},
		{"d", d2, true, false}, {"d", dm, false, true}, {"d", d1, false, false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v\nall: %+v", i, got[i], want[i], got)
		}
	}
	if page.NextKeyMarker != "" || page.NextVersionMarker != "" {
		t.Fatalf("markers on the last page: %+v", page)
	}
	for _, v := range page.Versions {
		if v.DeleteMarker && (v.Size != 0 || v.ETag != "") {
			t.Fatalf("marker with content: %+v", v)
		}
		if !v.DeleteMarker && (v.Size == 0 || v.ETag == "" || v.StorageClass != "STANDARD" || v.Modified.IsZero()) {
			t.Fatalf("version incomplete: %+v", v)
		}
	}

	// one folder level only
	sub, err := e.s.ListVersions(testCtx(t), e.loc("bkt", "sub/"), "", "")
	if err != nil || len(sub.Versions) != 1 || sub.Versions[0].Key != "sub/x" {
		t.Fatalf("sub = %+v, %v", sub, err)
	}
}

func TestVersionsListingPagination(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	total := 0
	for _, k := range []string{"a", "b", "c"} {
		for i := 0; i < 3; i++ {
			e.srv.PutObject("bkt", k, []byte{byte(i)}, "text/plain", nil)
			total++
		}
	}
	bkSetPage(t, 4)
	seen := map[string]bool{}
	km, vm, pages := "", "", 0
	for {
		p, err := e.s.ListVersions(testCtx(t), e.loc("bkt", ""), km, vm)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, v := range p.Versions {
			id := v.Key + "@" + v.VersionID
			if seen[id] {
				t.Fatalf("duplicate %s", id)
			}
			seen[id] = true
		}
		if p.NextKeyMarker == "" {
			break
		}
		km, vm = p.NextKeyMarker, p.NextVersionMarker
	}
	if len(seen) != total || pages != 3 {
		t.Fatalf("saw %d versions in %d pages, want %d in 3", len(seen), pages, total)
	}
}

func TestRestoreVersionCreatesNewCurrentAndKeepsHistory(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("one"), "text/plain", map[string]string{"gen": "1"})
	v2 := e.srv.PutObject("bkt", "k", []byte("two"), "text/plain", map[string]string{"gen": "2"})
	loc := e.loc("bkt", "")
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k", VersionID: v1}); err != nil {
		t.Fatal(err)
	}
	bkWantData(t, e, "bkt", "k", "one")
	vs := e.srv.Versions("bkt", "k")
	if len(vs) != 3 {
		t.Fatalf("versions = %+v", vs)
	}
	ids := bkVersionIDs(vs)
	if ids[0] == v1 || ids[0] == v2 || ids[1] != v2 || ids[2] != v1 {
		t.Fatalf("history changed: %v (old ids %s %s)", ids, v1, v2)
	}
	info := bkHeadInfo(t, e, "bkt", "k")
	if info.Metadata["gen"] != "1" {
		t.Fatalf("metadata of the restored copy = %v", info.Metadata)
	}
}

func TestRestoreVersionRejections(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("one"), "text/plain", nil)
	marker := e.srv.DeleteObject("bkt", "k")
	loc := e.loc("bkt", "")
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k", VersionID: marker}); !IsKind(err, ErrInvalid) || !strings.Contains(err.Error(), "delete marker") {
		t.Fatalf("marker: %v", err)
	}
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k"}); !IsKind(err, ErrInvalid) {
		t.Fatalf("no version: %v", err)
	}
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k", VersionID: "missing"}); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	// restore and then restore the (now current) restored copy
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k", VersionID: v1}); err != nil {
		t.Fatal(err)
	}
	cur := e.srv.Versions("bkt", "k")[0]
	if err := e.s.RestoreVersion(testCtx(t), loc, ObjectRef{Key: "k", VersionID: cur.VersionID}); !IsKind(err, ErrInvalid) {
		t.Fatalf("already current: %v", err)
	}
}

func TestRestoreDeletedSkipsLiveKeys(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	e.srv.PutObject("bkt", "d/a", []byte("a1"), "text/plain", nil)
	e.srv.PutObject("bkt", "d/a", []byte("a2"), "text/plain", nil)
	e.srv.DeleteObject("bkt", "d/a")
	e.srv.PutObject("bkt", "d/b", []byte("live"), "text/plain", nil)
	e.srv.PutObject("bkt", "d/c", []byte("c1"), "text/plain", nil)
	e.srv.DeleteObject("bkt", "d/c")
	e.srv.PutObject("bkt", "d/sub/f", []byte("f1"), "text/plain", nil)
	e.srv.DeleteObject("bkt", "d/sub/f")
	e.srv.DeleteMarker("bkt", "d/only-marker")
	loc := e.loc("bkt", "")

	// explicit keys: live and missing keys are reported, not touched
	id, err := e.s.RestoreDeleted(Selection{Location: loc, Keys: []string{"d/a", "d/b", "d/nope"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	items := bkItems(e, id)
	if tk.Status != StatusPartiallyFailed || items["d/a"].Status != ItemSucceeded {
		t.Fatalf("task %v items %+v", tk.Status, items)
	}
	if it := items["d/b"]; it.Status != ItemSkipped || it.Err != "not deleted" {
		t.Fatalf("live key: %+v", it)
	}
	if items["d/nope"].Status != ItemFailed {
		t.Fatalf("missing key: %+v", items["d/nope"])
	}
	bkWantData(t, e, "bkt", "d/a", "a2") // the newest non-marker version
	bkWantData(t, e, "bkt", "d/b", "live")
	if n := len(e.srv.Versions("bkt", "d/a")); n != 4 {
		t.Fatalf("history of d/a has %d entries, want 4 (history is never deleted)", n)
	}

	// a folder: only deleted keys with history are restored (d/a is live again)
	id, err = e.s.RestoreDeleted(Selection{Location: loc, Keys: []string{"d/"}})
	if err != nil {
		t.Fatal(err)
	}
	tk = bkOK(t, e, id)
	if tk.Items != 2 {
		t.Fatalf("items = %d: %+v", tk.Items, e.s.TaskItems(id))
	}
	bkWantData(t, e, "bkt", "d/c", "c1")
	bkWantData(t, e, "bkt", "d/sub/f", "f1")
	bkWantKeys(t, bkLive(t, e, "bkt"), "d/a", "d/b", "d/c", "d/sub/f")

	// everything again: nothing is deleted any more
	id, _ = e.s.RestoreDeleted(Selection{Location: loc, All: true})
	if tk := bkOK(t, e, id); tk.Items != 0 {
		t.Fatalf("items = %d", tk.Items)
	}
}

func TestRestoreDeletedLosesToConcurrentUpload(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	e.srv.PutObject("bkt", "k", []byte("old"), "text/plain", nil)
	e.srv.DeleteObject("bkt", "k")
	var once sync.Once
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		// Someone uploads the key right before the restore commits.
		if r.Method == http.MethodPost && r.URL.Query().Has("uploadId") {
			once.Do(func() { e.srv.PutObject("bkt", "k", []byte("NEW upload"), "text/plain", nil) })
		}
		return nil
	})
	id, err := e.s.RestoreDeleted(Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	e.srv.Inject(nil)
	it := bkItems(e, id)["k"]
	if tk.Status != StatusSucceeded || it.Status != ItemSkipped || !strings.Contains(it.Err, "uploaded meanwhile") {
		t.Fatalf("task %v item %+v", tk.Status, it)
	}
	bkWantData(t, e, "bkt", "k", "NEW upload")
}

func TestRestoreDeletedPagesAcrossListings(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		e.srv.PutObject("bkt", k, []byte(k+"1"), "text/plain", nil)
		e.srv.PutObject("bkt", k, []byte(k+"2"), "text/plain", nil)
		if k != "c" { // c stays live
			e.srv.DeleteObject("bkt", k)
		}
	}
	bkSetPage(t, 3) // a key's history straddles page boundaries
	id, _ := e.s.RestoreDeleted(Selection{Location: e.loc("bkt", ""), All: true})
	tk := bkOK(t, e, id)
	if tk.Items != 4 {
		t.Fatalf("items = %d: %+v", tk.Items, e.s.TaskItems(id))
	}
	for _, k := range []string{"a", "b", "d", "e"} {
		bkWantData(t, e, "bkt", k, k+"2")
	}
}

func TestDeleteVersionsIsExact(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("one"), "text/plain", nil)
	v2 := e.srv.PutObject("bkt", "k", []byte("two"), "text/plain", nil)
	marker := e.srv.DeleteObject("bkt", "k")
	e.srv.PutObject("bkt", "other", []byte("o"), "text/plain", nil)
	loc := e.loc("bkt", "")

	id, err := e.s.DeleteVersions(loc, []ObjectRef{{"k", v1}, {"k", v1}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if tk.Items != 1 {
		t.Fatalf("duplicates must collapse: %d items", tk.Items)
	}
	if got := bkVersionIDs(e.srv.Versions("bkt", "k")); strings.Join(got, ",") != marker+","+v2 {
		t.Fatalf("versions = %v, want [%s %s]", got, marker, v2)
	}
	bkWantData(t, e, "bkt", "other", "o")

	// removing the marker undeletes the key
	id, _ = e.s.DeleteVersions(loc, []ObjectRef{{"k", marker}})
	bkOK(t, e, id)
	bkWantData(t, e, "bkt", "k", "two")

	// Delete with explicit versions is permanent too
	id, _ = e.s.Delete(Selection{Location: loc, Versions: []ObjectRef{{"k", v2}}})
	bkOK(t, e, id)
	if len(e.srv.Versions("bkt", "k")) != 0 {
		t.Fatalf("versions left: %+v", e.srv.Versions("bkt", "k"))
	}
}

func TestDeleteVersionsNullVersionAndValidation(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("plain", false)
	if v := e.srv.PutObject("plain", "k", []byte("x"), "text/plain", nil); v != "null" {
		t.Fatalf("unversioned put returned version %q", v)
	}
	loc := e.loc("plain", "")
	id, err := e.s.DeleteVersions(loc, []ObjectRef{{"k", "null"}})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "plain"))
	if _, err := e.s.DeleteVersions(loc, nil); !IsKind(err, ErrInvalid) {
		t.Errorf("no refs: %v", err)
	}
	if _, err := e.s.DeleteVersions(loc, []ObjectRef{{"k", ""}}); !IsKind(err, ErrInvalid) {
		t.Errorf("no version id: %v", err)
	}
	if _, err := e.s.DeleteVersions(loc, []ObjectRef{{"", "v"}}); !IsKind(err, ErrInvalid) {
		t.Errorf("no key: %v", err)
	}
}

// bkHistory pages through ListObjectVersions with URL-encoded markers; the
// marker it feeds back must be raw, or the key's older versions are skipped.
func TestFxRestoreDeletedHistoryEncodedMarkerAcrossPages(t *testing.T) {
	e := newS3Env(t)
	bkSetPage(t, 2)
	e.srv.CreateBucket("bkt", true)
	key := "a b 文"
	e.srv.PutObject("bkt", key, []byte("precious"), "text/plain", nil)
	for i := 0; i < 5; i++ {
		e.srv.DeleteMarker("bkt", key)
	}
	id, err := e.s.RestoreDeleted(Selection{Location: e.loc("bkt", ""), Keys: []string{key}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	if it := bkItems(e, id)[key]; tk.Status != StatusSucceeded || it.Status != ItemSucceeded {
		t.Fatalf("task %v item %+v (all %+v)", tk.Status, it, e.s.TaskItems(id))
	}
	bkWantData(t, e, "bkt", key, "precious")
}
