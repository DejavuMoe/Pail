package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

func (h *harness) bucketConfig(bucket string, cfg core.BucketConfig) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	js, err := h.env.Svc.GetBucketConfig(ctx, h.env.Account.ID, bucket, cfg)
	if err != nil {
		h.t.Fatalf("get %s config: %v", cfg, err)
	}
	return js
}

func (h *harness) hasBucket(name string) bool { return slices.Contains(h.env.Srv.Buckets(), name) }

// ---- create ----------------------------------------------------------------------------------

func TestBucketNameRules(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"acme-data": true, "a1b": true, "my.bucket.name": true, "123": true,
		"ab": false, "": false, "Acme": false, "-acme": false, "acme-": false, "acme_data": false,
		"acme..data": false, "192.168.1.1": false, "xn--bucket": false, "bucket-s3alias": false,
		"bucket--ol-s3": false, strings.Repeat("a", 64): false, strings.Repeat("a", 63): true,
	} {
		if got := bucketNameError(name) == ""; got != want {
			t.Errorf("bucketNameError(%q) valid = %v, want %v (%q)", name, got, want, bucketNameError(name))
		}
	}
}

func TestCreateBucketValidatesLiveAndSelectsTheNewOne(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.mustClick("New bucket")
	h.mustHave("Create bucket", "Name", "Region", "Object Lock", "Create", "Cancel")
	h.tt.Type("bad_name")
	h.settle()
	h.mustHave("Use lowercase letters, digits, periods and hyphens")
	h.snap("create-bucket-invalid-light")
	h.tt.Click("Create") // disabled while the name is invalid
	h.settle()
	if h.hasBucket("bad_name") || !h.a.mg.nb.open {
		t.Fatal("an invalid name was submitted")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("acme-new")
	h.settle()
	h.mustNotHave("Use lowercase letters, digits, periods and hyphens")
	h.snap("create-bucket-light")
	h.mustClick("Create")
	h.waitFor("bucket created and opened", func() bool { return h.a.scope.Bucket == "acme-new" && !h.a.mg.nb.open })
	if !h.hasBucket("acme-new") {
		t.Fatal("the bucket is not on the server")
	}
	h.mustHave("acme-new", "Created bucket acme-new")
	if h.a.navSel != "b:acme-new" {
		t.Errorf("sidebar selection = %q", h.a.navSel)
	}
}

func TestCreateBucketWithObjectLockAndDuplicateName(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.a.run("newbucket", "test")
	h.settle()
	h.tt.Type("acme-locked")
	h.settle()
	h.mustClick("Object Lock") // the switch's label row
	if !h.a.mg.nb.lock {
		// the label is not clickable; flip it as the switch would
		h.a.mg.nb.lock = true
	}
	h.mustClick("Create")
	h.waitFor("created", func() bool { return h.hasBucket("acme-locked") })
	if !strings.Contains(h.bucketConfig("acme-locked", core.ConfigVersioning), "Enabled") {
		t.Error("Object Lock must turn versioning on")
	}
	// An existing name is reported inside the dialog, which stays open.
	h.a.openNewBucket()
	h.settle()
	h.tt.Type("acme-assets")
	h.mustClick("Create")
	h.waitFor("error shown", func() bool { return h.a.mg.nb.err != "" })
	if !h.a.mg.nb.open {
		t.Fatal("the dialog closed on an error")
	}
	h.mustHave(h.a.mg.nb.err)
}

// ---- delete ----------------------------------------------------------------------------------

func TestDeleteBucketNeedsTheNameTyped(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.env.Srv.CreateBucket("acme-tiny", false)
	h.a.loadBuckets(false)
	h.settle()
	if err := h.tt.RightClick("acme-tiny"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	menu := h.menu()
	for _, want := range []string{"Open", "Bucket settings…", "Empty bucket…", "Delete bucket…", "Copy name"} {
		if !slices.Contains(menu, want) {
			t.Errorf("bucket menu %q lacks %q", menu, want)
		}
	}
	if err := h.chooseMenu("Delete bucket…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.mustHave("Delete bucket?", demoAccountName, "acme-tiny", "Type the bucket name to confirm", "This cannot be undone.")
	h.snap("delete-bucket-light")

	h.tt.Type("acme-ti")
	h.settle()
	h.tt.Click("Delete bucket") // still disabled
	h.settle()
	if !h.hasBucket("acme-tiny") || !h.a.mg.db.open {
		t.Fatal("the delete button worked before the name matched")
	}
	h.tt.Type("ny")
	h.settle()
	h.snap("delete-bucket-ready-light")
	h.mustClick("Delete bucket")
	h.waitToast("Deleted bucket acme-tiny")
	if h.hasBucket("acme-tiny") {
		t.Fatal("the bucket is still on the server")
	}
	h.waitFor("sidebar updated", func() bool {
		for _, b := range h.a.buckets {
			if b.Name == "acme-tiny" {
				return false
			}
		}
		return true
	})
}

func TestDeleteBucketNotEmptyOffersEmptying(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.a.openDeleteBucket("acme-website")
	h.settle()
	h.tt.Type("acme-website")
	h.mustClick("Delete bucket")
	h.waitFor("error shown", func() bool { return h.a.mg.db.err != "" })
	if !h.a.mg.db.notEmpty || !h.hasBucket("acme-website") {
		t.Fatalf("state: %+v", h.a.mg.db)
	}
	h.mustHave("not empty", "Empty bucket…")
	h.snap("delete-bucket-not-empty-light")

	h.mustClick("Empty bucket…")
	h.mustHave("Empty bucket?", "acme-website")
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.mustHave("5 objects")
	h.mustClick("Empty bucket")
	h.waitToast("Emptied bucket acme-website")
	if n := h.countObjects("acme-website"); n != 0 {
		t.Fatalf("%d objects left", n)
	}
	h.a.openDeleteBucket("acme-website")
	h.settle()
	h.tt.Type("acme-website")
	h.mustClick("Delete bucket")
	h.waitFor("deleted", func() bool { return !h.hasBucket("acme-website") })
}

func (h *harness) countObjects(bucket string) int {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	est, err := h.env.Svc.Estimate(ctx, core.Selection{Location: core.Location{AccountID: h.env.Account.ID, Bucket: bucket}, All: true})
	if err != nil {
		h.t.Fatal(err)
	}
	return est.Objects
}

// ---- empty -----------------------------------------------------------------------------------

func TestEmptyBucketCountsFirstAndOffersVersions(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.a.openEmptyBucket("acme-media-archive")
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.mustHave("Empty bucket?", "3 objects")
	h.mustNotHave("Also delete all versions and delete markers") // not versioned
	h.snap("empty-bucket-light")
	h.mustClick("Empty bucket")
	h.waitToast("Emptied bucket acme-media-archive")
	if n := h.countObjects("acme-media-archive"); n != 0 {
		t.Fatalf("%d objects left", n)
	}

	// A versioned bucket keeps its history unless asked.
	h.a.openEmptyBucket(demoVersioned)
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.waitFor("versioning known", func() bool { return h.a.versioned(h.a.accountID, demoVersioned) })
	h.mustHave("Also delete all versions and delete markers")
	h.snap("empty-bucket-versioned-light")
	h.mustClick("Also delete all versions and delete markers")
	if !h.a.mg.eb.all {
		t.Fatal("the checkbox did not toggle")
	}
	h.mustClick("Empty bucket")
	h.waitToast("Emptied bucket " + demoVersioned)
	if n := len(h.env.Srv.Versions(demoVersioned, "notes.txt")) + len(h.env.Srv.Versions(demoVersioned, "old-plan.txt")); n != 0 {
		t.Fatalf("%d versions and markers left", n)
	}
}

func TestEmptyBucketWithoutCheckboxKeepsVersions(t *testing.T) {
	t.Parallel()
	h := newRawHarness(t, "light", true)
	h.a.openEmptyBucket(demoVersioned)
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.waitFor("versioning known", func() bool { return h.a.versioned(h.a.accountID, demoVersioned) })
	h.mustClick("Empty bucket")
	h.waitToast("Emptied bucket " + demoVersioned)
	if h.hasObject(demoVersioned, "notes.txt") {
		t.Fatal("the current object is still there")
	}
	if n := len(h.env.Srv.Versions(demoVersioned, "notes.txt")); n != 4 { // three versions and a delete marker
		t.Errorf("notes.txt has %d entries, want its history plus a delete marker", n)
	}
}

// ---- settings --------------------------------------------------------------------------------

func TestBucketSettingsVersioningForm(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.run("bucketsettings", "test")
	h.settle()
	h.mustHave("Bucket settings", bkt, "Versioning", "Encryption", "Public access", "Object ownership", "Policy", "CORS",
		"Lifecycle", "Logging", "Tags", "Website", "Object Lock", "Replication", "Requester pays", "Transfer acceleration", "Close")
	h.waitFor("versioning read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustHave("Versioning has never been enabled for this bucket.", "Enable")
	h.mustNotHave("Suspended")
	h.snap("bucket-settings-versioning-off-light")

	h.mustClick("Enable")
	h.waitFor("versioning on", func() bool {
		_, s := h.a.mg.bs.cur()
		return s.state == loadReady && s.server != "" && h.tt.HasText("Suspended")
	})
	if !strings.Contains(h.bucketConfig(bkt, core.ConfigVersioning), "Enabled") {
		t.Fatal("versioning is not enabled on the server")
	}
	h.mustHave("Enabled", "Suspended", "Saved")
	h.snap("bucket-settings-versioning-light")

	h.mustClick("Suspended")
	h.mustClick("Save")
	h.waitFor("suspended", func() bool { return strings.Contains(h.bucketConfig(bkt, core.ConfigVersioning), "Suspended") })
	// The open bucket's cached versioning status follows.
	h.waitFor("cache refreshed", func() bool {
		st, ok := h.a.versioningOf(h.a.accountID, bkt)
		return ok && st == "Suspended"
	})
}

func TestBucketSettingsPolicyJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.run("bucketsettings", "test")
	h.settle()
	h.mustClick("Policy")
	h.waitFor("policy read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustHave("Not configured", "Configure")
	h.mustClick("Configure")
	h.mustHave("Validate", "Save")
	if _, s := h.a.mg.bs.cur(); !strings.Contains(s.draft, "2012-10-17") || !strings.Contains(s.draft, "arn:aws:s3:::"+bkt+"/*") {
		t.Fatalf("the template does not name the bucket: %s", s.draft)
	}
	h.mustClick("Validate")
	h.mustHave("Valid JSON")
	h.snap("bucket-settings-policy-light")

	// Broken JSON is caught before anything is sent.
	_, s := h.a.mg.bs.cur()
	good := s.draft
	s.draft = `{"Version": "2012-10-17",`
	h.settle()
	h.mustClick("Validate")
	h.mustHave("Invalid JSON")
	h.mustClick("Save")
	if got := h.bucketConfig(bkt, core.ConfigPolicy); got != "" {
		t.Fatalf("invalid JSON reached the server: %s", got)
	}
	s.draft = good
	h.settle()
	h.mustClick("Save")
	h.waitFor("policy saved", func() bool { return strings.Contains(h.bucketConfig(bkt, core.ConfigPolicy), "AllowRead") })
	h.waitFor("saved shown", func() bool {
		_, s := h.a.mg.bs.cur()
		return s.state == loadReady && s.server != "" && h.tt.HasText("Saved")
	})
	h.mustHave("Remove configuration")
	h.snap("bucket-settings-policy-remove-light")

	// Removing asks first.
	h.mustClick("Remove configuration")
	h.mustHave("Remove configuration?", "Policy", bkt)
	h.mustClick("Cancel")
	if h.bucketConfig(bkt, core.ConfigPolicy) == "" {
		t.Fatal("Cancel removed the policy")
	}
	h.mustClick("Remove configuration")
	h.mustClick("Remove")
	h.waitFor("policy removed", func() bool { return h.bucketConfig(bkt, core.ConfigPolicy) == "" })
	h.waitFor("not configured", func() bool { return h.tt.HasText("Not configured") })
}

func TestBucketSettingsUnsupportedAndDenied(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("cors")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Query().Has("lifecycle") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no"}
		}
		return nil
	})
	h.a.openBucketSettings(bkt)
	h.settle()
	h.mustClick("CORS")
	h.waitFor("cors answered", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadFailed })
	h.mustHave("Not supported by this provider")
	h.snap("bucket-settings-unsupported-light")
	h.mustClick("Lifecycle")
	h.waitFor("lifecycle answered", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadFailed })
	h.mustHave("You don't have permission to read this setting", "Retry")
	h.env.Srv.Inject(nil)
	h.mustClick("Retry")
	h.waitFor("lifecycle read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustHave("Not configured")
}

