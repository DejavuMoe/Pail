package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The object table's metrics. Its rows are built under a theme whose Spacing
// is tableSpacing (table), which makes mygo's rows and header 8 units high and
// pads its cells by 1.5 units vertically and 2.5 horizontally.
const (
	tableSpacing = 3.75
	tableRowH    = 8 * tableSpacing
	cellPadX     = 2.5 * tableSpacing
	cellPadY     = 1.5 * tableSpacing
	checkColW    = 32
)

// objectColumns are the table columns, titled in the language shown now. The
// check box column comes first and stays there: Fixed, it is neither moved
// nor resized. Its width comes out of the name's minimum, so that the three
// columns of the narrowest table still fit beside the inspector at 960.
func objectColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "check", Width: checkColW, Fixed: true},
		{Title: locales.CommonName(), ID: "name", MinWidth: 180 - checkColW, Sortable: true},
		{Title: locales.CommonSize(), ID: "size", Width: 96, MinWidth: 64, Align: ui.End, Sortable: true},
		{Title: locales.ObjectsColType(), ID: "type", Width: 110, MinWidth: 64, Sortable: true},
		{Title: locales.ObjectsColModified(), ID: "modified", Width: 140, MinWidth: 100, Sortable: true},
		{Title: locales.ObjectsColClass(), ID: "class", Width: 130, MinWidth: 90, Sortable: true},
	}
}

// The three-column table is what fits beside the sidebar and the inspector at
// the smallest window (960 wide: 390 left for the table). Size and Modified are
// as wide as their text needs, which leaves Name enough to show a name, and the
// last column still ends with a gap before the divider: the table must never
// scroll sideways there, which would clip the date.
const (
	narrowNameMin = 120
	narrowSizeW   = 76
	narrowModW    = 128 // a date and time (about 100) with the cell's padding and lastColInset
)

// visibleColumns drops the least important columns when the table is too
// narrow for all of them, so nothing runs off the right edge: Storage class
// first, then Type (the inspector still shows both). tier is
// layoutMetrics.colTier: 0 all, 1 four data columns, anything more three.
func visibleColumns(tier int) []ui.TableColumn {
	all := objectColumns()
	switch {
	case tier <= 0:
		return all
	case tier == 1:
		return all[:5]
	default:
		narrow := []ui.TableColumn{all[0], all[1], all[2], all[4]}
		narrow[1].MinWidth = narrowNameMin
		narrow[2].Width, narrow[2].MinWidth = narrowSizeW, narrowSizeW
		narrow[3].Width = narrowModW
		return narrow
	}
}

// lastColInset is the room the right-most column keeps at its end, beside the
// cell's own padding, so that its text stays clear of the scroll bar's slot
// (scrollGutter, design.md "滚动").
const lastColInset = scrollGutter - cellPadX

// objects is the object table with its loading, error and empty states.
// Dropping OS files on it uploads them into the current prefix.
func (a *app) objects(c *ui.Context) {
	k := tk(c)
	zone := ui.Column(c).Grow(1).MinHeight(0).MinWidth(0)
	if files := zone.DroppedFiles(); len(files) > 0 {
		a.uploadPaths(files)
	}
	over := zone.FileDragOver()
	zone.Children(func() {
		switch {
		case a.listErr != "" && len(a.rows) == 0:
			a.listErrorBanner(c)
		case a.loading && len(a.raw) == 0:
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Spinner(c).Size(22, 22)
				mutedText(c, locales.ObjectsLoading(a.bucketLabel(a.scope.Bucket)))
			})
		case len(a.rows) == 0:
			a.emptyState(c)
		default:
			if a.listErr != "" { // a later page failed: keep what was loaded
				a.listErrorBanner(c)
			}
			a.table(c)
			if !over && a.dropHintShown() {
				a.dropHint(c)
			}
		}
		if over {
			ui.Column(c).Absolute().Top(4).Left(4).Right(4).Bottom(4).PassThrough().Center().Gap(8).
				Background(k.Selection.Alpha(0.5)).Border(1.5, k.Accent).Radius(RadiusPopover).Children(func() {
				ui.Icon(c, icons.Get("upload")).FontSize(24).TextColor(k.Accent)
				prefix := a.bucketLabel(a.scope.Bucket) + "/" + a.scope.Prefix
				ui.Text(c, locales.ObjectsDropToUpload(prefix)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
			})
		}
	})
}

