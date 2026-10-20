package s3test_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/DejavuMoe/Pail/internal/storage"
)

func setup(t *testing.T) (*s3test.Server, *storage.Client, *s3.Client) {
	t.Helper()
	srv := s3test.New()
	t.Cleanup(srv.Close)
	cl, err := storage.New(context.Background(), storage.Config{Endpoint: srv.URL, Region: "us-east-1", UsePathStyle: true,
		AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return srv, cl, cl.S3()
}

func s(v string) *string { return aws.String(v) }

func TestBucketsAndLocation(t *testing.T) {
	srv, cl, c := setup(t)
	ctx := context.Background()
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: s("alpha-bucket")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: s("eu-bucket"), CreateBucketConfiguration: &types.CreateBucketConfiguration{LocationConstraint: "eu-west-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: s("alpha-bucket")}); storage.ErrorCode(err) != "BucketAlreadyOwnedByYou" {
		t.Fatalf("recreate: %v", err)
	}
	bs, err := cl.ListBuckets(ctx)
	if err != nil || len(bs) != 2 || bs[0].Name != "alpha-bucket" {
		t.Fatalf("ListBuckets = %+v, %v", bs, err)
	}
	if r, err := cl.BucketRegion(ctx, "eu-bucket"); err != nil || r != "eu-west-1" {
		t.Fatalf("region = %q, %v", r, err)
	}
	if r, _ := cl.BucketRegion(ctx, "alpha-bucket"); r != "us-east-1" {
		t.Fatalf("region = %q", r)
	}
	if _, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: s("alpha-bucket")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: s("nope-bucket")}); err == nil {
		t.Fatal("head missing bucket succeeded")
	}
	srv.PutObject("alpha-bucket", "k", []byte("x"), "", nil)
	if _, err := c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: s("alpha-bucket")}); storage.ErrorCode(err) != "BucketNotEmpty" {
		t.Fatalf("delete non-empty: %v", err)
	}
	c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: s("alpha-bucket"), Key: s("k")})
	if _, err := c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: s("alpha-bucket")}); err != nil {
		t.Fatal(err)
	}
}

func TestListPaginationAndDelimiter(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("lst", false)
	for i := 0; i < 1005; i++ {
		srv.PutObject("lst", fmt.Sprintf("many/%05d", i), []byte("x"), "", nil)
	}
	for _, k := range []string{"a/1", "a/2", "b/x/1", "b/y", "top"} {
		srv.PutObject("lst", k, []byte("x"), "", nil)
	}
	total, pages := 0, 0
	var token *string
	for {
		out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: s("lst"), Prefix: s("many/"), ContinuationToken: token})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		total += int(aws.ToInt32(out.KeyCount))
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	if total != 1005 || pages != 2 {
		t.Fatalf("total=%d pages=%d", total, pages)
	}
	// delimiter with small pages
	var keys, prefixes []string
	token = nil
	for {
		out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: s("lst"), Delimiter: s("/"), MaxKeys: aws.Int32(1), ContinuationToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out.Contents {
			keys = append(keys, *o.Key)
		}
		for _, p := range out.CommonPrefixes {
			prefixes = append(prefixes, *p.Prefix)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	if strings.Join(keys, ",") != "top" || strings.Join(prefixes, ",") != "a/,b/,many/" {
		t.Fatalf("keys=%v prefixes=%v", keys, prefixes)
	}
	out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: s("lst"), Prefix: s("b/"), Delimiter: s("/")})
	if err != nil || len(out.Contents) != 1 || *out.Contents[0].Key != "b/y" || len(out.CommonPrefixes) != 1 || *out.CommonPrefixes[0].Prefix != "b/x/" {
		t.Fatalf("nested: %+v %v", out, err)
	}
	out, _ = c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: s("lst"), StartAfter: s("b/y")})
	if len(out.Contents) == 0 || *out.Contents[0].Key != "many/00000" || len(out.Contents) != 1000 {
		t.Fatalf("start-after: first=%v n=%d", aws.ToString(out.Contents[0].Key), len(out.Contents))
	}
}

