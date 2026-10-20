package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

const secretKey = "SUPERSECRET-KEY-0123456789"

type harness struct {
	t    *testing.T
	srv  *s3test.Server
	dir  string
	kr   app.Keyring
	acct app.Account
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := s3test.New()
	t.Cleanup(srv.Close)
	h := &harness{t: t, srv: srv, dir: t.TempDir(), kr: app.NewMemoryKeyring()}
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	h.acct, err = svc.SaveAccount(app.Account{Name: "test", Provider: app.ProviderS3, Endpoint: srv.URL, Region: "us-east-1",
		PathStyle: true, Credentials: app.CredStatic, AccessKeyID: "AKIATEST"}, &app.Secrets{SecretAccessKey: secretKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.CreateBucket("bkt", false)
	return h
}

type result struct {
	code        int
	out, errOut string
}

func (h *harness) runCtx(ctx context.Context, stdin string, tty bool, args ...string) result {
	var out, errb bytes.Buffer
	code := run(ctx, append([]string{"--config-dir", h.dir}, args...), IO{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb, StdinTTY: tty, Keyring: h.kr,
	})
	return result{code, out.String(), errb.String()}
}

func (h *harness) run(args ...string) result {
	h.t.Helper()
	return h.runCtx(context.Background(), "", false, args...)
}

func (h *harness) want(r result, code int) result {
	h.t.Helper()
	if r.code != code {
		h.t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.out, r.errOut)
	}
	return r
}

func (h *harness) obj(bucket, key string) (string, bool) {
	b, ok := h.srv.Object(bucket, key)
	return string(b), ok
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jsonLines parses every line of s as a JSON object.
func jsonLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func lastSummary(t *testing.T, s string) map[string]any {
	t.Helper()
	ls := jsonLines(t, s)
	if len(ls) == 0 || ls[len(ls)-1]["type"] != "summary" {
		t.Fatalf("no final summary in %q", s)
	}
	return ls[len(ls)-1]
}

func TestAccountsNeverPrintSecrets(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"accounts"}, {"--json", "accounts"}, {"buckets"}, {"--json", "ls", "s3://bkt/"}} {
		r := h.want(h.run(args...), 0)
		for _, s := range []string{secretKey, "SUPERSECRET", "AKIATEST"} {
			if strings.Contains(r.out, s) || strings.Contains(r.errOut, s) {
				t.Fatalf("%v leaked %q:\n%s%s", args, s, r.out, r.errOut)
			}
		}
	}
	r := h.run("accounts")
	if !strings.Contains(r.out, "test") || !strings.Contains(r.out, h.acct.ID) || !strings.Contains(r.out, h.srv.URL) {
		t.Fatalf("accounts output: %s", r.out)
	}
	ls := jsonLines(t, h.run("--json", "accounts").out)
	if ls[0]["type"] != "account" || ls[0]["name"] != "test" || lastSummary(t, h.run("--json", "accounts").out)["count"] != float64(1) {
		t.Fatalf("json accounts: %v", ls)
	}
}

func TestBucketsAndLs(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "a.txt", []byte("hello"), "text/plain", nil)
	h.srv.PutObject("bkt", "dir/b.txt", []byte("0123456789"), "text/plain", nil)
	h.srv.PutObject("bkt", "dir/sub/c.txt", []byte("x"), "text/plain", nil)

	r := h.want(h.run("buckets"), 0)
	if !strings.Contains(r.out, "bkt") {
		t.Fatalf("buckets: %s", r.out)
	}
	r = h.want(h.run("ls", "s3://bkt/"), 0)
	if !strings.Contains(r.out, "PRE dir/") || !strings.Contains(r.out, "a.txt") || !strings.Contains(r.out, "5 B") {
		t.Fatalf("ls: %s", r.out)
	}
	r = h.want(h.run("ls", "s3://bkt/dir/", "-r"), 0)
	if !strings.Contains(r.out, "sub/c.txt") || !strings.Contains(r.out, "b.txt") || strings.Contains(r.out, "PRE") {
		t.Fatalf("ls -r: %s", r.out)
	}
	r = h.want(h.run("--json", "ls", "s3://bkt/"), 0)
	ls := jsonLines(t, r.out)
	types := map[string]int{}
	for _, m := range ls {
		types[m["type"].(string)]++
	}
	if types["folder"] != 1 || types["object"] != 1 || types["summary"] != 1 {
		t.Fatalf("json ls types %v", types)
	}
	if s := lastSummary(t, r.out); s["objects"] != float64(1) || s["bytes"] != float64(5) {
		t.Fatalf("summary %v", s)
	}
	// ls of a single object key
	r = h.want(h.run("ls", "s3://bkt/a.txt"), 0)
	if !strings.Contains(r.out, "a.txt") {
		t.Fatalf("ls key: %s", r.out)
	}
	// Flags after the path and before the command both work.
	h.want(h.run("ls", "--json", "s3://bkt/"), 0)
}

