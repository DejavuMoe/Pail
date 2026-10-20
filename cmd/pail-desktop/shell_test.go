package main

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

const testW, testH = 1440, 900

// harness drives the real app against the demo environment: an s3test server
// and a Service, no network beyond loopback.
type harness struct {
	t   *testing.T
	a   *app
	tt  *ui.Tester
	env *demoEnv
}

// newRawHarness builds the app over a demo environment without opening a
// bucket; seed false leaves the server empty.
func newRawHarness(t *testing.T, theme string, seed bool) *harness {
	t.Helper()
	env, err := newDemoEnv(t.TempDir(), seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	return attach(t, env, theme)
}

func attach(t *testing.T, env *demoEnv, theme string) *harness {
	t.Helper()
	a := newApp(env.Svc, options{theme: theme, width: testW, height: testH, demo: true})
	t.Cleanup(a.close)
	t.Cleanup(func() { testLocale.Store("en") })
	a.localeSet = func(tag string) {
		if tag == "" {
			tag = "en"
		}
		testLocale.Store(tag)
	}
	h := &harness{t: t, a: a, env: env, tt: ui.NewTester(a.view, testW, testH)}
	h.settle()
	return h
}

// newHarness opens acme-assets, the bucket most tests browse.
func newHarness(t *testing.T, theme string) *harness {
	t.Helper()
	h := newRawHarness(t, theme, true)
	h.a.openBucket("acme-assets")
	h.settle()
	return h
}

// settle lets background loads finish, drawing frames as results arrive.
func (h *harness) settle() {
	h.t.Helper()
	for i := 0; i < 8000; i++ { // a refused connection is retried for a few seconds
		h.tt.Frame()
		if h.a.idle() && len(h.a.pending) == 0 {
			h.tt.Frame()
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("UI never settled; loading=%v bucketsLoad=%v head=%d prev=%d busyOps=%d", h.a.loading, h.a.bucketsLoad, h.a.headState, h.a.prev.state, h.a.busyOps.Load())
}

// waitFor draws frames until cond holds.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	for i := 0; i < 2500; i++ {
		h.tt.Frame()
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; texts %q", what, h.tt.Texts())
}

func (h *harness) mustClick(text string) {
	h.t.Helper()
	h.tt.Frame() // the click lands where the latest state laid the element out
	if err := h.tt.Click(text); err != nil {
		h.t.Fatalf("click %q: %v", text, err)
	}
	h.settle()
}

func (h *harness) mustHave(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Errorf("missing text %q; have %q", s, h.tt.Texts())
		}
	}
}

func (h *harness) mustNotHave(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if h.tt.HasText(s) {
			h.t.Errorf("unexpected text %q", s)
		}
	}
}

func (h *harness) has(key string) bool {
	_, ok := h.a.byKey[key]
	return ok
}

