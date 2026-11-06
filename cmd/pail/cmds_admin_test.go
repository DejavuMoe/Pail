package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
)

func TestBucketAlias(t *testing.T) {
	h := newHarness(t)
	if r := h.want(h.run("bucket", "alias", "s3://bkt"), 0); strings.TrimSpace(r.out) != "-" {
		t.Fatalf("no alias yet: %q", r.out)
	}
	r := h.want(h.run("bucket", "alias", "s3://bkt", "Photos"), 0)
	if !strings.Contains(r.out, "Photos") {
		t.Fatalf("set: %s", r.out)
	}
	if r := h.want(h.run("bucket", "alias", "s3://bkt"), 0); strings.TrimSpace(r.out) != "Photos" {
		t.Fatalf("read back: %q", r.out)
	}
	o := jsonLines(t, h.want(h.run("--json", "bucket", "alias", "s3://bkt"), 0).out)[0]
	if o["type"] != "alias" || o["alias"] != "Photos" {
		t.Fatalf("json: %v", o)
	}
	if r := h.want(h.run("account", "show"), 0); !strings.Contains(r.out, "bkt = Photos") {
		t.Fatalf("account show: %s", r.out)
	}
	// The alias is a label only: commands keep taking the real name.
	h.want(h.run("ls", "s3://bkt/"), 0)
	h.want(h.run("bucket", "alias", "s3://bkt", ""), 0) // an empty name clears
	if r := h.run("bucket", "alias", "s3://bkt"); strings.TrimSpace(r.out) != "-" {
		t.Fatalf("cleared: %q", r.out)
	}
	h.want(h.run("bucket", "alias", "s3://bkt", "Again"), 0)
	h.want(h.run("bucket", "alias", "s3://bkt", "--clear"), 0)
	if r := h.run("bucket", "alias", "s3://bkt"); strings.TrimSpace(r.out) != "-" {
		t.Fatalf("--clear: %q", r.out)
	}
	h.srv.CreateBucket("other", false)
	h.want(h.run("bucket", "alias", "s3://bkt", "Same"), 0)
	h.want(h.run("bucket", "alias", "s3://other", "Same"), 2) // aliases are unique per account
	h.want(h.run("bucket", "alias", "s3://bkt", "x\ny"), 2)
	h.want(h.run("bucket", "alias", "s3://bkt", "--clear", "Name"), 2)
	h.want(h.run("bucket", "alias", "s3://bkt/key", "Name"), 2)
	h.want(h.run("bucket", "alias"), 2)
	h.want(h.run("bucket", "bogus", "s3://bkt"), 2)
	h.want(h.run("bucket"), 2)
}

// startUpload creates an unfinished multipart upload on the test server.
func (h *harness) startUpload(bucket, key string) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/"+bucket+"/"+key+"?uploads", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		h.t.Fatalf("create upload: %s", resp.Status)
	}
}

