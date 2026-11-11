package main

import (
	"bytes"
	"strings"
	"testing"
)

func (h *harness) statJSON(key string) map[string]any {
	h.t.Helper()
	r := h.want(h.run("--json", "stat", "s3://bkt/"+key), 0)
	return jsonLines(h.t, r.out)[0]
}

func TestStat(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "docs/a.txt", []byte("hello world"), "text/plain", map[string]string{"color": "red"})
	r := h.want(h.run("stat", "s3://bkt/docs/a.txt"), 0)
	for _, w := range []string{"key:", "docs/a.txt", "11 B", "text/plain", "metadata color", "red", "storage class"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("stat lacks %q:\n%s", w, r.out)
		}
	}
	o := h.statJSON("docs/a.txt")
	if o["type"] != "object" || o["key"] != "docs/a.txt" || o["size"] != float64(11) || o["contentType"] != "text/plain" ||
		o["metadata"].(map[string]any)["color"] != "red" {
		t.Fatalf("stat json: %v", o)
	}
	if s := lastSummary(t, h.run("--json", "stat", "s3://bkt/docs/a.txt").out); s["command"] != "stat" {
		t.Fatalf("summary: %v", s)
	}
	h.want(h.run("stat", "s3://bkt/missing"), 1)
	h.want(h.run("stat", "s3://bkt/docs/"), 2)
	h.want(h.run("stat", "s3://bkt"), 2)
	h.want(h.run("stat", "docs/a.txt"), 2)
	h.want(h.run("stat"), 2)

	// A version.
	h.srv.CreateBucket("ver", true)
	v1 := h.srv.PutObject("ver", "k", []byte("one"), "", nil)
	h.srv.PutObject("ver", "k", []byte("three"), "", nil)
	r = h.want(h.run("--json", "stat", "s3://ver/k", "--version-id", v1), 0)
	if o := jsonLines(t, r.out)[0]; o["size"] != float64(3) || o["versionId"] != v1 {
		t.Fatalf("version stat: %v", o)
	}
}

func TestCatIsBinarySafeAndQuiet(t *testing.T) {
	h := newHarness(t)
	raw := make([]byte, 0, 70000)
	for i := 0; i < 70000; i++ {
		raw = append(raw, byte(i*7))
	}
	raw = append(raw, "\r\n\x00\xff"...)
	h.srv.PutObject("bkt", "bin/blob", raw, "application/octet-stream", nil)
	h.srv.PutObject("bkt", "empty", nil, "", nil)

	r := h.want(h.run("cat", "s3://bkt/bin/blob"), 0)
	if !bytes.Equal([]byte(r.out), raw) {
		t.Fatalf("cat output differs (%d bytes, want %d)", len(r.out), len(raw))
	}
	if r.errOut != "" {
		t.Fatalf("cat must not write to stderr on success: %q", r.errOut)
	}
	r = h.want(h.run("cat", "s3://bkt/empty"), 0)
	if r.out != "" || r.errOut != "" {
		t.Fatalf("empty object: %q %q", r.out, r.errOut)
	}
	r = h.want(h.run("cat", "s3://bkt/missing"), 1)
	if r.out != "" || !strings.Contains(r.errOut, "not found") {
		t.Fatalf("missing: %q %q", r.out, r.errOut)
	}
	h.want(h.run("--json", "cat", "s3://bkt/empty"), 2)
	h.want(h.run("cat", "s3://bkt/bin/"), 2)
	h.want(h.run("cat"), 2)

	h.srv.CreateBucket("ver", true)
	v1 := h.srv.PutObject("ver", "k", []byte("first"), "", nil)
	h.srv.PutObject("ver", "k", []byte("second"), "", nil)
	if r := h.want(h.run("cat", "s3://ver/k"), 0); r.out != "second" {
		t.Fatalf("current: %q", r.out)
	}
	if r := h.want(h.run("cat", "s3://ver/k", "--version-id", v1), 0); r.out != "first" {
		t.Fatalf("version: %q", r.out)
	}
}

