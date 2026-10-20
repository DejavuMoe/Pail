package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// s3Env is a Service wired to an in-memory S3 server through one saved account.
type s3Env struct {
	t   *testing.T
	s   *Service
	srv *s3test.Server
	acc Account
}

func newS3Env(t *testing.T) *s3Env {
	t.Helper()
	srv := s3test.New()
	t.Cleanup(srv.Close)
	s := newTestService(t, nil)
	acc, err := s.SaveAccount(staticAccount("mem", srv.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	return &s3Env{t: t, s: s, srv: srv, acc: acc}
}

func (e *s3Env) loc(bucket, prefix string) Location {
	return Location{AccountID: e.acc.ID, Bucket: bucket, Prefix: prefix}
}

func (e *s3Env) sdk() *s3.Client {
	c, err := e.s.storageClient(context.Background(), e.acc.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return c.S3()
}

func names(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
		if e.Folder {
			out[i] += "/"
		}
	}
	sort.Strings(out)
	return out
}

func TestBuckets(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("plain", false)
	if _, err := e.sdk().CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("eu-bkt"),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraintEuWest1}}); err != nil {
		t.Fatal(err)
	}
	bs, err := e.s.Buckets(testCtx(t), e.acc.ID)
	if err != nil || len(bs) != 2 {
		t.Fatalf("%v %v", bs, err)
	}
	by := map[string]Bucket{}
	for _, b := range bs {
		by[b.Name] = b
		if b.Created.IsZero() {
			t.Errorf("%s: no creation date", b.Name)
		}
	}
	if by["eu-bkt"].Region != "eu-west-1" {
		t.Fatalf("region not reported: %+v", by)
	}
	if _, err := e.s.Buckets(testCtx(t), "nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := e.s.Buckets(testCtx(t), ""); !IsKind(err, ErrInvalid) {
		t.Fatalf("empty account: %v", err)
	}
	// Learnt regions feed PublicURL (here: a bucket living in eu-west-1 on an AWS account).
	awsAcc, _ := e.s.SaveAccount(Account{Name: "aws", Provider: ProviderAWS, Region: "us-east-1", Credentials: CredDefault}, nil)
	e.s.mu.Lock()
	e.s.bucketRegion[awsAcc.ID+"/eu-bkt"] = "eu-west-1"
	e.s.mu.Unlock()
	if got := e.s.PublicURL(Location{AccountID: awsAcc.ID, Bucket: "eu-bkt"}, "k"); got != "https://eu-bkt.s3.eu-west-1.amazonaws.com/k" {
		t.Fatal(got)
	}
	// The mem account itself learnt the region through Buckets().
	if r := e.s.bucketRegion[e.acc.ID+"/eu-bkt"]; r != "eu-west-1" {
		t.Fatalf("region not remembered: %q", r)
	}
}

