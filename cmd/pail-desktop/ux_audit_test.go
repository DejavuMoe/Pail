package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The UX audit renders every view of the app at the largest and the smallest
// window, in both themes. TestDialogFootersReachableAtMinSize always runs and
// guards "a button is off screen or clipped at 960x600"; TestUXAudit writes
// the PNGs when PAIL_UI_SNAPSHOT_DIR is set (see docs/rewrite/DESIGN.md).

type auditSize struct {
	name string
	w, h int
}

var auditSizes = []auditSize{{"1440x900", 1440, 900}, {"960x600", 960, 600}}

// auditCase opens one view. footer lists the texts of buttons that must be on
// screen once it is open.
type auditCase struct {
	name   string
	setup  func(h *harness)
	footer []string
}

// resize sets the window size the headless tester lays out in.
func (h *harness) resize(w, ht int) {
	h.t.Helper()
	h.tt.SetSize(w, ht)
	h.settle()
}

// snapAudit writes name for both themes under dir/<size>/<theme>/.
func (h *harness) snapAudit(sz auditSize, name string) {
	h.t.Helper()
	base := os.Getenv("PAIL_UI_SNAPSHOT_DIR")
	if base == "" {
		return
	}
	was := h.a.opts.theme
	defer func() { h.a.opts.theme = was; h.tt.Frame() }()
	for _, th := range []string{"light", "dark"} {
		h.a.opts.theme = th
		h.tt.Frame()
		h.tt.Frame()
		dir := filepath.Join(base, sz.name, th)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			h.t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			h.t.Fatal(err)
		}
		if err := png.Encode(f, h.tt.Image()); err != nil {
			f.Close()
			h.t.Fatal(err)
		}
		f.Close()
	}
}

// expectOnScreen fails unless every text is shown inside the window.
func (h *harness) expectOnScreen(sz auditSize, texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		r, ok := h.tt.Find(s)
		if !ok {
			h.t.Errorf("%dx%d: %q is not on screen; have %q", sz.w, sz.h, s, h.tt.Texts())
			continue
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > float32(sz.w) || r.Y+r.H > float32(sz.h) {
			h.t.Errorf("%dx%d: %q at %+v is outside the window", sz.w, sz.h, s, r)
		}
		if r.W <= 0 || r.H <= 0 {
			h.t.Errorf("%dx%d: %q has an empty rect %+v", sz.w, sz.h, s, r)
		}
	}
}

