package integration_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

// TestAppCopyMoveRename runs the shared copy/move/rename scenario with the move
// going to a second bucket, then adds what only test-owned buckets allow: a copy
// into a versioned bucket and a move inside it that keeps the history.
func TestAppCopyMoveRename(t *testing.T) {
	a := newApp(t)
	plain, other, ver := a.target(t, false), a.target(t, false), a.target(t, true)
	files := copyMoveRename(t, plain, other, 11*1024*1024+5)

	// Across buckets, unversioned -> versioned.
	id, err := a.svc.Copy(plain.pick("", "src/"), ver.at("x/"), app.CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	ver.checkTree(t, "x/", files)

	// Move inside a versioned bucket: the source gets a delete marker, history stays.
	before := a.versions(t, ver.bucket, "x/")
	id, err = a.svc.Copy(ver.pick("", "x/"), ver.at("y/"), app.CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	ver.checkTree(t, "y/x/", files)
	after := a.versions(t, ver.bucket, "x/")
	if len(after) != 2*len(before) {
		t.Fatalf("versioned move: %d version entries before, %d after (want each source to keep its version and gain a marker)", len(before), len(after))
	}
	for _, v := range after {
		if v.Marker != v.Latest {
			t.Errorf("%s: marker=%v latest=%v; the delete marker must be the newest entry", v.Key, v.Marker, v.Latest)
		}
	}
	if left := ver.relKeys(t, "x/"); len(left) != 0 {
		t.Errorf("source keys still visible in the current listing: %v", left)
	}
	// History is readable.
	for _, v := range before {
		if got := a.get(t, ver.bucket, v.Key, v.ID); sum(got) != sum(files[strings.TrimPrefix(v.Key, "x/")]) {
			t.Errorf("history of %s lost", v.Key)
		}
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
			propertiesAndRewrites(t, a.target(t, versioned), versioned)
		})
	}
}
