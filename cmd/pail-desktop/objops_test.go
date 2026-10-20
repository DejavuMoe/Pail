package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

// ---- helpers -----------------------------------------------------------------------------------

// snapBoth saves the frame in the light and the dark theme.
func (h *harness) snapBoth(name string) {
	h.t.Helper()
	was := h.a.opts.theme
	h.a.opts.theme = "light"
	h.snap(name + "-light")
	h.a.opts.theme = "dark"
	h.snap(name + "-dark")
	h.a.opts.theme = was
	h.tt.Frame()
}

// clickBeside clicks dx pixels into the element that follows the one showing
// text (8px gap): how a test reaches a button whose own label is shared with
// another element on the screen.
func (h *harness) clickBeside(text string, dx float32) {
	h.t.Helper()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no element shows %q; have %q", text, h.tt.Texts())
	}
	h.tt.ClickAt(r.X+r.W+8+dx, r.Y+r.H/2)
	h.settle()
}

// rightClickChoose opens the context menu of the row or bucket showing text and
// chooses an item.
func (h *harness) rightClickChoose(text string, item ...string) {
	h.t.Helper()
	if err := h.tt.RightClick(text); err != nil {
		h.t.Fatal(err)
	}
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem(item...); err != nil {
		h.t.Fatalf("menu %q: %v", h.tt.Menu(), err)
	}
	h.settle()
}

func (h *harness) loc(bucket string) core.Location {
	return core.Location{AccountID: h.env.Account.ID, Bucket: bucket}
}

func (h *harness) estimate(sel core.Selection) core.Estimate {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	est, err := h.env.Svc.Estimate(ctx, sel)
	if err != nil {
		h.t.Fatal(err)
	}
	return est
}

func (h *harness) classOf(bucket, key string) string {
	h.t.Helper()
	return h.head(bucket, key).StorageClass
}

