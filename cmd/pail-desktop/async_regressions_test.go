package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/cloud"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

// Take a real UI completion out of the queue so the test chooses its order.
func nextCompletion(t *testing.T, a *app) func() {
	t.Helper()
	select {
	case f := <-a.pending:
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("missing UI completion")
		return nil
	}
}

func refuseConfigWrite(t *testing.T, h *harness, name string) {
	t.Helper()
	p := filepath.Join(h.env.Dir, name)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsLatestIntentSurvivesPendingSave(t *testing.T) {
	h := newHarness(t, "light")
	h.a.loadSettingsForm()
	initial := h.a.svc.Settings()
	h.a.settingsMu.Lock()
	h.a.sf.conflict = string(core.ConflictOverwrite)
	h.a.commitConflict()
	h.a.sf.conflict = string(initial.Conflict)
	h.a.commitConflict()
	h.a.sf.part = "32"
	h.a.commitNumber(numSettings[4])
	h.a.sf.part = "16"
	h.a.commitNumber(numSettings[4])
	h.a.sf.dir = t.TempDir()
	h.a.commitDir()
	h.a.sf.dir = initial.DefaultDownloadDir
	h.a.commitDir()
	h.a.setKeepRecentOps(true)
	h.a.settingsMu.Unlock()
	h.settle()
	got := h.a.svc.Settings()
	if got.Conflict != initial.Conflict || got.PartSizeMiB != initial.PartSizeMiB || got.DefaultDownloadDir != initial.DefaultDownloadDir || !got.KeepRecentOps {
		t.Fatalf("latest settings intent lost: initial=%+v saved=%+v", initial, got)
	}
	reopened, err := core.New(core.Options{ConfigDir: h.env.Dir, Keyring: core.NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	if st := reopened.Settings(); st.Conflict != got.Conflict || st.PartSizeMiB != got.PartSizeMiB || st.DefaultDownloadDir != got.DefaultDownloadDir || !st.KeepRecentOps {
		t.Fatalf("disk differs from saved settings: %+v", st)
	}
}

func TestSettingsStaleFailureDoesNotRevertLaterIntent(t *testing.T) {
	h := newHarness(t, "light")
	h.a.loadSettingsForm()
	refuseConfigWrite(t, h, "settings.json")
	h.a.sf.conflict = string(core.ConflictOverwrite)
	h.a.commitConflict()
	old := nextCompletion(t, h.a)
	if err := os.Remove(filepath.Join(h.env.Dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	h.a.sf.conflict = string(core.ConflictSkip)
	h.a.commitConflict()
	nextCompletion(t, h.a)()
	old()
	if h.a.sf.conflict != string(core.ConflictSkip) || h.a.sf.errs["conflict"] != "" || h.a.svc.Settings().Conflict != core.ConflictSkip {
		t.Fatalf("stale failure reverted a later save: form=%q err=%q saved=%q", h.a.sf.conflict, h.a.sf.errs["conflict"], h.a.svc.Settings().Conflict)
	}
}

func TestSettingsImmediateChoicesKeepLatestIntent(t *testing.T) {
	h := newHarness(t, "light")
	h.a.loadSettingsForm()
	h.a.settingsMu.Lock()
	for _, on := range []bool{true, false, true, false} {
		h.a.setTheme(map[bool]string{true: "dark", false: "light"}[on])
		h.a.setLogToFile(on)
		h.a.setKeepRecentOps(on)
		h.a.setTrayShown(on)
		h.a.setQuitWhenIdle(on)
		h.a.setCollapseTools(on)
	}
	h.a.setAutoLock(30)
	h.a.setAutoLock(0)
	h.a.settingsMu.Unlock()
	h.settle()
	st := h.a.svc.Settings()
	if st.Theme != "light" || st.LogToFile || st.KeepRecentOps || !st.HideTrayIcon || st.QuitWhenIdle || st.CollapseTools || st.VaultAutoLockMinutes != 0 {
		t.Fatalf("quick choices were saved out of order: %+v", st)
	}
}

func TestMultipartReentryRejectsOldResult(t *testing.T) {
	h := newHarness(t, "light")
	startMultipart(t, h, "old.bin")
	h.a.mpEnter()
	old := nextCompletion(t, h.a)
	h.a.scope.Bucket = "acme-backups"
	h.a.mpEnter()
	nextCompletion(t, h.a)()
	old()
	if m := h.a.tl.mp; m.loc.Bucket != "acme-backups" || len(m.items) != 0 || m.loading {
		t.Fatalf("old multipart result crossed scope: loc=%+v items=%+v loading=%v", m.loc, m.items, m.loading)
	}
}

type controlledCF struct {
	fakeCF
	fail   bool
	invErr error
}

func (f *controlledCF) Distributions(_ context.Context, acct string) ([]cloud.Distribution, error) {
	return []cloud.Distribution{{ID: "dist-" + acct}}, nil
}
func (f *controlledCF) Invalidations(_ context.Context, acct, dist string) ([]cloud.Invalidation, error) {
	if f.invErr != nil {
		return nil, f.invErr
	}
	return []cloud.Invalidation{{ID: "inv-" + acct, DistributionID: dist, Status: "Completed"}}, nil
}
func (f *controlledCF) Invalidate(ctx context.Context, acct, dist string, paths []string) (cloud.Invalidation, error) {
	if f.fail {
		return cloud.Invalidation{}, errors.New("submit denied")
	}
	return f.fakeCF.Invalidate(ctx, acct, dist, paths)
}

func TestCloudFrontReentryRejectsOldResults(t *testing.T) {
	for _, request := range []string{"distributions", "invalidations"} {
		t.Run(request, func(t *testing.T) {
			h := newHarness(t, "light")
			h.a.accounts = []Account{{ID: "A", Provider: core.ProviderAWS}, {ID: "B", Provider: core.ProviderAWS}}
			h.a.accountID = "A"
			h.a.tl.cf.api = &controlledCF{}
			h.a.cfEnter()
			t.Cleanup(h.a.cfLeave)
			old := nextCompletion(t, h.a)
			if request == "invalidations" {
				old()
				old = nextCompletion(t, h.a)
			}
			h.a.cfLeave()
			h.a.accountID = "B"
			h.a.cfEnter()
			nextCompletion(t, h.a)()
			nextCompletion(t, h.a)()
			old()
			cf := &h.a.tl.cf
			if len(cf.dists) != 1 || cf.dists[0].ID != "dist-B" || cf.invFor != "dist-B" || len(cf.invs) != 1 || cf.invs[0].ID != "inv-B" {
				t.Fatalf("old CF %s crossed account: dists=%+v for=%q invs=%+v", request, cf.dists, cf.invFor, cf.invs)
			}
		})
	}
}

func TestNewBucketCompletionPreservesReopenedDialog(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			h := newHarness(t, "light")
			if fail {
				h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
					if r.Method == http.MethodPut && r.URL.Path == "/first-created-bucket" {
						return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied"}
					}
					return nil
				})
			}
			h.a.openNewBucket()
			h.a.mg.nb.name = "first-created-bucket"
			h.a.createBucket()
			old := nextCompletion(t, h.a)
			h.tt.Frame()
			h.tt.Key(0, ui.KeyEscape)
			if h.a.mg.nb.open {
				t.Fatal("Escape did not close pending dialog")
			}
			h.a.openNewBucket()
			h.a.mg.nb.name = "second-unsent-bucket"
			old()
			f := h.a.mg.nb
			if !f.open || f.name != "second-unsent-bucket" || f.busy || f.err != "" || h.a.scope.Bucket != bkt {
				t.Errorf("old create changed new dialog/navigation: form=%+v scope=%+v", f, h.a.scope)
			}
			bs, err := h.a.svc.Buckets(context.Background(), h.a.accountID)
			if err != nil {
				t.Fatal(err)
			}
			created := slices.ContainsFunc(bs, func(b core.Bucket) bool { return b.Name == "first-created-bucket" })
			if created == fail {
				t.Fatalf("server operation outcome: created=%v fail=%v", created, fail)
			}
			if len(h.a.toasts) == 0 {
				t.Error("stale operation outcome was not reported")
			}
		})
	}
}

