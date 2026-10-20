package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/cloud"
)

func (h *harness) openToolView(id string) {
	h.t.Helper()
	h.tt.Frame() // the first frame enables the tool commands
	h.a.run("tool."+id, "test")
	h.settle()
	if h.a.tool != id {
		h.t.Fatalf("tool %q did not open (tool=%q)", id, h.a.tool)
	}
}

func TestToolsAreEnabledAndOpenFromTheSidebar(t *testing.T) {
	h := newHarness(t, "light")
	h.tt.Frame()
	for _, id := range []string{"sync", "cloudfront", "rules", "mime", "multipart", "settings"} {
		if !h.a.enabled(h.a.cmd("tool." + id)) {
			t.Errorf("tool %s is disabled", id)
		}
	}
	h.mustClick("Upload rules")
	if h.a.tool != "rules" {
		t.Fatalf("clicking Upload rules opened %q", h.a.tool)
	}
	h.mustHave("Manage secrets…", "Save", "Test")
	h.mustClick("Back")
	if h.a.tool != "" {
		t.Fatal("Back did not close the tool")
	}
}

// ---- upload rules ---------------------------------------------------------------------------

func TestRulesEditReorderSaveAndDraftResult(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("rules")
	rs := &h.a.tl.rules

	h.a.rulesAdd()
	rs.form.name, rs.form.key = "Images", "*.png"
	rs.form.headers = []ruleKV{{"Cache-Control", "max-age=60"}}
	rs.form.meta = []ruleKV{{"owner", "web"}}
	rs.form.class = "STANDARD_IA"
	h.tt.Frame()
	h.a.rulesAdd()
	rs.form.name, rs.form.key = "Logs", "logs/*"
	rs.form.comp, rs.form.level = "gzip", "6"
	h.tt.Frame()
	h.a.rulesMove(-1) // Logs first
	h.settle()
	if !rs.dirty() || len(rs.draft) != 2 || rs.draft[0].Name != "Logs" || rs.draft[1].Name != "Images" {
		t.Fatalf("draft after reorder: %+v dirty=%v", rs.draft, rs.dirty())
	}
	h.mustHave("Logs", "Images", "*.png", "Headers · STANDARD_IA", "GZIP 6")
	if len(h.a.svc.UploadRules()) != 0 {
		t.Fatal("editing must not save")
	}

	// The draft result evaluates the unsaved list.
	rs.testBucket, rs.testKey = "acme-assets", "hero.png"
	h.tt.Frame()
	want, ok := rs.draftResult()
	if !ok || !strings.Contains(want, "Cache-Control: max-age=60") || !strings.Contains(want, "STANDARD_IA") || !strings.Contains(want, "meta owner=web") {
		t.Fatalf("draft result = %q", want)
	}
	h.mustHave("Draft result", want)
	rs.testKey = "logs/a.txt"
	if got, _ := rs.draftResult(); !strings.Contains(got, "GZIP level 6") {
		t.Errorf("draft result for logs = %q", got)
	}
	h.snap("rules-editor-light")

	h.mustClick("Save")
	h.waitFor("rules saved", func() bool { return len(h.a.svc.UploadRules()) == 2 })
	h.settle()
	saved := h.a.svc.UploadRules()
	if saved[0].Name != "Logs" || saved[0].Compression == nil || saved[0].Compression.Method != "gzip" || saved[0].Compression.Level != 6 {
		t.Errorf("first saved rule = %+v", saved[0])
	}
	if saved[1].Headers["Cache-Control"] != "max-age=60" || saved[1].StorageClass != "STANDARD_IA" || saved[1].Metadata["owner"] != "web" {
		t.Errorf("second saved rule = %+v", saved[1])
	}
	if rs.dirty() {
		t.Error("still dirty after saving")
	}

	// Revert drops edits.
	rs.form.name = "Renamed"
	h.tt.Frame()
	if !rs.dirty() {
		t.Fatal("edit did not make the draft dirty")
	}
	h.mustClick("Revert")
	if rs.dirty() || rs.form.name != "Logs" {
		t.Errorf("revert left name %q dirty=%v", rs.form.name, rs.dirty())
	}

	// A rule the service rejects shows its error and saves nothing.
	rs.form.headers = []ruleKV{{"Expires", "tomorrow"}}
	h.tt.Frame()
	if rs.problem() == "" {
		t.Fatal("bad Expires was not flagged")
	}
	h.mustHave(rs.problem())
	h.mustClick("Save")
	h.waitFor("save error", func() bool { return rs.err != "" })
	if got := h.a.svc.UploadRules()[0]; got.Headers != nil {
		t.Errorf("invalid rule saved: %+v", got)
	}

	// Duplicate and delete, then the switch disables a rule.
	h.a.rulesRevert()
	h.a.rulesDuplicate()
	if len(rs.draft) != 3 || rs.draft[1].Name != "Logs copy" || rs.sel != 1 {
		t.Fatalf("duplicate: sel=%d %+v", rs.sel, rs.draft)
	}
	h.a.rulesDelete()
	if len(rs.draft) != 2 || rs.sel != 1 {
		t.Fatalf("delete: sel=%d len=%d", rs.sel, len(rs.draft))
	}
}