func TestVersioningAndDeleteMarkers(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("ver", true)
	v1 := srv.PutObject("ver", "k", []byte("one"), "text/plain", nil)
	v2 := srv.PutObject("ver", "k", []byte("two"), "text/plain", nil)
	if v1 == v2 || v1 == "null" {
		t.Fatalf("version ids %q %q", v1, v2)
	}
	del, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: s("ver"), Key: s("k")})
	if err != nil || !aws.ToBool(del.DeleteMarker) || aws.ToString(del.VersionId) == "" {
		t.Fatalf("delete: %+v %v", del, err)
	}
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("ver"), Key: s("k")}); storage.ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("get marker: %v", err)
	}
	old, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("ver"), Key: s("k"), VersionId: &v1})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(old.Body)
	if string(b) != "one" {
		t.Fatalf("old = %q", b)
	}
	vs := srv.Versions("ver", "k")
	if len(vs) != 3 || !vs[0].DeleteMarker || !vs[0].IsLatest || vs[1].VersionID != v2 || vs[2].VersionID != v1 {
		t.Fatalf("versions: %+v", vs)
	}
	for _, k := range []string{"a", "b", "c"} {
		srv.PutObject("ver", k, []byte(k), "", nil)
	}
	srv.DeleteMarker("ver", "b")
	var got []string
	var km, vm *string
	for {
		out, err := c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: s("ver"), MaxKeys: aws.Int32(2), KeyMarker: km, VersionIdMarker: vm})
		if err != nil {
			t.Fatal(err)
		}
		// merge Version + DeleteMarker in order of key via interleaving expectation: just record counts
		for _, v := range out.Versions {
			got = append(got, "V:"+*v.Key)
		}
		for _, d := range out.DeleteMarkers {
			got = append(got, "D:"+*d.Key)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		km, vm = out.NextKeyMarker, out.NextVersionIdMarker
	}
	// a:1V, b: DM + V, c: V, k: DM + 2V  => 4V... total entries 3+? count
	if len(got) != 1+2+1+3 {
		t.Fatalf("entries %v", got)
	}
	// delete a specific version, and the marker itself
	if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: s("ver"), Key: s("k"), VersionId: &v1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: s("ver"), Key: s("k"), VersionId: del.VersionId}); err != nil {
		t.Fatal(err)
	}
	if d, ok := srv.Object("ver", "k"); !ok || string(d) != "two" {
		t.Fatalf("after restore: %q %v", d, ok)
	}
	// unversioned bucket has null versions
	srv.CreateBucket("plain", false)
	id := srv.PutObject("plain", "x", []byte("1"), "", nil)
	if id != "null" {
		t.Fatalf("id=%q", id)
	}
	pv, _ := c.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: s("plain")})
	if len(pv.Versions) != 1 || *pv.Versions[0].VersionId != "null" || !aws.ToBool(pv.Versions[0].IsLatest) {
		t.Fatalf("plain versions: %+v", pv.Versions)
	}
	// enable versioning through the API; suspend keeps a null version
	if _, err := c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: s("plain"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	g, _ := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: s("plain")})
	if g.Status != types.BucketVersioningStatusEnabled {
		t.Fatalf("status %v", g.Status)
	}
	srv.PutObject("plain", "x", []byte("2"), "", nil)
	if n := len(srv.Versions("plain", "x")); n != 2 {
		t.Fatalf("versions after enable: %d", n)
	}
}

