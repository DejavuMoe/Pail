package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The flat view lists every object below the open folder, recursively, with
// the path relative to that folder as its name. Folders are not shown, so the
// selection and every action work on the listed objects.

// flatEntries turns a page of a delimiter-less listing into table rows: the
// folder placeholders go, and each name becomes the key below prefix.
func flatEntries(in []core.Entry, prefix string) []core.Entry {
	out := in[:0:0]
	for _, e := range in {
		if e.Folder {
			continue
		}
		e.Name = strings.TrimPrefix(e.Key, prefix)
		out = append(out, e)
	}
	return out
}

// toggleFlat switches the listing between the folder view and the flat one.
func (a *app) toggleFlat() {
	if !a.browsing() || a.mg.del.on {
		return
	}
	a.objOps.flat = !a.objOps.flat
	a.mg.ren = renameState{}
	a.sel.Clear()
	a.cursor = -1
	a.resetHead()
	a.reload()
}

// flatToggle is the "Flat" toggle of the path bar.
func (a *app) flatToggle(c *ui.Context) {
	tip := locales.FlatListTip()
	if a.mg.del.on {
		tip = locales.FlatListUnavailable()
	}
	if ghostToggle(c, icons.Get("layers"), locales.FlatListLabel(), tip, a.objOps.flat, a.mg.del.on, a.lay.compact).Clicked() {
		a.toggleFlat()
	}
}
