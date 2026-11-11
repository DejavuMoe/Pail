package app

import (
	"testing"
)

func TestSyncUploadMirrorDeletesRemoteOnlyNames(t *testing.T) {
	for _, name := range []string{"CON", "colon:name", "../remote-only"} {
		t.Run(name, func(t *testing.T) {
			e := syNewEnv(t)
			key := "sync/" + name
			e.srv.PutObject("b", key, []byte("extra"), "", nil)
			e.srv.PutObject("b", "outside", []byte("keep"), "", nil)
			pv := e.preview(e.job("upload", "mirror", "size_time"))
			syCounts(t, pv, 0, 0, 1, 0)
			trExpect(t, e.run(pv), StatusSucceeded)
			trMissing(t, e.trEnv, key)
			if trObject(t, e.trEnv, "outside") != "keep" {
				t.Fatal("unrelated object changed")
			}
			if got := trTree(t, e.dir); len(got) != 0 {
				t.Fatalf("remote deletion changed local files: %v", got)
			}
		})
	}
}

func TestSyncLocalActionsStillRejectUnsafePaths(t *testing.T) {
	e := syNewEnv(t)
	for _, op := range []string{"upload", "download", "delete"} {
		job := e.job("download", "mirror", "size")
		if op == "upload" {
			job.Direction = "upload"
		}
		pv := SyncPreview{Job: job, Actions: []SyncAction{{Op: op, Path: "../unsafe", Key: "sync/../unsafe", LocalExists: true}}}
		if _, err := e.s.RunSync(pv); !IsKind(err, ErrInvalid) {
			t.Errorf("%s accepted unsafe local path: %v", op, err)
		}
	}
	if len(e.s.Tasks()) != 0 {
		t.Fatal("invalid local actions started tasks")
	}
}
