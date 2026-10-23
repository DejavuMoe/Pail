package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DejavuMoe/Pail/internal/rules"
)

func TestMirrorProtectsSkippedDirectoryDescendants(t *testing.T) {
	e := syNewEnv(t)
	target := t.TempDir()
	trWrite(t, filepath.Join(target, "keep.txt"), []byte("local source"))
	link := filepath.Join(e.dir, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	t.Cleanup(func() { os.Remove(link) })
	for _, key := range []string{"linked/keep.txt", "linked/deep/keep.txt", "linked-other/stale.txt"} {
		e.srv.PutObject("b", "sync/"+key, []byte("remote backup"), "", nil)
	}
	pv := e.preview(e.job("upload", "mirror", "size_time"))
	if pv.Deletes != 1 {
		t.Fatalf("only linked-other/stale.txt is extraneous: deletes=%d actions=%+v", pv.Deletes, pv.Actions)
	}
	trExpect(t, e.run(pv), StatusSucceeded)
	for _, key := range []string{"linked/keep.txt", "linked/deep/keep.txt"} {
		if body := trObject(t, e.trEnv, "sync/"+key); string(body) != "remote backup" {
			t.Fatalf("protected object %s changed: %q", key, body)
		}
	}
	trMissing(t, e.trEnv, "sync/linked-other/stale.txt")
}

func TestSkippedEncryptedUploadDoesNotNeedPassword(t *testing.T) {
	e := newTrEnv(t)
	local := filepath.Join(t.TempDir(), "existing.txt")
	trWrite(t, local, []byte("local"))
	e.srv.PutObject("b", "existing.txt.zip", []byte("keep existing"), "", nil)
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "zip", KeyPattern: "*.txt", Compression: &rules.Compression{Method: "zip", Level: 1, PasswordRef: "unavailable-password"}}}); err != nil {
		t.Fatal(err)
	}
	tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictSkip})
	trExpect(t, tasks, StatusSucceeded)
	uploads := trKind(tasks, KindUpload)
	if len(uploads) != 1 {
		t.Fatalf("uploads=%+v", uploads)
	}
	items := e.s.TaskItems(uploads[0].ID)
	if len(items) != 1 || items[0].Status != ItemSkipped {
		t.Fatalf("expected skipped existing archive: %+v", items)
	}
	if body := trObject(t, e, "existing.txt.zip"); body != "keep existing" {
		t.Fatalf("archive changed: %q", body)
	}
	// An upload that actually needs the password must still fail.
	tasks = e.upload(UploadRequest{Dest: e.loc("b", "new/"), Paths: []string{local}, Conflict: ConflictSkip})
	trExpect(t, trKind(tasks, KindUpload), StatusFailed)
	trMissing(t, e, "new/existing.txt.zip")
}