func TestUploadDownloadCopy(t *testing.T) {
	h := newHarness(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "one.txt"), "one")
	writeFile(t, filepath.Join(src, "tree", "a.txt"), "aaa")
	writeFile(t, filepath.Join(src, "tree", "deep", "b.txt"), "bbb")

	// single file
	r := h.want(h.run("cp", filepath.Join(src, "one.txt"), "s3://bkt/up/"), 0)
	if !strings.Contains(r.errOut, "up/one.txt") {
		t.Fatalf("no per-item line: %s", r.errOut)
	}
	if v, ok := h.obj("bkt", "up/one.txt"); !ok || v != "one" {
		t.Fatalf("object = %q %v", v, ok)
	}
	// directory needs -r
	h.want(h.run("cp", filepath.Join(src, "tree"), "s3://bkt/up/"), 2)
	h.want(h.run("cp", filepath.Join(src, "tree"), "s3://bkt/up/", "-r"), 0)
	if v, _ := h.obj("bkt", "up/tree/deep/b.txt"); v != "bbb" {
		t.Fatalf("recursive upload missing: %q", v)
	}

	// download one object and a folder
	dl := t.TempDir()
	h.want(h.run("cp", "s3://bkt/up/one.txt", dl), 0)
	if readFile(t, filepath.Join(dl, "one.txt")) != "one" {
		t.Fatal("download content")
	}
	h.want(h.run("cp", "s3://bkt/up/tree/", dl), 2) // prefix without -r
	h.want(h.run("cp", "s3://bkt/up/tree/", dl, "-r"), 0)
	if readFile(t, filepath.Join(dl, "tree", "deep", "b.txt")) != "bbb" {
		t.Fatal("folder download content")
	}

	// s3 -> s3
	h.want(h.run("cp", "s3://bkt/up/one.txt", "s3://bkt/copy/"), 0)
	if v, _ := h.obj("bkt", "copy/one.txt"); v != "one" {
		t.Fatalf("copy = %q", v)
	}
	h.want(h.run("cp", "s3://bkt/up/tree/", "s3://bkt/copy/", "-r"), 0)
	if v, _ := h.obj("bkt", "copy/tree/a.txt"); v != "aaa" {
		t.Fatalf("recursive copy = %q", v)
	}
	if _, ok := h.obj("bkt", "up/one.txt"); !ok {
		t.Fatal("copy removed the source")
	}

	// invalid input
	h.want(h.run("cp", "a", "b"), 2)
	h.want(h.run("cp", filepath.Join(src, "one.txt"), "s3://bkt/x/", "--conflict", "bogus"), 2)
	h.want(h.run("cp", filepath.Join(src, "one.txt"), "s3://bkt/x/", "--decode", "gzip"), 2)
	h.want(h.run("cp", filepath.Join(src, "nope"), "s3://bkt/x/"), 2)
}