func TestMkdirAndDu(t *testing.T) {
	h := newHarness(t)
	r := h.want(h.run("mkdir", "s3://bkt/a/b/c/"), 0)
	if !strings.Contains(r.out, "s3://bkt/a/b/c/") {
		t.Fatalf("mkdir: %s", r.out)
	}
	if v, ok := h.obj("bkt", "a/b/c/"); !ok || v != "" {
		t.Fatalf("placeholder = %q %v", v, ok)
	}
	h.want(h.run("mkdir", "s3://bkt/top"), 0) // no trailing slash
	if _, ok := h.obj("bkt", "top/"); !ok {
		t.Fatal("top/ missing")
	}
	if r := h.want(h.run("ls", "s3://bkt/a/b/"), 0); !strings.Contains(r.out, "PRE c/") {
		t.Fatalf("ls: %s", r.out)
	}
	if s := lastSummary(t, h.want(h.run("--json", "mkdir", "s3://bkt/j/"), 0).out); s["command"] != "mkdir" {
		t.Fatalf("json: %v", s)
	}
	h.want(h.run("mkdir", "s3://bkt"), 2)
	h.want(h.run("mkdir", "s3://bkt/"), 2)
	h.want(h.run("mkdir", "s3://bkt//"), 2)
	h.want(h.run("mkdir", "s3://missing/x/"), 1)
	h.want(h.run("mkdir"), 2)

	h.srv.PutObject("bkt", "d/one", []byte("12345"), "", nil)
	h.srv.PutObject("bkt", "d/sub/two", []byte("1234567890"), "", nil)
	h.srv.PutObject("bkt", "other", []byte("zz"), "", nil)
	r = h.want(h.run("du", "s3://bkt/d/"), 0)
	if !strings.Contains(r.out, "2 objects") || !strings.Contains(r.out, "15 B") || !strings.Contains(r.out, "s3://bkt/d/") {
		t.Fatalf("du: %s", r.out)
	}
	h.want(h.run("du", "s3://bkt/d"), 0)
	o := jsonLines(t, h.want(h.run("--json", "du", "s3://bkt/d/"), 0).out)[0]
	if o["type"] != "du" || o["objects"] != float64(2) || o["bytes"] != float64(15) || o["path"] != "s3://bkt/d/" {
		t.Fatalf("du json: %v", o)
	}
	if o := jsonLines(t, h.run("--json", "du", "s3://bkt").out)[0]; o["bytes"] != float64(17) { // the folder placeholders created above are empty
		t.Fatalf("bucket du: %v", o)
	}
	h.want(h.run("du", "s3://nobucket/"), 1)
	h.want(h.run("du"), 2)
}

func TestTags(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "k", []byte("x"), "", nil)
	tags := func() string { return h.want(h.run("tags", "get", "s3://bkt/k"), 0).out }

	if r := h.want(h.run("tags", "get", "s3://bkt/k"), 0); r.out != "" || !strings.Contains(r.errOut, "no tags") {
		t.Fatalf("untagged: %q %q", r.out, r.errOut)
	}
	h.want(h.run("tags", "set", "s3://bkt/k", "env=prod", "team=a b"), 0)
	if got := tags(); got != "env=prod\nteam=a b\n" {
		t.Fatalf("tags = %q", got)
	}
	info := h.statJSON("k")["tags"].(map[string]any)
	if info["env"] != "prod" || info["team"] != "a b" {
		t.Fatalf("visible through stat: %v", info)
	}
	// set replaces; --merge keeps.
	h.want(h.run("tags", "set", "s3://bkt/k", "only=this"), 0)
	if got := tags(); got != "only=this\n" {
		t.Fatalf("after replace: %q", got)
	}
	h.want(h.run("tags", "set", "s3://bkt/k", "extra=1", "--merge"), 0)
	if got := tags(); got != "extra=1\nonly=this\n" {
		t.Fatalf("after merge: %q", got)
	}
	ev := jsonLines(t, h.want(h.run("--json", "tags", "get", "s3://bkt/k"), 0).out)
	if len(ev) != 3 || ev[0]["type"] != "tag" || ev[0]["key"] != "extra" || lastSummary(t, h.run("--json", "tags", "get", "s3://bkt/k").out)["count"] != float64(2) {
		t.Fatalf("json: %v", ev)
	}
	// Usage errors.
	h.want(h.run("tags", "set", "s3://bkt/k"), 2)                   // nothing to set
	h.want(h.run("tags", "set", "s3://bkt/k", "novalue"), 2)        // not KEY=VALUE
	h.want(h.run("tags", "set", "s3://bkt/k", "a=1", "a=2"), 2)     // duplicate
	h.want(h.run("tags", "set", "s3://bkt/k", "a=1", "--clear"), 2) // contradictory
	h.want(h.run("tags", "set", "s3://bkt/k", "--merge", "--clear"), 2)
	h.want(h.run("tags", "set", "s3://bkt/k", "aws:x=1"), 2) // reserved prefix, refused by the service layer
	h.want(h.run("tags", "get", "s3://bkt/k", "extra"), 2)
	h.want(h.run("tags", "bogus", "s3://bkt/k"), 2)
	h.want(h.run("tags", "get", "s3://bkt/"), 2)
	h.want(h.run("tags", "get", "s3://bkt/missing"), 1)
	// Clearing needs confirmation.
	r := h.want(h.run("tags", "set", "s3://bkt/k", "--clear"), 2)
	if !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("refusal: %s", r.errOut)
	}
	if got := tags(); got == "" {
		t.Fatal("refused clear removed the tags")
	}
	h.want(h.run("tags", "set", "s3://bkt/k", "--clear", "--yes"), 0)
	if got := tags(); got != "" {
		t.Fatalf("after clear: %q", got)
	}
}