func TestListFoldersObjectsAndPlaceholders(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	for _, k := range []string{"a/1.txt", "a/2.txt", "a/", "a/sub/deep.txt", "b/x", "top.txt", "dir name/ü ñ+%.txt"} {
		e.srv.PutObject("b", k, []byte(k), "text/plain", nil)
	}
	root, err := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(root.Entries); fmt.Sprint(got) != "[a/ b/ dir name/ top.txt]" {
		t.Fatalf("root: %v", got)
	}
	for _, en := range root.Entries {
		switch en.Name {
		case "a":
			if !en.Folder || en.Key != "a/" {
				t.Fatalf("folder entry %+v", en)
			}
		case "top.txt":
			if en.Folder || en.Key != "top.txt" || en.Size != int64(len("top.txt")) || en.ETag == "" || en.Modified.IsZero() || en.StorageClass != "STANDARD" {
				t.Fatalf("object entry %+v", en)
			}
		}
	}
	sub, _ := e.s.List(testCtx(t), e.loc("b", "a/"), ListOptions{})
	if got := names(sub.Entries); fmt.Sprint(got) != "[1.txt 2.txt sub/]" {
		t.Fatalf("a/: %v (the placeholder \"a/\" itself must be skipped)", got)
	}
	// Special characters survive (url-encoded listing).
	dir, _ := e.s.List(testCtx(t), e.loc("b", "dir name/"), ListOptions{})
	if len(dir.Entries) != 1 || dir.Entries[0].Key != "dir name/ü ñ+%.txt" || dir.Entries[0].Name != "ü ñ+%.txt" {
		t.Fatalf("encoded key: %+v", dir.Entries)
	}
	rootDirs, _ := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{})
	for _, en := range rootDirs.Entries {
		if en.Name == "dir name" && en.Key != "dir name/" {
			t.Fatalf("folder key %q", en.Key)
		}
	}
	// Flat: every key below the prefix, placeholders shown as folders.
	flat, _ := e.s.List(testCtx(t), e.loc("b", "a/"), ListOptions{Flat: true})
	var keys []string
	for _, en := range flat.Entries {
		keys = append(keys, en.Key)
	}
	sort.Strings(keys)
	if fmt.Sprint(keys) != "[a/1.txt a/2.txt a/sub/deep.txt]" {
		t.Fatalf("flat: %v", keys)
	}
	all, _ := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{Flat: true})
	if len(all.Entries) != 7 {
		t.Fatalf("flat root has %d entries", len(all.Entries))
	}
	for _, en := range all.Entries {
		if en.Key == "a/" && (!en.Folder || en.Name != "a") {
			t.Fatalf("placeholder in flat mode: %+v", en)
		}
	}
}

func TestListPagination(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	var want []string
	for i := 0; i < 25; i++ {
		k := fmt.Sprintf("f%02d", i)
		want = append(want, k)
		e.srv.PutObject("b", k, []byte("x"), "", nil)
	}
	var got []string
	tok := ""
	pages := 0
	for {
		p, err := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{MaxKeys: 10, Token: tok})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(p.Entries) > 10 {
			t.Fatalf("page too large: %d", len(p.Entries))
		}
		for _, en := range p.Entries {
			got = append(got, en.Key)
		}
		if p.NextToken == "" {
			break
		}
		tok = p.NextToken
	}
	if pages != 3 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pages=%d got=%v", pages, got)
	}
	// Default page size is 1000.
	p, _ := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{})
	if len(p.Entries) != 25 || p.NextToken != "" {
		t.Fatalf("default page: %d entries token %q", len(p.Entries), p.NextToken)
	}
	// The request asked for the default max-keys.
	reqs := e.srv.Requests()
	if q := reqs[len(reqs)-1].RawQuery; !strings.Contains(q, "max-keys=1000") || !strings.Contains(q, "delimiter=%2F") {
		t.Fatalf("query %q", q)
	}
}

func TestListErrors(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	if _, err := e.s.List(testCtx(t), e.loc("b", "no-slash"), ListOptions{}); !IsKind(err, ErrInvalid) {
		t.Fatalf("bad prefix: %v", err)
	}
	if _, err := e.s.List(testCtx(t), e.loc("", ""), ListOptions{}); !IsKind(err, ErrInvalid) {
		t.Fatalf("no bucket: %v", err)
	}
	if _, err := e.s.List(testCtx(t), Location{Bucket: "b"}, ListOptions{}); !IsKind(err, ErrInvalid) {
		t.Fatalf("no account: %v", err)
	}
	if _, err := e.s.List(testCtx(t), Location{AccountID: "ghost", Bucket: "b"}, ListOptions{}); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	_, err := e.s.List(testCtx(t), e.loc("missing", ""), ListOptions{})
	var oe *OpError
	if !IsKind(err, ErrNotFound) || !errors.As(err, &oe) || oe.Code != "NoSuchBucket" || oe.Op != "List" {
		t.Fatalf("missing bucket: %#v", err)
	}
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no"}
	})
	if _, err := e.s.List(testCtx(t), e.loc("b", ""), ListOptions{}); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("403: %v", err)
	}
	e.srv.Inject(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.s.List(ctx, e.loc("b", ""), ListOptions{}); !IsKind(err, ErrCanceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestHeadFullInfo(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", true)
	ctx := testCtx(t)
	_, err := e.sdk().PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("b"), Key: aws.String("dir/file.txt"), Body: strings.NewReader("hello world"),
		ContentType: aws.String("text/plain"), ContentEncoding: aws.String("identity"), ContentLanguage: aws.String("en"),
		ContentDisposition: aws.String(`attachment; filename="f.txt"`), CacheControl: aws.String("max-age=60"),
		Metadata: map[string]string{"Owner": "Ann", "x-Mixed": "V"}, StorageClass: types.StorageClassStandardIa,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.sdk().PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String("b"), Key: aws.String("dir/file.txt"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}}}}); err != nil {
		t.Fatal(err)
	}
	info, err := e.s.Head(ctx, e.loc("b", ""), "dir/file.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	wantHeaders := []Header{{"Content-Type", "text/plain"}, {"Content-Encoding", "identity"}, {"Content-Language", "en"},
		{"Content-Disposition", `attachment; filename="f.txt"`}, {"Cache-Control", "max-age=60"}, {"Expires", ""}}
	if fmt.Sprint(info.Headers) != fmt.Sprint(wantHeaders) {
		t.Fatalf("headers %v", info.Headers)
	}
	if info.Key != "dir/file.txt" || info.Size != 11 || info.ContentType != "text/plain" || info.StorageClass != "STANDARD_IA" ||
		info.ETag == "" || info.Modified.IsZero() || info.VersionID == "" {
		t.Fatalf("%+v", info)
	}
	if info.Metadata["owner"] != "Ann" || info.Metadata["x-mixed"] != "V" || len(info.Metadata) != 2 {
		t.Fatalf("metadata %v", info.Metadata)
	}
	if info.Tags["env"] != "prod" || info.TagsErr != nil {
		t.Fatalf("tags %v / %v", info.Tags, info.TagsErr)
	}
	if info.ReadOnlyReason != "" {
		t.Fatalf("plain object must be editable: %q", info.ReadOnlyReason)
	}
	// An explicit version returns that version.
	v2 := e.srv.PutObject("b", "dir/file.txt", []byte("v2"), "text/plain", nil)
	old, err := e.s.Head(ctx, e.loc("b", ""), "dir/file.txt", info.VersionID)
	if err != nil || old.Size != 11 || old.VersionID != info.VersionID {
		t.Fatalf("old version: %+v %v", old, err)
	}
	cur, _ := e.s.Head(ctx, e.loc("b", ""), "dir/file.txt", "")
	if cur.VersionID != v2 || cur.Size != 2 {
		t.Fatalf("current: %+v", cur)
	}
}

func TestHeadErrorsAndTagFailure(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	e.srv.PutObject("b", "k", []byte("x"), "", nil)
	if _, err := e.s.Head(testCtx(t), e.loc("b", ""), "missing", ""); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := e.s.Head(testCtx(t), e.loc("b", ""), "", ""); !IsKind(err, ErrInvalid) {
		t.Fatalf("empty key: %v", err)
	}
	// Tagging not implemented: Head still succeeds, TagsErr explains.
	e.srv.SetUnsupported("tagging")
	info, err := e.s.Head(testCtx(t), e.loc("b", ""), "k", "")
	if err != nil || info.Tags != nil || !IsKind(info.TagsErr, ErrUnsupported) {
		t.Fatalf("unsupported tagging: %+v err=%v", info, err)
	}
	e.srv.ClearUnsupported("tagging")
	// Tagging denied is AccessDenied, never "unsupported".
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if _, ok := r.URL.Query()["tagging"]; ok {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no s3:GetObjectTagging"}
		}
		return nil
	})
	info, err = e.s.Head(testCtx(t), e.loc("b", ""), "k", "")
	if err != nil || info.Tags != nil || !IsKind(info.TagsErr, ErrAccessDenied) {
		t.Fatalf("denied tagging: %+v err=%v", info, err)
	}
}

