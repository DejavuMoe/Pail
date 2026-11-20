package main

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	t.Parallel()
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

func TestRowsDueThrottlesOnlyWhileLoading(t *testing.T) {
	t.Parallel()
	a := &app{post: func(func()) {}} // the wake-up timer finds nobody to wake
	a.rowsDirty = true

	if !a.rowsDue() { // never rebuilt: the first page shows at once
		t.Fatal("the first rows wait")
	}
	a.rebuildRows()
	if a.rowsDue() {
		t.Fatal("clean rows are due")
	}

	a.loading, a.rowsDirty = true, true
	if a.rowsDue() {
		t.Fatal("a page that arrived right after a rebuild rebuilds again")
	}
	if !a.rebuildWake {
		t.Fatal("no wake-up is set for the skipped rebuild")
	}
	a.rebuiltAt = time.Now().Add(-2 * rebuildEvery)
	if !a.rowsDue() {
		t.Fatal("rows are still held back after the interval")
	}

	a.rebuildRows()
	a.rowsDirty = true
	a.loading = false // the listing ended
	if !a.rowsDue() {
		t.Fatal("rows of a finished listing wait")
	}
}

func TestRowsDueSortChangeIsImmediate(t *testing.T) {
	t.Parallel()
	a := &app{post: func(func()) {}, loading: true}
	a.rebuildRows()
	a.sort = ui.SortOrder{Column: "size"}
	if !a.rowsDue() {
		t.Fatal("a sort change waits for the interval while loading")
	}
}

func BenchmarkRebuildRows(b *testing.B) {
	for _, lowercase := range []bool{false, true} {
		for _, n := range []int{1000, 10000} {
			b.Run(fmt.Sprintf("%d/lowercase=%t", n, lowercase), func(b *testing.B) {
				a := &app{sort: ui.SortOrder{Column: "name"}}
				rng := rand.New(rand.NewPCG(7, 42))
				for i := 0; i < n; i++ {
					name := fmt.Sprintf("Report-%08d-%08d.json", rng.Uint32(), i)
					if lowercase {
						name = strings.ToLower(name)
					}
					a.raw = append(a.raw, Entry{Key: name, Name: name})
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					a.sort.Descending = !a.sort.Descending
					a.rebuildRows()
				}
			})
		}
	}
}