func TestSecretsAddDeleteAndReferenceSelect(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("rules")
	h.a.rulesAdd()
	h.settle()
	h.mustClick("Manage secrets…")
	h.mustHave("Secrets", "No secrets yet", "Done")
	s := &h.a.tl.secrets
	s.name, s.value = "backup-zip", "p4ss"
	h.snap("secrets-dialog-light")
	h.mustClick("Add")
	h.waitFor("secret saved", func() bool { return slices.Contains(h.a.svc.SecretRefs(), "backup-zip") })
	h.settle()
	h.mustHave("backup-zip")
	if s.name != "" || s.value != "" {
		t.Error("the fields were not cleared after adding")
	}
	// An empty value is refused before the service is asked.
	s.name = "x"
	h.mustClick("Add")
	if s.err != "Enter a value" {
		t.Errorf("error = %q", s.err)
	}
	// The reference is offered to rules.
	if got := h.a.refChoices("Choose…", ""); len(got) != 2 || got[1].Value != "backup-zip" {
		t.Errorf("ref choices = %+v", got)
	}

	h.mustClick("Delete backup-zip")
	if !h.a.confirm.open || h.a.confirm.title != "Delete secret?" {
		t.Fatalf("confirm = %+v", h.a.confirm)
	}
	h.mustHave("Delete secret?", "backup-zip")
	if len(h.a.svc.SecretRefs()) != 1 {
		t.Fatal("asking must not delete")
	}
	h.a.confirm.run()
	h.a.confirm.open = false
	h.waitFor("secret deleted", func() bool { return len(h.a.svc.SecretRefs()) == 0 })
	h.settle()
	h.mustHave("No secrets yet")
}

func TestRuleEncryptionAndZipPasswordReferences(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.a.svc.SetSecret("ssec-key", "x"); err != nil {
		t.Fatal(err)
	}
	h.openToolView("rules")
	h.a.rulesAdd()
	rs := &h.a.tl.rules
	rs.form.enc, rs.form.keyRef = "SSE-C", "ssec-key"
	rs.form.comp, rs.form.level, rs.form.pwRef = "zipaes", "-1", ""
	h.tt.Frame()
	if rs.problem() != "Choose a password reference for AES-256 ZIP" {
		t.Errorf("problem = %q", rs.problem())
	}
	rs.form.pwRef = "ssec-key"
	h.tt.Frame()
	if rs.problem() != "" {
		t.Errorf("problem = %q", rs.problem())
	}
	r := rs.draft[0]
	if r.SSE == nil || r.SSE.Type != "SSE-C" || r.SSE.CustomerKeyRef != "ssec-key" || r.Compression == nil || r.Compression.Method != "zip" || r.Compression.PasswordRef != "ssec-key" {
		t.Errorf("rule = %+v", r)
	}
	if f := formFromRule(r); f.comp != "zipaes" || f.enc != "SSE-C" {
		t.Errorf("round trip = %+v", f)
	}
	h.mustHave("Key reference", "Password", "AES-256 ZIP")
}

// ---- MIME types ---------------------------------------------------------------------------------