func TestPutGetHeadMetadata(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("obj", false)
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	put, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("obj"), Key: s("dir/file name+1.txt"), Body: strings.NewReader("hello world"),
		ContentType: s("text/plain"), ContentEncoding: s("identity"), CacheControl: s("max-age=5"), ContentDisposition: s("attachment"),
		ContentLanguage: s("en"), Expires: &exp, Metadata: map[string]string{"color": "blue", "Owner": "me"},
		StorageClass: types.StorageClassStandardIa, ServerSideEncryption: types.ServerSideEncryptionAwsKms, SSEKMSKeyId: s("key-1"),
		Tagging: s("a=1&b=2")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(put.ETag) != `"5eb63bbbe01eeed093cb22bb8f5acdc3"` {
		t.Fatalf("etag %v", aws.ToString(put.ETag))
	}
	h, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(h.ContentType) != "text/plain" || aws.ToInt64(h.ContentLength) != 11 || aws.ToString(h.CacheControl) != "max-age=5" ||
		aws.ToString(h.ContentDisposition) != "attachment" || aws.ToString(h.ContentLanguage) != "en" ||
		h.Metadata["color"] != "blue" || h.Metadata["owner"] != "me" || h.StorageClass != types.StorageClassStandardIa ||
		h.ServerSideEncryption != types.ServerSideEncryptionAwsKms || aws.ToString(h.SSEKMSKeyId) != "key-1" ||
		h.Expires == nil || !h.Expires.Equal(exp) || aws.ToInt32(h.TagCount) != 2 {
		t.Fatalf("head: %+v", h)
	}
	if h.LastModified == nil || h.LastModified.Nanosecond() != 0 {
		t.Fatalf("last modified %v", h.LastModified)
	}
	g, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(g.Body)
	if string(b) != "hello world" {
		t.Fatalf("body %q", b)
	}
	tg, err := c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if err != nil || len(tg.TagSet) != 2 {
		t.Fatalf("tags %+v %v", tg, err)
	}
	if _, err := c.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: s("obj"), Key: s("dir/file name+1.txt"), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: s("only"), Value: s("one")}}}}); err != nil {
		t.Fatal(err)
	}
	tg, _ = c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if len(tg.TagSet) != 1 || *tg.TagSet[0].Key != "only" {
		t.Fatalf("tags %+v", tg.TagSet)
	}
	if _, err := c.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("obj"), Key: s("missing")}); storage.ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("missing: %v", err)
	}
	acl, err := c.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if err != nil || len(acl.Grants) != 1 || acl.Grants[0].Permission != types.PermissionFullControl {
		t.Fatalf("acl %+v %v", acl, err)
	}
	if _, err := c.PutObjectAcl(ctx, &s3.PutObjectAclInput{Bucket: s("obj"), Key: s("dir/file name+1.txt"), ACL: types.ObjectCannedACLPublicRead}); err != nil {
		t.Fatal(err)
	}
	acl, _ = c.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: s("obj"), Key: s("dir/file name+1.txt")})
	if len(acl.Grants) != 2 {
		t.Fatalf("acl after canned: %+v", acl.Grants)
	}
}