func (a *app) table(c *ui.Context) {
	k := tk(c)
	rows := a.rows
	clear(a.rowRects)
	clear(a.rowTops)
	a.list.Key = func(i int) any { return rows[i].Key }
	a.list.Label = func(i int) string { return rows[i].Name }
	a.list.Selection = &a.sel.Selection
	a.list.Selected = &a.cursor
	a.list.Sort = &a.sort
	// Reorder switches on dragging rows; what a drop on the table itself would
	// mean (a new order) has no meaning for object keys, so it does nothing.
	a.list.Reorder = func([]int, int) {}
	prevDropKey := a.mg.dropKey
	a.mg.dropKey = ""
	// Rows paint their choice with the theme's Accent. the design spec gives
	// the table the softer Selection token, so the rows are built under a
	// theme whose Accent is that token; the rest of the frame keeps the real
	// Accent.
	defer tableThemeEdges(c, false)()

	cols := visibleColumns(a.lay.colTier)
	inset := func(col int) float32 { return lastColInset * b2f(col == len(cols)-1) }
	chosen := a.selectedCount()
	everyBox := chosen >= 2 // the selection bar is up: every row shows its box
	cell := func(row, col int) {
		e := &rows[row]
		switch cols[col].ID {
		case "check":
			if a.rowCheck(c, row, e, everyBox) {
				a.extendKey = e.Key
			}
		case "name":
			deleted, cut := a.isDeletedRow(e.Key), a.isCutRow(e.Key)
			faded := deleted || cut
			cell := ui.Row(c.Key("name")).Height(18).Gap(8).MinWidth(0).AlignItems(ui.Center)
			// Rows dragged over a folder move into it; over a file they are
			// swallowed so the table shows no reorder marker.
			hover := a.dropTarget(cell, a.dropDest(e))
			if hover {
				a.mg.dropKey = e.Key
			}
			over := hover || prevDropKey == e.Key
			cell.Children(func() {
				ic := ui.Icon(c, icons.ForKey(e.Key, e.Folder)).FontSize(16).TextColor(k.TextSubtle)
				if e.Folder && !faded {
					ic.TextColor(k.FolderIcon)
				}
				if a.mg.ren.key == e.Key {
					a.renameInput(c, *e)
					return
				}
				col := k.Text
				if faded {
					col = k.TextSubtle
				}
				ui.Text(c, e.Name).FontSize(sizeBody).TextColor(col).SingleLine().Grow(1).MinWidth(0)
				if deleted {
					badge(c, locales.ObjectsDeleted())
				}
			})
			if over {
				cell.Background(k.ActiveFill).Radius(RadiusSmall)
			}
			a.rowRects[e.Key] = cell.Bounds()
		case "size":
			if e.Folder {
				a.folderSizeCell(c, e)
			} else {
				a.dataCell(c, e, "size", humanBytes(e.Size), true, ui.End, 0, false)
			}
		case "type":
			a.dataCell(c, e, "type", typeLabel(*e), true, ui.Start, inset(col), false)
		case "modified":
			a.dataCell(c, e, "modified", folderDash(e, formatTime(e.Modified)), true, ui.Start, inset(col), e.Folder)
		case "class":
			a.dataCell(c, e, "class", folderDash(e, storageLabel(e.StorageClass)), true, ui.Start, inset(col), e.Folder)
		}
	}
	// The table and, over the empty header of its check box column, the
	// select-all box: mygo's headers show their title alone.
	var tbl ui.Element
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		tbl = ui.Table(c, &a.list, cols, len(rows), cell).Grow(1).MinHeight(0).Label(locales.ObjectsTable())
		a.sel.touch() // the table's keys, clicks and Ctrl+A change the choice in mygo
		a.selectAllBox(c, chosen)
		tableHeaderLine(c)
	})
	a.tblRect = tbl.Bounds()
	tbl.HandleInput(a.dragSelInput)
	a.dragSelFrame(c)
	tbl.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if box, ok := a.dragSelRect(r); ok {
			p.Fill(box, k.Selection.Alpha(0.5), 2)
			p.Stroke(box, k.Accent, 2, 1)
		}
	})
	if key := a.extendKey; key != "" {
		a.extendKey = ""
		if i, ok := a.byKey[key]; ok {
			a.list.ExtendTo(i)
			a.sel.touch()
		}
	}
	// Esc gives up a drag selecting rows, or clears the choice.
	if (a.drag.on || a.sel.Len() > 0 && a.mg.ren.key == "") && tbl.Shortcut(0, ui.KeyEscape) {
		if a.drag.on {
			a.dragSelCancel()
		} else {
			a.clearSelection()
		}
	}
	a.hoverKey = a.rowUnderPointer(tbl)
	// While a row is renamed, Shift+F10 is the name input's own editing menu.
	a.contextMenu(tbl, menuSpec{name: locales.MenuObjectActions(), keyAt: a.cursorCorner, noKeys: a.mg.ren.key != ""}, a.buildMenu)
	if tbl.Submitted() {
		a.openSelected()
	}
}