func TestMIMEAddEditDeleteSaveAndTest(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("mime")
	m := &h.a.tl.mime
	h.mustHave("No custom types", "Extension", "Content type")

	m.addExt, m.addType = "XYZ", "bad"
	h.mustClick("Add")
	if m.addErr == "" {
		t.Fatal("a type without a slash was accepted")
	}
	m.addType = "application/x-xyz"
	h.mustClick("Add")
	if len(m.rows) != 1 || m.rows[0].ext != ".xyz" || m.rows[0].typ != "application/x-xyz" {
		t.Fatalf("rows = %+v", m.rows)
	}
	m.addExt, m.addType = "xyz", "text/plain"
	h.mustClick("Add")
	if m.addErr == "" {
		t.Error("a duplicate extension was accepted")
	}
	m.addExt, m.addType, m.addErr = "", "", ""

	m.rows[0].typ = "application/x-xyz2" // inline edit
	m.testName = "movie.XYZ"
	h.settle()
	if got := m.draftType("movie.XYZ"); got != "application/x-xyz2" {
		t.Errorf("draft type = %q", got)
	}
	h.mustHave("Draft result", "application/x-xyz2")
	m.testName = "x.nosuchext"
	if got := m.draftType("x.nosuchext"); got != "application/octet-stream" {
		t.Errorf("fallback = %q", got)
	}
	m.testName = "a.xyz"
	h.snap("mime-light")
	if len(h.a.svc.MIMETypes()) != 0 {
		t.Fatal("editing must not save")
	}
	h.mustClick("Save")
	h.waitFor("saved", func() bool { return h.a.svc.MIMETypes()[".xyz"] == "application/x-xyz2" })
	h.settle()
	if got := h.a.svc.ContentTypeFor("a.xyz"); got != "application/x-xyz2" || m.dirty() {
		t.Errorf("service type %q, dirty %v", got, m.dirty())
	}

	h.mustClick("Delete .xyz")
	if len(m.rows) != 0 || !m.dirty() {
		t.Fatalf("rows after delete = %+v", m.rows)
	}
	h.mustClick("Save")
	h.waitFor("deleted", func() bool { return len(h.a.svc.MIMETypes()) == 0 })
}

// ---- multipart uploads ----------------------------------------------------------------------------

func startMultipart(t *testing.T, h *harness, key string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.env.Srv.URL+"/"+bkt+"/"+key+"?uploads", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=demo/20260101/us-east-1/s3/aws4_request")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("create multipart upload: %d", res.StatusCode)
	}
}

func TestMultipartListAndAbort(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("multipart")
	m := &h.a.tl.mp
	h.mustHave("No incomplete uploads", "Abort…")

	startMultipart(t, h, "big/one.bin")
	startMultipart(t, h, "big/two.bin")
	h.mustClick("Refresh")
	h.waitFor("uploads listed", func() bool { return len(m.items) == 2 })
	h.settle()
	h.mustHave("big/one.bin", "big/two.bin", "Initiated", "Upload ID")
	h.snap("multipart-light")

	for _, u := range m.items {
		m.sel.Add(u.UploadID)
	}
	h.tt.Frame()
	h.mustHave("2 selected")
	h.mustClick("Abort…")
	if !h.a.confirm.open || h.a.confirm.title != "Abort 2 uploads?" {
		t.Fatalf("confirm = %+v", h.a.confirm)
	}
	h.mustHave("Abort 2 uploads?", bkt)
	h.a.confirm.run()
	h.a.confirm.open = false
	h.waitFor("list emptied after the abort", func() bool { return m.loaded && len(m.items) == 0 && m.abortTask == "" })
	h.settle()
	h.mustHave("No incomplete uploads")
	if !h.a.transfersOpen {
		t.Error("the transfers panel did not open for the abort task")
	}
}

func TestMultipartNeedsABucketSelection(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openTool("multipart")
	h.a.scope = Scope{AccountID: h.a.accountID}
	h.a.tl.active = ""
	h.settle()
	h.mustHave("Choose a bucket")
}

// ---- CloudFront ---------------------------------------------------------------------------------------

func TestCloudFrontNeedsAnAWSAccount(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("cloudfront")
	h.mustHave("CloudFront is available for AWS accounts")
	h.mustNotHave("New invalidation…")
	h.snap("cloudfront-empty-light")
}

type fakeCF struct {
	mu     sync.Mutex
	status string
	made   bool
	paths  [][]string
}

