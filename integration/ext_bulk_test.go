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

func (x *extEnv) seedTree(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"src/a.txt":         []byte("alpha"),
		"src/d/b c d.txt":   []byte("beta"),
		"src/d/e/big.bin":   payload(11, 1*extMiB+5),
		"src/zh/文件 1.txt":   []byte("unicode"),
		"src/emoji-😀 x.txt": []byte("emoji"),
	}
	for k, v := range files {
		x.putk(t, k, v)
	}
	return files
}

func (x *extEnv) checkTree(t *testing.T, relPrefix string, files map[string][]byte) {
	t.Helper()
	for k, v := range files {
		dk := x.k(relPrefix + k)
		got, err := x.tryGet(x.bucket, dk, "")
		if err != nil || sum(got) != sum(v) {
			t.Errorf("%s: missing or wrong content (%v)", dk, err)
		}
	}
}

func TestExtCopyMoveRename(t *testing.T) {
	x := newExt(t, "")
	files := x.seedTree(t)

	// Copy a folder inside the bucket: it keeps its own name below the destination.
	id, err := x.svc.Copy(x.pick("", "src/"), x.at("cp/"), app.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items := x.taskOK(t, id)
	if got := countStatus(items, app.ItemSucceeded); got != len(files) {
		t.Errorf("copy items: %d succeeded of %d:%s", got, len(files), describeTasks(nil, items))
	}
	x.checkTree(t, "cp/", files)
	// Copying again with the default policy (skip) skips everything.
	id, _ = x.svc.Copy(x.pick("", "src/"), x.at("cp/"), app.CopyOptions{})
	if items := x.taskOK(t, id); countStatus(items, app.ItemSkipped) != len(files) {
		t.Errorf("second copy should skip all %d objects:%s", len(files), describeTasks(nil, items))
	}
	// Overwrite policy replaces a changed destination.
	x.put(t, x.bucket, x.k("cp/src/a.txt"), []byte("changed copy"))
	id, _ = x.svc.Copy(x.pick("", "src/"), x.at("cp/"), app.CopyOptions{Conflict: app.ConflictOverwrite})
	x.taskOK(t, id)
	x.checkTree(t, "cp/", files)
	// Copy / move into the folder itself is rejected.
	if _, err := x.svc.Copy(x.pick("", "src/"), x.at("src/inner/"), app.CopyOptions{}); err == nil {
		t.Error("copying a folder into itself must be rejected")
	}
	// A true move onto itself (selection rooted at src/, destination src/) is rejected up front.
	if id, err := x.svc.Copy(x.pick("src/", "src/a.txt"), x.at("src/"), app.CopyOptions{Move: true}); err == nil {
		tk, its := x.waitTask(t, id)
		t.Errorf("moving an object onto itself must be rejected by Copy(); task status=%s%s", tk.Status, describeTasks([]app.Task{tk}, its))
	} else if !app.IsKind(err, app.ErrInvalid) {
		t.Errorf("move onto itself: want an Invalid error, got %v", err)
	}
	if got, gerr := x.tryGet(x.bucket, x.k("src/a.txt"), ""); gerr != nil || string(got) != "alpha" {
		t.Errorf("object damaged by the rejected move (%v)", gerr)
	}
	// The destination is dest.Prefix + the key relative to the selection prefix: a legitimate
	// move into a sub-folder.
	x.putk(t, "mv/one.txt", []byte("movable"))
	id, err = x.svc.Copy(x.pick("mv/", "mv/one.txt"), x.at("mv/sub/"), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	if x.exists(x.bucket, x.k("mv/one.txt")) || string(x.get(t, x.bucket, x.k("mv/sub/one.txt"), "")) != "movable" {
		t.Errorf("move into a sub-folder: keys %v", x.relKeys(t, "mv/"))
	}

	// Move: source keys are deleted only after the copy was verified.
	id, err = x.svc.Copy(x.pick("", "cp/"), x.at("moved/"), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	x.checkTree(t, "moved/cp/", files)
	if left := x.relKeys(t, "cp/"); len(left) != 0 {
		t.Errorf("move left source keys: %v", left)
	}

	// Rename an object and a folder.
	id, err = x.svc.Rename(x.ctx, x.at("moved/cp/src/"), x.k("moved/cp/src/a.txt"), "a2.txt")
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	if x.exists(x.bucket, x.k("moved/cp/src/a.txt")) || string(x.get(t, x.bucket, x.k("moved/cp/src/a2.txt"), "")) != "alpha" {
		t.Error("object rename failed")
	}
	id, err = x.svc.Rename(x.ctx, x.at("moved/cp/"), x.k("moved/cp/src/"), "renamed")
	if err != nil {
		t.Fatal(err)
	}
	x.taskOK(t, id)
	renamed := map[string][]byte{}
	for k, v := range files {
		if k == "src/a.txt" {
			k, v = "src/a2.txt", []byte("alpha")
		}
		renamed["renamed/"+strings.TrimPrefix(k, "src/")] = v
	}
	x.checkTree(t, "moved/cp/", renamed)
	if left := x.relKeys(t, "moved/cp/src/"); len(left) != 0 {
		t.Errorf("folder rename left old keys: %v", left)
	}
	// Renaming onto an existing name fails and overwrites nothing.
	x.putk(t, "moved/cp/renamed/taken.txt", []byte("keep me"))
	id, _ = x.svc.Rename(x.ctx, x.at("moved/cp/renamed/"), x.k("moved/cp/renamed/a2.txt"), "taken.txt")
	tk, its := x.waitTask(t, id)
	if tk.Status == app.StatusSucceeded || string(x.get(t, x.bucket, x.k("moved/cp/renamed/taken.txt"), "")) != "keep me" {
		t.Errorf("rename onto an existing key must fail without overwriting:%s", describeTasks([]app.Task{tk}, its))
	}
	t.Logf("rename onto existing name: status=%s%s", tk.Status, describeTasks([]app.Task{tk}, its))
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
	body := payload(21, 123456)
	x.spend(t, int64(len(body))*2)
	put := func(key string) {
		_, err := x.sdk.S3().PutObject(x.ctx, &s3.PutObjectInput{
			Bucket: &x.bucket, Key: aws.String(x.k(key)), Body: strings.NewReader(string(body)),
			ContentType: aws.String("text/plain"), CacheControl: aws.String("max-age=60"), ContentLanguage: aws.String("en"),
			Metadata: map[string]string{"team": "blue", "keep": "yes"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	put("rw/one.txt")
	put("rw/two.txt")
	loc := x.at("")
	one := x.k("rw/one.txt")

	// Object ACL read through the app (read-only). Rewrites call PutObjectAcl to restore
	// grants other than the owner's own FULL_CONTROL; on a gateway whose ACLs are
	// bucket-scoped that would be a bucket-level write, so the rewrite steps only run
	// when the ACL read is empty/unsupported or owner-only.
	rewriteSafe := true
	acl, err := x.svc.ACL(x.ctx, loc, app.ObjectRef{Key: one})
	switch {
	case err != nil:
		t.Logf("ACL read: kind=%s: %v", app.KindOf(err), err)
		if !app.IsKind(err, app.ErrUnsupported) && !app.IsKind(err, app.ErrNotFound) && !app.IsKind(err, app.ErrAccessDenied) {
			t.Errorf("ACL read failed with an unexpected kind: %v", err)
		}
		rewriteSafe = app.IsKind(err, app.ErrUnsupported)
	default:
		t.Logf("ACL read: owner=%q grants=%+v", acl.Owner, acl.Grants)
		for _, g := range acl.Grants {
			if !(g.Grantee == acl.Owner && g.Permission == "FULL_CONTROL") {
				rewriteSafe = false
			}
		}
	}
	if !rewriteSafe {
		t.Log("ACL shows grants beyond owner FULL_CONTROL: skipping the rewrite steps (they would write ACLs back)")
	}

	tagsOK := true
	if err := x.svc.SetTags(x.ctx, loc, app.ObjectRef{Key: one}, map[string]string{"env": "test", "n": "1"}); err != nil {
		tagsOK = false
		t.Logf("SetTags not available: kind=%s: %v", app.KindOf(err), err)
		if !app.IsKind(err, app.ErrUnsupported) {
			t.Errorf("SetTags failed with kind %s, want success or Unsupported: %v", app.KindOf(err), err)
		}
	} else {
		info, err := x.svc.Head(x.ctx, loc, one, "")
		if err != nil || info.Tags["env"] != "test" || info.Tags["n"] != "1" {
			t.Errorf("Head tags = %v (%v / %v)", info.Tags, info.TagsErr, err)
		}
		t.Logf("object tags: supported")
	}
	if !rewriteSafe {
		return
	}

	// SetProperties keeps every untouched header, metadata entry and (best effort) the tags.
	id, err := x.svc.SetProperties(x.pick("", "rw/one.txt"), app.PropertiesPatch{
		SetHeaders:  map[string]string{"Content-Disposition": `attachment; filename="x.txt"`},
		SetMetadata: map[string]string{"added": "1"},
		DelMetadata: []string{"team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := x.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 1 {
		t.Fatalf("SetProperties items:%s", describeTasks(nil, items))
	}
	h := x.head(t, x.bucket, one)
	if aws.ToString(h.ContentType) != "text/plain" || aws.ToString(h.CacheControl) != "max-age=60" ||
		aws.ToString(h.ContentLanguage) != "en" || aws.ToString(h.ContentDisposition) != `attachment; filename="x.txt"` {
		t.Errorf("headers after SetProperties: type=%q cache=%q lang=%q disp=%q", aws.ToString(h.ContentType),
			aws.ToString(h.CacheControl), aws.ToString(h.ContentLanguage), aws.ToString(h.ContentDisposition))
	}
	if h.Metadata["keep"] != "yes" || h.Metadata["added"] != "1" || h.Metadata["team"] != "" {
		t.Errorf("metadata after SetProperties: %v", h.Metadata)
	}
	if sum(x.get(t, x.bucket, one, "")) != sum(body) {
		t.Error("content changed by SetProperties")
	}
	if tagsOK {
		tags, err := x.sdk.S3().GetObjectTagging(x.ctx, &s3.GetObjectTaggingInput{Bucket: &x.bucket, Key: aws.String(one)})
		if err != nil || len(tags.TagSet) != 2 {
			t.Errorf("tags lost by SetProperties: %+v %v", tags, err)
		}
		if err := x.svc.SetTags(x.ctx, loc, app.ObjectRef{Key: one}, map[string]string{"only": "this"}); err != nil {
			t.Fatal(err)
		}
		tags, _ = x.sdk.S3().GetObjectTagging(x.ctx, &s3.GetObjectTaggingInput{Bucket: &x.bucket, Key: aws.String(one)})
		if len(tags.TagSet) != 1 || aws.ToString(tags.TagSet[0].Key) != "only" {
			t.Errorf("SetTags result: %+v", tags.TagSet)
		}
	}

	// SetStorageClass on the folder: record what the gateway accepts.
	for _, class := range []string{"REDUCED_REDUNDANCY", "STANDARD_IA", "GLACIER"} {
		id, err = x.svc.SetStorageClass(x.pick("", "rw/"), class)
		if err != nil {
			t.Fatal(err)
		}
		tk, items := x.waitTask(t, id)
		if tk.Status == app.StatusSucceeded {
			var got []string
			for _, k := range []string{"rw/one.txt", "rw/two.txt"} {
				got = append(got, string(x.head(t, x.bucket, x.k(k)).StorageClass))
			}
			t.Logf("SetStorageClass %s: task succeeded; HEAD reports %v", class, got)
			for i, k := range []string{"rw/one.txt", "rw/two.txt"} {
				if got[i] != class {
					// The app verifies with a HEAD after the rewrite, so this must be a failed item.
					t.Errorf("%s: task succeeded but HEAD says %q, want %s", k, got[i], class)
				}
			}
		} else {
			t.Logf("SetStorageClass %s: refused (status %s):%s", class, tk.Status, describeTasks([]app.Task{tk}, items))
			if len(items) == 0 || items[0].Err == "" {
				t.Errorf("SetStorageClass %s failed without a clear per-item error", class)
			}
		}
		if sum(x.get(t, x.bucket, one, "")) != sum(body) {
			t.Fatalf("object damaged by SetStorageClass %s", class)
		}
		h = x.head(t, x.bucket, one)
		if h.Metadata["keep"] != "yes" || aws.ToString(h.CacheControl) != "max-age=60" {
			t.Errorf("SetStorageClass %s dropped properties: %v %q", class, h.Metadata, aws.ToString(h.CacheControl))
		}
	}
	id, _ = x.svc.SetStorageClass(x.pick("", "rw/two.txt"), "STANDARD")
	x.taskOK(t, id)
	id, _ = x.svc.SetStorageClass(x.pick("", "rw/two.txt"), "STANDARD")
	if items := x.taskOK(t, id); countStatus(items, app.ItemSkipped) != 1 {
		t.Errorf("a second STANDARD rewrite should skip:%s", describeTasks(nil, items))
	}

	// Encryption AES256 rewrite (SSE-S3).
	two := x.k("rw/two.txt")
	id, err = x.svc.SetEncryption(x.pick("", "rw/two.txt"), app.Encryption{Mode: "AES256"})
	if err != nil {
		t.Fatal(err)
	}
	tk, items := x.waitTask(t, id)
	if tk.Status == app.StatusSucceeded {
		sse := x.head(t, x.bucket, two).ServerSideEncryption
		t.Logf("SetEncryption AES256: accepted; HEAD reports SSE=%q", sse)
		if sse != types.ServerSideEncryptionAes256 {
			t.Errorf("SetEncryption AES256 succeeded but HEAD reports SSE=%q", sse)
		}
	} else {
		t.Logf("SetEncryption AES256: refused:%s", describeTasks([]app.Task{tk}, items))
		if len(items) == 0 || items[0].Err == "" {
			t.Errorf("SetEncryption failure without a clear error:%s", describeTasks([]app.Task{tk}, items))
		}
	}
	if sum(x.get(t, x.bucket, two, "")) != sum(body) {
		t.Error("encryption rewrite damaged the object")
	}
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