// dropHintMinRoom is the empty room below the last row that a short list needs
// before it shows the drop hint.
const dropHintMinRoom = 120

// dropHintRoom is the height left empty below the last row, from this frame's
// layout: the body above the status bar and the transfers panel (shell.go) less
// the top bar, the path bar and, with two or more rows chosen, the selection
// bar, then the header and the rows. It is not read from Element.Bounds, which
// lags a frame behind a resize.
func (a *app) dropHintRoom() float32 {
	room := a.bodyH - topBarH - pathBarH - tableBodyTop - float32(len(a.rows))*tableRowH
	if a.selectedCount() >= 2 {
		room -= selBarH
	}
	return room
}

// dropHintShown reports whether a plain listing is short enough to show the
// hint: nothing loading, no filter, no deleted view, a failed page leaves the
// banner in the way, and uploads possible here.
func (a *app) dropHintShown() bool {
	if a.loading || a.search != "" || a.mg.del.on || a.listErr != "" || len(a.rows) == 0 {
		return false
	}
	if cmd := a.cmd("upload.files"); cmd == nil || !a.enabled(cmd) {
		return false
	}
	return a.dropHintRoom() >= dropHintMinRoom
}

// dropHint is the one line in the empty area below a short list. It is
// pass-through: clicks, the rubber band and the context menu reach the table.
func (a *app) dropHint(c *ui.Context) {
	k := tk(c)
	ui.Row(c.Key("drophint")).Absolute().Left(0).Right(0).Bottom(0).Top(tableBodyTop + float32(len(a.rows))*tableRowH).
		PassThrough().Center().Gap(6).Children(func() {
		ui.Icon(c, icons.Get("upload")).FontSize(14).TextColor(k.TextSubtle)
		ui.Text(c, locales.ObjectsDropHint()).FontSize(sizeSecondary).TextColor(k.TextSubtle).SingleLine()
	})
}

// rowCheck is the check box of row i, showing whether it is chosen. It shows
// on the row under the pointer, on chosen rows and, with every, on all rows;
// elsewhere it is transparent and keeps its place. Its target is the whole
// cell, padding included. A click adds the row to the choice or takes it out,
// as a Ctrl/Cmd-click does, and leaves the other rows as they are; it reports
// a Shift-click, which the table extends the choice to once it is built. The
// box takes no focus: the press lands on the table, which keeps the keys.
func (a *app) rowCheck(c *ui.Context, i int, e *Entry, every bool) (extend bool) {
	on := a.sel.Has(e.Key)
	hit := ui.Row(c.Key("check")).Width(checkColW).Height(tableRowH).Margin(-cellPadY, -cellPadX).Center().
		Role(ui.RoleCheckBox).Checked(on).Label(locales.ObjectsSelectRow(e.Name))
	if b := hit.Bounds(); b.H > 0 {
		a.rowTops[e.Key] = b.Y
	}
	// The table takes the pointer's presses on the box (dragSelInput); a
	// click comes from assistive technology.
	if hit.Clicked() {
		if hit.ClickModifiers()&ui.Shift != 0 {
			extend = true
		} else {
			if on {
				a.sel.Remove(e.Key)
			} else {
				a.sel.Add(e.Key)
			}
			on = !on
			a.cursor = i
		}
	}
	st := checkOff
	if on {
		st = checkOn
	}
	shown := on || every || a.hoverKey == e.Key || hit.Hovered()
	hit.Children(func() {
		box := checkBox(c, st, hit.Hovered(), false)
		if !shown {
			box.Opacity(0) // its column stays, so the names do not move
		}
	})
	return extend
}