func TestConflictPolicies(t *testing.T) {
	h := newHarness(t)
	src := t.TempDir()
	f := filepath.Join(src, "f.txt")
	writeFile(t, f, "new content")
	h.srv.PutObject("bkt", "f.txt", []byte("old"), "text/plain", nil)

	h.want(h.run("cp", f, "s3://bkt/", "--conflict", "skip"), 0)
	if v, _ := h.obj("bkt", "f.txt"); v != "old" {
		t.Fatalf("skip overwrote: %q", v)
	}
	// overwrite is not confirmed and replaces the object
	h.want(h.run("cp", f, "s3://bkt/", "--conflict", "overwrite"), 0)
	if v, _ := h.obj("bkt", "f.txt"); v != "new content" {
		t.Fatalf("overwrite: %q", v)
	}
	h.srv.PutObject("bkt", "f.txt", []byte("zzz"), "text/plain", nil)
	h.want(h.run("cp", f, "s3://bkt/", "--conflict", "rename"), 0)
	if v, _ := h.obj("bkt", "f (1).txt"); v != "new content" {
		t.Fatalf("rename: %q", v)
	}
}

func TestDownloadDecodeGzip(t *testing.T) {
	h := newHarness(t)
	var zb bytes.Buffer
	gz := newGzip(&zb)
	gz.Write([]byte("plain text"))
	gz.Close()
	h.srv.PutObject("bkt", "z.txt", zb.Bytes(), "text/plain", nil)
	dl := t.TempDir()
	h.want(h.run("cp", "s3://bkt/z.txt", dl, "--decode", "gzip"), 0)
	if readFile(t, filepath.Join(dl, "z.txt")) != "plain text" {
		t.Fatal("not decoded")
	}
}

func TestMoveConfirmation(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "m/a.txt", []byte("a"), "text/plain", nil)

	// no terminal, no --yes: exit 2, scope printed, nothing moved
	r := h.want(h.run("mv", "s3://bkt/m/a.txt", "s3://bkt/n/"), 2)
	if !strings.Contains(r.errOut, "--yes") || !strings.Contains(r.errOut, "1 objects") || !strings.Contains(r.errOut, "test") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	if _, ok := h.obj("bkt", "n/a.txt"); ok {
		t.Fatal("moved without confirmation")
	}
	// terminal, answer no
	r = h.want(h.runCtx(context.Background(), "n\n", true, "mv", "s3://bkt/m/a.txt", "s3://bkt/n/"), 1)
	if !strings.Contains(r.errOut, "Proceed? [y/N]") || !strings.Contains(r.errOut, "aborted") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	if _, ok := h.obj("bkt", "m/a.txt"); !ok {
		t.Fatal("aborted mv removed the source")
	}
	// terminal, answer yes
	h.want(h.runCtx(context.Background(), "y\n", true, "mv", "s3://bkt/m/a.txt", "s3://bkt/n/"), 0)
	if v, ok := h.obj("bkt", "n/a.txt"); !ok || v != "a" {
		t.Fatal("not moved")
	}
	if _, ok := h.obj("bkt", "m/a.txt"); ok {
		t.Fatal("source still there")
	}
	// --yes and -r
	h.srv.PutObject("bkt", "p/x/1", []byte("1"), "", nil)
	h.srv.PutObject("bkt", "p/x/2", []byte("2"), "", nil)
	h.want(h.run("mv", "s3://bkt/p/x/", "s3://bkt/q/", "-r", "--yes"), 0)
	if v, _ := h.obj("bkt", "q/x/2"); v != "2" {
		t.Fatal("recursive move")
	}
	if _, ok := h.obj("bkt", "p/x/1"); ok {
		t.Fatal("recursive move kept source")
	}
	h.want(h.run("mv", "s3://bkt/q/", "s3://bkt/r/", "--yes"), 2) // prefix without -r
	h.want(h.run("mv", "s3://bkt/zz", "s3://bkt/r/", "--yes"), 1) // missing source
	h.want(h.run("mv", "/tmp/a", "s3://bkt/r/", "--yes"), 2)
}

