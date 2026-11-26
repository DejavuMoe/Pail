package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo"
)

// ---- fixtures ------------------------------------------------------------------------------------

// groupFixture is a 150-file folder upload in the state the screenshots showed:
// 8 files running, one paused, the rest queued, the scan finished.
func groupFixture(base time.Time) ([]Task, []core.GroupProgress) {
	var ts []Task
	ts = append(ts, Task{ID: "scan", GroupID: "g1", Kind: core.KindOther, Title: "Scan 1 item for upload", Status: core.StatusSucceeded, Pausable: true, Created: base, Finished: base})
	for i := 0; i < 150; i++ {
		t := Task{ID: fmt.Sprintf("f%03d", i), GroupID: "g1", Kind: core.KindUpload, Title: fmt.Sprintf("Upload photos/img%03d.jpg", i), Detail: "-> s3://acme-assets/",
			Status: core.StatusQueued, BytesTotal: 4 << 20, Pausable: true, Created: base.Add(time.Duration(i+1) * time.Millisecond)}
		switch {
		case i < 8:
			t.Status, t.BytesDone, t.Speed = core.StatusRunning, int64(i)<<19, 2<<20
			if i == 0 {
				t.Phase, t.BytesDone, t.Speed = core.PhaseCompressing, 0, 0
			}
		case i == 8:
			t.Status = core.StatusPaused
		}
		ts = append(ts, t)
	}
	g := core.GroupProgress{
		ID: "g1", Kind: core.KindUpload, Title: "Upload photos", Detail: "→ s3://acme-assets/", Roots: 1, Name: "photos", Created: base,
		Files: 150, FilesQueued: 141, FilesRunning: 8, FilesPaused: 1,
		BytesTotal: 600 << 20, BytesDone: 24 << 20, Speed: 16 << 20, Pausable: true,
	}
	return ts, []core.GroupProgress{g}
}

func (h *harness) setView(ts []Task, gs []core.GroupProgress) {
	h.a.tasks = append([]Task(nil), ts...)
	h.a.reindexTasks()
	h.a.tv.groups = gs
	h.a.rebuildTransfers()
}

// ---- the derived view ----------------------------------------------------------------------------

func TestGroupRowListsRunningAndPausedFilesOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	ts, gs := groupFixture(time.Now())
	h.setView(ts, gs)
	v := h.a.tv
	// The scan has finished and the 150 files are the work left: nothing else counts.
	if v.n != [3]int{150, 0, 0} {
		t.Fatalf("tab counts %v, want 150 active only (a succeeded scan is not counted)", v.n)
	}
	// group row, 8 running and 1 paused member, the queued line.
	if len(v.active) != 1+9+1 {
		t.Fatalf("%d rows, want 11", len(v.active))
	}
	if v.active[0].kind != trGroup || v.active[len(v.active)-1].kind != trQueued || v.active[len(v.active)-1].n != 141 {
		t.Fatalf("rows %+v", v.active)
	}
	for i, r := range v.active[1 : len(v.active)-1] {
		want := core.StatusRunning
		if i == 8 {
			want = core.StatusPaused
		}
		if !r.member || r.t.Status != want {
			t.Fatalf("member %d: %+v", i, r)
		}
	}
	if v.up != 149 || v.down != 0 {
		t.Fatalf("up %d down %d: queued and running files, not the paused one", v.up, v.down)
	}
	h.a.transfersOpen = true
	h.tt.Frame()
	h.mustHave("Upload photos", "→ s3://acme-assets/", "0 / 150 files", "Cancel this batch", "Pause this batch", "141 more queued", "Compressing…", "Active 150")
	h.mustNotHave("Upload photos/img100.jpg", "Scan 1 item for upload")
}

func TestActiveTabOrdersRunningBeforePausedBeforeQueued(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	base := time.Now()
	h.setView([]Task{
		{ID: "q1", Kind: core.KindOther, Title: "Copy a", Status: core.StatusQueued, Created: base},
		{ID: "p1", Kind: core.KindOther, Title: "Copy b", Status: core.StatusPaused, Created: base.Add(1 * time.Second)},
		{ID: "r2", Kind: core.KindOther, Title: "Copy c", Status: core.StatusRunning, Created: base.Add(3 * time.Second)},
		{ID: "r1", Kind: core.KindOther, Title: "Copy d", Status: core.StatusRunning, Created: base.Add(2 * time.Second)},
		{ID: "q2", Kind: core.KindOther, Title: "Copy e", Status: core.StatusQueued, Created: base.Add(4 * time.Second)},
	}, nil)
	var got []string
	for _, r := range h.a.tv.active {
		got = append(got, r.t.ID)
	}
	if want := "r1 r2 p1 q1 q2"; strings.Join(got, " ") != want {
		t.Fatalf("order %v, want %s", got, want)
	}
}

func TestSingleFileGroupAndScanningGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	base := time.Now()
	// One file and a finished scan: just the file's row, as before.
	h.setView([]Task{
		{ID: "f", GroupID: "g", Kind: core.KindUpload, Title: "Upload a.txt", Status: core.StatusRunning, BytesTotal: 10, Pausable: true, Created: base},
	}, []core.GroupProgress{{ID: "g", Kind: core.KindUpload, Files: 1, FilesRunning: 1, BytesTotal: 10, Pausable: true, Created: base}})
	if len(h.a.tv.active) != 1 || h.a.tv.active[0].kind != trTask || h.a.tv.n[0] != 1 {
		t.Fatalf("single file: %+v", h.a.tv)
	}
	// A scan still running: a group row of its own, the scan task has none, and
	// the batch counts as one until its first file exists.
	h.setView([]Task{
		{ID: "s", GroupID: "g2", Kind: core.KindOther, Title: "Scan 3 items for upload", Status: core.StatusRunning, Pausable: true, Created: base},
	}, []core.GroupProgress{{ID: "g2", Kind: core.KindUpload, Roots: 3, Scanning: true, Pausable: true, Created: base}})
	v := h.a.tv
	if len(v.active) != 1 || v.active[0].kind != trGroup || v.n[0] != 1 {
		t.Fatalf("scanning: %+v", v)
	}
	h.a.transfersOpen = true
	h.tt.Frame()
	h.mustHave("Upload 3 items", "0 / 0+ files", "Scanning…")
	h.mustNotHave("Scan 3 items for upload")
}

func TestFailedScanStaysListedSucceededScanDoesNot(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.setView([]Task{
		{ID: "s1", GroupID: "g", Kind: core.KindOther, Title: "Scan ok", Status: core.StatusSucceeded, Pausable: true},
		{ID: "s2", GroupID: "h", Kind: core.KindOther, Title: "Scan bad", Status: core.StatusPartiallyFailed, Pausable: true, Err: "unreadable"},
		{ID: "u", Kind: core.KindUpload, Title: "Upload x", Status: core.StatusSucceeded},
	}, nil)
	if n := h.a.tv.n; n != [3]int{0, 1, 1} {
		t.Fatalf("counts %v, want 0 active, 1 completed (the file), 1 failed (the scan that reported trouble)", n)
	}
}

func TestStatusBarAndTrayCountQueuedAndRunningFiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.setView([]Task{
		{ID: "a", Kind: core.KindUpload, Status: core.StatusRunning},
		{ID: "b", Kind: core.KindUpload, Status: core.StatusQueued},
		{ID: "c", Kind: core.KindUpload, Status: core.StatusPaused},
		{ID: "d", Kind: core.KindDownload, Status: core.StatusQueued},
		{ID: "e", Kind: core.KindOther, Status: core.StatusRunning, Title: "Delete 3 items"},
	}, nil)
	if v := h.a.tv; v.up != 2 || v.down != 1 {
		t.Fatalf("up %d down %d, want 2 and 1: paused and KindOther tasks count in neither", v.up, v.down)
	}
	if got := trayStatus(h.a.tasks, nil); got != "3 transfers in progress" {
		t.Fatalf("tray %q", got)
	}
}

func TestPausedTasksDoNotHoldRestartOrClose(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	f := &fakeUpdates{}
	f.use(h.a)
	h.a.tray.on, h.a.tray.quitIdle = true, true
	h.a.tasks = []Task{{ID: "p", Kind: core.KindUpload, Status: core.StatusPaused, Pausable: true}}
	if h.a.closeHides() {
		t.Error("only paused transfers left: closing should quit (they are saved)")
	}
	h.a.restartForUpdate()
	if f.relaunched != 1 {
		t.Errorf("a paused transfer held the restart back (relaunched %d)", f.relaunched)
	}
	h.a.tasks = append(h.a.tasks, Task{ID: "q", Kind: core.KindUpload, Status: core.StatusQueued})
	if !h.a.closeHides() {
		t.Error("a queued transfer must keep Pail in the tray")
	}
	h.a.restartForUpdate()
	if f.relaunched != 1 {
		t.Error("restarted with a queued transfer")
	}
	// A scan alone is work too, but does not count as a transfer in the message.
	h.a.tasks = []Task{{ID: "s", GroupID: "g", Kind: core.KindOther, Status: core.StatusRunning, Pausable: true}}
	if n, busy := busyTasks(h.a.tasks); n != 0 || !busy {
		t.Errorf("busyTasks = %d, %v", n, busy)
	}
}