func TestRangeAndConditionals(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("rng", false)
	srv.PutObject("rng", "k", []byte("0123456789"), "", nil)
	get := func(rng string) (string, *s3.GetObjectOutput, error) {
		out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("rng"), Key: s("k"), Range: s(rng)})
		if err != nil {
			return "", nil, err
		}
		b, _ := io.ReadAll(out.Body)
		return string(b), out, nil
	}
	for rng, want := range map[string]string{"bytes=2-4": "234", "bytes=7-": "789", "bytes=-3": "789", "bytes=8-100": "89"} {
		b, out, err := get(rng)
		if err != nil || b != want {
			t.Fatalf("%s = %q %v", rng, b, err)
		}
		if aws.ToString(out.ContentRange) == "" {
			t.Fatalf("%s no content-range", rng)
		}
	}
	if _, out, _ := get("bytes=2-4"); aws.ToString(out.ContentRange) != "bytes 2-4/10" {
		t.Fatalf("content-range %q", aws.ToString(out.ContentRange))
	}
	if _, _, err := get("bytes=50-60"); storage.ErrorCode(err) != "InvalidRange" {
		t.Fatalf("unsat: %v", err)
	}
	// conditional writes
	_, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("rng"), Key: s("k"), Body: strings.NewReader("z"), IfNoneMatch: s("*")})
	if storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("if-none-match: %v", err)
	}
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("rng"), Key: s("fresh"), Body: strings.NewReader("z"), IfNoneMatch: s("*")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("rng"), Key: s("k"), Body: strings.NewReader("z"), IfMatch: s(`"deadbeef"`)}); storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("if-match: %v", err)
	}
	h, _ := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("rng"), Key: s("k")})
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("rng"), Key: s("k"), Body: strings.NewReader("new"), IfMatch: h.ETag}); err != nil {
		t.Fatalf("if-match ok: %v", err)
	}
	// GET conditionals
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("rng"), Key: s("k"), IfMatch: s(`"nope"`)}); storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("get if-match: %v", err)
	}
	cur, _ := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("rng"), Key: s("k")})
	_, err = c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("rng"), Key: s("k"), IfNoneMatch: cur.ETag})
	var re interface{ HTTPStatusCode() int }
	if err == nil || !errors.As(err, &re) || re.HTTPStatusCode() != 304 {
		t.Fatalf("get if-none-match: %v", err)
	}
	// disabling conditional writes ignores If-None-Match
	srv.SetConditionalWrites(false)
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: s("rng"), Key: s("k"), Body: strings.NewReader("again"), IfNoneMatch: s("*")}); err != nil {
		t.Fatalf("conditional writes disabled: %v", err)
	}
	ok, err := storageClientProbe(t, srv)
	if err != nil || ok {
		t.Fatalf("probe disabled = %v %v", ok, err)
	}
	srv.SetConditionalWrites(true)
	ok, err = storageClientProbe(t, srv)
	if err != nil || !ok {
		t.Fatalf("probe enabled = %v %v", ok, err)
	}
}

func storageClientProbe(t *testing.T, srv *s3test.Server) (bool, error) {
	cl, _ := storage.New(context.Background(), storage.Config{Endpoint: srv.URL, UsePathStyle: true, AccessKeyID: "a", SecretAccessKey: "b"})
	return cl.ProbeConditionalWrites(context.Background(), "rng")
}

func TestCopyObject(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("src", true)
	srv.CreateBucket("dst", true)
	srv.PutObject("src", "a b/c+d.txt", []byte("payload"), "text/plain", map[string]string{"k": "v"})
	v2 := srv.PutObject("src", "a b/c+d.txt", []byte("payload2"), "text/html", map[string]string{"k": "v2"})
	src := storage.CopySource("src", "a b/c+d.txt", "")
	out, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("copy"), CopySource: s(src)})
	if err != nil || out.CopyObjectResult == nil || aws.ToString(out.CopyObjectResult.ETag) == "" || aws.ToString(out.CopySourceVersionId) != v2 || aws.ToString(out.VersionId) == "" {
		t.Fatalf("copy: %+v %v", out, err)
	}
	h, _ := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("dst"), Key: s("copy")})
	if aws.ToString(h.ContentType) != "text/html" || h.Metadata["k"] != "v2" {
		t.Fatalf("COPY directive: %+v", h)
	}
	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("repl"), CopySource: s(src), MetadataDirective: types.MetadataDirectiveReplace,
		ContentType: s("application/json"), Metadata: map[string]string{"n": "1"}, StorageClass: types.StorageClassGlacierIr})
	if err != nil {
		t.Fatal(err)
	}
	h, _ = c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("dst"), Key: s("repl")})
	if aws.ToString(h.ContentType) != "application/json" || h.Metadata["n"] != "1" || h.Metadata["k"] != "" || h.StorageClass != types.StorageClassGlacierIr {
		t.Fatalf("REPLACE directive: %+v", h)
	}
	// older version as source
	vs := srv.Versions("src", "a b/c+d.txt")
	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("old"), CopySource: s(storage.CopySource("src", "a b/c+d.txt", vs[1].VersionID))})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := srv.Object("dst", "old"); string(d) != "payload" {
		t.Fatalf("old = %q", d)
	}
	// source preconditions
	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("x"), CopySource: s(src), CopySourceIfMatch: s(`"bad"`)})
	if storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("copy-source-if-match: %v", err)
	}
	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("copy"), CopySource: s(src), IfNoneMatch: s("*")})
	if storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("dest if-none-match: %v", err)
	}
	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("x"), CopySource: s(storage.CopySource("src", "nokey", ""))})
	if storage.ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("missing source: %v", err)
	}
	// tagging directive
	c.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: s("src"), Key: s("a b/c+d.txt"), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: s("t"), Value: s("1")}}}})
	c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("tc"), CopySource: s(src)})
	c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: s("dst"), Key: s("tr"), CopySource: s(src), TaggingDirective: types.TaggingDirectiveReplace, Tagging: s("z=9")})
	tc, _ := c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("dst"), Key: s("tc")})
	tr, _ := c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("dst"), Key: s("tr")})
	if len(tc.TagSet) != 1 || *tc.TagSet[0].Key != "t" || len(tr.TagSet) != 1 || *tr.TagSet[0].Key != "z" {
		t.Fatalf("tags tc=%+v tr=%+v", tc.TagSet, tr.TagSet)
	}
}