var cfDists = []cloud.Distribution{
	{ID: "E1ABC", DomainName: "d1.cloudfront.net", Status: "Deployed", Enabled: true, Aliases: []string{"cdn.acme.test"}},
	{ID: "E2DEF", DomainName: "d2.cloudfront.net", Status: "Deployed", Enabled: false},
}

func (f *fakeCF) Distributions(context.Context, string) ([]cloud.Distribution, error) {
	return cfDists, nil
}

func (f *fakeCF) DistributionsForBucket(_ context.Context, _, bucket string) ([]cloud.Distribution, error) {
	return cfDists[:1], nil
}

func (f *fakeCF) Invalidate(_ context.Context, _, dist string, paths []string) (cloud.Invalidation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made, f.status = true, "InProgress"
	f.paths = append(f.paths, paths)
	return cloud.Invalidation{ID: "INEW1", DistributionID: dist, Status: "InProgress", CreateTime: time.Now()}, nil
}

func (f *fakeCF) Invalidations(_ context.Context, _, dist string) ([]cloud.Invalidation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []cloud.Invalidation{{ID: "IOLD1", DistributionID: dist, Status: "Completed", CreateTime: time.Now().Add(-time.Hour)}}
	if f.made {
		out = append([]cloud.Invalidation{{ID: "INEW1", DistributionID: dist, Status: f.status, CreateTime: time.Now()}}, out...)
	}
	return out, nil
}

func (f *fakeCF) set(status string) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func TestCloudFrontInvalidationFlowWithAFake(t *testing.T) {
	h := newHarness(t, "light")
	fake := &fakeCF{}
	h.a.tl.cf.api = fake
	h.a.accounts = []Account{{ID: "aws1", Name: "Production AWS", Provider: core.ProviderAWS}}
	h.a.accountID = "aws1"
	h.a.scope = Scope{AccountID: "aws1", Bucket: "acme-assets"}
	h.openToolView("cloudfront")
	cf := &h.a.tl.cf
	h.waitFor("distributions", func() bool { return len(cf.invs) == 1 })
	h.mustHave("E1ABC", "d1.cloudfront.net", "cdn.acme.test", "E2DEF", "Disabled", "IOLD1", "Completed", "New invalidation…")
	h.snap("cloudfront-light")

	h.mustClick("New invalidation…")
	h.waitFor("whole-bucket helper", func() bool { return cf.dlg.forBucket["E1ABC"] })
	h.mustHave("New invalidation", "Invalidate whole bucket", "Production AWS")
	if secondary := h.tt.HasText("Invalidate whole bucket"); !secondary {
		t.Fatal("helper missing")
	}
	h.mustClick("Invalidate whole bucket")
	if cf.dlg.paths != "/*" {
		t.Fatalf("paths = %q", cf.dlg.paths)
	}
	h.mustClick("Invalidate")
	h.waitFor("result", func() bool { return cf.dlg.result != nil })
	h.settle()
	h.mustHave("Invalidation INEW1 · InProgress")
	if len(fake.paths) != 1 || fake.paths[0][0] != "/*" {
		t.Fatalf("paths sent = %v", fake.paths)
	}
	h.waitFor("new row", func() bool { return len(cf.invs) == 2 })

	// Polling: the status turns Completed on the next tick.
	fake.set("Completed")
	h.a.cfTick()
	h.waitFor("status polled", func() bool { return cf.dlg.result.Status == "Completed" && cf.invs[0].Status == "Completed" })
	h.mustClick("Close")
	if cf.dlg.open {
		t.Error("dialog still open")
	}

	// The helper only appears for distributions of the bucket.
	cf.cursor = 1
	h.settle()
	h.a.cfOpenDialog("")
	h.waitFor("dialog", func() bool { return cf.dlg.open && cf.dlg.forBucket != nil })
	h.settle()
	h.mustNotHave("Invalidate whole bucket")
	cf.dlg.open = false

	// Opened with a browser selection.
	h.a.openInvalidation(invalidationPaths([]string{"a.txt", "dir/"}))
	h.settle()
	if cf.dlg.paths != "/a.txt\n/dir/*" {
		t.Errorf("prefill = %q", cf.dlg.paths)
	}
}

// ---- sync jobs ---------------------------------------------------------------------------------------------

