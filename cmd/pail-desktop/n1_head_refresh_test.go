package main

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

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

// A rename that ended while another object was chosen took the selection to
// the renamed object, and the other object's draft with it.
func TestN1RenameKeepsOtherSelectionAndDraft(t *testing.T) {
	h := newHarness(t, "light")
	started, release := holdRequests(t, h, isCopy)
	h.chooseForHeaders("manifest.json")
	h.a.mg.ren = renameState{key: "manifest.json", name: "manifest-2.json"}
	h.a.commitRename(true)
	waitStarted(t, started)
	p := h.chooseForHeaders("README.txt")
	p.hdrSet[hdrCacheControl], p.hdrVal[hdrCacheControl] = true, "no-store"
	release()
	h.waitToast("Renamed to manifest-2.json")
	h.waitFollows()
	if !h.hasObject(bkt, "manifest-2.json") {
		t.Fatal("rename did not happen")
	}
	if k, one := h.a.soleSelected(); !one || k != "README.txt" {
		t.Errorf("selection moved to %q", k)
	}
	if h.a.headKey != "README.txt" || !p.hdrSet[hdrCacheControl] || !p.dirty() {
		t.Fatalf("README.txt's draft lost: headKey=%q set=%v", h.a.headKey, p.hdrSet)
	}
}

// The renamed object is still chosen, under its new name, when nothing else
// was chosen meanwhile.
func TestN1RenameSelectsTheNewName(t *testing.T) {
	h := newHarness(t, "light")
	h.chooseForHeaders("manifest.json")
	h.a.mg.ren = renameState{key: "manifest.json", name: "manifest-2.json"}
	h.a.commitRename(true)
	h.waitToast("Renamed to manifest-2.json")
	h.waitFollows()
	h.waitFor("new name chosen", func() bool { k, one := h.a.soleSelected(); return one && k == "manifest-2.json" })
	h.waitFor("its details", func() bool { return h.a.headKey == "manifest-2.json" && h.a.headState == loadReady })
}

// Restoring or deleting a version of one object reset the details, the drafts
// and the version selection of the object chosen while it ran.
func TestN1VersionActionsKeepOtherObjectState(t *testing.T) {
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
