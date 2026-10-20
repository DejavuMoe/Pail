package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

const testCDN = "https://s3-cdn.example.com"

// setCDN stores the CDN settings on the demo account and refreshes the app's
// account list, as the account dialog does.
func (h *harness) setCDN(cdn string, buckets ...string) {
	h.t.Helper()
	ac := h.env.Svc.Accounts()[0]
	ac.CDNURL, ac.CDNBuckets = cdn, buckets
	if _, err := h.env.Svc.SaveAccount(ac, nil); err != nil {
		h.t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.settle()
}

// opener swaps in an injected URL opener and returns the channel it reports to.
func (h *harness) opener() chan string {
	ch := make(chan string, 4)
	h.a.openURL = func(u string) error { ch <- u; return nil }
	return ch
}

func (h *harness) runCmd(id string) {
	h.t.Helper()
	h.a.run(id, "test")
	h.tt.Frame()
	time.Sleep(150 * time.Millisecond) // past the double-delivery guard
}

func TestCDNInspectorRowCopyAndOpen(t *testing.T) {
	h := newHarness(t, "light")
	opened := h.opener()
	h.mustClick("manifest.json")
	h.mustNotHave("CDN URL") // not configured yet

	h.setCDN(testCDN)
	h.mustHave("CDN URL", testCDN+"/acme-assets/manifest.json")
	h.snapBoth("cdn-inspector")

	h.mustClick("Copy CDN URL")
	h.waitFor("clipboard", func() bool { return h.tt.Clipboard() == testCDN+"/acme-assets/manifest.json" })
	h.waitToast("CDN URL copied")

	h.mustClick("Open CDN URL")
	select {
	case u := <-opened:
		if u != testCDN+"/acme-assets/manifest.json" {
			t.Errorf("opened %q", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open CDN URL opened nothing")
	}
}

func TestCDNOpenFailureIsReported(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openURL = func(string) error { return fmt.Errorf("no browser") }
	h.setCDN(testCDN)
	h.mustClick("manifest.json")
	h.mustClick("Open CDN URL")
	h.waitToast("Could not open the link: no browser")
}

func TestCDNCommandsFollowTheSelection(t *testing.T) {
	h := newHarness(t, "light")
	opened := h.opener()
	h.setCDN(testCDN)

	// Nothing selected: both are visible for this bucket but disabled.
	for _, id := range []string{"copycdn", "opencdn"} {
		if h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s enabled without a selection", id)
		}
	}
	// A folder alone has no CDN link.
	h.mustClick("assets")
	for _, id := range []string{"copycdn", "opencdn"} {
		if h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s enabled for a folder", id)
		}
	}

	// One object: copy and open work.
	h.mustClick("manifest.json")
	h.runCmd("copycdn")
	if got := h.tt.Clipboard(); got != testCDN+"/acme-assets/manifest.json" {
		t.Errorf("copycdn copied %q", got)
	}
	h.runCmd("opencdn")
	select {
	case u := <-opened:
		if u != testCDN+"/acme-assets/manifest.json" {
			t.Errorf("opencdn opened %q", u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opencdn opened nothing")
	}

	// Several objects plus a folder: the objects' links, newline-joined; open is single only.
	h.mustClick("assets")
	h.tt.ClickWith(ui.Cmd, "manifest.json")
	h.tt.ClickWith(ui.Cmd, "README.txt")
	h.settle()
	if n := h.a.sel.Len(); n != 3 {
		t.Fatalf("selection = %d, want 3 (%v)", n, h.a.selectedKeys())
	}
	if h.a.enabled(h.a.cmd("opencdn")) {
		t.Error("opencdn enabled for several rows")
	}
	h.runCmd("copycdn")
	want := testCDN + "/acme-assets/manifest.json\n" + testCDN + "/acme-assets/README.txt"
	if got := h.tt.Clipboard(); got != want {
		t.Errorf("multi copy = %q, want %q", got, want)
	}
	h.waitToast("2 CDN URLs copied")
}

func TestCDNContextMenuAndBucketFilter(t *testing.T) {
	h := newHarness(t, "light")
	menuOf := func() []string {
		t.Helper()
		if err := h.tt.RightClick("manifest.json"); err != nil {
			t.Fatal(err)
		}
		h.tt.Frame()
		m := h.tt.Menu()
		h.tt.CloseMenu()
		h.settle()
		return m
	}
	if m := menuOf(); contains(m, "Copy CDN URL") || contains(m, "Open CDN URL") {
		t.Errorf("CDN items without a CDN: %q", m)
	}

	h.setCDN(testCDN)
	m := menuOf()
	if missing := menuHas(m, "Copy URL", "Copy CDN URL", "Open CDN URL"); len(missing) > 0 {
		t.Errorf("context menu %q misses %q", m, missing)
	}

	// Limited to another bucket: nothing for this one.
	h.setCDN(testCDN, "other-bucket")
	if m := menuOf(); contains(m, "Copy CDN URL") || contains(m, "Open CDN URL") {
		t.Errorf("CDN items for a filtered-out bucket: %q", m)
	}
	h.mustClick("manifest.json")
	h.mustNotHave("CDN URL")
	for _, id := range []string{"copycdn", "opencdn"} {
		if h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s enabled for a filtered-out bucket", id)
		}
	}

	// Listing this bucket brings it back.
	h.setCDN(testCDN, "other-bucket", bkt)
	h.mustClick("manifest.json")
	h.mustHave("CDN URL")
}

func TestCDNContextMenuChoose(t *testing.T) {
	h := newHarness(t, "light")
	h.setCDN("https://{bucket}.cdn.example.com/{key}")
	h.rightClickChoose("manifest.json", "Copy CDN URL")
	if got := h.tt.Clipboard(); got != "https://acme-assets.cdn.example.com/manifest.json" {
		t.Errorf("copied %q", got)
	}
}

func TestSignedURLDialogCopiesTheCDNURLWhenConfigured(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.a.run("copysigned", "test")
	h.settle()
	h.mustClick("Generate")
	h.waitFor("signed url shown", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.mustHave("Copy public URL")
	h.mustNotHave("Copy CDN URL")
	h.mustClick("Close")

	h.setCDN(testCDN)
	h.a.inspectorOpen = false // its own Copy CDN URL button would be found first
	h.a.run("copysigned", "test")
	h.settle()
	h.mustClick("Generate")
	h.waitFor("signed url shown", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.mustHave("Copy CDN URL")
	h.mustNotHave("Copy public URL")
	h.mustClick("Copy CDN URL")
	h.waitFor("clipboard", func() bool { return h.tt.Clipboard() == testCDN+"/acme-assets/manifest.json" })
}

func TestUploadToastOffersTheCDNLinkForOneFile(t *testing.T) {
	h := newHarness(t, "light")
	h.setCDN(testCDN)
	p := writeTemp(t, "hello cdn+1.txt", []byte("hi"))
	h.a.uploadPaths([]string{p})
	h.waitTask("Upload hello cdn+1.txt", core.StatusSucceeded)
	h.waitFor("toast action", func() bool { return h.tt.HasText("Copy CDN link") })
	h.mustClick("Copy CDN link")
	want := testCDN + "/acme-assets/hello%20cdn%2B1.txt"
	h.waitFor("clipboard", func() bool { return h.tt.Clipboard() == want })
}

func TestUploadToastHasNoCDNActionForSeveralFilesOrNoCDN(t *testing.T) {
	h := newHarness(t, "light")
	a := writeTemp(t, "one.txt", []byte("1"))
	b := writeTemp(t, "two.txt", []byte("2"))

	// Several files with a CDN: plain toast.
	h.setCDN(testCDN)
	h.a.uploadPaths([]string{a, b})
	h.waitTask("Upload one.txt", core.StatusSucceeded)
	h.waitTask("Upload two.txt", core.StatusSucceeded)
	h.waitToast("Uploaded 2 files")
	h.mustNotHave("Copy CDN link")

	// One file without a CDN: plain toast.
	h.setCDN("")
	c := writeTemp(t, "three.txt", []byte("3"))
	h.a.uploadPaths([]string{c})
	h.waitTask("Upload three.txt", core.StatusSucceeded)
	h.waitToast("Uploaded 1 file")
	h.mustNotHave("Copy CDN link")
}

// ---- account dialog ----------------------------------------------------------------------------

func TestAccountDialogCDNFieldsSaveAndFill(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(true)
	h.settle()
	h.mustHave("CDN URL", "CDN buckets")
	h.a.af.cdnURL, h.a.af.cdnBuckets = testCDN, "acme-assets, acme-backups"
	h.settle()
	h.snapBoth("cdn-account-dialog")
	h.a.af.cdnURL = " " + testCDN + "/ "
	h.a.af.cdnBuckets = "acme-assets, acme-backups ,,acme-assets"
	h.mustClick("Save")
	h.waitFor("saved", func() bool { return !h.a.af.open && h.env.Svc.Accounts()[0].CDNURL != "" })
	got := h.env.Svc.Accounts()[0]
	if got.CDNURL != testCDN || strings.Join(got.CDNBuckets, ",") != "acme-assets,acme-backups" {
		t.Fatalf("saved %q %q", got.CDNURL, got.CDNBuckets)
	}
	h.waitFor("buckets reloaded", func() bool { return len(h.a.buckets) == len(demoBuckets) && h.a.bucketsErr == "" })

	// Editing again shows the stored values; clearing them removes the CDN.
	h.a.openAccountForm(true)
	h.settle()
	if h.a.af.cdnURL != testCDN || h.a.af.cdnBuckets != "acme-assets, acme-backups" {
		t.Fatalf("form %q %q", h.a.af.cdnURL, h.a.af.cdnBuckets)
	}
	h.a.af.cdnURL, h.a.af.cdnBuckets = "", ""
	h.mustClick("Save")
	h.waitFor("cleared", func() bool { return !h.a.af.open && h.env.Svc.Accounts()[0].CDNURL == "" })
	if b := h.env.Svc.Accounts()[0].CDNBuckets; len(b) != 0 {
		t.Errorf("buckets not cleared: %q", b)
	}
}

func TestAccountDialogCDNValidation(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(true)
	h.settle()
	h.a.af.cdnURL = "ftp://cdn.example.com"
	h.a.af.cdnBuckets = "Not_A_Bucket"
	h.mustClick("Save")
	h.mustHave("CDN URL must be an http(s) URL without credentials, query or fragment")
	h.mustHave("Not_A_Bucket: bucket name")
	h.snapBoth("cdn-account-dialog-errors")
	if !h.a.af.open || h.env.Svc.Accounts()[0].CDNURL != "" {
		t.Fatal("an invalid CDN was saved")
	}

	h.a.af.cdnURL, h.a.af.cdnBuckets = "https://{bucket}.cdn.example.com", ""
	h.settle()
	h.mustHave("CDN URL template with {bucket} must also contain {key}")
	h.mustNotHave("Not_A_Bucket")
	h.a.af.cdnURL = "https://{bucket}.cdn.example.com/{key}"
	h.settle()
	h.mustNotHave("must also contain")
	h.mustClick("Save")
	h.waitFor("saved", func() bool { return !h.a.af.open })
	if got := h.env.Svc.Accounts()[0].CDNURL; got != "https://{bucket}.cdn.example.com/{key}" {
		t.Errorf("saved template %q", got)
	}
}
