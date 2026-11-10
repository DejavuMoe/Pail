package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Information density: what the status bar, the sidebar, the transfers strip
// and the toolbar leave out when they have nothing to say (docs/dev/design.md).

func count(texts []string, s string) int {
	n := 0
	for _, t := range texts {
		if t == s {
			n++
		}
	}
	return n
}

// ---- status bar ------------------------------------------------------------------------------

// The left of the status bar is the item count, then the selection; the account
// and the region are in the sidebar and the inspector. Tool views leave it empty.
func TestStatusBarLeftIsTheListingOnly(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = false
	h.tt.Frame()
	items := h.rectOf("8 items")
	if !near32(items.X, 16, 1) { // the sidebar's content edge
		t.Errorf("the item count starts at %.1f: something comes before it (account, region)", items.X)
	}
	// The account name shows once (the switcher) and so does the region.
	if n := count(h.tt.Texts(), h.a.account().Name); n != 1 {
		t.Errorf("%q shows %d times, want 1 (the switcher)", h.a.account().Name, n)
	}
	if n := count(h.tt.Texts(), h.a.account().Region); n != 1 {
		t.Errorf("the account region shows %d times, want 1 (the switcher)", n)
	}
	h.snap("statusbar-plain-light")

	h.mustClick("manifest.json")
	if sel := h.rectOf("1 selected, 303 B"); sel.X <= items.X+items.W {
		t.Errorf("the selection (%.1f) is not after the count (ends %.1f)", sel.X, items.X+items.W)
	}
	if got := count(h.tt.Texts(), "·"); got != 1 {
		t.Errorf("%d separators with a selection, want 1", got)
	}

	h.openToolView("settings")
	if h.tt.HasText("8 items") || h.tt.HasText("1 selected, 303 B") || count(h.tt.Texts(), "·") != 0 {
		t.Errorf("the status bar of a tool view has a left side: %q", h.tt.Texts())
	}
}

// ---- sidebar ---------------------------------------------------------------------------------

func TestRowRegionShowsOnlyAForeignRegion(t *testing.T) {
	for _, c := range []struct{ bucket, account, want string }{
		{"us-west-2", "us-east-1", "us-west-2"},
		{"us-east-1", "us-east-1", ""},
		{"us-west-2", "", ""}, // the account names no region: rows say none
		{"", "us-east-1", ""},
	} {
		if got := rowRegion(c.bucket, c.account); got != c.want {
			t.Errorf("rowRegion(%q, %q) = %q, want %q", c.bucket, c.account, got, c.want)
		}
	}
}

func TestBucketRowsShowARegionOnlyWhenItDiffers(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = false
	h.tt.Frame()
	ac := h.a.account()
	if ac.Region == "" {
		t.Fatal("the demo account has no region")
	}
	h.mustHave("us-west-2", "eu-west-1", "ap-southeast-2")
	// The account's own region shows in the switcher alone: the bucket rows in
	// that region have none.
	if n := count(h.tt.Texts(), ac.Region); n != 1 {
		t.Errorf("%q shows %d times, want 1 (the switcher)", ac.Region, n)
	}
	h.snap("sidebar-regions-light")
	// The tooltip keeps saying it.
	if tip := h.a.bucketTip("acme-assets"); tip == "" {
		t.Error("no tooltip text")
	}

	// An account without a region: no row has one.
	for i := range h.a.accounts {
		h.a.accounts[i].Region = ""
	}
	h.tt.Frame()
	h.mustNotHave("us-west-2", "eu-west-1", "ap-southeast-2")
}

