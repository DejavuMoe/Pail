package main

import (
	"fmt"
	"testing"
)

func TestSoleSelected(t *testing.T) {
	a := benchApp(5)
	if _, one := a.soleSelected(); one {
		t.Fatal("five chosen rows reported as one")
	}
	a.sel.Clear()
	if _, one := a.soleSelected(); one {
		t.Fatal("no chosen row reported as one")
	}
	a.sel.Add("folder/report-00000003.json")
	a.sel.Add("gone-since-listing") // a key that is no longer listed does not count
	if k, one := a.soleSelected(); !one || k != "folder/report-00000003.json" {
		t.Fatalf("soleSelected = %q, %v", k, one)
	}
	if a.selectedCount() != 1 || len(a.selectedKeys()) != 1 {
		t.Fatal("count and keys disagree with soleSelected")
	}
}

// A selection signature tells selections apart, whatever their size.
func TestSelectionSigDistinguishes(t *testing.T) {
	for _, n := range []int{3, sigExactMax, sigExactMax + 1, 500} {
		a := benchApp(n + 2)
		a.sel.Clear()
		for i := 0; i < n; i++ {
			a.sel.Add(a.rows[i].Key)
		}
		base := a.selectionSig()
		if again := a.selectionSig(); again != base {
			t.Fatalf("n=%d: the signature is not stable", n)
		}
		a.sel.Add(a.rows[n].Key) // one more row
		more := a.selectionSig()
		a.sel.Remove(a.rows[n].Key)
		a.sel.Remove(a.rows[0].Key)
		a.sel.Add(a.rows[n+1].Key) // the same number of rows, one of them another
		swapped := a.selectionSig()
		if base == more || base == swapped || more == swapped {
			t.Fatalf("n=%d: signatures collide (%q)", n, fmt.Sprint(base == more, base == swapped, more == swapped))
		}
	}
}

// The shared selection summary follows every change: through sel, a search
// that hides chosen rows, and a touch for what mygo's table does on its own.
func TestSelectionSummaryFollowsChanges(t *testing.T) {
	a := benchApp(6) // all six chosen; even sizes are 0, 2, 4
	check := func(when string, files int, bytes int64) {
		t.Helper()
		if f, n, b := a.selectedInfo(); f != 0 || n != files || b != bytes || a.selectedCount() != files {
			t.Fatalf("%s: info = %d folders, %d files, %d bytes, count %d; want %d files, %d bytes", when, f, n, b, a.selectedCount(), files, bytes)
		}
	}
	check("all", 6, 15)
	a.sel.Remove(a.rows[5].Key)
	check("remove", 5, 10)
	a.sel.Add(a.rows[5].Key)
	check("add", 6, 15)
	a.search = "0003"
	a.rebuildRows()
	check("search hides the rest", 1, 3)
	a.search = ""
	a.rebuildRows()
	check("search cleared", 6, 15)
	a.sel.Selection.Remove(a.rows[0].Key) // as mygo does, past the counter
	a.sel.touch()
	check("touched", 5, 15)
	a.sel.Clear()
	check("clear", 0, 0)
}

// One frame with every row chosen: the command states, the table, the
// inspector, the selection bar and the status bar each ask.
func BenchmarkSelectionFrameReads(b *testing.B) {
	for _, n := range []int{10000, 1000000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := benchApp(n)
			b.ReportAllocs()
			for b.Loop() {
				a.sel.touch() // the frame starts
				for range 3 {
					if a.selectedCount() != n {
						b.Fatal("wrong count")
					}
				}
				for range 3 {
					if _, files, _ := a.selectedInfo(); files != n {
						b.Fatal("wrong info")
					}
				}
			}
		})
	}
}

func BenchmarkSoleSelected(b *testing.B) {
	a := benchApp(10000)
	b.ReportAllocs()
	for b.Loop() {
		if _, one := a.soleSelected(); one {
			b.Fatal("many rows are chosen")
		}
	}
}

func BenchmarkSelectionSig(b *testing.B) {
	a := benchApp(10000)
	b.ReportAllocs()
	for b.Loop() {
		_ = a.selectionSig()
	}
}