func TestCloudFrontCompletionPreservesReopenedDialog(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			h := newHarness(t, "light")
			api := &controlledCF{fail: fail}
			h.a.scope.Bucket = ""
			h.a.tl.cf = cfState{api: api, acct: "A", dists: []cloud.Distribution{{ID: "dist-A"}}, cursor: 0}
			h.a.cfOpenDialog("/old")
			h.a.cfSubmit()
			old := nextCompletion(t, h.a)
			h.a.tl.cf.dlg.open = false
			h.a.cfOpenDialog("/later-unsent")
			old()
			d := h.a.tl.cf.dlg
			if !d.open || d.paths != "/later-unsent" || d.busy || d.err != "" || d.result != nil {
				t.Fatalf("old invalidation changed new dialog: %+v", d)
			}
			if !fail && (len(api.paths) != 1 || api.paths[0][0] != "/old") {
				t.Fatalf("submitted paths=%v", api.paths)
			}
			if len(h.a.toasts) == 0 {
				t.Error("stale operation outcome was not reported")
			}
		})
	}
}

func TestAccountCompletionPreservesReopenedDialog(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			h := newHarness(t, "light")
			h.a.openAccountForm(false)
			h.a.af.name, h.a.af.creds = "first-account", core.CredAnonymous
			h.a.af.provider, h.a.af.endpoint = 1, h.env.Srv.URL
			if fail {
				refuseConfigWrite(t, h, "accounts.json")
			}
			h.a.saveAccount()
			old := nextCompletion(t, h.a)
			h.a.af.open = false
			h.a.openAccountForm(false)
			h.a.af.name = "later-unsent-account"
			old()
			if f := h.a.af; !f.open || f.name != "later-unsent-account" || f.saving || f.saveErr != "" || h.a.accountID != h.env.Account.ID {
				t.Fatalf("old account save changed new dialog/selection: open=%v name=%q saving=%v err=%q account=%q", f.open, f.name, f.saving, f.saveErr, h.a.accountID)
			}
			added := slices.ContainsFunc(h.a.accounts, func(a Account) bool { return a.Name == "first-account" })
			if added == fail {
				t.Fatalf("saved account missing/wrong: added=%v fail=%v", added, fail)
			}
		})
	}
}

