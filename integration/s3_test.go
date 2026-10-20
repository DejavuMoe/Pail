package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/safepath"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// This suite only accepts an explicitly configured loopback HTTP endpoint and
// test credentials. It never falls back to an AWS profile, ambient credentials
// or a public endpoint.
func localConfig(t *testing.T) storage.Config {
	t.Helper()
	endpoint := os.Getenv("PAIL_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAIL_TEST_ENDPOINT and explicit test credentials to run isolated S3 integration tests")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		t.Fatal("PAIL_TEST_ENDPOINT must be a plain local HTTP endpoint")
	}
	if host := u.Hostname(); host != "localhost" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			t.Fatal("integration tests refuse non-loopback endpoints")
		}
	}
	accessKey, secretKey := os.Getenv("PAIL_TEST_ACCESS_KEY"), os.Getenv("PAIL_TEST_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		t.Fatal("explicit PAIL_TEST_ACCESS_KEY and PAIL_TEST_SECRET_KEY are required")
	}
	return storage.Config{Endpoint: endpoint, Region: "us-east-1", AccessKeyID: accessKey, SecretAccessKey: secretKey, UsePathStyle: true}
}

type env struct {
	ctx    context.Context
	c      *storage.Client
	cfg    storage.Config
	bucket string
}

func newEnv(t *testing.T, versioned bool) *env {
	t.Helper()
	cfg := localConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	c, err := storage.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	bucket := fmt.Sprintf("pail-it-%d", time.Now().UnixNano())
	if _, err := c.S3().CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanBucket(t, c, bucket) })
	if versioned {
		if _, err := c.S3().PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
			t.Fatal(err)
		}
	}
	return &env{ctx, c, cfg, bucket}
}

func (e *env) put(t *testing.T, key string, payload []byte) string {
	t.Helper()
	out, err := e.c.S3().PutObject(e.ctx, &s3.PutObjectInput{Bucket: &e.bucket, Key: &key, Body: bytes.NewReader(payload)})
	if err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
	return aws.ToString(out.VersionId)
}

func (e *env) get(t *testing.T, key, version string) []byte {
	t.Helper()
	in := &s3.GetObjectInput{Bucket: &e.bucket, Key: &key}
	if version != "" {
		in.VersionId = &version
	}
	out, err := e.c.S3().GetObject(e.ctx, in)
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) listAll(t *testing.T, prefix string) []string {
	t.Helper()
	p := s3.NewListObjectsV2Paginator(e.c.S3(), &s3.ListObjectsV2Input{Bucket: &e.bucket, Prefix: &prefix})
	var keys []string
	for p.HasMorePages() {
		page, err := p.NextPage(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys
}

func TestSpecialKeysAndCopy(t *testing.T) {
	e := newEnv(t, false)
	keys := []string{"特殊 目录/a+b%25?#.txt", "特殊 目录/二号.txt", "a b/c+d/e%f.txt", "plus+/sp ace", "nested/a/b/c/d/e.bin", "100%/done", "emoji/😀.txt", "quote'\"/x", "tilde~/ampersand&.txt"}
	payload := []byte("unicode, spaces, plus, percent, hash and question mark\n")
	for _, k := range keys {
		e.put(t, k, payload)
		if !bytes.Equal(e.get(t, k, ""), payload) {
			t.Fatalf("content mismatch for %q", k)
		}
	}
	got := e.listAll(t, "")
	sort.Strings(got)
	want := append([]string(nil), keys...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("listing differs:\n got %q\nwant %q", got, want)
	}
	for i, src := range keys {
		dst := fmt.Sprintf("copied 复制/%d +%%?#.txt", i)
		if err := e.c.Copy(e.ctx, &s3.CopyObjectInput{Bucket: &e.bucket, Key: &dst, CopySource: aws.String(storage.CopySource(e.bucket, src, ""))}, int64(len(payload)), storage.TransferOptions{}); err != nil {
			t.Fatalf("copy %q: %v", src, err)
		}
		if !bytes.Equal(e.get(t, dst, ""), payload) {
			t.Fatalf("copy of %q differs", src)
		}
	}
	// Delimiter listing keeps folders intact.
	out, err := e.c.S3().ListObjectsV2(e.ctx, &s3.ListObjectsV2Input{Bucket: &e.bucket, Delimiter: aws.String("/"), Prefix: aws.String("特殊 目录/")})
	if err != nil || len(out.Contents) != 2 {
		t.Fatalf("folder listing: %v %v", out, err)
	}
}

func TestPagination1005Objects(t *testing.T) {
	e := newEnv(t, false)
	const n = 1005
	var made atomic.Int32
	sem := make(chan struct{}, 16)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem }()
			key := fmt.Sprintf("bulk/obj-%04d", i)
			_, err := e.c.S3().PutObject(e.ctx, &s3.PutObjectInput{Bucket: &e.bucket, Key: &key, Body: strings.NewReader(key)})
			if err == nil {
				made.Add(1)
			}
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	keys := e.listAll(t, "bulk/")
	if len(keys) != n || !sort.StringsAreSorted(keys) {
		t.Fatalf("listed %d keys (sorted=%v), want %d", len(keys), sort.StringsAreSorted(keys), n)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("duplicate %q across pages", k)
		}
		seen[k] = true
	}
	// DeleteObjects crosses the 1000-item chunk boundary against a real service.
	ids := make([]storage.ObjectID, len(keys))
	for i, k := range keys {
		ids[i] = storage.ObjectID{Key: k}
	}
	for i, r := range e.c.DeleteObjects(e.ctx, e.bucket, ids) {
		if r.Status != storage.DeleteSucceeded || r.Key != keys[i] {
			t.Fatalf("delete %d: %+v", i, r)
		}
	}
	if left := e.listAll(t, "bulk/"); len(left) != 0 {
		t.Fatalf("%d objects left", len(left))
	}
}

