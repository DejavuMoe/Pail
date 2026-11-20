package main

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestMultiSelectEditShowsMixedAndSaysHowMany(t *testing.T) {
	t.Parallel()

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
	// Several objects are rewritten: the button says how many, and pressing
	// it is the decision (no second question).
	h.mustNotHave("Save")
	h.snap("headers-multi-save-light")
	h.mustClick("Update 2 items")
	if h.a.confirm.open {
		t.Fatal("the multi-object save asked again")
	}
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
	t.Parallel()
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	h.mustClick("Headers")
	h.waitFor("fields loaded", func() bool { return h.a.mg.props.ready })
	h.mustClick("Clear Content-Type")
	h.mustHave("Undo Content-Type", "Update 2 items")
	pt, msg := h.a.mg.props.patch()
	if msg != "" || len(pt.DelHeaders) != 1 || pt.DelHeaders[0] != "Content-Type" {
		t.Fatalf("patch = %+v %q", pt, msg)
	}
}

func TestReadOnlyObjectShowsReason(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestTagsDraftDoesNotFollowBucket(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	const otherBucket = "acme-backups"
	h.env.Srv.PutObject(otherBucket, "manifest.json", []byte("backup"), "application/json", nil)
	loc := Scope{AccountID: h.env.Account.ID, Bucket: otherBucket}
	if err := h.env.Svc.SetTags(context.Background(), loc, core.ObjectRef{Key: "manifest.json"}, map[string]string{"owner": "backup"}); err != nil {
		t.Fatal(err)
	}
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustClick("Add tag")
	h.typeAndEnter("env")
	h.typeAndEnter("prod")
	h.mustHave("Save", "Revert")

	h.mustClick(otherBucket)
	h.mustNotHave("Save", "Revert")
	// The action must also reject a draft that no longer has a selected object.
	h.a.saveTags()
	h.settle()
	if tags := h.head(bkt, "manifest.json").Tags; len(tags) != 0 {
		t.Errorf("leaving the object saved its draft: %v", tags)
	}
	if tags := h.head(otherBucket, "manifest.json").Tags; len(tags) != 1 || tags["owner"] != "backup" {
		t.Errorf("the old draft changed the other bucket's object: %v", tags)
	}

	// The same key in the new bucket loads its own tags and remains editable.
	h.mustClick("manifest.json")
	h.mustHave("owner", "backup")
	h.mustNotHave("Save", "Revert")
	h.mustClick("Add tag")
	h.typeAndEnter("env")
	h.typeAndEnter("staging")
	h.mustClick("Save")
	if tags := h.head(otherBucket, "manifest.json").Tags; len(tags) != 2 || tags["owner"] != "backup" || tags["env"] != "staging" {
		t.Errorf("saving the new object's tags: %v", tags)
	}
}

func TestTagsDraftClearsWhenSelectionChanges(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"none", "folder", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, "light")
			h.mustClick("manifest.json")
			h.mustClick("Tags")
			h.mustClick("Add tag")
			h.typeAndEnter("env")
			h.typeAndEnter("prod")
			switch mode {
			case "none":
				h.a.sel.Clear()
			case "folder":
				h.a.sel.Clear()
				h.a.sel.Add("website/")
			case "multiple":
				h.a.sel.Add("README.txt")
			}
			h.settle()
			h.mustNotHave("Save", "Revert")
			h.a.saveTags()
			h.settle()
			if tags := h.head(bkt, "manifest.json").Tags; len(tags) != 0 {
				t.Errorf("saving without the original single selection changed its tags: %v", tags)
			}
		})
	}
}

func TestTagsLimitAndDuplicateKey(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("tagging")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustHave("Tags are not available")
	h.mustNotHave("Add tag")
}

func TestTagSavePreservesOtherObjectDraft(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			h := newHarness(t, "light")
			h.mustClick("manifest.json")
			h.mustClick("Tags")
			h.a.mg.tags.items = []kvItem{{key: "first", val: "saved", isNew: true}}
			if fail {
				h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
					if r.Method == http.MethodPut && r.URL.Query().Has("tagging") {
						return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied"}
					}
					return nil
				})
			}
			h.a.saveTags()
			old := nextCompletion(t, h.a)
			if err := h.tt.Click("README.txt"); err != nil {
				t.Fatal(err)
			}
			h.waitFor("second object's tags", func() bool { return h.a.mg.tags.ready && h.a.mg.tags.key == "README.txt" })
			h.a.mg.tags.items = []kvItem{{key: "second", val: "unsaved", isNew: true}}
			old()
			h.settle()
			f := &h.a.mg.tags
			if f.key != "README.txt" || f.result()["second"] != "unsaved" || !f.dirty() || f.err != "" || f.saving {
				t.Fatalf("old save changed another object's draft: %+v tags=%v", f, f.result())
			}
			if saved := h.head(bkt, "manifest.json").Tags["first"] == "saved"; saved == fail {
				t.Fatalf("first save outcome wrong: saved=%v fail=%v", saved, fail)
			}
		})
	}
}

