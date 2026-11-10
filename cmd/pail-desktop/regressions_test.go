package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/cloud"
)

// Regression tests of reported bugs, one per bug.

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

// The Permissions tab kept the ACL of the object it last showed when an object
// of the same name was chosen in another bucket.
func TestPermissionsTabFollowsTheBucket(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.PutObject("acme-backups", "README.txt", []byte("public"), "text/plain", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	loc := core.Location{AccountID: h.env.Account.ID, Bucket: "acme-backups"}
	acl, err := h.env.Svc.ACL(ctx, loc, core.ObjectRef{Key: "README.txt"})
	if err != nil {
		t.Fatal(err)
	}
	acl.Grants = append(acl.Grants, core.Grant{Type: "Group", Grantee: groupAllUsers, Permission: "READ"})
	if err := h.env.Svc.SetACL(ctx, loc, core.ObjectRef{Key: "README.txt"}, acl); err != nil {
		t.Fatal(err)
	}
	h.mustClick("README.txt")
	h.a.inspTab = 3
	h.waitFor("acl of acme-assets", func() bool { return h.a.mg.acl.state == loadReady })
	if h.a.mg.acl.canned != "private" {
		t.Fatalf("acme-assets README.txt is %q", h.a.mg.acl.canned)
	}
	h.a.openBucket("acme-backups")
	h.settle()
	h.mustClick("README.txt")
	h.waitFor("acl of acme-backups", func() bool { return h.a.mg.acl.state == loadReady && h.a.mg.acl.canned == "public-read" })
}

// The Versions tab did not show the version a save of the headers had made.
func TestVersionsTabFollowsANewVersion(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.mustClick("notes.txt")
	h.a.inspTab = 4
	h.waitFor("versions", func() bool { return h.a.mg.vers.state == loadReady })
	n := len(h.a.mg.vers.rows)
	h.env.Srv.PutObject(demoVersioned, "notes.txt", []byte("v-next"), "text/plain", nil)
	h.a.headKey = "" // what a save of the headers does
	h.waitFor("one more version", func() bool { return h.a.mg.vers.state == loadReady && len(h.a.mg.vers.rows) == n+1 })
}
// "Show deleted" stayed on after a switch of accounts, for a bucket of the same
// name on the other account.
func TestShowDeletedEndsWithTheAccount(t *testing.T) {
	h := newHarness(t, "light")
	id := h.a.accountID
	h.a.openBucket("acme-analytics")
	h.settle()
	h.a.mg.del = deletedMode{on: true, bucket: "acme-analytics", keys: map[string]bool{}}
	h.a.selectAccount("")
	h.a.selectAccount(id)
	h.waitFor("first bucket", func() bool { return h.a.scope.Bucket == "acme-analytics" })
	if h.a.mg.del.on {
		t.Error("the other account's bucket opened showing deleted items")
	}
}

// An account that lost its secret kept showing its bucket list beside the
// "enter the secret" message.
func TestMissingSecretClearsTheBucketList(t *testing.T) {
	h := newHarness(t, "light")
	if len(h.a.buckets) == 0 {
		t.Fatal("no buckets to begin with")
	}
	for i := range h.a.accounts {
		if h.a.accounts[i].ID == h.a.accountID {
			h.a.accounts[i].SecretMissing = true
		}
	}
	h.a.loadBuckets(false)
	h.settle()
	if h.a.bucketsFix != fixSecret || len(h.a.buckets) != 0 {
		t.Errorf("fix %d, %d buckets still listed", h.a.bucketsFix, len(h.a.buckets))
	}
}
// A name with a space at an end was renamed without the trimmed space when
// the field was left untouched.
func TestUntouchedRenameKeepsSpaces(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.PutObject(bkt, "pad ", []byte("x"), "text/plain", nil)
	h.a.reload()
	h.settle()
	h.a.setSingle("pad ")
	h.a.startRename()
	h.settle()
	h.a.commitRename(true)
	h.settle()
	h.waitFor("idle", func() bool { return h.a.idle() })
	if !h.hasObject(bkt, "pad ") || h.hasObject(bkt, "pad") {
		t.Error(`an untouched rename of "pad " renamed it`)
	}
}

// With the chosen rows hidden by the folder filter, Delete, Copy and the rest
// looked enabled and did nothing.
func TestHiddenSelectionDisablesItsCommands(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.a.search = "zzzz-no-match"
	h.a.rowsDirty = true
	h.settle()
	for _, id := range []string{"delete", "copy", "cut", "download"} {
		if h.a.enabled(h.a.cmd(id)) {
			t.Errorf("%s is enabled with every chosen row filtered out", id)
		}
	}
}
// A signed URL that arrived after its dialog was closed and opened again for
// another object showed in the new dialog.
func TestLateSignedURLStaysOutOfANewDialog(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openSigned([]string{"README.txt"}, "")
	h.a.generateSigned()
	h.a.objOps.sg.open = false
	h.a.openSigned([]string{"manifest.json"}, "")
	h.waitFor("idle", func() bool { return h.a.idle() })
	h.settle()
	f := &h.a.objOps.sg
	if len(f.res) > 0 || f.busy {
		t.Errorf("the manifest.json dialog shows %+v (busy %v)", f.res, f.busy)
	}
}
// After a failed save, Test connection's progress and result never showed,
// and a result stayed on screen after the fields it tested were changed.
func TestAccountTestResultFollowsTheForm(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(true)
	h.settle()
	h.a.af.saveErr = "the keychain refused" // as a failed save leaves it
	h.a.testAccount()
	h.waitFor("test done", func() bool { return h.a.af.test == testOK })
	h.settle()
	h.mustHave("Connected")
	h.a.af.endpoint = "http://elsewhere.invalid:9000"
	h.settle()
	h.mustNotHave("Connected")
}
// Keys with %, ?, #, or * became wrong invalidation paths or were refused.
func TestInvalidationPathsNameTheKeys(t *testing.T) {
	keys := []string{"50%25 off.png", "a?b.txt", "a#b.txt", "x*.txt", "100%.png", "dir/a%b/", "plain.txt"}
	got, err := cloud.NormalizeInvalidationPaths(strings.Split(invalidationPaths(keys), "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, k := range keys {
		want["/"+k] = true
	}
	for _, p := range got {
		folder := strings.HasSuffix(p, "*")
		raw, err := url.PathUnescape(strings.TrimSuffix(p, "*"))
		if err != nil || !want[raw] || folder != strings.HasSuffix(raw, "/") {
			t.Errorf("path %q names %q (%v)", p, raw, err)
		}
		delete(want, raw)
	}
	if len(want) > 0 {
		t.Errorf("no path for %v", want)
	}
}
// A new sync job being written was lost when the user left the tool by the
// sidebar and came back.
func TestSyncDraftSurvivesLeavingTheTool(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("sync")
	h.a.syncNew()
	h.a.tl.sync.f.name = "my unsaved job"
	h.settle()
	h.mustClick("acme-backups")
	h.settle()
	h.openToolView("sync")
	if s := &h.a.tl.sync; s.f.name != "my unsaved job" || !s.edited() {
		t.Errorf("the draft is gone: name %q", s.f.name)
	}
}

// An upload rule being written was lost the same way.
func TestRulesDraftSurvivesLeavingTheTool(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("rules")
	h.a.rulesAdd()
	h.settle()
	h.mustClick("acme-backups")
	h.settle()
	h.openToolView("rules")
	if rs := &h.a.tl.rules; !rs.dirty() || len(rs.draft) == 0 {
		t.Errorf("the draft is gone: %d rules, dirty %v", len(rs.draft), rs.dirty())
	}
}