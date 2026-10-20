package integration_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/app"
)

func (a *appEnv) seedTree(t *testing.T, bucket string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"src/a.txt":       []byte("alpha"),
		"src/d/b+c d.txt": []byte("beta"),
		"src/d/e/big.bin": payload(11, 11*1024*1024+5),
		"src/zh/文件 1.txt": []byte("unicode"),
	}
	for k, v := range files {
		a.put(t, bucket, k, v)
	}
	return files
}

func (a *appEnv) checkTree(t *testing.T, bucket, prefix string, files map[string][]byte) {
	t.Helper()
	for k, v := range files {
		dk := prefix + k
		got, err := a.tryGet(bucket, dk, "")
		if err != nil || sum(got) != sum(v) {
			t.Errorf("%s/%s: missing or wrong content (%v)", bucket, dk, err)
		}
	}
}

func TestAppCopyMoveRename(t *testing.T) {
	a := newApp(t)
	plain := a.bucket(t, false)
	other := a.bucket(t, false)
	ver := a.bucket(t, true)
	files := a.seedTree(t, plain)

	// Copy a folder inside the bucket: it keeps its own name below the destination.
	id, err := a.svc.Copy(a.sel(plain, "", "src/"), a.loc(plain, "cp/"), app.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	a.checkTree(t, plain, "cp/", files)
	// Copying again with the default policy (skip) skips everything.
	id, _ = a.svc.Copy(a.sel(plain, "", "src/"), a.loc(plain, "cp/"), app.CopyOptions{})
	if items := a.taskOK(t, id); countStatus(items, app.ItemSkipped) != len(files) {
		t.Errorf("second copy should skip all %d objects:%s", len(files), describeTasks(nil, items))
	}
	// Copy / move into folder itself is rejected.
	if _, err := a.svc.Copy(a.sel(plain, "", "src/"), a.loc(plain, "src/inner/"), app.CopyOptions{}); err == nil {
		t.Error("copying a folder into itself must be rejected")
	}

	// Across buckets, unversioned -> versioned.
	id, _ = a.svc.Copy(a.sel(plain, "", "src/"), a.loc(ver, "x/"), app.CopyOptions{})
	a.taskOK(t, id)
	a.checkTree(t, ver, "x/", files)

	// Move across buckets (unversioned source loses the keys).
	id, err = a.svc.Copy(a.sel(plain, "", "cp/"), a.loc(other, ""), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	a.checkTree(t, other, "cp/", files)
	if left := a.keys(t, plain, "cp/"); len(left) != 0 {
		t.Errorf("unversioned move left source keys: %v", left)
	}

	// Move inside a versioned bucket: the source gets a delete marker, history stays.
	before := a.versions(t, ver, "x/")
	id, err = a.svc.Copy(a.sel(ver, "", "x/"), a.loc(ver, "y/"), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	a.checkTree(t, ver, "y/x/", files)
	after := a.versions(t, ver, "x/")
	if len(after) != 2*len(before) {
		t.Fatalf("versioned move: %d version entries before, %d after (want each source to keep its version and gain a marker)", len(before), len(after))
	}
	for _, v := range after {
		if v.Marker != v.Latest {
			t.Errorf("%s: marker=%v latest=%v; the delete marker must be the newest entry", v.Key, v.Marker, v.Latest)
		}
	}
	if len(a.keys(t, ver, "x/")) != 0 {
		t.Errorf("source keys still visible in the current listing: %v", a.keys(t, ver, "x/"))
	}
	// History is readable.
	for _, v := range before {
		if got := a.get(t, ver, v.Key, v.ID); sum(got) != sum(files[strings.TrimPrefix(v.Key, "x/")]) {
			t.Errorf("history of %s lost", v.Key)
		}
	}

	// Rename an object and a folder.
	id, err = a.svc.Rename(a.ctx, a.loc(other, "cp/src/"), "cp/src/a.txt", "a2.txt")
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if a.exists(other, "cp/src/a.txt") || string(a.get(t, other, "cp/src/a2.txt", "")) != "alpha" {
		t.Error("object rename failed")
	}
	id, err = a.svc.Rename(a.ctx, a.loc(other, "cp/"), "cp/src/", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	renamed := map[string][]byte{}
	for k, v := range files {
		if k == "src/a.txt" {
			k, v = "src/a2.txt", []byte("alpha")
		}
		renamed["renamed/"+strings.TrimPrefix(k, "src/")] = v
	}
	a.checkTree(t, other, "cp/", renamed)
	if left := a.keys(t, other, "cp/src/"); len(left) != 0 {
		t.Errorf("folder rename left old keys: %v", left)
	}
	// Renaming onto an existing name fails and overwrites nothing.
	a.put(t, other, "cp/renamed/taken.txt", []byte("keep me"))
	id, _ = a.svc.Rename(a.ctx, a.loc(other, "cp/renamed/"), "cp/renamed/a2.txt", "taken.txt")
	tk, items := a.waitTask(t, id)
	if tk.Status == app.StatusSucceeded || string(a.get(t, other, "cp/renamed/taken.txt", "")) != "keep me" {
		t.Errorf("rename onto an existing key must fail without overwriting:%s", describeTasks([]app.Task{tk}, items))
	}
}

func TestAppDeleteMoreThanThousand(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	const n = 1105
	a.putMany(t, bucket, "many/", n)
	a.put(t, bucket, "keep/me.txt", []byte("keep"))
	est, err := a.svc.Estimate(a.ctx, a.sel(bucket, "", "many/"))
	if err != nil || est.Objects != n || est.Bytes != n {
		t.Fatalf("Estimate = %+v, %v (want %d objects, %d bytes)", est, err, n, n)
	}
	id, err := a.svc.Delete(a.sel(bucket, "", "many/"))
	if err != nil {
		t.Fatal(err)
	}
	items := a.taskOK(t, id)
	if got := countStatus(items, app.ItemSucceeded); got != n {
		t.Errorf("%d succeeded items, want %d", got, n)
	}
	if left := a.keys(t, bucket, ""); len(left) != 1 || left[0] != "keep/me.txt" {
		t.Fatalf("after delete: %d keys left, first few %v", len(left), left[:min(len(left), 5)])
	}
}

func TestAppVersionedLifecycle(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, true)
	v1 := a.put(t, bucket, "doc.txt", []byte("one"))
	v2 := a.put(t, bucket, "doc.txt", []byte("two"))
	v3 := a.put(t, bucket, "doc.txt", []byte("three"))
	a.put(t, bucket, "other.txt", []byte("other"))

	page, err := a.svc.ListVersions(a.ctx, a.loc(bucket, ""), "", "")
	if err != nil || len(page.Versions) != 4 {
		t.Fatalf("ListVersions = %+v, %v", page, err)
	}

	// RestoreVersion: v1 becomes the newest version; history is kept (4 -> 5 versions).
	if err := a.svc.RestoreVersion(a.ctx, a.loc(bucket, ""), app.ObjectRef{Key: "doc.txt", VersionID: v1}); err != nil {
		t.Fatal(err)
	}
	if string(a.get(t, bucket, "doc.txt", "")) != "one" || len(a.versions(t, bucket, "doc.txt")) != 4 {
		t.Fatalf("RestoreVersion: current %q, versions %v", a.get(t, bucket, "doc.txt", ""), a.versions(t, bucket, "doc.txt"))
	}
	if string(a.get(t, bucket, "doc.txt", v3)) != "three" {
		t.Error("restore destroyed history")
	}
	if err := a.svc.RestoreVersion(a.ctx, a.loc(bucket, ""), app.ObjectRef{Key: "doc.txt", VersionID: v1}); err == nil {
		// v1 is no longer current (the restored copy is), so this one is legal; only log.
		t.Log("second restore of v1 created another version")
	}

	// Delete (current objects) -> delete markers; RestoreDeleted brings them back.
	id, err := a.svc.Delete(a.sel(bucket, "", "doc.txt", "other.txt"))
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if len(a.keys(t, bucket, "")) != 0 {
		t.Fatal("delete should hide both keys")
	}
	id, err = a.svc.RestoreDeleted(a.sel(bucket, "", "doc.txt", "other.txt"))
	if err != nil {
		t.Fatal(err)
	}
	items := a.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 2 {
		t.Fatalf("RestoreDeleted items:%s", describeTasks(nil, items))
	}
	if string(a.get(t, bucket, "other.txt", "")) != "other" || a.get(t, bucket, "doc.txt", "") == nil {
		t.Fatal("restored content wrong")
	}
	// A key that is not deleted is reported as skipped.
	id, _ = a.svc.RestoreDeleted(a.sel(bucket, "", "other.txt"))
	if items := a.taskOK(t, id); countStatus(items, app.ItemSkipped) != 1 {
		t.Errorf("restoring a live key should skip:%s", describeTasks(nil, items))
	}

	// DeleteVersions: permanently remove v2 and a marker.
	vs := a.versions(t, bucket, "doc.txt")
	var markerID string
	for _, v := range vs {
		if v.Marker {
			markerID = v.ID
		}
	}
	refs := []app.ObjectRef{{Key: "doc.txt", VersionID: v2}}
	if markerID != "" {
		refs = append(refs, app.ObjectRef{Key: "doc.txt", VersionID: markerID})
	}
	id, err = a.svc.DeleteVersions(a.loc(bucket, ""), refs)
	if err != nil {
		t.Fatal(err)
	}
	items = a.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != len(refs) {
		t.Fatalf("DeleteVersions items:%s", describeTasks(nil, items))
	}
	for _, v := range a.versions(t, bucket, "doc.txt") {
		if v.ID == v2 || v.ID == markerID {
			t.Errorf("version %s still present", v.ID)
		}
	}
	if _, err := a.tryGet(bucket, "doc.txt", v2); err == nil {
		t.Error("deleted version is still readable")
	}
	if string(a.get(t, bucket, "doc.txt", v1)) != "one" || string(a.get(t, bucket, "doc.txt", v3)) != "three" {
		t.Error("other versions were damaged")
	}
	// Delete of a version through the version-aware download.
	d := t.TempDir()
	g, err := a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, ""), Versions: []app.ObjectRef{{Key: "doc.txt", VersionID: v3}}, DestDir: d})
	if err != nil {
		t.Fatal(err)
	}
	a.groupOK(t, g)
	found := false
	for p, s := range localFiles(t, d) {
		if s == sum([]byte("three")) && strings.Contains(p, "(v") {
			found = true
		}
	}
	if !found {
		t.Errorf("version download did not produce a (v<id>) file: %v", localFiles(t, d))
	}
}