func TestTagSavePreservesOtherObjectDraft(t *testing.T) {
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

func TestMIMESavePreservesLaterEdits(t *testing.T) {
	h := newHarness(t, "light")
	h.a.mimeEnter()
	h.a.tl.mime.rows = []mimeRow{{".audit", "text/plain"}}
	h.a.mimeSave()
	old := nextCompletion(t, h.a)
	h.a.tl.mime.rows[0].typ = "application/json"
	old()
	if m := &h.a.tl.mime; m.rows[0].typ != "application/json" || !m.dirty() || m.saving || m.saved[".audit"] != "text/plain" || h.a.svc.MIMETypes()[".audit"] != "text/plain" {
		t.Fatalf("MIME saved snapshot erased later draft: %+v", m)
	}
	h.a.mimeSave()
	nextCompletion(t, h.a)()
	if h.a.tl.mime.dirty() || h.a.svc.MIMETypes()[".audit"] != "application/json" {
		t.Fatal("later MIME edit could not be saved")
	}
}

func TestDraftSaveFailuresPreserveCurrentInput(t *testing.T) {
	t.Run("mime", func(t *testing.T) {
		h := newHarness(t, "light")
		h.a.mimeEnter()
		m := &h.a.tl.mime
		m.rows = []mimeRow{{".audit", "invalid"}}
		h.a.mimeSave()
		old := nextCompletion(t, h.a)
		m.rows[0].typ = "application/json"
		old()
		if m.rows[0].typ != "application/json" || !m.dirty() || m.saving || m.err == "" || len(h.a.svc.MIMETypes()) != 0 {
			t.Fatalf("MIME failure lost draft/error: rows=%v dirty=%v saving=%v err=%q", m.rows, m.dirty(), m.saving, m.err)
		}
		h.a.mimeSave()
		nextCompletion(t, h.a)()
		if m.dirty() || m.err != "" {
			t.Fatal("corrected MIME save did not succeed")
		}
	})
	t.Run("sync", func(t *testing.T) {
		h := newHarness(t, "light")
		h.a.syncEnter()
		h.a.syncNew()
		s := &h.a.tl.sync
		s.f.name = "missing-folder"
		h.a.syncSave()
		old := nextCompletion(t, h.a)
		s.f.name, s.f.dir = "corrected", t.TempDir()
		old()
		if s.f.name != "corrected" || !s.dirty() || s.saving || s.err == "" || len(h.a.svc.SyncJobs()) != 0 {
			t.Fatalf("sync failure lost draft/error: f=%+v dirty=%v saving=%v err=%q", s.f, s.dirty(), s.saving, s.err)
		}
		h.a.syncSave()
		nextCompletion(t, h.a)()
		if s.dirty() || s.err != "" {
			t.Fatal("corrected sync save did not succeed")
		}
	})
}

func TestTagSavePreservesLaterEditsOnSameObject(t *testing.T) {
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

func TestMultipartAbortPartialUsesActualResults(t *testing.T) {
	h := newHarness(t, "light")
	startMultipart(t, h, "abort-ok.bin")
	startMultipart(t, h, "abort-denied.bin")
	loc := h.a.scope
	ups, err := h.a.svc.MultipartUploads(context.Background(), loc)
	if err != nil || len(ups) != 2 {
		t.Fatalf("uploads=%v err=%v", ups, err)
	}
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/abort-denied.bin") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied abort"}
		}
		return nil
	})
	id, err := h.a.svc.AbortMultipartUploads(loc, ups)
	if err != nil {
		t.Fatal(err)
	}
	h.waitTask("Abort 2 multipart upload(s)", core.StatusPartiallyFailed)
	h.settle()
	h.a.tl.mp = mpState{abortTask: id, abortN: 2, bucket: loc.Bucket, loc: loc}
	h.a.toasts = nil
	h.a.mpCheckAbort()
	if len(h.a.toasts) != 1 {
		t.Fatalf("completion toast count=%d", len(h.a.toasts))
	}
	msg := h.a.toasts[0].msg
	if !strings.HasPrefix(msg, "Aborted 1 upload · Could not abort:") || !strings.Contains(msg, "AccessDenied") {
		t.Fatalf("partial abort message=%q", msg)
	}
	remaining, err := h.a.svc.MultipartUploads(context.Background(), loc)
	if err != nil || len(remaining) != 1 || remaining[0].Key != "abort-denied.bin" {
		t.Fatalf("server outcome=%v err=%v", remaining, err)
	}
	h.settle()
	if len(h.a.tl.mp.items) != 1 || h.a.tl.mp.items[0].Key != "abort-denied.bin" {
		t.Fatalf("list was not refreshed from actual abort results: %v", h.a.tl.mp.items)
	}
}