// selectAllBox is the select-all check box over the header of the check box
// column: checked when every row shown is chosen, mixed when some are. A
// click chooses every row shown, as Ctrl/Cmd+A does, or none when all are.
func (a *app) selectAllBox(c *ui.Context, chosen int) {
	cmd := a.cmd("selectall")
	st := selectAllState(chosen, len(a.rows))
	box := ui.Row(c.Key("selectall")).Absolute().Top(0).Left(0).Width(checkColW).Height(tableRowH).Center().
		Role(ui.RoleCheckBox).Checked(st == checkOn).Label(cmd.text()).Tooltip(cmd.tooltip())
	if st == checkMixed {
		box.Mixed()
	}
	if box.Clicked() {
		if st == checkOn {
			a.clearSelection()
		} else {
			a.selectAll()
		}
		a.list.Focus(c)
	}
	box.Children(func() { checkBox(c, st, box.Hovered(), false) })
}

// selectAllState is the select-all box with chosen of n rows shown chosen.
func selectAllState(chosen, n int) checkState {
	switch {
	case chosen == 0:
		return checkOff
	case chosen >= n:
		return checkOn
	}
	return checkMixed
}

// openSelected is double click / Enter: folders open, objects show details.
func (a *app) openSelected() {
	keys := a.selectedKeys()
	if len(keys) != 1 {
		return
	}
	if key := keys[0]; key[len(key)-1] == '/' {
		a.openPrefix(key)
	} else {
		a.inspectorOpen, a.inspTab = true, 0
	}
}

func (a *app) listErrorBanner(c *ui.Context) {
	errorBanner(c, nil, locales.ObjectsListFailed(a.scope.Bucket, a.listErr), a.loading, a.reload)
}

func (a *app) emptyState(c *ui.Context) {
	ui.Column(c).Grow(1).Center().Gap(4).Children(func() {
		name, title, sub := "folder-open", locales.ObjectsEmptyTitle(), locales.ObjectsEmptySub()
		if a.mg.del.on {
			name, title, sub = "trash-2", locales.ObjectsNoDeletedTitle(), locales.ObjectsNoDeletedSub()
		}
		if a.search != "" {
			name, title, sub = "search", locales.ObjectsNoMatchesTitle(), locales.ObjectsNoMatchesSub(a.search)
		}
		emptyLines(c, name, title, sub)
		if a.search != "" {
			ghostButton(c, nil, locales.ObjectsClearFilter(), locales.ObjectsClearFilter(), false).Margin(12, 0, 0, 0).OnClick(func() { a.search = ""; a.rebuildRows() })
		}
	})
}

// dropDest is the folder rows dropped on e move into, nil for anything that is
// not a folder (a drop is then swallowed).
func (a *app) dropDest(e *Entry) *Scope {
	if !e.Folder || a.mg.del.on {
		return nil
	}
	s := a.folderScope(e.Key)
	return &s
}

// dataCell is a secondary table cell that, like the name, takes rows dropped
// on its row. tail is extra room kept at its end (lastColInset in the last
// column).
func (a *app) dataCell(c *ui.Context, e *Entry, id, text string, show bool, align ui.Align, tail float32, dash bool) {
	box := ui.Row(c.Key(id)).FillWidth().Height(18).Padding(0, tail, 0, 0).AlignItems(ui.Center)
	if align == ui.End {
		box.Justify(ui.End)
	}
	if a.dropTarget(box, a.dropDest(e)) {
		a.mg.dropKey = e.Key
	}
	box.Children(func() {
		if dash {
			subtleText(c, text).SingleLine() // a folder has no value here
			return
		}
		secondaryCell(c, text, show, align)
	})
}

// folderDash is the text of a data cell a folder has no value for: "–".
func folderDash(e *Entry, text string) string {
	if e.Folder {
		return "–"
	}
	return text
}

// secondaryCell is a 12px muted table cell, empty when show is false.
func secondaryCell(c *ui.Context, s string, show bool, align ui.Align) {
	if !show {
		return
	}
	t := ui.Text(c, s).FontSize(sizeSecondary).TextColor(tk(c).TextMuted).SingleLine()
	if align == ui.End {
		t.TextAlign(ui.End)
	}
}

// rowUnderPointer returns the key of the visible row under the pointer ("" over
// blank space or the header). Table rows are hit areas of their own, so the
// cells' previous-frame bounds are matched against the pointer instead.
func (a *app) rowUnderPointer(tbl ui.Element) string {
	px, py, over := tbl.PointerPosition()
	if !over {
		return ""
	}
	_, y := tbl.LocalToWindow(px, py)
	for key, r := range a.rowRects {
		if y >= r.Y && y < r.Y+r.H {
			return key
		}
	}
	return ""
}

// b2f is 1 for true and 0 for false.
func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
