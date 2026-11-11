package app

import (
	"context"
	"errors"
	"os"
	"sort"
)

// Pausing and resuming transfers.
//
// Only per-file uploads and downloads (tasks with a savedTransfer) can be
// paused. A queued task leaves its queue; a running one is interrupted like a
// cancel, cleans up the same way (the multipart upload is aborted, the
// temporary download file removed) and then waits as Paused. Resume puts it at
// the end of its queue and the file starts again from the beginning: an
// interrupted transfer is never half published. Pausing a group also pauses
// the files its scan submits later, and the scan itself waits at its backlog.
//
// SaveTransfers writes the unfinished per-file transfers to transfers.json;
// RestoreTransfers submits them again, paused, in a later session.

// savedTransfer is the serializable description of one per-file transfer.
type savedTransfer struct {
	Upload   *uploadJob   `json:"upload,omitempty"`
	Download *downloadJob `json:"download,omitempty"`
}

// transfersFile is the name of the saved transfer list in the config directory.
const transfersFile = "transfers.json"

// transfersFormat is the version of transfers.json.
const transfersFormat = 1

type savedTransfers struct {
	Format int              `json:"format"`
	Groups []savedTransferG `json:"groups"`
}

type savedTransferG struct {
	Jobs []savedTransfer `json:"jobs"`
}

// Pausable reports whether Pause accepts id: a per-file transfer that is
// queued or running, or a group with such a task.
func (s *Service) Pausable(id string) bool {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range s.membersLocked(id) {
		if t.spec.Saved != nil && (t.pub.Status == StatusQueued || t.pub.Status == StatusRunning) {
			return true
		}
	}
	return false
}