func TestRm(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "d/1.txt", []byte("1"), "", nil)
	h.srv.PutObject("bkt", "d/2.txt", []byte("2"), "", nil)
	h.srv.PutObject("bkt", "keep.txt", []byte("k"), "", nil)

	h.want(h.run("rm", "s3://bkt/d/1.txt"), 2) // needs --yes
	if _, ok := h.obj("bkt", "d/1.txt"); !ok {
		t.Fatal("deleted without confirmation")
	}
	h.want(h.run("rm", "s3://bkt/d/", "--yes"), 2) // prefix without -r
	h.want(h.run("rm", "s3://bkt/d/1.txt", "--yes"), 0)
	if _, ok := h.obj("bkt", "d/1.txt"); ok {
		t.Fatal("not deleted")
	}
	r := h.want(h.run("rm", "s3://bkt/d", "-r", "--yes", "--json"), 0)
	if s := lastSummary(t, r.out); s["succeeded"] != float64(1) && s["succeeded"] != float64(2) {
		t.Fatalf("summary %v", s)
	}
	if _, ok := h.obj("bkt", "d/2.txt"); ok {
		t.Fatal("recursive delete left an object")
	}
	if _, ok := h.obj("bkt", "keep.txt"); !ok {
		t.Fatal("deleted too much")
	}
	// nothing matches: success, nothing asked
	h.want(h.run("rm", "s3://bkt/gone.txt"), 0)
	// terminal prompt
	h.want(h.runCtx(context.Background(), "no\n", true, "rm", "s3://bkt/keep.txt"), 1)
	if _, ok := h.obj("bkt", "keep.txt"); !ok {
		t.Fatal("declined rm deleted")
	}
	h.want(h.runCtx(context.Background(), "yes\n", true, "rm", "s3://bkt/keep.txt"), 0)
	if _, ok := h.obj("bkt", "keep.txt"); ok {
		t.Fatal("confirmed rm kept object")
	}
}

func TestVersionsRestoreAndPermanentDelete(t *testing.T) {
	h := newHarness(t)
	h.srv.CreateBucket("ver", true)
	h.srv.PutObject("ver", "k.txt", []byte("v1"), "", nil)
	h.srv.PutObject("ver", "k.txt", []byte("v2"), "", nil)
	vs := h.srv.Versions("ver", "k.txt")
	if len(vs) != 2 {
		t.Fatalf("versions %d", len(vs))
	}
	var oldID string
	for _, v := range vs {
		if !v.IsLatest {
			oldID = v.VersionID
		}
	}

	r := h.want(h.run("--json", "versions", "s3://ver/"), 0)
	if s := lastSummary(t, r.out); s["count"] != float64(2) {
		t.Fatalf("summary %v", s)
	}
	h.want(h.run("restore", "s3://ver/k.txt"), 2)
	h.want(h.run("restore", "s3://ver/k.txt", "--version-id", oldID), 0)
	if v, _ := h.obj("ver", "k.txt"); v != "v1" {
		t.Fatalf("restored content %q", v)
	}

	// delete -> delete marker -> restore --deleted
	h.want(h.run("rm", "s3://ver/k.txt", "--yes"), 0)
	if _, ok := h.obj("ver", "k.txt"); ok {
		t.Fatal("not deleted")
	}
	r = h.want(h.run("versions", "s3://ver/"), 0)
	if !strings.Contains(r.out, "DELETE-MARKER") {
		t.Fatalf("no marker listed: %s", r.out)
	}
	h.want(h.run("restore", "s3://ver/", "--deleted"), 2) // prefix needs -r
	h.want(h.run("restore", "s3://ver/", "--deleted", "-r"), 0)
	if _, ok := h.obj("ver", "k.txt"); !ok {
		t.Fatal("not restored")
	}

	// permanent delete of all versions
	h.want(h.run("rm", "s3://ver/k.txt", "--versions"), 2)
	h.want(h.run("rm", "s3://ver/k.txt", "--versions", "-r", "--yes"), 2)
	h.want(h.run("rm", "s3://ver/k.txt", "--versions", "--yes"), 0)
	if n := len(h.srv.Versions("ver", "k.txt")); n != 0 {
		t.Fatalf("%d versions remain", n)
	}
}

