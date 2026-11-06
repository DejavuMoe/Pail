package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRowsDueThrottlesOnlyWhileLoading(t *testing.T) {
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
	a := &app{post: func(func()) {}, loading: true}
	a.rebuildRows()
	a.sort = ui.SortOrder{Column: "size"}
	if !a.rowsDue() {
		t.Fatal("a sort change waits for the interval while loading")
	}
}