func TestACL(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "k", []byte("x"), "", nil)
	r := h.want(h.run("acl", "get", "s3://bkt/k"), 0)
	if !strings.Contains(r.out, "owner:") || !strings.Contains(r.out, "FULL_CONTROL") || strings.Contains(r.out, "AllUsers") {
		t.Fatalf("default acl: %s", r.out)
	}
	h.want(h.run("acl", "set", "s3://bkt/k", "public-read"), 0)
	r = h.want(h.run("acl", "get", "s3://bkt/k"), 0)
	if !strings.Contains(r.out, "AllUsers") || !strings.Contains(r.out, "READ") {
		t.Fatalf("public-read: %s", r.out)
	}
	o := jsonLines(t, h.want(h.run("--json", "acl", "get", "s3://bkt/k"), 0).out)[0]
	if o["type"] != "acl" || len(o["grants"].([]any)) != 2 {
		t.Fatalf("acl json: %v", o)
	}
	h.want(h.run("acl", "set", "s3://bkt/k", "private"), 0)
	if r := h.want(h.run("acl", "get", "s3://bkt/k"), 0); strings.Contains(r.out, "AllUsers") {
		t.Fatalf("private: %s", r.out)
	}
	h.want(h.run("acl", "set", "s3://bkt/k", "bogus"), 2)
	h.want(h.run("acl", "set", "s3://bkt/k"), 2)
	h.want(h.run("acl", "get", "s3://bkt/missing"), 1)
	h.want(h.run("acl", "bogus", "s3://bkt/k"), 2)
	h.want(h.run("acl", "get"), 2)
}

func TestProps(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "k", []byte("body"), "text/plain", map[string]string{"old": "1", "keep": "2"})
	h.want(h.run("props", "set", "s3://bkt/k", "--content-type", "text/css", "--cache-control", "max-age=60",
		"--content-disposition", "inline", "--content-encoding", "identity", "--metadata", "color=blue", "--metadata", "size=big",
		"--delete-metadata", "old"), 0)
	o := h.statJSON("k")
	hdr := o["headers"].(map[string]any)
	meta := o["metadata"].(map[string]any)
	if o["contentType"] != "text/css" || hdr["Cache-Control"] != "max-age=60" || hdr["Content-Disposition"] != "inline" ||
		hdr["Content-Encoding"] != "identity" || meta["color"] != "blue" || meta["size"] != "big" || meta["keep"] != "2" || meta["old"] != nil {
		t.Fatalf("props: %v", o)
	}
	if v, _ := h.obj("bkt", "k"); v != "body" {
		t.Fatalf("body changed: %q", v)
	}
	// An empty value or --delete-header removes a header.
	h.want(h.run("props", "set", "s3://bkt/k", "--cache-control", "", "--delete-header", "Content-Disposition"), 0)
	hdr = h.statJSON("k")["headers"].(map[string]any)
	if _, ok := hdr["Cache-Control"]; ok {
		t.Fatalf("cache-control not removed: %v", hdr)
	}
	if _, ok := hdr["Content-Disposition"]; ok {
		t.Fatalf("content-disposition not removed: %v", hdr)
	}
	// props get is stat.
	if r := h.want(h.run("props", "get", "s3://bkt/k"), 0); !strings.Contains(r.out, "text/css") {
		t.Fatalf("props get: %s", r.out)
	}
	// Usage errors.
	h.want(h.run("props", "set", "s3://bkt/k"), 2)
	h.want(h.run("props", "set", "s3://bkt/k", "--metadata", "nokey"), 2)
	h.want(h.run("props", "set", "s3://bkt/k", "--metadata", "UPPER=1"), 2) // keys are lower case
	h.want(h.run("props", "set", "s3://bkt/k", "--delete-header", "X-Other"), 2)
	h.want(h.run("props", "set", "s3://bkt/k", "--expires", "tomorrow"), 2)
	h.want(h.run("props", "set", "s3://bkt/dir/", "--content-type", "text/css"), 2) // prefix needs -r
	h.want(h.run("props", "get", "s3://bkt/k", "--content-type", "x"), 2)
	h.want(h.run("props", "bogus", "s3://bkt/k"), 2)
	h.want(h.run("stat", "s3://bkt/k", "--content-type", "x"), 2)

	// Recursive changes ask first.
	for _, k := range []string{"tree/a", "tree/sub/b"} {
		h.srv.PutObject("bkt", k, []byte(k), "text/plain", nil)
	}
	r := h.want(h.run("props", "set", "s3://bkt/tree/", "-r", "--content-type", "image/png"), 2)
	if !strings.Contains(r.errOut, "2 objects") || !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("scope: %s", r.errOut)
	}
	if h.statJSON("tree/a")["contentType"] != "text/plain" {
		t.Fatal("refused run changed an object")
	}
	r = h.want(h.run("props", "set", "s3://bkt/tree/", "-r", "--content-type", "image/png", "--yes"), 0)
	if !strings.Contains(r.errOut, "tree/sub/b") {
		t.Fatalf("per-item lines: %s", r.errOut)
	}
	for _, k := range []string{"tree/a", "tree/sub/b"} {
		if h.statJSON(k)["contentType"] != "image/png" {
			t.Fatalf("%s not changed", k)
		}
	}
	if v, _ := h.obj("bkt", "tree/sub/b"); v != "tree/sub/b" {
		t.Fatal("body changed")
	}
	if s := lastSummary(t, h.want(h.run("--json", "--yes", "props", "set", "s3://bkt/k", "--metadata", "z=1"), 0).out); s["command"] != "props" || s["succeeded"] != float64(1) {
		t.Fatalf("json: %v", s)
	}
}

