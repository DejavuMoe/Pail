package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestBucketNameValidation(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"abc", true}, {"my-bucket.v2", true}, {"a1b", true}, {strings.Repeat("a", 63), true},
		{"ab", false}, {strings.Repeat("a", 64), false}, {"", false},
		{"Abc", false}, {"ab_c", false}, {"-abc", false}, {"abc-", false}, {".abc", false}, {"abc.", false},
		{"a..b", false}, {"192.168.1.1", false}, {"1.2.3.4", false}, {"xn--abc", false},
		{"abc-s3alias", false}, {"abc--ol-s3", false}, {"ab c", false}, {"abc/d", false}, {"日本語bucket", false},
		{"1.2.3.abc", true},
	}
	for _, tc := range cases {
		err := bmValidateBucketName(tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("%q: ok=%v err=%v", tc.name, tc.ok, err)
		}
	}
	e := newS3Env(t)
	err := e.s.CreateBucket(testCtx(t), e.acc.ID, "Bad_Name", "", false)
	if !IsKind(err, ErrInvalid) {
		t.Fatalf("want Invalid, got %v", err)
	}
	if len(e.srv.Requests()) != 0 {
		t.Fatal("an invalid name must not reach the service")
	}
}

func TestBucketCreateRegionAndObjectLock(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	for _, tc := range []struct {
		name, region, want string
		lock               bool
	}{
		{"plain-default", "", "us-east-1", false},
		{"plain-east", "us-east-1", "us-east-1", false},
		{"in-eu", "eu-west-1", "eu-west-1", false},
		{"locked-bkt", "eu-central-1", "eu-central-1", true},
	} {
		if err := e.s.CreateBucket(ctx, e.acc.ID, tc.name, tc.region, tc.lock); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	bs, err := e.s.Buckets(ctx, e.acc.ID)
	if err != nil || len(bs) != 4 {
		t.Fatalf("%v %v", bs, err)
	}
	got := map[string]string{}
	for _, b := range bs {
		got[b.Name] = b.Region
	}
	for n, want := range map[string]string{"plain-default": "us-east-1", "plain-east": "us-east-1", "in-eu": "eu-west-1", "locked-bkt": "eu-central-1"} {
		if got[n] != want {
			t.Errorf("%s region=%q want %q", n, got[n], want)
		}
	}
	if u := e.s.PublicURL(e.loc("in-eu", ""), "k"); u == "" {
		t.Fatal("empty public URL")
	}
	js, err := e.s.GetBucketConfig(ctx, e.acc.ID, "locked-bkt", ConfigObjectLock)
	if err != nil || !strings.Contains(js, "Enabled") {
		t.Fatalf("object lock not enabled: %q %v", js, err)
	}
	if js, _ := e.s.GetBucketConfig(ctx, e.acc.ID, "in-eu", ConfigObjectLock); js != "" {
		t.Fatalf("object lock must be off: %q", js)
	}
	if err := e.s.CreateBucket(ctx, e.acc.ID, "in-eu", "", false); !IsKind(err, ErrConflict) {
		t.Fatalf("duplicate: want Conflict, got %v", err)
	}
}

func TestBucketDeleteNonEmptyIsConflict(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("full", false)
	e.srv.PutObject("full", "k", []byte("x"), "", nil)
	err := e.s.DeleteBucket(ctx, e.acc.ID, "full")
	if !IsKind(err, ErrConflict) || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("want Conflict with message, got %v", err)
	}
	e.srv.DeleteObject("full", "k")
	if err := e.s.DeleteBucket(ctx, e.acc.ID, "full"); err != nil {
		t.Fatal(err)
	}
	if len(e.srv.Buckets()) != 0 {
		t.Fatal("bucket still exists")
	}
	if err := e.s.DeleteBucket(ctx, e.acc.ID, "full"); !IsKind(err, ErrNotFound) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func bmWait(t *testing.T, e *s3Env, id string) Task {
	t.Helper()
	tk, err := e.s.waitTask(testCtx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestBucketEmptyCurrentOnly(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("ver", true)
	for i := 0; i < 5; i++ {
		e.srv.PutObject("ver", fmt.Sprintf("d/k%d", i), []byte("v1"), "", nil)
		e.srv.PutObject("ver", fmt.Sprintf("d/k%d", i), []byte("v2"), "", nil)
	}
	id, err := e.s.EmptyBucket(e.acc.ID, "ver", false)
	if err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusSucceeded || tk.Items != 5 || tk.ItemsDone != 5 || tk.Kind != KindOther {
		t.Fatalf("%+v", tk)
	}
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("d/k%d", i)
		if _, ok := e.srv.Object("ver", k); ok {
			t.Fatalf("%s still current", k)
		}
		vs := e.srv.Versions("ver", k)
		if len(vs) != 3 || !vs[0].DeleteMarker { // marker + 2 old versions
			t.Fatalf("%s versions=%+v", k, vs)
		}
	}
}

func TestBucketEmptyAllVersions(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("ver", true)
	for i := 0; i < 4; i++ {
		k := fmt.Sprintf("k%d", i)
		e.srv.PutObject("ver", k, []byte("v1"), "", nil)
		e.srv.PutObject("ver", k, []byte("v2"), "", nil)
		if i%2 == 0 {
			e.srv.DeleteObject("ver", k) // delete marker on top
		}
	}
	e.srv.DeleteMarker("ver", "marker-only")
	id, err := e.s.EmptyBucket(e.acc.ID, "ver", true)
	if err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusSucceeded {
		t.Fatalf("%+v %+v", tk, e.s.TaskItems(id))
	}
	for _, k := range []string{"k0", "k1", "k2", "k3", "marker-only"} {
		if vs := e.srv.Versions("ver", k); len(vs) != 0 {
			t.Fatalf("%s left %+v", k, vs)
		}
	}
	if err := e.s.DeleteBucket(testCtx(t), e.acc.ID, "ver"); err != nil {
		t.Fatalf("bucket should now be deletable: %v", err)
	}
}

func TestBucketEmptyManyObjectsAcrossChunks(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("big", false)
	for i := 0; i < 1105; i++ {
		e.srv.PutObject("big", fmt.Sprintf("p/%05d", i), nil, "", nil)
	}
	id, err := e.s.EmptyBucket(e.acc.ID, "big", false)
	if err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusSucceeded || tk.ItemsDone != 1105 {
		t.Fatalf("%+v", tk)
	}
	if err := e.s.DeleteBucket(testCtx(t), e.acc.ID, "big"); err != nil {
		t.Fatal(err)
	}
}

func TestBucketEmptyCancelLeavesUnprocessed(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("slow", false)
	for i := 0; i < 3; i++ {
		e.srv.PutObject("slow", fmt.Sprintf("k%d", i), nil, "", nil)
	}
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
			return &s3test.Fault{Delay: 2 * time.Second}
		}
		return nil
	})
	id, err := e.s.EmptyBucket(e.acc.ID, "slow", false)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		hit := false
		for _, r := range e.srv.Requests() {
			if r.Method == http.MethodPost && strings.Contains(r.RawQuery, "delete") {
				hit = true
			}
		}
		if hit {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delete request never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.s.Cancel(id); err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusCanceled {
		t.Fatalf("%+v", tk)
	}
	unprocessed := 0
	for _, it := range e.s.TaskItems(id) {
		if it.Status == ItemUnprocessed {
			unprocessed++
		}
	}
	if unprocessed == 0 {
		t.Fatalf("no unprocessed items: %+v", e.s.TaskItems(id))
	}
	for i := 0; i < 3; i++ {
		if _, ok := e.srv.Object("slow", fmt.Sprintf("k%d", i)); !ok {
			t.Fatal("object deleted although the request never completed")
		}
	}
	// Retry is possible and finishes the job.
	e.srv.Inject(nil)
	rid, err := e.s.Retry(id)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if tk := bmWait(t, e, rid); tk.Status != StatusSucceeded {
		t.Fatalf("%+v", tk)
	}
	if _, ok := e.srv.Object("slow", "k0"); ok {
		t.Fatal("retry did not empty the bucket")
	}
}