// membersLocked returns the task id or the members of group id, in
// submission order. Caller holds q.mu.
func (s *Service) membersLocked(id string) []*task {
	if t := s.q.tasks[id]; t != nil {
		return []*task{t}
	}
	var ts []*task
	for _, t := range s.q.tasks {
		if t.pub.GroupID == id {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	return ts
}

// Pause pauses a task or every transfer of a group. Tasks that are not
// per-file transfers (scans, bulk operations) keep running. Pausing a group
// also pauses the transfers it submits later, until Resume or CancelGroup.
func (s *Service) Pause(id string) error {
	const op = "Pause"
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	members := s.membersLocked(id)
	if len(members) == 0 {
		return opErr(op, ErrNotFound, "no task or group %q", id)
	}
	if q.tasks[id] == nil {
		q.pausedGroups[id] = struct{}{}
	}
	for _, t := range members {
		s.pauseLocked(t)
	}
	return nil
}

func (s *Service) pauseLocked(t *task) {
	if t.spec.Saved == nil {
		return
	}
	switch t.pub.Status {
	case StatusQueued:
		i := kindIndex(t.pub.Kind)
		qq := s.q.queued[i]
		for j, x := range qq {
			if x == t {
				s.q.queued[i] = append(qq[:j:j], qq[j+1:]...)
				break
			}
		}
		t.pub.Status = StatusPaused
		s.notifyStateLocked()
		s.emitLocked(t, false)
	case StatusRunning:
		t.pausing, t.resumeAfter = true, false
		t.cancel()
	}
}

// Resume queues a paused task again, or every paused task of a group.
func (s *Service) Resume(id string) error {
	const op = "Resume"
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return invalid(op, "the application is shutting down")
	}
	members := s.membersLocked(id)
	if len(members) == 0 {
		return opErr(op, ErrNotFound, "no task or group %q", id)
	}
	delete(q.pausedGroups, id)
	for _, t := range members {
		if t.pausing {
			t.resumeAfter = true // still stopping: finishRunning queues it again
			continue
		}
		s.resumeLocked(t)
	}
	s.dispatchLocked()
	return nil
}

func (s *Service) resumeLocked(t *task) {
	if t.pub.Status != StatusPaused {
		return
	}
	if _, paused := s.q.pausedGroups[t.pub.GroupID]; paused && t.pub.GroupID != "" {
		delete(s.q.pausedGroups, t.pub.GroupID) // resuming one file resumes its group's future files
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	t.pub.Status, t.pub.Err = StatusQueued, ""
	t.bytes.Store(0)
	i := kindIndex(t.pub.Kind)
	s.q.queued[i] = append(s.q.queued[i], t)
	s.emitLocked(t, false)
}

// PauseAll pauses every queued and running transfer, and the groups they
// belong to. ResumeAll resumes every paused task.
func (s *Service) PauseAll() {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.spec.Saved != nil && (t.pub.Status == StatusQueued || t.pub.Status == StatusRunning) {
			if t.pub.GroupID != "" {
				q.pausedGroups[t.pub.GroupID] = struct{}{}
			}
			s.pauseLocked(t)
		}
	}
}

// ResumeAll resumes every paused task in submission order.
func (s *Service) ResumeAll() {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	clear(q.pausedGroups)
	ts := make([]*task, 0, len(q.tasks))
	for _, t := range q.tasks {
		if t.pausing {
			t.resumeAfter = true
		}
		if t.pub.Status == StatusPaused {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	for _, t := range ts {
		s.resumeLocked(t)
	}
	s.dispatchLocked()
}

// SaveTransfers writes the queued, running and paused per-file transfers to
// transfers.json so that RestoreTransfers can offer them again after a
// restart; with none it removes the file. Sync runs are not saved: their
// plan is only valid for the preview it came from. Call it before Shutdown.
func (s *Service) SaveTransfers() error {
	q := &s.q
	q.mu.Lock()
	byGroup := map[string]*savedTransferG{}
	var order []string
	ts := make([]*task, 0, len(q.tasks))
	for _, t := range q.tasks {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	for _, t := range ts {
		st := t.pub.Status
		if t.spec.Saved == nil || (st != StatusQueued && st != StatusRunning && st != StatusPaused) {
			continue
		}
		key := t.pub.GroupID
		if key == "" {
			key = t.pub.ID
		}
		g := byGroup[key]
		if g == nil {
			g = &savedTransferG{}
			byGroup[key] = g
			order = append(order, key)
		}
		g.Jobs = append(g.Jobs, *t.spec.Saved)
	}
	q.mu.Unlock()

	path := s.path(transfersFile)
	if len(order) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return classify("SaveTransfers", err)
		}
		return nil
	}
	out := savedTransfers{Format: transfersFormat}
	for _, k := range order {
		out.Groups = append(out.Groups, *byGroup[k])
	}
	if err := writeJSONAtomicRetry(path, out); err != nil {
		return classify("SaveTransfers", err)
	}
	return nil
}

// RestoreTransfers submits the transfers saved by SaveTransfers as paused
// tasks, one group per saved group, and removes the file. It returns the
// number of tasks. Transfers whose account no longer exists are dropped.
// Downloads look the object up again when they start, since it may have
// changed in the meantime.
func (s *Service) RestoreTransfers() (int, error) {
	const op = "RestoreTransfers"
	path := s.path(transfersFile)
	var saved savedTransfers
	found, err := readJSON(path, &saved)
	if err != nil || !found {
		return 0, err
	}
	if saved.Format != transfersFormat {
		return 0, invalid(op, "%s has format %d; this version reads format %d", transfersFile, saved.Format, transfersFormat)
	}
	known := map[string]bool{}
	for _, a := range s.Accounts() {
		known[a.ID] = true
	}
	n := 0
	for _, g := range saved.Groups {
		group := s.newGroupID()
		s.q.mu.Lock()
		s.q.pausedGroups[group] = struct{}{}
		s.q.mu.Unlock()
		for _, j := range g.Jobs {
			switch {
			case j.Upload != nil && known[j.Upload.Dest.AccountID] && j.Upload.Sync == nil:
				s.submit(s.uploadTaskSpec(group, *j.Upload))
				n++
			case j.Download != nil && known[j.Download.Src.AccountID] && j.Download.Sync == nil:
				s.submit(s.downloadTaskSpecRestored(group, *j.Download))
				n++
			}
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return n, classify(op, err)
	}
	return n, nil
}
