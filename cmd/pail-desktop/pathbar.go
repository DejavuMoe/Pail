package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// pathBar is the breadcrumb trail on the left and the folder filter on the
// right (40px high).
// pathBarH is the height of the path bar.
const pathBarH = 40

func (a *app) pathBar(c *ui.Context) {
	k := tk(c)
	parts := splitPrefix(a.scope.Prefix)
	items := append([]string{a.scope.Bucket}, parts...)
	last := len(items) - 1
	// The filter gives way to the breadcrumb: about a third of the bar, 140-240px.
	searchW := min(240, max(140, a.lay.centerW*0.3))
	ui.Row(c).Height(pathBarH).Padding(0, 12).Gap(12).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Border).Label(locales.PathbarPath()).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("database")).FontSize(14).TextColor(k.TextMuted).MarginX(4)
			for i, name := range items {
				if i > 0 {
					ui.Icon(c, icons.Get("chevron-right")).FontSize(12).TextColor(k.TextSubtle)
				}
				// The first part is the bucket: it shows its alias when it has one.
				shown, tip := name, name
				if i == 0 {
					shown, tip = a.bucketLabel(name), a.bucketTip(name)
				}
				seg := ui.ButtonBase(c.Key(i)).Label(shown).Height(24).Padding(0, 6).Radius(RadiusControl).Center().Shrink(1).MinWidth(0).Tooltip(tip).Transition(hoverFade())
				col, w := k.TextMuted, 400
				// Rows dropped on a parent folder move there.
				p := ""
				if i > 0 {
					p = strings.Join(parts[:i], "/") + "/"
				}
				dest := a.scope
				dest.Prefix = p
				over := i != last && !a.mg.del.on && !a.objOps.flat && a.dropTarget(seg, &dest)
				switch {
				case over:
					seg.Background(k.ActiveFill)
					col = k.Text
				case i == last:
					col, w = k.Text, 600
				case seg.Hovered():
					col = k.Text
				}
				seg.Children(func() {
					ui.Text(c, shown).FontSize(sizeBody).FontWeight(w).TextColor(col).SingleLine()
				})
				if i != last && seg.Clicked() {
					p := ""
					if i > 0 {
						p = strings.Join(parts[:i], "/") + "/"
					}
					a.openPrefix(p)
				}
			}
			if a.loading && len(a.raw) > 0 {
				ui.Spinner(c).Size(14, 14).MarginX(6)
			}
		})
		a.folderFilter(c, searchW)
		a.flatToggle(c)
		a.deletedToggle(c)
	})
}

// pathFilter remembers how the folder filter of a compact path bar is doing.
// While the bar is wide the filter is always a box and this stays at rest.
type pathFilter struct {
	open bool // the box was asked for (the icon, Ctrl+F) and has not been given up
	seen bool // it has held the keyboard since: losing it again, empty, closes it
	want int  // frames left in which it still takes the keyboard it was asked for
	esc  bool // Esc was pressed in it empty: it folds even though it still has the keyboard
}

// filterWantFrames is how many frames an opened filter box asks for the
// keyboard: the request lands a frame after the box is built.
const filterWantFrames = 3

// openFilter is the filter command: it shows the filter box, also when the
// compact path bar had folded it into an icon, and gives it the keyboard.
func (a *app) openFilter() {
	a.filter = pathFilter{open: true, want: filterWantFrames}
	a.searchH.Focus()
}

// filterShown works out whether the filter is a box in this frame. A compact
// path bar folds it into an icon so that the breadcrumb keeps the room; the box
// stays while it has the keyboard or holds text, and folds again once it has
// lost the keyboard while empty.
func (a *app) filterShown(c *ui.Context) bool {
	f := &a.filter
	if !a.lay.compact {
		*f = pathFilter{}
		return true
	}
	if f.esc {
		// The box that had the keyboard is gone this frame, and the keyboard with it.
		*f = pathFilter{}
		return a.search != ""
	}
	focused := a.searchH.FocusWithin(c)
	switch {
	case focused:
		f.seen, f.want = true, 0
	case f.open && f.want == 0 && f.seen && a.search == "":
		*f = pathFilter{}
	}
	return f.open || focused || a.search != ""
}

// folderFilter is the filter of the folder on the right of the path bar: a
// search box, or, in a compact bar, a 28px icon button that unfolds it.
func (a *app) folderFilter(c *ui.Context, width float32) {
	if !a.filterShown(c) {
		if ghostButton(c, icons.Get("search"), "", locales.PathbarFilterThisFolder(), false).Label(locales.PathbarFilterFolder()).Clicked() {
			a.openFilter()
		}
		return
	}
	// A box the compact bar unfolded keeps its width; the breadcrumb gives way.
	in := searchBoxFit(c, &a.search, &a.searchH, locales.PathbarFilterFolder(), locales.PathbarFilterThisFolder(), width, a.lay.compact, icons.Get("search"), icons.Get("x"))
	if in.Changed() {
		a.rebuildRows()
	}
	f := &a.filter
	if f.want > 0 {
		f.want--
		a.searchH.Focus()
	}
	// Esc in an empty box gives it up; while the bar is wide the box stays.
	a.searchH.OnShortcut(c, 0, ui.KeyEscape, func() {
		if a.lay.compact && a.search == "" {
			f.esc = true
		}
	})
}

// deletedToggle is the "Show deleted" switch right of the search box.
func (a *app) deletedToggle(c *ui.Context) {
	known, versioned := false, true
	if st, ok := a.versioningOf(a.accountID, a.scope.Bucket); ok {
		known, versioned = true, st == "Enabled" || st == "Suspended"
	}
	tip := locales.PathbarShowDeletedTip()
	if known && !versioned {
		tip = locales.PathbarNoVersionsTip()
	}
	if ghostToggle(c, icons.Get("trash-2"), locales.PathbarShowDeleted(), tip, a.mg.del.on, known && !versioned, a.lay.compact).Clicked() {
		a.toggleDeleted()
	}
}