func TestTaskbarProgressOfGroups(t *testing.T) {
	t.Parallel()
	bar := func(v float64) mygo.ProgressBar { return mygo.ProgressBar{State: mygo.ProgressNormal, Value: v} }
	g := core.GroupProgress{ID: "g", Kind: core.KindUpload, Files: 4, FilesSucceeded: 2, FilesRunning: 1, FilesQueued: 1, BytesTotal: 400, BytesDone: 250}
	file := func(id string, st core.Status, total, done int64) Task {
		return Task{ID: id, GroupID: "g", Kind: core.KindUpload, Status: st, BytesTotal: total, BytesDone: done}
	}
	for _, c := range []struct {
		name  string
		tasks []Task
		gs    []core.GroupProgress
		want  mygo.ProgressBar
	}{
		{"finished files count, queued ones too", []Task{file("a", core.StatusRunning, 100, 50), file("b", core.StatusQueued, 0, 0)}, []core.GroupProgress{g}, bar(0.625)},
		{"standalone tasks add their bytes", []Task{file("a", core.StatusRunning, 100, 50), {ID: "x", Kind: core.KindDownload, Status: core.StatusRunning, BytesTotal: 100, BytesDone: 0}}, []core.GroupProgress{g}, bar(0.5)},
		{"files when no bytes are known", nil, []core.GroupProgress{{ID: "g", Files: 4, FilesSucceeded: 1}}, bar(0.25)},
		{"a scan nothing came out of yet", []Task{{ID: "s", GroupID: "g", Kind: core.KindOther, Status: core.StatusRunning, Pausable: true}}, []core.GroupProgress{{ID: "g", Scanning: true}}, mygo.ProgressBar{State: mygo.ProgressIndeterminate}},
		{"all done in the group", nil, []core.GroupProgress{{ID: "g", Files: 2, FilesSucceeded: 1, FilesRunning: 1, BytesTotal: 10, BytesDone: 10}}, bar(1)},
	} {
		if got := taskbarProgress(c.tasks, c.gs); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// ---- through the service -------------------------------------------------------------------------

func TestGroupRowPauseResumeAndCancelWholeFolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/many/") {
			return &s3test.Fault{Delay: 3 * time.Second}
		}
		return nil
	})
	dir := filepath.Join(t.TempDir(), "many")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o600)
	}
	h.a.uploadPaths([]string{dir})
	h.waitFor("the group row with a backlog", func() bool {
		// The groups come from the service, the files from the events: wait for both.
		r := h.a.tv.active
		return len(r) > 2 && r[0].kind == trGroup && r[0].g.FilesQueued > 0 && r[1].member && r[len(r)-1].kind == trQueued
	})
	h.mustHave("Upload many", "Cancel this batch", "Pause this batch")
	// The files running are on top, the backlog is one line.
	if r := h.a.tv.active; r[0].kind != trGroup || r[1].t.Status != core.StatusRunning || r[len(r)-1].kind != trQueued {
		t.Fatalf("rows %+v", r)
	}
	h.mustClick("Pause this batch")
	h.waitFor("the group paused", func() bool { return len(h.a.tv.active) > 0 && h.a.tv.active[0].g.Paused })
	h.mustHave("Resume this batch")
	h.mustClick("Resume this batch")
	h.waitFor("the group running again", func() bool { return len(h.a.tv.active) > 0 && !h.a.tv.active[0].g.Paused })
	h.mustClick("Cancel this batch")
	h.waitFor("every file canceled", func() bool {
		if len(h.a.tv.active) != 0 {
			return false
		}
		n := 0
		for _, t := range h.a.tasks {
			if t.Kind == core.KindUpload && t.Status == core.StatusCanceled {
				n++
			}
		}
		return n > 0
	})
	h.env.Srv.Inject(nil)
	if h.a.tv.n[0] != 0 {
		t.Fatalf("still %d active after the whole group was canceled", h.a.tv.n[0])
	}
}

