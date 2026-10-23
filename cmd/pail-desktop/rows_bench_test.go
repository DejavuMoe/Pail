package main

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

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