func TestTagSavePreservesLaterEditsOnSameObject(t *testing.T) {
	t.Parallel()
	for _, edit := range []string{"value", "remove", "add", "inline"} {
		t.Run(edit, func(t *testing.T) {
			h := newHarness(t, "light")
			h.mustClick("manifest.json")
			h.mustClick("Tags")
			f := &h.a.mg.tags
			f.items = []kvItem{{key: "first", val: "submitted", isNew: true}}
			h.a.saveTags()
			old := nextCompletion(t, h.a)
			switch edit {
			case "value":
				f.items[0].val = "later"
			case "remove":
				f.items = nil
			case "add":
				f.items = append(f.items, kvItem{key: "second", val: "later", isNew: true})
			case "inline":
				f.edit.start("t0", "later typing")
			}
			old()
			h.settle()
			if got := h.head(bkt, "manifest.json").Tags; got["first"] != "submitted" || len(got) != 1 {
				t.Fatalf("submitted tags=%v", got)
			}
			switch edit {
			case "value":
				if f.result()["first"] != "later" || !f.dirty() {
					t.Fatalf("later value lost: %v dirty=%v", f.result(), f.dirty())
				}
			case "remove":
				if len(f.result()) != 0 || !f.dirty() {
					t.Fatalf("later removal lost: %v dirty=%v", f.result(), f.dirty())
				}
			case "add":
				if f.result()["second"] != "later" || !f.dirty() {
					t.Fatalf("later addition lost: %v dirty=%v", f.result(), f.dirty())
				}
			case "inline":
				if f.edit.id != "t0" || f.edit.draft != "later typing" {
					t.Fatalf("active inline draft lost: %+v", f.edit)
				}
			}
			f.revert()
			if got := f.result(); len(got) != 1 || got["first"] != "submitted" || f.dirty() {
				t.Fatalf("revert did not use submitted baseline: %v dirty=%v", got, f.dirty())
			}
		})
	}
}

// ---- drafts across a reload of the details ------------------------------------------------

// holdRequests keeps the requests match picks waiting on the server until
// release is called; started receives once for each request held.
func holdRequests(t *testing.T, h *harness, match func(*http.Request) bool) (started <-chan struct{}, release func()) {
	t.Helper()
	gate := make(chan struct{})
	ch := make(chan struct{}, 64)
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if match(r) {
			select {
			case ch <- struct{}{}:
			default:
			}
			<-gate
		}
		return nil
	})
	return ch, release
}

func isCopy(r *http.Request) bool {
	return r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != ""
}

func isVersionDelete(r *http.Request) bool {
	q := r.URL.Query()
	return r.Method == http.MethodDelete && q.Has("versionId") || r.Method == http.MethodPost && q.Has("delete")
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the held request never reached the server")
	}
}

// chooseForHeaders selects key alone on the Headers tab and waits for its form.
func (h *harness) chooseForHeaders(key string) *propsForm {
	h.t.Helper()
	h.a.inspTab = 1
	h.a.setSingle(key)
	p := &h.a.mg.props
	h.waitFor("headers of "+key, func() bool {
		return h.a.headKey == key && h.a.headState == loadReady && p.ready && p.sig == h.a.selectionSig() && p.gen == h.a.headGen
	})
	return p
}

// waitFollows waits until every followed task has reported its end.
func (h *harness) waitFollows() {
	h.t.Helper()
	h.waitFor("followed tasks to end", func() bool { return len(h.a.mg.follows) == 0 })
	h.settle()
}

const (
	hdrContentLanguage = 2
	hdrCacheControl    = 4
)