func TestRetryScanFollowsNewTransfers(t *testing.T) {
	h := newHarness(t, "light")
	p := filepath.Join(t.TempDir(), "retry-scan.txt")
	h.a.uploadPaths([]string{p})
	var failed Task
	h.waitFor("upload scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	if err := os.WriteFile(p, []byte("rescanned"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.retryTask(failed.ID)
	h.waitTask("Upload retry-scan.txt", core.StatusSucceeded)
	h.settle()
	if !h.has("retry-scan.txt") {
		t.Error("retried scan did not refresh uploaded children")
	}
	h.mustHave("Uploaded 1 file")
	h.mustNotHave("Uploaded 0 files")
}

func TestRetryScanOutcomeSurvivesHistoryEviction(t *testing.T) {
	h := newHarness(t, "light")
	st := h.a.svc.Settings()
	st.Limits.Upload = 1
	if err := h.a.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "retry-history")
	h.a.uploadPaths([]string{dir})
	var failed Task
	h.waitFor("upload scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1002; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/item-0000.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "first retry item denied"}
		}
		return nil
	})
	h.a.retryTask(failed.ID)
	h.waitFor("retried scan and uploads finished", func() bool {
		if len(h.a.watches) != 0 {
			return false
		}
		for _, task := range h.a.svc.Tasks() {
			if !task.Status.Finished() {
				return false
			}
		}
		_, exists := h.env.Srv.Object(bkt, "retry-history/item-1001.txt")
		return exists
	})
	h.settle()
	if len(h.a.svc.Tasks()) != 1000 {
		t.Fatalf("history bound changed: %d", len(h.a.svc.Tasks()))
	}
	h.mustHave("Upload finished with errors: 1001 uploaded, 1 failed")
	if !slices.ContainsFunc(h.a.ops, func(e opEntry) bool { return e.what == tookUpload && e.state == opFailed && e.n == 1001 && e.bad == 1 }) {
		t.Error("retry lost evicted outcomes from recent operations")
	}
}

