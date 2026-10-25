package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Drag selection in the object table (DESIGN.md "对象表", 拖动选择). The table
// takes a press in its check box column or in the blank space below its rows
// (HandleInput) before the row under it would: from there a drag selects, and
// a press that does not move is a click. Presses on the name and data cells
// go to the rows as before, where a drag moves rows into a folder: mygo's own
// ListState.DragSelect cannot be had with Reorder.
//
// Rows are tableRowH high and follow one another without gaps, so a height
// in the window is a row position (rowPos): an index and the fraction of that
// row above it. A drag keeps where it began as a row position, so that it
// stays with its row as the list scrolls.

const (
	dragSlop       = 4             // mygo's dragStart: a press that moves less is a click
	autoScrollEdge = 32            // how near the list's top or bottom a drag scrolls it
	scrollbarSlot  = 16            // the right edge of the table, where its scroll bar is
	tableBodyTop   = tableRowH + 1 // below the header and its divider, table-local
)

// dragSel is a press the object table took.
type dragSel struct {
	on, moved bool
	check     bool   // pressed in the check box column: the drag adds and draws nothing
	key       string // the row whose box was pressed; "" in blank space
	add       bool   // the drag keeps what was chosen before it
	mods      ui.Modifiers
	x0, y0    float32  // the press, table-local
	x, y      float32  // the pointer, table-local
	from      float64  // the press as a row position
	base      []string // what was chosen before the press, filtered rows too
	scrollAt  time.Time
	scrollAcc float64
}

// rowPos returns the row position of wy, a height in the window, from the
// rows the table built in the last frame.
func (a *app) rowPos(wy float32) (float64, bool) {
	i, top, ok := a.rowAnchor()
	if !ok {
		return 0, false
	}
	return float64(i) + float64(wy-top)/tableRowH, true
}

// rowY is rowPos the other way: the height in the window of row position pos.
func (a *app) rowY(pos float64) (float32, bool) {
	i, top, ok := a.rowAnchor()
	if !ok {
		return 0, false
	}
	return top + float32(pos-float64(i))*tableRowH, true
}

// rowAnchor is the first row built in the last frame and its top, in the
// window.
func (a *app) rowAnchor() (i int, top float32, ok bool) {
	i = -1
	for k, y := range a.rowTops {
		if j, listed := a.byKey[k]; listed && (i < 0 || j < i) {
			i, top = j, y
		}
	}
	return i, top, i >= 0
}

// dragSelInput is the object table's HandleInput: it takes the presses that
// select by dragging, and their moves and release.
func (a *app) dragSelInput(ev ui.InputEvent) bool {
	d := &a.drag
	switch ev.Kind {
	case ui.InputPointerDown:
		return a.dragSelPress(ev)
	case ui.InputPointerMove:
		if !d.on {
			return false
		}
		d.x, d.y = ev.X, ev.Y
		if !d.moved && math.Hypot(float64(d.x-d.x0), float64(d.y-d.y0)) >= dragSlop {
			d.moved = true
		}
		if d.moved {
			a.dragSelApply()
		}
		return true
	case ui.InputPointerUp:
		if !d.on {
			return false
		}
		if !d.moved {
			a.dragSelClick()
		}
		*d = dragSel{}
		return true
	}
	return false
}

// dragSelPress starts a drag selection for a primary press in the check box
// column of a row, or below the last row; elsewhere the rows have it.
func (a *app) dragSelPress(ev ui.InputEvent) bool {
	if ev.Button != 0 || a.mg.ren.key != "" || len(a.rows) == 0 {
		return false
	}
	if ev.Y < tableBodyTop || ev.Y >= a.tblRect.H || ev.X < 0 || ev.X >= a.tblRect.W-scrollbarSlot {
		return false
	}
	pos, ok := a.rowPos(a.tblRect.Y + ev.Y)
	if !ok {
		return false
	}
	row := int(math.Floor(pos))
	d := dragSel{on: true, mods: ev.Mods, x0: ev.X, y0: ev.Y, x: ev.X, y: ev.Y, from: pos}
	switch {
	case row >= len(a.rows):
		d.add = ev.Mods&ui.Cmd != 0
	case row >= 0 && ev.X < checkColW:
		d.check, d.add, d.key = true, true, a.rows[row].Key
		d.from = float64(row) + 0.5 // the drag always takes the row pressed
	default:
		return false
	}
	for k := range a.sel.All() {
		d.base = append(d.base, k)
	}
	a.drag = d
	return true
}

