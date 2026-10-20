package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// head reads an object straight from the service, the way the server holds it.
func (h *harness) head(bucket, key string) core.ObjectInfo {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := h.env.Svc.Head(ctx, core.Location{AccountID: h.env.Account.ID, Bucket: bucket}, key, "")
	if err != nil {
		h.t.Fatalf("head %s/%s: %v", bucket, key, err)
	}
	return info
}

func headerOf(info core.ObjectInfo, name string) string {
	for _, hd := range info.Headers {
		if hd.Name == name {
			return hd.Value
		}
	}
	return ""
}

// typeAndEnter types into the field that has the keyboard and presses Enter.
func (h *harness) typeAndEnter(s string) {
	h.t.Helper()
	h.tt.Type(s)
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
}

// replaceAndEnter replaces the field's text and presses Enter.
func (h *harness) replaceAndEnter(s string) {
	h.t.Helper()
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.typeAndEnter(s)
}

func (h *harness) waitToast(text string) {
	h.t.Helper()
	h.waitFor("toast "+text, func() bool { return h.tt.HasText(text) })
}

// ---- headers and metadata -----------------------------------------------------------------

func TestEditHeadersAndMetadataOfOneObject(t *testing.T) {
	h := newHarness(t, "light")
	before, _ := h.env.Srv.Object(bkt, "manifest.json")
	h.mustClick("manifest.json")
	h.mustClick("Headers")
	h.mustHave("Content-Type", "Cache-Control", "build-id", "Add metadata")
	h.mustNotHave("Revert") // nothing edited yet: no footer
	h.snap("headers-clean-light")

	// Edit a header inline, change a metadata value, add a pair, remove one.
	h.mustClick("Edit Cache-Control")
	h.typeAndEnter("max-age=3600")
	h.mustHave("max-age=3600", "Revert", "Save")
	h.mustClick("b-20261018.4")
	h.replaceAndEnter("b-99")
	h.mustClick("Add metadata")
	h.typeAndEnter("owner") // the key, then the value
	h.typeAndEnter("platform")
	h.mustClick("Remove source-sha")
	h.mustHave("Removed", "owner", "platform", "b-99")
	h.snap("headers-dirty-light")
	// Nothing reaches the server before Save.
	if got := headerOf(h.head(bkt, "manifest.json"), "Cache-Control"); got != "" {
		t.Fatalf("the server has Cache-Control %q before Save", got)
	}
	h.mustClick("Save")
	h.waitToast("Saved")
	h.settle()

	info := h.head(bkt, "manifest.json")
	if got := headerOf(info, "Cache-Control"); got != "max-age=3600" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := headerOf(info, "Content-Type"); got != "application/json" {
		t.Errorf("Content-Type changed to %q", got)
	}
	want := map[string]string{"build-id": "b-99", "uploaded-by": "ci-deploy", "owner": "platform"}
	for k, v := range want {
		if info.Metadata[k] != v {
			t.Errorf("metadata %s = %q, want %q (all: %v)", k, info.Metadata[k], v, info.Metadata)
		}
	}
	if _, ok := info.Metadata["source-sha"]; ok {
		t.Error("the removed metadata is still on the server")
	}
	if after, _ := h.env.Srv.Object(bkt, "manifest.json"); string(after) != string(before) {
		t.Error("the object body changed")
	}
	// The panel shows what the server now has, and the footer is gone.
	h.waitFor("fresh values", func() bool { return h.tt.HasText("max-age=3600") && !h.tt.HasText("Revert") })
	h.mustNotHave("source-sha")
}

func TestRevertDropsTheEdits(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Headers")
	h.mustClick("Edit Content-Language")
	h.typeAndEnter("de")
	h.mustHave("Revert")
	h.mustClick("Revert")
	h.mustNotHave("Revert")
	if headerOf(h.head(bkt, "manifest.json"), "Content-Language") != "" {
		t.Error("revert wrote to the server")
	}
	// Escape leaves a field untouched.
	h.mustClick("Edit Content-Language")
	h.tt.Type("fr")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	h.mustNotHave("Revert")
}

func TestVersionedBucketSaveNote(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openBucket(demoVersioned)
	h.settle()
	h.mustClick("notes.txt")
	h.mustClick("Headers")
	h.mustClick("Edit Content-Language")
	h.typeAndEnter("en")
	h.mustHave("Saving creates a new version.")
	h.snap("headers-versioned-note-light")
	h.mustClick("Save")
	h.waitToast("Saved")
	if n := len(h.env.Srv.Versions(demoVersioned, "notes.txt")); n != 4 {
		t.Errorf("notes.txt has %d versions after saving, want 4", n)
	}
	// An unversioned bucket carries no such note.
	h.a.openBucket(bkt)
	h.settle()
	h.mustClick("manifest.json")
	h.mustClick("Headers")
	h.mustClick("Edit Content-Language")
	h.typeAndEnter("en")
	h.mustHave("Save")
	h.mustNotHave("Saving creates a new version.")
}

