package main

import (
	"testing"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// Short plain listings show the drop hint under the last row; filters, the
// deleted view and a window with no room for it do not.
func TestDropHintShowsOnlyInShortPlainListings(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// S3 keeps no size for a folder: with a folder among the chosen rows the sum
// covers the objects alone, and its label says so.
func TestSizeSumNamesWhatItCounts(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.tt.ClickWith(ui.Cmd, "manifest.json")
	h.settle()
	h.mustHave(locales.InspectorTotalSize())
	h.mustNotHave(locales.InspectorObjectsSize())
	h.tt.ClickWith(ui.Cmd, "assets")
	h.settle()
	h.mustHave(locales.InspectorObjectsSize())
	h.mustNotHave(locales.InspectorTotalSize())
}