// dragSelClick is a press that did not move: on a box it adds the row to the
// choice or takes it out (Shift extends the choice to it, once the table is
// built); in blank space it clears the choice, unless with Ctrl/Cmd.
func (a *app) dragSelClick() {
	d := &a.drag
	if !d.check {
		if d.mods&ui.Cmd == 0 {
			a.clearSelection()
		}
		return
	}
	i, ok := a.byKey[d.key]
	if !ok {
		return
	}
	switch {
	case d.mods&ui.Shift != 0:
		a.extendKey = d.key
	case a.sel.Has(d.key):
		a.sel.Remove(d.key)
		a.cursor = i
	default:
		a.sel.Add(d.key)
		a.cursor = i
	}
}

// dragSelApply chooses the rows from where the drag began to the pointer,
// besides those chosen before it when it adds.
func (a *app) dragSelApply() {
	d := &a.drag
	cur, ok := a.rowPos(a.tblRect.Y + d.y)
	if !ok {
		return
	}
	lo, hi := min(d.from, cur), max(d.from, cur)
	first := max(0, int(math.Floor(lo)))
	last := min(len(a.rows)-1, int(math.Ceil(hi))-1)
	a.sel.Clear()
	if d.add {
		for _, k := range d.base {
			a.sel.Add(k)
		}
	}
	for i := first; i <= last; i++ {
		a.sel.Add(a.rows[i].Key)
	}
	if first <= last {
		a.cursor = min(max(int(math.Floor(cur)), first), last)
	}
}

// dragSelCancel ends the drag with the choice it found (Esc).
func (a *app) dragSelCancel() {
	a.sel.Clear()
	for _, k := range a.drag.base {
		a.sel.Add(k)
	}
	a.drag = dragSel{}
}

// dragSelFrame keeps a drag going in a frame: it scrolls the list while the
// pointer is near its top or bottom, or past them, faster the farther out,
// and chooses again the rows the drag now covers.
func (a *app) dragSelFrame(c *ui.Context) {
	d := &a.drag
	if !d.on || !d.moved {
		return
	}
	var over float32 // how far into the edge or past it; up is negative
	switch top, bottom := float32(tableBodyTop)+autoScrollEdge, a.tblRect.H-autoScrollEdge; {
	case d.y < top:
		over = d.y - top
	case d.y > bottom:
		over = d.y - bottom
	}
	now := c.Now()
	if over == 0 {
		d.scrollAt, d.scrollAcc = time.Time{}, 0
	} else {
		if !d.scrollAt.IsZero() {
			// Rows a second: from 8 at the edge up to 60 well past it.
			speed := min(8+float64(abs32(over))*0.75, 60)
			d.scrollAcc += now.Sub(d.scrollAt).Seconds() * speed
		}
		d.scrollAt = now
		if steps := int(d.scrollAcc); steps > 0 {
			d.scrollAcc -= float64(steps)
			first, last := a.list.Visible()
			if over < 0 {
				a.list.ScrollIntoView(max(0, first-steps))
			} else {
				a.list.ScrollIntoView(min(len(a.rows)-1, last+steps))
			}
		}
		c.AnimationFrame()
	}
	a.dragSelApply()
}

// dragSelRect is the selection rectangle of a drag from blank space, in the
// table's box r as DrawOver gives it, clipped to the list below the header.
func (a *app) dragSelRect(r ui.Rect) (ui.Rect, bool) {
	d := &a.drag
	if !d.on || !d.moved || d.check {
		return ui.Rect{}, false
	}
	wy, ok := a.rowY(d.from)
	if !ok {
		return ui.Rect{}, false
	}
	y0 := wy - a.tblRect.Y
	x0, x1 := max(min(d.x0, d.x), 0), min(max(d.x0, d.x), r.W)
	t, b := max(min(y0, d.y), tableBodyTop), min(max(y0, d.y), r.H)
	if x1 <= x0 || b <= t {
		return ui.Rect{}, false
	}
	return ui.Rect{X: r.X + x0, Y: r.Y + t, W: x1 - x0, H: b - t}, true
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }
