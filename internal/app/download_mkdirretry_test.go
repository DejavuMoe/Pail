package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadScanMkdirFailureIsRetryable(t *testing.T) {
	for _, whole := range []bool{false, true} {
		name := "folder"
		if whole {
			name = "whole bucket"
		}
		t.Run(name, func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "x/", nil, "", nil)
			dest := t.TempDir()
			blocker := filepath.Join(dest, "x")
			trWrite(t, blocker, []byte("blocking"))
			tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"x/"}, WholeBucket: whole, DestDir: dest})
			scan := trKind(tasks, KindOther)[0]
			if scan.Status != StatusPartiallyFailed {
				t.Fatalf("mkdir must fail: %+v", scan)
			}
			if it := e.s.TaskItems(scan.ID); len(it) != 1 || it[0].Key != "x/" || it[0].Status != ItemFailed {
				t.Fatalf("failed mkdir item: %+v", it)
			}
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			id, err := e.s.Retry(scan.ID)
			if err != nil {
				t.Fatalf("retry after clearing obstruction: %v", err)
			}
			bkOK(t, e.s3Env, id)
			if fi, err := os.Stat(blocker); err != nil || !fi.IsDir() {
				t.Fatalf("retry did not create directory: %v, %v", fi, err)
			}
			if it := e.s.TaskItems(id); len(it) != 1 || it[0].Status != ItemSucceeded {
				t.Fatalf("retried mkdir item: %+v", it)
			}
		})
	}
}

func TestDownloadScanUnsafeDirectoryIsNotRetryable(t *testing.T) {
	e := newTrEnv(t)
	e.srv.PutObject("b", "../escape/", nil, "", nil)
	tasks := e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{"../escape/"}, DestDir: t.TempDir()})
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusPartiallyFailed {
		t.Fatalf("unsafe mkdir must fail: %+v", scan)
	}
	if _, err := e.s.Retry(scan.ID); !IsKind(err, ErrInvalid) {
		t.Fatalf("unsafe path must stay non-retryable: %v", err)
	}
}

func TestDownloadVersionedMkdirRetryKeepsSelection(t *testing.T) {
	e := newTrEnv(t)
	e.srv.CreateBucket("versions", true)
	version := e.srv.PutObject("versions", "x/", nil, "", nil)
	e.srv.PutObject("versions", "x/not-selected.txt", []byte("not selected"), "", nil)
	dest := t.TempDir()
	blocker := filepath.Join(dest, "x")
	trWrite(t, blocker, []byte("blocking"))
	tasks := e.download(DownloadRequest{Source: e.loc("versions", ""), Versions: []ObjectRef{{Key: "x/", VersionID: version}}, DestDir: dest})
	scan := trKind(tasks, KindOther)[0]
	if scan.Status != StatusPartiallyFailed {
		t.Fatalf("mkdir must fail: %+v", scan)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	id, err := e.s.Retry(scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	retried := wait(t, e.s, id)
	trExpect(t, trWaitGroup(t, e.s, retried.GroupID), StatusSucceeded)
	if files := trTree(t, dest); len(files) != 0 {
		t.Fatalf("retry broadened selected folder version to its children: %v", files)
	}
	if it := e.s.TaskItems(id); len(it) != 1 || it[0].VersionID != version || it[0].Status != ItemSucceeded {
		t.Fatalf("retry lost the selected version: %+v", it)
	}
}
