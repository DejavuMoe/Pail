package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

func (h *harness) aclOf(bucket, key string) core.ACL {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acl, err := h.env.Svc.ACL(ctx, core.Location{AccountID: h.env.Account.ID, Bucket: bucket}, core.ObjectRef{Key: key})
	if err != nil {
		h.t.Fatalf("acl %s/%s: %v", bucket, key, err)
	}
	return acl
}

func hasGroupGrant(acl core.ACL, group, perm string) bool {
	for _, g := range acl.Grants {
		if g.Type == "Group" && g.Grantee == group && g.Permission == perm {
			return true
		}
	}
	return false
}

func (h *harness) hasObject(bucket, key string) bool {
	_, ok := h.env.Srv.Object(bucket, key)
	return ok
}

func (h *harness) openVersioned() {
	h.t.Helper()
	h.a.openBucket(demoVersioned)
	h.settle()
}

// ---- permissions ------------------------------------------------------------------------------

func TestCannedACLPublicNeedsConfirmation(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Permissions")
	h.waitFor("acl loaded", func() bool { return h.a.mg.acl.state == 2 })
	h.mustHave("Owner", "Full control", "Private", "Canned ACL")
	h.snap("permissions-light")

	// Public read stops at the confirmation, which says what happens.
	h.mustClick("Private")
	h.mustClick("Public read")
	h.mustHave("Make object public?", "publicly readable", "manifest.json", demoAccountName, bkt)
	h.snap("permissions-public-confirm-light")
	if hasGroupGrant(h.aclOf(bkt, "manifest.json"), groupAllUsers, "READ") {
		t.Fatal("made public without confirming")
	}
	if h.a.mg.acl.canned != "private" {
		t.Errorf("the select shows %q while waiting for the answer", h.a.mg.acl.canned)
	}
	h.mustClick("Cancel")
	if hasGroupGrant(h.aclOf(bkt, "manifest.json"), groupAllUsers, "READ") {
		t.Fatal("Cancel made the object public")
	}
	h.mustClick("Private")
	h.mustClick("Public read")
	h.mustClick("Make public")
	h.waitToast("Permissions updated")
	if !hasGroupGrant(h.aclOf(bkt, "manifest.json"), groupAllUsers, "READ") {
		t.Fatalf("the object is not public: %+v", h.aclOf(bkt, "manifest.json"))
	}
	h.waitFor("grants refreshed", func() bool { return h.tt.HasText("Everyone (public)") })
	h.mustHave("Public read")

	// A private ACL needs no confirmation.
	h.mustClick("Public read")
	h.mustClick("Private")
	h.waitFor("private again", func() bool { return !hasGroupGrant(h.aclOf(bkt, "manifest.json"), groupAllUsers, "READ") })
}

func TestACLUnsupportedShowsEmptyState(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("acl")
	h.mustClick("manifest.json")
	h.mustClick("Permissions")
	h.waitFor("acl answered", func() bool { return h.a.mg.acl.state == 3 })
	h.mustHave("ACLs are disabled for this bucket")
	h.mustNotHave("Canned ACL")
	h.snap("permissions-disabled-light")
}

// ---- versions ---------------------------------------------------------------------------------

func TestVersionsTabListsAndRestores(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.mustClick("notes.txt")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	vs := h.env.Srv.Versions(demoVersioned, "notes.txt")
	if len(h.a.mg.vers.rows) != 3 || len(vs) != 3 {
		t.Fatalf("rows %d, server %d", len(h.a.mg.vers.rows), len(vs))
	}
	if !h.a.mg.vers.rows[0].Latest || h.a.mg.vers.rows[0].VersionID != vs[0].VersionID {
		t.Fatalf("the latest version is not first: %+v", h.a.mg.vers.rows)
	}
	h.mustHave("Latest", shortVersion(vs[1].VersionID), shortVersion(vs[2].VersionID))
	h.snap("versions-tab-light")

	// Restore the second one: it becomes a new current version, history stays.
	h.mustClick("Restore this version")
	h.waitToast("Restored version " + shortVersion(vs[1].VersionID))
	now, _ := h.env.Srv.Object(demoVersioned, "notes.txt")
	if string(now) != "Draft notes\n- ship the beta\n" {
		t.Errorf("current body after restore = %q", now)
	}
	if n := len(h.env.Srv.Versions(demoVersioned, "notes.txt")); n != 4 {
		t.Errorf("%d versions after restore, want 4 (history is kept)", n)
	}
	h.waitFor("list refreshed", func() bool { return h.a.mg.vers.state == 2 && len(h.a.mg.vers.rows) == 4 })
}

