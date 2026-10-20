package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
)

func TestAppBucketManagement(t *testing.T) {
	a := newApp(t)
	name := fmt.Sprintf("pail-app-mgmt-%d", time.Now().UnixNano())
	if err := a.svc.CreateBucket(a.ctx, a.acct.ID, name, "", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.forceClean(t, name) })
	list, err := a.svc.Buckets(a.ctx, a.acct.ID)
	found := false
	for _, b := range list {
		if b.Name == name {
			found = !b.Created.IsZero()
		}
	}
	if err != nil || !found {
		t.Fatalf("Buckets: found=%v err=%v", found, err)
	}
	if err := a.svc.CreateBucket(a.ctx, a.acct.ID, name, "", false); err == nil || !(app.IsKind(err, app.ErrConflict) || app.IsKind(err, app.ErrOther)) {
		t.Errorf("creating an existing bucket should fail with a conflict, got %v", err)
	}

	// Versioning on, then off (suspended).
	if cfg, err := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); err != nil || cfg != "" {
		t.Errorf("fresh bucket versioning = %q, %v (want not configured)", cfg, err)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Enabled"}`); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); !strings.Contains(cfg, "Enabled") {
		t.Errorf("versioning after enable: %q", cfg)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Suspended"}`); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); !strings.Contains(cfg, "Suspended") {
		t.Errorf("versioning after suspend: %q", cfg)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Maybe"}`); !app.IsKind(err, app.ErrInvalid) {
		t.Errorf("bad versioning status must be Invalid, got %v", err)
	}

	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/public/*"]}]}`, name)
	for _, c := range []struct {
		cfg    app.BucketConfig
		body   string
		marker string
	}{
		{app.ConfigPolicy, policy, "s3:GetObject"},
		{app.ConfigCORS, `{"CORSRules":[{"AllowedMethods":["GET","PUT"],"AllowedOrigins":["https://example.test"],"AllowedHeaders":["*"],"MaxAgeSeconds":600}]}`, "https://example.test"},
		{app.ConfigLifecycle, `{"Rules":[{"ID":"expire-tmp","Status":"Enabled","Filter":{"Prefix":"tmp/"},"Expiration":{"Days":7}}]}`, "expire-tmp"},
		{app.ConfigTagging, `{"TagSet":[{"Key":"env","Value":"integration"}]}`, "integration"},
	} {
		t.Run(string(c.cfg), func(t *testing.T) {
			ctx := a.ctx
			if got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil || got != "" {
				t.Fatalf("initial Get = %q, %v (want \"\", nil)", got, err)
			}
			if err := a.svc.PutBucketConfig(ctx, a.acct.ID, name, c.cfg, c.body); err != nil {
				if app.IsKind(err, app.ErrUnsupported) {
					t.Skipf("this MinIO does not implement the %s API; the app reports it as Unsupported: %v", c.cfg, err)
				}
				t.Fatalf("Put: %v", err)
			}
			got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg)
			if err != nil || !strings.Contains(got, c.marker) {
				t.Fatalf("Get after Put = %q, %v", got, err)
			}
			if err := a.svc.DeleteBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil || got != "" {
				t.Fatalf("Get after Delete = %q, %v", got, err)
			}
		})
	}

	// Seed versions, delete markers and an unfinished multipart upload, then DeleteBucket must refuse.
	a.put(t, name, "v/a.txt", []byte("1"))
	a.put(t, name, "v/a.txt", []byte("2"))
	a.put(t, name, "v/b.txt", []byte("3"))
	if _, err := a.sdk.S3().DeleteObject(a.ctx, &s3.DeleteObjectInput{Bucket: &name, Key: aws.String("v/b.txt")}); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Enabled"}`); err != nil {
		t.Fatal(err)
	}
	a.put(t, name, "v/a.txt", []byte("3"))
	if _, err := a.sdk.S3().DeleteObject(a.ctx, &s3.DeleteObjectInput{Bucket: &name, Key: aws.String("v/a.txt")}); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.DeleteBucket(a.ctx, a.acct.ID, name); err == nil {
		t.Fatal("DeleteBucket of a non-empty bucket must fail")
	} else {
		t.Logf("DeleteBucket(non-empty): %v", err)
	}

	// EmptyBucket without allVersions only hides the objects.
	id, err := a.svc.EmptyBucket(a.acct.ID, name, false)
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if len(a.keys(t, name, "")) != 0 || len(a.versions(t, name, "")) == 0 {
		t.Errorf("EmptyBucket(allVersions=false): keys=%v versions=%d", a.keys(t, name, ""), len(a.versions(t, name, "")))
	}
	id, err = a.svc.EmptyBucket(a.acct.ID, name, true)
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if v := a.versions(t, name, ""); len(v) != 0 {
		t.Fatalf("EmptyBucket(allVersions=true) left %+v", v)
	}
	if err := a.svc.DeleteBucket(a.ctx, a.acct.ID, name); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	list, _ = a.svc.Buckets(a.ctx, a.acct.ID)
	for _, b := range list {
		if b.Name == name {
			t.Fatal("bucket still listed after DeleteBucket")
		}
	}
}

// ---- sync ---------------------------------------------------------------------------

func syncJob(a *appEnv, dir, bucket, prefix, direction, mode, compare string) app.SyncJob {
	return app.SyncJob{
		Name: "it", LocalDir: dir, Remote: a.loc(bucket, prefix), Direction: direction, Mode: mode, Compare: compare,
	}
}

func (a *appEnv) preview(t *testing.T, j app.SyncJob) app.SyncPreview {
	t.Helper()
	p, err := a.svc.PreviewSync(a.ctx, j)
	if err != nil {
		t.Fatalf("PreviewSync: %v", err)
	}
	return p
}

func (a *appEnv) runSync(t *testing.T, p app.SyncPreview) []app.ItemResult {
	t.Helper()
	g, err := a.svc.RunSync(p)
	if err != nil {
		t.Fatalf("RunSync: %v", err)
	}
	return a.groupOK(t, g)
}

func previewSummary(p app.SyncPreview) string {
	s := fmt.Sprintf("up=%d down=%d del=%d skip=%d", p.Uploads, p.Downloads, p.Deletes, p.Skips)
	for _, ac := range p.Actions {
		s += fmt.Sprintf("\n  %s %s (%s)", ac.Op, ac.Path, ac.Reason)
	}
	return s
}

func TestAppSyncUploadAndMirror(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	dir := t.TempDir()
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha"))
	writeLocal(t, filepath.Join(dir, "sub", "b.bin"), payload(31, 200*1024))
	writeLocal(t, filepath.Join(dir, "sub", "deep", "c.txt"), []byte("gamma"))

	job := syncJob(a, dir, bucket, "mirror/", "upload", "new_changed", "size_time")
	p := a.preview(t, job)
	if p.Uploads != 3 || p.Deletes != 0 || p.Downloads != 0 {
		t.Fatalf("first preview: %s", previewSummary(p))
	}
	a.runSync(t, p)
	for rel, want := range localFiles(t, dir) {
		if sum(a.get(t, bucket, "mirror/"+rel, "")) != want {
			t.Errorf("%s not uploaded correctly", rel)
		}
	}
	if p2 := a.preview(t, job); p2.Uploads != 0 || p2.Deletes != 0 || p2.Downloads != 0 {
		t.Fatalf("second preview must converge: %s", previewSummary(p2))
	}

	// A changed file is uploaded again, an untouched one is not.
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha changed"))
	p = a.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("changed-file preview: %s", previewSummary(p))
	}
	a.runSync(t, p)
	if string(a.get(t, bucket, "mirror/a.txt", "")) != "alpha changed" {
		t.Fatal("changed file not synchronised")
	}

	// Exclusions.
	job.Excludes = []string{"*.bin"}
	writeLocal(t, filepath.Join(dir, "sub", "skipped.bin"), []byte("x"))
	if p = a.preview(t, job); p.Uploads != 0 {
		t.Errorf("excluded file planned: %s", previewSummary(p))
	}
	job.Excludes = nil
	_ = os.Remove(filepath.Join(dir, "sub", "skipped.bin"))

	// Mirror: remote objects without a local file are deleted; objects outside the prefix are not.
	a.put(t, bucket, "outside.txt", []byte("outside"))
	a.put(t, bucket, "mirror/extra.txt", []byte("extra"))
	mirror := job
	mirror.Mode = "mirror"
	p = a.preview(t, mirror)
	if p.Deletes != 1 || p.Uploads != 0 {
		t.Fatalf("mirror preview: %s", previewSummary(p))
	}
	// new_changed mode never deletes.
	if pn := a.preview(t, job); pn.Deletes != 0 {
		t.Errorf("new_changed planned deletes: %s", previewSummary(pn))
	}
	a.runSync(t, p)
	if a.exists(bucket, "mirror/extra.txt") || !a.exists(bucket, "outside.txt") || !a.exists(bucket, "mirror/a.txt") {
		t.Fatalf("mirror result wrong: %v", a.keys(t, bucket, ""))
	}
	if pf := a.preview(t, mirror); pf.Uploads+pf.Deletes+pf.Downloads != 0 {
		t.Fatalf("mirror did not converge: %s", previewSummary(pf))
	}
}

func TestAppSyncDownload(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	files := map[string][]byte{"r/one.txt": []byte("one"), "r/dir/two.bin": payload(41, 150*1024), "r/dir/x/three.txt": []byte("three")}
	for k, v := range files {
		a.put(t, bucket, k, v)
	}
	dir := t.TempDir()
	job := syncJob(a, dir, bucket, "r/", "download", "new_changed", "size_time")
	p := a.preview(t, job)
	if p.Downloads != 3 {
		t.Fatalf("download preview: %s", previewSummary(p))
	}
	a.runSync(t, p)
	got := localFiles(t, dir)
	for k, v := range files {
		if got[strings.TrimPrefix(k, "r/")] != sum(v) {
			t.Errorf("%s not downloaded correctly: %v", k, got)
		}
	}
	if p2 := a.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("second download preview must converge: %s", previewSummary(p2))
	}
	// Remote change is picked up.
	a.put(t, bucket, "r/one.txt", []byte("one, but longer"))
	p = a.preview(t, job)
	if p.Downloads != 1 {
		t.Fatalf("remote-change preview: %s", previewSummary(p))
	}
	a.runSync(t, p)
	if string(readLocal(t, filepath.Join(dir, "one.txt"))) != "one, but longer" {
		t.Fatal("remote change not downloaded")
	}

	// Mirror download plans the local delete. The app's trash hook is unexported,
	// so executing it would move a file into the real OS trash of this host; the
	// plan is verified, the execution is covered by the unit tests in internal/app.
	writeLocal(t, filepath.Join(dir, "local-only.txt"), []byte("not on the server"))
	mirror := job
	mirror.Mode = "mirror"
	p = a.preview(t, mirror)
	if p.Deletes != 1 || p.Downloads != 0 {
		t.Fatalf("mirror download preview: %s", previewSummary(p))
	}
	for _, ac := range p.Actions {
		if ac.Op == "delete" && ac.Path != "local-only.txt" {
			t.Errorf("unexpected delete %q", ac.Path)
		}
	}
	t.Log("SKIPPED (by design): execution of the local mirror delete; syncTrash is unexported, so it would use the real OS trash")
}

func TestAppSyncChecksumMode(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	dir := t.TempDir()
	file := filepath.Join(dir, "same-size.bin")
	orig := payload(51, 64*1024)
	writeLocal(t, file, orig)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mtime := st.ModTime()

	job := syncJob(a, dir, bucket, "ck/", "upload", "new_changed", "checksum")
	p := a.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("checksum preview: %s", previewSummary(p))
	}
	a.runSync(t, p)
	if p2 := a.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("checksum round trip does not converge: %s", previewSummary(p2))
	}
	if sha := a.head(t, bucket, "ck/same-size.bin").Metadata["pail-sha256"]; sha != sum(orig) {
		t.Errorf("remote pail-sha256 = %q, want %s", sha, sum(orig))
	}

	// Same size, same mtime, different bytes: only checksum mode notices.
	changed := payload(52, 64*1024)
	writeLocal(t, file, changed)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if pc := a.preview(t, job); pc.Uploads != 1 {
		t.Fatalf("checksum mode missed a same-size same-mtime change: %s", previewSummary(pc))
	}
	sizeTime := job
	sizeTime.Compare = "size_time"
	if ps := a.preview(t, sizeTime); ps.Uploads != 0 {
		t.Logf("size_time unexpectedly saw the change: %s", previewSummary(ps))
	}
	a.runSync(t, a.preview(t, job))
	if sum(a.get(t, bucket, "ck/same-size.bin", "")) != sum(changed) {
		t.Fatal("changed content was not uploaded")
	}
	if p3 := a.preview(t, job); p3.Uploads != 0 {
		t.Fatalf("no convergence after checksum upload: %s", previewSummary(p3))
	}

	// Download direction in checksum mode.
	ddir := t.TempDir()
	dj := syncJob(a, ddir, bucket, "ck/", "download", "new_changed", "checksum")
	dp := a.preview(t, dj)
	t.Logf("checksum download preview: %s", previewSummary(dp))
	a.runSync(t, dp)
	if got := localFiles(t, ddir)["same-size.bin"]; got != sum(changed) {
		t.Fatalf("checksum download mismatch")
	}
	if dp2 := a.preview(t, dj); dp2.Uploads+dp2.Downloads+dp2.Deletes != 0 {
		t.Fatalf("checksum download does not converge: %s", previewSummary(dp2))
	}
}