func TestBucketEmptyValidatesArguments(t *testing.T) {
	e := newS3Env(t)
	if _, err := e.s.EmptyBucket("", "b", false); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := e.s.EmptyBucket(e.acc.ID, "", false); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// ListObjectVersions answers with URL-encoded keys and markers; EmptyBucket
// must hand the decoded marker back, or the second page skips keys.
func TestFxEmptyBucketAllVersionsEncodedMarkersAcrossPages(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("enc", true)
	const n = 1100 // more than one ListObjectVersions page
	for i := 0; i < n; i++ {
		e.srv.PutObject("enc", fmt.Sprintf("a b %05d 文+%%", i), []byte("x"), "", nil)
	}
	id, err := e.s.EmptyBucket(e.acc.ID, "enc", true)
	if err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusSucceeded || tk.ItemsDone != n {
		t.Fatalf("%+v", tk)
	}
	left := 0
	for i := 0; i < n; i++ {
		left += len(e.srv.Versions("enc", fmt.Sprintf("a b %05d 文+%%", i)))
	}
	if left != 0 {
		t.Fatalf("%d versions left behind", left)
	}
	if err := e.s.DeleteBucket(testCtx(t), e.acc.ID, "enc"); err != nil {
		t.Fatalf("bucket should now be deletable: %v", err)
	}
}

// A truncated listing without a continuation token must fail the task instead
// of silently pretending the bucket is empty.
func TestFxEmptyBucketTruncatedWithoutTokenFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>b</Name><Prefix></Prefix><KeyCount>0</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>true</IsTruncated></ListBucketResult>`)
	}))
	defer ts.Close()
	s := newTestService(t, nil)
	acc, err := s.SaveAccount(staticAccount("trunc", ts.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.EmptyBucket(acc.ID, "b", false)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := s.waitTask(testCtx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status == StatusSucceeded {
		t.Fatalf("a truncated listing without a token reported success: %+v", tk)
	}
	found := false
	for _, it := range s.TaskItems(id) {
		if it.Status == ItemUnprocessed && strings.Contains(it.Err, "continuation token") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no remainder item naming the problem: %+v", s.TaskItems(id))
	}
}
