package integration_test

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/app"
)

// Copy, move, rename and rewrite scenarios shared by TestApp* (MinIO) and
// TestExt* (external endpoint).

// seedTree puts the source tree below src/ and returns it (key relative to the
// target -> content). bigSize keeps the external run inside its data budget. The
// "+" key is MinIO-only: the external suite records how its gateway decodes "+"
// in x-amz-copy-source separately (TestExtCopySourceEncoding).
func seedTree(t *testing.T, tg *target, bigSize int) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"src/a.txt":         []byte("alpha"),
		"src/d/e/big.bin":   payload(11, bigSize),
		"src/zh/文件 1.txt":   []byte("unicode"),
		"src/emoji-😀 x.txt": []byte("emoji"),
	}
	if tg.external {
		files["src/d/b c d.txt"] = []byte("beta")
	} else {
		files["src/d/b+c d.txt"] = []byte("beta")
	}
	for k, v := range files {
		tg.putk(t, k, v)
	}
	return files
}

// copyMoveRename copies the seeded tree inside the target, checks the conflict and
// self-move rules, moves the copy to moveTo (another bucket on MinIO, a sibling
// folder on the external endpoint) and renames an object and a folder there.
// It returns the seeded tree, which is still below src/.
func copyMoveRename(t *testing.T, tg, moveTo *target, bigSize int) map[string][]byte {
	files := seedTree(t, tg, bigSize)

	// Copy a folder inside the bucket: it keeps its own name below the destination.
	id, err := tg.svc.Copy(tg.pick("", "src/"), tg.at("cp/"), app.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items := tg.taskOK(t, id)
	if got := countStatus(items, app.ItemSucceeded); got != len(files) {
		t.Errorf("copy items: %d succeeded of %d:%s", got, len(files), describeTasks(nil, items))
	}
	tg.checkTree(t, "cp/", files)
	// Copying again with the default policy (skip) skips everything.
	id, _ = tg.svc.Copy(tg.pick("", "src/"), tg.at("cp/"), app.CopyOptions{})
	if items := tg.taskOK(t, id); countStatus(items, app.ItemSkipped) != len(files) {
		t.Errorf("second copy should skip all %d objects:%s", len(files), describeTasks(nil, items))
	}
	// Overwrite policy replaces a changed destination.
	tg.putk(t, "cp/src/a.txt", []byte("changed copy"))
	id, _ = tg.svc.Copy(tg.pick("", "src/"), tg.at("cp/"), app.CopyOptions{Conflict: app.ConflictOverwrite})
	tg.taskOK(t, id)
	tg.checkTree(t, "cp/", files)
	// Copy / move into the folder itself is rejected.
	if _, err := tg.svc.Copy(tg.pick("", "src/"), tg.at("src/inner/"), app.CopyOptions{}); err == nil {
		t.Error("copying a folder into itself must be rejected")
	}
	// A true move onto itself (selection rooted at src/, destination src/) is rejected up front.
	if id, err := tg.svc.Copy(tg.pick("src/", "src/a.txt"), tg.at("src/"), app.CopyOptions{Move: true}); err == nil {
		tk, its := tg.waitTask(t, id)
		t.Errorf("moving an object onto itself must be rejected by Copy(); task status=%s%s", tk.Status, describeTasks([]app.Task{tk}, its))
	} else if !app.IsKind(err, app.ErrInvalid) {
		t.Errorf("move onto itself: want an Invalid error, got %v", err)
	}
	if got, gerr := tg.tryGet(tg.bucket, tg.k("src/a.txt"), ""); gerr != nil || string(got) != "alpha" {
		t.Errorf("object damaged by the rejected move (%v)", gerr)
	}
	// The destination is dest.Prefix + the key relative to the selection prefix: a legitimate
	// move into a sub-folder.
	tg.putk(t, "mv/one.txt", []byte("movable"))
	id, err = tg.svc.Copy(tg.pick("mv/", "mv/one.txt"), tg.at("mv/sub/"), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	tg.taskOK(t, id)
	if tg.existsk("mv/one.txt") || string(tg.getk(t, "mv/sub/one.txt")) != "movable" {
		t.Errorf("move into a sub-folder: keys %v", tg.relKeys(t, "mv/"))
	}

	// Move: source keys are deleted only after the copy was verified.
	id, err = tg.svc.Copy(tg.pick("", "cp/"), moveTo.at(""), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	tg.taskOK(t, id)
	moveTo.checkTree(t, "cp/", files)
	if left := tg.relKeys(t, "cp/"); len(left) != 0 {
		t.Errorf("move left source keys: %v", left)
	}

	// Rename an object and a folder.
	id, err = moveTo.svc.Rename(moveTo.ctx, moveTo.at("cp/src/"), moveTo.k("cp/src/a.txt"), "a2.txt")
	if err != nil {
		t.Fatal(err)
	}
	moveTo.taskOK(t, id)
	if moveTo.existsk("cp/src/a.txt") || string(moveTo.getk(t, "cp/src/a2.txt")) != "alpha" {
		t.Error("object rename failed")
	}
	id, err = moveTo.svc.Rename(moveTo.ctx, moveTo.at("cp/"), moveTo.k("cp/src/"), "renamed")
	if err != nil {
		t.Fatal(err)
	}
	moveTo.taskOK(t, id)
	renamed := map[string][]byte{}
	for k, v := range files {
		if k == "src/a.txt" {
			k, v = "src/a2.txt", []byte("alpha")
		}
		renamed["renamed/"+strings.TrimPrefix(k, "src/")] = v
	}
	moveTo.checkTree(t, "cp/", renamed)
	if left := moveTo.relKeys(t, "cp/src/"); len(left) != 0 {
		t.Errorf("folder rename left old keys: %v", left)
	}
	// Renaming onto an existing name fails and overwrites nothing.
	moveTo.putk(t, "cp/renamed/taken.txt", []byte("keep me"))
	id, _ = moveTo.svc.Rename(moveTo.ctx, moveTo.at("cp/renamed/"), moveTo.k("cp/renamed/a2.txt"), "taken.txt")
	tk, its := moveTo.waitTask(t, id)
	if tk.Status == app.StatusSucceeded || string(moveTo.getk(t, "cp/renamed/taken.txt")) != "keep me" {
		t.Errorf("rename onto an existing key must fail without overwriting:%s", describeTasks([]app.Task{tk}, its))
	}
	t.Logf("rename onto existing name: status=%s%s", tk.Status, describeTasks([]app.Task{tk}, its))
	return files
}

// propertiesAndRewrites: tags, SetProperties (untouched headers, metadata and tags
// kept), storage class and SSE rewrites. versioned: the bucket keeps versions, so a
// rewrite must add exactly one. On an external target the object ACL is read first
// and the rewrite steps run only when that is safe; server limits are recorded, and
// whatever the server refuses must come back as a clear per-item error with the
// object untouched.
func propertiesAndRewrites(t *testing.T, tg *target, versioned bool) {
	body := payload(21, 123456)
	tg.spend(t, int64(len(body))*2)
	put := func(rel string) {
		_, err := tg.sdk.S3().PutObject(tg.ctx, &s3.PutObjectInput{
			Bucket: &tg.bucket, Key: aws.String(tg.k(rel)), Body: strings.NewReader(string(body)),
			ContentType: aws.String("text/plain"), CacheControl: aws.String("max-age=60"), ContentLanguage: aws.String("en"),
			Metadata: map[string]string{"team": "blue", "keep": "yes"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	put("rw/one.txt")
	put("rw/two.txt")
	loc := tg.at("")
	one, two := tg.k("rw/one.txt"), tg.k("rw/two.txt")

	// Object ACL read through the app (read-only). Rewrites call PutObjectAcl to restore
	// grants other than the owner's own FULL_CONTROL; on a gateway whose ACLs are
	// bucket-scoped that would be a bucket-level write, so on the external endpoint the
	// rewrite steps only run when the ACL read is empty/unsupported or owner-only.
	rewriteSafe := true
	if tg.external {
		acl, err := tg.svc.ACL(tg.ctx, loc, app.ObjectRef{Key: one})
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
	}

	tagsOK := true
	if err := tg.svc.SetTags(tg.ctx, loc, app.ObjectRef{Key: one}, map[string]string{"env": "test", "n": "1"}); err != nil {
		if !tg.external {
			t.Fatalf("SetTags: %v", err)
		}
		tagsOK = false
		t.Logf("SetTags not available: kind=%s: %v", app.KindOf(err), err)
		if !app.IsKind(err, app.ErrUnsupported) {
			t.Errorf("SetTags failed with kind %s, want success or Unsupported: %v", app.KindOf(err), err)
		}
	} else {
		info, err := tg.svc.Head(tg.ctx, loc, one, "")
		if err != nil || info.Tags["env"] != "test" || info.Tags["n"] != "1" {
			t.Errorf("Head tags = %v (%v / %v)", info.Tags, info.TagsErr, err)
		}
	}
	if !rewriteSafe {
		return
	}
	verBefore := 0
	if versioned {
		verBefore = len(tg.versions(t, tg.bucket, one))
	}

	// SetProperties keeps every untouched header, metadata entry and the tags.
	id, err := tg.svc.SetProperties(tg.pick("", "rw/one.txt"), app.PropertiesPatch{
		SetHeaders:  map[string]string{"Content-Disposition": `attachment; filename="x.txt"`},
		SetMetadata: map[string]string{"added": "1"},
		DelMetadata: []string{"team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := tg.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 1 {
		t.Fatalf("SetProperties items:%s", describeTasks(nil, items))
	}
	h := tg.head(t, tg.bucket, one)
	if aws.ToString(h.ContentType) != "text/plain" || aws.ToString(h.CacheControl) != "max-age=60" ||
		aws.ToString(h.ContentLanguage) != "en" || aws.ToString(h.ContentDisposition) != `attachment; filename="x.txt"` {
		t.Errorf("headers after SetProperties: type=%q cache=%q lang=%q disp=%q", aws.ToString(h.ContentType),
			aws.ToString(h.CacheControl), aws.ToString(h.ContentLanguage), aws.ToString(h.ContentDisposition))
	}
	if h.Metadata["keep"] != "yes" || h.Metadata["added"] != "1" || h.Metadata["team"] != "" {
		t.Errorf("metadata after SetProperties: %v", h.Metadata)
	}
	if sum(tg.get(t, tg.bucket, one, "")) != sum(body) {
		t.Error("content changed by SetProperties")
	}
	if versioned {
		if n := len(tg.versions(t, tg.bucket, one)); n != verBefore+1 {
			t.Errorf("versioned rewrite should add one version: %d -> %d", verBefore, n)
		}
	}
	if tagsOK {
		tags, err := tg.sdk.S3().GetObjectTagging(tg.ctx, &s3.GetObjectTaggingInput{Bucket: &tg.bucket, Key: aws.String(one)})
		if err != nil || len(tags.TagSet) != 2 {
			t.Errorf("tags lost by SetProperties: %+v %v", tags, err)
		}
		// SetTags replaces the set.
		if err := tg.svc.SetTags(tg.ctx, loc, app.ObjectRef{Key: one}, map[string]string{"only": "this"}); err != nil {
			t.Fatal(err)
		}
		tags, _ = tg.sdk.S3().GetObjectTagging(tg.ctx, &s3.GetObjectTaggingInput{Bucket: &tg.bucket, Key: aws.String(one)})
		if len(tags.TagSet) != 1 || aws.ToString(tags.TagSet[0].Key) != "only" {
			t.Errorf("SetTags result: %+v", tags.TagSet)
		}
	}

	// SetStorageClass on the folder. MinIO must accept REDUCED_REDUNDANCY; the external
	// endpoint records which classes its gateway accepts.
	classes := []string{"REDUCED_REDUNDANCY"}
	if tg.external {
		classes = append(classes, "STANDARD_IA", "GLACIER")
	}
	for _, class := range classes {
		id, err = tg.svc.SetStorageClass(tg.pick("", "rw/"), class)
		if err != nil {
			t.Fatal(err)
		}
		tk, items := tg.waitTask(t, id)
		if tk.Status == app.StatusSucceeded {
			var got []string
			for _, k := range []string{"rw/one.txt", "rw/two.txt"} {
				got = append(got, string(tg.head(t, tg.bucket, tg.k(k)).StorageClass))
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
			if !tg.external {
				t.Errorf("SetStorageClass %s failed", class)
			}
			if len(items) == 0 || items[0].Err == "" {
				t.Errorf("SetStorageClass %s failed without a clear per-item error", class)
			}
		}
		// Unchanged data and properties after the class rewrite.
		if sum(tg.get(t, tg.bucket, one, "")) != sum(body) {
			t.Fatalf("object damaged by SetStorageClass %s", class)
		}
		h = tg.head(t, tg.bucket, one)
		if h.Metadata["keep"] != "yes" || aws.ToString(h.CacheControl) != "max-age=60" {
			t.Errorf("SetStorageClass %s dropped properties: %v %q", class, h.Metadata, aws.ToString(h.CacheControl))
		}
	}
	// Back to STANDARD; already-STANDARD objects are skipped.
	id, _ = tg.svc.SetStorageClass(tg.pick("", "rw/two.txt"), "STANDARD")
	tg.taskOK(t, id)
	if c := tg.head(t, tg.bucket, two).StorageClass; c != "" && c != types.StorageClassStandard {
		t.Errorf("STANDARD rewrite left class %q", c)
	}
	id, _ = tg.svc.SetStorageClass(tg.pick("", "rw/two.txt"), "STANDARD")
	if items := tg.taskOK(t, id); countStatus(items, app.ItemSkipped) != 1 {
		t.Errorf("a second STANDARD rewrite should skip:%s", describeTasks(nil, items))
	}

	// Encryption AES256 rewrite (SSE-S3); MinIO needs a KMS/auto-encryption setup for it.
	id, err = tg.svc.SetEncryption(tg.pick("", "rw/two.txt"), app.Encryption{Mode: "AES256"})
	if err != nil {
		t.Fatal(err)
	}
	tk, items := tg.waitTask(t, id)
	if tk.Status == app.StatusSucceeded {
		sse := tg.head(t, tg.bucket, two).ServerSideEncryption
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
	if sum(tg.get(t, tg.bucket, two, "")) != sum(body) {
		t.Error("encryption rewrite damaged the object")
	}
}
