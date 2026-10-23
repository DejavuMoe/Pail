package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

func TestSavedSyncJobSelectsItsAccount(t *testing.T) {
	h := newHarness(t)
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "a.txt"), "abc")
	s, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	job, err := s.SaveSyncJob(app.SyncJob{Name: "nightly", LocalDir: local, Remote: app.Location{AccountID: h.acct.ID, Bucket: "bkt", Prefix: "jobs/"}, Direction: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	a := h.acct
	a.ID, a.Name = "", "second"
	if _, err = s.SaveAccount(a, &app.Secrets{SecretAccessKey: secretKey}); err != nil {
		t.Fatal(err)
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{job.Name, job.ID} {
		h.want(h.run("sync", "--job", name, "--dry-run"), exitOK)
	}
	h.want(h.run("sync", "--job", "nightly", "--account", "second", "--dry-run"), exitUsage)
	h.want(h.run("sync", "--job", "nightly", "--account", h.acct.ID, "--dry-run"), exitOK)
	h.want(h.run("sync", "--job", "nightly"), exitOK)
	if body, ok := h.obj("bkt", "jobs/a.txt"); !ok || body != "abc" {
		t.Fatalf("saved job did not upload: exists=%v body=%q", ok, body)
	}
	// Account selection must also precede unlocking that account's vault.
	s, err = app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecretBackend(context.Background(), app.SecretBackendVault, vaultTestPW); err != nil {
		t.Fatal(err)
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := h.want(h.run("sync", "--job", "nightly", "--dry-run"), exitUsage)
	if !strings.Contains(r.errOut, "vault is locked") {
		t.Fatalf("wrong account or vault error: %s", r.errOut)
	}
	r = h.want(h.runWith(vaultTestPW+"\n", nil, "sync", "--job", "nightly", "--dry-run", "--vault-password-stdin"), exitOK)
	noSecretLeak(t, r)
}

func TestOutcomeUsesCountersWithoutRetainedItems(t *testing.T) {
	w := &watcher{recs: map[string]*rec{
		"t1": {task: app.Task{ID: "t1", GroupID: "g1", Status: app.StatusPartiallyFailed,
			ItemsDone: 20010, ItemsFailed: 5, ItemsSkipped: 10003, ItemsUnknown: 3, ItemsUnprocessed: 4}},
	}}
	got := w.collect("g1", false)
	want := outcome{Tasks: 1, Succeeded: 10002, Skipped: 10003, Failed: 2, Unknown: 3, Unprocessed: 4, PartialTasks: 1}
	if got != want {
		t.Fatalf("outcome=%+v want=%+v", got, want)
	}
}

func TestBulkSummaryBeyondItemRetention(t *testing.T) {
	h := newHarness(t)
	const n = 10001
	for i := 0; i < n; i++ {
		h.srv.PutObject("bkt", fmt.Sprintf("bulk/%05d", i), []byte("x"), "", nil)
	}
	r := h.want(h.run("rm", "s3://bkt/bulk/", "-r", "--yes", "--json"), exitOK)
	for i := 0; i < n; i++ {
		if _, ok := h.obj("bkt", fmt.Sprintf("bulk/%05d", i)); ok {
			t.Fatalf("object %d was not deleted", i)
		}
	}
	sum := lastSummary(t, r.out)
	if sum["succeeded"] != float64(n) {
		t.Fatalf("deleted %d but summary says %v", n, sum["succeeded"])
	}
}

func TestDoubleDashDownloadDestination(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())
	h.srv.PutObject("bkt", "file", []byte("downloaded"), "", nil)
	h.want(h.run("cp", "--", "s3://bkt/file", "-download"), exitOK)
	if got := readFile(t, filepath.Join("-download", "file")); got != "downloaded" {
		t.Fatalf("download = %q", got)
	}
}

func TestInterspersedEndOfOptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
		value      string
		verbose    bool
	}{
		{"sentinel first", []string{"--", "source", "-dest"}, []string{"source", "-dest"}, "", false},
		{"sentinel between paths", []string{"source", "--", "-dest", "--verbose"}, []string{"source", "-dest", "--verbose"}, "", false},
		{"interspersed", []string{"source", "--verbose", "--value", "v", "dest"}, []string{"source", "dest"}, "v", true},
		{"sentinel as value", []string{"--value", "--", "source", "--verbose", "dest"}, []string{"source", "dest"}, "--", true},
		{"assigned value", []string{"--value=--", "source", "--verbose", "--", "-dest"}, []string{"source", "-dest"}, "--", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			value := fs.String("value", "", "")
			verbose := fs.Bool("verbose", false, "")
			got, err := parseInterspersed(fs, tc.args)
			if err != nil || !reflect.DeepEqual(got, tc.want) || *value != tc.value || *verbose != tc.verbose {
				t.Fatalf("positions=%q value=%q verbose=%v err=%v", got, *value, *verbose, err)
			}
		})
	}
}