func TestMultiSelectEditShowsMixedAndAsksFirst(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	h.mustClick("Headers")
	h.waitFor("fields loaded", func() bool { return h.a.mg.props.ready })
	h.mustHave("Mixed", "Not set")
	// Content-Type differs, Cache-Control is unset on both.
	if !h.a.mg.props.hdrBase[0].mixed || h.a.mg.props.hdrBase[4].mixed {
		t.Fatalf("mixed flags = %+v", h.a.mg.props.hdrBase)
	}
	h.snap("headers-multi-mixed-light")

	h.mustClick("Edit Cache-Control")
	h.typeAndEnter("no-cache")
	h.mustClick("Save")
	// Several objects are rewritten: the standard confirmation comes first.
	h.mustHave("Update 2 items?", "2 objects", demoAccountName, bkt, "set Cache-Control")
	if headerOf(h.head(bkt, "README.txt"), "Cache-Control") != "" {
		t.Fatal("saved without confirming")
	}
	h.snap("headers-multi-confirm-light")
	h.mustClick("Update 2 items")
	h.waitToast("Saved")
	for _, k := range []string{"README.txt", "manifest.json"} {
		if got := headerOf(h.head(bkt, k), "Cache-Control"); got != "no-cache" {
			t.Errorf("%s Cache-Control = %q", k, got)
		}
	}
	// Fields the user did not touch stay as they were on each object.
	if got := headerOf(h.head(bkt, "manifest.json"), "Content-Type"); got != "application/json" {
		t.Errorf("manifest.json Content-Type = %q", got)
	}
	if got := headerOf(h.head(bkt, "README.txt"), "Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("README.txt Content-Type = %q", got)
	}
	if h.head(bkt, "manifest.json").Metadata["build-id"] != "b-20261018.4" {
		t.Error("metadata of manifest.json was lost")
	}
}

func TestMultiSelectClearsAMixedField(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	h.mustClick("Headers")
	h.waitFor("fields loaded", func() bool { return h.a.mg.props.ready })
	h.mustClick("Clear Content-Type")
	h.mustHave("Undo Content-Type", "Save")
	pt, msg := h.a.mg.props.patch()
	if msg != "" || len(pt.DelHeaders) != 1 || pt.DelHeaders[0] != "Content-Type" {
		t.Fatalf("patch = %+v %q", pt, msg)
	}
}

func TestReadOnlyObjectShowsReason(t *testing.T) {
	h := newHarness(t, "light")
	req, _ := http.NewRequest(http.MethodPut, h.env.Srv.URL+"/"+bkt+"/secret.bin", strings.NewReader("sealed"))
	req.Header.Set("x-amz-server-side-encryption-customer-algorithm", "AES256")
	req.Header.Set("x-amz-server-side-encryption-customer-key-MD5", "abcdefghijklmnopqrstuv==")
	req.Header.Set("Authorization", "x")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("put sse-c object: %v %v", err, res)
	}
	res.Body.Close()
	h.a.reload()
	h.settle()
	h.mustClick("secret.bin")
	h.mustClick("Headers")
	h.waitFor("fields loaded", func() bool { return h.a.mg.props.ready })
	if h.a.mg.props.readOnly == "" {
		t.Fatal("the SSE-C object is not marked read-only")
	}
	h.mustHave("Read-only", "Content-Type")
	h.mustNotHave("Edit Content-Type", "Add metadata", "Save", "Revert")
	h.snap("headers-readonly-light")
}

// ---- tags -----------------------------------------------------------------------------------------

func TestEditTags(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustHave("Add tag", "None")
	h.mustClick("Add tag")
	h.typeAndEnter("env")
	h.typeAndEnter("prod")
	h.mustClick("Add tag")
	h.typeAndEnter("team")
	h.typeAndEnter("web")
	h.mustHave("env", "prod", "team", "web", "Revert", "Save")
	h.snap("tags-edit-light")
	h.mustClick("Save")
	h.waitToast("Saved")
	if tags := h.head(bkt, "manifest.json").Tags; tags["env"] != "prod" || tags["team"] != "web" || len(tags) != 2 {
		t.Fatalf("tags on the server: %v", tags)
	}
	// Remove one and change the other.
	h.waitFor("tags reloaded", func() bool { return h.a.mg.tags.ready && len(h.a.mg.tags.items) == 2 && !h.a.mg.tags.dirty() })
	h.mustClick("Remove team")
	h.mustClick("prod")
	h.replaceAndEnter("staging")
	h.mustClick("Save")
	h.waitToast("Saved")
	if tags := h.head(bkt, "manifest.json").Tags; len(tags) != 1 || tags["env"] != "staging" {
		t.Fatalf("tags on the server: %v", tags)
	}
}

func TestTagsLimitAndDuplicateKey(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	for i := 0; i < maxObjectTags; i++ {
		h.mustClick("Add tag")
		h.typeAndEnter("k" + string(rune('a'+i)))
		h.typeAndEnter("v")
	}
	// The eleventh tag cannot be added: the button is disabled.
	n := len(h.a.mg.tags.items)
	h.tt.Click("Add tag")
	h.settle()
	if len(h.a.mg.tags.items) != n {
		t.Fatalf("an eleventh tag was added (%d items)", len(h.a.mg.tags.items))
	}
	h.mustClick("Remove kj")
	h.mustClick("Add tag")
	h.typeAndEnter("ka") // duplicate
	h.typeAndEnter("v")
	h.mustHave("Key used twice")
	h.tt.Click("Save")
	h.settle()
	h.mustHave("Fix the highlighted tags first")
}

func TestTagsUnsupportedStillShowsNote(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("tagging")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustHave("Tags are not available")
	h.mustNotHave("Add tag")
}