func TestMultipartUploadAndCopy(t *testing.T) {
	srv, cl, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("mp", false)
	const mib = 1 << 20
	data := make([]byte, 12*mib+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	var progress atomic.Int64
	out, err := cl.Upload(ctx, &s3.PutObjectInput{Bucket: s("mp"), Key: s("big.bin"), Body: bytes.NewReader(data), ContentType: s("application/x-test"),
		Metadata: map[string]string{"m": "1"}}, storage.TransferOptions{PartSize: 5 * mib, Concurrency: 2, Progress: func(n int64) { progress.Store(n) }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.Trim(aws.ToString(out.ETag), `"`), "-3") {
		t.Fatalf("etag %q", aws.ToString(out.ETag))
	}
	got, ok := srv.Object("mp", "big.bin")
	if !ok || !bytes.Equal(got, data) {
		t.Fatal("uploaded data differs")
	}
	h, _ := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("mp"), Key: s("big.bin")})
	if aws.ToString(h.ContentType) != "application/x-test" || h.Metadata["m"] != "1" {
		t.Fatalf("head %+v", h)
	}
	// no leftover in-progress uploads
	lu, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: s("mp")})
	if err != nil || len(lu.Uploads) != 0 {
		t.Fatalf("uploads %+v %v", lu, err)
	}
	// multipart copy path (forced by destination precondition)
	err = cl.Copy(ctx, &s3.CopyObjectInput{Bucket: s("mp"), Key: s("copy.bin"), CopySource: s(storage.CopySource("mp", "big.bin", "")), IfNoneMatch: s("*")},
		int64(len(data)), storage.TransferOptions{PartSize: 5 * mib})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = srv.Object("mp", "copy.bin")
	if !bytes.Equal(got, data) {
		t.Fatal("copied data differs")
	}
	err = cl.Copy(ctx, &s3.CopyObjectInput{Bucket: s("mp"), Key: s("copy.bin"), CopySource: s(storage.CopySource("mp", "big.bin", "")), IfNoneMatch: s("*")},
		int64(len(data)), storage.TransferOptions{PartSize: 5 * mib})
	if storage.ErrorCode(err) != "PreconditionFailed" {
		t.Fatalf("second create-only copy: %v", err)
	}
	// manual list parts / uploads / abort
	cu, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: s("mp"), Key: s("manual")})
	if err != nil {
		t.Fatal(err)
	}
	for n := int32(1); n <= 3; n++ {
		if _, err := c.UploadPart(ctx, &s3.UploadPartInput{Bucket: s("mp"), Key: s("manual"), UploadId: cu.UploadId, PartNumber: aws.Int32(n), Body: strings.NewReader(strings.Repeat("x", int(n)))}); err != nil {
			t.Fatal(err)
		}
	}
	lp, err := c.ListParts(ctx, &s3.ListPartsInput{Bucket: s("mp"), Key: s("manual"), UploadId: cu.UploadId, MaxParts: aws.Int32(2)})
	if err != nil || len(lp.Parts) != 2 || !aws.ToBool(lp.IsTruncated) || aws.ToInt64(lp.Parts[1].Size) != 2 {
		t.Fatalf("parts %+v %v", lp, err)
	}
	lu, _ = c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: s("mp")})
	if len(lu.Uploads) != 1 || *lu.Uploads[0].Key != "manual" {
		t.Fatalf("uploads %+v", lu.Uploads)
	}
	_, err = c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: s("mp"), Key: s("manual"), UploadId: cu.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: s(`"bad"`)}}}})
	if storage.ErrorCode(err) != "InvalidPart" {
		t.Fatalf("bad etag: %v", err)
	}
	if _, err := c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: s("mp"), Key: s("manual"), UploadId: cu.UploadId}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListParts(ctx, &s3.ListPartsInput{Bucket: s("mp"), Key: s("manual"), UploadId: cu.UploadId}); storage.ErrorCode(err) != "NoSuchUpload" {
		t.Fatalf("after abort: %v", err)
	}
}

