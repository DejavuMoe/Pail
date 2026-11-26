package main

import (
	"reflect"
	"slices"

	"github.com/egoist/mygo/ui"
)

// selectedKeys returns the selected keys that are listed, in table order. The
// slice is shared until the choice or the rows change: read it, do not change
// it. Asked twelve times a frame by the commands' Enabled functions, the
// selection bar and the inspector, it is built once per change.
func (a *app) selectedKeys() []string {
	a.sel.sync()
	if c := &a.vc; c.keysOK && c.keysSel == a.sel.gen && c.keysRows == a.rowsGen && c.keysN == len(a.rows) {
		return c.keys
	}
	out := a.buildSelectedKeys()
	a.vc.keys, a.vc.keysSel, a.vc.keysRows, a.vc.keysN, a.vc.keysOK = out, a.sel.gen, a.rowsGen, len(a.rows), true
	return out
}

func (a *app) buildSelectedKeys() []string {
	a.vc.builds.keys++
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
	// stamp is mygo's own counter of the choice at the last sync.
	stamp uint64
}

func (s *selection) Add(k string)    { s.Selection.Add(k); s.touch() }
func (s *selection) Remove(k string) { s.Selection.Remove(k); s.touch() }
func (s *selection) Clear()          { s.Selection.Clear(); s.touch() }

// touch marks the choice as possibly changed: by mygo, or by new rows.
func (s *selection) touch() { s.gen++; s.stamp, _ = selStamp(&s.Selection) }

// sync notices a change mygo's table made to the embedded Selection (a click,
// the keyboard, Ctrl+A, a drag) without going through Add, Remove or Clear.
// mygo gives every change of a Selection's keys a new stamp, which is what it
// compares itself; reading it takes a few nanoseconds, so the frame asks
// instead of assuming a change, and what is derived from the choice survives
// the frames in which nothing happened. Where the stamp cannot be read (a mygo
// that dropped the field) every sync counts as a change, as it did before.
func (s *selection) sync() {
	if st, ok := selStamp(&s.Selection); st != s.stamp || !ok {
		s.gen++
		s.stamp = st
	}
}

// stampField is the index of ui.Selection's unexported stamp; -1 when mygo has none.
var stampField = func() int {
	f, ok := reflect.TypeFor[ui.Selection[string]]().FieldByName("stamp")
	if !ok || f.Type.Kind() != reflect.Uint64 {
		return -1
	}
	return f.Index[0]
}()

// selStamp reads mygo's change counter of s; ok is false if there is none.
func selStamp(s *ui.Selection[string]) (stamp uint64, ok bool) {
	if stampField < 0 {
		return 0, false
	}
	return reflect.ValueOf(s).Elem().Field(stampField).Uint(), true
}

// selSummary is what one pass over the selection counts. A Ctrl+A over a
// million rows makes that pass take a few hundred milliseconds, and the
// command states, the table, the inspector, the selection bar and the status
// bar all ask in one frame: they share one pass while sel.gen stays put.
type selSummary struct {
	gen, rows      uint64
	ok             bool
	folders, files int
	bytes          int64
}

func (a *app) selSummary() selSummary {
	a.sel.sync()
	if s := a.selSum; s.ok && s.gen == a.sel.gen && s.rows == a.rowsGen {
		return s
	}
	a.vc.builds.selSum++
	s := selSummary{gen: a.sel.gen, rows: a.rowsGen, ok: true}
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

// folderSummary totals the loaded listing; the inspector asks every frame while
// nothing is chosen, so the totals are kept until the listing changes.
func (a *app) folderSummary() (folders, files int, bytes int64) {
	if c := &a.vc; c.sumOK && c.sumRaw == a.rawGen && c.sumN == len(a.raw) {
		return c.sumFolders, c.sumFiles, c.sumBytes
	}
	a.vc.builds.folderSum++
	defer func() {
		a.vc.sumFolders, a.vc.sumFiles, a.vc.sumBytes = folders, files, bytes
		a.vc.sumRaw, a.vc.sumN, a.vc.sumOK = a.rawGen, len(a.raw), true
	}()
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
