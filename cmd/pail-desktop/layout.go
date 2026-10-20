package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Width breakpoints. Each one collapses below threshold-hysteresis and expands
// again above threshold+hysteresis, so a window being dragged across it does
// not flip the layout back and forth.
const (
	// tbLabelsNeed is the toolbar width, without the room of the window buttons,
	// that the labelled Upload, Download and New folder buttons need; narrower
	// toolbars drop the labels.
	tbLabelsNeed = 680
	// compactBelow is the center column width under which the path bar drops the
	// text of its toggles.
	compactBelow = 560
	// colsAllNeed and colsFourNeed are the table widths that show all five and
	// the first four columns; narrower tables show Name, Size and Modified.
	colsAllNeed  = 720
	colsFourNeed = 560

	hysteresis = 12

	dividerW   = 1  // the line of a Split
	splitMinPn = 40 // a Split keeps this much of either pane (10 spacing units)
)

// breakpoint is one width threshold that remembers which side it is on.
type breakpoint struct {
	at    float32
	below bool
	set   bool
}

// update returns whether w is below the threshold. It changes its answer only
// once w has moved more than hysteresis past the threshold; the first call
// answers by the threshold itself.
func (b *breakpoint) update(w float32) bool {
	switch {
	case !b.set:
		b.below, b.set = w < b.at, true
	case b.below && w > b.at+hysteresis:
		b.below = false
	case !b.below && w < b.at-hysteresis:
		b.below = true
	}
	return b.below
}

// layoutMetrics is what the views need to know about widths, worked out from
// the size of this frame and the pane widths the app holds. Reading the boxes
// of the last frame instead (Element.Bounds) would lay out one frame late
// during a window resize.
type layoutMetrics struct {
	sidebarW  float32 // the sidebar pane as the Split lays it out
	inspW     float32 // the inspector pane, 0 when it is not shown
	barW      float32 // the top bar: everything right of the sidebar
	centerW   float32 // the path bar and table: the main column minus the inspector
	tbCompact bool    // the toolbar drops its labels
	compact   bool    // the path bar drops the text of its toggles
	colTier   int     // 0 all table columns, 1 four, 2 three
}

// layoutIn is what layoutMetrics are computed from.
type layoutIn struct {
	winW            float32
	sidebarW, inspW float32 // the widths asked for (stored, dragged by the user)
	inspector       bool    // the inspector is shown
	barRight        float32 // the room of the window buttons in the top bar
}

// layoutState keeps the breakpoints' memory from frame to frame.
type layoutState struct {
	toolbar, path, colsAll, colsFour breakpoint
}

func newLayoutState() layoutState {
	return layoutState{
		toolbar:  breakpoint{at: tbLabelsNeed},
		path:     breakpoint{at: compactBelow},
		colsAll:  breakpoint{at: colsAllNeed},
		colsFour: breakpoint{at: colsFourNeed},
	}
}

// fitPane is a Split's first pane for the size asked: the Split keeps
// splitMinPn of either side of its divider.
func fitPane(want, total float32) float32 {
	return max(min(want, total-dividerW-splitMinPn), splitMinPn)
}

// metrics works out the layout of one frame.
func (s *layoutState) metrics(in layoutIn) layoutMetrics {
	var m layoutMetrics
	m.sidebarW = fitPane(in.sidebarW, in.winW)
	m.barW = in.winW - m.sidebarW - dividerW
	m.centerW = m.barW
	if in.inspector {
		first := fitPane(m.barW-dividerW-in.inspW, m.barW)
		m.centerW = first
		m.inspW = m.barW - dividerW - first
	}
	m.tbCompact = s.toolbar.update(m.barW - in.barRight)
	m.compact = s.path.update(m.centerW)
	if s.colsAll.update(m.centerW) {
		m.colTier = 1
		if s.colsFour.update(m.centerW) {
			m.colTier = 2
		}
	} else {
		s.colsFour.update(m.centerW) // keeps its memory while the wider one decides
	}
	return m
}

// splitFx styles the divider of one Split. The mygo Split paints its line with
// the theme's Accent while hovered or dragged, which the design keeps for
// focus, selection and primary actions; the Split gets a private copy of the
// theme whose Accent is BorderStrong at hover and TextSubtle while the line is
// moving.
type splitFx struct {
	th    ui.Theme
	moved time.Time // the last frame the divider moved
}

// splitDragHold is how long after its last move a divider still counts as
// dragged: the Split does not say whether the pointer is still down.
const splitDragHold = 200 * time.Millisecond

// split is ui.Split or ui.SplitVertical, filling the room it is given, with the
// divider colours of fx. It reports whether the divider moved. The panes are
// built under the real theme.
func (a *app) split(c *ui.Context, fx *splitFx, vertical bool, size *float32, first, second func()) (changed bool) {
	k := tk(c)
	real := c.Theme()
	fx.th = *real
	under := func(f func()) func() {
		return func() {
			c.SetTheme(real)
			f()
			c.SetTheme(&fx.th)
		}
	}
	c.SetTheme(&fx.th)
	var e ui.Element
	if vertical {
		e = ui.SplitVertical(c, size, under(first), under(second))
	} else {
		e = ui.Split(c, size, under(first), under(second))
	}
	c.SetTheme(real)
	changed = e.Grow(1).MinHeight(0).Changed()
	if changed {
		fx.moved = c.Now()
	}
	// The Split paints its line from fx.th when it draws, after this build.
	fx.th.Accent = k.BorderStrong
	if c.Now().Sub(fx.moved) < splitDragHold {
		fx.th.Accent = k.TextSubtle
		c.After(splitDragHold)
	}
	return changed
}
