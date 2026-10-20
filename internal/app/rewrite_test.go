package app

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// bkPutFull stores an object that carries every attribute a rewrite must keep.
func bkPutFull(t *testing.T, e *s3Env, bucket, key string) {
	t.Helper()
	e.srv.CreateBucket(bucket, false)
	_, err := e.sdk().PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("body of " + key),
		ContentType: aws.String("text/csv"), CacheControl: aws.String("max-age=60"), ContentLanguage: aws.String("en"),
		ContentDisposition: aws.String("attachment; filename=\"a.csv\""), ContentEncoding: aws.String("gzip"),
		Expires:  aws.Time(time.Date(2031, 1, 2, 3, 4, 5, 0, time.UTC)),
		Metadata: map[string]string{"owner": "me", "team": "x"}, Tagging: aws.String("env=prod&tier=a"),
		StorageClass: types.StorageClassStandardIa, ServerSideEncryption: types.ServerSideEncryptionAes256,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func bkHeadInfo(t *testing.T, e *s3Env, bucket, key string) ObjectInfo {
	t.Helper()
	info, err := e.s.Head(testCtx(t), e.loc(bucket, ""), key, "")
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func bkHeader(info ObjectInfo, name string) string {
	for _, h := range info.Headers {
		if h.Name == name {
			return h.Value
		}
	}
	return "<missing>"
}

// bkWantUntouched asserts that every attribute except the ones in skip equals
// what bkPutFull stored.
func bkWantUntouched(t *testing.T, info ObjectInfo, skip ...string) {
	t.Helper()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	check := func(name, got, want string) {
		if !skipped[name] && got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	check("Content-Type", bkHeader(info, "Content-Type"), "text/csv")
	check("Cache-Control", bkHeader(info, "Cache-Control"), "max-age=60")
	check("Content-Language", bkHeader(info, "Content-Language"), "en")
	check("Content-Disposition", bkHeader(info, "Content-Disposition"), "attachment; filename=\"a.csv\"")
	check("Content-Encoding", bkHeader(info, "Content-Encoding"), "gzip")
	if exp := bkHeader(info, "Expires"); !skipped["Expires"] && !strings.Contains(exp, "2031") {
		t.Errorf("Expires = %q", exp)
	}
	if !skipped["meta"] && (info.Metadata["owner"] != "me" || info.Metadata["team"] != "x" || len(info.Metadata) != 2) {
		t.Errorf("metadata = %v", info.Metadata)
	}
	if info.TagsErr != nil || info.Tags["env"] != "prod" || info.Tags["tier"] != "a" || len(info.Tags) != 2 {
		t.Errorf("tags = %v (%v)", info.Tags, info.TagsErr)
	}
	check("class", info.StorageClass, "STANDARD_IA")
	check("sse", info.SSE, "AES256")
}

func TestStorageClassRewritePreservesEverything(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "d/one.csv")
	bkPutFull(t, e, "bkt", "d/sub/two.csv")
	id, err := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Keys: []string{"d/"}}, "glacier_ir")
	if err != nil {
		t.Fatal(err)
	}
	tk := bkOK(t, e, id)
	if tk.Items != 2 {
		t.Fatalf("items = %d", tk.Items)
	}
	for _, k := range []string{"d/one.csv", "d/sub/two.csv"} {
		info := bkHeadInfo(t, e, "bkt", k)
		if info.StorageClass != "GLACIER_IR" {
			t.Fatalf("%s class = %q", k, info.StorageClass)
		}
		bkWantUntouched(t, info, "class")
		bkWantData(t, e, "bkt", k, "body of "+k)
		if info.Size != int64(len("body of "+k)) {
			t.Fatalf("size changed: %d", info.Size)
		}
	}
}

func TestStorageClassSkipsSameAndValidates(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "k")
	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}
	id, _ := e.s.SetStorageClass(sel, "STANDARD_IA")
	bkOK(t, e, id)
	if it := bkItems(e, id)["k"]; it.Status != ItemSkipped {
		t.Fatalf("item %+v", it)
	}
	for _, bad := range []string{"", "standard ia", "x", "A;B", "STANDARD/IA"} {
		if _, err := e.s.SetStorageClass(sel, bad); !IsKind(err, ErrInvalid) {
			t.Errorf("class %q: %v", bad, err)
		}
	}
	if _, err := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Versions: []ObjectRef{{"k", "v"}}}, "STANDARD"); !IsKind(err, ErrInvalid) {
		t.Errorf("versions selection: %v", err)
	}
}

