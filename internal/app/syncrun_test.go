package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/trash"
)

// syTask returns the single task of kind k in tasks.
func syTask(t *testing.T, tasks []Task, k Kind) Task {
	t.Helper()
	ts := trKind(tasks, k)
	if len(ts) != 1 {
		t.Fatalf("want exactly one %s task, got %d of %d", k, len(ts), len(tasks))
	}
	return ts[0]
}

func syItemsOf(e *syEnv, tk Task) map[string]ItemResult {
	m := map[string]ItemResult{}
	for _, it := range e.s.TaskItems(tk.ID) {
		m[it.Key] = it
	}
	return m
}

func TestSyncRunUploadMirrorExactlyThePreview(t *testing.T) {
	e := syNewEnv(t)
	e.write("keep.txt", "keep", syBase)
	e.write("change.txt", "v1", syBase)
	job := e.job("upload", "mirror", "size_time")
	trExpect(t, e.run(e.preview(job)), StatusSucceeded)

	e.write("change.txt", "version two", syBase.Add(time.Hour))
	e.write("add/new.txt", "new", syBase)
	e.srv.PutObject("b", "sync/extra1.txt", []byte("1"), "", nil)
	e.srv.PutObject("b", "sync/dir/extra2.txt", []byte("2"), "", nil)
	e.srv.PutObject("b", "sync2/sibling.txt", []byte("s"), "", nil) // outside the prefix
	e.srv.PutObject("b", "other.txt", []byte("o"), "", nil)

	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"keep.txt": "skip:unchanged", "change.txt": "upload:size differs", "add/new.txt": "upload:new",
		"extra1.txt": "delete:not in the source", "dir/extra2.txt": "delete:not in the source",
	})
	// A file that appears after the preview is not part of the run.
	e.write("late.txt", "late", syBase)

	tasks := e.run(pv)
	trExpect(t, tasks, StatusSucceeded)
	if len(trKind(tasks, KindUpload)) != 2 || len(trKind(tasks, KindOther)) != 1 || len(tasks) != 3 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if got := trObject(t, e.trEnv, "sync/change.txt"); got != "version two" {
		t.Errorf("change.txt = %q", got)
	}
	trObject(t, e.trEnv, "sync/add/new.txt")
	trObject(t, e.trEnv, "sync/keep.txt")
	trMissing(t, e.trEnv, "sync/extra1.txt")
	trMissing(t, e.trEnv, "sync/dir/extra2.txt")
	trMissing(t, e.trEnv, "sync/late.txt")
	trObject(t, e.trEnv, "sync2/sibling.txt")
	trObject(t, e.trEnv, "other.txt")

	del := syTask(t, tasks, KindOther)
	items := syItemsOf(e, del)
	if len(items) != 2 || items["sync/extra1.txt"].Status != ItemSucceeded || items["sync/dir/extra2.txt"].Status != ItemSucceeded {
		t.Errorf("delete items = %+v", items)
	}
	up := syItemsOf(e, trKind(tasks, KindUpload)[0])
	up2 := syItemsOf(e, trKind(tasks, KindUpload)[1])
	if len(up)+len(up2) != 2 {
		t.Errorf("upload items %v %v", up, up2)
	}

	// The preview after the run only wants the late file.
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"keep.txt": "skip:unchanged", "change.txt": "skip:unchanged", "add/new.txt": "skip:unchanged", "late.txt": "upload:new",
	})
	trExpect(t, e.run(pv), StatusSucceeded)
	pv = e.preview(job)
	syCounts(t, pv, 0, 0, 0, 4)
	if _, err := e.s.RunSync(pv); !IsKind(err, ErrInvalid) {
		t.Errorf("RunSync of a plan without work: %v", err)
	}
}

