package integration_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// TestExtBucketsAndBrowse: account test, bucket list/region, folder browsing.
func TestExtBucketsAndBrowse(t *testing.T) {
	x := newExt(t, "")
	if err := x.svc.TestAccount(x.ctx, x.acct, &app.Secrets{SecretAccessKey: x.ecfg.SecretKey}); err != nil {
		t.Fatalf("TestAccount: %v", err)
	}
	bs, err := x.svc.Buckets(x.ctx, x.acct.ID)
	if err != nil {
		t.Fatalf("Buckets: %v", err)
	}
	found := false
	for _, b := range bs {
		if b.Name == x.bucket {
			found = true
			t.Logf("bucket %q: region=%q created=%s", b.Name, b.Region, b.Created.Format(time.RFC3339))
			if b.Region == "" {
				t.Logf("server does not report a bucket region in ListBuckets (the app falls back to the account region %q)", x.ecfg.Region)
			}
		}
	}
	t.Logf("account sees %d bucket(s)", len(bs))
	if !found {
		t.Fatalf("bucket %q is not visible to the account", x.bucket)
	}

	// Read-only sanity check of the bucket root: counts only, no names.
	if pg, err := x.svc.List(x.ctx, x.loc(x.bucket, ""), app.ListOptions{MaxKeys: 50}); err != nil {
		t.Errorf("List(bucket root): %v", err)
	} else {
		t.Logf("bucket root first page: %d entries (more=%v)", len(pg.Entries), pg.NextToken != "")
	}

	// Own prefix: empty, then a small tree.
	if pg, err := x.svc.List(x.ctx, x.at(""), app.ListOptions{}); err != nil || len(pg.Entries) != 0 {
		t.Fatalf("fresh prefix List = %+v, %v", pg, err)
	}
	x.putk(t, "a.txt", []byte("a"))
	x.putk(t, "dir/b.txt", []byte("bb"))
	x.putk(t, "dir/sub/c.txt", []byte("ccc"))
	if err := x.svc.CreateFolder(x.ctx, x.at(""), "made"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	pg, err := x.svc.List(x.ctx, x.at(""), app.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]app.Entry{}
	for _, e := range pg.Entries {
		names[e.Name] = e
	}
	if len(names) != 3 || !names["dir"].Folder || !names["made"].Folder || names["a.txt"].Size != 1 || names["a.txt"].Folder {
		t.Fatalf("delimiter listing: %+v", pg.Entries)
	}
	if names["a.txt"].Modified.IsZero() || names["a.txt"].ETag == "" {
		t.Errorf("entry misses Modified/ETag: %+v", names["a.txt"])
	}
	t.Logf("storage class reported in listings: %q", names["a.txt"].StorageClass)
	flat, err := x.svc.List(x.ctx, x.at(""), app.ListOptions{Flat: true})
	if err != nil || len(flat.Entries) != 4 { // a.txt, dir/b.txt, dir/sub/c.txt, made/
		t.Fatalf("flat listing: %+v, %v", flat.Entries, err)
	}
	info, err := x.svc.Head(x.ctx, x.at(""), x.k("dir/b.txt"), "")
	if err != nil || info.Size != 2 || info.ETag == "" {
		t.Fatalf("Head = %+v, %v", info, err)
	}
	t.Logf("Head: contentType=%q storageClass=%q tags=%v tagsErr=%v sse=%q", info.ContentType, info.StorageClass, info.Tags, info.TagsErr, info.SSE)
	if _, err := x.svc.Head(x.ctx, x.at(""), x.k("nope.txt"), ""); !app.IsKind(err, app.ErrNotFound) {
		t.Errorf("Head of a missing key must be NotFound, got %v", err)
	}
	data, trunc, err := x.svc.Preview(x.ctx, x.at(""), x.k("dir/sub/c.txt"), "", 2)
	if err != nil || string(data) != "cc" || !trunc {
		t.Errorf("Preview = %q trunc=%v err=%v", data, trunc, err)
	}
}

// TestExtListingPagingAndBulkDelete creates 1105 one-byte objects, pages through them
// (default and small page size), estimates and deletes them via the app (DeleteObjects
// batching across the 1000-key boundary) and checks the per-item results.
func TestExtListingPagingAndBulkDelete(t *testing.T) {
	x := newExt(t, "")
	const n = 1105
	x.spend(t, n)
	start := time.Now()
	x.putMany(t, x.bucket, x.prefix+"many/", n)
	t.Logf("created %d one-byte objects in %s", n, time.Since(start).Round(time.Millisecond))
	x.putk(t, "keep/me.txt", []byte("keep"))

	count := func(maxKeys int32) (entries, pages int) {
		tok := ""
		for {
			pg, err := x.svc.List(x.ctx, x.at("many/"), app.ListOptions{Flat: true, Token: tok, MaxKeys: maxKeys})
			if err != nil {
				t.Fatalf("List page %d: %v", pages, err)
			}
			entries += len(pg.Entries)
			pages++
			if pg.NextToken == "" {
				return
			}
			tok = pg.NextToken
		}
	}
	if e, p := count(0); e != n || p != 2 {
		t.Errorf("default page size: %d entries over %d pages (want %d over 2)", e, p, n)
	}
	if e, p := count(200); e != n || p != 6 {
		t.Errorf("MaxKeys=200: %d entries over %d pages (want %d over 6)", e, p, n)
	}
	est, err := x.svc.Estimate(x.ctx, x.pick("", "many/"))
	if err != nil || est.Objects != n || est.Bytes != n {
		t.Fatalf("Estimate = %+v, %v", est, err)
	}
	start = time.Now()
	id, err := x.svc.Delete(x.pick("", "many/"))
	if err != nil {
		t.Fatal(err)
	}
	items := x.taskOK(t, id)
	t.Logf("deleted %d objects through the app in %s", n, time.Since(start).Round(time.Millisecond))
	if got := countStatus(items, app.ItemSucceeded); got != n || len(items) != n {
		t.Errorf("%d succeeded of %d items, want %d", got, len(items), n)
	}
	if left := x.relKeys(t, ""); len(left) != 1 || left[0] != "keep/me.txt" {
		t.Fatalf("after delete: %d keys left, e.g. %v", len(left), left[:min(len(left), 5)])
	}
}

// TestExtBucketConfigReads reads every bucket sub-resource (read-only) and records
// which ones this server implements. Nothing is written.
func TestExtBucketConfigReads(t *testing.T) {
	x := newExt(t, "")
	all := []app.BucketConfig{
		app.ConfigVersioning, app.ConfigEncryption, app.ConfigPolicy, app.ConfigCORS, app.ConfigLifecycle,
		app.ConfigLogging, app.ConfigTagging, app.ConfigWebsite, app.ConfigPublicAccessBlock, app.ConfigOwnership,
		app.ConfigObjectLock, app.ConfigRequestPayment, app.ConfigAccelerate, app.ConfigReplication,
	}
	var supported, unsupported, empty []string
	for _, c := range all {
		js, err := x.svc.GetBucketConfig(x.ctx, x.acct.ID, x.bucket, c)
		switch {
		case err == nil && js == "":
			empty = append(empty, string(c))
			t.Logf("%-20s supported, not configured", c)
		case err == nil:
			supported = append(supported, string(c))
			t.Logf("%-20s supported, configured (%d bytes)", c, len(js))
		default:
			var oe *app.OpError
			kind := app.KindOf(err)
			code := ""
			if ok := asOpError(err, &oe); ok {
				code = oe.Code
			}
			unsupported = append(unsupported, fmt.Sprintf("%s=%s/%s", c, kind, code))
			t.Logf("%-20s error kind=%s code=%q: %v", c, kind, code, err)
			switch kind {
			case app.ErrNetwork, app.ErrCanceled, app.ErrInvalid:
				t.Errorf("%s: unexpected error kind %s for a read-only GET: %v", c, kind, err)
			}
		}
	}
	t.Logf("SUMMARY supported-configured=%v supported-empty=%v errors=%v", supported, empty, unsupported)

	// Versioning state decides whether the versions test runs; the bucket is never changed.
	out, err := x.sdk.S3().GetBucketVersioning(x.ctx, &s3.GetBucketVersioningInput{Bucket: &x.bucket})
	if err != nil {
		t.Logf("GetBucketVersioning: %v", err)
	} else {
		t.Logf("GetBucketVersioning: status=%q", out.Status)
	}
	if loc, err := x.sdk.S3().GetBucketLocation(x.ctx, &s3.GetBucketLocationInput{Bucket: &x.bucket}); err == nil {
		t.Logf("GetBucketLocation: %q", loc.LocationConstraint)
	} else {
		t.Logf("GetBucketLocation: %v", err)
	}
	if acl, err := x.sdk.S3().GetBucketAcl(x.ctx, &s3.GetBucketAclInput{Bucket: &x.bucket}); err == nil {
		t.Logf("GetBucketAcl: %d grant(s)", len(acl.Grants))
	} else {
		t.Logf("GetBucketAcl: %v", err)
	}
}

// TestExtConditionalWrites records whether the server honours If-None-Match: * and
// If-Match on PutObject and CopyObject (raw SDK calls on keys below the run prefix).
func TestExtConditionalWrites(t *testing.T) {
	x := newExt(t, "")
	s := x.sdk.S3()
	key := x.k("cond/one.txt")
	put := func(body string, mod func(*s3.PutObjectInput)) error {
		in := &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: strings.NewReader(body)}
		if mod != nil {
			mod(in)
		}
		_, err := s.PutObject(x.ctx, in)
		return err
	}
	code := func(err error) string {
		if err == nil {
			return "accepted"
		}
		return fmt.Sprintf("%s (%v)", errCode(err), err)
	}
	e1 := put("first", func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String("*") })
	t.Logf("PutObject If-None-Match:* on a free key: %s", code(e1))
	e2 := put("second", func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String("*") })
	t.Logf("PutObject If-None-Match:* on an existing key: %s", code(e2))
	body, _ := x.tryGet(x.bucket, key, "")
	switch {
	case e1 != nil:
		t.Logf("RESULT: If-None-Match is rejected on PutObject; the app marks the account and falls back to HEAD-only checks")
	case e2 == nil && string(body) == "second":
		t.Logf("RESULT: If-None-Match:* is IGNORED on PutObject (second write overwrote); create-only relies on the app's HEAD check")
	case e2 != nil && string(body) == "first":
		t.Logf("RESULT: If-None-Match:* is HONOURED on PutObject (existing key refused, content intact)")
	default:
		t.Logf("RESULT: inconclusive; body=%q", body)
	}
	head := x.head(t, x.bucket, key)
	e3 := put("third", func(in *s3.PutObjectInput) { in.IfMatch = aws.String(`"00000000000000000000000000000000"`) })
	t.Logf("PutObject If-Match:<wrong etag>: %s", code(e3))
	e4 := put("fourth", func(in *s3.PutObjectInput) { in.IfMatch = head.ETag })
	t.Logf("PutObject If-Match:<current etag>: %s", code(e4))
	body, _ = x.tryGet(x.bucket, key, "")
	t.Logf("content after the If-Match writes: %q (If-Match wrong etag honoured=%v)", body, e3 != nil)

	// CopyObject onto an existing destination with If-None-Match.
	dst := x.k("cond/copy.txt")
	_, ce1 := s.CopyObject(x.ctx, &s3.CopyObjectInput{Bucket: &x.bucket, Key: &dst, CopySource: aws.String(url.PathEscape(x.bucket) + "/" + escapeKey(key)), IfNoneMatch: aws.String("*")})
	_, ce2 := s.CopyObject(x.ctx, &s3.CopyObjectInput{Bucket: &x.bucket, Key: &dst, CopySource: aws.String(url.PathEscape(x.bucket) + "/" + escapeKey(key)), IfNoneMatch: aws.String("*")})
	t.Logf("CopyObject If-None-Match:* free dst: %s; existing dst: %s", code(ce1), code(ce2))

	// The app on top of whatever the server does: a skip-policy upload to an existing key must not overwrite.
	src := t.TempDir()
	writeLocal(t, src+"/one.txt", []byte("from the app"))
	g, err := x.svc.Upload(app.UploadRequest{Dest: x.at("cond/"), Paths: []string{src + "/one.txt"}, Conflict: app.ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	items := x.groupOK(t, g)
	if countStatus(items, app.ItemSkipped) != 1 {
		t.Errorf("skip policy against an existing key must skip:%s", describeTasks(nil, items))
	}
}

// ---- small shared helpers ----------------------------------------------------------

func asOpError(err error, target **app.OpError) bool { return errors.As(err, target) }

func codeOf(err error) string { return storage.ErrorCode(err) }

func errCode(err error) string {
	var oe *app.OpError
	if asOpError(err, &oe) && oe.Code != "" {
		return oe.Code
	}
	return codeOf(err)
}

// escapeKey percent-encodes every segment of an object key for CopySource / URL paths.
func escapeKey(k string) string {
	parts := strings.Split(k, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// httpGet performs a plain GET with a short timeout and returns status, body (<= 4 MiB) and headers.
func httpGet(rawURL string, timeout time.Duration) (int, []byte, http.Header, error) {
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Get(rawURL)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, b, resp.Header, err
}