func TestMultipartListAndAbort(t *testing.T) {
	h := newHarness(t)
	for _, k := range []string{"a/one", "a/two", "b/three"} {
		h.startUpload("bkt", k)
	}
	count := func() int {
		r := h.want(h.run("--json", "multipart", "ls", "s3://bkt"), 0)
		return int(lastSummary(t, r.out)["count"].(float64))
	}
	r := h.want(h.run("multipart", "ls", "s3://bkt"), 0)
	for _, k := range []string{"a/one", "a/two", "b/three", "STANDARD"} {
		if !strings.Contains(r.out, k) {
			t.Fatalf("ls lacks %q:\n%s", k, r.out)
		}
	}
	r = h.want(h.run("--json", "multipart", "ls", "s3://bkt/a/"), 0)
	ev := jsonLines(t, r.out)
	if len(ev) != 3 || ev[0]["type"] != "upload" || ev[0]["uploadId"] == "" || ev[0]["key"] != "a/one" {
		t.Fatalf("json ls: %v", ev)
	}

	// Aborting asks first and shows the scope.
	r = h.want(h.run("multipart", "abort", "s3://bkt"), 2)
	if !strings.Contains(r.errOut, "3 unfinished") || !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("scope: %s", r.errOut)
	}
	if count() != 3 {
		t.Fatal("a refused abort removed uploads")
	}
	// --older-than keeps recent uploads.
	r = h.want(h.run("multipart", "abort", "s3://bkt", "--older-than", "1h", "--yes"), 0)
	if !strings.Contains(r.errOut, "nothing to abort") || count() != 3 {
		t.Fatalf("recent uploads must stay: %s", r.errOut)
	}
	h.want(h.run("multipart", "abort", "s3://bkt", "--older-than", "soon", "--yes"), 2)
	h.want(h.run("multipart", "abort", "s3://bkt", "--older-than", "-5h", "--yes"), 2)
	h.want(h.run("multipart", "ls", "s3://bkt", "--older-than", "1h"), 2)

	time.Sleep(30 * time.Millisecond)
	r = h.want(h.run("multipart", "abort", "s3://bkt/a/", "--older-than", "10ms", "--yes"), 0)
	if !strings.Contains(r.errOut, "a/one") || !strings.Contains(r.errOut, "a/two") {
		t.Fatalf("abort: %s", r.errOut)
	}
	if n := count(); n != 1 {
		t.Fatalf("%d uploads left, want only b/three", n)
	}
	if r := h.want(h.run("multipart", "ls", "s3://bkt"), 0); !strings.Contains(r.out, "b/three") {
		t.Fatalf("wrong upload aborted: %s", r.out)
	}
	if s := lastSummary(t, h.want(h.run("--json", "multipart", "abort", "s3://bkt", "--yes"), 0).out); s["command"] != "multipart abort" || s["succeeded"] != float64(1) {
		t.Fatalf("json: %v", s)
	}
	if count() != 0 {
		t.Fatal("uploads remain")
	}
	h.want(h.run("multipart", "abort", "s3://bkt"), 0) // nothing to abort needs no confirmation
	h.want(h.run("multipart", "ls", "s3://missing"), 1)
	h.want(h.run("multipart"), 2)
	h.want(h.run("multipart", "ls"), 2)
	h.want(h.run("multipart", "bogus", "s3://bkt"), 2)
	h.want(h.run("multipart", "ls", "local/path"), 2)
}