func menuHas(menu []string, want ...string) []string {
	var missing []string
	for _, w := range want {
		if !contains(menu, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// ---- change storage class -------------------------------------------------------------------------

func TestChangeStorageClassOfTheSelection(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if m := menuHas(h.tt.Menu(), "Change storage class…", "Change encryption…"); len(m) > 0 {
		t.Fatalf("context menu %q lacks %q", h.tt.Menu(), m)
	}
	if err := h.tt.ChooseMenuItem("Change storage class…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	total := h.a.rows[h.a.byKey["README.txt"]].Size + h.a.rows[h.a.byKey["manifest.json"]].Size
	h.mustHave("Change storage class", "Account", "Bucket", "Scope", "2 items", "2 objects · "+humanBytes(total),
		"Rewrites each object; versioned buckets keep the previous version.", "Apply", "Cancel")
	h.snapBoth("class")

	// Pick a class from the select.
	// "Standard" is also a table cell; open the select by its place above the hint.
	r, ok := h.tt.Find("Rewrites each object; versioned buckets keep the previous version.")
	if !ok {
		t.Fatal("no rewrite hint")
	}
	h.tt.ClickAt(r.X+40, r.Y+r.H/2-58)
	h.settle()
	h.mustHave("One Zone-IA", "Intelligent-Tiering", "Glacier IR", "Glacier", "Deep Archive", "Reduced Redundancy", "Other…")
	h.snap("class-select-light")
	h.mustClick("Standard-IA")
	if h.a.objOps.rw.class != "STANDARD_IA" {
		t.Fatalf("class = %q", h.a.objOps.rw.class)
	}
	h.mustClick("Apply")
	h.waitTask("Set storage class STANDARD_IA on 2 items", core.StatusSucceeded)
	h.waitToast("Storage class set to STANDARD_IA on 2 objects")
	for _, k := range []string{"README.txt", "manifest.json"} {
		if got := h.classOf(bkt, k); got != "STANDARD_IA" {
			t.Errorf("%s class on the server = %q", k, got)
		}
	}
	h.waitFor("listing refreshed", func() bool {
		i, ok := h.a.byKey["README.txt"]
		return ok && h.a.rows[i].StorageClass == "STANDARD_IA"
	})
	h.mustHave("Standard-IA")
}

func TestChangeStorageClassOfAFolderCountsFirst(t *testing.T) {
	h := newHarness(t, "light")
	want := h.estimate(core.Selection{Location: h.loc(bkt), Keys: []string{"reports/"}})
	if want.Objects < 5 {
		t.Fatalf("seed has %d objects under reports/", want.Objects)
	}
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if strings.Contains(r.URL.RawQuery, "list-type=2") {
			return &s3test.Fault{Delay: 400 * time.Millisecond}
		}
		return nil
	})
	h.mustClick("reports")
	h.a.run("storageclass", "test")
	h.tt.Frame()
	h.mustHave("Counting…")
	h.env.Srv.Inject(nil)
	h.settle()
	h.mustHave(plural(want.Objects, "object", "objects") + " · " + humanBytes(want.Bytes))
	h.mustNotHave("Counting…")

	h.a.objOps.rw.class = "GLACIER_IR"
	h.mustClick("Apply")
	h.waitTask("Set storage class GLACIER_IR on reports", core.StatusSucceeded)
	for _, k := range []string{"reports/2026-Q1-financials.pdf", "reports/customers-export.csv", "reports/exports/2026-10-17.csv"} {
		if got := h.classOf(bkt, k); got != "GLACIER_IR" {
			t.Errorf("%s class = %q", k, got)
		}
	}
	if got := h.classOf(bkt, "manifest.json"); got != "STANDARD" {
		t.Errorf("manifest.json outside the folder changed to %q", got)
	}
}

func TestChangeStorageClassOfAWholeBucketFromTheBucketMenu(t *testing.T) {
	h := newRawHarness(t, "light", true)
	if err := h.tt.RightClick("acme-media-archive"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	menu := h.tt.Menu()
	if m := menuHas(menu, "Open", "Bucket settings…", "Download bucket…", "Copy contents to…", "Move contents to…",
		"Change storage class…", "Change encryption…", "Empty bucket…", "Delete bucket…", "Copy name"); len(m) > 0 {
		t.Fatalf("bucket menu %q lacks %q", menu, m)
	}
	if contains(menu, "Invalidate CloudFront…") {
		t.Error("CloudFront belongs to AWS accounts only")
	}
	if err := h.tt.ChooseMenuItem("Change storage class…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.mustHave("Change storage class", "Whole bucket", "acme-media-archive", "3 objects")
	h.snap("class-bucket-light")
	h.a.objOps.rw.class = "DEEP_ARCHIVE"
	h.mustClick("Apply")
	h.waitTask("Set storage class DEEP_ARCHIVE on everything in acme-media-archive", core.StatusSucceeded)
	for _, k := range []string{"2024/shoot-a/IMG_0001.jpg", "2024/shoot-a/IMG_0002.jpg", "2024/shoot-b/clip.mp4"} {
		if got := h.classOf("acme-media-archive", k); got != "DEEP_ARCHIVE" {
			t.Errorf("%s class = %q", k, got)
		}
	}
}

func TestStorageClassFreeTextForCompatibleProviders(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.a.run("storageclass", "test")
	h.settle()
	h.a.objOps.rw.class = customClass
	h.a.objOps.rw.custom = "cold storage"
	h.tt.Frame()
	h.mustHave("Use capital letters, digits and underscores")
	if h.a.rewriteValid() {
		t.Fatal("a class name with a space was accepted")
	}
	h.snap("class-custom-light")
	h.a.objOps.rw.custom = "cold_tier"
	h.tt.Frame()
	if !h.a.rewriteValid() || h.a.objOps.rw.classValue() != "COLD_TIER" {
		t.Fatalf("free text class = %q valid=%v", h.a.objOps.rw.classValue(), h.a.rewriteValid())
	}
}

func TestScopeCountSaysAtLeastWhenTruncated(t *testing.T) {
	e := estState{state: delReady, est: core.Estimate{Objects: 100000, Bytes: 1 << 30, Truncated: true}}
	if got := e.summary(); got != "at least 100,000 objects · 1.0 GB" {
		t.Errorf("summary = %q", got)
	}
	e = estState{state: delCounting}
	if e.summary() != "Counting…" {
		t.Errorf("summary = %q", e.summary())
	}
}

// ---- change encryption ----------------------------------------------------------------------------

func TestChangeEncryptionAES256AndNone(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.rightClickChoose("manifest.json", "Change encryption…")
	h.mustHave("Change encryption", "None", "SSE-S3", "SSE-KMS", "SSE-C", "1 object · 303 B", "Apply")
	h.snapBoth("encryption")
	if h.a.objOps.rw.mode != 1 {
		t.Fatalf("the dialog starts on mode %d, want SSE-S3", h.a.objOps.rw.mode)
	}
	h.mustClick("Apply")
	h.waitToast("Encryption set to SSE-S3 on 1 object")
	if got := h.head(bkt, "manifest.json").SSE; got != "AES256" {
		t.Fatalf("SSE on the server = %q, want AES256", got)
	}

	// None removes the explicit request again.
	time.Sleep(150 * time.Millisecond) // past the double-delivery guard of a command from another source
	h.a.run("encryption", "test")
	h.settle()
	h.mustClick("None")
	h.mustClick("Apply")
	h.waitToast("Encryption set to None on 1 object")
	if got := h.head(bkt, "manifest.json").SSE; got != "" {
		t.Fatalf("SSE after None = %q", got)
	}
}

func TestChangeEncryptionKMSKeyIsTyped(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.a.run("encryption", "test")
	h.settle()
	h.mustClick("SSE-KMS")
	h.mustHave("KMS key")
	h.tt.Type("arn:aws:kms:us-east-1:111:key/abc")
	h.settle()
	h.snap("encryption-kms-light")
	h.mustClick("Apply")
	h.waitToast("Encryption set to SSE-KMS on 1 object")
	info := h.head(bkt, "manifest.json")
	if info.SSE != "aws:kms" || info.KMSKeyID != "arn:aws:kms:us-east-1:111:key/abc" {
		t.Fatalf("SSE %q key %q", info.SSE, info.KMSKeyID)
	}
}

func TestSSECIsDisabledOverHTTPWithAReason(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.env.Svc.SetSecret("sse-key", base64.StdEncoding.EncodeToString(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	h.mustClick("manifest.json")
	h.a.run("encryption", "test")
	h.settle()
	h.mustHave("SSE-C needs an https endpoint")
	h.tt.Click("SSE-C")
	h.settle()
	if h.a.objOps.rw.mode == encSSEC {
		t.Fatal("SSE-C was chosen on an http endpoint")
	}
	h.snap("encryption-ssec-http-light")
	h.a.objOps.rw.mode = encSSEC // even forced, the dialog does not submit it
	h.a.objOps.rw.keyRef = "sse-key"
	if h.a.rewriteValid() {
		t.Fatal("SSE-C accepted over http")
	}
	h.a.submitRewrite()
	h.settle()
	if got := h.head(bkt, "manifest.json").SSE; got != "" {
		t.Fatalf("SSE = %q after a refused request", got)
	}
}

func TestEndpointHTTPS(t *testing.T) {
	for ep, want := range map[string]bool{
		"": true, "https://s3.example.com": true, "HTTPS://S3.EXAMPLE.COM": true,
		"http://127.0.0.1:9000": false, "s3.example.com": false,
	} {
		if got := endpointHTTPS(Account{Endpoint: ep}); got != want {
			t.Errorf("endpointHTTPS(%q) = %v, want %v", ep, got, want)
		}
	}
}

// ---- bucket download, copy and move -----------------------------------------------------------------

func TestDownloadBucketFromTheBucketMenu(t *testing.T) {
	h := newRawHarness(t, "light", true)
	dest := t.TempDir()
	asked := make(chan bool, 1)
	h.a.pick = func(dir bool) []string { asked <- dir; return []string{dest} }
	h.rightClickChoose("acme-media-archive", "Download bucket…")
	if dir := <-asked; !dir {
		t.Fatal("the bucket download must ask for a folder")
	}
	h.waitToast("Downloaded 3 files")
	for _, k := range []string{"2024/shoot-a/IMG_0001.jpg", "2024/shoot-a/IMG_0002.jpg", "2024/shoot-b/clip.mp4"} {
		p := filepath.Join(dest, "acme-media-archive", filepath.FromSlash(k))
		want, _ := h.env.Srv.Object("acme-media-archive", k)
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(want) {
			t.Errorf("%s: err=%v, %d bytes, want %d", k, err, len(got), len(want))
		}
	}
}

func TestCopyContentsToAnotherBucket(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.rightClickChoose("acme-media-archive", "Copy contents to…")
	h.mustHave("Copy contents to…", "Target bucket", "Target prefix", "If an object already exists", "3 objects", "Copy contents", "Cancel")
	h.mustNotHave("Move everything")
	if got := h.a.destChoices("acme-media-archive"); len(got) != 6 {
		t.Fatalf("target buckets = %v (every bucket but the source)", got)
	}
	h.a.objOps.bc.dest = "acme-backups"
	h.a.objOps.bc.prefix = "mirror"
	h.tt.Frame()
	h.snapBoth("copy-contents")
	h.mustClick("Copy contents")
	h.waitToast("Copied 3 items")
	for _, k := range []string{"mirror/2024/shoot-a/IMG_0001.jpg", "mirror/2024/shoot-a/IMG_0002.jpg", "mirror/2024/shoot-b/clip.mp4"} {
		if !h.hasObject("acme-backups", k) {
			t.Errorf("%s was not copied", k)
		}
	}
	if n := h.countObjects("acme-media-archive"); n != 3 {
		t.Errorf("a copy left %d objects in the source", n)
	}
	// Running it again with the default policy (rename) never overwrites.
	if h.a.objOps.bc.open {
		t.Error("the dialog stayed open after the copy started")
	}
}

func TestCopyContentsRefusesBadPrefixAndNeedsATarget(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openBucketCopy("acme-media-archive", false)
	h.settle()
	f := &h.a.objOps.bc
	f.prefix = "a//b"
	h.tt.Frame()
	h.mustHave("No empty folder names")
	if f.canSubmit() {
		t.Error("a prefix with an empty folder name was accepted")
	}
	f.prefix = " /backup/nightly "
	if got := f.destScope("acct").Prefix; got != "backup/nightly/" {
		t.Errorf("normalised prefix = %q", got)
	}
	if normPrefix("/") != "" || normPrefix("") != "" || normPrefix("x/") != "x/" {
		t.Error("normPrefix")
	}
}

func TestMoveContentsNeedsTheExtraConfirmation(t *testing.T) {
	h := newRawHarness(t, "light", true)
	before := h.countObjects("acme-website")
	h.rightClickChoose("acme-website", "Move contents to…")
	h.mustHave("Move contents to…", "Continue")
	h.mustNotHave("Move everything")
	h.a.objOps.bc.dest = "acme-logs"
	h.a.objOps.bc.prefix = "site/"
	h.mustClick("Continue")
	h.mustHave("Move everything in acme-website?", "To", "acme-logs/site/", "Each object is deleted from acme-website once it is copied. This cannot be undone.",
		"Back", "Move everything")
	h.snapBoth("move-confirm")
	if n := h.countObjects("acme-website"); n != before {
		t.Fatalf("the move ran before it was confirmed (%d of %d objects left)", n, before)
	}
	if _, ok := h.task("Move everything in acme-website"); ok {
		t.Fatal("a move task was queued before the confirmation")
	}
	// Back returns to the options without moving anything.
	h.mustClick("Back")
	h.mustHave("Target bucket")
	h.mustClick("Continue")
	h.mustClick("Move everything")
	h.waitTask("Move everything in acme-website", core.StatusSucceeded)
	h.waitToast("Moved " + plural(before, "item", "items"))
	if n := h.countObjects("acme-website"); n != 0 {
		t.Errorf("%d objects left in the source after the move", n)
	}
	if !h.hasObject("acme-logs", "site/index.html") || !h.hasObject("acme-logs", "site/about.html") {
		t.Error("the objects are not in the target")
	}
}

// ---- signed URL --------------------------------------------------------------------------------------

func (h *harness) openSignedFor(keys ...string) {
	h.t.Helper()
	h.a.openSigned(keys, "")
	h.settle()
	h.mustHave("Signed URL")
}

// expiresOf reads X-Amz-Expires from a signed URL.
func expiresOf(t *testing.T, u string) string {
	t.Helper()
	i := strings.Index(u, "X-Amz-Expires=")
	if i < 0 {
		t.Fatalf("no X-Amz-Expires in %s", u)
	}
	v := u[i+len("X-Amz-Expires="):]
	if j := strings.IndexByte(v, '&'); j >= 0 {
		v = v[:j]
	}
	return v
}

func TestSignedURLPresets(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.openSignedFor("manifest.json")
	h.mustHave("Method", "GET", "PUT", "Valid for", "15 min", "1 hour", "1 day", "7 days", "Custom", "Version ID (optional)", "Generate", "Cancel")
	h.snapBoth("signed-options")
	for label, want := range map[string]string{"15 min": "900", "1 day": "86400", "7 days": "604800", "1 hour": "3600"} {
		h.mustClick(label)
		h.mustClick("Generate")
		h.waitFor("signed "+label, func() bool { return len(h.a.objOps.sg.res) == 1 })
		if got := expiresOf(t, h.a.objOps.sg.res[0].URL); got != want {
			t.Errorf("%s signs X-Amz-Expires=%s, want %s", label, got, want)
		}
		h.a.objOps.sg.res = nil // back to the options
		h.tt.Frame()
	}
}

func TestSignedURLResultCopiesAndShowsExpiry(t *testing.T) {
	h := newHarness(t, "light")
	h.openSignedFor("manifest.json")
	h.mustClick("Generate")
	h.waitFor("result", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.mustHave("Copy public URL", "Close", "GET · expires ", "valid for 1 hour")
	h.snapBoth("signed-result")
	if time.Until(h.a.objOps.sg.expires) < 59*time.Minute || time.Until(h.a.objOps.sg.expires) > time.Hour {
		t.Errorf("expires %v", h.a.objOps.sg.expires)
	}
	h.clickBeside("Close", 20) // Copy
	h.waitFor("copied", func() bool { return strings.Contains(h.tt.Clipboard(), "X-Amz-Signature") })
	if got := h.tt.Clipboard(); got != h.a.objOps.sg.res[0].URL {
		t.Errorf("clipboard %q", got)
	}
	h.mustClick("Copy public URL")
	if got, want := h.tt.Clipboard(), h.env.Srv.URL+"/acme-assets/manifest.json"; got != want {
		t.Errorf("public URL copied %q, want %q", got, want)
	}
	res, err := http.Get(h.a.objOps.sg.res[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("signed URL status %d", res.StatusCode)
	}
}

func TestSignedURLCustomExpiryIsChecked(t *testing.T) {
	h := newHarness(t, "light")
	h.openSignedFor("manifest.json")
	h.mustClick("Custom")
	h.mustHave("Minutes", "Hours", "Days")
	check := func(amount string, unit int, wantTTL time.Duration, wantErr string) {
		t.Helper()
		f := &h.a.objOps.sg
		f.amount, f.unit = amount, unit
		h.tt.Frame()
		ttl, msg := f.ttl()
		if ttl != wantTTL || msg != wantErr {
			t.Errorf("custom %q unit %d = %v %q, want %v %q", amount, unit, ttl, msg, wantTTL, wantErr)
		}
		if wantErr != "" {
			h.mustHave(wantErr)
		}
	}
	check("", 1, 0, "")
	check("0", 1, 0, "Enter a whole number")
	check("abc", 1, 0, "Enter a whole number")
	check("2.5", 1, 0, "Enter a whole number")
	check("8", 2, 0, "At most 7 days")
	check("169", 1, 0, "At most 7 days")
	check("10081", 0, 0, "At most 7 days")
	check("99999999999", 0, 0, "At most 7 days")
	check("7", 2, 7*24*time.Hour, "")
	check("10080", 0, 7*24*time.Hour, "")
	check("36", 1, 36*time.Hour, "")

	// An invalid amount cannot be generated; a valid one signs it.
	h.a.objOps.sg.amount, h.a.objOps.sg.unit = "8", 2
	h.tt.Frame()
	h.a.generateSigned()
	h.settle()
	if len(h.a.objOps.sg.res) != 0 {
		t.Fatal("generated with an invalid expiry")
	}
	h.snap("signed-custom-invalid-light")
	h.a.objOps.sg.amount, h.a.objOps.sg.unit = "", 1
	h.tt.Frame()
	h.tt.Type("36") // the amount field has the keyboard
	h.settle()
	if h.a.objOps.sg.amount != "36" {
		t.Fatalf("typed amount = %q", h.a.objOps.sg.amount)
	}
	h.mustClick("Generate")
	h.waitFor("custom signed", func() bool { return len(h.a.objOps.sg.res) == 1 })
	if got := expiresOf(t, h.a.objOps.sg.res[0].URL); got != "129600" {
		t.Errorf("36 hours = X-Amz-Expires=%s", got)
	}
	h.mustHave("valid for 36 hours")
}

func TestSignedURLPutUploads(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.openSignedFor("README.txt")
	h.mustClick("PUT")
	h.mustNotHave("Version ID (optional)") // a PUT URL cannot name a version
	h.mustClick("Generate")
	h.waitFor("put url", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.mustHave("PUT · expires ")
	req, _ := http.NewRequest(http.MethodPut, h.a.objOps.sg.res[0].URL, strings.NewReader("replaced through a signed URL"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("PUT status %d", res.StatusCode)
	}
	if got, _ := h.env.Srv.Object(bkt, "README.txt"); string(got) != "replaced through a signed URL" {
		t.Errorf("object after the PUT = %q", got)
	}
}

func TestSignedURLVersionIDIsPrefilledFromTheVersionsTab(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.mustClick("notes.txt")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	vs := h.env.Srv.Versions(demoVersioned, "notes.txt")
	if len(vs) != 3 {
		t.Fatalf("seed has %d versions", len(vs))
	}
	// A version chosen in the tab is prefilled in the dialog the command opens.
	h.a.mg.vers.sel = map[string]bool{vs[1].VersionID: true} // the row's click, whose short id is the same on every row of the test server
	h.tt.Frame()
	h.a.run("copysigned", "test")
	h.settle()
	if h.a.objOps.sg.version != vs[1].VersionID {
		t.Fatalf("version field = %q, want %q", h.a.objOps.sg.version, vs[1].VersionID)
	}
	h.snap("signed-version-light")
	h.mustClick("Generate")
	h.waitFor("versioned url", func() bool { return len(h.a.objOps.sg.res) == 1 })
	if u := h.a.objOps.sg.res[0].URL; !strings.Contains(u, "versionId="+vs[1].VersionID) {
		t.Errorf("signed URL lacks the version: %s", u)
	}
	h.mustClick("Close")
	if h.a.objOps.sg.open {
		t.Fatal("Close left the dialog open")
	}

	// The row's own button opens the dialog for that very version.
	h.a.mg.vers.sel = nil
	h.a.objOps.sg = signedForm{}
	h.tt.Frame() // the "1 version selected" bar is gone and the rows moved up
	h.mustClick("Signed URL for this version")
	if !h.a.objOps.sg.open || h.a.objOps.sg.version != vs[0].VersionID {
		t.Errorf("row button prefilled %q, want the latest %q", h.a.objOps.sg.version, vs[0].VersionID)
	}
}

func TestSignedURLForSeveralObjects(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Shift, "manifest.json")
	h.settle()
	h.a.run("copysigned", "test")
	h.settle()
	h.mustHave("Objects", "2 objects")
	h.mustNotHave("Version ID (optional)")
	h.mustClick("Generate")
	h.waitFor("two urls", func() bool { return len(h.a.objOps.sg.res) == 2 })
	h.mustHave("manifest.json", "README.txt", "Copy all", "Copy public URL")
	h.snapBoth("signed-multi")
	h.clickBeside("Close", 20)
	lines := strings.Split(h.tt.Clipboard(), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "/acme-assets/manifest.json") || !strings.Contains(lines[1], "/acme-assets/README.txt") {
		t.Errorf("copied %q", lines)
	}
	// A folder in the selection is not an object: nothing to sign for it alone.
	h.a.objOps.sg.open = false
	h.a.sel.Clear()
	h.mustClick("assets")
	if h.a.enabled(h.a.cmd("copysigned")) {
		t.Error("signed URL enabled for a folder")
	}
}

// ---- flat view --------------------------------------------------------------------------------------

// flatObjects counts the objects below prefix the way a flat listing shows them.
func (h *harness) flatObjects(bucket, prefix string) int {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, token := 0, ""
	for {
		pg, err := h.env.Svc.List(ctx, core.Location{AccountID: h.env.Account.ID, Bucket: bucket, Prefix: prefix}, core.ListOptions{Flat: true, Token: token})
		if err != nil {
			h.t.Fatal(err)
		}
		for _, e := range pg.Entries {
			if !e.Folder {
				n++
			}
		}
		if pg.NextToken == "" {
			return n
		}
		token = pg.NextToken
	}
}

func TestFlatViewListsNestedKeysWithRelativeNames(t *testing.T) {
	h := newHarness(t, "light")
	want := h.flatObjects(bkt, "")
	if want < 5000 {
		t.Fatalf("seed has %d objects", want)
	}
	h.env.Srv.ResetRequests()
	h.mustClick("Flat")
	h.waitFor("flat listing", func() bool { return !h.a.loading && len(h.a.rows) == want })
	if !h.a.objOps.flat {
		t.Fatal("flat is off after the click")
	}
	for _, e := range h.a.rows {
		if e.Folder {
			t.Fatalf("flat view shows the folder %q", e.Key)
		}
		if e.Name != e.Key {
			t.Fatalf("name %q is not the path relative to the root %q", e.Name, e.Key)
		}
	}
	h.mustHave("assets/audio/podcast-ep12.mp3", "assets/fonts/Inter-Regular.woff2")
	pages := 0
	for _, r := range h.env.Srv.Requests() {
		if strings.Contains(r.RawQuery, "list-type=2") && !strings.Contains(r.RawQuery, "delimiter") {
			pages++
		}
	}
	if pages < 6 {
		t.Errorf("flat listing used %d paged requests, want at least 6 for %d objects", pages, want)
	}
	h.snapBoth("flat")

	// Selection and actions work on the listed objects; rename does not (the row
	// lives in another folder).
	h.mustClick("assets/audio/podcast-ep12.mp3")
	if keys := h.a.selectedKeys(); len(keys) != 1 || keys[0] != "assets/audio/podcast-ep12.mp3" {
		t.Fatalf("selection = %v", keys)
	}
	if !h.a.enabled(h.a.cmd("download")) || !h.a.enabled(h.a.cmd("delete")) || h.a.enabled(h.a.cmd("rename")) {
		t.Error("commands do not follow the flat selection")
	}
	h.a.run("copykey", "test")
	h.tt.Frame()
	if got := h.tt.Clipboard(); got != "assets/audio/podcast-ep12.mp3" {
		t.Errorf("copied key %q", got)
	}

	// Below a folder, names are relative to it; toggling off brings the folders back.
	h.a.openPrefix("assets/")
	h.settle()
	if !h.a.objOps.flat {
		t.Fatal("opening a folder turned flat off")
	}
	if want := h.flatObjects(bkt, "assets/"); len(h.a.rows) != want {
		t.Fatalf("%d rows below assets/, want %d", len(h.a.rows), want)
	}
	if i, ok := h.a.byKey["assets/images/hero.png"]; !ok || h.a.rows[i].Name != "images/hero.png" {
		t.Fatalf("hero.png row = %+v", h.a.rows)
	}
	h.mustHave("images/hero.png")
	h.snap("flat-prefix-light")
	h.mustClick("Flat")
	h.settle()
	if h.a.objOps.flat || !h.has("assets/images/") || !h.a.rows[0].Folder {
		t.Fatalf("flat off: rows %d first %+v", len(h.a.rows), h.a.rows[0])
	}
}

func TestFlatViewIsOffInDeletedViewAndAnotherBucket(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.a.toggleFlat()
	h.settle()
	if !h.a.objOps.flat {
		t.Fatal("flat did not turn on")
	}
	h.a.toggleDeleted()
	h.settle()
	if h.a.objOps.flat || !h.a.mg.del.on {
		t.Fatalf("flat=%v deleted=%v: the deleted listing is a folder view", h.a.objOps.flat, h.a.mg.del.on)
	}
	h.a.toggleFlat()
	if h.a.objOps.flat {
		t.Error("flat turned on over the deleted listing")
	}
	h.a.toggleDeleted()
	h.a.toggleFlat()
	h.settle()
	h.a.openBucket("acme-assets")
	h.settle()
	if h.a.objOps.flat {
		t.Error("flat survived opening another bucket")
	}
}

func TestFlatEntries(t *testing.T) {
	in := []core.Entry{
		{Key: "a/b/c.txt", Name: "c.txt"}, {Key: "a/d/", Name: "d", Folder: true}, {Key: "a/e.txt", Name: "e.txt"},
	}
	out := flatEntries(in, "a/")
	if len(out) != 2 || out[0].Name != "b/c.txt" || out[1].Name != "e.txt" {
		t.Fatalf("flatEntries = %+v", out)
	}
	if in[0].Name != "c.txt" {
		t.Error("the page was modified in place")
	}
}

// ---- preview ----------------------------------------------------------------------------------------

func (h *harness) previewOf(key string) {
	h.t.Helper()
	h.a.selectAfter = key
	h.a.reload()
	h.settle()
	h.mustClick(key)
	h.mustClick("Preview")
	h.waitFor("preview of "+key, func() bool {
		return h.a.prev.key == key && h.a.prev.state != previewLoading && h.a.prev.state != previewIdle
	})
}

func TestPreviewJSONIsPrettyPrintedInTheEditor(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.PutObject(bkt, "compact.json", []byte(`{"b":[1,2,{"c":null}],"a":"x"}`), "application/json", nil)
	h.previewOf("compact.json")
	p := &h.a.prev
	if p.kind != "json" || p.state != previewReady || p.ed == nil || !p.ed.ReadOnly {
		t.Fatalf("preview = kind %q state %d ed %v", p.kind, p.state, p.ed)
	}
	want := "{\n  \"b\": [\n    1,\n    2,\n    {\n      \"c\": null\n    }\n  ],\n  \"a\": \"x\"\n}"
	if p.text != want || p.ed.Buffer.String() != want {
		t.Fatalf("text %q", p.text)
	}
	h.mustNotHave("Preview limited")
	h.snapBoth("preview-json")
}

func TestPreviewCSVIsATableWithABoldHeader(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openPrefix("reports/")
	h.settle()
	h.mustClick("customers-export.csv")
	h.mustClick("Preview")
	h.waitFor("csv", func() bool { return h.a.prev.state == previewReady })
	p := &h.a.prev
	if p.kind != "csv" || len(p.rows) != 7 || len(p.colW) != 5 {
		t.Fatalf("csv preview: kind %q rows %d widths %d", p.kind, len(p.rows), len(p.colW))
	}
	if p.rows[0][0] != "id" || p.rows[0][4] != "mrr" || p.rows[1][1] != "Northwind Traders" {
		t.Errorf("rows %v", p.rows[:2])
	}
	h.mustHave("id", "name", "Northwind Traders")
	h.snapBoth("preview-csv")
}

func TestPreviewBinaryIsAHexDump(t *testing.T) {
	h := newHarness(t, "light")
	body := make([]byte, 6000)
	for i := range body {
		body[i] = byte(i)
	}
	h.env.Srv.PutObject(bkt, "blob.bin", body, "application/octet-stream", nil)
	h.previewOf("blob.bin")
	p := &h.a.prev
	if p.kind != "hex" || p.state != previewReady {
		t.Fatalf("preview kind %q state %d", p.kind, p.state)
	}
	if n := strings.Count(p.text, "\n") + 1; n != 4096/8 {
		t.Errorf("hex dump has %d lines, want %d (4 KiB)", n, 4096/8)
	}
	if !strings.HasPrefix(p.text, "0000  00 01 02 03 04 05 06 07  ........\n0008  08 09") {
		t.Errorf("first line %q", strings.SplitN(p.text, "\n", 2)[0])
	}
	h.mustHave("Showing first 4 KiB")
	h.snapBoth("preview-hex")

	// A small binary shows whole, without the note.
	h.env.Srv.PutObject(bkt, "tiny.bin", []byte{0, 1, 2, 'A', 'B'}, "application/octet-stream", nil)
	h.previewOf("tiny.bin")
	if h.a.prev.kind != "hex" || h.a.prev.note != "" {
		t.Errorf("tiny: kind %q note %q", h.a.prev.kind, h.a.prev.note)
	}
}

func TestPreviewLargeTextSaysItIsLimited(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.PutObject(bkt, "big.txt", []byte(strings.Repeat("lorem ipsum dolor sit amet\n", 60_000)), "text/plain", nil)
	h.previewOf("big.txt")
	p := &h.a.prev
	if p.kind != "text" || p.state != previewReady || len(p.text) != previewLimit {
		t.Fatalf("kind %q state %d len %d", p.kind, p.state, len(p.text))
	}
	if p.note != "Preview limited to the first 1.0 MB" {
		t.Errorf("note %q", p.note)
	}
	h.mustHave("Preview limited to the first 1.0 MB")
	h.snap("preview-limited-light")
}

func TestPreviewXMLMarkdownAndUnknownText(t *testing.T) {
	h := newHarness(t, "light")
	for key, body := range map[string]string{"feed.xml": "<a><b>1</b></a>\n", "notes.md": "# Title\n\nSome *text*\n", "NOTES": "plain text without an extension\n"} {
		h.env.Srv.PutObject(bkt, key, []byte(body), "application/octet-stream", nil)
		h.previewOf(key)
		if h.a.prev.kind != "text" || h.a.prev.text != body || h.a.prev.note != "" {
			t.Errorf("%s: kind %q text %q note %q", key, h.a.prev.kind, h.a.prev.text, h.a.prev.note)
		}
	}
	if previewLanguage("text", "feed.xml", "") != "xml" || previewLanguage("text", "notes.md", "") != "markdown" || previewLanguage("json", "x", "") != "json" {
		t.Error("previewLanguage")
	}
}

func TestBuildPreview(t *testing.T) {
	var rows strings.Builder
	rows.WriteString("a,b\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&rows, "%d,%d\n", i, i*2)
	}
	b := buildPreview("csv", false, []byte(rows.String()), false, previewLimit)
	if b.kind != "csv" || len(b.rows) != previewCSVRows || b.note != "Showing the first 200 rows" {
		t.Errorf("long csv: kind %q rows %d note %q", b.kind, len(b.rows), b.note)
	}
	// A cut-off CSV drops its last, possibly partial, row.
	b = buildPreview("csv", false, []byte("a,b\n1,2\n3,"), true, 11)
	if len(b.rows) != 2 || b.note != "Preview limited to the first 11 B" {
		t.Errorf("cut csv: rows %v note %q", b.rows, b.note)
	}
	// TSV.
	b = buildPreview("csv", true, []byte("a\tb\n1\t2\n"), false, previewLimit)
	if len(b.rows) != 2 || b.rows[1][1] != "2" {
		t.Errorf("tsv rows %v", b.rows)
	}
	// Broken JSON is shown as it is; truncated JSON is not reformatted.
	if b = buildPreview("json", false, []byte(`{"a":`), false, previewLimit); b.text != `{"a":` || b.kind != "json" {
		t.Errorf("broken json: %+v", b)
	}
	if b = buildPreview("json", false, []byte(`{"a":1}`), true, 7); b.text != `{"a":1}` || b.note == "" {
		t.Errorf("cut json: %+v", b)
	}
	// NUL bytes make a text name a hex dump.
	if b = buildPreview("text", false, []byte("abc\x00def"), false, previewLimit); b.kind != "hex" {
		t.Errorf("binary text: kind %q", b.kind)
	}
	// Unknown kinds are hex dumps, unless an extension-less, untyped file reads as text.
	if b = buildPreview("", false, []byte("looks like text\n"), false, previewHexLen); b.kind != "hex" {
		t.Errorf("unknown kind: %q", b.kind)
	}
	if b = buildPreview("sniff", false, []byte("looks like text\n"), false, previewHexLen); b.kind != "text" {
		t.Errorf("sniffed text: %q", b.kind)
	}
	if b = buildPreview("sniff", false, []byte{1, 2, 3, 0xff}, false, previewHexLen); b.kind != "hex" {
		t.Errorf("sniffed binary: %q", b.kind)
	}
	for _, c := range []struct {
		key, ct string
		want    bool
	}{{"Makefile", "", true}, {"LICENSE", "application/octet-stream; x=y", true}, {"clip", "video/mp4", false}, {"a.bin", "", false}} {
		if got := sniffable(c.key, c.ct); got != c.want {
			t.Errorf("sniffable(%q, %q) = %v", c.key, c.ct, got)
		}
	}
	// A character cut by the end of the range does not make text binary.
	cut := []byte("héllo")[:2]
	if b = buildPreview("text", false, cut, true, 2); b.kind != "text" {
		t.Errorf("split rune: kind %q", b.kind)
	}
	wantHex := "0000  30 31 32 33 34 35 36 37  01234567\n" +
		"0008  38 39 61 62 63 64 65 66  89abcdef\n" +
		"0010  58 59 5a 0a" + strings.Repeat(" ", 14) + "XYZ."
	if got := hexDump([]byte("0123456789abcdefXYZ\n")); got != wantHex {
		t.Errorf("hexDump = %q, want %q", got, wantHex)
	}
	for _, c := range []struct{ key, ct, want string }{
		{"a.json", "", "json"}, {"a", "application/json", "json"}, {"a", "application/vnd.api+json", "json"},
		{"a.csv", "", "csv"}, {"a.tsv", "", "csv"}, {"a", "text/csv", "csv"}, {"a.jsonl", "", "text"},
		{"a.xml", "", "text"}, {"a.md", "", "text"}, {"a.png", "", "image"}, {"a.bin", "application/octet-stream", ""},
		{"a.mp4", "video/mp4", ""}, {"a", "", ""},
	} {
		if got := previewKind(c.key, c.ct); got != c.want {
			t.Errorf("previewKind(%q, %q) = %q, want %q", c.key, c.ct, got, c.want)
		}
	}
}

// ---- settings ---------------------------------------------------------------------------------------

func TestSettingsLogSwitchPersistsAndOpensTheLogFolder(t *testing.T) {
	h := newRawHarness(t, "light", true)
	opened := make(chan string, 2)
	h.a.openPath = func(p string) error { opened <- p; return nil }
	h.a.openTool("settings")
	h.settle()
	h.mustHave("DIAGNOSTICS", "Write diagnostic log", "Open log folder")
	if h.env.Svc.Settings().LogToFile {
		t.Fatal("the log starts on")
	}
	h.snapBoth("settings-log-off")

	// The switch sits right of its label; clicking it saves at once.
	h.clickSettingSwitch("Write diagnostic log")
	h.waitFor("log setting saved", func() bool { return h.env.Svc.Settings().LogToFile })
	if !h.a.sf.logOn {
		t.Error("the switch does not show on")
	}
	raw, err := os.ReadFile(filepath.Join(h.env.Dir, "settings.json"))
	if err != nil || !strings.Contains(string(raw), `"logToFile": true`) && !strings.Contains(string(raw), `"logToFile":true`) {
		t.Errorf("settings.json = %s (%v)", raw, err)
	}
	// Changing another setting keeps it.
	h.editSetting("Part size (MiB)", "24")
	h.waitFor("part size saved", func() bool { return h.env.Svc.Settings().PartSizeMiB == 24 })
	if !h.env.Svc.Settings().LogToFile {
		t.Error("saving another setting turned the log off")
	}
	h.tt.Advance(time.Second) // the switch's knob slides
	h.settle()
	h.snapBoth("settings-log-on")

	h.mustClick("Open log folder")
	select {
	case p := <-opened:
		if p != h.env.Svc.LogDir() {
			t.Errorf("opened %q, want %q", p, h.env.Svc.LogDir())
		}
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Errorf("the log folder does not exist: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open log folder did not open anything")
	}

	// And off again.
	h.clickSettingSwitch("Write diagnostic log")
	h.waitFor("log setting cleared", func() bool { return !h.env.Svc.Settings().LogToFile })
}

func TestOpenLogFolderFailureIsReported(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openPath = func(string) error { return fmt.Errorf("no file manager") }
	h.a.openTool("settings")
	h.settle()
	h.mustClick("Open log folder")
	h.waitToast("Could not open the log folder: no file manager")
}