func TestDeleteObjectsAndDroppedAcks(t *testing.T) {
	srv, cl, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("del", false)
	for _, k := range []string{"a", "b", "c"} {
		srv.PutObject("del", k, []byte(k), "", nil)
	}
	res := cl.DeleteObjects(ctx, "del", []storage.ObjectID{{Key: "a"}, {Key: "missing"}})
	if res[0].Status != storage.DeleteSucceeded || res[1].Status != storage.DeleteSucceeded {
		t.Fatalf("res %+v", res)
	}
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPost {
			return &s3test.Fault{DropDeleteAcks: []string{"b"}}
		}
		return nil
	})
	res = cl.DeleteObjects(ctx, "del", []storage.ObjectID{{Key: "b"}, {Key: "c"}})
	if res[0].Status != storage.DeleteUnknown || res[1].Status != storage.DeleteSucceeded {
		t.Fatalf("dropped: %+v", res)
	}
	if _, ok := srv.Object("del", "b"); ok {
		t.Fatal("b should still have been deleted")
	}
	srv.Inject(nil)
	// quiet mode
	srv.PutObject("del", "q", []byte("1"), "", nil)
	out, err := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: s("del"), Delete: &types.Delete{Quiet: aws.Bool(true), Objects: []types.ObjectIdentifier{{Key: s("q")}}}})
	if err != nil || len(out.Deleted) != 0 {
		t.Fatalf("quiet: %+v %v", out, err)
	}
	// versioned delete returns marker info
	srv.CreateBucket("vdel", true)
	srv.PutObject("vdel", "k", []byte("1"), "", nil)
	out, err = c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: s("vdel"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: s("k")}}}})
	if err != nil || len(out.Deleted) != 1 || !aws.ToBool(out.Deleted[0].DeleteMarker) || aws.ToString(out.Deleted[0].DeleteMarkerVersionId) == "" {
		t.Fatalf("versioned: %+v %v", out, err)
	}
}

func TestInjectRetryAndFaults(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("flt", false)
	srv.PutObject("flt", "k", []byte("v"), "", nil)
	var n atomic.Int32
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && n.Add(1) == 1 {
			return &s3test.Fault{Status: 503, Code: "SlowDown", Message: "slow"}
		}
		return nil
	})
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("flt"), Key: s("k")})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	out.Body.Close()
	if n.Load() != 2 {
		t.Fatalf("attempts %d", n.Load())
	}
	srv.Inject(func(r *http.Request) *s3test.Fault {
		return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "nope"}
	})
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: s("flt"), Key: s("k")}); storage.ErrorCode(err) != "AccessDenied" {
		t.Fatalf("403: %v", err)
	}
	// delay respects context
	srv.Inject(func(r *http.Request) *s3test.Fault { return &s3test.Fault{Delay: 5 * time.Second} })
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.HeadObject(cctx, &s3.HeadObjectInput{Bucket: s("flt"), Key: s("k")}); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("delay: %v after %v", err, time.Since(start))
	}
	// dropped connection once, then success through retry
	var d atomic.Int32
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if d.Add(1) == 1 {
			return &s3test.Fault{DropConnection: true}
		}
		return nil
	})
	if _, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: s("flt"), Key: s("k")}); err != nil {
		t.Fatalf("drop+retry: %v", err)
	}
	srv.Inject(nil)
	reqs := srv.Requests()
	if len(reqs) == 0 || reqs[0].Header.Get("Authorization") == "" || reqs[0].Path != "/flt/k" {
		t.Fatalf("log %+v", reqs[:1])
	}
	srv.ResetRequests()
	if len(srv.Requests()) != 0 {
		t.Fatal("reset")
	}
}