func TestHeadReadOnlyReasons(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	ctx := testCtx(t)
	put := func(key string, mod func(*s3.PutObjectInput)) {
		in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String(key), Body: strings.NewReader("secret data")}
		mod(in)
		if _, err := e.sdk().PutObject(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	put("kms", func(in *s3.PutObjectInput) {
		in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
		in.SSEKMSKeyId = aws.String("arn:aws:kms:us-east-1:1:key/abc")
	})
	put("s3sse", func(in *s3.PutObjectInput) { in.ServerSideEncryption = types.ServerSideEncryptionAes256 })
	put("ssec", func(in *s3.PutObjectInput) {
		in.SSECustomerAlgorithm = aws.String("AES256")
		in.SSECustomerKey = aws.String(strings.Repeat("k", 32))
		in.SSECustomerKeyMD5 = aws.String("") // SDK fills this in
	})

	// KMS with a working read: editable, KMS fields reported.
	info, err := e.s.Head(ctx, e.loc("b", ""), "kms", "")
	if err != nil || info.SSE != "aws:kms" || info.KMSKeyID != "arn:aws:kms:us-east-1:1:key/abc" || info.ReadOnlyReason != "" {
		t.Fatalf("kms ok: %+v %v", info, err)
	}
	// SSE-S3 needs no check.
	if info, err := e.s.Head(ctx, e.loc("b", ""), "s3sse", ""); err != nil || info.SSE != "AES256" || info.ReadOnlyReason != "" {
		t.Fatalf("sse-s3: %+v %v", info, err)
	}
	// KMS but reading is denied: read-only with an explanation.
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/kms") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "kms:Decrypt denied"}
		}
		return nil
	})
	info, err = e.s.Head(ctx, e.loc("b", ""), "kms", "")
	if err != nil || info.ReadOnlyReason == "" || !strings.Contains(info.ReadOnlyReason, "KMS") {
		t.Fatalf("kms denied: %+v %v", info, err)
	}
	e.srv.Inject(nil)
	// SSE-C: whatever the server reports about the algorithm, the object is read-only.
	info, err = e.s.Head(ctx, e.loc("b", ""), "ssec", "")
	if err == nil && !strings.Contains(info.ReadOnlyReason, "SSE-C") {
		t.Fatalf("SSE-C object must be read-only: %+v", info)
	}
}

func TestPreview(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", true)
	e.srv.PutObject("b", "big", []byte("0123456789"), "", nil)
	e.srv.PutObject("b", "empty", nil, "", nil)
	v1 := e.srv.PutObject("b", "v", []byte("first"), "", nil)
	e.srv.PutObject("b", "v", []byte("second"), "", nil)
	ctx := testCtx(t)
	loc := e.loc("b", "")

	data, trunc, err := e.s.Preview(ctx, loc, "big", "", 4)
	if err != nil || string(data) != "0123" || !trunc {
		t.Fatalf("%q %v %v", data, trunc, err)
	}
	data, trunc, err = e.s.Preview(ctx, loc, "big", "", 10)
	if err != nil || string(data) != "0123456789" || trunc {
		t.Fatalf("exact fit: %q %v %v", data, trunc, err)
	}
	data, trunc, err = e.s.Preview(ctx, loc, "big", "", 1<<20)
	if err != nil || string(data) != "0123456789" || trunc {
		t.Fatalf("below limit: %q %v %v", data, trunc, err)
	}
	data, trunc, err = e.s.Preview(ctx, loc, "empty", "", 100)
	if err != nil || len(data) != 0 || trunc {
		t.Fatalf("empty object: %q %v %v", data, trunc, err)
	}
	if data, _, err := e.s.Preview(ctx, loc, "v", v1, 100); err != nil || string(data) != "first" {
		t.Fatalf("version: %q %v", data, err)
	}
	if _, _, err := e.s.Preview(ctx, loc, "missing", "", 10); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := e.s.Preview(ctx, loc, "big", "", 0); !IsKind(err, ErrInvalid) {
		t.Fatalf("zero limit: %v", err)
	}
}

