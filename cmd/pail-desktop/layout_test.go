package main

import (
	"math"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// ---- breakpoints with hysteresis -------------------------------------------------------------

// A width that wobbles within 10 DIPs of a threshold, from whichever side the
// layout started on, must not flip the tier; a clear crossing must.
func TestBreakpointDoesNotFlipWithinHysteresis(t *testing.T) {
	for _, at := range []float32{tbLabelsNeed, compactBelow, colsAllNeed, colsFourNeed} {
		for _, start := range []float32{at - 100, at + 100} {
			b := breakpoint{at: at}
			want := b.update(start)
			if want != (start < at) {
				t.Fatalf("T=%v: first answer at %v is %v", at, start, want)
			}
			// Settle just inside the band from the side it started on.
			edge := at + 10
			if want {
				edge = at - 10
			}
			if got := b.update(edge); got != want {
				t.Fatalf("T=%v: moving to %v flipped the tier to %v", at, edge, got)
			}
			for i := 0; i < 200; i++ {
				w := at + float32(i%21-10) // at-10 .. at+10
				if got := b.update(w); got != want {
					t.Fatalf("T=%v start %v: width %v flipped the tier to %v", at, start, w, got)
				}
			}
		}
	}
}

func TestBreakpointSwitchesPastTheBand(t *testing.T) {
	const at = 560
	b := breakpoint{at: at}
	if b.update(at + 100) {
		t.Fatal("a wide start is below the threshold")
	}
	if b.update(at - 11) {
		t.Fatal("collapsed at T-11, inside the band")
	}
	if !b.update(at - 13) {
		t.Fatal("did not collapse at T-13")
	}
	if !b.update(at + 11) {
		t.Fatal("expanded at T+11, inside the band")
	}
	if b.update(at + 13) {
		t.Fatal("did not expand at T+13")
	}
	if b.update(at-12) != false || !b.update(at-12.5) {
		t.Fatal("the collapse edge is T-12 exclusive")
	}
}

// A drag that sweeps the whole range changes the tiers once per threshold and
// then back once, not on every pixel.
func TestLayoutTiersChangeOncePerCrossing(t *testing.T) {
	s := newLayoutState()
	in := layoutIn{winW: 1500, sidebarW: 248, inspW: 320, inspector: true}
	flips, last := 0, s.metrics(in)
	for w := float32(1500); w >= 960; w-- {
		in.winW = w
		m := s.metrics(in)
		if m != last {
			if m.colTier != last.colTier || m.compact != last.compact || m.tbCompact != last.tbCompact {
				flips++
			}
			last = m
		}
	}
	for w := float32(960); w <= 1500; w++ {
		in.winW = w
		m := s.metrics(in)
		if m != last && (m.colTier != last.colTier || m.compact != last.compact || m.tbCompact != last.tbCompact) {
			flips++
		}
		last = m
	}
	// Narrowing: table 720 -> 560 (two steps), toolbar labels, and the way back.
	if flips > 8 {
		t.Fatalf("the tiers flipped %d times over one down and one up sweep", flips)
	}
}

func TestLayoutTiersFollowTheWidth(t *testing.T) {
	s := newLayoutState()
	wide := s.metrics(layoutIn{winW: 1600, sidebarW: 248, inspW: 320, inspector: true})
	if wide.tbCompact || wide.compact || wide.colTier != 0 {
		t.Errorf("1600 wide: %+v", wide)
	}
	narrow := s.metrics(layoutIn{winW: 960, sidebarW: 248, inspW: 320, inspector: true, barRight: 138})
	if !narrow.tbCompact || narrow.colTier != 2 {
		t.Errorf("960 wide: %+v", narrow)
	}
}

// The pieces of a row add up to the window, whatever the stored widths: the
// views lay out by these numbers, so they must agree with what the Splits do.
func TestLayoutMetricsPartitionTheWindow(t *testing.T) {
	for _, w := range []float32{960, 1100, 1440, 2000} {
		for _, insp := range []bool{false, true} {
			s := newLayoutState()
			m := s.metrics(layoutIn{winW: w, sidebarW: 248, inspW: 320, inspector: insp})
			if m.sidebarW != 248 {
				t.Errorf("w=%v: the sidebar was resized to %v; panels keep the width the user gave them", w, m.sidebarW)
			}
			sum := m.sidebarW + dividerW + m.centerW
			if insp {
				if m.inspW != 320 {
					t.Errorf("w=%v: the inspector was resized to %v", w, m.inspW)
				}
				sum += dividerW + m.inspW
			} else if m.inspW != 0 {
				t.Errorf("w=%v: inspector width %v while closed", w, m.inspW)
			}
			if math.Abs(float64(sum-w)) > 0.01 {
				t.Errorf("w=%v insp=%v: panes add up to %v", w, insp, sum)
			}
		}
	}
	// A window too small for the panes squeezes them the way the Split does.
	s := newLayoutState()
	m := s.metrics(layoutIn{winW: 300, sidebarW: 248, inspW: 320, inspector: true})
	if m.sidebarW < splitMinPn || m.centerW < splitMinPn || m.inspW < 0 {
		t.Errorf("a 300 wide window: %+v", m)
	}
}

// leastWidth is the narrowest a table of cols can be laid out: the columns with
// a width as they are, the others at their least.
func leastWidth(cols []ui.TableColumn) float32 {
	var w float32
	for _, c := range cols {
		if c.Width > 0 {
			w += max(c.Width, c.MinWidth)
		} else {
			w += c.MinWidth
		}
	}
	return w
}

// Each table tier fits the width it is shown at, down to where the next tier
// takes over, so that the table never scrolls sideways: a column that runs out
// of room would clip its text, the date of the last one first.
func TestObjectColumnsFitTheirTier(t *testing.T) {
	s := newLayoutState()
	min960 := s.metrics(layoutIn{winW: 960, sidebarW: 248, inspW: 320, inspector: true, barRight: 138}).centerW
	// Tier 2 is shown below colsFourNeed-hysteresis; at 960 it gets min960.
	if got := leastWidth(visibleColumns(2)); got > min960 {
		t.Errorf("the three-column table needs %v, the 960 window leaves %v", got, min960)
	}
	// The last column of each tier keeps the room for its text (a date is about
	// 100 wide) with the cell's padding on both sides and the scroll bar's slot.
	const dateW = 100
	for tier := 0; tier <= 2; tier++ {
		cols := visibleColumns(tier)
		last := cols[len(cols)-1]
		if last.ID == "modified" && last.Width < dateW+2*cellPadX+lastColInset {
			t.Errorf("tier %d: the Modified column is %v wide, too little for a date and its end gap", tier, last.Width)
		}
	}
	// A tier is only shown where its columns fit: tier 1 below colsAllNeed,
	// tier 0 above it, tier 2 below colsFourNeed.
	if got := leastWidth(visibleColumns(1)); got > colsFourNeed-hysteresis {
		t.Errorf("the four-column table needs %v, shown from %v", got, colsFourNeed-hysteresis)
	}
	if got := leastWidth(visibleColumns(0)); got > colsAllNeed+hysteresis {
		t.Errorf("the five-column table needs %v, shown from %v", got, colsAllNeed+hysteresis)
	}
}

// ---- in the app ------------------------------------------------------------------------------

// At the smallest window the last cell of a row ends before the inspector's
// divider with a gap, not at or under it, and the table does not scroll sideways.
func TestObjectTableKeepsItsLastColumnClearAtTheMinimumWindow(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = true
	h.resize(960, 600)
	if h.a.lay.colTier != 2 {
		t.Fatalf("column tier at 960 = %d, want 2", h.a.lay.colTier)
	}
	var date string
	for i := range h.a.rows {
		if !h.a.rows[i].Folder && !h.a.rows[i].Modified.IsZero() {
			date = formatTime(h.a.rows[i].Modified)
			break
		}
	}
	if date == "" {
		t.Fatal("no file with a date in the table")
	}
	r, ok := h.tt.Find(date)
	if !ok {
		t.Fatalf("the date %q is not shown; have %q", date, h.tt.Texts())
	}
	tbl := h.a.tblRect
	if gap := tbl.X + tbl.W - (r.X + r.W); gap < scrollGutter {
		t.Errorf("the date ends %.1f short of the table's end (%v), want at least %d", gap, tbl.X+tbl.W, scrollGutter)
	}
	if want := leastWidth(visibleColumns(2)); want > tbl.W {
		t.Errorf("the table is %.1f wide but its columns need %.1f: it scrolls sideways", tbl.W, want)
	}
	h.snap("objects-narrow-light")
}

// With the path bar compact the folder filter is a 28px icon, so the
// breadcrumb keeps the room; it unfolds when asked for (the icon, Ctrl+F),
// stays while it has the keyboard or text, and folds again once it lost the
// keyboard empty, or on Esc.
func TestCompactPathBarFoldsTheFolderFilter(t *testing.T) {
	h := newHarness(t, "light")
	h.a.inspectorOpen = true
	h.resize(960, 600)
	if !h.a.lay.compact {
		t.Fatalf("the path bar is not compact at 960 (centre %v)", h.a.lay.centerW)
	}
	label := locales.PathbarFilterFolder()
	width := func() float32 {
		t.Helper()
		h.tt.Frame()
		r, ok := h.tt.Find(label)
		if !ok {
			t.Fatalf("no %q in the path bar; have %q", label, h.tt.Texts())
		}
		return r.W
	}
	if w := width(); w != 28 {
		t.Fatalf("a compact bar shows the filter %v wide, want the 28px icon", w)
	}
	h.snap("pathbar-compact-folded-light")

	// The icon unfolds the box and gives it the keyboard.
	h.mustClick(label)
	h.settle()
	// The input inside the box: its normal width (140 at this size) less the
	// icon and the padding, not squeezed by the breadcrumb.
	if w := width(); w < 60 {
		t.Fatalf("the unfolded filter is %v wide", w)
	}
	if !h.a.filter.open || !h.a.filter.seen {
		t.Fatalf("the unfolded filter did not take the keyboard: %+v", h.a.filter)
	}
	h.snap("pathbar-compact-open-light")
	// Esc on the empty box folds it.
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if w := width(); w != 28 {
		t.Errorf("Esc left the filter %v wide", w)
	}
	if h.a.filter != (pathFilter{}) {
		t.Errorf("Esc left the filter state %+v", h.a.filter)
	}

	// It stays unfolded while it holds text, even without the keyboard.
	h.a.openFilter()
	h.settle()
	h.tt.Type("man")
	h.settle()
	if h.a.search != "man" {
		t.Fatalf("typed %q into the filter", h.a.search)
	}
	h.mustClick("manifest.json")
	if w := width(); w < 60 {
		t.Errorf("a filter with text folded: %v wide", w)
	}
	// Emptied and left, it folds.
	h.a.search = ""
	h.a.rebuildRows()
	h.a.openFilter()
	h.settle()
	h.mustClick("manifest.json")
	h.settle()
	if w := width(); w != 28 {
		t.Errorf("an empty filter without the keyboard stayed %v wide", w)
	}

	// The command opens it as well.
	h.runCmd("search")
	h.settle()
	if w := width(); w < 60 || !h.a.filter.seen {
		t.Errorf("Ctrl+F: the filter is %v wide, state %+v", w, h.a.filter)
	}

	// A wide bar always shows the box and forgets what was asked.
	h.resize(1440, 900)
	if h.a.lay.compact {
		t.Fatal("the path bar is compact at 1440")
	}
	if w := width(); w < 60 {
		t.Errorf("a wide path bar shows the filter %v wide", w)
	}
	if h.a.filter != (pathFilter{}) {
		t.Errorf("a wide path bar keeps filter state %+v", h.a.filter)
	}
}

// The panels keep the width they were given at the smallest window: there is no
// one-time narrowing any more.
func TestPanelsAreNotNarrowedAtTheMinimumWindow(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	for _, sz := range auditSizes {
		h.resize(sz.w, sz.h)
	}
	if h.a.sidebarW != 248 || h.a.inspW != 320 {
		t.Errorf("panel widths %v / %v after shrinking to 960, want 248 / 320", h.a.sidebarW, h.a.inspW)
	}
	// Every command stays reachable: icon only, but on screen.
	h.expectOnScreen(auditSizes[1], "Refresh", "More")
}

// The path bar of a frame is as wide as the metrics say it is, in the frame
// that follows the resize, not one frame later.
func TestLayoutUsesTheCurrentFrameSize(t *testing.T) {
	h := newHarness(t, "light")
	for _, w := range []int{1440, 1300, 1180, 1100, 1010, 960, 1200, 1440} {
		h.tt.SetSize(w, 700)
		h.tt.Frame()
		bar, ok := h.tt.Find(locales.PathbarPath())
		if !ok {
			t.Fatalf("%d: no path bar", w)
		}
		if math.Abs(float64(bar.W-h.a.lay.centerW)) > 1 {
			t.Errorf("%d wide: the path bar is %.1f wide, the layout assumed %.1f", w, bar.W, h.a.lay.centerW)
		}
	}
}

// Every standard header keeps its label next to the "?" at the inspector widths
// users have: the label is as wide as it is in a wide panel, never the lone
// ellipsis a zero-width label draws.
func TestInspectorHeaderLabelsKeepTheirWidth(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Headers")

	measure := func(w float32) map[string]float32 {
		h.a.inspW = w
		h.settle()
		got := map[string]float32{}
		for _, name := range stdHeaders {
			r, ok := h.tt.Find(name)
			if !ok {
				t.Fatalf("inspector %v: no label %q; have %q", w, name, h.tt.Texts())
			}
			got[name] = r.W
		}
		return got
	}
	full := measure(520)
	for _, w := range []float32{320, 280} {
		for name, lw := range measure(w) {
			// At least 5 DIPs a letter, up to 48 (a text of 12px Inter is wider),
			// so that a label frozen at no width cannot hide behind a baseline
			// that is frozen too.
			if lw < min(48, 5*float32(len(name))) {
				t.Errorf("inspector %v: the %q label is %.1f wide", w, name, lw)
			}
			if lw < full[name]-0.5 {
				t.Errorf("inspector %v: the %q label is %.1f wide, %.1f when there is room", w, name, lw, full[name])
			}
		}
		h.snap("headers-inspector-" + map[float32]string{320: "320", 280: "280"}[w])
	}
}

// The dividers are never painted in the accent colour: hover is BorderStrong,
// and a divider that just moved is TextSubtle.
func TestSplitDividersAvoidTheAccent(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	k := tokensFor(false)
	for name, fx := range map[string]*splitFx{"sidebar": &h.a.fxSide, "inspector": &h.a.fxInsp, "transfers": &h.a.fxTrans} {
		if name == "transfers" {
			continue // the panel collapses by itself while there is no transfer
		}
		if fx.th.Accent == k.Accent {
			t.Errorf("%s divider is painted with the accent colour", name)
		}
		if fx.th.Accent != k.BorderStrong {
			t.Errorf("%s divider at rest: %v, want BorderStrong", name, fx.th.Accent)
		}
	}
	// Dragging the sidebar divider turns it TextSubtle, and it falls back to
	// BorderStrong once it has been still for a moment.
	x, y := h.a.sidebarW+0.5, float32(400)
	h.tt.Move(x, y)
	h.tt.Press(x, y)
	h.tt.Move(x+30, y)
	h.tt.Frame()
	if h.a.sidebarW < 270 {
		t.Fatalf("the drag did not move the sidebar divider: %v", h.a.sidebarW)
	}
	if got := h.a.fxSide.th.Accent; got != k.TextSubtle {
		t.Errorf("a dragged divider is %v, want TextSubtle %v", got, k.TextSubtle)
	}
	h.tt.Release(x+30, y)
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if got := h.a.fxSide.th.Accent; got == k.Accent || got == k.TextSubtle {
		t.Errorf("a divider at rest is %v", got)
	}
}
