package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// Views name radii through the constants of theme.go: a numeric literal in a
// .Radius( call outside theme.go is a bare radius (design.md principle 5).
func TestNoBareRadiusOutsideTheme(t *testing.T) {
	bare := regexp.MustCompile(`\.Radius\(\s*[0-9]`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "theme.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if bare.MatchString(line) {
				t.Errorf("%s:%d: bare radius: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Short plain listings show the drop hint under the last row; filters, the
// deleted view and a window with no room for it do not.
func TestDropHintShowsOnlyInShortPlainListings(t *testing.T) {
	h := newHarness(t, "light")
	h.settle()
	hint := locales.ObjectsDropHint()
	if !h.tt.HasText(hint) {
		t.Fatalf("no drop hint under a short list; have %q", h.tt.Texts())
	}
	// It sits below the last row and is centred in the table's width.
	last := h.rectOf(h.a.rows[len(h.a.rows)-1].Name)
	r := h.rectOf(hint)
	if r.Y < last.Y+last.H {
		t.Errorf("hint at y=%.1f is not below the last row (ends %.1f)", r.Y, last.Y+last.H)
	}
	h.snap("drophint-light")

	h.a.search = "man"
	h.a.rebuildRows()
	h.settle()
	if h.tt.HasText(hint) {
		t.Error("the hint shows while a filter is typed")
	}
	h.a.search = ""
	h.a.rebuildRows()
	h.settle()
	if !h.tt.HasText(hint) {
		t.Fatal("the hint did not come back")
	}

	h.resize(960, 400) // no 120px left under 8 rows
	if h.tt.HasText(hint) {
		t.Errorf("the hint shows with %.0fpx of room", h.a.dropHintRoom())
	}
}

// Folders show the same dash in every column they have no value for; Type keeps
// its word.
func TestFolderRowsDashTheirEmptyCells(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = false
	h.settle()
	folders := 0
	for _, e := range h.a.rows {
		if e.Folder {
			folders++
		}
	}
	if folders == 0 {
		t.Skip("no folders in the demo listing")
	}
	if got, want := count(h.tt.Texts(), "–"), 3*folders; got != want {
		t.Errorf("%d dashes for %d folders, want %d (size, modified, storage class)", got, folders, want)
	}
	if !h.tt.HasText(locales.ObjectsColType()) {
		t.Error("the Type column is gone")
	}
}

// Every tool's list header is the same strip: the create action is a labelled
// ghost button, the rest are 28px icon buttons.
func TestToolListHeadersShareOneShape(t *testing.T) {
	h := newHarness(t, "light")
	if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "J", LocalDir: t.TempDir(), Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website"}, Direction: "upload"}); err != nil {
		t.Fatal(err)
	}
	h.openToolView("sync")
	create := h.rectOf(locales.SyncNew())
	del := h.rectOf(locales.CommonDelete())
	h.snap("sync-header-light")
	if create.W <= 28 {
		t.Errorf("the sync create button is %.1f wide: it has no visible label", create.W)
	}
	if math.Abs(float64(del.W-28)) > 0.5 || math.Abs(float64(del.H-28)) > 0.5 {
		t.Errorf("sync delete is %.1fx%.1f, want 28x28", del.W, del.H)
	}
	if math.Abs(float64(create.H-28)) > 0.5 {
		t.Errorf("sync create is %.1f high, want 28", create.H)
	}

	h.a.closeTool()
	h.openToolView("rules")
	h.a.rulesAdd()
	h.settle()
	rcreate := h.rectOf(locales.RulesAddRule())
	rdel := h.rectOf(locales.RulesDeleteRule())
	h.snap("rules-header-light")
	if rcreate.W <= 28 {
		t.Errorf("the rules create button is %.1f wide: it has no visible label", rcreate.W)
	}
	if math.Abs(float64(rdel.W-28)) > 0.5 || math.Abs(float64(rdel.H-28)) > 0.5 {
		t.Errorf("rules delete is %.1fx%.1f, want 28x28", rdel.W, rdel.H)
	}
	if math.Abs(float64(rcreate.H-create.H)) > 0.5 || math.Abs(float64(rcreate.Y-create.Y)) > 0.5 {
		t.Errorf("rules create at y=%.1f h=%.1f, sync create at y=%.1f h=%.1f: the headers differ", rcreate.Y, rcreate.H, create.Y, create.H)
	}
}
