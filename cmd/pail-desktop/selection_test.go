package main

import (
	"slices"
	"testing"
	"time"

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

// The middle of the row showing name, and the middle of its check box.
func (h *harness) rowMid(name string) (checkX, y float32) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(name)
	if !ok {
		h.t.Fatalf("no row %q; have %q", name, h.tt.Texts())
	}
	return h.a.tblRect.X + checkColW/2, r.Y + r.H/2
}

// dragTo presses at (x0, y0), moves to (x1, y1) in a few steps and releases.
func (h *harness) dragTo(x0, y0, x1, y1 float32) {
	h.t.Helper()
	h.tt.Move(x0, y0)
	h.tt.Press(x0, y0)
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		h.tt.Move(x0+(x1-x0)*f, y0+(y1-y0)*f)
		h.tt.Frame()
	}
	h.tt.Release(x1, y1)
	h.settle()
}

// A drag down the check box column adds the rows it passes to the choice.
func TestCheckColumnDragAdds(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	x, y0 := h.rowMid("assets")
	_, y1 := h.rowMid("logs")
	h.dragTo(x, y0, x, y1)
	h.expectChosen("README.txt", "assets/", "backups/", "logs/")
	// Over rows already chosen it takes none out.
	_, y2 := h.rowMid("backups")
	h.dragTo(x, y2, x, y1)
	h.expectChosen("README.txt", "assets/", "backups/", "logs/")
}

// A drag from the blank space below the rows draws a rectangle and chooses
// the rows it reaches, in place of the choice; with Ctrl/Cmd, besides it.
func TestBlankSpaceDragSelects(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("assets")
	_, last := h.rowMid("README.txt")
	_, tmp := h.rowMid("tmp")
	x := h.a.tblRect.X + h.a.tblRect.W/2
	blank := last + tableRowH*2
	h.tt.Move(x, blank)
	h.tt.Press(x, blank)
	h.tt.Move(x-40, (blank+tmp)/2)
	h.tt.Frame()
	h.tt.Move(x-60, tmp)
	h.tt.Frame()
	h.tt.Frame()
	if _, ok := h.a.dragSelRect(h.a.tblRect); !ok {
		t.Error("no selection rectangle while dragging from blank space")
	}
	h.snapBoth("drag-select-rectangle")
	h.tt.Release(x-60, tmp)
	h.settle()
	if _, ok := h.a.dragSelRect(h.a.tblRect); ok {
		t.Error("the rectangle stayed after the release")
	}
	h.expectChosen("tmp/", "website/", "manifest.json", "README.txt")

	// Tester.Press carries no modifiers: the Ctrl/Cmd drag goes to the
	// table's input handler as the window would give it.
	h.mustClick("assets")
	at := func(kind ui.InputKind, wy float32) {
		h.a.dragSelInput(ui.InputEvent{Kind: kind, X: x - h.a.tblRect.X, Y: wy - h.a.tblRect.Y, Mods: ui.Cmd})
	}
	at(ui.InputPointerDown, blank)
	at(ui.InputPointerMove, last)
	at(ui.InputPointerUp, last)
	h.expectChosen("assets/", "README.txt")
}

// A press below the rows that does not move clears the choice, unless with
// Ctrl/Cmd; one that moves less than the slop is still a click.
func TestBlankSpaceClick(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	h.mustClick("Select manifest.json")
	_, last := h.rowMid("README.txt")
	x, blank := h.a.tblRect.X+h.a.tblRect.W/2, last+tableRowH*2
	h.tt.ClickAtWith(ui.Cmd, x, blank)
	h.settle()
	h.expectChosen("README.txt", "manifest.json")
	h.dragTo(x, blank, x+2, blank+1)
	h.expectChosen()
}

// Esc gives up a drag: the choice goes back to what it was before the press.
func TestEscGivesUpADrag(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("README.txt")
	x, y0 := h.rowMid("assets")
	_, y1 := h.rowMid("logs")
	h.tt.Move(x, y0)
	h.tt.Press(x, y0)
	h.tt.Move(x, y1)
	h.tt.Frame()
	h.expectChosen("README.txt", "assets/", "backups/", "logs/")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	h.expectChosen("README.txt")
	h.tt.Move(x, y1+tableRowH)
	h.tt.Release(x, y1+tableRowH)
	h.settle()
	h.expectChosen("README.txt")
}

// Held below the list, a drag scrolls it and goes on choosing the rows that
// come into view.
func TestDragSelectScrolls(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openPrefix("logs/access/")
	h.settle()
	if len(h.a.rows) < 1000 {
		t.Fatalf("logs/access/ lists %d rows", len(h.a.rows))
	}
	first := h.a.rows[0].Name
	x, y0 := h.rowMid(first)
	_, shown := h.a.list.Visible()
	below := h.a.tblRect.Y + h.a.tblRect.H + 20
	h.tt.Move(x, y0)
	h.tt.Press(x, y0)
	h.tt.Move(x, below)
	h.tt.Frame()
	for range 40 {
		h.tt.Advance(50 * time.Millisecond)
		h.tt.Frame()
	}
	h.tt.Release(x, below)
	h.settle()
	n := len(h.chosenKeys())
	if n <= shown+1 {
		t.Fatalf("the drag chose %d rows; %d were in view: it did not scroll", n, shown+1)
	}
	if !h.a.sel.Has(h.a.rows[0].Key) || !h.a.sel.Has(h.a.rows[n-1].Key) {
		t.Fatalf("the drag did not choose rows 0 to %d in a run", n-1)
	}
}
