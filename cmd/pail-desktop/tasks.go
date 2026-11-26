package main

import (
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

func (a *app) setTasks(ts []Task) {
	a.tasks = append(a.tasks[:0], ts...)
	a.reindexTasks()
	a.refreshTransfers()
}

func (a *app) reindexTasks() {
	a.reindexes++
	clear(a.taskIdx)
	for i, t := range a.tasks {
		a.taskIdx[t.ID] = i
	}
}

// progressEvery bounds how often a batch of progress-only events reaches the
// UI thread: eight running transfers report about ten times a second each, and
// every apply costs a rebuild of the transfer view and a frame.
const progressEvery = 100 * time.Millisecond

// onEvent is the Service subscription callback; it runs off the UI thread.
//
// An event that only moves a running task's numbers is progress: it waits for
// the next apply slot (at most one per progressEvery for the whole window; a
// timer takes the last state to the UI thread, so none is lost). Anything else
// (a task that was queued, paused, finished or dropped) is applied at once, so
// that completions, cancels and everything the user waits on are not delayed.
func (a *app) onEvent(e core.Event) {
	in := &a.inbox
	progress := !e.Removed && e.Task.Status == core.StatusRunning
	in.mu.Lock()
	in.events = append(in.events, e)
	if !progress {
		in.urgent = true
	}
	post, quiet := false, false
	var wait time.Duration
	switch {
	case in.scheduled:
		// an apply is on its way and takes this event too
	case !progress:
		post = true
	case time.Since(in.lastApply) >= progressEvery:
		post, quiet = true, true
	case !in.timerSet:
		in.timerSet = true
		wait = progressEvery - time.Since(in.lastApply)
	}
	if post {
		in.scheduled = true
	}
	in.mu.Unlock()
	switch {
	case post && quiet:
		a.postProgress(a.applyInbox)
	case post:
		a.post(a.applyInbox)
	case wait > 0:
		time.AfterFunc(wait, a.inboxTimer)
	}
}

// inboxTimer brings the progress that waited for its slot to the UI thread.
func (a *app) inboxTimer() {
	in := &a.inbox
	in.mu.Lock()
	in.timerSet = false
	post := len(in.events) > 0 && !in.scheduled
	quiet := post && !in.urgent
	if post {
		in.scheduled = true
	}
	in.mu.Unlock()
	switch {
	case post && quiet:
		a.postProgress(a.applyInbox)
	case post:
		a.post(a.applyInbox)
	}
}

// postProgress queues progress for the UI thread. Nothing on screen shows it
// while the transfer list is closed (the status bar and the strip count files,
// not bytes; the taskbar and the tray are set from the drain itself), so it
// then asks for no frame (the real window's quiet wake-up, see main).
func (a *app) postProgress(fn func()) {
	if a.listShowsProgress.Load() || a.postQuiet == nil {
		a.post(fn)
		return
	}
	a.postQuiet(fn)
}

// applyInbox folds the queued task events into the task list and settles the
// watches of the groups and tasks they touched.
func (a *app) applyInbox() {
	in := &a.inbox
	in.mu.Lock()
	evs := in.events
	in.events, in.scheduled, in.urgent = nil, false, false
	if len(evs) > 0 {
		in.lastApply = time.Now()
	}
	in.mu.Unlock()
	if len(evs) == 0 {
		return
	}
	a.applies++
	touched := map[string]bool{}
	var gone map[int]bool // positions of the tasks dropped in this batch
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
			delete(a.retryWatches, t.ID)
			if ok {
				if gone == nil {
					gone = map[int]bool{}
				}
				gone[i] = true
				delete(a.taskIdx, t.ID)
			}
		case ok:
			a.tasks[i] = t
		default:
			a.taskIdx[t.ID] = len(a.tasks)
			a.tasks = append(a.tasks, t)
		}
	}
	if len(gone) > 0 {
		a.compactTasks(gone)
	}
	for k := range touched {
		if a.watches[k] != nil {
			a.checkWatch(k)
		}
	}
	a.syncKeepRunning()
	a.refreshTransfers()
	if len(gone) > 0 && len(a.retryWatches) > 0 {
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

// compactTasks drops the tasks at the positions in gone in one pass over the
// list, from the first of them on, and gives the tasks that moved their new
// positions. (Dropping one by one would move the tail, and index it again,
// for every task: "Clear finished" is a thousand of them.)
func (a *app) compactTasks(gone map[int]bool) {
	first := len(a.tasks)
	for i := range gone {
		first = min(first, i)
	}
	w := first
	for i := first; i < len(a.tasks); i++ {
		if gone[i] {
			continue
		}
		a.tasks[w] = a.tasks[i]
		a.taskIdx[a.tasks[w].ID] = w
		w++
	}
	clear(a.tasks[w:])
	a.tasks = a.tasks[:w]
}

// syncKeepRunning holds a Power.KeepRunning request while any task is queued
// or running, so transfers keep their speed when the window is hidden. A paused
// task does no work and does not hold it.
func (a *app) syncKeepRunning() {
	if a.keepRunning == nil {
		return
	}
	_, busy := busyTasks(a.tasks)
	switch {
	case busy && a.runRelease == nil:
		a.runRelease = a.keepRunning(locales.PowerTransferring())
	case !busy && a.runRelease != nil:
		a.runRelease()
		a.runRelease = nil
	}
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

// pauseGroup, resumeGroup and cancelGroup are the buttons of a group's row. A
// cancel asks nothing, like a single task's: what was transferred already stays,
// and the same files can be sent again.
func (a *app) pauseGroup(id string) {
	if err := a.svc.PauseGroup(id); err != nil {
		a.toast(locales.TransfersCouldNotPause(humanError(err)))
	}
}

func (a *app) resumeGroup(id string) {
	if err := a.svc.ResumeGroup(id); err != nil {
		a.toast(locales.TransfersCouldNotResume(humanError(err)))
	}
}

func (a *app) cancelGroup(id string) {
	if err := a.svc.CancelGroup(id); err != nil {
		a.toast(locales.StateCouldNotCancel(humanError(err)))
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
