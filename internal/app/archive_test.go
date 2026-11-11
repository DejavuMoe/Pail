package app

import (
	"testing"
)

func TestRestoreArchived(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "cold/a.bin", "cold/deep.bin", "cold/hot.txt", "cold/busy.bin")
	e.srv.SetStorageClass("bkt", "cold/a.bin", "GLACIER")
	e.srv.SetStorageClass("bkt", "cold/deep.bin", "DEEP_ARCHIVE")
	e.srv.SetStorageClass("bkt", "cold/busy.bin", "GLACIER")
	sel := Selection{Location: e.loc("bkt", "cold/"), Keys: []string{"cold/busy.bin"}}
	id, err := e.s.RestoreArchived(sel, ArchiveRestoreOptions{Days: 3})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)

	// The whole folder, Expedited: Deep Archive fails, the running restore and
	// the STANDARD object are skipped.
	id, err = e.s.RestoreArchived(Selection{Location: e.loc("bkt", "cold/"), All: true}, ArchiveRestoreOptions{Days: 3, Tier: TierExpedited})
	if err != nil {
		t.Fatal(err)
	}
	bkWait(t, e, id)
	got := map[string]string{}
	for _, it := range e.s.TaskItems(id) {
		got[it.Key] = it.Status
	}
	want := map[string]string{"cold/a.bin": ItemSucceeded, "cold/deep.bin": ItemFailed, "cold/hot.txt": ItemSkipped, "cold/busy.bin": ItemSkipped}
	for k, st := range want {
		if got[k] != st {
			t.Errorf("%s: %q, want %q (all %v)", k, got[k], st, got)
		}
	}
	info, err := e.s.Head(testCtx(t), e.loc("bkt", ""), "cold/a.bin", "")
	if err != nil {
		t.Fatal(err)
	}
	if rs, ok := ParseRestore(info.Restore); !ok || !rs.InProgress {
		t.Fatalf("restore header %q", info.Restore)
	}
	e.srv.FinishRestore("bkt", "cold/a.bin")
	info, _ = e.s.Head(testCtx(t), e.loc("bkt", ""), "cold/a.bin", "")
	if rs, ok := ParseRestore(info.Restore); !ok || rs.InProgress || rs.Expiry == "" {
		t.Fatalf("finished restore %q -> %+v", info.Restore, rs)
	}

	for _, bad := range []ArchiveRestoreOptions{{Days: 0}, {Days: MaxRestoreDays + 1}, {Days: 1, Tier: "Fast"}} {
		if _, err := e.s.RestoreArchived(sel, bad); !IsKind(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
}
