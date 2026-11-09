package main

import (
	"net/http"
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

// restrictKey makes the demo server behave like a key whose policy allows
// only the folder dir/ of the bucket data (s3:ListBucket on that prefix,
// object actions below it): every other request, ListBuckets and the
// bucket-level reads the window makes when it opens a bucket included, is
// denied.
func (h *harness) restrictKey() {
	h.env.Srv.CreateBucket("data", false)
	h.env.Srv.PutObject("data", "dir/a.txt", []byte("a"), "text/plain", nil)
	h.env.Srv.PutObject("data", "dir/sub/b.txt", []byte("b"), "text/plain", nil)
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/data" && q.Get("list-type") == "2" && strings.HasPrefix(q.Get("prefix"), "dir/"):
			return nil
		case strings.HasPrefix(r.URL.Path, "/data/dir/") && r.URL.RawQuery == "":
			return nil
		}
		return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
	})
}

func TestNamedBucketsConnectAndOpenTheirFolder(t *testing.T) {
	h := newRawHarness(t, "light", false)
	h.restrictKey()
	h.a.openAccountForm(false)
	h.fillAccount("Restricted", h.env.Srv.URL, "restricted", "restricted-secret")
	h.a.af.path = true
	h.mustHave("Buckets")

	// Without named buckets the test is refused, and the dialog says what to do.
	h.mustClick("Test connection")
	h.waitFor("denied test", func() bool { return h.a.af.test == testFailed })
	h.mustHave("(AccessDenied)", "name them under Buckets")

	// Named with its folder, the test lists that folder and passes.
	h.a.af.buckets = "data/dir/"
	h.settle()
	h.mustClick("Test connection")
	h.waitFor("test result", func() bool { return h.a.af.test == testOK || h.a.af.test == testFailed })
	if h.a.af.test != testOK {
		t.Fatalf("test with a named bucket failed: %s", h.a.af.testMsg)
	}
	h.snap("account-named-buckets-light")

	h.mustClick("Save")
	h.waitFor("account saved", func() bool { return len(h.env.Svc.Accounts()) == 2 && !h.a.af.open })
	saved := h.env.Svc.Accounts()[1]
	if len(saved.Buckets) != 1 || saved.Buckets[0] != (core.AccountBucket{Name: "data", Prefix: "dir/"}) {
		t.Fatalf("saved buckets = %+v", saved.Buckets)
	}
	// The sidebar shows the named bucket, and it opens in its folder.
	h.waitFor("named bucket listed", func() bool { return len(h.a.buckets) == 1 && h.a.bucketsErr == "" })
	if h.a.buckets[0].Name != "data" || h.a.buckets[0].Prefix != "dir/" {
		t.Fatalf("sidebar buckets = %+v", h.a.buckets)
	}
	h.waitFor("folder opened", func() bool { return h.a.scope.Bucket == "data" && h.a.scope.Prefix == "dir/" && !h.a.loading })
	if h.a.listErr != "" {
		t.Fatalf("listing the named folder: %s", h.a.listErr)
	}
	h.mustHave("a.txt", "sub")
	// The sidebar and the breadcrumb name the folder, and the window does not
	// offer what is above it: the key may not list it.
	h.mustHave("data/dir/", "data/dir")
	if h.a.canGoUp() {
		t.Fatal("Up is offered above the named folder")
	}
	h.runCmd("up")
	if h.a.scope.Prefix != "dir/" {
		t.Fatalf("Up left the named folder for %q", h.a.scope.Prefix)
	}
	// Below it, Up works as usual and comes back to it.
	h.a.openPrefix("dir/sub/")
	h.waitFor("sub folder", func() bool { return !h.a.loading && h.a.listErr == "" && h.a.scope.Prefix == "dir/sub/" })
	h.mustHave("b.txt")
	h.snap("named-bucket-subfolder-light")
	if !h.a.canGoUp() {
		t.Fatal("Up is not offered below the named folder")
	}
	h.runCmd("up")
	h.waitFor("back in the named folder", func() bool { return !h.a.loading && h.a.scope.Prefix == "dir/" })
	if h.a.listErr != "" {
		t.Fatalf("listing after Up: %s", h.a.listErr)
	}
	// Whole-bucket actions cover the named folder, not the bucket root.
	if r := h.a.bucketRoot("data"); r.Prefix != "dir/" {
		t.Fatalf("bucketRoot = %+v", r)
	}

	// Editing shows the named buckets in their text form.
	h.a.openAccountForm(true)
	h.settle()
	if h.a.af.buckets != "data/dir/" {
		t.Fatalf("form buckets = %q", h.a.af.buckets)
	}
	// A bucket named twice is refused before saving.
	h.a.af.buckets = "data/a/, data/b/"
	h.mustClick("Save")
	h.mustHave("named twice")
	if h.env.Svc.Accounts()[1].Buckets[0].Prefix != "dir/" {
		t.Fatal("an invalid form was saved")
	}
}

func TestDeniedBucketListOffersNamingBuckets(t *testing.T) {
	h := newRawHarness(t, "light", false)
	h.restrictKey()
	second, err := h.env.Svc.SaveAccount(core.Account{Name: "Restricted", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL, PathStyle: true,
		Credentials: core.CredStatic, AccessKeyID: "restricted"}, &core.Secrets{SecretAccessKey: "restricted-secret"})
	if err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.a.selectAccount(second.ID)
	h.waitFor("bucket list refused", func() bool { return h.a.bucketsErr != "" && !h.a.bucketsLoad })
	if h.a.bucketsFix != "buckets" {
		t.Fatalf("bucketsFix = %q, want the offer to name the buckets", h.a.bucketsFix)
	}
	h.mustHave("(AccessDenied)", "Retry", "Name buckets…")
	h.snap("buckets-denied-light")
	h.mustClick("Name buckets…")
	if !h.a.af.open || h.a.af.editing != second.ID {
		t.Fatalf("the offer did not open the account for editing: %+v", h.a.af)
	}
	h.mustHave("Edit account", "Buckets")
}