func TestBucketSettingsSimpleForms(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openBucketSettings(bkt)
	h.settle()

	h.mustClick("Encryption")
	h.waitFor("encryption read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustClick("None")
	h.mustClick("AES-256 (SSE-S3)")
	h.mustClick("Save")
	h.waitFor("encryption set", func() bool { return strings.Contains(h.bucketConfig(bkt, core.ConfigEncryption), "AES256") })
	h.waitFor("encryption reread", func() bool {
		_, s := h.a.mg.bs.cur()
		return s.state == loadReady && s.sse == "AES256" && s.msg == "Saved"
	})
	h.mustClick("AES-256 (SSE-S3)")
	h.mustClick("None")
	h.mustClick("Save")
	h.waitFor("encryption removed", func() bool { return h.bucketConfig(bkt, core.ConfigEncryption) == "" })

	h.mustClick("Public access")
	h.waitFor("public access read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustHave("Block public ACLs", "Ignore public ACLs", "Block public bucket policies", "Restrict public buckets")
	_, s := h.a.mg.bs.cur()
	s.pab[0], s.pab[2] = true, true
	h.mustClick("Save")
	h.waitFor("public access set", func() bool {
		js := h.bucketConfig(bkt, core.ConfigPublicAccessBlock)
		return strings.Contains(js, `"BlockPublicAcls": true`) && strings.Contains(js, `"IgnorePublicAcls": false`)
	})
	h.snap("bucket-settings-public-access-light")

	h.mustClick("Object ownership")
	h.waitFor("ownership read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	h.mustHave("Bucket owner enforced")
	h.mustClick("Bucket owner enforced")
	h.mustClick("Object writer")
	h.mustClick("Save")
	h.waitFor("ownership set", func() bool { return strings.Contains(h.bucketConfig(bkt, core.ConfigOwnership), "ObjectWriter") })

	h.mustClick("Requester pays")
	h.waitFor("payer read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady })
	_, s = h.a.mg.bs.cur()
	s.requester = true
	h.mustClick("Save")
	h.waitFor("payer set", func() bool { return strings.Contains(h.bucketConfig(bkt, core.ConfigRequestPayment), "Requester") })
}

func TestBucketSettingsCloseAndEscape(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openBucketSettings(bkt)
	h.settle()
	h.mustClick("Close")
	if h.a.mg.bs.open {
		t.Fatal("Close left the dialog open")
	}
	h.a.openBucketSettings(bkt)
	h.settle()
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.mg.bs.open {
		t.Fatal("Escape left the dialog open")
	}
}

// ---- account form ----------------------------------------------------------------------------

func TestNewAccountStartsWithPathStyle(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openAccountForm(false)
	h.settle()
	if !h.a.af.path {
		t.Fatal("path-style addressing must default to on for a new account")
	}
	h.mustClick("S3-compatible")
	h.mustHave("Path-style addressing")
	ac, _ := h.a.af.build()
	if ac.Provider != core.ProviderS3 || !ac.PathStyle {
		t.Fatalf("built account %+v", ac)
	}
}

// ---- late completions and moves --------------------------------------------------------------

func TestNewBucketCompletionPreservesReopenedDialog(t *testing.T) {
	t.Parallel()
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

// Moving a bucket's contents in a language other than English said "Copied".
func TestBucketMoveReportsAMoveInChinese(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.setLocale("zh-Hans")
	n := h.countObjects("acme-website")
	h.a.openBucketCopy("acme-website", true)
	h.a.objOps.bc.dest = "acme-logs"
	h.a.objOps.bc.prefix = "site/"
	h.a.submitBucketCopy()
	h.waitFor("move done", func() bool {
		tk, ok := h.task("Move everything in acme-website")
		return ok && tk.Status == core.StatusSucceeded
	})
	want := "已移动 " + strings.TrimSpace(groupInt(n)) + " 项"
	h.waitToast(want)
}

// ---- bucket aliases --------------------------------------------------------------------------

// openAliasFromMenu opens the Set alias dialog the way a user does.
func (h *harness) openAliasFromMenu(bucket string) {
	h.t.Helper()
	if err := h.tt.RightClick(bucket); err != nil {
		h.t.Fatal(err)
	}
	h.tt.Frame()
	if menu := h.menu(); !slices.Contains(menu, "Set alias…") {
		h.t.Fatalf("bucket menu %q lacks Set alias…", menu)
	}
	if err := h.chooseMenu("Set alias…"); err != nil {
		h.t.Fatal(err)
	}
	h.settle()
	if !h.a.alias.open || h.a.alias.bucket != bucket {
		h.t.Fatalf("the dialog did not open for %s: %+v", bucket, h.a.alias)
	}
}

func TestBucketAliasShowsInPlaceOfTheNameAndNothingElseChanges(t *testing.T) {
	h := newHarness(t, "light")
	h.openAliasFromMenu("acme-assets")
	h.mustHave("Bucket alias", "Bucket", "acme-assets", "Alias", "Save", "Cancel",
		"Shown instead of the bucket name in the sidebar, the path bar and the command palette. The bucket itself is not renamed.")
	h.snapBoth("bucket-alias-dialog")

	h.tt.Type("Storefront assets")
	h.settle()
	h.mustClick("Save")
	h.waitFor("dialog closed", func() bool { return !h.a.alias.open })
	h.waitToast("Saved the alias of acme-assets")
	if got := h.env.Svc.BucketAlias(h.a.accountID, "acme-assets"); got != "Storefront assets" {
		t.Fatalf("saved alias = %q", got)
	}

	// The sidebar and the path bar show the alias; the real name is in the tooltip.
	h.mustHave("Storefront assets")
	if got := h.a.bucketLabel("acme-assets"); got != "Storefront assets" {
		t.Fatalf("bucketLabel = %q", got)
	}
	if got := h.a.bucketTip("acme-assets"); got != "Storefront assets · acme-assets" {
		t.Fatalf("bucketTip = %q", got)
	}
	if got := h.a.bucketLabel("acme-logs"); got != "acme-logs" || h.a.bucketTip("acme-logs") != "acme-logs" {
		t.Fatalf("a bucket without an alias is labelled %q", got)
	}
	if h.hasExactText("acme-assets") {
		t.Error("the real name is still shown as a label")
	}
	h.snapBoth("bucket-alias-shown")

	// Both names find the bucket in the sidebar filter.
	for _, q := range []string{"storefront", "acme-assets"} {
		h.a.bucketFilter = q
		h.tt.Frame()
		h.mustHave("Storefront assets")
		h.mustNotHave("acme-logs")
	}
	h.a.bucketFilter = ""

	// It is still the same bucket: browsing, the object URI and the real name in
	// the dialogs that act on it.
	h.settle()
	if h.a.scope.Bucket != "acme-assets" || !h.has("manifest.json") {
		t.Fatalf("browsing changed: bucket %q", h.a.scope.Bucket)
	}
	if got := h.a.objectURI("manifest.json"); got != "s3://acme-assets/manifest.json" {
		t.Fatalf("object URI = %q", got)
	}
	h.a.openDeleteBucket("acme-assets")
	h.settle()
	h.mustHave("Delete bucket?", "acme-assets")
	h.a.mg.db.open = false
	h.settle()

	// Another bucket keeps its own name in the path bar, then this one shows the alias again.
	h.a.openBucket("acme-logs")
	h.settle()
	h.mustHave("acme-logs")
	h.a.openBucket("acme-assets")
	h.settle()
	h.mustHave("Storefront assets")

	// The command palette finds it by the alias and by the real name.
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	for _, q := range []string{"Storefront", "acme-assets"} {
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(q)
		h.settle()
		h.mustHave(locales.PaletteOpenBucket("Storefront assets"))
	}
	h.a.pal.open = false
	h.settle()

	// It survives a restart, and is Chinese-ready.
	if got := h.env.Svc.Accounts()[0].BucketAliases["acme-assets"]; got != "Storefront assets" {
		t.Fatalf("Accounts() lost the alias: %v", h.env.Svc.Accounts()[0].BucketAliases)
	}
	h.zhDialog(func() { h.a.openAliasDialog("acme-assets") }, "存储桶别名", "存储桶", "别名", "保存", "取消",
		"在侧边栏、路径栏和命令面板里显示，代替存储桶名称。存储桶本身不会被重命名。")
	h.a.alias.open = false
	h.settle()
}

func TestBucketAliasCanBeChangedAndRemoved(t *testing.T) {
	h := newHarness(t, "light")
	acct := h.a.accountID

	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = "  Shop  "
	h.settle()
	h.a.saveAlias()
	h.waitFor("saved", func() bool { return !h.a.alias.open })
	if got := h.env.Svc.BucketAlias(acct, "acme-assets"); got != "Shop" {
		t.Fatalf("alias = %q, want it trimmed", got)
	}

	// The dialog starts from the saved alias; another name replaces it.
	h.a.openAliasDialog("acme-assets")
	if h.a.alias.text != "Shop" {
		t.Fatalf("the dialog starts with %q", h.a.alias.text)
	}
	h.a.alias.text = "Shop (live)"
	h.a.saveAlias()
	h.waitFor("renamed", func() bool { return !h.a.alias.open })
	h.settle()
	h.mustHave("Shop (live)")
	if h.hasExactText("Shop") {
		t.Error("the old alias is still shown")
	}

	// Emptying it removes the alias.
	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = ""
	h.settle()
	h.mustClick("Save")
	h.waitFor("removed", func() bool { return !h.a.alias.open })
	h.waitToast("Removed the alias of acme-assets")
	if got := h.env.Svc.BucketAlias(acct, "acme-assets"); got != "" {
		t.Fatalf("alias after removing = %q", got)
	}
	if !h.hasExactText("acme-assets") || h.hasExactText("Shop (live)") {
		t.Error("the bucket does not show its own name again")
	}

	// Cancel changes nothing.
	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = "Never saved"
	h.settle()
	h.mustClick("Cancel")
	if h.env.Svc.BucketAlias(acct, "acme-assets") != "" || h.a.alias.open {
		t.Fatal("Cancel saved or kept the dialog")
	}
}

func TestBucketAliasErrorsStayInTheDialog(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.env.Svc.SetBucketAlias(h.a.accountID, "acme-assets", "Shop"); err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.settle()

	// Another bucket cannot take the same alias; the Save button is off.
	h.a.openAliasDialog("acme-logs")
	h.a.alias.text = "Shop"
	h.settle()
	h.mustHave("Already used by acme-assets")
	h.mustClick("Save")
	if !h.a.alias.open || h.env.Svc.BucketAlias(h.a.accountID, "acme-logs") != "" {
		t.Fatal("a duplicate alias was saved")
	}
	// A name this bucket already has is fine.
	h.a.alias.text = "Shop 2"
	h.settle()
	h.mustNotHave("Already used by acme-assets")

	h.a.alias.text = strings.Repeat("长", core.MaxAliasRunes+1)
	h.settle()
	h.mustHave("Use at most 64 characters")
	h.mustClick("Save")
	if !h.a.alias.open {
		t.Fatal("a too long alias closed the dialog")
	}
	h.a.alias.text = strings.Repeat("长", core.MaxAliasRunes)
	h.settle()
	h.mustNotHave("Use at most 64 characters")

	// A failure of the service is reported in the dialog, which stays open.
	h.a.alias.text = "Logs"
	h.a.accountID = "gone"
	h.a.saveAlias()
	h.waitFor("failure shown", func() bool { return h.a.alias.err != "" })
	h.mustHave(h.a.alias.err)
	if !strings.HasPrefix(h.a.alias.err, "Could not save the alias: ") || !h.a.alias.open {
		t.Fatalf("failure: %q, open %v", h.a.alias.err, h.a.alias.open)
	}
}

func TestAliasDialogAndStatusBarAreTranslated(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.env.Svc.SetBucketAlias(h.a.accountID, "acme-logs", "Taken"); err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.settle()

	open := func() {
		h.a.openAliasDialog("acme-assets")
		h.a.alias.text = "Taken" // shows the "already used" line
	}
	if left := h.newPlainTexts(open); len(left) > 0 {
		t.Errorf("the alias dialog has texts that are not translated: %q", left)
	}
	h.a.alias.open = false
	h.settle()
	open = func() {
		h.a.openAliasDialog("acme-assets")
		h.a.alias.text = strings.Repeat("x", core.MaxAliasRunes+1)
	}
	if left := h.newPlainTexts(open); len(left) > 0 {
		t.Errorf("the alias dialog (too long) has texts that are not translated: %q", left)
	}
	h.a.alias.open = false
	h.settle()

	for _, what := range []string{tookBuckets, tookList, tookUpload, tookDownload, tookDelete} {
		w := what
		if left := h.newPlainTexts(func() { h.a.took = opTime{what: w, d: 4200 * time.Millisecond} }); len(left) > 0 {
			t.Errorf("the status bar after %s has texts that are not translated: %q", w, left)
		}
	}
	// Each kind says its own thing in Chinese.
	for what, want := range map[string]string{tookBuckets: "加载存储桶用时 4.2 s", tookList: "加载列表用时 4.2 s",
		tookUpload: "上传用时 4.2 s", tookDownload: "下载用时 4.2 s", tookDelete: "删除用时 4.2 s"} {
		h.a.took = opTime{what: what, d: 4200 * time.Millisecond}
		h.setLocale("zh-Hans")
		h.mustHave(want)
		h.setLocale("en")
	}
}