func TestAppRewrites(t *testing.T) {
	a := newApp(t)
	for _, versioned := range []bool{false, true} {
		t.Run(fmt.Sprintf("versioned=%v", versioned), func(t *testing.T) {
			bucket := a.bucket(t, versioned)
			body := payload(21, 123456)
			put := func(key string) {
				_, err := a.sdk.S3().PutObject(a.ctx, &s3.PutObjectInput{
					Bucket: &bucket, Key: &key, Body: strings.NewReader(string(body)),
					ContentType: aws.String("text/plain"), CacheControl: aws.String("max-age=60"), ContentLanguage: aws.String("en"),
					Metadata: map[string]string{"team": "blue", "keep": "yes"},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			put("rw/one.txt")
			put("rw/two.txt")
			loc := a.loc(bucket, "")

			if err := a.svc.SetTags(a.ctx, loc, app.ObjectRef{Key: "rw/one.txt"}, map[string]string{"env": "test", "n": "1"}); err != nil {
				t.Fatalf("SetTags: %v", err)
			}
			info, err := a.svc.Head(a.ctx, loc, "rw/one.txt", "")
			if err != nil || info.Tags["env"] != "test" || info.Tags["n"] != "1" {
				t.Fatalf("Head tags = %v (%v / %v)", info.Tags, info.TagsErr, err)
			}
			verBefore := len(a.versions(t, bucket, "rw/one.txt"))

			// SetProperties keeps every untouched header, metadata entry and the tags.
			id, err := a.svc.SetProperties(a.sel(bucket, "", "rw/one.txt"), app.PropertiesPatch{
				SetHeaders:  map[string]string{"Content-Disposition": `attachment; filename="x.txt"`},
				SetMetadata: map[string]string{"added": "1"},
				DelMetadata: []string{"team"},
			})
			if err != nil {
				t.Fatal(err)
			}
			items := a.taskOK(t, id)
			if countStatus(items, app.ItemSucceeded) != 1 {
				t.Fatalf("SetProperties items:%s", describeTasks(nil, items))
			}
			h := a.head(t, bucket, "rw/one.txt")
			if aws.ToString(h.ContentType) != "text/plain" || aws.ToString(h.CacheControl) != "max-age=60" ||
				aws.ToString(h.ContentLanguage) != "en" || aws.ToString(h.ContentDisposition) != `attachment; filename="x.txt"` {
				t.Errorf("headers after SetProperties: type=%q cache=%q lang=%q disp=%q", aws.ToString(h.ContentType),
					aws.ToString(h.CacheControl), aws.ToString(h.ContentLanguage), aws.ToString(h.ContentDisposition))
			}
			if h.Metadata["keep"] != "yes" || h.Metadata["added"] != "1" || h.Metadata["team"] != "" {
				t.Errorf("metadata after SetProperties: %v", h.Metadata)
			}
			if sum(a.get(t, bucket, "rw/one.txt", "")) != sum(body) {
				t.Error("content changed by SetProperties")
			}
			tags, err := a.sdk.S3().GetObjectTagging(a.ctx, &s3.GetObjectTaggingInput{Bucket: &bucket, Key: aws.String("rw/one.txt")})
			if err != nil || len(tags.TagSet) != 2 {
				t.Errorf("tags lost by SetProperties: %+v %v", tags, err)
			}
			if versioned {
				if n := len(a.versions(t, bucket, "rw/one.txt")); n != verBefore+1 {
					t.Errorf("versioned rewrite should add one version: %d -> %d", verBefore, n)
				}
			}

			// SetTags replaces the set.
			if err := a.svc.SetTags(a.ctx, loc, app.ObjectRef{Key: "rw/one.txt"}, map[string]string{"only": "this"}); err != nil {
				t.Fatal(err)
			}
			tags, _ = a.sdk.S3().GetObjectTagging(a.ctx, &s3.GetObjectTaggingInput{Bucket: &bucket, Key: aws.String("rw/one.txt")})
			if len(tags.TagSet) != 1 || aws.ToString(tags.TagSet[0].Key) != "only" {
				t.Errorf("SetTags result: %+v", tags.TagSet)
			}

			// SetStorageClass on a folder.
			id, err = a.svc.SetStorageClass(a.sel(bucket, "", "rw/"), "REDUCED_REDUNDANCY")
			if err != nil {
				t.Fatal(err)
			}
			tk, items := a.waitTask(t, id)
			if tk.Status != app.StatusSucceeded {
				t.Logf("SetStorageClass REDUCED_REDUNDANCY not accepted by this server:%s", describeTasks([]app.Task{tk}, items))
				t.Errorf("SetStorageClass failed")
			} else {
				for _, k := range []string{"rw/one.txt", "rw/two.txt"} {
					if c := a.head(t, bucket, k).StorageClass; c != types.StorageClassReducedRedundancy {
						t.Errorf("%s storage class %q", k, c)
					}
				}
				// Unchanged data and properties after the class rewrite.
				h = a.head(t, bucket, "rw/one.txt")
				if h.Metadata["keep"] != "yes" || aws.ToString(h.CacheControl) != "max-age=60" {
					t.Errorf("SetStorageClass dropped properties: %v %q", h.Metadata, aws.ToString(h.CacheControl))
				}
			}
			// Back to STANDARD; already-STANDARD objects are skipped.
			id, _ = a.svc.SetStorageClass(a.sel(bucket, "", "rw/two.txt"), "STANDARD")
			a.taskOK(t, id)
			if c := a.head(t, bucket, "rw/two.txt").StorageClass; c != "" && c != types.StorageClassStandard {
				t.Errorf("STANDARD rewrite left class %q", c)
			}
			id, _ = a.svc.SetStorageClass(a.sel(bucket, "", "rw/two.txt"), "STANDARD")
			if items := a.taskOK(t, id); countStatus(items, app.ItemSkipped) != 1 {
				t.Errorf("second STANDARD rewrite should skip:%s", describeTasks(nil, items))
			}

			// Encryption AES256: MinIO needs a KMS/auto-encryption setup for SSE-S3.
			id, err = a.svc.SetEncryption(a.sel(bucket, "", "rw/two.txt"), app.Encryption{Mode: "AES256"})
			if err != nil {
				t.Fatal(err)
			}
			tk, items = a.waitTask(t, id)
			if tk.Status == app.StatusSucceeded {
				if sse := a.head(t, bucket, "rw/two.txt").ServerSideEncryption; sse != types.ServerSideEncryptionAes256 {
					t.Errorf("SetEncryption reported success but SSE is %q", sse)
				}
				t.Logf("SetEncryption AES256 supported by this MinIO")
			} else {
				if len(items) == 0 || items[0].Err == "" {
					t.Errorf("SetEncryption failure without a clear error:%s", describeTasks([]app.Task{tk}, items))
				}
				if sum(a.get(t, bucket, "rw/two.txt", "")) != sum(body) {
					t.Error("failed encryption rewrite damaged the object")
				}
				t.Logf("SetEncryption AES256 refused (MinIO has no KMS configured): %s", describeTasks([]app.Task{tk}, items))
			}
		})
	}
}