// A save of the headers of one object dropped the unsaved header draft of the
// object chosen while the save ran.
func TestN1PropsSavePreservesOtherObjectDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	started, release := holdRequests(t, h, isCopy)
	p := h.chooseForHeaders("manifest.json")
	p.hdrSet[hdrContentLanguage], p.hdrVal[hdrContentLanguage] = true, "de"
	h.a.saveProps()
	waitStarted(t, started)

	p = h.chooseForHeaders("README.txt")
	p.hdrSet[hdrContentLanguage], p.hdrVal[hdrContentLanguage] = true, "fr"
	release()
	h.waitToast("Saved")
	h.settle()

	if h.a.headKey != "README.txt" || p.sig != h.a.selectionSig() || !p.ready {
		t.Fatalf("the form left README.txt: headKey=%q ready=%v", h.a.headKey, p.ready)
	}
	if !p.hdrSet[hdrContentLanguage] || p.hdrVal[hdrContentLanguage] != "fr" || !p.dirty() || p.saving {
		t.Fatalf("README.txt's unsaved draft was lost when manifest.json's save ended: set=%v val=%q dirty=%v saving=%v",
			p.hdrSet[hdrContentLanguage], p.hdrVal[hdrContentLanguage], p.dirty(), p.saving)
	}
	if got := headerOf(h.head(bkt, "manifest.json"), "Content-Language"); got != "de" {
		t.Fatalf("manifest.json Content-Language = %q", got)
	}
	if got := headerOf(h.head(bkt, "README.txt"), "Content-Language"); got != "" {
		t.Fatalf("README.txt draft reached the server: %q", got)
	}
	// The saved object shows its new values when it is chosen again.
	p = h.chooseForHeaders("manifest.json")
	if p.hdrBase[hdrContentLanguage].baseVal != "de" || p.dirty() {
		t.Fatalf("manifest.json shows %q dirty=%v", p.hdrBase[hdrContentLanguage].baseVal, p.dirty())
	}
}

// A save of the headers kept the edits made while it ran, on the same object,
// over the values it saved.
func TestN1PropsSaveKeepsLaterEditsOnSameObject(t *testing.T) {
	t.Parallel()
	for _, edit := range []string{"header", "change", "metadata", "inline"} {
		t.Run(edit, func(t *testing.T) {
			h := newHarness(t, "light")
			started, release := holdRequests(t, h, isCopy)
			p := h.chooseForHeaders("manifest.json")
			p.hdrSet[hdrContentLanguage], p.hdrVal[hdrContentLanguage] = true, "de"
			h.a.saveProps()
			waitStarted(t, started)
			switch edit {
			case "header":
				p.hdrSet[hdrCacheControl], p.hdrVal[hdrCacheControl] = true, "max-age=60"
			case "change":
				p.hdrSet[hdrContentLanguage], p.hdrVal[hdrContentLanguage] = true, "it"
			case "metadata":
				p.meta = append(p.meta, kvItem{key: "owner", val: "later", isNew: true, touched: true})
			case "inline":
				p.edit.start("h4", "later typing")
			}
			release()
			h.waitToast("Saved")
			h.settle()
			info := h.head(bkt, "manifest.json")
			if got := headerOf(info, "Content-Language"); got != "de" {
				t.Fatalf("Content-Language on the server = %q", got)
			}
			if headerOf(info, "Cache-Control") != "" || info.Metadata["owner"] != "" {
				t.Fatalf("a later edit reached the server: %+v", info)
			}
			if p.sig != h.a.selectionSig() || !p.ready || p.saving {
				t.Fatalf("form dropped: ready=%v saving=%v", p.ready, p.saving)
			}
			if p.hdrBase[hdrContentLanguage].baseVal != "de" {
				t.Fatalf("the saved value is not the form's base: %q", p.hdrBase[hdrContentLanguage].baseVal)
			}
			switch edit {
			case "header":
				if p.hdrSet[hdrContentLanguage] || !p.hdrSet[hdrCacheControl] || p.hdrVal[hdrCacheControl] != "max-age=60" || !p.dirty() {
					t.Fatalf("later header edit lost or saved edit still pending: set=%v vals=%q", p.hdrSet, p.hdrVal)
				}
			case "change":
				if !p.hdrSet[hdrContentLanguage] || p.hdrVal[hdrContentLanguage] != "it" || !p.dirty() {
					t.Fatalf("later change of the saved header lost: set=%v vals=%q", p.hdrSet, p.hdrVal)
				}
			case "metadata":
				found := false
				for _, it := range p.meta {
					if it.isNew && it.key == "owner" && it.val == "later" {
						found = true
					}
				}
				if !found || !p.dirty() || p.hdrSet[hdrContentLanguage] {
					t.Fatalf("later metadata lost: %+v", p.meta)
				}
			case "inline":
				if p.edit.id != "h4" || p.edit.draft != "later typing" {
					t.Fatalf("open field lost: %+v", p.edit)
				}
			}
			if edit == "header" {
				h.a.saveProps()
				h.waitToast("Saved")
				h.waitFor("second save", func() bool { return !p.saving && p.ready })
				h.settle()
				info := h.head(bkt, "manifest.json")
				if headerOf(info, "Cache-Control") != "max-age=60" || headerOf(info, "Content-Language") != "de" {
					t.Fatalf("second save: %+v", info.Headers)
				}
			}
		})
	}
}