func TestSyncJobs(t *testing.T) {
	h := newHarness(t)
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "j.txt"), "job")
	if r := h.want(h.run("sync", "job", "ls"), 0); r.out != "" || !strings.Contains(r.errOut, "no saved sync jobs") {
		t.Fatalf("empty: %q %q", r.out, r.errOut)
	}
	r := h.want(h.run("sync", "job", "add", "nightly", local, "s3://bkt/jobs/", "--mode", "mirror", "--exclude", "*.tmp", "--compare", "size"), 0)
	if !strings.Contains(r.out, "nightly") {
		t.Fatalf("add: %s", r.out)
	}
	dl := filepath.Join(t.TempDir(), "restore")
	writeFile(t, filepath.Join(dl, "keep"), "x")
	h.want(h.run("sync", "job", "add", "pull", "s3://bkt/jobs/", dl, "--no-rules"), 0)

	r = h.want(h.run("sync", "job", "ls"), 0)
	for _, w := range []string{"nightly", "pull", "upload", "download", "mirror", "new_changed", "s3://bkt/jobs/", local, "test"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("ls lacks %q:\n%s", w, r.out)
		}
	}
	ev := jsonLines(t, h.want(h.run("--json", "sync", "job", "ls"), 0).out)
	if len(ev) != 3 || ev[0]["type"] != "job" || ev[0]["name"] != "nightly" || ev[0]["mode"] != "mirror" || ev[0]["compare"] != "size" ||
		ev[0]["excludes"].([]any)[0] != "*.tmp" || ev[0]["account"] != "test" || ev[1]["noRules"] != true || ev[1]["direction"] != "download" {
		t.Fatalf("json: %v", ev)
	}
	// The desktop app's service sees the same jobs, and the saved job runs.
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if jobs := svc.SyncJobs(); len(jobs) != 2 || jobs[0].LocalDir != local || jobs[0].Remote.Prefix != "jobs/" || jobs[0].Remote.AccountID != h.acct.ID {
		t.Fatalf("saved jobs: %+v", jobs)
	}
	svc.Shutdown(context.Background())
	h.want(h.run("sync", "--job", "nightly"), 0)
	if v, _ := h.obj("bkt", "jobs/j.txt"); v != "job" {
		t.Fatal("saved job did not run")
	}

	// Usage errors.
	h.want(h.run("sync", "job", "add", "nightly", local, "s3://bkt/x/"), 2) // name exists
	h.want(h.run("sync", "job", "add", "x", local), 2)
	h.want(h.run("sync", "job", "add", "x", local, local), 2)
	h.want(h.run("sync", "job", "add", "x", filepath.Join(local, "nope"), "s3://bkt/x/"), 2)
	h.want(h.run("sync", "job", "add", "x", local, "s3://bkt/x/", "--mode", "weird"), 2)
	h.want(h.run("sync", "job", "add", "x", local, "s3://bkt/x/", "--dry-run"), 2)
	h.want(h.run("sync", "job", "ls", "--mode", "new"), 2)
	h.want(h.run("sync", "job", "ls", "extra"), 2)
	h.want(h.run("sync", "job", "rm"), 2)
	h.want(h.run("sync", "job", "rm", "missing", "--yes"), 2)

	// rm asks first and removes only the saved job.
	r = h.want(h.run("sync", "job", "rm", "nightly"), 2)
	if !strings.Contains(r.errOut, "nightly") || !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("refusal: %s", r.errOut)
	}
	if !strings.Contains(h.run("sync", "job", "ls").out, "nightly") {
		t.Fatal("refused rm removed the job")
	}
	rp := func(string) (string, error) { return "", nil }
	h.want(h.runWith("n\n", rp, "sync", "job", "rm", "nightly"), 1)
	h.want(h.runWith("y\n", rp, "sync", "job", "rm", "nightly"), 0)
	h.want(h.run("sync", "job", "rm", "pull", "--yes"), 0)
	if r := h.want(h.run("sync", "job", "ls"), 0); r.out != "" {
		t.Fatalf("jobs left: %s", r.out)
	}
	if v, _ := h.obj("bkt", "jobs/j.txt"); v != "job" {
		t.Fatal("rm touched the objects")
	}
	if readFile(t, filepath.Join(local, "j.txt")) != "job" {
		t.Fatal("rm touched local files")
	}
	// A local folder called "job" is still a normal sync source.
	h.want(h.run("sync", "./job", "s3://bkt/x/", "--dry-run"), 2) // missing dir: usage error, not a job command
}