func waitTasksDone(h *harness) {
	h.t.Helper()
	h.waitFor("tasks finished", func() bool {
		if len(h.a.tasks) == 0 {
			return false
		}
		for _, tk := range h.a.tasks {
			if !tk.Status.Finished() {
				return false
			}
		}
		return true
	})
}

func TestSyncJobPreviewRunAndMirrorWarning(t *testing.T) {
	h := newHarness(t, "light")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bravo bravo"), 0o600)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o700)
	os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("charlie"), 0o600)
	os.WriteFile(filepath.Join(dir, "skip.tmp"), []byte("junk"), 0o600)

	h.openToolView("sync")
	s := &h.a.tl.sync
	h.mustHave("No sync jobs", "Create a sync job")
	h.mustClick("New")
	h.mustHave("Direction", "Local folder", "Excludes", "Preview", "Run sync", "Choose…")
	s.f.name, s.f.dir = "Site", dir
	s.f.acct, s.f.bucket, s.f.prefix = h.env.Account.ID, "acme-website", "sync-it/"
	s.f.excludes = "*.tmp"
	h.tt.Frame()
	h.snap("sync-editor-light")
	h.mustClick("Save")
	h.waitFor("job saved", func() bool { return len(h.a.svc.SyncJobs()) == 1 && !s.hasDraft })
	h.settle()
	saved := h.a.svc.SyncJobs()[0]
	if saved.Name != "Site" || saved.Direction != "upload" || saved.Remote.Bucket != "acme-website" || len(saved.Excludes) != 1 {
		t.Fatalf("saved job = %+v", saved)
	}
	h.mustHave("Site")
	if s.dirty() {
		t.Error("dirty right after saving")
	}

	// Preview lists the three files; the excluded one is skipped.
	h.mustClick("Preview")
	h.waitFor("preview", func() bool { return s.pv != nil })
	h.settle()
	if s.pv.Uploads != 3 || s.pv.Deletes != 0 {
		t.Fatalf("preview = uploads %d deletes %d: %+v", s.pv.Uploads, s.pv.Deletes, s.pv.Actions)
	}
	h.mustHave("3 uploads", "0 deletes", "a.txt", "sub/c.txt", "All", "Changes", "Deletes", "Skipped")
	h.snap("sync-preview-light")

	// Filters.
	h.mustClick("Skipped")
	if len(s.shown) != s.pv.Skips || (s.pv.Skips > 0 && s.shown[0].Op != "skip") {
		t.Errorf("skipped filter shows %+v", s.shown)
	}
	h.mustClick("All")

	// Editing the job makes the preview stale.
	s.f.prefix = "other/"
	h.tt.Frame()
	if s.pv != nil {
		t.Fatal("the preview survived an edit")
	}
	h.mustHave("Save")
	s.f.prefix = "sync-it/"
	h.mustClick("Preview")
	h.waitFor("preview again", func() bool { return s.pv != nil })

	// Run asks first and shows what it will do.
	h.mustClick("Run sync")
	if !h.a.confirm.open || h.a.confirm.title != "Run sync?" || h.a.confirm.danger {
		t.Fatalf("confirm = %+v", h.a.confirm)
	}
	h.mustHave("Run sync?", "3 files", "s3://acme-website/sync-it/")
	if _, ok := h.env.Srv.Object("acme-website", "sync-it/a.txt"); ok {
		t.Fatal("asking must not upload")
	}
	h.a.confirm.run()
	h.a.confirm.open = false
	h.waitFor("objects uploaded", func() bool {
		_, ok := h.env.Srv.Object("acme-website", "sync-it/sub/c.txt")
		return ok
	})
	waitTasksDone(h)
	if b, _ := h.env.Srv.Object("acme-website", "sync-it/b.txt"); string(b) != "bravo bravo" {
		t.Errorf("b.txt = %q", b)
	}
	if _, ok := h.env.Srv.Object("acme-website", "sync-it/skip.tmp"); ok {
		t.Error("the excluded file was uploaded")
	}
	if !h.a.transfersOpen {
		t.Error("the transfers panel did not open")
	}

	// A second preview has nothing to do.
	h.mustClick("Preview")
	h.waitFor("clean preview", func() bool { return s.pv != nil })
	h.settle()
	if s.pv.Uploads+s.pv.Downloads+s.pv.Deletes != 0 {
		t.Fatalf("second preview is not clean: %+v", s.pv.Actions)
	}

	// Mirror mode: the extra object is deleted, and the dialog says so.
	h.env.Srv.PutObject("acme-website", "sync-it/extra.txt", []byte("stale"), "text/plain", nil)
	s.f.mode = "mirror"
	h.tt.Frame()
	h.mustHave("Deletes extra files at the destination")
	h.mustClick("Preview")
	h.waitFor("mirror preview", func() bool { return s.pv != nil })
	h.settle()
	if s.pv.Deletes != 1 {
		t.Fatalf("mirror preview deletes = %d: %+v", s.pv.Deletes, s.pv.Actions)
	}
	h.mustHave("1 delete")
	h.mustClick("Deletes")
	if len(s.shown) != 1 || s.shown[0].Path != "extra.txt" {
		t.Fatalf("deletes filter shows %+v", s.shown)
	}
	h.mustClick("Run sync")
	if !h.a.confirm.danger {
		t.Error("a sync with deletes must use the danger button")
	}
	h.mustHave("1 file will be deleted at the destination")
	h.snap("sync-confirm-deletes-light")
	h.a.confirm.run()
	h.a.confirm.open = false
	h.waitFor("extra object deleted", func() bool {
		_, ok := h.env.Srv.Object("acme-website", "sync-it/extra.txt")
		return !ok
	})
	waitTasksDone(h)
}