func TestSyncRunDownloadRoundTripKeepsTimes(t *testing.T) {
	e := syNewEnv(t)
	e.write("a.txt", "alpha", syBase)
	e.write("deep/er/b.txt", "bravo", syBase.Add(-72*time.Hour))
	trExpect(t, e.run(e.preview(e.job("upload", "new_changed", "checksum"))), StatusSucceeded)

	dst := t.TempDir()
	job := e.job("download", "mirror", "size_time")
	job.LocalDir = dst
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "download:new", "deep/er/b.txt": "download:new"})
	tasks := e.run(pv)
	trExpect(t, tasks, StatusSucceeded)
	if trRead(t, filepath.Join(dst, "a.txt")) != "alpha" || trRead(t, filepath.Join(dst, "deep", "er", "b.txt")) != "bravo" {
		t.Fatal("content mismatch")
	}
	for rel, want := range map[string]time.Time{"a.txt": syBase, "deep/er/b.txt": syBase.Add(-72 * time.Hour)} {
		fi, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(want) {
			t.Errorf("%s: mtime %v, want the original %v", rel, fi.ModTime(), want)
		}
	}
	trTree(t, dst) // no temporary files
	syCounts(t, e.previewIn(job), 0, 0, 0, 2)

	// A changed object comes down; the local file is replaced.
	e.write("a.txt", "alpha two", syBase.Add(time.Hour))
	trExpect(t, e.run(e.preview(e.job("upload", "new_changed", "size_time"))), StatusSucceeded)
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "download:size differs", "deep/er/b.txt": "skip:unchanged"})
	trExpect(t, e.run(pv), StatusSucceeded)
	if trRead(t, filepath.Join(dst, "a.txt")) != "alpha two" {
		t.Fatal("a.txt was not replaced")
	}
	syCounts(t, e.previewIn(job), 0, 0, 0, 2)
}

// previewIn previews a job on whatever LocalDir it names.
func (e *syEnv) previewIn(job SyncJob) SyncPreview { return e.preview(job) }

func TestSyncRunMirrorDownloadMovesExtraneousToTrash(t *testing.T) {
	e := syNewEnv(t)
	trashed := syStubTrash(t)
	e.srv.PutObject("b", "sync/keep.txt", []byte("keep"), "", map[string]string{"pail-mtime": syBase.Format(time.RFC3339Nano)})
	e.write("keep.txt", "keep", syBase)
	extra := e.write("extra.txt", "extra", syBase)
	nested := e.write("sub/nested.txt", "nested", syBase)
	job := e.job("download", "mirror", "size_time")
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"keep.txt": "skip:unchanged", "extra.txt": "delete:not in the source", "sub/nested.txt": "delete:not in the source",
	})
	tasks := e.run(pv)
	trExpect(t, tasks, StatusSucceeded)
	if len(*trashed) != 2 {
		t.Fatalf("trashed = %v", *trashed)
	}
	for _, p := range []string{extra, nested} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if trRead(t, filepath.Join(e.dir, "keep.txt")) != "keep" {
		t.Fatal("keep.txt changed")
	}
	items := syItemsOf(e, syTask(t, tasks, KindOther))
	if len(items) != 2 || items[extra].Status != ItemSucceeded || items[nested].Status != ItemSucceeded {
		t.Errorf("items = %+v", items)
	}
	syCounts(t, e.preview(job), 0, 0, 0, 1)
}

func TestSyncRunLocalDeleteNeverDeletesWithoutTrash(t *testing.T) {
	e := syNewEnv(t)
	e.srv.PutObject("b", "sync/keep.txt", []byte("keep"), "", nil)
	extra := e.write("extra.txt", "precious", syBase)
	old := syncTrash
	var calls int
	syncTrash = func(string) error { calls++; return trash.ErrUnavailable }
	t.Cleanup(func() { syncTrash = old })

	job := e.job("download", "mirror", "size")
	pv := e.preview(job)
	tasks := e.run(pv)
	tk := syTask(t, tasks, KindOther)
	if tk.Status != StatusPartiallyFailed || tk.ItemsFailed != 1 {
		t.Fatalf("task = %+v", tk)
	}
	it := syItemsOf(e, tk)[extra]
	if it.Status != ItemFailed || !strings.Contains(it.Err, "trash is not available") || !strings.Contains(it.Err, "kept") {
		t.Errorf("item = %+v", it)
	}
	if calls != 1 || trRead(t, extra) != "precious" {
		t.Fatalf("calls %d, file %q", calls, trRead(t, extra))
	}

	// A trash that claims success without moving the file is an error too.
	syncTrash = func(string) error { return nil }
	tasks = e.run(e.preview(job))
	if tk := syTask(t, tasks, KindOther); tk.Status != StatusPartiallyFailed {
		t.Fatalf("task = %+v", tk)
	}

	// Retry runs the failed item again.
	syncTrash = func(p string) error { return os.Rename(p, p+".trashed") }
	var other Task
	for _, tk := range e.s.Tasks() {
		if tk.Kind == KindOther && tk.Status == StatusPartiallyFailed {
			other = tk
		}
	}
	id, err := e.s.Retry(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk := wait(t, e.s, id); tk.Status != StatusSucceeded {
		t.Fatalf("retry = %+v", tk)
	}
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatal("file survived the retry")
	}
}

