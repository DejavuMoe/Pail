package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Pausing and resuming transfers.
//
// Per-file uploads and downloads (tasks with a savedTransfer) and the scans
// of folder transfers can be paused. A queued task leaves its queue; a running
// one is interrupted like a cancel, cleans up the same way (the multipart
// upload is aborted, the temporary download file removed) and then waits as
// Paused. Resume puts it at the end of its queue and the file starts again
// from the beginning: an interrupted transfer is never half published. A
// resumed scan continues at its position (scanState). Pausing a group pauses
// its scan and the files submitted later.
//
// SaveTransfers writes the unfinished per-file transfers and the positions of
// unfinished scans to transfers.json; RestoreTransfers submits them again,
// paused, in a later session.

// savedTransfer is the serializable description of one per-file transfer.
type savedTransfer struct {
	Upload   *uploadJob   `json:"upload,omitempty"`
	Download *downloadJob `json:"download,omitempty"`
}

// transfersFile is the name of the saved transfer list in the config directory.
const transfersFile = "transfers.json"

// transfersFormat is the version of transfers.json written. Format 1
// (released up to v0.7.x) holds per-file jobs only; format 2 adds the group's
// description and the position of its scan. Both are read.
const transfersFormat = 2

type savedTransfers struct {
	Format int              `json:"format"`
	Groups []savedTransferG `json:"groups"`
}

type savedTransferG struct {
	Jobs []savedTransfer `json:"jobs"`
	Info *groupInfo      `json:"info,omitempty"` // format 2
	Scan *savedScan      `json:"scan,omitempty"` // format 2: the scan was unfinished
}

// savedScan describes where an unfinished scan continues.
type savedScan struct {
	Upload   *savedUploadScan   `json:"upload,omitempty"`
	Download *savedDownloadScan `json:"download,omitempty"`
}

type savedUploadScan struct {
	Dest    Location           `json:"dest"`
	Roots   []scanRoot         `json:"roots"`
	Policy  ConflictPolicy     `json:"policy"`
	NoRules bool               `json:"noRules,omitempty"`
	Pos     savedScanPos       `json:"pos"`
	Failed  []savedScanFailure `json:"failed,omitempty"`
}

type savedDownloadScan struct {
	Request DownloadRequest    `json:"request"` // Conflict and DestDir resolved
	Pos     savedScanPos       `json:"pos"`
	Failed  []savedScanFailure `json:"failed,omitempty"`
	// Claims are the local names handed out (case-insensitive file systems):
	// folded path -> owner.
	Claims map[string]string `json:"claims,omitempty"`
}

// savedScanPos is a scanState position: roots (units) before Next are done;
// inside roots[Next] the walk continues after Path (upload) or the listing
// after the key After (download).
type savedScanPos struct {
	Next  int      `json:"next"`
	Path  []string `json:"path,omitempty"`
	After string   `json:"after,omitempty"`
	Files int      `json:"files,omitempty"`
}

// savedScanFailure is a failed item of a scan, reported again by the restored
// scan; Root or Unit is what Retry rescans for it.
type savedScanFailure struct {
	Key       string    `json:"key"`
	VersionID string    `json:"versionId,omitempty"`
	Err       string    `json:"err,omitempty"`
	Root      *scanRoot `json:"root,omitempty"`
	Unit      *scanUnit `json:"unit,omitempty"`
}

// Pausable reports whether Pause accepts id: a per-file transfer or a scan
// that is queued or running, or a group with such a task.
func (s *Service) Pausable(id string) bool {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if t := q.tasks[id]; t != nil {
		return canPause(t.spec) && (t.pub.Status == StatusQueued || t.pub.Status == StatusRunning)
	}
	if g := q.groups[id]; g != nil {
		return g.pausable > 0 || g.scans > g.scansPaused
	}
	return false
}