func TestSyncDownloadDeleteConfirmMentionsTrash(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("sync")
	s := &h.a.tl.sync
	h.a.syncNew()
	s.f.dir, s.f.up, s.f.mode = t.TempDir(), 1, "mirror"
	s.f.acct, s.f.bucket = h.env.Account.ID, "acme-website"
	os.WriteFile(filepath.Join(s.f.dir, "local-only.txt"), []byte("x"), 0o600)
	h.settle()
	h.mustClick("Preview")
	h.waitFor("preview", func() bool { return s.pv != nil })
	h.settle()
	if s.pv.Deletes != 1 || s.pv.Downloads == 0 {
		t.Fatalf("preview = %+v", s.pv)
	}
	h.mustClick("Run sync")
	h.mustHave("1 file will be deleted at the destination", "moved to the "+trashName())
}

func TestSyncDeleteJobAsksFirst(t *testing.T) {
	h := newHarness(t, "light")
	if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "J", LocalDir: t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
		t.Fatal(err)
	}
	h.openToolView("sync")
	h.mustHave("J")
	h.mustClick("Delete")
	if !h.a.confirm.open || len(h.a.svc.SyncJobs()) != 1 {
		t.Fatal("delete must ask first")
	}
	h.a.confirm.run()
	h.a.confirm.open = false
	h.waitFor("job deleted", func() bool { return len(h.a.svc.SyncJobs()) == 0 })
}

// ---- transfers panel ---------------------------------------------------------------------------------------

func TestTransfersCollapseWithoutTasksAndExpandOnANewTask(t *testing.T) {
	h := newHarness(t, "light")
	if h.a.transfersOpen {
		t.Fatal("the panel is open with no tasks")
	}
	h.mustHave("Transfers", "0 active · 0 completed · 0 failed")
	h.snap("transfers-collapsed-light")

	upload := func(name string) {
		_, err := h.a.svc.Upload(core.UploadRequest{Dest: h.a.scope, Paths: []string{writeTemp(t, name, []byte(name))}, Conflict: core.ConflictOverwrite})
		if err != nil {
			t.Fatal(err)
		}
	}
	upload("one.txt") // bypasses the UI, which would open the panel itself
	h.waitFor("panel opened by the new task", func() bool { return h.a.transfersOpen })
	h.mustHave("Active", "Completed")
	waitTasksDone(h)

	// A manual collapse sticks while tasks exist, even when more arrive.
	h.a.transfersOpen = false
	h.tt.Frame()
	before := len(h.a.tasks)
	upload("two.txt")
	h.waitFor("second upload", func() bool { return len(h.a.tasks) > before })
	waitTasksDone(h)
	if h.a.transfersOpen {
		t.Error("a new task re-opened a panel the user collapsed")
	}

	// Clearing the list collapses it; the next task opens it again.
	h.a.transfersOpen = true
	h.tt.Frame()
	h.a.clearFinished()
	h.waitFor("tasks cleared", func() bool { return len(h.a.tasks) == 0 })
	h.tt.Frame()
	if h.a.transfersOpen {
		t.Error("the panel stayed open with no tasks")
	}
	upload("three.txt")
	h.waitFor("reopened", func() bool { return h.a.transfersOpen })
}