// bucketSettingsCase opens bucket settings on one section.
func bucketSettingsCase(section string) auditCase {
	return auditCase{
		name: "bucket-settings-" + strings.ToLower(strings.ReplaceAll(section, " ", "-")),
		setup: func(h *harness) {
			h.a.openBucketSettings(bkt)
			h.settle()
			for i, s := range bsSections {
				if s.label() == section {
					h.a.mg.bs.section = i
				}
			}
			h.settle()
			h.waitFor("section read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == 2 || s.state == 3 })
			h.settle()
		},
		footer: []string{"Close"},
	}
}

func dialogCases() []auditCase {
	cs := []auditCase{
		{name: "account-add-aws", footer: []string{"Test connection", "Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(false)
			h.a.af.provider = 0
			h.settle()
		}},
		{name: "account-add-s3-advanced", footer: []string{"Test connection", "Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(false)
			h.a.af.provider = 1
			h.a.af.advanced = true
			h.settle()
		}},
		{name: "account-add-s3-advanced-scrolled", footer: []string{"Test connection", "Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(false)
			h.a.af.provider = 1
			h.a.af.advanced = true
			h.settle()
			h.tt.Scroll(480, 300, 0, -400) // the bar shows while the body moves
			h.tt.Frame()
		}},
		{name: "select-open", footer: []string{"Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(false)
			h.settle()
			h.mustClick("Access keys")
		}},
		{name: "bucket-empty-versioned", footer: []string{"Cancel", "Empty bucket"}, setup: func(h *harness) {
			h.a.openEmptyBucket("acme-versioned")
			h.waitFor("versioning known", func() bool { return h.a.versioned(h.a.accountID, "acme-versioned") })
			h.a.mg.eb.all = true
			h.settle()
		}},
		{name: "account-edit-cdn-errors", footer: []string{"Test connection", "Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(true)
			h.settle()
			h.a.af.advanced = true
			h.a.af.cdnURL, h.a.af.cdnBuckets = "ftp://cdn.example.com", "Not_A_Bucket"
			h.mustClick("Save")
		}},
		{name: "account-test-failed", footer: []string{"Test connection", "Cancel", "Save"}, setup: func(h *harness) {
			h.a.openAccountForm(false)
			h.a.af.provider = 1
			h.a.af.advanced = true
			h.a.af.test, h.a.af.testMsg = testFailed, "Can't reach the server. Check the connection and the endpoint"
			h.settle()
		}},
		{name: "chrome-about", footer: []string{"Close"}, setup: func(h *harness) { h.a.run("about", "test"); h.settle() }},
		{name: "chrome-shortcuts", footer: []string{"Close"}, setup: func(h *harness) { h.a.run("shortcuts", "test"); h.settle() }},
		{name: "bucket-create", footer: []string{"Cancel", "Create"}, setup: func(h *harness) { h.a.openNewBucket(); h.settle() }},
		{name: "bucket-create-error", footer: []string{"Cancel", "Create"}, setup: func(h *harness) {
			h.a.openNewBucket()
			h.a.mg.nb.name = "Bad_Name"
			h.a.mg.nb.err = "BucketAlreadyOwnedByYou: you already own this bucket"
			h.settle()
		}},
		{name: "bucket-delete", footer: []string{"Cancel", "Delete bucket"}, setup: func(h *harness) {
			h.a.openDeleteBucket("acme-backups")
			h.a.mg.db.err, h.a.mg.db.notEmpty = "The bucket you tried to delete is not empty", true
			h.settle()
		}},
		{name: "bucket-empty", footer: []string{"Cancel", "Empty bucket"}, setup: func(h *harness) { h.a.openEmptyBucket("acme-assets"); h.settle() }},
		{name: "delete-confirm", footer: []string{"Cancel", "Delete 2 items"}, setup: func(h *harness) {
			h.mustClick("README.txt")
			h.tt.ClickWith(ui.Shift, "manifest.json")
			h.settle()
			h.a.askDelete()
			h.settle()
		}},
		{name: "folder-new", footer: []string{"Cancel", "Create"}, setup: func(h *harness) { h.a.newFolder = true; h.settle() }},
		{name: "confirm-generic", footer: []string{"Cancel", "Remove"}, setup: func(h *harness) {
			h.a.confirm = confirmState{open: true, title: "Remove configuration?", rows: h.a.scopeRows(Scope{Bucket: bkt}), danger: true, action: "Remove",
				message: "The bucket goes back to the provider's default for this setting."}
			h.settle()
		}},
		{name: "storage-class", footer: []string{"Cancel", "Apply"}, setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.a.openRewriteSelection(false)
			h.settle()
		}},
		{name: "encryption", footer: []string{"Cancel", "Apply"}, setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.a.openRewriteSelection(true)
			h.settle()
		}},
		{name: "bucket-copy", footer: []string{"Cancel", "Copy contents"}, setup: func(h *harness) { h.a.openBucketCopy(bkt, false); h.settle() }},
		{name: "bucket-move-confirm", footer: []string{"Back", "Move everything"}, setup: func(h *harness) {
			h.a.openBucketCopy(bkt, true)
			h.settle()
			h.a.objOps.bc.confirming = true
			h.settle()
		}},
		{name: "signed-url-options", footer: []string{"Cancel", "Generate"}, setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.a.openSigned([]string{"manifest.json"}, "")
			h.settle()
		}},
		{name: "signed-url-result", footer: []string{"Close", "Copy"}, setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.a.openSigned([]string{"manifest.json"}, "")
			h.settle()
			h.mustClick("Generate")
			h.waitFor("signed", func() bool { return len(h.a.objOps.sg.res) > 0 })
			h.settle()
		}},
		{name: "signed-url-many", footer: []string{"Close", "Copy all"}, setup: func(h *harness) {
			h.a.openSigned([]string{"README.txt", "manifest.json", "assets/", "a.txt", "b.txt", "c.txt", "d.txt"}, "")
			h.settle()
			h.mustClick("Generate")
			h.waitFor("signed", func() bool { return len(h.a.objOps.sg.res) > 0 })
			h.settle()
		}},
	}
	for _, sec := range []string{"Versioning", "Encryption", "Public access", "Object ownership", "Requester pays", "Transfer acceleration", "Policy", "CORS", "Lifecycle", "Logging", "Tags", "Website", "Object Lock"} {
		cs = append(cs, bucketSettingsCase(sec))
	}
	return append(cs, vaultCases()...)
}

// TestDialogFootersReachableAtMinSize opens every dialog in the smallest
// window: each footer button must be fully inside it.
func TestDialogFootersReachableAtMinSize(t *testing.T) {
	sz := auditSizes[1]
	for _, c := range dialogCases() {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "light")
			h.resize(sz.w, sz.h)
			c.setup(h)
			h.tt.Frame()
			h.expectOnScreen(sz, c.footer...)
		})
	}
}