// A storage-class rewrite reloaded the details on show whatever object they
// were, and the unsaved edits of the object went with them.
func TestN1StorageClassRewriteKeepsDrafts(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"other", "same"} {
		t.Run(which, func(t *testing.T) {
			h := newHarness(t, "light")
			started, release := holdRequests(t, h, isCopy)
			p := h.chooseForHeaders("manifest.json")
			h.a.openRewriteSelection(false)
			h.a.objOps.rw.class = "STANDARD_IA"
			h.a.submitRewrite()
			waitStarted(t, started)
			key := "manifest.json"
			if which == "other" {
				key = "README.txt"
				p = h.chooseForHeaders(key)
			}
			p.hdrSet[hdrCacheControl], p.hdrVal[hdrCacheControl] = true, "no-store"
			release()
			h.waitFollows()
			if got := h.head(bkt, "manifest.json").StorageClass; got != "STANDARD_IA" {
				t.Fatalf("class on the server = %q", got)
			}
			if h.a.headKey != key || p.sig != h.a.selectionSig() || !p.hdrSet[hdrCacheControl] || p.hdrVal[hdrCacheControl] != "no-store" || !p.dirty() {
				t.Fatalf("%s's draft lost: headKey=%q set=%v vals=%q", key, h.a.headKey, p.hdrSet, p.hdrVal)
			}
			if which == "same" {
				h.waitFor("new class on show", func() bool { return h.a.head.StorageClass == "STANDARD_IA" })
				if !p.hdrSet[hdrCacheControl] || !p.dirty() {
					t.Fatal("the fresh details dropped the draft")
				}
			}
		})
	}
}

// The same for a draft of the Tags tab: a rewrite of the object keeps it, and
// the tags read afresh become its base.
func TestN1StorageClassRewriteKeepsTagDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	started, release := holdRequests(t, h, isCopy)
	h.chooseForHeaders("manifest.json")
	h.a.inspTab = 2
	tg := &h.a.mg.tags
	h.waitFor("tags", func() bool { return tg.ready && tg.key == "manifest.json" })
	h.a.openRewriteSelection(false)
	h.a.objOps.rw.class = "STANDARD_IA"
	h.a.submitRewrite()
	waitStarted(t, started)
	tg.items = append(tg.items, kvItem{key: "later", val: "draft", isNew: true, touched: true})
	tg.edit.start("t"+strconv.Itoa(len(tg.items)-1), "typing")
	release()
	h.waitFollows()
	h.waitFor("new class on show", func() bool { return h.a.head.StorageClass == "STANDARD_IA" })
	if tg.key != "manifest.json" || tg.result()["later"] != "draft" || !tg.dirty() {
		t.Fatalf("tag draft lost: key=%q tags=%v", tg.key, tg.result())
	}
	if want := "t" + strconv.Itoa(len(tg.items)-1); tg.edit.id != want || tg.edit.draft != "typing" {
		t.Fatalf("open tag field moved: %+v want %s", tg.edit, want)
	}
	if !maps.Equal(tg.base, h.head(bkt, "manifest.json").Tags) && len(tg.base)+len(h.head(bkt, "manifest.json").Tags) > 0 {
		t.Fatalf("tags base %v is not the server's", tg.base)
	}
}

