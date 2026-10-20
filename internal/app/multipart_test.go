package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestMultipartListAndAbort(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("mp", false)
	var ids []string
	for _, k := range []string{"a/one", "a/two", "b/three"} {
		out, err := e.sdk().CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("mp"), Key: aws.String(k)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, aws.ToString(out.UploadId))
	}
	ups, err := e.s.MultipartUploads(ctx, e.loc("mp", ""))
	if err != nil || len(ups) != 3 {
		t.Fatalf("%+v %v", ups, err)
	}
	for _, u := range ups {
		if u.Key == "" || u.UploadID == "" || u.Initiated.IsZero() || u.StorageClass != "STANDARD" {
			t.Fatalf("incomplete %+v", u)
		}
	}
	if sub, err := e.s.MultipartUploads(ctx, e.loc("mp", "a/")); err != nil || len(sub) != 2 {
		t.Fatalf("prefix: %+v %v", sub, err)
	}
	if _, err := e.s.MultipartUploads(ctx, Location{AccountID: e.acc.ID}); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}

	// Abort two real uploads and one that never existed.
	sort.Slice(ups, func(i, j int) bool { return ups[i].Key < ups[j].Key })
	sel := []MultipartUpload{ups[0], ups[1], {Key: "ghost", UploadID: "nope"}}
	id, err := e.s.AbortMultipartUploads(e.loc("mp", ""), sel)
	if err != nil {
		t.Fatal(err)
	}
	tk := bmWait(t, e, id)
	if tk.Status != StatusSucceeded || tk.Items != 3 || tk.ItemsDone != 3 || tk.Kind != KindOther {
		t.Fatalf("%+v", tk)
	}
	gone := 0
	for _, it := range e.s.TaskItems(id) {
		if it.Status != ItemSucceeded {
			t.Fatalf("%+v", it)
		}
		if it.Key == "ghost" {
			if it.Err != "already gone" {
				t.Fatalf("%+v", it)
			}
			gone++
		}
	}
	if gone != 1 {
		t.Fatal("ghost item missing")
	}
	left, err := e.s.MultipartUploads(ctx, e.loc("mp", ""))
	if err != nil || len(left) != 1 || left[0].Key != "b/three" {
		t.Fatalf("%+v %v", left, err)
	}
	_ = ids
}

func TestMultipartAbortValidation(t *testing.T) {
	e := newS3Env(t)
	if _, err := e.s.AbortMultipartUploads(e.loc("mp", ""), nil); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := e.s.AbortMultipartUploads(e.loc("mp", ""), []MultipartUpload{{Key: "k"}}); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := e.s.AbortMultipartUploads(Location{AccountID: e.acc.ID}, []MultipartUpload{{Key: "k", UploadID: "u"}}); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// MinIO answers ListMultipartUploads with nothing when Prefix is a folder
// rather than a whole key. A proxy in front of s3test reproduces that.
func TestMultipartListFolderPrefixIgnoredByServer(t *testing.T) {
	srv := s3test.New()
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(r *http.Request) {
		direct(r)
		q := r.URL.Query()
		if q.Has("uploads") && q.Get("prefix") != "" {
			q.Set("prefix", q.Get("prefix")+"\x00no-such-key")
			r.URL.RawQuery = q.Encode()
		}
	}
	ps := httptest.NewServer(proxy)
	t.Cleanup(ps.Close)
	s := newTestService(t, nil)
	acc, err := s.SaveAccount(staticAccount("minio-like", ps.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	e := &s3Env{t: t, s: s, srv: srv, acc: acc}
	ctx := testCtx(t)
	srv.CreateBucket("mp", false)
	for _, k := range []string{"mp/a.bin", "mp/sub/b.bin", "other/c.bin"} {
		if _, err := e.sdk().CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("mp"), Key: aws.String(k)}); err != nil {
			t.Fatal(err)
		}
	}
	ups, err := s.MultipartUploads(ctx, e.loc("mp", "mp/"))
	if err != nil || len(ups) != 2 {
		t.Fatalf("got %+v err=%v, want the two uploads below mp/", ups, err)
	}
	for _, u := range ups {
		if !strings.HasPrefix(u.Key, "mp/") {
			t.Errorf("upload outside the prefix: %s", u.Key)
		}
	}
	if none, err := s.MultipartUploads(ctx, e.loc("mp", "empty/")); err != nil || len(none) != 0 {
		t.Fatalf("empty folder: %+v err=%v", none, err)
	}
}

func TestMultipartListPagesAndKeysNeedingEncoding(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("mp", false)
	const n = 1005 // more than one ListMultipartUploads page
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("dir/%04d 文件", i)
		if _, err := e.sdk().CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("mp"), Key: aws.String(key)}); err != nil {
			t.Fatal(err)
		}
	}
	ups, err := e.s.MultipartUploads(ctx, e.loc("mp", "dir/"))
	if err != nil || len(ups) != n {
		t.Fatalf("got %d err=%v", len(ups), err)
	}
	seen := map[string]bool{}
	for _, u := range ups {
		seen[u.Key] = true
	}
	if len(seen) != n || !seen["dir/0000 文件"] {
		t.Fatal("keys not decoded or duplicated across pages")
	}
}