func TestUnsupportedAndSubresources(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("sub", false)
	srv.PutObject("sub", "k", []byte("v"), "", nil)
	// missing configs -> proper error codes
	for name, want := range map[string]func() error{
		"NoSuchBucketPolicy":      func() error { _, e := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: s("sub")}); return e },
		"NoSuchCORSConfiguration": func() error { _, e := c.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: s("sub")}); return e },
		"NoSuchLifecycleConfiguration": func() error {
			_, e := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: s("sub")})
			return e
		},
		"ServerSideEncryptionConfigurationNotFoundError": func() error {
			_, e := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: s("sub")})
			return e
		},
		"NoSuchWebsiteConfiguration": func() error { _, e := c.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{Bucket: s("sub")}); return e },
		"NoSuchPublicAccessBlockConfiguration": func() error {
			_, e := c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: s("sub")})
			return e
		},
		"OwnershipControlsNotFoundError": func() error {
			_, e := c.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: s("sub")})
			return e
		},
		"ObjectLockConfigurationNotFoundError": func() error {
			_, e := c.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: s("sub")})
			return e
		},
		"ReplicationConfigurationNotFoundError": func() error {
			_, e := c.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: s("sub")})
			return e
		},
		"NoSuchTagSet": func() error { _, e := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: s("sub")}); return e },
	} {
		if got := storage.ErrorCode(want()); got != name {
			t.Errorf("want %s, got %q", name, got)
		}
	}
	// defaults
	if o, err := c.GetBucketRequestPayment(ctx, &s3.GetBucketRequestPaymentInput{Bucket: s("sub")}); err != nil || o.Payer != types.PayerBucketOwner {
		t.Fatalf("request payment %+v %v", o, err)
	}
	if _, err := c.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{Bucket: s("sub")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBucketAccelerateConfiguration(ctx, &s3.GetBucketAccelerateConfigurationInput{Bucket: s("sub")}); err != nil {
		t.Fatal(err)
	}
	// round trips
	pol := `{"Version":"2012-10-17","Statement":[]}`
	if _, err := c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: s("sub"), Policy: s(pol)}); err != nil {
		t.Fatal(err)
	}
	if o, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: s("sub")}); err != nil || aws.ToString(o.Policy) != pol {
		t.Fatalf("policy %+v %v", o, err)
	}
	if _, err := c.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: s("sub")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: s("sub")}); storage.ErrorCode(err) != "NoSuchBucketPolicy" {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := c.PutBucketCors(ctx, &s3.PutBucketCorsInput{Bucket: s("sub"), CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{AllowedMethods: []string{"GET"}, AllowedOrigins: []string{"*"}}}}}); err != nil {
		t.Fatal(err)
	}
	if o, err := c.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: s("sub")}); err != nil || len(o.CORSRules) != 1 || o.CORSRules[0].AllowedOrigins[0] != "*" {
		t.Fatalf("cors %+v %v", o, err)
	}
	if _, err := c.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{Bucket: s("sub"), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: s("env"), Value: s("t")}}}}); err != nil {
		t.Fatal(err)
	}
	if o, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: s("sub")}); err != nil || len(o.TagSet) != 1 {
		t.Fatalf("bucket tags %+v %v", o, err)
	}
	if _, err := c.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: s("sub")}); err != nil {
		t.Fatal(err)
	}
	// unsupported
	srv.SetUnsupported("tagging")
	if _, err := c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("sub"), Key: s("k")}); storage.ErrorCode(err) != "NotImplemented" {
		t.Fatalf("unsupported: %v", err)
	}
	if _, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: s("sub")}); storage.ErrorCode(err) != "NotImplemented" {
		t.Fatalf("unsupported bucket: %v", err)
	}
	srv.ClearUnsupported("tagging")
	if _, err := c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: s("sub"), Key: s("k")}); err != nil {
		t.Fatal(err)
	}
}