// Restoring or deleting a version of one object reset the details, the drafts
// and the version selection of the object chosen while it ran.
func TestN1VersionActionsKeepOtherObjectState(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"restore", "delete"} {
		t.Run(action, func(t *testing.T) {
			h := newRawHarness(t, "light", true)
			h.openVersioned()
			match := isCopy
			if action == "delete" {
				match = isVersionDelete
			}
			started, release := holdRequests(t, h, match)
			h.chooseForHeaders("notes.txt")
			vs := h.env.Srv.Versions(demoVersioned, "notes.txt")
			loc := h.a.scope
			ref := core.ObjectRef{Key: "notes.txt", VersionID: vs[2].VersionID}
			if action == "restore" {
				h.a.restoreVersion(loc, ref)
			} else {
				h.a.askDeleteVersions(loc, []core.ObjectRef{ref})
				h.a.confirm.run()
				h.a.confirm = confirmState{}
			}
			waitStarted(t, started)

			p := h.chooseForHeaders("report.pdf")
			p.hdrSet[hdrCacheControl], p.hdrVal[hdrCacheControl] = true, "no-store"
			h.a.inspTab = 4
			f := &h.a.mg.vers
			h.waitFor("report.pdf versions", func() bool { return f.key == "report.pdf" && f.state == loadReady && len(f.rows) > 0 })
			vid := f.rows[0].VersionID
			f.sel = map[string]bool{vid: true}
			release()
			if action == "restore" {
				h.waitToast("Restored version " + shortVersion(ref.VersionID))
			} else {
				h.waitToast("Deleted 1 version")
			}
			h.waitFollows()
			if n := len(h.env.Srv.Versions(demoVersioned, "notes.txt")); (action == "restore" && n != 4) || (action == "delete" && n != 2) {
				t.Fatalf("%s did not happen: %d versions", action, n)
			}
			if f.key != "report.pdf" || !f.sel[vid] {
				t.Errorf("report.pdf's chosen versions lost: key=%q sel=%v", f.key, f.sel)
			}
			h.a.inspTab = 1
			h.tt.Frame()
			if h.a.headKey != "report.pdf" || p.sig != h.a.selectionSig() || !p.hdrSet[hdrCacheControl] || !p.dirty() {
				t.Fatalf("report.pdf's draft lost: headKey=%q set=%v", h.a.headKey, p.hdrSet)
			}
		})
	}
}

// rebaseItems keeps each kind of edit over a new base and drops what the new
// base already holds.
func TestN1RebaseItems(t *testing.T) {
	t.Parallel()
	items := []kvItem{
		{key: "a", val: "1", baseVal: "1"},                  // unedited, still there: follows the base
		{key: "b", val: "x", baseVal: "1", touched: true},   // changed, the base now holds it
		{key: "c", val: "y", baseVal: "1", touched: true},   // changed, gone from the base: an addition
		{key: "d", val: "1", baseVal: "1", removed: true},   // removed, still there
		{key: "e", val: "1", baseVal: "1"},                  // unedited, gone
		{key: "F", val: "new", isNew: true, touched: true},  // added, the base now holds it
		{key: "g", val: "mine", isNew: true, touched: true}, // added, the base has another value
		{isNew: true}, // a pair whose name is being typed
	}
	base := []kvItem{{key: "a", val: "2", baseVal: "2"}, {key: "b", val: "x", baseVal: "x"}, {key: "d", val: "1", baseVal: "1"},
		{key: "f", val: "new", baseVal: "new"}, {key: "g", val: "theirs", baseVal: "theirs"}}
	out, moved := rebaseItems(items, base, metaKey)
	want := []kvItem{
		{key: "a", val: "2", baseVal: "2"},
		{key: "b", val: "x", baseVal: "x"},
		{key: "d", val: "1", baseVal: "1", removed: true},
		{key: "f", val: "new", baseVal: "new"},
		{key: "g", val: "mine", baseVal: "theirs", touched: true},
		{key: "c", val: "y", isNew: true, touched: true},
		{isNew: true},
	}
	if !slices.Equal(out, want) {
		t.Fatalf("rebased\n got %+v\nwant %+v", out, want)
	}
	if wantMoved := []int{0, 1, 5, 2, -1, 3, 4, 6}; !slices.Equal(moved, wantMoved) {
		t.Fatalf("moved %v want %v", moved, wantMoved)
	}
	e := inlineEdit{id: "m7k", draft: "typing"}
	remapEdit(&e, "m", moved)
	if e.id != "m6k" {
		t.Fatalf("edit id %q", e.id)
	}
	e = inlineEdit{id: "m4", draft: "x"}
	remapEdit(&e, "m", moved)
	if e.id != "" {
		t.Fatalf("edit on a pair that went away stayed open: %q", e.id)
	}
}
