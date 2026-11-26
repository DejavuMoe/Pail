package main

import (
	"slices"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo"
)

// What the transfers panel, the status bar and the tray show of the task list
// is computed here, once per batch of task events (applyInbox) and not per
// frame; the views only read a.tv.

// trRowKind is what a row of the Active tab is.
type trRowKind uint8

const (
	trTask   trRowKind = iota // one task
	trGroup                   // the summary row of a group of files
	trQueued                  // "N more queued" under a group
	trPaused                  // "N paused" under a group paused as a whole
)

// trRow is one row of the Active tab. member marks a row shown under its
// group row.
type trRow struct {
	kind   trRowKind
	g      core.GroupProgress
	t      Task
	member bool
	n      int // trQueued, trPaused: how many
}

// trView is the derived state of the task list.
type trView struct {
	groups []core.GroupProgress // the service's snapshot at the last event

	n [3]int // tab counts: active files and standalone tasks, completed, failed
	// active is the Active tab in display order, at most maxTaskRows rows;
	// activeMore is how many rows were cut off.
	active     []trRow
	activeMore int
	// finished are the Completed and Failed tabs, newest first, capped the same way.
	finished     [2][]Task
	finishedMore [2]int

	// up and down count the files that are queued or running (not paused).
	up, down int
	// pausable and paused say whether "Pause all" and "Resume all" do anything.
	pausable, paused bool
	progress         mygo.ProgressBar
}

// isScanTask reports whether t is the scan of a folder transfer: a task that
// is no transfer itself, belongs to a group and can be paused. Nothing else
// in a group is both (a sync run's delete task cannot be paused).
func isScanTask(t Task) bool {
	return t.Kind == core.KindOther && t.GroupID != "" && t.Pausable
}

// busyTasks is how many tasks are queued or running, scans left out: what
// restarting, quitting and keeping the machine awake wait for. Paused tasks do
// not count: they are saved at quit and come back paused. anyBusy is whether
// anything at all, a scan included, is queued or running.
func busyTasks(ts []Task) (n int, anyBusy bool) {
	for _, t := range ts {
		if t.Status != core.StatusQueued && t.Status != core.StatusRunning {
			continue
		}
		anyBusy = true
		if !isScanTask(t) {
			n++
		}
	}
	return
}

// refreshTransfers takes the service's groups and rebuilds the view.
func (a *app) refreshTransfers() {
	a.tv.groups = a.svc.TaskGroups()
	a.rebuildTransfers()
}

// hasGroupRow reports whether g gets a row of its own: a transfer of more than
// one file, or one whose scan has not finished (more files may follow).
func hasGroupRow(g core.GroupProgress) bool {
	return g.Kind != core.KindOther && (g.Files > 1 || g.Scanning)
}

func statusRank(s core.Status) int {
	switch s {
	case core.StatusRunning:
		return 0
	case core.StatusPaused:
		return 1
	}
	return 2
}

func groupRank(g core.GroupProgress) int {
	switch {
	case g.FilesRunning > 0 || g.Scanning && !g.Paused:
		return 0
	case g.Paused:
		return 1
	}
	return 2
}