func TestMakeAndRemoveBucket(t *testing.T) {
	h := newHarness(t)
	h.want(h.run("mb", "s3://newb", "--region", "us-east-1"), 0)
	found := false
	for _, b := range h.srv.Buckets() {
		found = found || b == "newb"
	}
	if !found {
		t.Fatal("bucket not created")
	}
	h.want(h.run("mb", "s3://newb/key"), 2)
	h.srv.PutObject("newb", "x", []byte("x"), "", nil)
	h.want(h.run("rb", "s3://newb"), 2)                // confirmation
	h.want(h.run("rb", "s3://newb", "--yes"), 1)       // not empty
	h.want(h.run("rm", "s3://newb", "-r", "--yes"), 0) // empty it
	r := h.want(h.run("rb", "s3://newb", "--yes", "--json"), 0)
	if s := lastSummary(t, r.out); s["ok"] != true {
		t.Fatalf("summary %v", s)
	}
	for _, b := range h.srv.Buckets() {
		if b == "newb" {
			t.Fatal("bucket not removed")
		}
	}
}

func TestPresign(t *testing.T) {
	h := newHarness(t)
	r := h.want(h.run("presign", "s3://bkt/some key.txt", "--expires", "30m"), 0)
	u := strings.TrimSpace(r.out)
	if !strings.HasPrefix(u, h.srv.URL+"/bkt/") || !strings.Contains(u, "X-Amz-Signature=") || !strings.Contains(u, "X-Amz-Expires=1800") {
		t.Fatalf("url: %s", u)
	}
	r = h.want(h.run("--json", "presign", "s3://bkt/k", "--method", "put"), 0)
	if m := jsonLines(t, r.out)[0]; m["method"] != "PUT" || !strings.Contains(m["url"].(string), "X-Amz-Signature") {
		t.Fatalf("json: %v", m)
	}
	h.want(h.run("presign", "s3://bkt/"), 2)
	h.want(h.run("presign", "s3://bkt/k", "--method", "POST"), 2)
}

func TestSyncDryRunAndRun(t *testing.T) {
	h := newHarness(t)
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "a.txt"), "aaa")
	writeFile(t, filepath.Join(local, "sub", "b.txt"), "bbb")
	writeFile(t, filepath.Join(local, "skip.log"), "log")
	before := len(h.srv.Requests())

	r := h.want(h.run("sync", local, "s3://bkt/mirror/", "--dry-run", "--exclude", "*.log"), 0)
	if !strings.Contains(r.out, "upload") || !strings.Contains(r.out, "a.txt") || !strings.Contains(r.out, "dry run") || strings.Contains(r.out, "skip.log\n") && strings.Contains(r.out, "upload   skip.log") {
		t.Fatalf("dry-run output: %s", r.out)
	}
	for _, req := range h.srv.Requests()[before:] {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			t.Fatalf("dry run sent %s %s", req.Method, req.Path)
		}
	}
	if _, ok := h.obj("bkt", "mirror/a.txt"); ok {
		t.Fatal("dry run uploaded")
	}
	r = h.want(h.run("sync", local, "s3://bkt/mirror/", "--dry-run", "--json"), 0)
	if s := lastSummary(t, r.out); s["dryRun"] != true || s["uploads"] != float64(3) {
		t.Fatalf("summary %v", s)
	}

	h.want(h.run("sync", local, "s3://bkt/mirror/", "--exclude", "*.log"), 0)
	if v, _ := h.obj("bkt", "mirror/sub/b.txt"); v != "bbb" {
		t.Fatal("sync did not upload")
	}
	if _, ok := h.obj("bkt", "mirror/skip.log"); ok {
		t.Fatal("excluded file uploaded")
	}
	// second run: nothing to do
	r = h.want(h.run("sync", local, "s3://bkt/mirror/", "--exclude", "*.log"), 0)
	if !strings.Contains(r.errOut, "up to date") {
		t.Fatalf("second run: %s", r.errOut)
	}

	// mirror with a remote extra object needs confirmation
	h.srv.PutObject("bkt", "mirror/extra.txt", []byte("e"), "", nil)
	h.want(h.run("sync", local, "s3://bkt/mirror/", "--mode", "mirror", "--exclude", "*.log"), 2)
	if _, ok := h.obj("bkt", "mirror/extra.txt"); !ok {
		t.Fatal("deleted without confirmation")
	}
	r = h.want(h.run("sync", local, "s3://bkt/mirror/", "--mode", "mirror", "--exclude", "*.log", "--dry-run"), 0)
	if !strings.Contains(r.out, "delete") || !strings.Contains(r.out, "extra.txt") {
		t.Fatalf("mirror dry-run: %s", r.out)
	}
	if _, ok := h.obj("bkt", "mirror/extra.txt"); !ok {
		t.Fatal("dry run deleted")
	}
	r = h.want(h.run("sync", local, "s3://bkt/mirror/", "--mode", "mirror", "--exclude", "*.log", "--yes"), 0)
	if _, ok := h.obj("bkt", "mirror/extra.txt"); ok {
		t.Fatal("mirror did not delete")
	}

	// download direction
	dl := filepath.Join(t.TempDir(), "dl")
	h.want(h.run("sync", "s3://bkt/mirror/", dl), 0)
	if readFile(t, filepath.Join(dl, "sub", "b.txt")) != "bbb" {
		t.Fatal("sync download")
	}
	h.want(h.run("sync", local), 2)
	h.want(h.run("sync", local, local), 2)
}