// TestUXAudit renders the whole app for visual review.
func TestUXAudit(t *testing.T) {
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("set PAIL_UI_SNAPSHOT_DIR to write the audit PNGs")
	}
	for _, sz := range auditSizes {
		t.Run(sz.name, func(t *testing.T) {
			for _, c := range append(browserCases(), append(dialogCases(), toolCases()...)...) {
				if only := os.Getenv("PAIL_UI_SNAPSHOT_ONLY"); only != "" && !strings.Contains(c.name, only) {
					continue // a name filter, to snapshot one area
				}
				t.Run(c.name, func(t *testing.T) {
					h := newHarness(t, "light")
					h.resize(sz.w, sz.h)
					h.tt.SetTitleBar(ui.TitleBar{Height: topBarH, Right: 138}) // Windows: three 46px buttons
					h.settle()
					c.setup(h)
					h.tt.Frame()
					h.expectOnScreen(sz, c.footer...)
					h.snapAudit(sz, c.name)
				})
			}
		})
	}
}

func seedTasks(h *harness) {
	now := time.Now()
	h.a.tasks = []Task{
		{ID: "t1", Kind: core.KindUpload, Title: "Upload quarterly-report-final-v3-with-a-very-long-name.pdf", Detail: "-> s3://acme-assets/reports/2026/", Status: core.StatusRunning, BytesTotal: 2400 << 20, BytesDone: 1600 << 20, Speed: 38 << 20, Created: now, Started: now},
		{ID: "t2", Kind: core.KindDownload, Title: "Download backup.tar.gz", Detail: "-> C:\\Users\\me\\Downloads", Status: core.StatusRunning, BytesTotal: 800 << 20, BytesDone: 120 << 20, Speed: 12 << 20, Created: now, Started: now},
		{ID: "t3", Kind: core.KindUpload, Title: "Upload photos (240 files)", Status: core.StatusQueued, Created: now},
		{ID: "t4", Kind: core.KindUpload, Title: "Upload notes.txt", Status: core.StatusSucceeded, BytesTotal: 4 << 10, BytesDone: 4 << 10, Created: now, Finished: now},
		{ID: "t5", Kind: core.KindUpload, Title: "Upload secrets.env", Status: core.StatusFailed, Err: "Access denied. Check the credentials and what they are allowed to do (AccessDenied)", Created: now, Finished: now},
		{ID: "t6", Kind: core.KindOther, Title: "Delete 12 items", Status: core.StatusPartiallyFailed, Items: 12, ItemsFailed: 3, Created: now, Finished: now},
	}
	h.a.reindexTasks()
	h.a.transfersOpen = true
	h.a.tl.tr = trAuto{init: true, seen: len(h.a.tasks), prevOpen: true}
	h.tt.Frame()
}

func browserCases() []auditCase {
	cs := []auditCase{
		{name: "browser-empty-selection", setup: func(h *harness) {}},
		{name: "browser-selected-file", setup: func(h *harness) { h.mustClick("manifest.json") }},
		{name: "browser-multi", setup: func(h *harness) {
			h.mustClick("README.txt")
			h.tt.ClickWith(ui.Shift, "manifest.json")
			h.settle()
		}},
		{name: "browser-folder-selected", setup: func(h *harness) { h.mustClick("assets") }},
		{name: "browser-no-match", setup: func(h *harness) { h.a.search = "zzzz"; h.a.rebuildRows(); h.settle() }},
		{name: "browser-inspector-closed", setup: func(h *harness) { h.a.inspectorOpen = false; h.settle() }},
		{name: "browser-toast", setup: func(h *harness) {
			h.a.toast("Bucket name copied")
			h.tt.Frame()
			h.tt.Frame()
		}},
		{name: "browser-context-menu", setup: func(h *harness) {
			if err := h.tt.RightClick("manifest.json"); err != nil {
				h.t.Fatal(err)
			}
			h.tt.Frame()
		}},
		{name: "browser-transfers", setup: func(h *harness) { seedTasks(h) }},
		{name: "browser-transfers-failed", setup: func(h *harness) { seedTasks(h); h.a.transTab = 2; h.tt.Frame() }},
		{name: "browser-transfers-completed", setup: func(h *harness) { seedTasks(h); h.a.transTab = 1; h.tt.Frame() }},
		{name: "browser-transfers-collapsed", setup: func(h *harness) {
			seedTasks(h)
			h.a.transfersOpen = false
			h.a.tl.tr.userCollapsed = true
			h.a.tl.tr.prevOpen = false
			h.tt.Frame()
		}},
		{name: "browser-versions-tab", setup: func(h *harness) { h.mustClick("manifest.json"); h.mustClick("Versions"); h.settle() }},
		{name: "browser-account-menu", setup: func(h *harness) { h.mustClick("Account") }},
		{name: "chrome-main", setup: func(h *harness) { h.mustClick("manifest.json") }},
		{name: "chrome-palette", setup: func(h *harness) { h.openPaletteWith(ui.Cmd, ui.KeyK) }},
		{name: "chrome-palette-query", setup: func(h *harness) {
			h.openPaletteWith(ui.Cmd, ui.KeyK)
			h.tt.Type("co")
			h.settle()
			h.tt.Key(0, ui.KeyDown)
			h.settle()
		}},
		{name: "chrome-palette-empty", setup: func(h *harness) { h.openPaletteWith(ui.Cmd, ui.KeyK); h.tt.Type("zzzz"); h.settle() }},
		{name: "chrome-more-menu", setup: func(h *harness) { h.mustClick("More") }},
	}
	for i, tab := range inspectorTabs {
		cs = append(cs, auditCase{name: fmt.Sprintf("inspector-%d-%s", i, strings.ToLower(tab.name)), setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.mustClick(tab.name)
			h.settle()
		}})
	}
	cs = append(cs,
		auditCase{name: "inspector-headers-dirty", setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.mustClick("Headers")
			h.mustClick("Edit Cache-Control")
			h.typeAndEnter("max-age=3600")
		}},
		auditCase{name: "rename-inline", setup: func(h *harness) {
			h.mustClick("manifest.json")
			h.a.startRename()
			h.settle()
		}},
	)
	return cs
}