func (h *harness) snap(name string) {
	h.t.Helper()
	dir := os.Getenv("PAIL_UI_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.tt.Frame()
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, h.tt.Image()); err != nil {
		h.t.Fatal(err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.TrimSpace(x) == s {
			return true
		}
	}
	return false
}

// ---- browsing -------------------------------------------------------------------------------

func TestShellRendersKeyTexts(t *testing.T) {
	h := newRawHarness(t, "light", true)
	// The first bucket opens by itself.
	if got := h.a.scope.Bucket; got != "acme-analytics" {
		t.Fatalf("first bucket not opened, scope=%+v", h.a.scope)
	}
	h.mustHave(demoAccountName, "BUCKETS", "TOOLS", "acme-assets", "acme-backups", "us-west-2", "eu-west-1", "ap-southeast-2",
		"Sync jobs", "CloudFront", "Upload rules", "MIME types", "Multipart uploads", "Settings",
		"Name", "Size", "Modified", "Storage class", "README.md", "Transfers", "Overview", "Current folder")
	h.mustClick("acme-assets")
	h.mustHave("assets", "website", "README.txt", "manifest.json", "Standard")
	h.snap("main-light")
	if err := h.tt.Click("acme-assets"); err != nil {
		t.Fatal(err)
	}
}

func TestDoubleClickFolderAndBreadcrumbUp(t *testing.T) {
	h := newHarness(t, "light")
	h.tt.Click("assets")
	h.tt.Click("assets")
	h.settle()
	if h.a.scope.Prefix != "assets/" {
		t.Fatalf("prefix = %q, want assets/ (selected=%v)", h.a.scope.Prefix, h.a.selectedKeys())
	}
	h.mustHave("images", "fonts", "video")
	h.a.openPrefix("assets/images/")
	h.settle()
	// Breadcrumb: bucket > assets > images; clicking "assets" goes up one level.
	h.mustClick("assets")
	if h.a.scope.Prefix != "assets/" {
		t.Fatalf("breadcrumb click left prefix %q", h.a.scope.Prefix)
	}
}

func TestBackspaceGoesUp(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openPrefix("assets/images/")
	h.settle()
	h.tt.Key(0, ui.KeyBackspace)
	h.settle()
	if h.a.scope.Prefix != "assets/" {
		t.Fatalf("prefix = %q after Backspace", h.a.scope.Prefix)
	}
}

func TestMultiSelectUpdatesStatusBar(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	if n := h.a.sel.Len(); n != 2 {
		t.Fatalf("selection = %d, want 2", n)
	}
	total := h.a.rows[h.a.byKey["README.txt"]].Size + h.a.rows[h.a.byKey["manifest.json"]].Size
	h.mustHave("2 selected, "+humanBytes(total), "2 items · "+humanBytes(total))
}

func TestBigFolderPagesToTheEnd(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.ResetRequests()
	h.a.openPrefix("logs/access/")
	h.settle()
	if got := len(h.a.rows); got != 5000 {
		t.Fatalf("rows = %d, want 5000 (all pages loaded)", got)
	}
	pages := 0
	for _, r := range h.env.Srv.Requests() {
		if strings.Contains(r.RawQuery, "list-type=2") && strings.Contains(r.RawQuery, "logs") {
			pages++
		}
	}
	if pages < 5 {
		t.Errorf("listing used %d requests, want at least 5 pages of 1000", pages)
	}
	h.mustHave("5,000 items", "access-0000.log.gz")
	if h.tt.HasText("access-4999.log.gz") {
		t.Error("the whole 5,000-row table was built; the table is not virtualised")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.settle()
	if h.a.sel.Len() != 5000 {
		t.Errorf("select all = %d", h.a.sel.Len())
	}
	h.mustHave("5000 selected")
}

func TestStaleListingIsDiscarded(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openPrefix("logs/access/")
	h.a.openPrefix("assets/") // the user moved on before the first answer
	h.settle()
	if h.a.scope.Prefix != "assets/" || len(h.a.rows) != 5 {
		t.Fatalf("scope %q has %d rows; a stale page leaked in", h.a.scope.Prefix, len(h.a.rows))
	}
	for _, e := range h.a.rows {
		if strings.HasPrefix(e.Key, "logs/") {
			t.Fatalf("stale entry %q", e.Key)
		}
	}
}

func TestSearchFiltersLoadedEntries(t *testing.T) {
	h := newHarness(t, "light")
	h.a.search = "manifest"
	h.a.rebuildRows()
	h.tt.Frame()
	if len(h.a.rows) != 1 || h.a.rows[0].Name != "manifest.json" {
		t.Fatalf("filtered rows = %v", h.a.rows)
	}
	h.mustHave("1 item of")
}

func TestSortKeepsFoldersFirst(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("Size")
	h.mustClick("Size")
	if !h.a.rows[0].Folder {
		t.Fatalf("first row after sorting by size descending is %q, folders must stay first", h.a.rows[0].Key)
	}
	last := int64(-1)
	for _, e := range h.a.rows {
		if e.Folder {
			continue
		}
		if last >= 0 && e.Size > last {
			t.Fatalf("not sorted descending: %d after %d", e.Size, last)
		}
		last = e.Size
	}
}

func TestDarkModeRenders(t *testing.T) {
	h := newHarness(t, "dark")
	if !h.tt.HasText("acme-assets") {
		t.Fatal("dark shell missing content")
	}
	h.snap("main-dark")
	// A system dark appearance also applies in "system" mode.
	s := newHarness(t, "system")
	s.tt.SetDark(true)
	s.tt.Frame()
	r, g, b, _ := s.tt.Image().At(700, 300).RGBA()
	if r>>8 > 0x40 || g>>8 > 0x40 || b>>8 > 0x40 {
		t.Errorf("system dark did not darken the main area: %x %x %x", r>>8, g>>8, b>>8)
	}
}

func TestSelectionCommandsFollowTheSelection(t *testing.T) {
	// (every tool is wired and enabled: see tools_test.go)
	h := newHarness(t, "light")
	for _, id := range []string{"copy", "cut", "paste", "rename", "download", "delete"} {
		if h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s must be disabled without a selection or clipboard", id)
		}
	}
	if !h.a.enabled(h.a.cmd("bucketsettings")) || !h.a.enabled(h.a.cmd("newbucket")) {
		t.Error("bucket commands must be enabled while a bucket is open")
	}
	h.mustClick("manifest.json")
	for _, id := range []string{"copy", "cut", "rename", "download", "delete"} {
		if !h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s must be enabled with an object selected", id)
		}
	}
	if h.a.enabled(h.a.cmd("paste")) {
		t.Error("paste needs something on the clipboard")
	}
}

func TestContextMenuFromCommandTable(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	menu := h.menu()
	for _, want := range []string{"Download", "Copy key", "Copy URI", "Copy URL", "Copy signed URL…", "Delete", "Refresh", "Select all"} {
		if !contains(menu, want) {
			t.Errorf("context menu %q missing %q", menu, want)
		}
	}
	if keys := h.a.selectedKeys(); len(keys) != 1 || keys[0] != "manifest.json" {
		t.Errorf("right click did not select the row under the pointer: %v", keys)
	}
	h.snap("context-menu-light")
	h.closeMenu()
	h.settle()
}

func TestBucketListErrorHasRetry(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
		}
		return nil
	})
	h.a.loadBuckets(false)
	h.settle()
	h.mustHave("Access denied", "Retry")
	h.env.Srv.Inject(nil)
	h.mustClick("Retry")
	h.mustHave("acme-assets")
	h.mustNotHave("Access denied")
}

func TestListErrorShowsRetry(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "list-type=2") && strings.Contains(r.URL.RawQuery, "reports") {
			return &s3test.Fault{Status: 404, Code: "NoSuchBucket", Message: "gone"}
		}
		return nil
	})
	h.a.openPrefix("reports/")
	h.settle()
	h.mustHave("Could not list acme-assets", "Not found", "Retry")
	h.env.Srv.Inject(nil)
	h.mustClick("Retry")
	h.mustHave("customers-export.csv")
}
