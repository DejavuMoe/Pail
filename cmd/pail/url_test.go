package main

import (
	"context"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

// setCDN stores CDN settings on the harness account, as the desktop app would.
func (h *harness) setCDN(cdn string, buckets ...string) {
	h.t.Helper()
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		h.t.Fatal(err)
	}
	defer svc.Shutdown(context.Background())
	a := svc.Accounts()[0]
	a.CDNURL, a.CDNBuckets = cdn, buckets
	if _, err := svc.SaveAccount(a, nil); err != nil {
		h.t.Fatal(err)
	}
}

func TestURLDefaultsToCDNWhenConfigured(t *testing.T) {
	h := newHarness(t)
	public := h.srv.URL + "/bkt/dir/some%20key%2B.txt"

	// No CDN: the public address, and --kind cdn fails.
	r := h.want(h.run("url", "s3://bkt/dir/some key+.txt"), 0)
	if got := strings.TrimSpace(r.out); got != public {
		t.Fatalf("public = %q, want %q", got, public)
	}
	r = h.want(h.run("url", "s3://bkt/k", "--kind", "cdn"), 1)
	if !strings.Contains(r.errOut, "no CDN URL is configured") || r.out != "" {
		t.Fatalf("stdout %q stderr %q", r.out, r.errOut)
	}

	h.setCDN("https://s3-cdn.example.com/")
	cdn := "https://s3-cdn.example.com/bkt/dir/some%20key%2B.txt"
	for _, args := range [][]string{
		{"url", "s3://bkt/dir/some key+.txt"},
		{"url", "s3://bkt/dir/some key+.txt", "--kind", "cdn"},
		{"url", "s3://bkt/dir/some key+.txt", "--kind", "CDN"},
	} {
		r = h.want(h.run(args...), 0)
		if got := strings.TrimSpace(r.out); got != cdn {
			t.Errorf("%v = %q, want %q", args, got, cdn)
		}
	}
	r = h.want(h.run("url", "s3://bkt/dir/some key+.txt", "--kind", "public"), 0)
	if got := strings.TrimSpace(r.out); got != public {
		t.Errorf("--kind public = %q, want %q", got, public)
	}
}

func TestURLJSON(t *testing.T) {
	h := newHarness(t)
	h.setCDN("https://{bucket}.cdn.example.com/{key}")
	r := h.want(h.run("--json", "url", "s3://bkt/a/b.png"), 0)
	ls := jsonLines(t, r.out)
	if len(ls) != 2 || ls[0]["type"] != "url" || ls[0]["kind"] != "cdn" || ls[0]["url"] != "https://bkt.cdn.example.com/a/b.png" {
		t.Fatalf("json = %v", ls)
	}
	if s := lastSummary(t, r.out); s["command"] != "url" || s["ok"] != true {
		t.Fatalf("summary = %v", s)
	}
	r = h.want(h.run("url", "s3://bkt/a/b.png", "--json", "--kind", "public"), 0)
	if m := jsonLines(t, r.out)[0]; m["kind"] != "public" || m["url"] != h.srv.URL+"/bkt/a/b.png" {
		t.Fatalf("public json = %v", m)
	}
	// The error object for a missing CDN.
	h.setCDN("")
	r = h.want(h.run("--json", "url", "s3://bkt/k", "--kind", "cdn"), 1)
	if m := jsonLines(t, r.out); len(m) != 1 || m[0]["type"] != "error" {
		t.Fatalf("error json = %v", m)
	}
}

func TestURLBucketFilter(t *testing.T) {
	h := newHarness(t)
	h.srv.CreateBucket("other", false)
	h.setCDN("https://cdn.example.com", "bkt")
	r := h.want(h.run("url", "s3://bkt/k"), 0)
	if got := strings.TrimSpace(r.out); got != "https://cdn.example.com/bkt/k" {
		t.Fatalf("listed bucket: %q", got)
	}
	// A bucket the CDN does not serve falls back to the public address by
	// default and fails when the CDN is demanded.
	r = h.want(h.run("url", "s3://other/k"), 0)
	if got := strings.TrimSpace(r.out); got != h.srv.URL+"/other/k" {
		t.Fatalf("filtered bucket: %q", got)
	}
	h.want(h.run("url", "s3://other/k", "--kind", "cdn"), 1)
}

func TestURLUsageErrors(t *testing.T) {
	h := newHarness(t)
	h.setCDN("https://cdn.example.com")
	h.want(h.run("url"), 2)
	h.want(h.run("url", "s3://bkt/"), 2)
	h.want(h.run("url", "s3://bkt"), 2)
	h.want(h.run("url", "local/file"), 2)
	h.want(h.run("url", "s3://bkt/k", "--kind", "signed"), 2)
	h.want(h.run("url", "s3://bkt/a", "s3://bkt/b"), 2)
	h.want(h.run("url", "s3://bkt/dir/"), 2)
}