// ---- drift between preview and run --------------------------------------------------------

func TestSyncRunUploadDriftRemoteChanged(t *testing.T) {
	e := syNewEnv(t)
	e.write("chg.txt", "v1", syBase)
	job := e.job("upload", "new_changed", "size_time")
	trExpect(t, e.run(e.preview(job)), StatusSucceeded)

	e.write("chg.txt", "v2 longer", syBase.Add(time.Hour))
	e.write("fresh.txt", "fresh", syBase)
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{"chg.txt": "upload:size differs", "fresh.txt": "upload:new"})
	// Someone else writes both keys after the preview.
	e.srv.PutObject("b", "sync/chg.txt", []byte("theirs-1"), "", nil)
	e.srv.PutObject("b", "sync/fresh.txt", []byte("theirs-2"), "", nil)

	tasks := e.run(pv)
	for _, tk := range trKind(tasks, KindUpload) {
		if tk.Status != StatusFailed || !strings.Contains(tk.Err, "since the sync preview") {
			t.Errorf("%s: %s %q", tk.Title, tk.Status, tk.Err)
		}
	}
	if len(trKind(tasks, KindUpload)) != 2 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if trObject(t, e.trEnv, "sync/chg.txt") != "theirs-1" || trObject(t, e.trEnv, "sync/fresh.txt") != "theirs-2" {
		t.Fatal("a concurrent write was overwritten")
	}
}

func TestSyncRunUploadConditionalHeaderGuardsTheRace(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "create-only"
		if existing {
			name = "if-match"
		}
		t.Run(name, func(t *testing.T) {
			e := syNewEnv(t)
			e.write("r.txt", "mine!", syBase)
			if existing {
				e.srv.PutObject("b", "sync/r.txt", []byte("old"), "", nil)
			}
			pv := e.preview(e.job("upload", "new_changed", "size"))
			if got := syPlan(pv)["r.txt"]; !strings.HasPrefix(got, "upload:") {
				t.Fatalf("plan: %v", got)
			}
			// The racer wins between the HEAD of the task and its PUT.
			e.srv.Inject(trRaceOnce(isPut("sync/r.txt"), func() {
				e.srv.PutObject("b", "sync/r.txt", []byte("racer"), "", nil)
			}))
			tasks := e.run(pv)
			tk := syTask(t, tasks, KindUpload)
			if tk.Status != StatusFailed {
				t.Fatalf("task = %+v", tk)
			}
			if trObject(t, e.trEnv, "sync/r.txt") != "racer" {
				t.Fatal("the racer was overwritten")
			}
		})
	}
}

func TestSyncRunUploadLocalChangedIsSkipped(t *testing.T) {
	e := syNewEnv(t)
	e.write("a.txt", "one", syBase)
	e.write("b.txt", "two", syBase)
	pv := e.preview(e.job("upload", "new_changed", "checksum"))
	e.write("a.txt", "one, edited", syBase)          // size changed
	e.write("b.txt", "TWO", syBase.Add(time.Second)) // same size, time changed
	tasks := e.run(pv)
	trExpect(t, tasks, StatusSucceeded)
	for _, tk := range trKind(tasks, KindUpload) {
		for _, it := range e.s.TaskItems(tk.ID) {
			if it.Status != ItemSkipped || it.Err != "local file changed since preview" {
				t.Errorf("item %+v", it)
			}
		}
	}
	trMissing(t, e.trEnv, "sync/a.txt")
	trMissing(t, e.trEnv, "sync/b.txt")
	// A fresh preview sees the new state and uploads the right bytes with the right hash.
	trExpect(t, e.run(e.preview(e.job("upload", "new_changed", "checksum"))), StatusSucceeded)
	if m := syMeta(t, e, "sync/b.txt"); m["pail-sha256"] != sySha("TWO") {
		t.Errorf("metadata = %v", m)
	}
}

