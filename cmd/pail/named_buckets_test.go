package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// restrictKey makes the server behave like a key whose policy allows only the
// folder dir/ of the bucket data (s3:ListBucket on that prefix, object actions
// below it): every other request, ListBuckets included, is denied.
func (h *harness) restrictKey() {
	h.srv.CreateBucket("data", false)
	h.srv.PutObject("data", "dir/a.txt", []byte("a"), "text/plain", nil)
	h.srv.Inject(func(r *http.Request) *s3test.Fault {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/data" && q.Get("list-type") == "2" && strings.HasPrefix(q.Get("prefix"), "dir/"):
			return nil
		case strings.HasPrefix(r.URL.Path, "/data/dir/") && r.URL.RawQuery == "":
			return nil
		}
		return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
	})
}

func TestAccountNamedBuckets(t *testing.T) {
	h := newHarness(t)
	h.restrictKey()
	add := func(extra ...string) result {
		args := append([]string{"account", "add", "restricted", "--endpoint", h.srv.URL, "--region", "us-east-1", "--path-style",
			"--access-key-id", "AKIARESTRICTED", "--secret-stdin"}, extra...)
		return h.runCtx(t.Context(), newSecret+"\n", false, args...)
	}
	// The key cannot list buckets: the connection test refuses the account.
	r := h.want(add(), 1)
	if !strings.Contains(r.errOut, "AccessDenied") || !strings.Contains(r.errOut, "--bucket") {
		t.Fatalf("add without named buckets:\n%s", r.errOut)
	}
	// A folder it may not list fails the test too, naming the bucket.
	r = h.want(add("--bucket", "data"), 1)
	if !strings.Contains(r.errOut, "data: Access Denied") {
		t.Fatalf("add with the bucket root:\n%s", r.errOut)
	}
	// Named with its folder, the account is tested on that folder and saved.
	r = h.want(add("--bucket", "data/dir"), 0)
	if !strings.Contains(r.out, "added account restricted") {
		t.Fatalf("add: %s", r.out)
	}

	r = h.want(h.run("--account", "restricted", "buckets"), 0)
	if !strings.Contains(r.out, "data/dir/") {
		t.Fatalf("buckets: %s", r.out)
	}
	ev := jsonLines(t, h.want(h.run("--json", "--account", "restricted", "buckets"), 0).out)
	if ev[0]["type"] != "bucket" || ev[0]["name"] != "data" || ev[0]["prefix"] != "dir/" {
		t.Fatalf("buckets json: %v", ev)
	}
	r = h.want(h.run("--account", "restricted", "ls"), 0)
	if !strings.Contains(r.out, "data/dir/") {
		t.Fatalf("ls: %s", r.out)
	}
	r = h.want(h.run("--account", "restricted", "ls", "s3://data/dir/"), 0)
	if !strings.Contains(r.out, "a.txt") {
		t.Fatalf("ls dir: %s", r.out)
	}

	r = h.want(h.run("account", "show", "restricted"), 0)
	if !strings.Contains(r.out, "buckets:") || !strings.Contains(r.out, "data/dir/") {
		t.Fatalf("show: %s", r.out)
	}
	o := jsonLines(t, h.want(h.run("--json", "account", "show", "restricted"), 0).out)[0]
	bs, _ := o["buckets"].([]any)
	if len(bs) != 1 || bs[0].(map[string]any)["name"] != "data" || bs[0].(map[string]any)["prefix"] != "dir/" {
		t.Fatalf("show json buckets: %v", o["buckets"])
	}
	r = h.want(h.run("account", "test", "restricted"), 0)
	if !strings.Contains(r.out, "ok") || !strings.Contains(r.out, "data/dir/") {
		t.Fatalf("test: %s", r.out)
	}

	// edit: a bucket named twice or badly is refused; an empty value clears the list.
	h.want(h.run("account", "edit", "restricted", "--bucket", "data/a", "--bucket", "data/b"), 2)
	h.want(h.run("account", "edit", "restricted", "--bucket", "Data"), 2)
	h.want(h.run("account", "edit", "restricted", "--bucket", "other", "--bucket", "data/dir/"), 0)
	o = jsonLines(t, h.want(h.run("--json", "account", "show", "restricted"), 0).out)[0]
	if bs, _ := o["buckets"].([]any); len(bs) != 2 || bs[0].(map[string]any)["name"] != "other" {
		t.Fatalf("edited buckets: %v", o["buckets"])
	}
	h.want(h.run("account", "edit", "restricted", "--bucket", ""), 0)
	o = jsonLines(t, h.want(h.run("--json", "account", "show", "restricted"), 0).out)[0]
	if o["buckets"] != nil {
		t.Fatalf("cleared buckets: %v", o["buckets"])
	}
	r = h.want(h.run("--account", "restricted", "buckets"), 1)
	if !strings.Contains(r.errOut, "AccessDenied") {
		t.Fatalf("buckets after clearing: %s", r.errOut)
	}
}