func TestSettings(t *testing.T) {
	h := newHarness(t)
	r := h.want(h.run("settings", "get"), 0)
	for _, w := range []string{"theme", "upload-limit", "conflict", "rename", "secret-backend", "keyring", "vault-auto-lock-minutes"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("get lacks %q:\n%s", w, r.out)
		}
	}
	if r := h.want(h.run("settings", "get", "conflict"), 0); r.out != "rename\n" {
		t.Fatalf("get conflict: %q", r.out)
	}
	h.want(h.run("settings", "set", "conflict", "overwrite"), 0)
	if r := h.want(h.run("settings", "get", "conflict"), 0); r.out != "overwrite\n" {
		t.Fatalf("after set: %q", r.out)
	}
	// The desktop app's service reads the same file.
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Settings().Conflict != app.ConflictOverwrite {
		t.Fatalf("settings not persisted: %+v", svc.Settings())
	}
	svc.Shutdown(context.Background())

	h.want(h.run("settings", "set", "upload-limit", "4"), 0)
	h.want(h.run("settings", "set", "show-versions", "on"), 0)
	h.want(h.run("settings", "set", "vault-auto-lock-minutes", "0"), 0) // 0 is a real value
	h.want(h.run("settings", "set", "theme", "dark"), 0)
	dir := t.TempDir()
	h.want(h.run("settings", "set", "download-dir", dir), 0)
	for k, want := range map[string]string{"upload-limit": "4\n", "show-versions": "true\n", "vault-auto-lock-minutes": "0\n", "theme": "dark\n", "download-dir": dir + "\n"} {
		if r := h.want(h.run("settings", "get", k), 0); r.out != want {
			t.Fatalf("%s = %q, want %q", k, r.out, want)
		}
	}
	h.want(h.run("settings", "set", "download-dir", ""), 0)
	if r := h.run("settings", "get", "download-dir"); r.out != "\n" {
		t.Fatalf("cleared dir: %q", r.out)
	}
	if r := h.want(h.run("settings", "set", "conflict", "rename"), 0); !strings.Contains(r.out, "conflict = rename") {
		t.Fatalf("set output: %s", r.out)
	}
	// Other settings stay as they were.
	if r := h.run("settings", "get", "upload-limit"); r.out != "4\n" {
		t.Fatalf("collateral change: %q", r.out)
	}

	ev := jsonLines(t, h.want(h.run("--json", "settings", "get"), 0).out)
	found := false
	for _, e := range ev {
		if e["type"] == "setting" && e["key"] == "upload-limit" && e["value"] == float64(4) {
			found = true
		}
		if e["type"] == "setting" && e["key"] == "secret-backend" && e["readOnly"] != true {
			t.Fatalf("secret-backend must be read-only: %v", e)
		}
	}
	if !found || lastSummary(t, h.run("--json", "settings", "get").out)["command"] != "settings get" {
		t.Fatalf("json: %v", ev)
	}
	if e := jsonLines(t, h.want(h.run("--json", "settings", "get", "show-versions"), 0).out)[0]; e["value"] != true {
		t.Fatalf("typed value: %v", e)
	}

	// Usage errors, and nothing is saved by them.
	h.want(h.run("settings", "set", "upload-limit", "0"), 2) // refused by the settings checks
	h.want(h.run("settings", "set", "upload-limit", "many"), 2)
	h.want(h.run("settings", "set", "show-versions", "maybe"), 2)
	h.want(h.run("settings", "set", "conflict", "sometimes"), 2)
	h.want(h.run("settings", "set", "conflict", ""), 2)
	h.want(h.run("settings", "set", "theme", "neon"), 2)
	h.want(h.run("settings", "set", "secret-backend", "vault"), 2)
	h.want(h.run("settings", "set", "bogus", "1"), 2)
	h.want(h.run("settings", "get", "bogus"), 2)
	h.want(h.run("settings", "get", "a", "b"), 2)
	h.want(h.run("settings", "set", "theme"), 2)
	h.want(h.run("settings"), 2)
	h.want(h.run("settings", "bogus"), 2)
	if r := h.run("settings", "get", "upload-limit"); r.out != "4\n" {
		t.Fatalf("a failed set changed something: %q", r.out)
	}
}