func TestStorageClassVersionedBucketCreatesNewVersion(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	old := e.srv.PutObject("bkt", "k", []byte("payload"), "text/plain", nil)
	id, _ := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}, "ONEZONE_IA")
	bkOK(t, e, id)
	vs := e.srv.Versions("bkt", "k")
	if len(vs) != 2 || vs[1].VersionID != old || vs[0].StorageClass != "ONEZONE_IA" || vs[1].StorageClass != "STANDARD" {
		t.Fatalf("versions = %+v", vs)
	}
}

func TestPropertiesPatchChangesOnlyWhatIsAsked(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "p/f.csv")
	patch := PropertiesPatch{
		SetHeaders:  map[string]string{"cache-control": "no-cache", "Content-Type": "application/json"},
		DelHeaders:  []string{"Content-Language"},
		SetMetadata: map[string]string{"stage": "blue", "owner": "you"},
		DelMetadata: []string{"team"},
	}
	id, err := e.s.SetProperties(Selection{Location: e.loc("bkt", "p/"), Keys: []string{"p/f.csv"}}, patch)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	info := bkHeadInfo(t, e, "bkt", "p/f.csv")
	if bkHeader(info, "Cache-Control") != "no-cache" || info.ContentType != "application/json" || bkHeader(info, "Content-Language") != "<missing>" && bkHeader(info, "Content-Language") != "" {
		t.Fatalf("headers = %+v", info.Headers)
	}
	if info.Metadata["stage"] != "blue" || info.Metadata["owner"] != "you" || len(info.Metadata) != 2 {
		t.Fatalf("metadata = %v", info.Metadata)
	}
	// untouched: encoding, disposition, expires, tags, class, SSE
	if bkHeader(info, "Content-Encoding") != "gzip" || !strings.Contains(bkHeader(info, "Content-Disposition"), "a.csv") || !strings.Contains(bkHeader(info, "Expires"), "2031") {
		t.Fatalf("headers = %+v", info.Headers)
	}
	if info.Tags["env"] != "prod" || len(info.Tags) != 2 || info.StorageClass != "STANDARD_IA" || info.SSE != "AES256" {
		t.Fatalf("info = %+v", info)
	}
	bkWantData(t, e, "bkt", "p/f.csv", "body of p/f.csv")

	// the same patch again changes nothing: skipped, no new write
	id, _ = e.s.SetProperties(Selection{Location: e.loc("bkt", "p/"), Keys: []string{"p/f.csv"}}, patch)
	bkOK(t, e, id)
	if it := bkItems(e, id)["p/f.csv"]; it.Status != ItemSkipped {
		t.Fatalf("second run: %+v", it)
	}
}

func TestPropertiesPatchValidation(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k")
	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}
	big := strings.Repeat("v", 2049)
	cases := map[string]PropertiesPatch{
		"empty patch":          {},
		"unknown header":       {SetHeaders: map[string]string{"X-Custom": "1"}},
		"system header":        {SetHeaders: map[string]string{"Content-Length": "1"}},
		"del unknown header":   {DelHeaders: []string{"ETag"}},
		"non-ascii header":     {SetHeaders: map[string]string{"Content-Disposition": "attachment; filename=\"é.txt\""}},
		"control char header":  {SetHeaders: map[string]string{"Cache-Control": "a\r\nb: c"}},
		"empty header value":   {SetHeaders: map[string]string{"Cache-Control": ""}},
		"padded header value":  {SetHeaders: map[string]string{"Cache-Control": " x"}},
		"bad expires":          {SetHeaders: map[string]string{"Expires": "tomorrow"}},
		"bad content type":     {SetHeaders: map[string]string{"Content-Type": "no slash;;"}},
		"set and del header":   {SetHeaders: map[string]string{"Cache-Control": "x"}, DelHeaders: []string{"cache-control"}},
		"upper case meta key":  {SetMetadata: map[string]string{"Owner": "x"}},
		"bad meta key chars":   {SetMetadata: map[string]string{"a b": "x"}},
		"empty meta key":       {SetMetadata: map[string]string{"": "x"}},
		"reserved meta prefix": {SetMetadata: map[string]string{"x-amz-foo": "x"}},
		"internal meta":        {SetMetadata: map[string]string{"pail-mtime": "1"}},
		"internal meta delete": {DelMetadata: []string{"pail-sha256"}},
		"non-ascii meta value": {SetMetadata: map[string]string{"k": "café"}},
		"newline meta value":   {SetMetadata: map[string]string{"k": "a\nb"}},
		"meta over 2 KiB":      {SetMetadata: map[string]string{"k": big}},
		"set and del meta":     {SetMetadata: map[string]string{"k": "v"}, DelMetadata: []string{"k"}},
	}
	for name, p := range cases {
		if id, err := e.s.SetProperties(sel, p); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: id %q err %v, want Invalid", name, id, err)
		}
	}
	ok := PropertiesPatch{
		SetHeaders:  map[string]string{"Expires": "Mon, 02 Jan 2006 15:04:05 GMT", "Content-Type": "text/html; charset=utf-8"},
		SetMetadata: map[string]string{"a.b-c_d": strings.Repeat("v", 2040)},
	}
	if id, err := e.s.SetProperties(sel, ok); err != nil {
		t.Errorf("valid patch rejected: %v", err)
	} else {
		bkOK(t, e, id)
	}
}