// membersLocked returns the task id or the members of group id, in
// submission order. Caller holds q.mu.
func (s *Service) membersLocked(id string) []*task {
	if t := s.q.tasks[id]; t != nil {
		return []*task{t}
	}
	return s.groupMembersLocked(id)
}

// groupMembersLocked returns the retained members of group id in submission
// order. Caller holds q.mu.
func (s *Service) groupMembersLocked(id string) []*task {
	g := s.q.groups[id]
	if g == nil {
		return nil
	}
	ts := make([]*task, 0, len(g.members)-g.dead)
	for _, t := range g.members {
		if !t.removed {
			ts = append(ts, t)
		}
	}
	return ts
}

// Pause pauses a task or every transfer of a group. Tasks that are not
// per-file transfers or scans (bulk operations) keep running. Pausing a group
// also pauses its scan and the transfers submitted later, until Resume or
// CancelGroup.
func (s *Service) Pause(id string) error {
	const op = "Pause"
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.tasks[id] == nil {
		return s.pauseGroupLocked(op, id)
	}
	s.pauseLocked(q.tasks[id])
	return nil
}

// PauseGroup pauses every transfer and the scan of group id (Pause for a
// group id only).
func (s *Service) PauseGroup(id string) error {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	return s.pauseGroupLocked("PauseGroup", id)
}

func (s *Service) pauseGroupLocked(op, id string) error {
	q := &s.q
	members := s.groupMembersLocked(id)
	if len(members) == 0 {
		return opErr(op, ErrNotFound, "no task or group %q", id)
	}
	q.pausedGroups[id] = struct{}{}
	s.unqueueWhereLocked(func(t *task) bool { return t.pub.GroupID == id && canPause(t.spec) })
	for _, t := range members {
		s.pauseLocked(t)
	}
	return nil
}

func (s *Service) pauseLocked(t *task) {
	if !canPause(t.spec) {
		return
	}
	switch t.pub.Status {
	case StatusQueued:
		s.unqueueLocked(t)
		s.setStatusLocked(t, StatusPaused)
		s.notifyStateLocked()
		s.emitLocked(t, false)
	case StatusRunning:
		t.pausing, t.resumeAfter = true, false
		t.cancel()
	}
}

// Resume queues a paused task again, or every paused task of a group.
func (s *Service) Resume(id string) error {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	return s.resumeIDLocked("Resume", s.membersLocked(id), id)
}

// ResumeGroup resumes every paused task of group id (Resume for a group id only).
func (s *Service) ResumeGroup(id string) error {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	return s.resumeIDLocked("ResumeGroup", s.groupMembersLocked(id), id)
}

func (s *Service) resumeIDLocked(op string, members []*task, id string) error {
	q := &s.q
	if q.closed {
		return invalid(op, "the application is shutting down")
	}
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
	s.setStatusLocked(t, StatusQueued)
	t.pub.Err = ""
	t.bytes.Store(0)
	t.phase.Store(PhaseNone)
	s.enqueueLocked(t)
	s.emitLocked(t, false)
}

