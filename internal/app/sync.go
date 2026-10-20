package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/DejavuMoe/Pail/internal/safepath"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// Sync (APP_API.md section 9). Layout:
//
//	sync.go         saved jobs and PreviewSync (the plan)
//	syncscan.go     local walk, remote listing, metadata, hashing, exclusions
//	synccompare.go  the pure comparison rules
//	syncrun.go      RunSync: the plan becomes ordinary queue tasks
//
// A sync is one-way (upload: local to remote, download: remote to local) and has
// two steps. PreviewSync reads both sides and returns the exact list of actions
// with everything it observed; it changes nothing. RunSync executes exactly
// those actions as normal upload, download and delete tasks of one group, and
// every task re-checks its precondition (see syncrun.go).
//
// Scope and limits of v1:
//   - Only regular files are synchronised. Symbolic links, junctions and other
//     irregular entries are reported as skipped. Empty directories and folder
//     placeholder objects ("name/") are neither transferred nor deleted.
//   - Downloads copy objects byte for byte: a compressed object (gzip, ZIP) is
//     not decoded, so it arrives with its ".zip" suffix and compressed content.
//     In the upload direction the upload rules decide the object key and
//     compression, and the logical (uncompressed) size is compared.
//   - Remote deletes of mirror mode are plain-key deletes after an ETag check:
//     a versioned bucket receives a delete marker, old versions stay.
//   - On case-insensitive local file systems (Windows, macOS) names that differ
//     only by case are treated as the same file.

const syncJobsFile = "sync-jobs.json"

// SyncJobs returns a copy of the saved sync jobs.
func (s *Service) SyncJobs() []SyncJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSyncJobs(s.syncJobs)
}

func cloneSyncJobs(in []SyncJob) []SyncJob {
	out := make([]SyncJob, len(in))
	for i, j := range in {
		j.Excludes = append([]string(nil), j.Excludes...)
		out[i] = j
	}
	return out
}

// SaveSyncJob validates and stores a job. An empty ID creates a job and assigns
// its ID; a known ID replaces the job; an unknown ID is an error. Empty Mode
// and Compare mean "new_changed" and "size_time" (mirror, which deletes, is
// never a default); Direction is required.
func (s *Service) SaveSyncJob(job SyncJob) (SyncJob, error) {
	const op = "SaveSyncJob"
	job, err := syNormalize(op, job)
	if err != nil {
		return SyncJob{}, err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	next := cloneSyncJobs(s.syncJobs)
	s.mu.Unlock()
	if job.ID == "" {
		for {
			job.ID = syNewID()
			if syFindJob(next, job.ID) < 0 {
				break
			}
		}
		next = append(next, job)
	} else if i := syFindJob(next, job.ID); i >= 0 {
		next[i] = job
	} else {
		return SyncJob{}, opErr(op, ErrNotFound, "no sync job %q", job.ID)
	}
	if err := writeJSONAtomic(s.path(syncJobsFile), next); err != nil {
		return SyncJob{}, classify(op, err)
	}
	s.mu.Lock()
	s.syncJobs = next
	s.mu.Unlock()
	job.Excludes = append([]string(nil), job.Excludes...)
	return job, nil
}

// DeleteSyncJob removes a saved job. Nothing at either side is touched.
func (s *Service) DeleteSyncJob(id string) error {
	const op = "DeleteSyncJob"
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	cur := cloneSyncJobs(s.syncJobs)
	s.mu.Unlock()
	i := syFindJob(cur, id)
	if i < 0 {
		return opErr(op, ErrNotFound, "no sync job %q", id)
	}
	next := append(cur[:i:i], cur[i+1:]...)
	if err := writeJSONAtomic(s.path(syncJobsFile), next); err != nil {
		return classify(op, err)
	}
	s.mu.Lock()
	s.syncJobs = next
	s.mu.Unlock()
	return nil
}

func syFindJob(jobs []SyncJob, id string) int {
	for i, j := range jobs {
		if j.ID == id {
			return i
		}
	}
	return -1
}

func syNewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sync-" + hex.EncodeToString(b[:])
}