// TOOLS folds to its header; the open tool's row stays under it; the choice is
// kept in settings.json, and a settings file without it unfolds the group.
func TestToolsGroupFoldsAndKeepsTheOpenTool(t *testing.T) {
	h := newHarness(t, "light")
	rows := []string{"Sync jobs", "CloudFront", "Upload rules", "MIME types", "Multipart uploads", "Settings"}
	h.mustHave(append(rows, "TOOLS")...)
	if h.a.collapseTools || h.env.Svc.Settings().CollapseTools {
		t.Fatal("the group starts folded")
	}
	open := h.rectOf("Sync jobs")
	h.snap("sidebar-tools-open-light")

	h.mustClick("TOOLS")
	if !h.a.collapseTools {
		t.Fatal("the header did not fold the group")
	}
	h.mustHave("TOOLS")
	h.mustNotHave(rows...)
	h.waitFor("the choice is saved", func() bool { return h.env.Svc.Settings().CollapseTools })
	h.snap("sidebar-tools-folded-light")
	raw, err := os.ReadFile(filepath.Join(h.env.Dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"collapseTools": true`) && !strings.Contains(string(raw), `"collapseTools":true`) {
		t.Errorf("settings.json lacks collapseTools: %s", raw)
	}

	// The tool that is open keeps its row, with its selected cues.
	h.openToolView("rules")
	h.mustHave("Upload rules")
	h.mustNotHave("Sync jobs", "CloudFront", "MIME types", "Multipart uploads")
	if h.a.navSel != "t:rules" {
		t.Errorf("navSel = %q, want the open tool's row selected", h.a.navSel)
	}
	h.snap("sidebar-tools-folded-open-light")

	// The keyboard: the header is a button, Enter and Space toggle it.
	h.mustClick("TOOLS")
	h.settle()
	if h.a.collapseTools {
		t.Fatal("the second click did not unfold")
	}
	h.mustHave(rows...)
	if r := h.rectOf("Sync jobs"); !near32(r.X, open.X, 0.5) {
		t.Errorf("unfolded rows moved: %v vs %v", r, open)
	}
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if !h.a.collapseTools {
		t.Error("Enter did not fold the group")
	}
	h.tt.Key(0, ui.KeySpace)
	h.settle()
	if h.a.collapseTools {
		t.Error("Space did not unfold the group")
	}
	h.waitFor("unfolding saved", func() bool { return !h.env.Svc.Settings().CollapseTools })
}

func TestCollapseToolsComesBackFromSettingsAndOldFilesUnfold(t *testing.T) {
	h := newHarness(t, "light")
	st := h.env.Svc.Settings()
	st.CollapseTools = true
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	a := newApp(h.env.Svc, options{theme: "light", width: testW, height: testH, demo: true, still: true})
	t.Cleanup(a.close)
	if !a.collapseTools {
		t.Error("a new window ignores the saved fold")
	}
	st.CollapseTools = false
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(h.env.Dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "collapseTools") {
		t.Errorf("an unfolded group writes the field (old readers see a new key): %s", raw)
	}
	b := newApp(h.env.Svc, options{theme: "light", width: testW, height: testH, demo: true, still: true})
	t.Cleanup(b.close)
	if b.collapseTools {
		t.Error("a settings file without the field starts folded")
	}
}

// ---- transfers strip -----------------------------------------------------------------------------

// transfersCounts clicks the status bar's up/down button: the "0" of its two
// counts is the first "0" text in the frame.
func (h *harness) clickTransferCounts() {
	h.t.Helper()
	h.tt.Frame()
	h.playOut()
	r := h.rectOf("0")
	if r.Y < float32(testH-statusBarH) {
		h.t.Fatalf("the counts are at y=%.0f, not in the status bar", r.Y)
	}
	h.tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	h.settle()
}

func TestTransfersStripIsHiddenWithoutTasksAndTheCountsOpenThePanel(t *testing.T) {
	h := newHarness(t, "light")
	if len(h.a.tasks) != 0 || h.a.transfersOpen {
		t.Fatal("not a fresh window")
	}
	h.mustNotHave("Transfers", "0 active")
	// The table reaches the status bar: nothing sits between them.
	if bottom := h.a.tblRect.Y + h.a.tblRect.H; !near32(bottom, float32(testH-statusBarH), 0.5) {
		t.Errorf("the table ends at %.1f, the status bar starts at %d", bottom, testH-statusBarH)
	}

	// The counts are a button that opens the panel with its empty state.
	h.clickTransferCounts()
	if !h.a.transfersOpen {
		t.Fatal("the status bar's counts did not open the panel")
	}
	h.mustHave("Transfers", "Active 0", "Completed 0", "Failed 0")
	h.snap("transfers-opened-empty-light")
	// And close it, which leaves no strip behind.
	h.clickTransferCounts()
	if h.a.transfersOpen {
		t.Fatal("the counts did not close the panel")
	}
	h.mustNotHave("Transfers", "0 active")

	// Ctrl+J and the palette command open it as well.
	time.Sleep(150 * time.Millisecond) // past the double-delivery guard of commands
	h.runCmd("transfers")
	h.settle()
	if !h.a.transfersOpen {
		t.Error("the transfers command did not open the panel")
	}
	h.mustHave("Active 0")
	h.runCmd("transfers")
	h.settle()
	h.mustNotHave("Transfers")

	// A task brings the strip (collapsed by hand it stays), the last one takes
	// it away.
	h.a.setTasks([]Task{{ID: "x", Kind: core.KindUpload, Title: "Upload x", Status: core.StatusSucceeded, Finished: time.Now()}})
	h.settle()
	h.a.transfersOpen = false
	h.settle()
	h.mustHave("Transfers", "1 completed")
	if bottom := h.a.tblRect.Y + h.a.tblRect.H; !near32(bottom, float32(testH-statusBarH-collapsedBar), 0.5) {
		t.Errorf("with the strip the table ends at %.1f, want %d", bottom, testH-statusBarH-collapsedBar)
	}
	h.snap("transfers-strip-light")
	h.a.setTasks(nil)
	h.settle()
	h.mustNotHave("Transfers", "0 active")
}

// The first task brings the panel in the frame it arrives (two: the Split is
// new, and its boxes are known one frame on), with nothing left to animate.
func TestTransfersPanelComesWithTheFirstTaskAtOnce(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.rest()
	if h.tt.HasText("Transfers") {
		t.Fatal("the strip shows with no task")
	}
	tableBottom := func() float32 { return h.a.tblRect.Y + h.a.tblRect.H }
	rest := tableBottom()
	h.a.setTasks([]Task{{ID: "x", Kind: core.KindUpload, Title: "Upload x", Status: core.StatusRunning, BytesTotal: 100, BytesDone: 10, Created: time.Now(), Started: time.Now()}})
	h.tt.Frame()
	h.tt.Frame()
	first := tableBottom()
	h.rest()
	if end := tableBottom(); first != end || end >= rest {
		t.Errorf("the table's bottom goes %.1f, %.1f, then %.1f: the panel did not come at once", rest, first, end)
	}
	if !h.a.transfersOpen {
		t.Error("the first task did not open the panel")
	}
}

// ---- snapshots -------------------------------------------------------------------------------------

// TestDensitySnapshots writes the states of this batch at both sizes in both
// themes when PAIL_UI_SNAPSHOT_DIR is set (it checks nothing otherwise).
func TestDensitySnapshots(t *testing.T) {
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("PAIL_UI_SNAPSHOT_DIR is not set")
	}
	for _, sz := range auditSizes {
		h := newHarness(t, "light")
		h.resize(sz.w, sz.h)
		h.snapAudit(sz, "density-1-nothing-selected")
		h.mustClick("manifest.json")
		h.a.run("copy", "test")
		h.settle()
		h.snapAudit(sz, "density-2-selection-and-clipboard")
		h.a.selectAll()
		h.settle()
		h.snapAudit(sz, "density-3-multi-selection")
		h.a.clearSelection()
		h.a.mg.clip = clipData{}
		h.a.setTasks([]Task{
			{ID: "t1", Kind: core.KindUpload, Title: "Upload report.pdf", Detail: "-> s3://acme-assets/", Status: core.StatusRunning, BytesTotal: 200 << 20, BytesDone: 80 << 20, Speed: 20 << 20, Created: time.Now(), Started: time.Now()},
			{ID: "t2", Kind: core.KindDownload, Title: "Download backup.tar.gz", Status: core.StatusSucceeded, BytesTotal: 4 << 10, BytesDone: 4 << 10, Created: time.Now(), Finished: time.Now()},
		})
		h.settle()
		h.snapAudit(sz, "density-4-transfers-open")
		h.a.transfersOpen = false
		h.settle()
		h.snapAudit(sz, "density-5-transfers-strip")
		h.a.setTasks(nil)
		h.a.setCollapseTools(true)
		h.settle()
		h.snapAudit(sz, "density-6-tools-folded")
		h.openToolView("sync")
		h.snapAudit(sz, "density-7-tools-folded-tool-open")
		h.a.setCollapseTools(false)
		h.openToolView("settings")
		h.snapAudit(sz, "density-8-tool-view-expanded")
		h.waitFor("settings saved", func() bool { return !h.env.Svc.Settings().CollapseTools })
	}
}

// ---- toolbar ---------------------------------------------------------------------------------------

// The transfers toggle and "⋯" keep their place in every view; the inspector
// toggle, only the browser has, comes before them.
func TestRightToolbarGroupKeepsItsPlace(t *testing.T) {
	h := newHarness(t, "light")
	transfers, more, inspector := h.a.cmd("transfers").text(), "More", h.a.cmd("inspector").text()
	bx, bm, bi := h.rectOf(transfers), h.rectOf(more), h.rectOf(inspector)
	if !(bi.X < bx.X && bx.X < bm.X) {
		t.Errorf("browser order: inspector %.1f, transfers %.1f, more %.1f", bi.X, bx.X, bm.X)
	}
	for _, id := range []string{"sync", "settings"} {
		h.openToolView(id)
		if h.tt.HasText(inspector) {
			t.Errorf("tool %s shows the inspector toggle", id)
		}
		tx, tm := h.rectOf(transfers), h.rectOf(more)
		if tx.X != bx.X || tx.Y != bx.Y || tm.X != bm.X {
			t.Errorf("tool %s: transfers toggle at %v, more at %v; the browser has %v and %v", id, tx, tm, bx, bm)
		}
	}
	h.a.run("tool.sync", "test") // back out of the tool
	h.mustClick("Back")
	h.settle()
	if r := h.rectOf(transfers); r.X != bx.X {
		t.Errorf("back in the browser the toggle moved to %.1f from %.1f", r.X, bx.X)
	}
}

func toolbarX(t *testing.T, h *harness, ids ...string) []float32 {
	t.Helper()
	var xs []float32
	for _, id := range ids {
		xs = append(xs, h.rectOf(h.a.cmd(id).text()).X)
	}
	return xs
}

var selectionOnly = []string{"download", "copy", "cut", "rename", "delete"}

func hasToolbarButton(h *harness, id string) bool {
	return h.tt.HasText(h.a.cmd(id).text())
}

// The buttons that work on a selection are there only with one; Paste only with
// something on the clipboard; the order is Upload, New folder | sizes, Refresh |
// Download, Copy, Cut, Paste, Rename, Delete.
func TestToolbarShowsSelectionButtonsOnlyWithASelection(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = false
	h.tt.Frame()
	always := []string{"newfolder", "foldersizes", "refresh"}
	for _, id := range always {
		if !hasToolbarButton(h, id) {
			t.Errorf("%s is missing with no selection", id)
		}
	}
	h.mustHave("Upload")
	for _, id := range append(selectionOnly, "paste") {
		if hasToolbarButton(h, id) {
			t.Errorf("%s shows with nothing selected and nothing copied", id)
		}
	}
	// Nothing after Refresh: no group, so no separator either.
	refresh := h.rectOf(h.a.cmd("refresh").text())
	h.snap("toolbar-no-selection-light")

	// One file: the selection group, no Paste.
	h.mustClick("manifest.json")
	order := []string{"newfolder", "foldersizes", "refresh", "download", "copy", "cut", "rename", "delete"}
	for _, id := range order {
		if !hasToolbarButton(h, id) {
			t.Fatalf("%s is missing with a selection", id)
		}
	}
	if hasToolbarButton(h, "paste") {
		t.Error("Paste shows with an empty clipboard")
	}
	xs := toolbarX(t, h, order...)
	if !slices.IsSorted(xs) {
		t.Errorf("toolbar order %v at x=%v", order, xs)
	}
	if up := h.rectOf("Upload"); up.X >= xs[0] {
		t.Errorf("Upload (%.1f) is not first", up.X)
	}
	// The separator makes the gap before the group wider than between buttons.
	dl := h.rectOf(h.a.cmd("download").text())
	if gap := dl.X - (refresh.X + refresh.W); gap < 12 {
		t.Errorf("the group starts %.1f after Refresh: no separator", gap)
	}
	h.snap("toolbar-selection-light")

	// Copy puts Paste between Cut and Rename.
	h.a.run("copy", "test")
	h.settle()
	order = []string{"download", "copy", "cut", "paste", "rename", "delete"}
	if xs := toolbarX(t, h, order...); !slices.IsSorted(xs) {
		t.Errorf("with a clipboard the order %v is at x=%v", order, xs)
	}

	// Two files: Rename stays, dimmed.
	h.a.selectAll()
	h.settle()
	if !hasToolbarButton(h, "rename") || h.a.enabled(h.a.cmd("rename")) {
		t.Error("Rename should show disabled with two or more selected")
	}

	// The clipboard alone: Paste after a separator, nothing else of the group.
	h.a.clearSelection()
	h.settle()
	if !hasToolbarButton(h, "paste") {
		t.Fatal("Paste is gone with something on the clipboard")
	}
	for _, id := range selectionOnly {
		if hasToolbarButton(h, id) {
			t.Errorf("%s shows with no selection", id)
		}
	}
	if p := h.rectOf(h.a.cmd("paste").text()); p.X-(refresh.X+refresh.W) < 12 {
		t.Errorf("Paste is %.1f after Refresh: no separator", p.X-(refresh.X+refresh.W))
	}
	h.snap("toolbar-paste-only-light")
	h.a.mg.clip = clipData{}
	h.settle()
	if hasToolbarButton(h, "paste") {
		t.Error("Paste stays after the clipboard emptied")
	}
}

// With every button showing, in the language with the longest labels, the
// toolbar still holds them while it shows labels, down to the width where it
// drops them; and the smallest window shows icons only.
func TestToolbarFitsAtItsLabelBreakpointWithEveryButton(t *testing.T) {
	for _, loc := range []string{"en", "zh-Hans", "en-XA"} {
		t.Run(loc, func(t *testing.T) {
			h := newHarness(t, "light")
			h.setLocale(loc)
			h.a.inspectorOpen = true
			h.a.selectAll()
			h.a.run("copy", "test") // Paste shows too
			h.settle()
			del, inspector := h.a.cmd("delete").text(), h.a.cmd("inspector").text()
			h.resize(1440, 900)
			var narrowest int
			var spare float32
			for w := 1440; w >= 960; w -= 2 {
				h.resize(w, 700)
				if h.a.lay.tbCompact {
					break
				}
				narrowest = w
				spare = h.rectOf(inspector).X - (h.rectOf(del).X + h.rectOf(del).W)
				d, i := h.rectOf(del), h.rectOf(inspector)
				if d.X+d.W > i.X {
					t.Fatalf("%s at %d wide (bar %.0f): Delete ends at %.1f, past the inspector toggle at %.1f", loc, w, h.a.lay.barW, d.X+d.W, i.X)
				}
			}
			if narrowest == 0 {
				t.Fatal("the toolbar never showed labels")
			}
			t.Logf("%s: labels down to a %d wide window, %.1f spare between Delete and the right group", loc, narrowest, spare)
			h.resize(960, 600)
			if !h.a.lay.tbCompact {
				t.Error("the smallest window shows toolbar labels")
			}
			if up := h.rectOf(locales.ToolbarUpload()); up.W > 28 {
				t.Errorf("the Upload button is %.1f wide at 960x600: it shows its label", up.W)
			}
			h.snap("toolbar-all-960-" + loc)
		})
	}
}