// PauseAll pauses every queued and running transfer and scan, and the groups
// they belong to. ResumeAll resumes every paused task.
func (s *Service) PauseAll() {
	q := &s.q
	q.mu.Lock()
	defer q.mu.Unlock()
	var ts []*task
	for _, t := range q.tasks {
		if canPause(t.spec) && (t.pub.Status == StatusQueued || t.pub.Status == StatusRunning) {
			if t.pub.GroupID != "" {
				q.pausedGroups[t.pub.GroupID] = struct{}{}
			}
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	s.unqueueWhereLocked(func(t *task) bool { return canPause(t.spec) })
	for _, t := range ts {
		s.pauseLocked(t)
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

// SaveTransfers writes the queued, running and paused per-file transfers, and
// where the unfinished scans of their groups stand, to transfers.json so that
// RestoreTransfers can offer them again after a restart; with none it removes
// the file. Sync runs are not saved: their plan is only valid for the preview
// it came from. Call it before Shutdown.
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
		if st != StatusQueued && st != StatusRunning && st != StatusPaused {
			continue
		}
		var scan *savedScan
		switch {
		case t.spec.Saved != nil:
		case t.spec.Save != nil:
			if scan = t.spec.Save(); scan == nil {
				continue
			}
		default:
			continue
		}
		key := t.pub.GroupID
		if key == "" {
			key = t.pub.ID
		}
		g := byGroup[key]
		if g == nil {
			g = &savedTransferG{}
			if tg := q.groups[t.pub.GroupID]; tg != nil && tg.info != nil {
				info := *tg.info
				g.Info = &info
			}
			byGroup[key] = g
			order = append(order, key)
		}
		if scan != nil {
			g.Scan = scan
		} else {
			g.Jobs = append(g.Jobs, *t.spec.Saved)
		}
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
// tasks, one group per saved group, together with the paused scan of the
// group when it was unfinished, and removes the file. It returns the number
// of tasks. Transfers whose account no longer exists are dropped. Downloads
// look the object up again when they start, since it may have changed in the
// meantime. Both formats are read: 1 (per-file jobs only) and 2.
func (s *Service) RestoreTransfers() (int, error) {
	const op = "RestoreTransfers"
	path := s.path(transfersFile)
	var saved savedTransfers
	found, err := readJSON(path, &saved)
	if err != nil || !found {
		return 0, err
	}
	if saved.Format != 1 && saved.Format != transfersFormat {
		return 0, invalid(op, "%s has format %d; this version reads formats 1 and %d", transfersFile, saved.Format, transfersFormat)
	}
	known := map[string]bool{}
	for _, a := range s.Accounts() {
		known[a.ID] = true
	}
	n := 0
	for _, g := range saved.Groups {
		var specs []taskSpec
		group := s.newGroupID()
		for _, j := range g.Jobs {
			switch {
			case j.Upload != nil && known[j.Upload.Dest.AccountID] && j.Upload.Sync == nil:
				specs = append(specs, s.uploadTaskSpec(group, *j.Upload))
			case j.Download != nil && known[j.Download.Src.AccountID] && j.Download.Sync == nil:
				specs = append(specs, s.downloadTaskSpecRestored(group, *j.Download))
			}
		}
		if sc := g.Scan; sc != nil {
			st := newScanState()
			switch {
			case sc.Upload != nil && known[sc.Upload.Dest.AccountID] && sc.Upload.Policy.Valid():
				u := sc.Upload
				st.restore(u.Pos, u.Failed)
				specs = append(specs, s.uploadScanSpecFrom(group, u.Dest, u.Roots, u.Policy, u.NoRules, "Scan", st))
			case sc.Download != nil && known[sc.Download.Request.Source.AccountID] && sc.Download.Request.Conflict.Valid():
				d := sc.Download
				st.restore(d.Pos, d.Failed)
				for k, v := range d.Claims {
					st.names.owners[k] = v
				}
				specs = append(specs, s.downloadScanSpecFrom(group, d.Request, "Scan", st))
			}
		}
		if len(specs) == 0 {
			continue
		}
		// The group's description: saved (format 2), else that of its first file.
		info := g.Info
		if info == nil {
			info = &groupInfo{Kind: specs[0].Kind, Title: specs[0].Title, Detail: specs[0].Detail}
			if len(specs) > 1 {
				info.Title = fmt.Sprintf("%s %d files", strings.ToUpper(specs[0].Kind.String()[:1])+specs[0].Kind.String()[1:], len(specs))
			}
		}
		specs[0].Info = info
		s.q.mu.Lock()
		s.q.pausedGroups[group] = struct{}{}
		s.q.mu.Unlock()
		for _, spec := range specs {
			s.submit(spec)
			n++
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return n, classify(op, err)
	}
	return n, nil
}