func TestPreviewServerIgnoringRange(t *testing.T) {
	body := strings.Repeat("x", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		fmt.Fprint(w, body) // always 200, whole object
	}))
	defer srv.Close()
	s := newTestService(t, nil)
	a, _ := s.SaveAccount(staticAccount("norange", srv.URL), &Secrets{SecretAccessKey: "s"})
	data, trunc, err := s.Preview(testCtx(t), Location{AccountID: a.ID, Bucket: "b"}, "k", "", 10)
	if err != nil || len(data) != 10 || !trunc {
		t.Fatalf("%d bytes trunc=%v err=%v", len(data), trunc, err)
	}
	data, trunc, err = s.Preview(testCtx(t), Location{AccountID: a.ID, Bucket: "b"}, "k", "", 100)
	if err != nil || len(data) != 100 || trunc {
		t.Fatalf("%d bytes trunc=%v err=%v", len(data), trunc, err)
	}
}

func TestContentRangeTotal(t *testing.T) {
	for in, want := range map[string]int64{"bytes 0-99/1234": 1234, "bytes 0-0/1": 1} {
		if n, ok := contentRangeTotal(in); !ok || n != want {
			t.Errorf("%q: %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "bytes 0-99/*", "garbage"} {
		if _, ok := contentRangeTotal(in); ok {
			t.Errorf("%q should not parse", in)
		}
	}
}

func TestPresignURL(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	loc := e.loc("b", "")
	u, err := e.s.PresignURL(ctx, loc, "dir/a b.txt", "", "GET", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	if !strings.HasPrefix(u, e.srv.URL+"/b/dir/a%20b.txt?") || q.Get("X-Amz-Expires") != "3600" || q.Get("X-Amz-Signature") == "" ||
		!strings.HasPrefix(q.Get("X-Amz-Credential"), "AKIATEST/") {
		t.Fatalf("url %s", u)
	}
	if u, err := e.s.PresignURL(ctx, loc, "k", "v1", "get", time.Minute); err != nil || !strings.Contains(u, "versionId=v1") {
		t.Fatalf("version url %s %v", u, err)
	}
	if u, err := e.s.PresignURL(ctx, loc, "k", "", "", time.Minute); err != nil || !strings.Contains(u, "X-Amz-Signature") {
		t.Fatalf("default method: %s %v", u, err)
	}
	if u, err := e.s.PresignURL(ctx, loc, "k", "", "PUT", time.Minute); err != nil || !strings.Contains(u, "X-Amz-Signature") {
		t.Fatalf("put: %s %v", u, err)
	}
	for name, f := range map[string]func() error{
		"zero ttl":    func() error { _, err := e.s.PresignURL(ctx, loc, "k", "", "GET", 0); return err },
		"8 days":      func() error { _, err := e.s.PresignURL(ctx, loc, "k", "", "GET", 8*24*time.Hour); return err },
		"bad method":  func() error { _, err := e.s.PresignURL(ctx, loc, "k", "", "DELETE", time.Hour); return err },
		"put+version": func() error { _, err := e.s.PresignURL(ctx, loc, "k", "v", "PUT", time.Hour); return err },
		"no key":      func() error { _, err := e.s.PresignURL(ctx, loc, "", "", "GET", time.Hour); return err },
		"no bucket":   func() error { _, err := e.s.PresignURL(ctx, e.loc("", ""), "k", "", "GET", time.Hour); return err },
		"unknown acct": func() error {
			_, err := e.s.PresignURL(ctx, Location{AccountID: "x", Bucket: "b"}, "k", "", "GET", time.Hour)
			return err
		},
	} {
		err := f()
		want := ErrInvalid
		if name == "unknown acct" {
			want = ErrNotFound
		}
		if !IsKind(err, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.srv.Requests()) != 0 {
		t.Fatal("presigning must not send requests")
	}
}

func TestPublicURL(t *testing.T) {
	s := newTestService(t, nil)
	save := func(a Account) string {
		a.Name = "n"
		if a.Credentials == "" {
			a.Credentials = CredDefault
		}
		got, err := s.SaveAccount(a, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	awsEU := save(Account{Provider: ProviderAWS, Region: "eu-central-1"})
	awsNoRegion := save(Account{Provider: ProviderAWS})
	awsCN := save(Account{Provider: ProviderAWS, Region: "cn-north-1"})
	pathStyle := save(Account{Provider: ProviderS3, Endpoint: "https://s3.example.com:9000", PathStyle: true})
	virtual := save(Account{Provider: ProviderS3, Endpoint: "https://s3.example.com:9000"})
	withBase := save(Account{Provider: ProviderS3, Endpoint: "http://gw.example.com/s3/"})
	ip := save(Account{Provider: ProviderS3, Endpoint: "http://127.0.0.1:9000"})
	cases := []struct {
		acct, bucket, key, want string
	}{
		{awsEU, "bkt", "a/b c+d.txt", "https://bkt.s3.eu-central-1.amazonaws.com/a/b%20c%2Bd.txt"},
		{awsEU, "my.dotted.bucket", "k", "https://s3.eu-central-1.amazonaws.com/my.dotted.bucket/k"},
		{awsNoRegion, "bkt", "k", "https://bkt.s3.us-east-1.amazonaws.com/k"},
		{awsCN, "bkt", "k", "https://bkt.s3.cn-north-1.amazonaws.com.cn/k"},
		{pathStyle, "bkt", "dir/ü.txt", "https://s3.example.com:9000/bkt/dir/%C3%BC.txt"},
		{virtual, "bkt", "k", "https://bkt.s3.example.com:9000/k"},
		{withBase, "bkt", "k", "http://bkt.gw.example.com/s3/k"},
		{ip, "bkt", "k", "http://127.0.0.1:9000/bkt/k"},
		{awsEU, "bkt", "?#&%", "https://bkt.s3.eu-central-1.amazonaws.com/%3F%23%26%25"},
		{"unknown", "bkt", "k", ""},
		{awsEU, "", "k", ""},
	}
	for _, c := range cases {
		if got := s.PublicURL(Location{AccountID: c.acct, Bucket: c.bucket}, c.key); got != c.want {
			t.Errorf("%s/%s: got %q want %q", c.bucket, c.key, got, c.want)
		}
	}
}

func TestCreateFolder(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	ctx := testCtx(t)
	if err := e.s.CreateFolder(ctx, e.loc("b", ""), "docs"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.CreateFolder(ctx, e.loc("b", "docs/"), "2026/"); err != nil { // one trailing slash is accepted
		t.Fatal(err)
	}
	for _, k := range []string{"docs/", "docs/2026/"} {
		data, ok := e.srv.Object("b", k)
		if !ok || len(data) != 0 {
			t.Fatalf("%s: ok=%v len=%d", k, ok, len(data))
		}
	}
	p, _ := e.s.List(ctx, e.loc("b", ""), ListOptions{})
	if len(p.Entries) != 1 || !p.Entries[0].Folder || p.Entries[0].Name != "docs" {
		t.Fatalf("listing after create: %+v", p.Entries)
	}
	for _, bad := range []string{"", "/", ".", "..", "a/b", "/a", "a\x00b", "a\nb"} {
		if err := e.s.CreateFolder(ctx, e.loc("b", ""), bad); !IsKind(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := e.s.CreateFolder(ctx, e.loc("nope", ""), "x"); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing bucket: %v", err)
	}
	if err := e.s.CreateFolder(ctx, e.loc("b", "bad"), "x"); !IsKind(err, ErrInvalid) {
		t.Fatalf("bad prefix: %v", err)
	}
}
