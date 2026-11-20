package integration_test

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// TestExtCopyMoveRename runs the shared copy/move/rename scenario with the move
// going to the sibling folder moved/ below the same test prefix.
func TestExtCopyMoveRename(t *testing.T) {
	x := newExt(t, "")
	copyMoveRename(t, x.target, x.sub("moved/"), 1*extMiB+5)
}

// TestExtCopySourceEncoding copies single objects whose keys contain URL-sensitive
// characters through the app (each key its own subtest) and, for "+", shows with raw SDK
// calls how this server reads x-amz-copy-source.
func TestExtCopySourceEncoding(t *testing.T) {
	x := newExt(t, "")
	names := []string{
		"plus+only.txt", "a b+c d.txt", "pct%41.txt", "semi;amp&eq=hash#q?.txt", "中文 文件.txt", "emoji-😀.txt",
		"tilde~star*paren(1)!.txt", "quote'dq\"lt<gt>.txt", "plus+/nested+dir/x+y.txt",
	}
	for i, name := range names {
		t.Run(fmt.Sprintf("key%d", i), func(t *testing.T) {
			rel := "enc/" + name
			body := []byte("body of " + name)
			x.putk(t, rel, body)
			dest := "encout" + string(rune('a'+i)) + "/"
			id, err := x.svc.Copy(x.pick("enc/", rel), x.at(dest), app.CopyOptions{})
			if err != nil {
				t.Fatal(err)
			}
			tk, items := x.waitTask(t, id)
			if tk.Status != app.StatusSucceeded {
				t.Errorf("app Copy of %q failed:%s", name, describeTasks([]app.Task{tk}, items))
				return
			}
			keys := x.relKeys(t, dest)
			if len(keys) != 1 {
				t.Fatalf("destination keys: %v", keys)
			}
			if got := x.get(t, x.bucket, x.k(keys[0]), ""); !bytes.Equal(got, body) {
				t.Errorf("copy of %q has wrong content", name)
			}
		})
	}

	// Raw view of the same problem: how does the gateway decode "+" in x-amz-copy-source?
	src := x.k("enc/plus+only.txt")
	for n, c := range []struct{ label, source string }{
		{"literal + (as storage.CopySource emits it)", x.bucket + "/" + strings.ReplaceAll(url.PathEscape(src), "%2F", "/")},
		{"%2B (fully escaped)", x.bucket + "/" + strings.ReplaceAll(strings.ReplaceAll(url.PathEscape(src), "+", "%2B"), "%2F", "/")},
	} {
		dst := x.k(fmt.Sprintf("enc/raw-%d.txt", n))
		_, err := x.sdk.S3().CopyObject(x.ctx, &s3.CopyObjectInput{Bucket: &x.bucket, Key: &dst, CopySource: aws.String(c.source)})
		t.Logf("raw CopyObject with copy-source using %s: %v", c.label, map[bool]string{true: "OK", false: "FAILED " + errCode(err)}[err == nil])
	}
	t.Logf("storage.CopySource emits: %q", strings.TrimPrefix(storage.CopySource(x.bucket, src, ""), x.bucket+"/"+x.prefix))
}

// TestExtPropertiesAndRewrites: headers/metadata/tags/ACL read, storage class and
// encryption rewrites. Server limits are recorded; whatever the server refuses must come
// back as a clear per-item error with the object untouched.
func TestExtPropertiesAndRewrites(t *testing.T) {
	x := newExt(t, "")
	propertiesAndRewrites(t, x.target, false)
}

// TestExtVersions runs only when the bucket reports versioning Enabled; it never turns it on.
func TestExtVersions(t *testing.T) {
	x := newExt(t, "")
	out, err := x.sdk.S3().GetBucketVersioning(x.ctx, &s3.GetBucketVersioningInput{Bucket: &x.bucket})
	if err != nil {
		t.Skipf("bucket versioning state unavailable (%s: %v); this suite never changes bucket configuration", errCode(err), err)
	}
	if out.Status != types.BucketVersioningStatusEnabled {
		t.Skipf("bucket versioning is %q, not Enabled; this suite never changes bucket configuration", out.Status)
	}
	v1 := x.put(t, x.bucket, x.k("v/doc.txt"), []byte("one"))
	v2 := x.put(t, x.bucket, x.k("v/doc.txt"), []byte("two"))
	v3 := x.put(t, x.bucket, x.k("v/doc.txt"), []byte("three"))
	page, err := x.svc.ListVersions(x.ctx, x.at("v/"), "", "")
	if err != nil || len(page.Versions) != 3 {
		t.Fatalf("ListVersions = %+v, %v", page, err)
	}
	if err := x.svc.RestoreVersion(x.ctx, x.at("v/"), app.ObjectRef{Key: x.k("v/doc.txt"), VersionID: v1}); err != nil {
		t.Fatal(err)
	}
	if string(x.get(t, x.bucket, x.k("v/doc.txt"), "")) != "one" || string(x.get(t, x.bucket, x.k("v/doc.txt"), v3)) != "three" {
		t.Fatal("RestoreVersion result wrong")
	}
	id, err := x.svc.Delete(x.pick("v/", "v/doc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	id, err = x.svc.RestoreDeleted(x.pick("v/", "v/doc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	id, err = x.svc.DeleteVersions(x.at("v/"), []app.ObjectRef{{Key: x.k("v/doc.txt"), VersionID: v2}})
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	if _, err := x.tryGet(x.bucket, x.k("v/doc.txt"), v2); err == nil {
		t.Error("deleted version is still readable")
	}
}
