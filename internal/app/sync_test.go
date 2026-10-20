package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/rules"
)

// ---- helpers (prefix sy) ------------------------------------------------------------------

// syEnv is a transfer environment plus a local sync folder.
type syEnv struct {
	*trEnv
	dir string
}

func syNewEnv(t *testing.T) *syEnv {
	t.Helper()
	return &syEnv{trEnv: newTrEnv(t), dir: t.TempDir()}
}

var syBase = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

func (e *syEnv) job(direction, mode, compare string) SyncJob {
	return SyncJob{Name: "t", LocalDir: e.dir, Remote: e.loc("b", "sync/"), Direction: direction, Mode: mode, Compare: compare}
}

// write creates a file below the sync folder with the given modification time.
func (e *syEnv) write(rel, content string, mt time.Time) string {
	e.t.Helper()
	p := filepath.Join(e.dir, filepath.FromSlash(rel))
	trWrite(e.t, p, []byte(content))
	trChtimes(e.t, p, mt)
	return p
}

func (e *syEnv) preview(job SyncJob) SyncPreview {
	e.t.Helper()
	pv, err := e.s.PreviewSync(testCtx(e.t), job)
	if err != nil {
		e.t.Fatalf("PreviewSync: %v", err)
	}
	return pv
}

// syPlan maps path -> "op:reason".
func syPlan(pv SyncPreview) map[string]string {
	m := map[string]string{}
	for _, a := range pv.Actions {
		m[a.Path] = a.Op + ":" + a.Reason
	}
	return m
}

