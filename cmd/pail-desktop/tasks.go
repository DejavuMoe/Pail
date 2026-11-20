package main

import (
	"slices"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

func (a *app) setTasks(ts []Task) {
	a.tasks = append(a.tasks[:0], ts...)
	a.reindexTasks()
}

func (a *app) reindexTasks() {
	clear(a.taskIdx)
	for i, t := range a.tasks {
		a.taskIdx[t.ID] = i
	}
}

// onEvent is the Service subscription callback; it runs off the UI thread.
func (a *app) onEvent(e core.Event) {
	in := &a.inbox
	in.mu.Lock()
	in.events = append(in.events, e)
	schedule := !in.scheduled
	in.scheduled = true
	in.mu.Unlock()
	if schedule {
		a.post(a.applyInbox)
	}
}

// applyInbox folds the queued task events into the task list and settles the
// watches of the groups and tasks they touched.
func (a *app) applyInbox() {
	in := &a.inbox
	in.mu.Lock()
	evs := in.events
	in.events, in.scheduled = nil, false
	in.mu.Unlock()
	if len(evs) == 0 {
		return
	}
	touched := map[string]bool{}
	removed := false
	for _, e := range evs {
		t := e.Task
		touched[t.ID] = true
		if t.GroupID != "" {
			touched[t.GroupID] = true
			// A fast rescan can leave retained history before retryTask finds its group.
			if w := a.watches[t.ID]; w != nil && t.Kind == core.KindOther {
				delete(a.watches, t.ID)
				a.watches[t.GroupID], a.retryWatches[t.GroupID] = w, w
			}
		}
		i, ok := a.taskIdx[t.ID]
		switch {
		case e.Removed:
			removed = true
			delete(a.retryWatches, t.ID)
			if ok {
				a.tasks = slices.Delete(a.tasks, i, i+1)
				a.reindexTasks()
			}
		case ok:
			a.tasks[i] = t
		default:
			a.taskIdx[t.ID] = len(a.tasks)
			a.tasks = append(a.tasks, t)
		}
	}
	for k := range touched {
		if a.watches[k] != nil {
			a.checkWatch(k)
		}
	}
	a.syncKeepRunning()
	if removed {
		retained := map[string]bool{}
		for _, t := range a.tasks {
			retained[t.ID], retained[t.GroupID] = true, true
		}
		for key := range a.retryWatches {
			if !retained[key] {
				delete(a.retryWatches, key)
			}
		}
	}
}

// syncKeepRunning holds a Power.KeepRunning request while any task is queued
// or running, so transfers keep their speed when the window is hidden.
func (a *app) syncKeepRunning() {
	if a.keepRunning == nil {
		return
	}
	busy := slices.ContainsFunc(a.tasks, func(t Task) bool {
		return t.Status == core.StatusQueued || t.Status == core.StatusRunning
	})
	switch {
	case busy && a.runRelease == nil:
		a.runRelease = a.keepRunning(locales.PowerTransferring())
	case !busy && a.runRelease != nil:
		a.runRelease()
		a.runRelease = nil
	}
}

// taskCounts totals the tasks of the three panel tabs.
func countTasks(ts []Task) (n [3]int) {
	for _, t := range ts {
		n[taskGroup(t)]++
	}
	return
}

// pauseTask and resumeTask are the panel's pause and resume buttons.
func (a *app) pauseTask(id string) {
	if err := a.svc.Pause(id); err != nil {
		a.toast(locales.TransfersCouldNotPause(humanError(err)))
	}
}

func (a *app) resumeTask(id string) {
	if err := a.svc.Resume(id); err != nil {
		a.toast(locales.TransfersCouldNotResume(humanError(err)))
	}
}

// cancelTask and retryTask are the panel's buttons.
func (a *app) cancelTask(id string) {
	if err := a.svc.Cancel(id); err != nil {
		a.toast(locales.StateCouldNotCancel(humanError(err)))
	}
}

func (a *app) retryTask(id string) {
	var original Task
	for _, t := range a.svc.Tasks() {
		if t.ID == id {
			original = t
		}
	}
	w := a.retryWatches[id]
	if w == nil {
		w = a.retryWatches[original.GroupID]
	}
	next, err := a.svc.Retry(id)
	if err != nil {
		a.toast(locales.StateCouldNotRetry(humanError(err)))
		return
	}
	if w != nil {
		key := next
		for _, t := range a.svc.Tasks() {
			if t.ID == next && t.GroupID != "" && t.GroupID != original.GroupID {
				key = t.GroupID // a rescan owns a fresh group, including its new transfers
				break
			}
		}
		a.addWatch(key, w)
	}
	a.transTab = 0
}

func (a *app) clearFinished() { a.svc.ClearFinished() }