func TestVersionsDeletePermanentlyAsksFirst(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.mustClick("config")
	h.tt.Key(0, ui.KeyEnter) // open the folder
	h.settle()
	h.mustClick("app.yaml")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	vs := h.env.Srv.Versions(demoVersioned, "config/app.yaml")
	if len(vs) != 2 {
		t.Fatalf("seed has %d versions of app.yaml", len(vs))
	}
	h.mustClick("Delete this version permanently")
	h.mustHave("Delete version permanently?", "This cannot be undone.", "config/app.yaml", shortVersion(vs[0].VersionID))
	h.snap("versions-delete-confirm-light")
	h.mustClick("Cancel")
	if n := len(h.env.Srv.Versions(demoVersioned, "config/app.yaml")); n != 2 {
		t.Fatalf("cancel deleted a version (%d left)", n)
	}
	h.mustClick("Delete this version permanently")
	h.mustClick("Delete version")
	h.waitToast("Deleted 1 version")
	left := h.env.Srv.Versions(demoVersioned, "config/app.yaml")
	if len(left) != 1 || left[0].VersionID != vs[1].VersionID {
		t.Fatalf("versions left: %+v", left)
	}
}

func TestVersionsMultiSelectDeleteAndDownload(t *testing.T) {
	h := newRawHarness(t, "light", true)
	dest := t.TempDir()
	st := h.env.Svc.Settings()
	st.DefaultDownloadDir = dest
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	h.openVersioned()
	h.mustClick("notes.txt")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	vs := h.env.Srv.Versions(demoVersioned, "notes.txt")
	h.mustClick(shortVersion(vs[1].VersionID))
	h.mustClick(shortVersion(vs[2].VersionID))
	h.mustHave("2 versions selected")

	h.mustClick("Download chosen versions")
	h.waitFor("both versions on disk", func() bool {
		m, _ := filepath.Glob(filepath.Join(dest, "notes*"))
		return len(m) == 2
	})
	m, _ := filepath.Glob(filepath.Join(dest, "notes*"))
	for _, f := range m {
		if !strings.Contains(filepath.Base(f), "(v") {
			t.Errorf("downloaded name %q carries no version suffix", f)
		}
	}
	oldest, err := os.ReadFile(filepath.Join(dest, "notes (v"+shortVersion(vs[2].VersionID)+").txt"))
	if err != nil || string(oldest) != "Draft notes\n" {
		t.Errorf("oldest version file: %q %v", oldest, err)
	}

	h.mustClick("Delete chosen versions permanently")
	h.mustHave("Delete versions permanently?", "2 versions")
	h.mustClick("Delete versions")
	h.waitToast("Deleted 2 versions")
	left := h.env.Srv.Versions(demoVersioned, "notes.txt")
	if len(left) != 1 || left[0].VersionID != vs[0].VersionID {
		t.Fatalf("versions left: %+v", left)
	}
}

func TestVersionsTabShowsDeleteMarkers(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.mustClick("report.pdf")
	// Someone deletes the key while it is open: its newest entry is a marker.
	h.env.Srv.DeleteObject(demoVersioned, "report.pdf")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	rows := h.a.mg.vers.rows
	if len(rows) != 2 || !rows[0].DeleteMarker || !rows[0].Latest || rows[1].DeleteMarker {
		t.Fatalf("rows = %+v", rows)
	}
	h.mustHave("Delete marker", "Latest", "11.7 KB")
	h.snap("versions-delete-marker-light")
	// A marker cannot be downloaded or restored, only removed.
	h.mustClick(shortVersion(rows[0].VersionID))
	h.mustNotHave("Download chosen versions")
	h.mustHave("1 version selected")
}