func TestRetryScanWatchSurvivesRemovalBeforeRegistration(t *testing.T) {
	h := newHarness(t, "light")
	st := h.a.svc.Settings()
	st.Limits.Upload = 2
	if err := h.a.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "late-watch")
	h.a.uploadPaths([]string{dir})
	var failed Task
	h.waitFor("scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	w := h.a.retryWatches[failed.GroupID]
	if w == nil {
		t.Fatal("missing original watch context")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut {
			if strings.HasSuffix(r.URL.Path, "/item-0000.txt") {
				return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "first retry item denied"}
			}
			if strings.HasSuffix(r.URL.Path, "/item-0001.txt") {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
		}
		return nil
	})
	scanned, removed := make(chan string, 1), make(chan string, 1)
	finished := make(chan struct{}, 3)
	unsub := h.a.svc.Subscribe(func(e core.Event) {
		if e.Task.Title == "Rescan 1 item for upload" {
			if e.Removed {
				removed <- e.Task.ID
			} else if e.Task.Status.Finished() {
				scanned <- e.Task.ID
			}
		}
		if !e.Removed && e.Task.Kind == core.KindUpload && e.Task.Status.Finished() {
			finished <- struct{}{}
		}
	})
	defer unsub()
	next, err := h.a.svc.Retry(failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-scanned:
	case <-time.After(10 * time.Second):
		t.Fatal("retry scan did not finish")
	}
	// Keep one descendant alive, so the group's complete counters survive removal.
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatal("retry siblings did not finish")
		}
	}
	h.a.svc.ClearFinished()
	select {
	case id := <-removed:
		if id != next {
			t.Fatalf("removed scan=%q want=%q", id, next)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retry scan was not removed")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("last retry child did not finish")
	}
	if slices.ContainsFunc(h.a.svc.Tasks(), func(t Task) bool { return t.ID == next }) {
		t.Fatal("test did not reach the lookup/retention window")
	}
	// No UI frames have drained inbox since Retry: its receipt is still there.
	h.a.addWatch(next, w)
	h.settle()
	if len(h.a.watches) != 0 {
		t.Errorf("removed scan left an unresolved watch: %v", h.a.watches)
	}
	h.mustHave("Upload finished with errors: 2 uploaded, 1 failed")
	if !h.has("late-watch/") {
		t.Error("late retry watch did not refresh listing")
	}
}

func TestSyncSavePreservesLaterEdits(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			h := newHarness(t, "light")
			h.a.syncEnter()
			h.a.syncNew()
			h.a.tl.sync.f.name, h.a.tl.sync.f.dir = "first-name", t.TempDir()
			if existing {
				h.a.syncSave()
				nextCompletion(t, h.a)()
				h.a.tl.sync.f.name = "submitted-name"
			}
			submitted := h.a.tl.sync.f.name
			left := false
			h.a.syncSave(func() { left = true })
			old := nextCompletion(t, h.a)
			h.a.tl.sync.f.name = "later-unsaved-name"
			old()
			s := &h.a.tl.sync
			if s.f.name != "later-unsaved-name" || !s.dirty() || s.saving || left || s.jobID() == "" || h.a.svc.SyncJobs()[0].Name != submitted {
				t.Fatalf("sync save erased later draft or left editor: f=%+v dirty=%v left=%v id=%q", s.f, s.dirty(), left, s.jobID())
			}
			h.a.syncSave(func() { left = true })
			nextCompletion(t, h.a)()
			if !left || s.dirty() || len(h.a.svc.SyncJobs()) != 1 || h.a.svc.SyncJobs()[0].Name != "later-unsaved-name" {
				t.Fatal("second save did not update the same job and continue")
			}
		})
	}
}

