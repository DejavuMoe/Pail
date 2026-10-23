package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func FuzzCompareNames(f *testing.F) {
	for _, pair := range [][2]string{{"Report-01", "report-02"}, {"ALPHA", "alpha"}, {"K", "k"}, {"İ", "i"}, {"Σ", "ς"}, {"报告", "报告A"}, {"a", "AK"}, {"", "x"}, {"\xff", "\xfe"}} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		want := strings.Compare(strings.ToLower(a), strings.ToLower(b))
		if got := compareNames(a, b); got != want {
			t.Fatalf("compareNames(%q,%q)=%d want=%d", a, b, got, want)
		}
	})
}

func TestRebuildRowsPreservesNamesAndStableTies(t *testing.T) {
	a := &app{sort: ui.SortOrder{Column: "name"}, raw: []Entry{
		{Key: "z", Name: "Zoo", Size: 9},
		{Key: "k1", Name: "K", Size: 2},
		{Key: "a1", Name: "ALPHA", Size: 2},
		{Key: "dir", Name: "Zulu", Folder: true},
		{Key: "k2", Name: "k", Size: 2},
		{Key: "a2", Name: "alpha", Size: 2},
		{Key: "cjk", Name: "报告", Size: 3},
	}}
	raw := slices.Clone(a.raw)
	check := func(want ...string) {
		t.Helper()
		a.rebuildRows()
		var got []string
		for i, e := range a.rows {
			got = append(got, e.Key)
			if a.byKey[e.Key] != i {
				t.Fatalf("bad row index for %q", e.Key)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("rows=%v want=%v", got, want)
		}
		if !reflect.DeepEqual(a.raw, raw) {
			t.Fatal("sorting modified the source entries")
		}
	}
	check("dir", "a1", "a2", "k1", "k2", "z", "cjk")
	a.sort.Descending = true
	check("dir", "cjk", "z", "k1", "k2", "a1", "a2")
	a.search = "  ALpHa  "
	check("a1", "a2")
	a.search = "K"
	check("k1", "k2")
	a.search = ""
	a.sort.Column = "size"
	check("dir", "z", "cjk", "a1", "a2", "k1", "k2")
	a.raw = []Entry{{Key: "new", Name: "ALPHA"}}
	raw = slices.Clone(a.raw)
	check("new")
}
