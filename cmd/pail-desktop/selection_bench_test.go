package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// benchApp lists n objects and selects all of them, as Ctrl+A does.
func benchApp(n int) *app {
	a := &app{sort: ui.SortOrder{Column: "name"}}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("folder/report-%08d.json", i)
		a.raw = append(a.raw, Entry{Key: name, Name: name, Size: int64(i)})
	}
	a.rebuildRows()
	for _, e := range a.rows {
		a.sel.Add(e.Key)
	}
	return a
}

// selectedKeys runs about a dozen times in one frame (the Enabled funcs of the
// commands, the selection bar, the inspector), so what one call costs with many
// rows chosen is what every frame pays.
func BenchmarkSelectedKeys(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := benchApp(n)
			b.ReportAllocs()
			for b.Loop() {
				if len(a.selectedKeys()) != n {
					b.Fatal("wrong selection")
				}
			}
		})
	}
}

// selectedCount is the cheaper question most callers ask.
func BenchmarkSelectedCount(b *testing.B) {
	a := benchApp(10000)
	b.ReportAllocs()
	for b.Loop() {
		if a.selectedCount() != 10000 {
			b.Fatal("wrong selection")
		}
	}
}