func TestSyncSavedJob(t *testing.T) {
	h := newHarness(t)
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "j.txt"), "job")
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveSyncJob(app.SyncJob{Name: "nightly", LocalDir: local, Remote: app.Location{AccountID: h.acct.ID, Bucket: "bkt", Prefix: "jobs/"},
		Direction: "upload", Mode: "new_changed", Compare: "size_time"}); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown(context.Background())

	r := h.want(h.run("sync", "--job", "nightly", "--dry-run"), 0)
	if !strings.Contains(r.out, "j.txt") {
		t.Fatalf("job dry-run: %s", r.out)
	}
	h.want(h.run("sync", "--job", "nightly"), 0)
	if v, _ := h.obj("bkt", "jobs/j.txt"); v != "job" {
		t.Fatal("job did not run")
	}
	h.want(h.run("sync", "--job", "missing"), 2)
}

func TestExitCodes(t *testing.T) {
	h := newHarness(t)
	h.want(h.run(), 2)
	h.want(h.run("bogus"), 2)
	h.want(h.run("ls"), 0)
	h.want(h.run("ls", "bkt"), 2)
	h.want(h.run("--nope", "ls"), 2)
	h.want(h.run("ls", "--account", "nobody"), 2)
	h.want(h.run("--timeout", "-1s", "ls"), 2)
	h.want(h.run("help"), 0)

	// 1: a failing operation (nonexistent bucket)
	r := h.want(h.run("ls", "s3://missing-bucket/"), 1)
	if !strings.Contains(r.errOut, "not found") {
		t.Fatalf("kind in human words expected: %s", r.errOut)
	}
	r = h.want(h.run("--json", "ls", "s3://missing-bucket/"), 1)
	if m := jsonLines(t, r.out); m[len(m)-1]["type"] != "error" {
		t.Fatalf("json error object expected: %s", r.out)
	}
	// 1: every item fails
	h.srv.PutObject("bkt", "bad.txt", []byte("bad"), "", nil)
	h.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/bad.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no"}
		}
		return nil
	})
	dl := t.TempDir()
	r = h.want(h.run("cp", "s3://bkt/bad.txt", dl), 1)
	if !strings.Contains(r.errOut, "FAILED") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	// 3: partial failure
	h.srv.PutObject("bkt", "pf/good.txt", []byte("good"), "", nil)
	h.srv.PutObject("bkt", "pf/bad.txt", []byte("bad"), "", nil)
	r = h.want(h.run("cp", "s3://bkt/pf/", dl, "-r"), 3)
	if !strings.Contains(r.errOut, "FAILED") || !strings.Contains(r.errOut, "done") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	if readFile(t, filepath.Join(dl, "pf", "good.txt")) != "good" {
		t.Fatal("good file missing")
	}
	if _, err := os.Stat(filepath.Join(dl, "pf", "bad.txt")); err == nil {
		t.Fatal("failed download left a file")
	}
	h.srv.Inject(nil)

	// 2: several accounts and no --account
	svc, _ := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if _, err := svc.SaveAccount(app.Account{Name: "second", Provider: app.ProviderS3, Endpoint: h.srv.URL, Region: "us-east-1",
		PathStyle: true, Credentials: app.CredStatic, AccessKeyID: "AK2"}, &app.Secrets{SecretAccessKey: "s2"}); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown(context.Background())
	r = h.want(h.run("buckets"), 2)
	if !strings.Contains(r.errOut, "test") || !strings.Contains(r.errOut, "second") {
		t.Fatalf("account list missing: %s", r.errOut)
	}
	h.want(h.run("buckets", "--account", "second"), 0)
	h.want(h.run("buckets", "--account", h.acct.ID), 0)
	h.want(h.run("accounts"), 0)
}