func TestSyncRunDownloadDrift(t *testing.T) {
	e := syNewEnv(t)
	mt := map[string]string{"pail-mtime": syBase.Add(time.Hour).Format(time.RFC3339Nano)}
	e.srv.PutObject("b", "sync/changed.txt", []byte("remote one"), "", mt)
	e.srv.PutObject("b", "sync/fresh.txt", []byte("remote fresh"), "", mt)
	e.srv.PutObject("b", "sync/gone.txt", []byte("remote gone"), "", mt)
	chg := e.write("changed.txt", "local", syBase)
	e.write("gone.txt", "x", syBase)
	pv := e.preview(e.job("download", "new_changed", "size_time"))
	syWantPlan(t, pv, map[string]string{
		"changed.txt": "download:size differs", "fresh.txt": "download:new", "gone.txt": "download:size differs",
	})

	// Local edits after the preview: an overwrite target changed, a new target appeared.
	e.write("changed.txt", "edited meanwhile", syBase)
	fresh := e.write("fresh.txt", "mine", syBase)
	// The third object changes remotely: If-Match must stop the GET.
	e.srv.PutObject("b", "sync/gone.txt", []byte("remote changed again"), "", mt)

	tasks := e.run(pv)
	if len(trKind(tasks, KindDownload)) != 3 {
		t.Fatalf("tasks = %+v", tasks)
	}
	for _, tk := range trKind(tasks, KindDownload) {
		switch tk.Title {
		case "Download changed.txt", "Download fresh.txt":
			if tk.Status != StatusSucceeded {
				t.Errorf("%s: %s %s", tk.Title, tk.Status, tk.Err)
			}
			for _, it := range e.s.TaskItems(tk.ID) {
				if it.Status != ItemSkipped || it.Err != "local file changed since preview" {
					t.Errorf("%s item %+v", tk.Title, it)
				}
			}
		case "Download gone.txt":
			if tk.Status != StatusFailed || !strings.Contains(tk.Err, "changed after it was listed") {
				t.Errorf("%s: %s %q", tk.Title, tk.Status, tk.Err)
			}
		}
	}
	if trRead(t, chg) != "edited meanwhile" || trRead(t, fresh) != "mine" || trRead(t, filepath.Join(e.dir, "gone.txt")) != "x" {
		t.Fatal("a local file was overwritten")
	}
	trTree(t, e.dir)
}

func TestSyncRunMirrorDeleteDrift(t *testing.T) {
	e := syNewEnv(t)
	e.write("keep.txt", "keep", syBase)
	for _, k := range []string{"x1", "x2", "x3"} {
		e.srv.PutObject("b", "sync/"+k+".txt", []byte(k), "", nil)
	}
	job := e.job("upload", "mirror", "size")
	pv := e.preview(job)
	if pv.Deletes != 3 {
		t.Fatalf("plan: %+v", pv.Actions)
	}
	e.srv.PutObject("b", "sync/x1.txt", []byte("x1 modified"), "", nil) // changed
	e.srv.DeleteObject("b", "sync/x2.txt")                              // already gone
	tasks := e.run(pv)
	tk := syTask(t, tasks, KindOther)
	if tk.Status != StatusPartiallyFailed || tk.ItemsFailed != 1 {
		t.Fatalf("task = %+v", tk)
	}
	items := syItemsOf(e, tk)
	if items["sync/x1.txt"].Status != ItemFailed || !strings.Contains(items["sync/x1.txt"].Err, "changed since preview") {
		t.Errorf("x1 = %+v", items["sync/x1.txt"])
	}
	if items["sync/x2.txt"].Status != ItemSkipped || items["sync/x3.txt"].Status != ItemSucceeded {
		t.Errorf("items = %+v", items)
	}
	if trObject(t, e.trEnv, "sync/x1.txt") != "x1 modified" {
		t.Fatal("a changed object was deleted")
	}
	trMissing(t, e.trEnv, "sync/x3.txt")
}