func TestVersionsTabFailureHasRetry(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.env.Srv.SetUnsupported("versions")
	h.mustClick("report.pdf")
	h.mustClick("Versions")
	h.waitFor("versions failed", func() bool { return h.a.mg.vers.state == 3 })
	h.mustHave("Not supported by this provider", "Retry")
	h.env.Srv.ClearUnsupported("")
	h.mustClick("Retry")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	h.mustHave("Latest")
}

// ---- deleted items ---------------------------------------------------------------------------

func TestShowDeletedAndRestore(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.waitFor("versioning known", func() bool { return h.a.versioned(h.a.accountID, demoVersioned) })
	h.mustHave("notes.txt", "report.pdf")
	h.mustNotHave("old-plan.txt")

	h.mustClick("Show deleted")
	h.waitFor("deleted listing", func() bool { return h.has("old-plan.txt") && !h.has("report.pdf") })
	h.mustHave("old-plan.txt", "Deleted", "config")
	if h.has("notes.txt") {
		t.Fatal("a live object is listed as deleted")
	}
	h.mustClick("old-plan.txt")
	h.mustHave("Restore") // the inspector's button
	h.snap("show-deleted-light")

	if err := h.tt.RightClick("old-plan.txt"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if menu := h.tt.Menu(); !contains(menu, "Restore") || !contains(menu, "Restore all in this folder") {
		t.Fatalf("context menu %q has no restore entries", menu)
	}
	if err := h.tt.ChooseMenuItem("Restore"); err != nil {
		t.Fatal(err)
	}
	h.waitToast("Restored 1 item")
	body, ok := h.env.Srv.Object(demoVersioned, "old-plan.txt")
	if !ok || string(body) != "The plan we dropped.\n" {
		t.Fatalf("old-plan.txt after restore: %q %v", body, ok)
	}
	h.waitFor("nothing deleted left", func() bool { return !h.has("old-plan.txt") })
	h.mustNotHave("Deleted")

	// Back to the normal listing.
	h.mustClick("Show deleted")
	h.waitFor("live listing", func() bool { return h.has("old-plan.txt") && h.has("report.pdf") })
	h.mustNotHave("Deleted")
}

func TestRestoreAllDeletedInFolder(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.env.Srv.DeleteObject(demoVersioned, "report.pdf")
	h.env.Srv.DeleteObject(demoVersioned, "notes.txt")
	h.openVersioned()
	h.waitFor("versioning known", func() bool { return h.a.versioned(h.a.accountID, demoVersioned) })
	h.mustClick("Show deleted")
	h.waitFor("three deleted", func() bool { return h.has("old-plan.txt") && h.has("report.pdf") && h.has("notes.txt") })
	h.a.run("restoreall", "test")
	h.settle()
	h.mustHave("Restore deleted items?", demoVersioned)
	h.mustClick("Restore")
	h.waitToast("Restored 3 items")
	for _, k := range []string{"old-plan.txt", "report.pdf", "notes.txt"} {
		if !h.hasObject(demoVersioned, k) {
			t.Errorf("%s was not restored", k)
		}
	}
}

func TestShowDeletedDisabledWithoutVersioning(t *testing.T) {
	h := newHarness(t, "light")
	h.waitFor("versioning known", func() bool { _, ok := h.a.versioningOf(h.a.accountID, bkt); return ok })
	h.tt.Click("Show deleted")
	h.settle()
	if h.a.mg.del.on {
		t.Fatal("the toggle worked on a bucket without versioning")
	}
}

// ---- clipboard ---------------------------------------------------------------------------------

func TestCopyAndPasteIntoAnotherFolder(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.Key(ui.Cmd, ui.KeyC)
	h.settle()
	if !h.a.enabled(h.a.cmd("paste")) {
		t.Fatal("Ctrl+C did not enable paste")
	}
	h.a.openPrefix("website/")
	h.settle()
	h.tt.Key(ui.Cmd, ui.KeyV)
	h.settle()
	h.waitToast("Copied 1 item")
	if !h.hasObject(bkt, "website/README.txt") || !h.hasObject(bkt, "README.txt") {
		t.Fatal("copy must leave the source and create the copy")
	}
	h.waitFor("pasted row listed", func() bool { return h.has("website/README.txt") })
	// The clipboard survives a copy: paste again into another folder.
	if !h.a.mg.clip.has() {
		t.Error("a copy emptied the clipboard")
	}
}

func TestCutAndPasteMovesObjectsAndFolders(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.tt.Key(ui.Cmd, ui.KeyX)
	h.settle()
	h.snap("cut-row-light")
	h.a.openPrefix("reports/")
	h.settle()
	h.tt.Key(ui.Cmd, ui.KeyV)
	h.settle()
	h.waitToast("Moved 1 item")
	if h.hasObject(bkt, "manifest.json") || !h.hasObject(bkt, "reports/manifest.json") {
		t.Fatal("cut and paste must move the object")
	}
	if h.a.mg.clip.has() || h.a.enabled(h.a.cmd("paste")) {
		t.Error("a cut is pasted once")
	}

	// A folder moves with everything below it.
	h.a.openPrefix("assets/")
	h.settle()
	h.mustClick("fonts")
	h.tt.Key(ui.Cmd, ui.KeyX)
	h.a.openPrefix("backups/")
	h.settle()
	h.tt.Key(ui.Cmd, ui.KeyV)
	h.settle()
	h.waitToast("Moved 3 items")
	if !h.hasObject(bkt, "backups/fonts/Inter-Regular.woff2") || h.hasObject(bkt, "assets/fonts/Inter-Regular.woff2") {
		t.Fatal("the folder was not moved")
	}
}

func TestPasteAcrossAccountsIsRefused(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.a.run("copy", "test")
	h.a.mg.clip.loc.AccountID = "another-account"
	h.a.openPrefix("website/")
	h.settle()
	h.a.run("paste", "test")
	h.settle()
	h.waitToast("Copying between accounts is not supported yet")
	if h.hasObject(bkt, "website/README.txt") {
		t.Fatal("pasted across accounts")
	}
}

func TestPasteConflictIsReported(t *testing.T) {
	h := newHarness(t, "light")
	st := h.env.Svc.Settings()
	st.Conflict = core.ConflictSkip
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	h.mustClick("README.txt")
	h.a.run("copy", "test")
	h.a.openPrefix("website/")
	h.settle()
	h.env.Srv.PutObject(bkt, "website/README.txt", []byte("already here"), "text/plain", nil)
	h.a.reload()
	h.settle()
	h.a.run("paste", "test")
	h.waitToast("Skipped 1 item that already exist")
	if b, _ := h.env.Srv.Object(bkt, "website/README.txt"); string(b) != "already here" {
		t.Errorf("the existing object was overwritten: %q", b)
	}
}

// drag moves the pointer from the middle of one text to the middle of another in
// a few steps, as a hand does.
func (h *harness) drag(from, to string) {
	h.t.Helper()
	a, ok := h.tt.Find(from)
	if !ok {
		h.t.Fatalf("no %q on screen", from)
	}
	b, ok := h.tt.Find(to)
	if !ok {
		h.t.Fatalf("no %q on screen", to)
	}
	// Find reports the whole row; aim at its name, a little in from the left.
	at := func(r ui.Rect) (float32, float32) {
		if r.W > 300 {
			return r.X + 60, r.Y + r.H/2
		}
		return r.X + r.W/2, r.Y + r.H/2
	}
	x0, y0 := at(a)
	x1, y1 := at(b)
	h.tt.Move(x0, y0)
	h.tt.Press(x0, y0)
	for i := 1; i <= 6; i++ {
		f := float32(i) / 6
		h.tt.Move(x0+(x1-x0)*f, y0+(y1-y0)*f)
		h.tt.Frame()
	}
	h.tt.Release(x1, y1)
	h.settle()
}

func TestDragRowOntoFolderMoves(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.drag("README.txt", "assets")
	h.mustHave("Move 1 item?", "README.txt", "assets/", demoAccountName)
	h.snap("drag-move-confirm-light")
	if !h.hasObject(bkt, "README.txt") {
		t.Fatal("moved before the confirmation")
	}
	h.mustClick("Move")
	h.waitToast("Moved 1 item")
	if h.hasObject(bkt, "README.txt") || !h.hasObject(bkt, "assets/README.txt") {
		t.Fatal("the drop did not move the object")
	}
}

func TestDragRowOntoFileDoesNothing(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.drag("README.txt", "manifest.json")
	if h.a.confirm.open {
		t.Fatal("dropping on a file asked to move")
	}
	if !h.hasObject(bkt, "README.txt") {
		t.Fatal("the object vanished")
	}
}

func TestDragRowOntoBreadcrumbMovesUp(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openPrefix("assets/images/")
	h.settle()
	h.mustClick("hero.png")
	// Breadcrumb: acme-assets > assets > images; drop on "assets".
	h.drag("hero.png", "assets")
	h.mustHave("Move 1 item?", "assets/images/", "assets/")
	h.mustClick("Move")
	h.waitToast("Moved 1 item")
	if !h.hasObject(bkt, "assets/hero.png") || h.hasObject(bkt, "assets/images/hero.png") {
		t.Fatal("the breadcrumb drop did not move the object")
	}
}

// ---- rename ------------------------------------------------------------------------------------

func TestRenameObjectInline(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.Key(0, ui.KeyF2)
	h.settle()
	if h.a.mg.ren.key != "README.txt" {
		t.Fatalf("F2 did not start renaming: %+v", h.a.mg.ren)
	}
	h.snap("rename-light")
	// Bad names keep the field open and say why.
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.typeAndEnter("a/b.txt")
	h.waitToast("Names cannot contain /")
	if h.a.mg.ren.key == "" {
		t.Fatal("an invalid name closed the field")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.typeAndEnter("manifest.json") // exists
	h.waitToast("A file with this name exists")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.typeAndEnter("READ-ME.txt")
	h.waitFor("renamed in the listing", func() bool { return h.has("READ-ME.txt") && !h.has("README.txt") })
	if !h.hasObject(bkt, "READ-ME.txt") || h.hasObject(bkt, "README.txt") {
		t.Fatal("the server was not renamed")
	}
	if !h.a.sel.Has("READ-ME.txt") {
		t.Error("the renamed row is not selected")
	}
	if h.a.mg.ren.key != "" {
		t.Error("the field is still open")
	}
}

func TestRenameUnchangedAndEscapeCloseQuietly(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.a.run("rename", "test")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if h.a.mg.ren.key != "" || len(h.a.tasks) != 0 {
		t.Fatalf("unchanged name: ren=%+v tasks=%d", h.a.mg.ren, len(h.a.tasks))
	}
	h.a.run("rename", "test")
	h.settle()
	h.tt.Type("zzz")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.mg.ren.key != "" || !h.hasObject(bkt, "README.txt") {
		t.Fatal("Escape did not cancel")
	}
}

func TestRenameFolderMovesEverythingBelow(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("reports")
	h.tt.Key(0, ui.KeyF2)
	h.settle()
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.typeAndEnter("reports-2026")
	h.waitFor("folder renamed", func() bool { return h.has("reports-2026/") && !h.has("reports/") })
	for _, k := range []string{"reports-2026/customers-export.csv", "reports-2026/exports/2026-10-17.csv"} {
		if !h.hasObject(bkt, k) {
			t.Errorf("%s missing after the folder rename", k)
		}
	}
	if h.hasObject(bkt, "reports/customers-export.csv") {
		t.Error("old keys are still there")
	}
	if !h.a.sel.Has("reports-2026/") {
		t.Error("the renamed folder is not selected")
	}
}
