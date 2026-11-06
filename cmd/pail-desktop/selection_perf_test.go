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