func TestPropertiesMetadataLimitIsCheckedPerObject(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", false)
	e.srv.PutObject("bkt", "full", []byte("x"), "text/plain", map[string]string{"existing": strings.Repeat("e", 1500)})
	e.srv.PutObject("bkt", "empty", []byte("x"), "text/plain", nil)
	id, err := e.s.SetProperties(Selection{Location: e.loc("bkt", ""), Keys: []string{"full", "empty"}},
		PropertiesPatch{SetMetadata: map[string]string{"added": strings.Repeat("a", 1000)}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	items := bkItems(e, id)
	if tk.Status != StatusPartiallyFailed || items["full"].Status != ItemFailed || !strings.Contains(items["full"].Err, "limit") || items["empty"].Status != ItemSucceeded {
		t.Fatalf("task %v items %+v", tk.Status, items)
	}
	if len(bkHeadInfo(t, e, "bkt", "full").Metadata) != 1 {
		t.Fatal("the refused object must be unchanged")
	}
}

func TestEncryptionRewrite(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "enc.csv")
	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"enc.csv"}}

	id, err := e.s.SetEncryption(sel, Encryption{Mode: "aws:kms", KMSKeyID: "arn:aws:kms:us-east-1:111:key/abc"})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	info := bkHeadInfo(t, e, "bkt", "enc.csv")
	if info.SSE != "aws:kms" || info.KMSKeyID != "arn:aws:kms:us-east-1:111:key/abc" {
		t.Fatalf("kms: %+v", info)
	}
	bkWantUntouched(t, info, "sse")

	// same target again: nothing to do
	id, _ = e.s.SetEncryption(sel, Encryption{Mode: "aws:kms", KMSKeyID: "arn:aws:kms:us-east-1:111:key/abc"})
	bkOK(t, e, id)
	if it := bkItems(e, id)["enc.csv"]; it.Status != ItemSkipped {
		t.Fatalf("same kms: %+v", it)
	}

	id, _ = e.s.SetEncryption(sel, Encryption{Mode: "AES256"})
	bkOK(t, e, id)
	info = bkHeadInfo(t, e, "bkt", "enc.csv")
	if info.SSE != "AES256" || info.KMSKeyID != "" {
		t.Fatalf("aes: %+v", info)
	}
	bkWantUntouched(t, info)

	id, _ = e.s.SetEncryption(sel, Encryption{Mode: "none"})
	bkOK(t, e, id)
	info = bkHeadInfo(t, e, "bkt", "enc.csv")
	if info.SSE != "" || info.KMSKeyID != "" {
		t.Fatalf("none: %+v", info)
	}
	bkWantUntouched(t, info, "sse")
}

