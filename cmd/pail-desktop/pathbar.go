package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// pathBar is the breadcrumb trail on the left and the folder filter on the
// right (40px high).
func (a *app) pathBar(c *ui.Context) {
	k := tk(c)
	parts := splitPrefix(a.scope.Prefix)
	items := append([]string{a.scope.Bucket}, parts...)
	last := len(items) - 1
	// The filter gives way to the breadcrumb: about a third of the bar, 140-240px.
	searchW := min(240, max(140, a.lay.centerW*0.3))
	ui.Row(c).Height(40).Padding(0, 12).Gap(12).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Border).Label(locales.PathbarPath()).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("database")).FontSize(14).TextColor(k.TextMuted).MarginX(4)
			for i, name := range items {
				if i > 0 {
					ui.Icon(c, icons.Get("chevron-right")).FontSize(12).TextColor(k.TextSubtle)
				}
				seg := ui.ButtonBase(c.Key(i)).Label(name).Height(24).Padding(0, 6).Radius(6).Center().Shrink(1).MinWidth(0).Tooltip(name).Transition(hoverFade())
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
					ui.Text(c, name).FontSize(sizeBody).FontWeight(w).TextColor(col).SingleLine()
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
		if searchBox(c, &a.search, &a.searchH, locales.PathbarFilterFolder(), locales.PathbarFilterThisFolder(), searchW, icons.Get("search"), icons.Get("x")).Changed() {
			a.rebuildRows()
		}
		a.flatToggle(c)
		a.deletedToggle(c)
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
