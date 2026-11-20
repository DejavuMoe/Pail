package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

// Sync scenarios shared by TestApp* (MinIO) and TestExt* (external endpoint).

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

func syncUploadAndMirror(t *testing.T, tg *target) {
	dir := t.TempDir()
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha"))
	writeLocal(t, filepath.Join(dir, "sub", "b.bin"), payload(31, 200*1024))
	writeLocal(t, filepath.Join(dir, "sub", "deep", "c.txt"), []byte("gamma"))

	job := tg.syncJob(dir, "mirror/", "upload", "new_changed", "size_time")
	p := tg.preview(t, job)
	if p.Uploads != 3 || p.Deletes != 0 || p.Downloads != 0 {
		t.Fatalf("first preview: %s", previewSummary(p))
	}
	tg.runSync(t, p)
	for rel, want := range localFiles(t, dir) {
		if sum(tg.getk(t, "mirror/"+rel)) != want {
			t.Errorf("%s not uploaded correctly", rel)
		}
	}
	if p2 := tg.preview(t, job); p2.Uploads != 0 || p2.Deletes != 0 || p2.Downloads != 0 {
		t.Fatalf("second preview must converge: %s", previewSummary(p2))
	}

	// A changed file is uploaded again, an untouched one is not.
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha changed"))
	p = tg.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("changed-file preview: %s", previewSummary(p))
	}
	tg.runSync(t, p)
	if string(tg.getk(t, "mirror/a.txt")) != "alpha changed" {
		t.Fatal("changed file not synchronised")
	}

	// Exclusions.
	job.Excludes = []string{"*.bin"}
	writeLocal(t, filepath.Join(dir, "sub", "skipped.bin"), []byte("x"))
	if p = tg.preview(t, job); p.Uploads != 0 {
		t.Errorf("excluded file planned: %s", previewSummary(p))
	}
	job.Excludes = nil
	_ = os.Remove(filepath.Join(dir, "sub", "skipped.bin"))

	// Mirror: remote objects without a local file are deleted; objects outside the sync prefix are not.
	tg.putk(t, "outside.txt", []byte("outside"))
	tg.putk(t, "mirror/extra.txt", []byte("extra"))
	mirror := job
	mirror.Mode = "mirror"
	p = tg.preview(t, mirror)
	if p.Deletes != 1 || p.Uploads != 0 {
		t.Fatalf("mirror preview: %s", previewSummary(p))
	}
	// new_changed mode never deletes.
	if pn := tg.preview(t, job); pn.Deletes != 0 {
		t.Errorf("new_changed planned deletes: %s", previewSummary(pn))
	}
	tg.runSync(t, p)
	if tg.existsk("mirror/extra.txt") || !tg.existsk("outside.txt") || !tg.existsk("mirror/a.txt") {
		t.Fatalf("mirror result wrong: %v", tg.relKeys(t, ""))
	}
	if pf := tg.preview(t, mirror); pf.Uploads+pf.Deletes+pf.Downloads != 0 {
		t.Fatalf("mirror did not converge: %s", previewSummary(pf))
	}
}

func syncDownload(t *testing.T, tg *target) {
	files := map[string][]byte{"r/one.txt": []byte("one"), "r/dir/two.bin": payload(41, 150*1024), "r/dir/x/three.txt": []byte("three")}
	for k, v := range files {
		tg.putk(t, k, v)
	}
	dir := t.TempDir()
	job := tg.syncJob(dir, "r/", "download", "new_changed", "size_time")
	p := tg.preview(t, job)
	if p.Downloads != 3 {
		t.Fatalf("download preview: %s", previewSummary(p))
	}
	tg.runSync(t, p)
	got := localFiles(t, dir)
	for k, v := range files {
		if got[strings.TrimPrefix(k, "r/")] != sum(v) {
			t.Errorf("%s not downloaded correctly: %v", k, got)
		}
	}
	if p2 := tg.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("second download preview must converge: %s", previewSummary(p2))
	}
	// Remote change is picked up.
	tg.putk(t, "r/one.txt", []byte("one, but longer"))
	p = tg.preview(t, job)
	if p.Downloads != 1 {
		t.Fatalf("remote-change preview: %s", previewSummary(p))
	}
	tg.runSync(t, p)
	if string(readLocal(t, filepath.Join(dir, "one.txt"))) != "one, but longer" {
		t.Fatal("remote change not downloaded")
	}

	// Mirror download plans the local delete. The app's trash hook is unexported,
	// so executing it would move a file into the real OS trash of this host; the
	// plan is verified, the execution is covered by the unit tests in internal/app.
	writeLocal(t, filepath.Join(dir, "local-only.txt"), []byte("not on the server"))
	mirror := job
	mirror.Mode = "mirror"
	p = tg.preview(t, mirror)
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

func syncChecksumMode(t *testing.T, tg *target) {
	dir := t.TempDir()
	file := filepath.Join(dir, "same-size.bin")
	orig := payload(51, 64*1024)
	writeLocal(t, file, orig)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mtime := st.ModTime()

	job := tg.syncJob(dir, "ck/", "upload", "new_changed", "checksum")
	p := tg.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("checksum preview: %s", previewSummary(p))
	}
	tg.runSync(t, p)
	if p2 := tg.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("checksum round trip does not converge: %s", previewSummary(p2))
	}
	if sha := tg.head(t, tg.bucket, tg.k("ck/same-size.bin")).Metadata["pail-sha256"]; sha != sum(orig) {
		t.Errorf("remote pail-sha256 = %q, want %s", sha, sum(orig))
	}

	// Same size, same mtime, different bytes: only checksum mode notices.
	changed := payload(52, 64*1024)
	writeLocal(t, file, changed)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if pc := tg.preview(t, job); pc.Uploads != 1 {
		t.Fatalf("checksum mode missed a same-size same-mtime change: %s", previewSummary(pc))
	}
	sizeTime := job
	sizeTime.Compare = "size_time"
	if ps := tg.preview(t, sizeTime); ps.Uploads != 0 {
		t.Logf("size_time unexpectedly saw the change: %s", previewSummary(ps))
	}
	tg.runSync(t, tg.preview(t, job))
	if sum(tg.getk(t, "ck/same-size.bin")) != sum(changed) {
		t.Fatal("changed content was not uploaded")
	}
	if p3 := tg.preview(t, job); p3.Uploads != 0 {
		t.Fatalf("no convergence after checksum upload: %s", previewSummary(p3))
	}

	// Download direction in checksum mode.
	ddir := t.TempDir()
	dj := tg.syncJob(ddir, "ck/", "download", "new_changed", "checksum")
	dp := tg.preview(t, dj)
	t.Logf("checksum download preview: %s", previewSummary(dp))
	tg.runSync(t, dp)
	if got := localFiles(t, ddir)["same-size.bin"]; got != sum(changed) {
		t.Fatalf("checksum download mismatch")
	}
	if dp2 := tg.preview(t, dj); dp2.Uploads+dp2.Downloads+dp2.Deletes != 0 {
		t.Fatalf("checksum download does not converge: %s", previewSummary(dp2))
	}
}