func TestCloudFrontNeverReachesTheNetworkWithoutConfirmation(t *testing.T) {
	h := newHarness(t)
	// Only AWS accounts have CloudFront.
	r := h.want(h.run("cloudfront", "invalidate", "E123ABC", "/index.html", "--yes"), 1)
	if !strings.Contains(r.errOut, "only available for AWS accounts") {
		t.Fatalf("non-AWS: %s", r.errOut)
	}
	h.want(h.run("cloudfront", "ls"), 1)

	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAccount(app.Account{Name: "aws", Provider: app.ProviderAWS, Region: "us-east-1", Credentials: app.CredDefault}, nil); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown(context.Background())

	// The scope is shown and nothing is sent without a yes (--yes would call AWS, so it is never used here).
	r = h.want(h.run("--account", "aws", "cloudfront", "invalidate", "E123ABC", "/index.html", "/img/*"), 2)
	for _, w := range []string{"E123ABC", "/index.html", "/img/*", "Paths:  2", "--yes", "aws"} {
		if !strings.Contains(r.errOut, w) {
			t.Fatalf("scope lacks %q:\n%s", w, r.errOut)
		}
	}
	rp := func(string) (string, error) { return "", nil }
	r = h.want(h.runWith("n\n", rp, "--account", "aws", "cloudfront", "invalidate", "E123ABC", "/a"), 1)
	if !strings.Contains(r.errOut, "aborted") {
		t.Fatalf("declined: %s", r.errOut)
	}
	h.want(h.run("--account", "aws", "cloudfront", "invalidate", "E123ABC"), 2)
	h.want(h.run("--account", "aws", "cloudfront", "invalidate"), 2)
	h.want(h.run("--account", "aws", "cloudfront", "bogus"), 2)
	h.want(h.run("--account", "aws", "cloudfront"), 2)
	h.want(h.run("--account", "aws", "cloudfront", "ls", "extra"), 2)
	h.want(h.run("--account", "aws", "cloudfront", "invalidate", "E1", "/a", "--bucket", "x"), 2)
}

func TestHelpAndUsageErrors(t *testing.T) {
	h := newHarness(t)
	for _, c := range commands {
		r := h.want(h.run("help", c.name), 0)
		if !strings.Contains(r.out, "usage: pail "+strings.SplitN(c.synopsis, "\n", 2)[0]) || !strings.Contains(r.out, c.desc) {
			t.Fatalf("help %s:\n%s", c.name, r.out)
		}
		r = h.want(h.run(c.name, "--help"), 0)
		if !strings.Contains(r.out, "usage: pail "+strings.SplitN(c.synopsis, "\n", 2)[0]) {
			t.Fatalf("%s --help:\n%s", c.name, r.out)
		}
	}
	r := h.want(h.run("help", "account"), 0)
	for _, w := range []string{"pail account add NAME", "pail account rm", "--secret-stdin", "--set-secret"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("help account lacks %q:\n%s", w, r.out)
		}
	}
	if r := h.want(h.run("help", "version"), 0); !strings.Contains(r.out, "usage: pail version") {
		t.Fatal(r.out)
	}
	h.want(h.run("help", "nosuch"), 2)
	// The list of commands names every new command.
	r = h.want(h.run("help"), 0)
	for _, n := range []string{"account", "vault", "bucket", "stat", "cat", "mkdir", "du", "tags", "acl", "props", "storage-class", "multipart", "cloudfront", "settings"} {
		if !strings.Contains(r.out, "  "+n+" ") {
			t.Fatalf("help does not list %s:\n%s", n, r.out)
		}
	}

	for _, args := range [][]string{
		{"account"}, {"account", "bogus"}, {"account", "add"}, {"account", "add", "a", "b"}, {"account", "ls", "x"},
		{"account", "show", "a", "b"}, {"account", "test", "nobody"}, {"account", "edit", "nobody", "--region", "x"},
		{"vault"}, {"vault", "bogus"}, {"vault", "status", "x"}, {"vault", "status", "--new-vault-password-stdin"}, {"vault", "init", "x"},
		{"vault", "reset", "x"}, {"vault", "backend", "a", "b"},
		{"bucket"}, {"stat"}, {"cat"}, {"mkdir"}, {"du"}, {"tags"}, {"acl"}, {"props"}, {"storage-class"},
		{"multipart"}, {"cloudfront"}, {"settings"}, {"sync", "job", "add"},
	} {
		if r := h.run(args...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2\n%s", args, r.code, r.errOut)
		}
	}
	// JSON mode reports usage errors as an error object.
	r = h.want(h.run("--json", "settings", "bogus"), 2)
	if ev := jsonLines(t, r.out); ev[len(ev)-1]["type"] != "error" || ev[len(ev)-1]["exitCode"] != float64(2) {
		t.Fatalf("json: %s", r.out)
	}
}