func TestRetryRefreshesListingAndRecentOperation(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/retry-refresh.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied"}
		}
		return nil
	})
	h.a.uploadPaths([]string{writeTemp(t, "retry-refresh.txt", []byte("retry"))})
	failed := h.waitTask("Upload retry-refresh.txt", core.StatusFailed)
	h.settle()
	h.env.Srv.Inject(nil)
	h.a.retryTask(failed.ID)
	h.waitFor("retry completed", func() bool {
		for _, task := range h.a.tasks {
			if task.ID != failed.ID && task.Title == failed.Title && task.Status == core.StatusSucceeded {
				return true
			}
		}
		return false
	})
	h.settle()
	if body, ok := h.env.Srv.Object(bkt, "retry-refresh.txt"); !ok || string(body) != "retry" {
		t.Fatal("retry did not upload data")
	}
	if !h.has("retry-refresh.txt") {
		t.Error("successful retry did not refresh the listing")
	}
	h.mustHave("Uploaded 1 file")
	if !slices.ContainsFunc(h.a.ops, func(e opEntry) bool {
		return e.what == tookUpload && e.state == opDone && e.n == 1 && e.account == h.env.Account.ID
	}) {
		t.Error("successful retry missing from recent operations")
	}
	h.a.clearFinished()
	h.settle()
	if len(h.a.retryWatches) != 0 {
		t.Fatal("cleared task history retained retry contexts")
	}
}

func TestRetryDownloadKeepsOriginalDestination(t *testing.T) {
	h := newHarness(t, "light")
	dir := t.TempDir()
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manifest.json") && !r.URL.Query().Has("tagging") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "download denied"}
		}
		return nil
	})
	h.a.downloadKeys(h.a.scope, []string{"manifest.json"}, dir)
	failed := h.waitTask("Download manifest.json", core.StatusFailed)
	h.settle()
	h.a.openBucket("acme-backups")
	h.settle()
	h.env.Srv.Inject(nil)
	h.a.retryTask(failed.ID)
	h.waitFor("retry download finished", func() bool {
		for _, task := range h.a.tasks {
			if task.ID != failed.ID && task.Title == failed.Title && task.Status == core.StatusSucceeded {
				return true
			}
		}
		return false
	})
	h.settle()
	if body, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err != nil || !strings.Contains(string(body), "acme-storefront") {
		t.Fatalf("retried download body=%q err=%v", body, err)
	}
	if h.a.scope.Bucket != "acme-backups" {
		t.Fatal("retry changed the current bucket")
	}
	h.mustHave("Downloaded 1 file", "Show in folder")
	revealed := make(chan string, 1)
	h.a.reveal = func(p string) { revealed <- p }
	h.mustClick("Show in folder")
	select {
	case path := <-revealed:
		if path != filepath.Join(dir, "manifest.json") {
			t.Fatalf("retry reveal path=%q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry completion lost original reveal action")
	}
}