func TestPresignedGetAndAuth(t *testing.T) {
	srv, cl, _ := setup(t)
	srv.CreateBucket("pre", false)
	srv.PutObject("pre", "dir/a b.txt", []byte("presigned!"), "text/plain", nil)
	p, err := cl.Presign(context.Background(), http.MethodGet, "pre", "dir/a b.txt", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "presigned!" || resp.Header.Get("x-amz-request-id") == "" {
		t.Fatalf("presigned: %d %q", resp.StatusCode, b)
	}
	// no credentials -> 403
	r2, err := http.Get(srv.URL + "/pre/dir/a%20b.txt")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != 403 {
		t.Fatalf("anonymous status %d", r2.StatusCode)
	}
}

func TestAWSChunkedDecoding(t *testing.T) {
	srv, _, _ := setup(t)
	srv.CreateBucket("chk", false)
	body := "5\r\nhello\r\n6;chunk-signature=abc\r\n world\r\n0\r\nx-amz-checksum-crc32:AAAA\r\n\r\n"
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/chk/k", strings.NewReader(body))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 x")
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("x-amz-content-sha256", "STREAMING-UNSIGNED-PAYLOAD-TRAILER")
	req.Header.Set("x-amz-decoded-content-length", "11")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if d, _ := srv.Object("chk", "k"); string(d) != "hello world" {
		t.Fatalf("decoded %q", d)
	}
}

func TestStorageClassHelperAndListFields(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("cls", false)
	srv.PutObject("cls", "k", []byte("abc"), "", nil)
	if !srv.SetStorageClass("cls", "k", "GLACIER") {
		t.Fatal("set storage class")
	}
	out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: s("cls"), EncodingType: types.EncodingTypeUrl})
	if err != nil || len(out.Contents) != 1 || out.Contents[0].StorageClass != types.ObjectStorageClassGlacier || aws.ToInt64(out.Contents[0].Size) != 3 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestFxListMultipartUploadsEncodingTypeURL(t *testing.T) {
	srv, _, c := setup(t)
	ctx := context.Background()
	srv.CreateBucket("mpe", false)
	for _, k := range []string{"a b 文", "z+y%"} {
		if _, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: s("mpe"), Key: s(k)}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: s("mpe"), MaxUploads: aws.Int32(1), EncodingType: types.EncodingTypeUrl})
	if err != nil {
		t.Fatal(err)
	}
	if out.EncodingType != types.EncodingTypeUrl || len(out.Uploads) != 1 || aws.ToString(out.Uploads[0].Key) != "a+b+%E6%96%87" ||
		aws.ToString(out.NextKeyMarker) != "a+b+%E6%96%87" || !aws.ToBool(out.IsTruncated) {
		t.Fatalf("encoded listing = %+v", out)
	}
	// Markers in requests are raw, as with real S3.
	out2, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: s("mpe"), EncodingType: types.EncodingTypeUrl,
		KeyMarker: s("a b 文"), UploadIdMarker: out.NextUploadIdMarker})
	if err != nil || len(out2.Uploads) != 1 || aws.ToString(out2.Uploads[0].Key) != "z%2By%25" {
		t.Fatalf("second page = %+v, %v", out2, err)
	}
	// Without encoding-type the keys stay raw.
	plain, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: s("mpe")})
	if err != nil || plain.EncodingType != "" || len(plain.Uploads) != 2 || aws.ToString(plain.Uploads[0].Key) != "a b 文" {
		t.Fatalf("plain listing = %+v, %v", plain, err)
	}
}