func TestQuietSuppressesProgress(t *testing.T) {
	h := newHarness(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "q.txt"), "q")
	r := h.want(h.run("cp", filepath.Join(src, "q.txt"), "s3://bkt/", "--quiet"), 0)
	if r.errOut != "" || r.out != "" {
		t.Fatalf("quiet printed: %q %q", r.out, r.errOut)
	}
	r = h.want(h.run("cp", filepath.Join(src, "q.txt"), "s3://bkt/", "--json", "--conflict", "overwrite"), 0)
	ls := jsonLines(t, r.out)
	if ls[len(ls)-1]["type"] != "summary" || ls[0]["type"] != "item" || ls[0]["status"] != "succeeded" {
		t.Fatalf("json items: %v", ls)
	}
}

func TestInterruptCancelsAndLeavesNoPartialFiles(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "slow.bin", bytes.Repeat([]byte("x"), 4096), "", nil)
	h.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/slow.bin") {
			return &s3test.Fault{Delay: 30 * time.Second}
		}
		return nil
	})
	dl := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(700 * time.Millisecond); cancel() }()
	start := time.Now()
	r := h.runCtx(ctx, "", false, "cp", "s3://bkt/slow.bin", dl)
	if r.code != 130 {
		t.Fatalf("exit code %d, want 130\n%s", r.code, r.errOut)
	}
	if d := time.Since(start); d > 12*time.Second {
		t.Fatalf("cancel took %v", d)
	}
	ents, _ := os.ReadDir(dl)
	if len(ents) != 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("partial files left: %v", names)
	}
	if !strings.Contains(r.errOut, "interrupted") {
		t.Fatalf("stderr: %s", r.errOut)
	}
}

func TestMvAndS3CpUseSettingsConflictDefault(t *testing.T) {
	h := newHarness(t)
	// the saved default is "rename": an existing target is kept, never skipped.
	h.srv.PutObject("bkt", "src/a.txt", []byte("moved"), "text/plain", nil)
	h.srv.PutObject("bkt", "dst/a.txt", []byte("existing"), "text/plain", nil)
	h.want(h.run("mv", "s3://bkt/src/a.txt", "s3://bkt/dst/", "--yes"), 0)
	if v, _ := h.obj("bkt", "dst/a.txt"); v != "existing" {
		t.Fatalf("mv overwrote the target: %q", v)
	}
	if v, ok := h.obj("bkt", "dst/a (1).txt"); !ok || v != "moved" {
		t.Fatalf("mv did not follow the settings default (rename): %q %v", v, ok)
	}
	h.srv.PutObject("bkt", "src/b.txt", []byte("copied"), "text/plain", nil)
	h.srv.PutObject("bkt", "dst/b.txt", []byte("existing"), "text/plain", nil)
	h.want(h.run("cp", "s3://bkt/src/b.txt", "s3://bkt/dst/"), 0)
	if v, ok := h.obj("bkt", "dst/b (1).txt"); !ok || v != "copied" {
		t.Fatalf("s3 cp did not follow the settings default: %q %v", v, ok)
	}
	// an explicit flag still wins
	h.srv.PutObject("bkt", "src/c.txt", []byte("c"), "text/plain", nil)
	h.srv.PutObject("bkt", "dst/c.txt", []byte("existing"), "text/plain", nil)
	h.want(h.run("mv", "s3://bkt/src/c.txt", "s3://bkt/dst/", "--yes", "--conflict", "skip"), 0)
	if v, _ := h.obj("bkt", "dst/c.txt"); v != "existing" {
		t.Fatalf("--conflict skip ignored: %q", v)
	}
}