func TestEncryptionValidation(t *testing.T) {
	e := newS3Env(t) // s3test is plain http: SSE-C must be refused
	bkSeed(e, "bkt", false, "k")
	if err := e.s.SetSecret("sse-key", base64.StdEncoding.EncodeToString(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}
	for name, enc := range map[string]Encryption{
		"unknown mode":      {Mode: "rot13"},
		"empty mode":        {},
		"sse-c over http":   {Mode: "SSE-C", KeyRef: "sse-key"},
		"sse-c without key": {Mode: "SSE-C"},
		"sse-c with kms":    {Mode: "SSE-C", KeyRef: "sse-key", KMSKeyID: "k"},
		"aes with kms key":  {Mode: "AES256", KMSKeyID: "k"},
		"kms with key ref":  {Mode: "aws:kms", KeyRef: "sse-key"},
		"none with key ref": {Mode: "none", KeyRef: "sse-key"},
	} {
		if id, err := e.s.SetEncryption(sel, enc); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: id %q err %v, want Invalid", name, id, err)
		}
	}
	if err := bkRequireHTTPS("https://s3.example.com"); err != nil {
		t.Errorf("https endpoint: %v", err)
	}
	if err := bkRequireHTTPS(""); err != nil {
		t.Errorf("AWS endpoint: %v", err)
	}
	if err := bkRequireHTTPS("http://localhost:9000"); err == nil {
		t.Error("http endpoint accepted")
	}
}

func TestRewriteRefusals(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", false)
	// SSE-C object
	key := make([]byte, 32)
	sum := md5.Sum(key)
	_, err := e.sdk().PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("bkt"), Key: aws.String("ssec"), Body: strings.NewReader("secret"),
		SSECustomerAlgorithm: aws.String("AES256"), SSECustomerKey: aws.String(base64.StdEncoding.EncodeToString(key)),
		SSECustomerKeyMD5: aws.String(base64.StdEncoding.EncodeToString(sum[:])),
	})
	if err != nil {
		t.Fatal(err)
	}
	// archived object
	e.srv.PutObject("bkt", "cold", []byte("c"), "text/plain", nil)
	e.srv.SetStorageClass("bkt", "cold", "GLACIER")
	e.srv.PutObject("bkt", "deep", []byte("c"), "text/plain", nil)
	e.srv.SetStorageClass("bkt", "deep", "DEEP_ARCHIVE")
	e.srv.PutObject("bkt", "fine", []byte("c"), "text/plain", nil)

	sel := Selection{Location: e.loc("bkt", ""), Keys: []string{"ssec", "cold", "deep", "fine", "absent"}}
	id, err := e.s.SetProperties(sel, PropertiesPatch{SetMetadata: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	items := bkItems(e, id)
	if tk.Status != StatusPartiallyFailed || items["fine"].Status != ItemSucceeded {
		t.Fatalf("task %v items %+v", tk.Status, items)
	}
	if !strings.Contains(items["ssec"].Err, "SSE-C") || items["ssec"].Status != ItemFailed {
		t.Errorf("ssec: %+v", items["ssec"])
	}
	for _, k := range []string{"cold", "deep"} {
		if items[k].Status != ItemFailed || !strings.Contains(items[k].Err, "restore it first") {
			t.Errorf("%s: %+v", k, items[k])
		}
	}
	if items["absent"].Status != ItemFailed {
		t.Errorf("absent: %+v", items["absent"])
	}
	// refused objects are untouched
	if info := bkHeadInfo(t, e, "bkt", "cold"); info.StorageClass != "GLACIER" || len(info.Metadata) != 0 {
		t.Errorf("cold changed: %+v", info)
	}
}

func TestRewriteKeepsACLAndToleratesDisabledACLs(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "pub", "plain")
	loc := e.loc("bkt", "")
	if err := e.s.SetACL(testCtx(t), loc, ObjectRef{Key: "pub"}, ACL{Canned: "public-read"}); err != nil {
		t.Fatal(err)
	}
	sel := Selection{Location: loc, Keys: []string{"pub", "plain"}}
	id, _ := e.s.SetStorageClass(sel, "STANDARD_IA")
	bkOK(t, e, id)
	acl, err := e.s.ACL(testCtx(t), loc, ObjectRef{Key: "pub"})
	if err != nil {
		t.Fatal(err)
	}
	public := false
	for _, g := range acl.Grants {
		if g.Type == "Group" && strings.HasSuffix(g.Grantee, "/AllUsers") && g.Permission == "READ" {
			public = true
		}
	}
	if !public {
		t.Fatalf("public-read ACL lost by the rewrite: %+v", acl)
	}
	plain, _ := e.s.ACL(testCtx(t), loc, ObjectRef{Key: "plain"})
	if len(plain.Grants) != 1 || plain.Grants[0].Permission != "FULL_CONTROL" {
		t.Fatalf("a private object must stay private: %+v", plain)
	}

	// ACLs unavailable: the rewrite still works and reports no error
	e.srv.SetUnsupported("acl")
	id, _ = e.s.SetStorageClass(sel, "ONEZONE_IA")
	tk := bkOK(t, e, id)
	for _, it := range e.s.TaskItems(id) {
		if it.Err != "" {
			t.Errorf("item %+v", it)
		}
	}
	if tk.ItemsFailed != 0 || bkHeadInfo(t, e, "bkt", "pub").StorageClass != "ONEZONE_IA" {
		t.Fatalf("task %+v", tk)
	}
}

