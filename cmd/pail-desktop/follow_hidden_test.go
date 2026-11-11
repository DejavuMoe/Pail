package main

import (
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// While the window is hidden in the tray the toolkit draws no frames (the
// first frame aside), but Window.Update still runs the drain. The follow-ups
// of finished tasks (notifications, list refresh) must run from that turn, not
// wait for a frame that comes when the window is shown again.
func TestFollowRunsWhileWindowHidden(t *testing.T) {
	h := newHarness(t, "light")
	a := h.a
	hidden := true
	a.tray.visible = func() bool { return !hidden }
	var notes []string
	a.tray.notify = func(msg string) { notes = append(notes, msg) }

	called := 0
	a.follow("fx-hidden", func(Task) {
		called++
		a.toast("Bucket emptied")
	})
	a.onEvent(core.Event{Task: Task{ID: "fx-hidden", Kind: core.KindOther, Status: core.StatusRunning}})
	a.uiTurn() // what Window.Update runs; no frame is drawn
	if called != 0 {
		t.Fatalf("the follow-up ran before the task finished")
	}
	a.onEvent(core.Event{Task: Task{ID: "fx-hidden", Kind: core.KindOther, Status: core.StatusSucceeded}})
	a.uiTurn()
	if called != 1 {
		t.Fatalf("the follow-up of a task that finished while the window was hidden ran %d times, want 1", called)
	}
	if len(notes) != 1 || notes[0] != "Bucket emptied" || len(a.toasts) != 0 {
		t.Fatalf("notifications = %v, queued toasts = %d; want one system notification", notes, len(a.toasts))
	}
	a.uiTurn()
	if called != 1 {
		t.Fatalf("the follow-up ran again: %d", called)
	}
}

// With the window shown the follow-up still runs once, and its toast is
// the in-window toast.
func TestFollowRunsOnceWhileWindowShown(t *testing.T) {
	h := newHarness(t, "light")
	a := h.a
	var notes []string
	a.tray.notify = func(msg string) { notes = append(notes, msg) }
	called := 0
	a.follow("fx-shown", func(Task) { called++; a.toast("Renamed") })
	a.onEvent(core.Event{Task: Task{ID: "fx-shown", Kind: core.KindOther, Status: core.StatusSucceeded}})
	a.uiTurn()
	h.settle() // frames: mgFrame polls as well
	if called != 1 {
		t.Fatalf("follow-up ran %d times, want 1", called)
	}
	if len(notes) != 0 {
		t.Fatalf("a shown window got system notifications: %v", notes)
	}
}