func TestDownloadFinishKeepsSizesAndListing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	loc := h.a.scope
	if loc.Bucket == "" {
		t.Fatal("no bucket open")
	}
	id := "fs-kept"
	if h.a.sizes.by == nil {
		h.a.sizes.by = map[string]*folderSize{}
	}
	h.a.sizes.by[id] = &folderSize{scope: Scope{AccountID: loc.AccountID, Bucket: loc.Bucket, Prefix: loc.Prefix + "x/"}}
	for kind, drops := range map[string]bool{"download": false, "upload": true, "delete": true} {
		h.a.sizes.by[id] = &folderSize{scope: Scope{AccountID: loc.AccountID, Bucket: loc.Bucket, Prefix: loc.Prefix + "x/"}}
		h.a.finishWatch(&watch{loc: loc, kind: kind}, []Task{{ID: "t", Kind: core.KindDownload, Status: core.StatusSucceeded, ItemsDone: 1, Items: 1}}, core.TaskGroupResult{}, false)
		if _, kept := h.a.sizes.by[id]; kept == drops {
			t.Errorf("%s: sizes kept=%v", kind, kept)
		}
	}
}

func TestSyncRunFinishIsToldAndRecorded(t *testing.T) {
	h := newHarness(t, "light")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "synced.txt"), []byte("sync me"), 0o600)
	job, err := h.env.Svc.SaveSyncJob(core.SyncJob{
		Name: "nightly", LocalDir: dir, Direction: "upload", Mode: "new", Compare: "size_time",
		Remote: core.Location{AccountID: h.a.accountID, Bucket: bkt, Prefix: "synced/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pv, err := h.env.Svc.PreviewSync(h.a.ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	h.a.toasts = nil
	ops := len(h.a.ops)
	h.a.runSync(pv)
	h.waitFor("a sync toast", func() bool { return h.tt.HasText("Synced 1 file") })
	if len(h.a.ops) != ops+1 || h.a.ops[len(h.a.ops)-1].what != tookUpload {
		t.Errorf("recent operations: %d entries, want one more (the sync, as an upload)", len(h.a.ops))
	}
	if _, ok := h.env.Srv.Object(bkt, "synced/synced.txt"); !ok {
		t.Error("the sync did not upload")
	}
}

// A group paused as a whole does not list its paused files one by one: one
// line counts them, and the scanning hint is gone while nothing scans.
func TestPausedGroupCountsItsPausedFiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	base := time.Now()
	var ts []Task
	ts = append(ts, Task{ID: "scan", GroupID: "g1", Kind: core.KindOther, Status: core.StatusPaused, Pausable: true, Created: base})
	for i := 0; i < 72; i++ {
		ts = append(ts, Task{ID: fmt.Sprintf("f%03d", i), GroupID: "g1", Kind: core.KindUpload, Title: fmt.Sprintf("Upload photos/img%03d.jpg", i),
			Status: core.StatusPaused, Pausable: true, Created: base.Add(time.Duration(i+1) * time.Millisecond)})
	}
	g := core.GroupProgress{ID: "g1", Kind: core.KindUpload, Roots: 1, Name: "photos", Created: base,
		Files: 100, FilesPaused: 72, FilesSucceeded: 28, BytesTotal: 400 << 20, BytesDone: 112 << 20, Scanning: true, Paused: true}
	h.setView(ts, []core.GroupProgress{g})
	v := h.a.tv
	if len(v.active) != 2 || v.active[0].kind != trGroup || v.active[1].kind != trPaused || v.active[1].n != 72 {
		t.Fatalf("rows %+v, want the group row and one line counting 72 paused files", v.active)
	}
	if v.n[0] != 72 || !v.paused {
		t.Fatalf("active count %d, paused %v", v.n[0], v.paused)
	}
	h.a.transfersOpen, h.a.transTab = true, 0
	h.tt.Frame()
	if !h.tt.HasText(locales.TransfersGroupPaused("72")) {
		t.Fatal("the line counting the paused files is not shown")
	}
	if h.tt.HasText(locales.TransfersScanning()) {
		t.Fatal("a paused group still says it is scanning")
	}
}