func TestRewriteCancelReportsUnprocessed(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", false)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		e.srv.PutObject("bkt", k, []byte(k), "text/plain", nil)
	}
	bkWorkersSetting(t, e, 1)
	// Cancel on the third copy request.
	var holder atomic.Value
	bkCancelDuring(e, &holder, 3, func(r *http.Request) bool {
		return r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != ""
	})
	tid, err := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), All: true}, "STANDARD_IA")
	if err != nil {
		t.Fatal(err)
	}
	holder.Store(tid)
	tk := bkWait(t, e, tid)
	e.srv.Inject(nil)
	if tk.Status != StatusCanceled {
		t.Fatalf("status %v", tk.Status)
	}
	rid, err := e.s.Retry(tid)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, rid)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		if c := bkHeadInfo(t, e, "bkt", k).StorageClass; c != "STANDARD_IA" {
			t.Errorf("%s class %q", k, c)
		}
	}
}

// A rewrite is a self-copy that resets the ACL; when the ACL cannot be read
// (and ACLs are not simply unsupported) the item must fail before the copy.
func TestFxRewriteFailsWhenACLReadDenied(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "k")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Query().Has("acl") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no acl"}
		}
		return nil
	})
	e.srv.ResetRequests()
	id, err := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}, "GLACIER_IR")
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	it := bkItems(e, id)["k"]
	if tk.Status != StatusPartiallyFailed || it.Status != ItemFailed || !strings.Contains(it.Err, "cannot read ACL") {
		t.Fatalf("task %v item %+v", tk.Status, it)
	}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "" {
			t.Fatalf("the object was rewritten although its ACL was unreadable: %+v", r)
		}
	}
	e.srv.Inject(nil)
	if info := bkHeadInfo(t, e, "bkt", "k"); info.StorageClass != "STANDARD_IA" {
		t.Fatalf("class = %q, object was modified", info.StorageClass)
	}
}

func TestFxRewriteProceedsWhenACLsUnsupported(t *testing.T) {
	e := newS3Env(t)
	bkPutFull(t, e, "bkt", "k")
	e.srv.SetUnsupported("acl")
	id, err := e.s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Keys: []string{"k"}}, "GLACIER_IR")
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	e.srv.ClearUnsupported("")
	if info := bkHeadInfo(t, e, "bkt", "k"); info.StorageClass != "GLACIER_IR" {
		t.Fatalf("class = %q", info.StorageClass)
	}
}

// Some servers (VersityGW) answer a self-copy with 200 but keep the old
// storage class and encryption. The item must fail, not report success.
func TestRewriteIgnoredByServerFails(t *testing.T) {
	srv := s3test.New()
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(r *http.Request) {
		direct(r)
		if r.Header.Get("x-amz-copy-source") != "" {
			r.Header.Del("x-amz-storage-class")
			r.Header.Del("x-amz-server-side-encryption")
		}
	}
	ps := httptest.NewServer(proxy)
	t.Cleanup(ps.Close)
	s := newTestService(t, nil)
	acc, err := s.SaveAccount(staticAccount("ignores", ps.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	e := &s3Env{t: t, s: s, srv: srv, acc: acc}
	bkSeed(e, "bkt", false, "a.txt")
	for name, submit := range map[string]func() (string, error){
		"class": func() (string, error) {
			return s.SetStorageClass(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, "STANDARD_IA")
		},
		"sse": func() (string, error) {
			return s.SetEncryption(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, Encryption{Mode: "AES256"})
		},
	} {
		id, err := submit()
		if err != nil {
			t.Fatal(name, err)
		}
		bkWait(t, e, id)
		it := bkItems(e, id)["a.txt"]
		if it.Status != ItemFailed || !strings.Contains(it.Err, "did not apply") {
			t.Errorf("%s: item %+v, want failed with %q", name, it, bkNotApplied)
		}
	}
}