func TestSyncRunMirrorLocalDeleteDrift(t *testing.T) {
	e := syNewEnv(t)
	trashed := syStubTrash(t)
	e.srv.PutObject("b", "sync/keep.txt", []byte("k"), "", nil)
	edited := e.write("edited.txt", "before", syBase)
	e.write("keep.txt", "k", syBase)
	pv := e.preview(e.job("download", "mirror", "size"))
	if pv.Deletes != 1 {
		t.Fatalf("plan %+v", pv.Actions)
	}
	e.write("edited.txt", "after!!", syBase)
	tasks := e.run(pv)
	trExpect(t, tasks, StatusSucceeded)
	if len(*trashed) != 0 || trRead(t, edited) != "after!!" {
		t.Fatalf("trashed %v, file %q", *trashed, trRead(t, edited))
	}
	it := syItemsOf(e, syTask(t, tasks, KindOther))[edited]
	if it.Status != ItemSkipped || it.Err != "local file changed since preview" {
		t.Errorf("item = %+v", it)
	}
}

func TestSyncRunUploadRulesChangedSincePreview(t *testing.T) {
	e := syNewEnv(t)
	e.write("a.txt", "abc", syBase)
	pv := e.preview(e.job("upload", "new_changed", "size_time"))
	if err := e.s.SaveUploadRules(zipRule("*.txt")); err != nil {
		t.Fatal(err)
	}
	tk := syTask(t, e.run(pv), KindUpload)
	if tk.Status != StatusFailed || !strings.Contains(tk.Err, "upload rules changed") {
		t.Fatalf("task = %+v", tk)
	}
	trMissing(t, e.trEnv, "sync/a.txt")
	trMissing(t, e.trEnv, "sync/a.txt.zip")
}

func TestSyncRunRejectsMalformedPreviews(t *testing.T) {
	e := syNewEnv(t)
	e.write("a.txt", "abc", syBase)
	up := e.preview(e.job("upload", "mirror", "size_time"))
	if len(up.Actions) != 1 {
		t.Fatal(up.Actions)
	}
	base := up.Actions[0]
	mut := func(f func(a *SyncAction)) SyncPreview {
		p := up
		p.Actions = []SyncAction{base}
		f(&p.Actions[0])
		return p
	}
	cases := map[string]SyncPreview{
		"key outside prefix": mut(func(a *SyncAction) { a.Key = "other/a.txt" }),
		"unrelated key":      mut(func(a *SyncAction) { a.Key = "sync/zzz.txt" }),
		"traversal path":     mut(func(a *SyncAction) { a.Path = "../a.txt" }),
		"absolute path":      mut(func(a *SyncAction) { a.Path = "/etc/passwd" }),
		"unknown op":         mut(func(a *SyncAction) { a.Op = "move" }),
		"download in upload": mut(func(a *SyncAction) { a.Op = "download" }),
		"delete outside":     mut(func(a *SyncAction) { a.Op, a.Key = "delete", "elsewhere/a.txt" }),
		"delete the prefix":  mut(func(a *SyncAction) { a.Op, a.Key = "delete", "sync/" }),
		"only skips":         mut(func(a *SyncAction) { a.Op = "skip" }),
		"empty":              {Job: up.Job},
		"bad job":            func() SyncPreview { p := up; p.Job.LocalDir = "relative"; return p }(),
		"unknown account":    func() SyncPreview { p := up; p.Job.Remote.AccountID = "nope"; return p }(),
	}
	for name, p := range cases {
		if g, err := e.s.RunSync(p); err == nil {
			t.Errorf("%s: accepted (group %s)", name, g)
		}
	}
	// A download preview refuses upload actions and local deletes without state.
	dn := e.preview(e.job("download", "mirror", "size"))
	dn.Actions = []SyncAction{{Op: "upload", Path: "a.txt", Key: "sync/a.txt"}}
	if _, err := e.s.RunSync(dn); err == nil {
		t.Error("upload action accepted in a download job")
	}
	dn.Actions = []SyncAction{{Op: "delete", Path: "a.txt"}}
	if _, err := e.s.RunSync(dn); err == nil {
		t.Error("local delete without observed state accepted")
	}
	if len(e.s.Tasks()) != 0 {
		t.Errorf("rejected previews started %d tasks", len(e.s.Tasks()))
	}
}
