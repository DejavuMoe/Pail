package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// chosenKeys is the choice of the object table, sorted.
func (h *harness) chosenKeys() []string {
	keys := h.a.selectedKeys()
	slices.Sort(keys)
	return keys
}

func (h *harness) expectChosen(want ...string) {
	h.t.Helper()
	slices.Sort(want)
	if got := h.chosenKeys(); !slices.Equal(got, want) {
		h.t.Fatalf("chosen = %q, want %q", got, want)
	}
}

func TestSelectAllState(t *testing.T) {
	for _, tc := range []struct {
		chosen, n int
		want      checkState
	}{
		{0, 0, checkOff}, {0, 5, checkOff}, {1, 5, checkMixed}, {4, 5, checkMixed}, {5, 5, checkOn},
	} {
		if got := selectAllState(tc.chosen, tc.n); got != tc.want {
			t.Errorf("selectAllState(%d, %d) = %d, want %d", tc.chosen, tc.n, got, tc.want)
		}
	}
}

// A row's box adds the row to the choice or takes it out, leaving the others;
// a click elsewhere on a row still chooses it alone.
func TestRowCheckBoxTogglesOneRow(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.mustClick("Select manifest.json")
	h.expectChosen("README.txt", "manifest.json")
	h.mustClick("Select tmp")
	h.expectChosen("tmp/", "README.txt", "manifest.json")
	h.mustClick("Select README.txt")
	h.expectChosen("tmp/", "manifest.json")
	h.mustClick("README.txt")
	h.expectChosen("README.txt")
	// The press on the box leaves the keyboard with the table.
	h.mustClick("Select manifest.json")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.settle()
	if got, want := len(h.chosenKeys()), len(h.a.rows); got != want {
		t.Fatalf("Ctrl+A after a box click chose %d of %d rows", got, want)
	}
}

// Shift-clicking a box chooses the rows from the one last clicked to it.
func TestShiftClickOnABoxExtends(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("assets")
	if err := h.tt.ClickWith(ui.Shift, "Select logs"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.expectChosen("assets/", "backups/", "logs/")
}

// The header box chooses every row shown, or none once all are; the rows the
// filter hides are not chosen.
func TestSelectAllBox(t *testing.T) {
	h := newHarness(t, "light")
	n := len(h.a.rows)
	h.mustClick("Select all")
	if got := len(h.chosenKeys()); got != n {
		t.Fatalf("Select all chose %d of %d rows", got, n)
	}
	if st := selectAllState(h.a.selectedCount(), n); st != checkOn {
		t.Fatalf("header state = %d after Select all", st)
	}
	h.mustClick("Select all")
	h.expectChosen()

	// Some chosen: the header is mixed, and a click chooses all.
	h.mustClick("README.txt")
	if st := selectAllState(h.a.selectedCount(), n); st != checkMixed {
		t.Fatalf("header state = %d with one row chosen", st)
	}
	h.mustClick("Select all")
	if got := len(h.chosenKeys()); got != n {
		t.Fatalf("Select all from mixed chose %d of %d rows", got, n)
	}

	// Filtered: only the rows shown are chosen.
	h.mustClick("Select all")
	h.a.search = "json"
	h.a.rebuildRows()
	h.settle()
	h.mustClick("Select all")
	h.expectChosen("manifest.json")
	h.a.search = ""
	h.a.rebuildRows()
	h.settle()
	h.expectChosen("manifest.json")
}

// Esc in the table clears the choice, but not while a row is renamed.
func TestEscClearsTheChoice(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.mustClick("Select manifest.json")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	h.expectChosen()

	h.mustClick("README.txt")
	h.tt.Key(0, ui.KeyF2)
	h.settle()
	if h.a.mg.ren.key == "" {
		t.Fatal("F2 did not start renaming")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.mg.ren.key != "" {
		t.Fatal("Esc did not end renaming")
	}
	h.expectChosen("README.txt")
}

// The selection bar shows for two or more rows: their count, the size of the
// objects and the folders among them; Clear selection folds it away.
func TestSelectionBar(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.mustNotHave("1 item selected")
	if _, ok := h.tt.Find("Selection"); ok {
		t.Fatal("the selection bar shows for one row")
	}
	h.mustClick("Select manifest.json")
	h.mustHave("2 items selected")
	h.mustNotHave("includes")
	_, _, bytes := h.a.selectedInfo()
	h.mustHave(humanBytes(bytes))
	h.mustClick("Select tmp")
	h.mustHave("3 items selected", "includes 1 folder")
	// The toolbar has buttons of the same names; these two are the bar's own.
	for _, s := range []string{"Object actions", "Clear selection"} {
		if _, ok := h.tt.Find(s); !ok {
			t.Errorf("the selection bar has no %q", s)
		}
	}
	h.snapBoth("selection-bar")
	h.mustClick("Clear selection")
	h.expectChosen()
	h.mustNotHave("items selected")
}

// "⋯" in the selection bar opens the table's menu for the choice.
func TestSelectionBarMoreMenu(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.mustClick("Select manifest.json")
	h.mustClick("Object actions")
	if items := h.menu(); !slices.Contains(items, "Delete") || !slices.Contains(items, "Copy") {
		t.Fatalf("menu = %q", items)
	}
	h.closeMenu()
	h.expectChosen("README.txt", "manifest.json")
}

// At the smallest window the bar fits beside the inspector: Clear selection,
// its last button, ends inside it.
func TestSelectionBarAtMinSize(t *testing.T) {
	h := newHarness(t, "light")
	sz := auditSize{"960x600", 960, 600}
	h.resize(sz.w, sz.h)
	h.mustClick("README.txt")
	h.mustClick("Select manifest.json")
	bar, ok := h.tt.Find("Selection")
	if !ok {
		t.Fatal("no selection bar")
	}
	h.expectOnScreen(sz, "Select all", "Clear selection", "Object actions")
	for _, s := range []string{"Object actions", "Clear selection"} {
		if r, _ := h.tt.Find(s); r.X < bar.X || r.X+r.W > bar.X+bar.W {
			t.Errorf("%q at %+v is outside the bar %+v", s, r, bar)
		}
	}
	h.snapAudit(sz, "selection-bar")
}