func syWantPlan(t *testing.T, pv SyncPreview, want map[string]string) {
	t.Helper()
	got := syPlan(pv)
	if len(got) != len(pv.Actions) {
		t.Errorf("duplicate paths in the plan: %+v", pv.Actions)
	}
	var diffs []string
	for k, w := range want {
		if g, ok := got[k]; !ok {
			diffs = append(diffs, "missing "+k+" = "+w)
		} else if g != w {
			diffs = append(diffs, k+": got "+g+", want "+w)
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok {
			diffs = append(diffs, "unexpected "+k+" = "+g)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("plan differs:\n  %s", strings.Join(diffs, "\n  "))
	}
}

// syCounts checks the totals of a preview.
func syCounts(t *testing.T, pv SyncPreview, up, down, del, skip int) {
	t.Helper()
	if pv.Uploads != up || pv.Downloads != down || pv.Deletes != del || pv.Skips != skip {
		t.Fatalf("counts up/down/del/skip = %d/%d/%d/%d, want %d/%d/%d/%d", pv.Uploads, pv.Downloads, pv.Deletes, pv.Skips, up, down, del, skip)
	}
	if up+down+del+skip != len(pv.Actions) {
		t.Fatalf("%d actions for counts %d/%d/%d/%d", len(pv.Actions), up, down, del, skip)
	}
}

// syWait waits until every task of the group is final (RunSync registers them all
// before it returns) and returns them.
func syWait(t testing.TB, s *Service, group string) []Task {
	t.Helper()
	var out []Task
	eventually(t, "sync group "+group, func() bool {
		out = out[:0]
		for _, tk := range s.Tasks() {
			if tk.GroupID != group {
				continue
			}
			out = append(out, tk)
			if !tk.Status.Finished() {
				return false
			}
		}
		return len(out) > 0
	})
	return out
}

func (e *syEnv) run(pv SyncPreview) []Task {
	e.t.Helper()
	g, err := e.s.RunSync(pv)
	if err != nil {
		e.t.Fatalf("RunSync: %v", err)
	}
	return syWait(e.t, e.s, g)
}

// syStubTrash replaces the trash by moving files into a temp folder and
// returns the list of trashed paths.
func syStubTrash(t *testing.T) *[]string {
	t.Helper()
	bin := t.TempDir()
	var trashed []string
	old := syncTrash
	syncTrash = func(p string) error {
		trashed = append(trashed, p)
		return os.Rename(p, filepath.Join(bin, strings.NewReplacer(`\`, "_", "/", "_", ":", "_").Replace(p)))
	}
	t.Cleanup(func() { syncTrash = old })
	return &trashed
}

func sySha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func syMeta(t *testing.T, e *syEnv, key string) map[string]string {
	t.Helper()
	info, err := e.s.Head(testCtx(t), e.loc("b", ""), key, "")
	if err != nil {
		t.Fatal(err)
	}
	return info.Metadata
}

// ---- jobs ---------------------------------------------------------------------------------

func TestSyncJobsCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := t.TempDir()
	s, _ := newTestServiceDir(t, cfg, nil)
	if len(s.SyncJobs()) != 0 {
		t.Fatal("fresh service has jobs")
	}
	in := SyncJob{LocalDir: dir, Remote: Location{AccountID: "a", Bucket: "b", Prefix: "p/"}, Direction: "upload", Excludes: []string{"*.tmp", "cache/"}}
	saved, err := s.SaveSyncJob(in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Name != filepath.Base(dir) || saved.Mode != "new_changed" || saved.Compare != "size_time" {
		t.Fatalf("defaults not applied: %+v", saved)
	}
	second, err := s.SaveSyncJob(SyncJob{Name: "two", LocalDir: dir, Remote: Location{AccountID: "a", Bucket: "b"}, Direction: "download", Mode: "mirror", Compare: "checksum", NoRules: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == saved.ID {
		t.Fatal("IDs must be unique")
	}

	// Update by ID.
	saved.Name = "renamed"
	saved.Mode = "new"
	if _, err := s.SaveSyncJob(saved); err != nil {
		t.Fatal(err)
	}
	// Returned jobs are copies.
	got := s.SyncJobs()
	got[0].Excludes[0] = "mutated"
	if s.SyncJobs()[0].Excludes[0] != "*.tmp" {
		t.Fatal("SyncJobs exposes internal state")
	}
	if len(got) != 2 || got[0].Name != "renamed" || got[0].Mode != "new" || got[1].Name != "two" {
		t.Fatalf("jobs = %+v", s.SyncJobs())
	}

	// Persisted: a new Service on the same directory sees them.
	s2, _ := newTestServiceDir(t, cfg, nil)
	jobs := s2.SyncJobs()
	if len(jobs) != 2 || jobs[0].ID != saved.ID || jobs[1].Compare != "checksum" || !jobs[1].NoRules || len(jobs[0].Excludes) != 2 {
		t.Fatalf("reloaded jobs = %+v", jobs)
	}

	if _, err := s.SaveSyncJob(SyncJob{ID: "nope", LocalDir: dir, Remote: in.Remote, Direction: "upload"}); !IsKind(err, ErrNotFound) {
		t.Errorf("unknown ID: %v", err)
	}
	if err := s.DeleteSyncJob("nope"); !IsKind(err, ErrNotFound) {
		t.Errorf("delete unknown: %v", err)
	}
	if err := s.DeleteSyncJob(saved.ID); err != nil {
		t.Fatal(err)
	}
	if jobs := s.SyncJobs(); len(jobs) != 1 || jobs[0].ID != second.ID {
		t.Fatalf("after delete: %+v", jobs)
	}
	s3, _ := newTestServiceDir(t, cfg, nil)
	if len(s3.SyncJobs()) != 1 {
		t.Fatal("delete was not persisted")
	}
	if _, err := os.Stat(filepath.Join(cfg, "sync-jobs.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncJobValidation(t *testing.T) {
	s := newTestService(t, nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	trWrite(t, file, []byte("x"))
	ok := SyncJob{LocalDir: dir, Remote: Location{AccountID: "a", Bucket: "b", Prefix: "p/"}, Direction: "upload", Mode: "new_changed", Compare: "size_time"}
	if _, err := s.SaveSyncJob(ok); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	for name, mut := range map[string]func(j *SyncJob){
		"empty dir":       func(j *SyncJob) { j.LocalDir = "" },
		"relative dir":    func(j *SyncJob) { j.LocalDir = "rel/dir" },
		"missing dir":     func(j *SyncJob) { j.LocalDir = filepath.Join(dir, "missing") },
		"dir is a file":   func(j *SyncJob) { j.LocalDir = file },
		"no bucket":       func(j *SyncJob) { j.Remote.Bucket = "" },
		"no account":      func(j *SyncJob) { j.Remote.AccountID = "" },
		"prefix no slash": func(j *SyncJob) { j.Remote.Prefix = "p" },
		"no direction":    func(j *SyncJob) { j.Direction = "" },
		"bad direction":   func(j *SyncJob) { j.Direction = "both" },
		"bad mode":        func(j *SyncJob) { j.Mode = "sync" },
		"bad compare":     func(j *SyncJob) { j.Compare = "md5" },
		"empty exclude":   func(j *SyncJob) { j.Excludes = []string{"*.tmp", ""} },
		"backslash":       func(j *SyncJob) { j.Excludes = []string{`a\b`} },
		"leading slash":   func(j *SyncJob) { j.Excludes = []string{"/a"} },
		"control in glob": func(j *SyncJob) { j.Excludes = []string{"a\nb"} },
		"root slash glob": func(j *SyncJob) { j.Excludes = []string{"/"} },
	} {
		j := ok
		mut(&j)
		if _, err := s.SaveSyncJob(j); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: want Invalid, got %v", name, err)
		}
	}
	if n := len(s.SyncJobs()); n != 1 {
		t.Errorf("invalid jobs were stored: %d jobs", n)
	}
}

// ---- preview: classification --------------------------------------------------------------

func TestSyncPreviewUploadModes(t *testing.T) {
	e := syNewEnv(t)
	e.write("same.txt", "same", syBase)
	e.write("size.txt", "aaaa", syBase)
	e.write("time.txt", "tttt", syBase)
	e.write("sub/old.txt", "old", syBase)

	job := e.job("upload", "new_changed", "size_time")
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"same.txt": "upload:new", "size.txt": "upload:new", "time.txt": "upload:new", "sub/old.txt": "upload:new",
	})
	syCounts(t, pv, 4, 0, 0, 0)
	if pv.Bytes != int64(len("same")+len("aaaa")+len("tttt")+len("old")) {
		t.Errorf("Bytes = %d", pv.Bytes)
	}
	trExpect(t, e.run(pv), StatusSucceeded)
	for _, a := range pv.Actions {
		if a.Key != "sync/"+a.Path {
			t.Errorf("action %+v: wrong key", a)
		}
	}

	// Everything is unchanged right after a sync (logical size + pail-mtime).
	pv = e.preview(job)
	syCounts(t, pv, 0, 0, 0, 4)
	for _, a := range pv.Actions {
		if a.Reason != "unchanged" {
			t.Errorf("%s: %s", a.Path, a.Reason)
		}
	}

	e.write("size.txt", "aaaaXX", syBase)              // size differs
	e.write("time.txt", "tttt", syBase.Add(time.Hour)) // newer
	e.write("sub/old.txt", "old", syBase.Add(-time.Hour))
	e.write("new.txt", "n", syBase)
	e.srv.PutObject("b", "sync/gone.txt", []byte("remote only"), "", nil)

	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "upload:size differs", "time.txt": "upload:newer",
		"sub/old.txt": "upload:time differs", "new.txt": "upload:new",
	})
	syCounts(t, pv, 4, 0, 0, 1)

	// Mode new: only what does not exist yet.
	job.Mode = "new"
	syWantPlan(t, e.preview(job), map[string]string{
		"same.txt": "skip:exists", "size.txt": "skip:exists", "time.txt": "skip:exists", "sub/old.txt": "skip:exists", "new.txt": "upload:new",
	})

	// Compare size ignores time.
	job.Mode, job.Compare = "new_changed", "size"
	syWantPlan(t, e.preview(job), map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "upload:size differs", "time.txt": "skip:unchanged",
		"sub/old.txt": "skip:unchanged", "new.txt": "upload:new",
	})

	// Mirror also deletes the object nothing maps to.
	job.Mode, job.Compare = "mirror", "size_time"
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "upload:size differs", "time.txt": "upload:newer",
		"sub/old.txt": "upload:time differs", "new.txt": "upload:new", "gone.txt": "delete:not in the source",
	})
	syCounts(t, pv, 4, 0, 1, 1)
	for _, a := range pv.Actions {
		if a.Op == "delete" && (a.Key != "sync/gone.txt" || a.ETag == "") {
			t.Errorf("delete action %+v", a)
		}
	}
}

func TestSyncPreviewUploadFallsBackToLastModified(t *testing.T) {
	e := syNewEnv(t)
	// Objects written by another tool carry no pail-mtime: compare with LastModified.
	e.srv.PutObject("b", "sync/old.txt", []byte("1234"), "", nil)
	e.srv.PutObject("b", "sync/fresh.txt", []byte("1234"), "", nil)
	e.write("old.txt", "1234", time.Now().Add(-48*time.Hour))
	e.write("fresh.txt", "1234", time.Now().Add(time.Hour))
	pv := e.preview(e.job("upload", "new_changed", "size_time"))
	syWantPlan(t, pv, map[string]string{"old.txt": "skip:unchanged", "fresh.txt": "upload:newer"})
}

func TestSyncPreviewExcludesAndSymlinks(t *testing.T) {
	e := syNewEnv(t)
	e.write("keep.txt", "keep", syBase)
	e.write("a.log", "log", syBase)
	e.write("deep/b.log", "log", syBase)
	e.write("cache/x.txt", "x", syBase)
	e.write("cache/sub/y.txt", "y", syBase)
	e.write("src/cache.txt", "c", syBase)
	e.write(".pail-123.tmp", "partial", syBase)
	linkMade := os.Symlink(filepath.Join(e.dir, "keep.txt"), filepath.Join(e.dir, "link.txt")) == nil
	// Remote counterparts of excluded and skipped entries must survive a mirror.
	for _, k := range []string{"a.log", "deep/b.log", "cache/x.txt", "cache/sub/y.txt", "link.txt", "stale.txt", "cache/remote-only.txt"} {
		e.srv.PutObject("b", "sync/"+k, []byte("r"), "", nil)
	}
	job := e.job("upload", "mirror", "size_time")
	job.Excludes = []string{"*.log", "cache/"}
	pv := e.preview(job)
	want := map[string]string{
		"keep.txt": "upload:new", "src/cache.txt": "upload:new",
		"a.log": "skip:excluded", "deep/b.log": "skip:excluded", "cache/": "skip:excluded",
		".pail-123.tmp": "skip:Pail temporary file",
		"stale.txt":     "delete:not in the source",
	}
	if linkMade {
		want["link.txt"] = "skip:symbolic link (not followed)"
	}
	syWantPlan(t, pv, want)
	if !linkMade {
		t.Log("symlinks cannot be created here; symlink case not exercised")
	}

	// Only the stale object is deleted; excluded and skipped ones remain.
	trExpect(t, e.run(pv), StatusSucceeded)
	for _, k := range []string{"a.log", "deep/b.log", "cache/x.txt", "cache/sub/y.txt", "cache/remote-only.txt", "link.txt"} {
		if _, ok := e.srv.Object("b", "sync/"+k); !ok && (k != "link.txt" || linkMade) {
			t.Errorf("protected object %s was deleted", k)
		}
	}
	trMissing(t, e.trEnv, "sync/stale.txt")
	trObject(t, e.trEnv, "sync/keep.txt")
}

func TestSyncPreviewDownloadModes(t *testing.T) {
	e := syNewEnv(t)
	mt := func(d time.Duration) map[string]string {
		return map[string]string{"pail-mtime": syBase.Add(d).Format(time.RFC3339Nano)}
	}
	e.srv.PutObject("b", "sync/same.txt", []byte("same"), "", mt(0))
	e.srv.PutObject("b", "sync/size.txt", []byte("longer"), "", mt(0))
	e.srv.PutObject("b", "sync/newer.txt", []byte("1234"), "", mt(time.Hour))
	e.srv.PutObject("b", "sync/older.txt", []byte("1234"), "", mt(-time.Hour))
	e.srv.PutObject("b", "sync/new/deep.txt", []byte("d"), "", mt(0))
	e.srv.PutObject("b", "sync/folder/", nil, "", nil) // placeholder: not part of a sync
	e.write("same.txt", "same", syBase)
	e.write("size.txt", "short", syBase)
	e.write("newer.txt", "1234", syBase)
	e.write("older.txt", "1234", syBase)
	e.write("local-only.txt", "mine", syBase)

	job := e.job("download", "new_changed", "size_time")
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "download:size differs", "newer.txt": "download:newer",
		"older.txt": "skip:unchanged", "new/deep.txt": "download:new",
	})
	syCounts(t, pv, 0, 3, 0, 2)
	if pv.Bytes != int64(len("longer")+len("1234")+len("d")) {
		t.Errorf("Bytes = %d", pv.Bytes)
	}

	job.Mode = "new"
	syWantPlan(t, e.preview(job), map[string]string{
		"same.txt": "skip:exists", "size.txt": "skip:exists", "newer.txt": "skip:exists", "older.txt": "skip:exists", "new/deep.txt": "download:new",
	})

	job.Mode, job.Compare = "new_changed", "size"
	syWantPlan(t, e.preview(job), map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "download:size differs", "newer.txt": "skip:unchanged",
		"older.txt": "skip:unchanged", "new/deep.txt": "download:new",
	})

	job.Mode, job.Compare = "mirror", "size_time"
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"same.txt": "skip:unchanged", "size.txt": "download:size differs", "newer.txt": "download:newer",
		"older.txt": "skip:unchanged", "new/deep.txt": "download:new", "local-only.txt": "delete:not in the source",
	})
	for _, a := range pv.Actions {
		if a.Op == "delete" && (!a.LocalExists || a.LocalSize != 4 || a.LocalMTime != syBase.UnixNano()) {
			t.Errorf("delete action %+v", a)
		}
	}
}

func TestSyncPreviewDownloadSkipsUnsafeKeys(t *testing.T) {
	e := syNewEnv(t)
	for _, k := range []string{"ok.txt", "a/../evil.txt", "dots/./x.txt", "dbl//x.txt"} {
		e.srv.PutObject("b", "sync/"+k, []byte("x"), "", nil)
	}
	pv := e.preview(e.job("download", "new_changed", "size_time"))
	plan := syPlan(pv)
	if plan["ok.txt"] != "download:new" {
		t.Fatalf("plan = %v", plan)
	}
	for _, k := range []string{"a/../evil.txt", "dots/./x.txt", "dbl//x.txt"} {
		if !strings.HasPrefix(plan[k], "skip:cannot be stored locally") {
			t.Errorf("%s: %s", k, plan[k])
		}
	}
	syCounts(t, pv, 0, 1, 0, 3)
}

func TestSyncPreviewPaginatesRemoteListing(t *testing.T) {
	e := syNewEnv(t)
	const n = 1105 // more than one page of 1000
	for i := 0; i < n; i++ {
		e.srv.PutObject("b", fmt.Sprintf("sync/many/f%04d.txt", i), []byte("x"), "", nil)
	}
	pv := e.preview(e.job("download", "new_changed", "size"))
	syCounts(t, pv, 0, n, 0, 0)
}

// ---- checksum and upload rules ------------------------------------------------------------

func TestSyncChecksumRoundTrip(t *testing.T) {
	e := syNewEnv(t)
	e.write("a.txt", "alpha", syBase)
	e.write("b.txt", "bravo", syBase)
	job := e.job("upload", "new_changed", "checksum")
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "upload:new", "b.txt": "upload:new"})
	for _, a := range pv.Actions {
		if a.SHA256 != sySha(map[string]string{"a.txt": "alpha", "b.txt": "bravo"}[a.Path]) {
			t.Errorf("%s: SHA256 %q", a.Path, a.SHA256)
		}
	}
	trExpect(t, e.run(pv), StatusSucceeded)
	m := syMeta(t, e, "sync/a.txt")
	if m["pail-sha256"] != sySha("alpha") || m["pail-size"] != "5" || m["pail-mtime"] == "" {
		t.Fatalf("metadata = %v", m)
	}

	// Second preview: all unchanged, even though the times are touched.
	e.write("a.txt", "alpha", syBase.Add(time.Hour))
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "skip:unchanged", "b.txt": "skip:unchanged"})

	// Same size, different content.
	e.write("b.txt", "BRAVO", syBase)
	// A remote object without checksum metadata (another tool) is "changed".
	e.srv.PutObject("b", "sync/c.txt", []byte("charlie"), "", nil)
	e.write("c.txt", "charlie", syBase)
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{
		"a.txt": "skip:unchanged", "b.txt": "upload:checksum differs", "c.txt": "upload:no checksum metadata",
	})

	// Without checksum compare, no SHA-256 is computed or written.
	job.Compare = "size_time"
	for _, a := range e.preview(job).Actions {
		if a.SHA256 != "" {
			t.Errorf("%s carries a hash outside checksum mode", a.Path)
		}
	}
}

func TestSyncChecksumDownload(t *testing.T) {
	e := syNewEnv(t)
	meta := func(content string) map[string]string {
		return map[string]string{"pail-sha256": sySha(content), "pail-mtime": syBase.Format(time.RFC3339Nano)}
	}
	e.srv.PutObject("b", "sync/same.txt", []byte("same"), "", meta("same"))
	e.srv.PutObject("b", "sync/diff.txt", []byte("abcd"), "", meta("abcd"))
	e.srv.PutObject("b", "sync/nometa.txt", []byte("abcd"), "", nil)
	e.write("same.txt", "same", syBase)
	e.write("diff.txt", "ABCD", syBase)
	e.write("nometa.txt", "abcd", syBase)
	pv := e.preview(e.job("download", "new_changed", "checksum"))
	syWantPlan(t, pv, map[string]string{
		"same.txt": "skip:unchanged", "diff.txt": "download:checksum differs", "nometa.txt": "download:no checksum metadata",
	})
}

func TestSyncUploadRulesMapKeysAndCompareLogicalSize(t *testing.T) {
	e := syNewEnv(t)
	if err := e.s.SaveUploadRules([]rules.UploadRule{
		{Name: "zip", KeyPattern: "*.txt", Compression: &rules.Compression{Method: "zip", Level: -1}},
		{Name: "gz", KeyPattern: "*.log", Compression: &rules.Compression{Method: "gzip", Level: -1}},
	}); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("compressible line\n", 200)
	e.write("a.txt", body, syBase)
	e.write("d/b.log", body, syBase)
	e.write("c.bin", "plain", syBase)
	job := e.job("upload", "mirror", "size_time")
	pv := e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "upload:new", "d/b.log": "upload:new", "c.bin": "upload:new"})
	keys := map[string]string{}
	for _, a := range pv.Actions {
		keys[a.Path] = a.Key
		if a.Path == "a.txt" && a.Size != int64(len(body)) {
			t.Errorf("Size is %d, want the logical size", a.Size)
		}
	}
	if keys["a.txt"] != "sync/a.txt.zip" || keys["d/b.log"] != "sync/d/b.log" || keys["c.bin"] != "sync/c.bin" {
		t.Fatalf("keys = %v", keys)
	}
	trExpect(t, e.run(pv), StatusSucceeded)
	if raw, ok := e.srv.Object("b", "sync/a.txt.zip"); !ok || len(raw) >= len(body) {
		t.Fatalf("zip object missing or not compressed (%d bytes)", len(raw))
	}
	if m := syMeta(t, e, "sync/a.txt.zip"); m["pail-size"] != strconv.Itoa(len(body)) {
		t.Errorf("pail-size = %q", m["pail-size"])
	}
	// Round trip converges although the stored sizes differ from the local ones;
	// the .zip object is not extraneous in mirror mode.
	pv = e.preview(job)
	syWantPlan(t, pv, map[string]string{"a.txt": "skip:unchanged", "d/b.log": "skip:unchanged", "c.bin": "skip:unchanged"})

	// A real size change is seen through the compression.
	e.write("a.txt", body+"more\n", syBase)
	e.write("d/b.log", body, syBase.Add(time.Hour))
	syWantPlan(t, e.preview(job), map[string]string{"a.txt": "upload:size differs", "d/b.log": "upload:newer", "c.bin": "skip:unchanged"})

	// NoRules maps keys 1:1 and ignores the compression rules.
	job.NoRules = true
	pv = e.preview(job)
	for _, a := range pv.Actions {
		if a.Op == "upload" && a.Key != "sync/"+a.Path {
			t.Errorf("NoRules key %q for %q", a.Key, a.Path)
		}
	}
	// ... and then the .zip object from before is extraneous.
	if got := syPlan(pv)["a.txt.zip"]; got != "delete:not in the source" {
		t.Errorf("a.txt.zip: %q", got)
	}
}

func TestSyncUploadRuleKeyCollision(t *testing.T) {
	e := syNewEnv(t)
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "zip", KeyPattern: "*.txt", Compression: &rules.Compression{Method: "zip", Level: -1}}}); err != nil {
		t.Fatal(err)
	}
	e.write("a.txt", "x", syBase)
	e.write("a.txt.zip", "y", syBase) // would map to the same key as a.txt
	pv := e.preview(e.job("upload", "new_changed", "size_time"))
	plan := syPlan(pv)
	if plan["a.txt"] != "upload:new" || !strings.HasPrefix(plan["a.txt.zip"], "skip:maps to the same object as a.txt") {
		t.Fatalf("plan = %v", plan)
	}
}

func TestSyncPreviewErrors(t *testing.T) {
	e := syNewEnv(t)
	job := e.job("upload", "new_changed", "size_time")
	job.LocalDir = filepath.Join(e.dir, "missing")
	if _, err := e.s.PreviewSync(testCtx(t), job); !IsKind(err, ErrInvalid) {
		t.Errorf("missing dir: %v", err)
	}
	job = e.job("upload", "new_changed", "size_time")
	job.Remote.AccountID = "nope"
	if _, err := e.s.PreviewSync(testCtx(t), job); err == nil {
		t.Error("unknown account must fail")
	}
	job = e.job("upload", "new_changed", "size_time")
	job.Remote.Bucket = "no-such-bucket"
	if _, err := e.s.PreviewSync(testCtx(t), job); !IsKind(err, ErrNotFound) {
		t.Errorf("missing bucket: %v", err)
	}
}