func toolCases() []auditCase {
	open := func(id string) func(h *harness) {
		return func(h *harness) {
			h.openToolView(id)
			h.settle()
		}
	}
	return []auditCase{
		{name: "tool-sync-empty", setup: open("sync")},
		{name: "tool-sync-jobs", setup: func(h *harness) {
			for _, n := range []string{"Docs", "Photos backup to a very long destination name"} {
				if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: n, LocalDir: h.t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
					h.t.Fatal(err)
				}
			}
			h.openToolView("sync")
			h.a.syncSelect(0)
			h.settle()
		}},
		{name: "tool-sync-new", setup: func(h *harness) { h.openToolView("sync"); h.mustClick("New") }},
		{name: "sync-discard", footer: []string{"Cancel", "Discard"}, setup: func(h *harness) {
			if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "Docs", LocalDir: h.t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
				h.t.Fatal(err)
			}
			h.openToolView("sync")
			h.a.syncSelect(0)
			h.settle()
			h.a.tl.sync.f.name = "Docs v2"
			h.tt.Frame()
			h.mustClick("New")
		}},
		{name: "sync-confirm", footer: []string{"Cancel", "Delete"}, setup: func(h *harness) {
			if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "Docs", LocalDir: h.t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
				h.t.Fatal(err)
			}
			h.openToolView("sync")
			h.a.syncSelect(0)
			h.settle()
			h.a.syncAskDelete()
			h.settle()
		}},
		{name: "tool-cloudfront", setup: func(h *harness) {
			h.a.tl.cf.api = &fakeCF{}
			h.a.accounts = []Account{{ID: "aws1", Name: "Production AWS", Provider: core.ProviderAWS}}
			h.a.accountID = "aws1"
			h.a.scope = Scope{AccountID: "aws1", Bucket: "acme-assets"}
			h.openToolView("cloudfront")
			h.waitFor("distributions", func() bool { return len(h.a.tl.cf.invs) == 1 })
		}},
		{name: "cloudfront-invalidate", footer: []string{"Cancel", "Invalidate"}, setup: func(h *harness) {
			h.a.tl.cf.api = &fakeCF{}
			h.a.accounts = []Account{{ID: "aws1", Name: "Production AWS", Provider: core.ProviderAWS}}
			h.a.accountID = "aws1"
			h.a.scope = Scope{AccountID: "aws1", Bucket: "acme-assets"}
			h.openToolView("cloudfront")
			h.waitFor("distributions", func() bool { return len(h.a.tl.cf.invs) == 1 })
			h.mustClick("New invalidation…")
			h.waitFor("dialog", func() bool { return h.a.tl.cf.dlg.open && h.a.tl.cf.dlg.forBucket != nil })
			h.settle()
		}},
		{name: "tool-rules", setup: func(h *harness) { h.openToolView("rules"); h.mustClick("Add rule"); h.settle() }},
		{name: "rules-secrets", footer: []string{"Done"}, setup: func(h *harness) {
			h.openToolView("rules")
			h.mustClick("Manage secrets…")
			h.settle()
		}},
		{name: "tool-mime", setup: open("mime")},
		{name: "tool-multipart", setup: open("multipart")},
		{name: "tool-settings", setup: open("settings")},
	}
}