// syNormalize validates job and fills the defaults. It never touches the ID.
func syNormalize(op string, j SyncJob) (SyncJob, error) {
	if strings.TrimSpace(j.LocalDir) == "" {
		return j, invalid(op, "the local folder is required")
	}
	if !filepath.IsAbs(j.LocalDir) {
		return j, invalid(op, "the local folder %q must be an absolute path", j.LocalDir)
	}
	j.LocalDir = filepath.Clean(j.LocalDir)
	fi, err := os.Stat(j.LocalDir)
	if err != nil {
		return j, invalid(op, "the local folder: %v", err)
	}
	if !fi.IsDir() {
		return j, invalid(op, "%q is not a folder", j.LocalDir)
	}
	if err := checkLocation(op, j.Remote, true); err != nil {
		return j, err
	}
	switch j.Direction {
	case syUp, syDown:
	default:
		return j, invalid(op, "direction must be %q or %q, not %q", syUp, syDown, j.Direction)
	}
	if j.Mode == "" {
		j.Mode = syModeNewChanged
	}
	switch j.Mode {
	case syModeNew, syModeNewChanged, syModeMirror:
	default:
		return j, invalid(op, "unknown sync mode %q", j.Mode)
	}
	if j.Compare == "" {
		j.Compare = syCmpSizeTime
	}
	switch j.Compare {
	case syCmpSizeTime, syCmpSize, syCmpChecksum:
	default:
		return j, invalid(op, "unknown compare rule %q", j.Compare)
	}
	j.Name = strings.TrimSpace(j.Name)
	if j.Name == "" {
		j.Name = filepath.Base(j.LocalDir)
	}
	for _, p := range j.Excludes {
		switch {
		case p == "" || p == "/":
			return j, invalid(op, "an empty exclude pattern would match everything")
		case strings.ContainsAny(p, "\x00\r\n"):
			return j, invalid(op, "exclude pattern %q contains control characters", p)
		case strings.Contains(p, `\`):
			return j, invalid(op, "exclude pattern %q: use \"/\" as the path separator", p)
		case strings.HasPrefix(p, "/"):
			return j, invalid(op, "exclude pattern %q must be relative to the sync folder (no leading \"/\")", p)
		}
	}
	j.Excludes = append([]string(nil), j.Excludes...)
	return j, nil
}

// syFold is the key under which the names of the local file system compare:
// case-insensitive on Windows and macOS.
func syFold(rel string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(rel)
	}
	return rel
}

// ---- preview ------------------------------------------------------------------------------

// syItem is one file that exists on the source side (or, for uploads, the pair
// of a local file and the object its key maps to).
type syItem struct {
	l          *syLocal
	r          *syRemote
	rel        string // reported path
	key        string // upload: physical object key; download: the object key
	compressed bool

	needHead, needHash bool
	meta               map[string]string
	metaErr            error
	sha                string
	hashErr            error
}

type syPlanner struct {
	s     *Service
	c     *storage.Client
	job   SyncJob
	cmp   syCmp
	ex    *syExcluder
	local *syLocalScan
	rem   []syRemote

	acts  []SyncAction
	skipd map[string]bool
}

// skip adds a skip action once per path.
func (p *syPlanner) skip(path, reason string, size int64) {
	if p.skipd[path] {
		return
	}
	p.skipd[path] = true
	p.acts = append(p.acts, SyncAction{Op: "skip", Path: path, Reason: reason, Size: size})
}

// PreviewSync reads both sides of job and returns the actions a sync would
// perform. It writes nothing and reads file contents only in checksum mode.
// The job need not be saved. A local folder that cannot be read completely is
// an error, never a partial plan: a partial listing must not drive deletions.
func (s *Service) PreviewSync(ctx context.Context, job SyncJob) (SyncPreview, error) {
	const op = "PreviewSync"
	job, err := syNormalize(op, job)
	if err != nil {
		return SyncPreview{}, err
	}
	c, err := s.bucketClient(ctx, op, job.Remote)
	if err != nil {
		return SyncPreview{}, err
	}
	p := &syPlanner{
		s: s, c: c, job: job, skipd: map[string]bool{},
		cmp: syCmp{Upload: job.Direction == syUp, Mode: job.Mode, Compare: job.Compare},
		ex:  newSyExcluder(job.Excludes),
	}
	if p.local, err = syScanLocal(ctx, job.LocalDir, p.ex); err != nil {
		return SyncPreview{}, classify(op, err)
	}
	if p.rem, err = syListRemote(ctx, c, job.Remote); err != nil {
		return SyncPreview{}, classify(op, err)
	}
	for _, sk := range p.local.skips {
		p.skip(sk.Path, sk.Reason, 0)
	}

	var items []*syItem
	if p.cmp.Upload {
		items, err = p.planUpload()
	} else {
		items, err = p.planDownload()
	}
	if err != nil {
		return SyncPreview{}, err
	}
	if err := p.gather(ctx, items); err != nil {
		return SyncPreview{}, classify(op, err)
	}
	p.decide(items)

	sort.SliceStable(p.acts, func(i, j int) bool {
		if p.acts[i].Path != p.acts[j].Path {
			return p.acts[i].Path < p.acts[j].Path
		}
		return p.acts[i].Op < p.acts[j].Op
	})
	pv := SyncPreview{Job: job, Actions: p.acts}
	for _, a := range p.acts {
		switch a.Op {
		case "upload":
			pv.Uploads++
			pv.Bytes += a.Size
		case "download":
			pv.Downloads++
			pv.Bytes += a.Size
		case "delete":
			pv.Deletes++
		default:
			pv.Skips++
		}
	}
	s.lgInfo("sync preview", "job", job.ID, "direction", job.Direction, "uploads", pv.Uploads, "downloads", pv.Downloads, "deletes", pv.Deletes, "skips", pv.Skips, "bytes", pv.Bytes)
	return pv, nil
}

// planUpload maps every local file to its object key through the upload rules
// and pairs it with the existing object. In mirror mode the objects no local
// file maps to become delete actions.
func (p *syPlanner) planUpload() ([]*syItem, error) {
	rem := make(map[string]*syRemote, len(p.rem))
	for i := range p.rem {
		rem[p.rem[i].Rel] = &p.rem[i]
	}
	list := p.s.UploadRules()
	prefix, bucket := p.job.Remote.Prefix, p.job.Remote.Bucket
	used := map[string]bool{} // object rels that belong to a local entry
	protect := func(rel string) { used[rel], used[rel+".zip"] = true, true }
	owner := map[string]string{} // object key -> local rel
	var items []*syItem
	for i := range p.local.files {
		l := &p.local.files[i]
		key := prefix + l.Rel
		if len(key) > maxKeyBytes {
			protect(l.Rel)
			p.skip(l.Rel, "object key is longer than 1024 bytes", l.Size)
			continue
		}
		phys, comp, why, err := syPhysicalKey(list, bucket, key, p.job.NoRules)
		if err != nil {
			return nil, err
		}
		if why == "" && len(phys) > maxKeyBytes {
			why = "object key is longer than 1024 bytes"
		}
		if why != "" {
			protect(l.Rel)
			p.skip(l.Rel, why, l.Size)
			continue
		}
		if other, dup := owner[phys]; dup {
			protect(l.Rel)
			p.skip(l.Rel, "maps to the same object as "+other, l.Size)
			continue
		}
		owner[phys] = l.Rel
		physRel := phys[len(prefix):]
		used[physRel] = true
		items = append(items, &syItem{l: l, r: rem[physRel], rel: l.Rel, key: phys, compressed: comp})
	}
	for rel := range p.local.protected {
		protect(rel)
	}
	if p.job.Mode != syModeMirror {
		return items, nil
	}
	for i := range p.rem {
		r := &p.rem[i]
		if used[r.Rel] {
			continue
		}
		if path, ok := p.ex.path(r.Rel); ok {
			p.skip(path, "excluded", 0)
			continue
		}
		if base, ok := strings.CutSuffix(r.Rel, ".zip"); ok {
			if path, ok := p.ex.path(base); ok {
				p.skip(path, "excluded", 0)
				continue
			}
		}
		p.acts = append(p.acts, SyncAction{Op: "delete", Path: r.Rel, Reason: "not in the source", Size: r.Size, Key: r.Key, ETag: r.ETag})
	}
	return items, nil
}

// planDownload pairs every usable object with the local file of the same
// relative path. In mirror mode local files without an object become delete
// actions (moved to the trash by RunSync).
func (p *syPlanner) planDownload() ([]*syItem, error) {
	loc := make(map[string]*syLocal, len(p.local.files))
	for i := range p.local.files {
		loc[syFold(p.local.files[i].Rel)] = &p.local.files[i]
	}
	used := map[string]bool{} // folded rels that belong to an object
	var items []*syItem
	for i := range p.rem {
		r := &p.rem[i]
		fold := syFold(r.Rel)
		if path, ok := p.ex.path(r.Rel); ok {
			used[fold] = true
			p.skip(path, "excluded", 0)
			continue
		}
		if used[fold] {
			p.skip(r.Rel, "same local name as another object (case-insensitive file system)", r.Size)
			continue
		}
		used[fold] = true
		if err := safepath.ValidKey(r.Rel); err != nil {
			p.skip(r.Rel, "cannot be stored locally: "+err.Error(), r.Size)
			continue
		}
		if _, err := safepath.Join(p.job.LocalDir, r.Rel); err != nil {
			p.skip(r.Rel, "cannot be stored locally: "+err.Error(), r.Size)
			continue
		}
		items = append(items, &syItem{l: loc[fold], r: r, rel: r.Rel, key: r.Key})
	}
	if p.job.Mode != syModeMirror {
		return items, nil
	}
	for i := range p.local.files {
		l := &p.local.files[i]
		if used[syFold(l.Rel)] {
			continue
		}
		p.acts = append(p.acts, SyncAction{
			Op: "delete", Path: l.Rel, Reason: "not in the source", Size: l.Size,
			LocalExists: true, LocalSize: l.Size, LocalMTime: l.MTime.UnixNano(),
		})
	}
	return items, nil
}

func (it *syItem) localInfo() *syLocalInfo {
	if it.l == nil {
		return nil
	}
	return &syLocalInfo{Size: it.l.Size, MTime: it.l.MTime, SHA: it.sha}
}

func (it *syItem) remoteInfo() *syRemoteInfo {
	if it.r == nil {
		return nil
	}
	return &syRemoteInfo{Size: it.r.Size, Modified: it.r.Modified, Meta: it.meta, Compressed: it.compressed}
}

// gather fetches the metadata and hashes the plan needs, with bounded
// parallelism. Failures are kept per item and become skips in decide.
func (p *syPlanner) gather(ctx context.Context, items []*syItem) error {
	checksum := p.cmp.Compare == syCmpChecksum
	var work []*syItem
	for _, it := range items {
		it.needHead = syNeedsHead(p.cmp, it.localInfo(), it.remoteInfo())
		if checksum && it.l != nil {
			if p.cmp.Upload {
				it.needHash = !(it.r != nil && p.cmp.Mode == syModeNew)
			} else {
				it.needHash = it.r != nil && p.cmp.Mode != syModeNew && it.r.Size == it.l.Size
			}
		}
		if it.needHead || it.needHash {
			work = append(work, it)
		}
	}
	syParallel(ctx, len(work), 8, func(i int) {
		it := work[i]
		if it.needHead {
			it.meta, it.metaErr = syHead(ctx, p.c, p.job.Remote.Bucket, it.r.Key)
		}
		if it.needHash {
			it.sha, it.hashErr = syHashFile(ctx, *it.l)
		}
	})
	return ctx.Err()
}

// decide turns the gathered items into actions.
func (p *syPlanner) decide(items []*syItem) {
	for _, it := range items {
		size := int64(0)
		switch {
		case it.l != nil && p.cmp.Upload:
			size = it.l.Size
		case it.r != nil:
			size = it.r.Size
		}
		if it.needHash && it.hashErr != nil {
			p.skip(it.rel, "cannot read the local file: "+it.hashErr.Error(), size)
			continue
		}
		if it.needHead && it.metaErr != nil && (p.cmp.Upload || it.l != nil) {
			p.skip(it.rel, "cannot read the remote metadata: "+classify("", it.metaErr).Error(), size)
			continue
		}
		if p.cmp.Upload && it.r != nil && it.r.ETag == "" && p.cmp.Mode != syModeNew {
			p.skip(it.rel, "the remote object has no ETag to guard the overwrite", size)
			continue
		}
		cp, reason := syDecide(p.cmp, it.localInfo(), it.remoteInfo())
		if !cp {
			p.skip(it.rel, reason, size)
			continue
		}
		a := SyncAction{Path: it.rel, Reason: reason, Size: size, Key: it.key}
		if it.l != nil {
			a.LocalExists, a.LocalSize, a.LocalMTime = true, it.l.Size, it.l.MTime.UnixNano()
		}
		if p.cmp.Upload {
			a.Op = "upload"
			if it.r != nil {
				a.ETag = it.r.ETag
			}
			a.SHA256 = it.sha
		} else {
			a.Op = "download"
			a.ETag = it.r.ETag
			a.RemoteTime = syRemoteTime(it.remoteInfo()).UnixNano()
		}
		p.acts = append(p.acts, a)
	}
}

// ---- guards shared with upload.go / download.go ------------------------------------

// syncUploadPre is what a sync upload task carries from the preview.
type syncUploadPre struct {
	Key   string            // the object key the preview computed (after upload rules)
	ETag  string            // "" = the key did not exist: create-only
	Size  int64             // local size observed
	MTime int64             // local modification time observed, UnixNano
	Meta  map[string]string // pail-size, pail-sha256
}

// syncDownloadPre is the local state a sync download may replace.
type syncDownloadPre struct {
	Exists bool
	Size   int64
	MTime  int64
}

// localChanged reports whether the local path is no longer in the state the
// preview saw: absent vs present, not a regular file, or a different size or time.
func (p *syncDownloadPre) localChanged(local string) (bool, error) {
	fi, err := os.Lstat(local)
	if errors.Is(err, fs.ErrNotExist) {
		return p.Exists, nil
	}
	if err != nil {
		return false, err
	}
	if !p.Exists || !fi.Mode().IsRegular() {
		return true, nil
	}
	return fi.Size() != p.Size || fi.ModTime().UnixNano() != p.MTime, nil
}

func syEtagEqual(a, b string) bool {
	norm := func(s string) string { return strings.Trim(strings.TrimPrefix(s, "W/"), `"`) }
	return norm(a) == norm(b)
}

// syncUploadDecision re-checks the destination of a sync upload with a HEAD
// (the conditional header sent with the write is a second guard, see caps.go):
// a new key must still be absent, a changed one must still carry the ETag the
// preview saw.
func syncUploadDecision(ctx context.Context, c *storage.Client, bucket, key string, pre *syncUploadPre) (conflictDecision, error) {
	const op = "Upload"
	t, err := headTarget(ctx, c, bucket, key)
	if err != nil {
		return conflictDecision{}, err
	}
	switch {
	case pre.ETag == "" && t.exists:
		return conflictDecision{}, opErr(op, ErrConflict, "%q was created since the sync preview; nothing was overwritten", key)
	case pre.ETag == "":
		return conflictDecision{Key: key, CreateOnly: true}, nil
	case !t.exists:
		return conflictDecision{}, opErr(op, ErrConflict, "%q was deleted since the sync preview; nothing was written", key)
	case !syEtagEqual(t.etag, pre.ETag):
		return conflictDecision{}, opErr(op, ErrConflict, "%q changed since the sync preview; nothing was overwritten", key)
	}
	return conflictDecision{Key: key, IfMatch: pre.ETag}, nil
}
