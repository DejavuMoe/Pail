package main

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

// selectedKeys returns the selected keys that are listed, in table order.
func (a *app) selectedKeys() []string {
	var out []string
	for k := range a.sel.All() {
		if _, ok := a.byKey[k]; ok {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(x, y string) int { return a.byKey[x] - a.byKey[y] })
	return out
}

// soleSelected is the key of the one listed row that is chosen; ok is false when
// none or several are. It stops reading the selection at the second row, so the
// per-frame questions "is exactly one row chosen" do not grow with a Ctrl+A.
func (a *app) soleSelected() (key string, ok bool) {
	n := 0
	for k := range a.sel.All() {
		if _, listed := a.byKey[k]; !listed {
			continue
		}
		if n++; n > 1 {
			return "", false
		}
		key = k
	}
	return key, n == 1
}

// selection is the object table's choice with a counter that moves on every
// change made through it. mygo's table changes the embedded Selection without
// it, so the frame (view), the table's build and ExtendTo call touch as well.
type selection struct {
	ui.Selection[string]
	gen uint64
}

func (s *selection) Add(k string)    { s.Selection.Add(k); s.gen++ }
func (s *selection) Remove(k string) { s.Selection.Remove(k); s.gen++ }
func (s *selection) Clear()          { s.Selection.Clear(); s.gen++ }

// touch marks the choice as possibly changed: by mygo, or by new rows.
func (s *selection) touch() { s.gen++ }

// selSummary is what one pass over the selection counts. A Ctrl+A over a
// million rows makes that pass take a few hundred milliseconds, and the
// command states, the table, the inspector, the selection bar and the status
// bar all ask in one frame: they share one pass while sel.gen stays put.
type selSummary struct {
	gen            uint64
	ok             bool
	folders, files int
	bytes          int64
}

func (a *app) selSummary() selSummary {
	if s := a.selSum; s.ok && s.gen == a.sel.gen {
		return s
	}
	s := selSummary{gen: a.sel.gen, ok: true}
	for k := range a.sel.All() {
		i, ok := a.byKey[k]
		if !ok {
			continue
		}
		if e := a.rows[i]; e.Folder {
			s.folders++
		} else {
			s.files++
			s.bytes += e.Size
		}
	}
	a.selSum = s
	return s
}

// selectedCount is how many listed rows are chosen: len(selectedKeys()),
// without sorting them.
func (a *app) selectedCount() int {
	s := a.selSummary()
	return s.folders + s.files
}

// selectedInfo summarises the selection: counts and total object bytes.
func (a *app) selectedInfo() (folders, files int, bytes int64) {
	s := a.selSummary()
	return s.folders, s.files, s.bytes
}

// setSingle selects exactly one key.
func (a *app) setSingle(key string) {
	a.sel.Clear()
	a.sel.Add(key)
	a.cursor = -1
	if i, ok := a.byKey[key]; ok {
		a.cursor = i
	}
}

// folderSummary totals the loaded listing.
func (a *app) folderSummary() (folders, files int, bytes int64) {
	for _, e := range a.raw {
		if e.Folder {
			folders++
		} else {
			files++
			bytes += e.Size
		}
	}
	return
}