func TestMultipartAbortReportsTerminalOutcome(t *testing.T) {
	for _, status := range []core.Status{core.StatusSucceeded, core.StatusFailed, core.StatusPartiallyFailed, core.StatusCanceled} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t, "light")
			h.a.tasks = []Task{{ID: "abort", Status: status, Items: 2, ItemsDone: 2, ItemsFailed: 2, Err: "denied"}}
			if status == core.StatusSucceeded {
				h.a.tasks[0].ItemsFailed, h.a.tasks[0].Err = 0, ""
			}
			if status == core.StatusPartiallyFailed {
				h.a.tasks[0].ItemsFailed = 1
			}
			h.a.tl.mp = mpState{abortTask: "abort", abortN: 2}
			h.a.toasts = nil
			h.a.mpCheckAbort()
			if len(h.a.toasts) != 1 {
				t.Fatalf("completion toast count=%d", len(h.a.toasts))
			}
			msg := h.a.toasts[0].msg
			if status == core.StatusSucceeded {
				if msg != "Aborted 2 uploads" {
					t.Fatalf("success message=%q", msg)
				}
			} else if status == core.StatusCanceled {
				if !strings.Contains(strings.ToLower(msg), "cancel") {
					t.Fatalf("cancellation reported as success: %q", msg)
				}
			} else if status == core.StatusPartiallyFailed && msg != "Aborted 1 upload · Could not abort: denied" {
				t.Fatalf("partial failure count/message=%q", msg)
			} else if !strings.Contains(msg, "denied") || msg == "Aborted 2 uploads" {
				t.Fatalf("failure reported as success: %q", msg)
			}
			if h.a.tl.mp.abortTask != "" {
				t.Fatal("finished abort still followed")
			}
		})
	}
}

func TestCloudFrontSlowPollDoesNotStarve(t *testing.T) {
	h := newHarness(t, "light")
	h.a.tool = "cloudfront"
	h.a.tl.cf = cfState{api: &controlledCF{}, acct: "A", invFor: "dist-A", invs: []cloud.Invalidation{{ID: "inv-A", Status: "InProgress"}}}
	h.a.cfTick()
	old := nextCompletion(t, h.a)
	gen := h.a.tl.cf.invGen
	for i := 0; i < 3; i++ {
		h.a.cfTick()
	}
	if h.a.tl.cf.invGen != gen {
		t.Errorf("slow poll superseded by another tick: gen=%d want=%d", h.a.tl.cf.invGen, gen)
	}
	old()
	if cf := &h.a.tl.cf; cf.invLoad || cf.invs[0].Status != "Completed" {
		t.Fatalf("slow poll could not converge: invLoad=%v invs=%+v", cf.invLoad, cf.invs)
	}
}

func TestCloudFrontPollRecoversAndSelectionInvalidates(t *testing.T) {
	h := newHarness(t, "light")
	api := &controlledCF{invErr: errors.New("poll denied")}
	h.a.tool = "cloudfront"
	cf := &h.a.tl.cf
	*cf = cfState{api: api, acct: "A", invFor: "dist-A", dists: []cloud.Distribution{{ID: "dist-A"}, {ID: "dist-B"}}, invs: []cloud.Invalidation{{ID: "inv-A", Status: "InProgress"}}}
	h.a.cfTick()
	nextCompletion(t, h.a)()
	if cf.invLoad || cf.invErr != "" || cf.invs[0].Status != "InProgress" {
		t.Fatal("silent failure wedged poll or erased current status")
	}
	api.invErr = nil
	h.a.cfTick()
	old := nextCompletion(t, h.a)
	cf.cursor = 1
	h.a.cfSyncSelection()
	old()
	if !cf.invLoad || cf.invFor != "dist-B" {
		t.Fatal("old poll completed the new selection's request")
	}
	nextCompletion(t, h.a)()
	if cf.invLoad || len(cf.invs) != 1 || cf.invs[0].DistributionID != "dist-B" || cf.invs[0].Status != "Completed" {
		t.Fatalf("new selection did not load: %v", cf.invs)
	}
}

func TestTrayUploadAvailabilityFollowsNavigation(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openTool("settings")
	h.settle()
	calls, enabled := 0, false
	h.a.tray.refresh = func(string) { calls++; enabled = h.a.enabled(h.a.cmd("upload.files")) }
	h.a.syncTray()
	h.a.openBucket(bkt)
	h.settle()
	if calls != 2 || !enabled {
		t.Fatalf("tray stayed disabled after navigation: refreshes=%d enabled=%v", calls, enabled)
	}
	h.a.syncTray()
	if calls != 2 {
		t.Error("unchanged menu was needlessly refreshed")
	}
	h.a.openTool("settings")
	h.settle()
	if calls != 3 || enabled {
		t.Fatalf("tray stayed enabled outside browser: refreshes=%d enabled=%v", calls, enabled)
	}
}
