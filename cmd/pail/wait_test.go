package main

import (
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

func TestDrainWaitsForFreshFinishedSnapshot(t *testing.T) {
	finished := app.Task{ID: "scan", GroupID: "group", Status: app.StatusSucceeded}
	w := &watcher{recs: map[string]*rec{
		"scan": {task: finished, done: true, printed: true},
	}}

	// The scan was running when Tasks took its snapshot, but its completion
	// event arrived before drain. It may have submitted more work meanwhile.
	stale := finished
	stale.Status = app.StatusRunning
	if w.drain("group", []app.Task{stale}) {
		t.Fatal("accepted a stale snapshot of the running scan")
	}

	child := app.Task{ID: "upload", GroupID: "group", Status: app.StatusRunning}
	if w.drain("group", []app.Task{finished, child}) {
		t.Fatal("accepted a snapshot with an unfinished upload")
	}
	child.Status = app.StatusSucceeded
	w.recs[child.ID] = &rec{task: child, done: true, printed: true}
	if !w.drain("group", []app.Task{finished, child}) {
		t.Fatal("did not accept completed scan and upload")
	}
}