// rebuildTransfers derives a.tv from a.tasks and a.tv.groups.
func (a *app) rebuildTransfers() {
	v := &a.tv
	groups := v.groups
	rowOf := map[string]int{} // group ID -> index, for the groups that get a row
	for i, g := range groups {
		if hasGroupRow(g) {
			rowOf[g.ID] = i
		}
	}
	members := make([][]Task, len(groups))
	type unit struct {
		rank    int
		created time.Time
		seq     int
		row     trRow
		gi      int // index in groups, -1 for a task
	}
	var units []unit
	v.n, v.up, v.down = [3]int{}, 0, 0
	v.pausable, v.paused = false, false
	for i, t := range a.tasks {
		if taskGroup(t) != 0 {
			continue
		}
		if t.Status == core.StatusPaused {
			v.paused = true
		} else if t.Pausable {
			v.pausable = true
		}
		if t.Status == core.StatusQueued || t.Status == core.StatusRunning {
			switch t.Kind {
			case core.KindUpload:
				v.up++
			case core.KindDownload:
				v.down++
			}
			// KindOther tasks (scans, bulk deletes, copies) are neither an
			// upload nor a download: they count in no direction, in the
			// status bar and in the tray alike.
		}
		if gi, ok := rowOf[t.GroupID]; ok && t.GroupID != "" {
			switch {
			case isScanTask(t):
				// the group row speaks for its scan
			case t.Kind == core.KindOther:
				v.n[0]++
				members[gi] = append(members[gi], t)
			case t.Status != core.StatusQueued:
				members[gi] = append(members[gi], t) // queued files are only counted
			}
			continue
		}
		v.n[0]++
		units = append(units, unit{rank: statusRank(t.Status), created: t.Created, seq: i, row: trRow{kind: trTask, t: t}, gi: -1})
	}
	for _, gi := range rowOf {
		g := groups[gi]
		// A batch still scanning with no unfinished file yet counts as one, so
		// that "Active 0" never sits next to its row.
		left := g.FilesQueued + g.FilesRunning + g.FilesPaused
		if left == 0 && g.Scanning {
			left = 1
		}
		v.n[0] += left
		if g.FilesPaused > 0 || g.Paused {
			v.paused = true
		}
		if g.Pausable {
			v.pausable = true
		}
		units = append(units, unit{rank: groupRank(g), created: g.Created, seq: len(a.tasks) + gi, row: trRow{kind: trGroup, g: g}, gi: gi})
	}
	slices.SortFunc(units, func(x, y unit) int {
		switch {
		case x.rank != y.rank:
			return x.rank - y.rank
		case !x.created.Equal(y.created):
			return x.created.Compare(y.created)
		}
		return x.seq - y.seq
	})
	rows := make([]trRow, 0, len(units))
	for _, u := range units {
		rows = append(rows, u.row)
		if u.gi < 0 {
			continue
		}
		// A group paused as a whole says how many files wait in one line: its
		// own button resumes them, and a row each would bury the other groups.
		g := u.row.g
		for _, t := range members[u.gi] {
			if g.Paused && t.Status == core.StatusPaused && t.Kind != core.KindOther {
				continue
			}
			rows = append(rows, trRow{kind: trTask, t: t, member: true})
		}
		if g.Paused && g.FilesPaused > 0 {
			rows = append(rows, trRow{kind: trPaused, g: g, n: g.FilesPaused, member: true})
		}
		if g.FilesQueued > 0 {
			rows = append(rows, trRow{kind: trQueued, g: g, n: g.FilesQueued, member: true})
		}
	}
	v.active, v.activeMore = rows, 0
	if len(rows) > maxTaskRows {
		v.active, v.activeMore = rows[:maxTaskRows], len(rows)-maxTaskRows
	}
	// Completed and failed: newest first. A scan that succeeded says nothing
	// the group's files do not say better, so it is neither listed nor counted.
	v.finished, v.finishedMore = [2][]Task{}, [2]int{}
	for i := len(a.tasks) - 1; i >= 0; i-- {
		t := a.tasks[i]
		tab := taskGroup(t)
		if tab == 0 || tab == 1 && isScanTask(t) {
			continue
		}
		v.n[tab]++
		if l := &v.finished[tab-1]; len(*l) < maxTaskRows {
			*l = append(*l, t)
		} else {
			v.finishedMore[tab-1]++
		}
	}
	v.progress = taskbarProgress(a.tasks, groups)
}

// groupTitle is the group's name in the language shown: what was uploaded or
// downloaded, from the service's description of it.
func groupTitle(g core.GroupProgress) string {
	if strings.HasPrefix(g.Title, "Sync ") {
		name := g.Name
		if name == "" {
			name = locales.SyncUntitledJob()
		}
		return locales.TransfersGroupSync(name)
	}
	up := g.Kind == core.KindUpload
	n := g.Roots
	switch {
	case n == 1 && g.Name != "":
		if up {
			return locales.TransfersGroupUploadName(g.Name)
		}
		return locales.TransfersGroupDownloadName(g.Name)
	case n > 0:
		if up {
			return locales.TransfersGroupUploadItems(n)
		}
		return locales.TransfersGroupDownloadItems(n)
	case g.Files > 0:
		if up {
			return locales.TransfersGroupUploadFiles(g.Files)
		}
		return locales.TransfersGroupDownloadFiles(g.Files)
	}
	if up {
		return locales.TransfersUpload()
	}
	return locales.TransfersDownload()
}

// phaseLabel is what a running transfer says while no bytes move.
func phaseLabel(p core.Phase) string {
	switch p {
	case core.PhasePreparing:
		return locales.TransfersPhasePreparing()
	case core.PhaseCompressing:
		return locales.TransfersPhaseCompressing()
	case core.PhaseVerifying:
		return locales.TransfersPhaseVerifying()
	case core.PhaseDecoding:
		return locales.TransfersPhaseDecoding()
	case core.PhaseFinishing:
		return locales.TransfersPhaseFinishing()
	}
	return ""
}

// groupFilesText is "x / y files" of a group; the total grows while it scans.
func groupFilesText(g core.GroupProgress) string {
	done := groupInt(g.FilesFinished())
	if g.Scanning {
		return locales.TransfersGroupFilesGrowing(done, g.Files)
	}
	return locales.TransfersGroupFiles(done, g.Files)
}