func TestStorageClass(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "k", []byte("body"), "text/plain", nil)
	r := h.want(h.run("storage-class", "s3://bkt/k", "standard_ia"), 0)
	if !strings.Contains(r.errOut, "done") {
		t.Fatalf("stderr: %s", r.errOut)
	}
	if h.statJSON("k")["storageClass"] != "STANDARD_IA" {
		t.Fatalf("class: %v", h.statJSON("k"))
	}
	if v, _ := h.obj("bkt", "k"); v != "body" {
		t.Fatal("body changed")
	}
	h.want(h.run("storage-class", "s3://bkt/k", "not a class"), 2)
	h.want(h.run("storage-class", "s3://bkt/k"), 2)
	h.want(h.run("storage-class", "s3://bkt/tree/", "GLACIER"), 2) // prefix needs -r

	for _, k := range []string{"tree/a", "tree/b"} {
		h.srv.PutObject("bkt", k, []byte("x"), "", nil)
	}
	r = h.want(h.run("storage-class", "s3://bkt/tree/", "ONEZONE_IA", "-r"), 2)
	if !strings.Contains(r.errOut, "2 objects") {
		t.Fatalf("scope: %s", r.errOut)
	}
	h.want(h.run("storage-class", "s3://bkt/tree/", "ONEZONE_IA", "-r", "--yes"), 0)
	for _, k := range []string{"tree/a", "tree/b"} {
		if c := h.statJSON(k)["storageClass"]; c != "ONEZONE_IA" {
			t.Fatalf("%s class = %v", k, c)
		}
	}
}

func TestArchiveRestore(t *testing.T) {
	h := newHarness(t)
	h.srv.PutObject("bkt", "cold/a", []byte("a"), "", nil)
	h.srv.PutObject("bkt", "cold/b", []byte("b"), "", nil)
	h.srv.SetStorageClass("bkt", "cold/a", "DEEP_ARCHIVE")
	h.want(h.run("archive-restore", "s3://bkt/cold/a", "--days", "3"), 0)
	if !strings.Contains(h.statJSON("cold/a")["restore"].(string), `ongoing-request="true"`) {
		t.Fatalf("no restore started: %v", h.statJSON("cold/a"))
	}
	h.want(h.run("archive-restore", "s3://bkt/cold/a", "--tier", "fast"), 2)
	h.want(h.run("archive-restore", "s3://bkt/cold/a", "--days", "0"), 2)
	h.want(h.run("archive-restore", "s3://bkt/cold/"), 2) // a prefix needs -r
	// The STANDARD object is skipped, the running restore too.
	h.want(h.run("--yes", "archive-restore", "s3://bkt/cold/", "-r"), 0)
}