func TestSyncUnsavedEditsAskBeforeLeaving(t *testing.T) {
	h := newHarness(t, "light")
	if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "Docs", LocalDir: t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
		t.Fatal(err)
	}
	h.openToolView("sync")
	s := &h.a.tl.sync
	// No edits: New switches at once.
	h.mustClick("New")
	if s.dsc.open || !s.hasDraft {
		t.Fatalf("New with no edits asked: dsc=%v draft=%v", s.dsc.open, s.hasDraft)
	}
	h.a.syncSelect(0)
	h.settle()
	s.f.name = "Docs v2"
	h.tt.Frame()
	h.mustClick("New")
	h.mustHave("Discard changes to Docs v2?", "Cancel", "Discard", "Save")
	h.snap("sync-discard-light")
	if s.hasDraft {
		t.Fatal("switched before the user chose")
	}
	h.mustClick("Cancel")
	if s.dsc.open || s.f.name != "Docs v2" || s.hasDraft {
		t.Fatalf("Cancel must keep the edits: %+v", s.f)
	}
	h.mustClick("New")
	h.mustClick("Discard")
	if s.dsc.open || !s.hasDraft || s.f.name != "" {
		t.Fatalf("Discard must switch to the new draft: draft=%v name=%q", s.hasDraft, s.f.name)
	}
	if got := h.a.svc.SyncJobs()[0].Name; got != "Docs" {
		t.Fatalf("Discard saved the edit: %q", got)
	}
	// Back with edits asks too; Save stores them and then leaves.
	s.f.name, s.f.dir, s.f.acct, s.f.bucket = "Draft", t.TempDir(), h.env.Account.ID, "acme-website"
	h.tt.Frame()
	h.mustClick("Back")
	h.mustHave("Discard changes to Draft?")
	// The dialog's Save sits right of Discard (the editor behind has a Save too).
	r, ok := h.tt.Find("Discard")
	if !ok {
		t.Fatal("no Discard button")
	}
	h.tt.ClickAt(r.X+r.W+50, r.Y+r.H/2)
	h.settle()
	h.waitFor("saved and left", func() bool { return len(h.a.svc.SyncJobs()) == 2 && h.a.tool == "" })
}

func TestInvalidateOnlyForAWSAccounts(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if contains(h.menu(), "Invalidate…") {
		t.Fatalf("Invalidate offered for a non-AWS account: %q", h.menu())
	}
	h.closeMenu()
	h.settle()
	h.a.accounts = []Account{{ID: h.a.accountID, Name: "Production AWS", Provider: core.ProviderAWS}}
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if !contains(h.menu(), "Invalidate…") {
		t.Fatalf("Invalidate missing for an AWS account: %q", h.menu())
	}
	h.closeMenu()
	h.a.run("invalidate", "test")
	if h.a.tool != "cloudfront" {
		t.Fatalf("tool = %q", h.a.tool)
	}
	if p := h.a.tl.cf.pending; p == nil || *p != "/manifest.json" {
		t.Fatalf("prefill = %v", p)
	}
}

func TestRenameSelectsTheBaseName(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.a.run("rename", "test")
	h.tt.Frame()
	h.tt.Frame()
	h.snap("rename-selection-light")
	if got := renameStem(Entry{Name: "manifest.json"}); got != "manifest" {
		t.Errorf("stem = %q", got)
	}
	if got := renameStem(Entry{Name: "a.tar.gz"}); got != "a.tar" {
		t.Errorf("stem = %q", got)
	}
	if got := renameStem(Entry{Name: ".env"}); got != ".env" {
		t.Errorf("stem = %q", got)
	}
	if got := renameStem(Entry{Name: "v1.2", Folder: true}); got != "v1.2" {
		t.Errorf("stem = %q", got)
	}
}