func TestMultipartUploadDownloadRoundTrip(t *testing.T) {
	e := newEnv(t, false)
	payload := make([]byte, 12*1024*1024+4321)
	for i := range payload {
		payload[i] = byte(i*31 + i>>8)
	}
	sum := sha256.Sum256(payload)
	var upProgress, downProgress atomic.Int64
	key := "multipart/large 文件.bin"
	if _, err := e.c.Upload(e.ctx, &s3.PutObjectInput{Bucket: &e.bucket, Key: &key, Body: bytes.NewReader(payload)},
		storage.TransferOptions{PartSize: 5 << 20, Concurrency: 3, Progress: func(n int64) { upProgress.Store(n) }}); err != nil {
		t.Fatal(err)
	}
	if upProgress.Load() != int64(len(payload)) {
		t.Fatalf("upload progress %d, want %d", upProgress.Load(), len(payload))
	}
	head, err := e.c.S3().HeadObject(e.ctx, &s3.HeadObjectInput{Bucket: &e.bucket, Key: &key})
	if err != nil || aws.ToInt64(head.ContentLength) != int64(len(payload)) || !strings.HasSuffix(strings.Trim(aws.ToString(head.ETag), `"`), "-3") {
		t.Fatalf("not a 3-part object: %v %v", head, err)
	}
	// Download through the same path the app uses: temp file next to the target, then publish.
	dir := t.TempDir()
	target, err := safepath.Join(dir, "out/"+key)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := safepath.CreateTemp(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	n, err := e.c.Download(e.ctx, &s3.GetObjectInput{Bucket: &e.bucket, Key: &key}, tmp, storage.TransferOptions{PartSize: 5 << 20, Concurrency: 3, Size: int64(len(payload)), Progress: func(n int64) { downProgress.Store(n) }})
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("download n=%d err=%v", n, err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if downProgress.Load() != int64(len(payload)) {
		t.Fatalf("download progress %d", downProgress.Load())
	}
	if err := safepath.Publish(tmp.Name(), target, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || sha256.Sum256(got) != sum {
		t.Fatalf("SHA-256 mismatch after round trip (err %v)", err)
	}
	tmp2, _ := safepath.CreateTemp(filepath.Dir(target))
	tmp2.Close()
	if err := safepath.Publish(tmp2.Name(), target, false); !errors.Is(err, safepath.ErrExists) {
		t.Fatalf("existing target must not be replaced: %v", err)
	}
}

func TestMultipartCopy(t *testing.T) {
	e := newEnv(t, false)
	payload := bytes.Repeat([]byte("0123456789abcdef"), 700000) // 11.2 MB
	src, dst := "copy/src.bin", "copy/dst 复制.bin"
	e.put(t, src, payload)
	// A create-only condition forces the multipart UploadPartCopy path.
	in := &s3.CopyObjectInput{Bucket: &e.bucket, Key: &dst, CopySource: aws.String(storage.CopySource(e.bucket, src, "")), IfNoneMatch: aws.String("*")}
	var progress atomic.Int64
	if err := e.c.Copy(e.ctx, in, int64(len(payload)), storage.TransferOptions{PartSize: 5 << 20, Concurrency: 2, Progress: func(n int64) { progress.Store(n) }}); err != nil {
		t.Fatal(err)
	}
	if progress.Load() != int64(len(payload)) || sha256.Sum256(e.get(t, dst, "")) != sha256.Sum256(payload) {
		t.Fatalf("multipart copy differs (progress %d)", progress.Load())
	}
	head, err := e.c.S3().HeadObject(e.ctx, &s3.HeadObjectInput{Bucket: &e.bucket, Key: &dst})
	if err != nil || !strings.Contains(aws.ToString(head.ETag), "-") {
		t.Fatalf("copy was not multipart: %v %v", head, err)
	}
	// Zero-byte multipart copy.
	e.put(t, "copy/empty", nil)
	in = &s3.CopyObjectInput{Bucket: &e.bucket, Key: aws.String("copy/empty2"), CopySource: aws.String(storage.CopySource(e.bucket, "copy/empty", "")), IfNoneMatch: aws.String("*")}
	if err := e.c.Copy(e.ctx, in, 0, storage.TransferOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(e.get(t, "copy/empty2", "")) != 0 {
		t.Fatal("empty copy not empty")
	}
}

func TestVersioningDeleteMarkersAndDeleteObjects(t *testing.T) {
	e := newEnv(t, true)
	key := "versions/对象 +.txt"
	v1 := e.put(t, key, []byte("first"))
	v2 := e.put(t, key, []byte("second"))
	v3 := e.put(t, key, []byte("third"))
	if v1 == "" || v1 == v2 || v2 == v3 {
		t.Fatal("versioning did not produce distinct IDs")
	}
	if !bytes.Equal(e.get(t, key, v1), []byte("first")) || !bytes.Equal(e.get(t, key, ""), []byte("third")) {
		t.Fatal("version reads wrong")
	}
	// Copy from a historical version.
	if err := e.c.Copy(e.ctx, &s3.CopyObjectInput{Bucket: &e.bucket, Key: aws.String("restored.txt"), CopySource: aws.String(storage.CopySource(e.bucket, key, v1))}, 5, storage.TransferOptions{}); err != nil {
		t.Fatal(err)
	}
	if string(e.get(t, "restored.txt", "")) != "first" {
		t.Fatal("version copy wrong")
	}
	del, err := e.c.S3().DeleteObject(e.ctx, &s3.DeleteObjectInput{Bucket: &e.bucket, Key: &key})
	if err != nil || !aws.ToBool(del.DeleteMarker) {
		t.Fatalf("no delete marker: %v %v", del, err)
	}
	if _, err = e.c.S3().GetObject(e.ctx, &s3.GetObjectInput{Bucket: &e.bucket, Key: &key}); storage.ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("expected NoSuchKey, got %v", err)
	}
	list := func() ([]types.ObjectVersion, []types.DeleteMarkerEntry) {
		out, err := e.c.S3().ListObjectVersions(e.ctx, &s3.ListObjectVersionsInput{Bucket: &e.bucket, Prefix: &key})
		if err != nil {
			t.Fatal(err)
		}
		return out.Versions, out.DeleteMarkers
	}
	vs, ms := list()
	if len(vs) != 3 || len(ms) != 1 {
		t.Fatalf("%d versions %d markers", len(vs), len(ms))
	}
	// DeleteObjects: middle version, the delete marker, a never-existing version and a duplicate.
	ids := []storage.ObjectID{{Key: key, VersionID: v2}, {Key: key, VersionID: aws.ToString(ms[0].VersionId)}, {Key: key, VersionID: "does-not-exist"}, {Key: key, VersionID: v2}}
	res := e.c.DeleteObjects(e.ctx, e.bucket, ids)
	if len(res) != 4 {
		t.Fatalf("got %d results", len(res))
	}
	for i, r := range res {
		if r.Key != ids[i].Key || r.VersionID != ids[i].VersionID {
			t.Fatalf("result %d out of order: %+v", i, r)
		}
		if r.Status == storage.DeleteUnprocessed {
			t.Fatalf("result %d unprocessed: %+v", i, r)
		}
	}
	if res[0].Status != storage.DeleteSucceeded || res[1].Status != storage.DeleteSucceeded || res[0] != res[3] {
		t.Fatalf("expected v2 and the marker deleted: %+v", res)
	}
	t.Logf("nonexistent version outcome: %+v", res[2]) // S3 acknowledges; MinIO may report NoSuchVersion
	vs, ms = list()
	if len(vs) != 2 || len(ms) != 0 {
		t.Fatalf("after delete: %d versions %d markers", len(vs), len(ms))
	}
	if string(e.get(t, key, "")) != "third" { // marker gone => latest version visible again
		t.Fatal("latest version not restored after removing the delete marker")
	}
}

func TestPresignGetAndHead(t *testing.T) {
	e := newEnv(t, true)
	key, payload := "presign/空 格+%?#.txt", []byte("presigned payload")
	v := e.put(t, key, payload)
	e.put(t, key, []byte("newer"))
	hc := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer hc.CloseIdleConnections()
	for _, tc := range []struct{ method, version string }{{"GET", ""}, {"GET", v}, {"HEAD", ""}} {
		p, err := e.c.Presign(e.ctx, tc.method, e.bucket, key, tc.version, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(e.ctx, tc.method, p.URL, nil)
		req.Header = p.Header.Clone()
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		want := "newer"
		if tc.version != "" {
			want = string(payload)
		}
		if resp.StatusCode != 200 || (tc.method == "GET" && string(body) != want) {
			t.Fatalf("%s %q: status %d body %q", tc.method, tc.version, resp.StatusCode, body)
		}
	}
	p, _ := e.c.Presign(e.ctx, "PUT", e.bucket, "presign/put.txt", "", time.Minute)
	req, _ := http.NewRequestWithContext(e.ctx, "PUT", p.URL, strings.NewReader("via presigned put"))
	req.Header = p.Header.Clone()
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("presigned PUT: %v %v", resp, err)
	}
	resp.Body.Close()
	if string(e.get(t, "presign/put.txt", "")) != "via presigned put" {
		t.Fatal("presigned PUT content wrong")
	}
	cancelled, stop := context.WithCancel(e.ctx)
	stop()
	if _, err := e.c.S3().ListObjectsV2(cancelled, &s3.ListObjectsV2Input{Bucket: &e.bucket}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request returned %v", err)
	}
}

func TestBucketsAndRegion(t *testing.T) {
	e := newEnv(t, false)
	buckets, err := e.c.ListBuckets(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range buckets {
		if b.Name == e.bucket {
			found = true
			if b.CreationDate.IsZero() || time.Since(b.CreationDate) > 24*time.Hour {
				t.Fatalf("bad creation date %v", b.CreationDate)
			}
			t.Logf("ListBuckets region for %s: %q", b.Name, b.Region)
		}
	}
	if !found {
		t.Fatal("new bucket not listed")
	}
	if r, err := e.c.BucketRegion(e.ctx, e.bucket); err != nil || r != "us-east-1" {
		t.Fatalf("BucketRegion=%q %v", r, err)
	}
}

// Checksums against a real S3-compatible service in every mode, including the
// multipart path that otherwise adds CRC32 on its own.
func TestChecksumModes(t *testing.T) {
	e := newEnv(t, false)
	payload := bytes.Repeat([]byte("checksum"), 1<<20) // 8 MiB => multipart at 5 MiB parts
	for _, mode := range []string{"", storage.ChecksumAuto, storage.ChecksumWhenRequired, storage.ChecksumWhenSupported} {
		cfg := e.cfg
		cfg.ChecksumMode = mode
		c, err := storage.New(e.ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{0, 100, len(payload)} {
			key := fmt.Sprintf("checksum/m-%s/%d", mode, size)
			if _, err := c.Upload(e.ctx, &s3.PutObjectInput{Bucket: &e.bucket, Key: &key, Body: bytes.NewReader(payload[:size])}, storage.TransferOptions{PartSize: 5 << 20}); err != nil {
				t.Fatalf("mode %q size %d: upload: %v", mode, size, err)
			}
			buf := &memWriterAt{b: make([]byte, size)}
			if _, err := c.Download(e.ctx, &s3.GetObjectInput{Bucket: &e.bucket, Key: &key}, buf, storage.TransferOptions{PartSize: 5 << 20}); err != nil || !bytes.Equal(buf.b, payload[:size]) {
				t.Fatalf("mode %q size %d: download: %v", mode, size, err)
			}
		}
	}
}

type memWriterAt struct{ b []byte }

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) { return copy(m.b[off:], p), nil }

func TestProbeConditionalWrites(t *testing.T) {
	e := newEnv(t, false)
	ok, err := e.c.ProbeConditionalWrites(e.ctx, e.bucket)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MinIO honours If-None-Match:* on PutObject: %v", ok)
	if left := e.listAll(t, ".pail-probe-"); len(left) != 0 {
		t.Fatalf("probe object left behind: %v", left)
	}
}

func cleanBucket(t *testing.T, c *storage.Client, bucket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Re-read the first page after deleting it, so deletion cannot invalidate a continuation marker.
	for pages := 0; pages < 100; pages++ {
		page, err := c.S3().ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: &bucket})
		if err != nil {
			t.Errorf("cleanup list versions: %v", err)
			return
		}
		var ids []storage.ObjectID
		for _, v := range page.Versions {
			ids = append(ids, storage.ObjectID{Key: aws.ToString(v.Key), VersionID: aws.ToString(v.VersionId)})
		}
		for _, m := range page.DeleteMarkers {
			ids = append(ids, storage.ObjectID{Key: aws.ToString(m.Key), VersionID: aws.ToString(m.VersionId)})
		}
		if len(ids) == 0 {
			if _, err := c.S3().DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: &bucket}); err != nil {
				t.Errorf("cleanup delete bucket: %v", err)
			}
			return
		}
		for _, r := range c.DeleteObjects(ctx, bucket, ids) {
			if r.Status != storage.DeleteSucceeded {
				t.Errorf("cleanup delete %+v", r)
				return
			}
		}
	}
	t.Error("cleanup exceeded page budget")
}
