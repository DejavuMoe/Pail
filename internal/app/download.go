package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/safepath"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// downloadJob is the spec of one per-file download task. Rel is the local path
// below DestDir (already carrying a version suffix for version downloads).
type downloadJob struct {
	Src         Location
	Key         string
	VersionID   string
	ETag        string
	Size        int64
	Modified    time.Time
	DestDir     string
	Rel         string
	Policy      ConflictPolicy
	Decode      DecodeMode
	PasswordRef string
	MaxDecoded  int64
	// Note is reported with the succeeded item (e.g. the scan gave the file a
	// numbered name because of a case-insensitive collision).
	Note string
	// Sync is set by RunSync (syncrun.go): the download then publishes only if
	// the local file is still in the state the preview observed.
	Sync *syncDownloadPre
}

// Download downloads objects, folders, specific versions or a whole bucket into
// req.DestDir and returns the group ID of the tasks it creates. It returns as
// soon as the request is validated; a scan task (KindOther) expands the
// selection and submits one KindDownload task per file.
//
// The local path of an object is DestDir joined with its key relative to
// Source.Prefix (the full key for WholeBucket). Versions get " (v<first 8
// characters of the version id>)" before the extension. Folder placeholder keys
// ("name/") create directories only. Keys the local file system cannot store
// faithfully (".." segments, drive letters, reserved Windows names, symlinks on
// the way ...) are reported as failed items, never written.
//
// On Windows and macOS, keys whose paths differ only in letter case would share
// one file: the scan gives the later ones a numbered name whatever the policy.
// Under "rename" a local file identical to the object (same size, mtime within
// 2 s of LastModified) is skipped rather than numbered, so rescans are idempotent.
//
// Each file is downloaded to a hidden temporary file next to its destination
// with If-Match set to the ETag found by the scan, optionally decoded, and
// published according to the conflict policy. A failure or cancellation removes
// the temporary files; a partial file is never published. An empty req.Conflict
// means the default from Settings; an empty DestDir means Settings'
// DefaultDownloadDir.
func (s *Service) Download(req DownloadRequest) (groupID string, err error) {
	const op = "Download"
	if err := checkLocation(op, req.Source, true); err != nil {
		return "", err
	}
	if err := s.requireKnownAccount(op, req.Source.AccountID); err != nil {
		return "", err
	}
	if !req.WholeBucket && len(req.Keys) == 0 && len(req.Versions) == 0 {
		return "", invalid(op, "nothing selected to download")
	}
	for _, k := range req.Keys {
		if k == "" {
			return "", invalid(op, "empty object key")
		}
	}
	for _, v := range req.Versions {
		if v.Key == "" || v.VersionID == "" {
			return "", invalid(op, "a version needs both a key and a version id")
		}
	}
	if req.DestDir == "" {
		req.DestDir = s.Settings().DefaultDownloadDir
	}
	if req.DestDir == "" {
		return "", invalid(op, "no destination folder given and no default download folder configured")
	}
	abs, err := filepath.Abs(req.DestDir)
	if err != nil {
		return "", invalid(op, "%v", err)
	}
	req.DestDir = abs
	if req.Conflict, err = s.transferPolicy(op, req.Conflict); err != nil {
		return "", err
	}
	switch req.Decode {
	case DecodeRaw, DecodeGzip, DecodeZip, DecodeAuto:
	default:
		return "", invalid(op, "unknown decode mode %q", req.Decode)
	}
	if req.MaxDecoded < 0 {
		return "", invalid(op, "the decoded size limit must not be negative")
	}
	if req.MaxDecoded == 0 {
		req.MaxDecoded = defaultMaxDecoded
	}
	if req.PasswordRef != "" {
		if err := validRefName(req.PasswordRef); err != nil {
			return "", invalid(op, "%v", err)
		}
	}
	group := s.newGroupID()
	s.submit(s.downloadScanSpec(group, req, "Scan"))
	return group, nil
}

// scanUnit is one selection of a download request: what the scan task handles
// (and reports as unprocessed on cancellation).
type scanUnit struct {
	Item      string // ItemResult.Key
	Key       string
	VersionID string
	Whole     bool
	Folder    bool
}

func (u scanUnit) id() string { return u.Item + "\x00" + u.VersionID }

func downloadUnits(req DownloadRequest) []scanUnit {
	if req.WholeBucket {
		return []scanUnit{{Item: "s3://" + req.Source.Bucket + "/", Whole: true}}
	}
	var us []scanUnit
	for _, k := range req.Keys {
		us = append(us, scanUnit{Item: k, Key: k, Folder: strings.HasSuffix(k, "/")})
	}
	for _, v := range req.Versions {
		us = append(us, scanUnit{Item: v.Key, Key: v.Key, VersionID: v.VersionID})
	}
	return us
}

// downloadScanSpec is the spec of the scan task of req. Retry rescans the units
// whose items failed or were left unprocessed.
func (s *Service) downloadScanSpec(group string, req DownloadRequest, verb string) taskSpec {
	const op = "Download"
	units := downloadUnits(req)
	var (
		holdMu sync.Mutex
		last   *remoteScan
	)
	spec := taskSpec{
		Kind: KindOther, Group: group,
		Title:  fmt.Sprintf("%s %d selection(s) for download", verb, len(units)),
		Detail: "s3://" + req.Source.Bucket + "/" + req.Source.Prefix + " → " + req.DestDir,
	}
	spec.Work = func(ctx context.Context, r *run) error {
		c, err := s.bucketClient(ctx, op, req.Source)
		if err != nil {
			return err
		}
		ds := &remoteScan{s: s, r: r, ctx: ctx, c: c, req: req, group: group, retry: map[string]scanUnit{}, names: newNameClaims(runtime.GOOS)}
		if !req.WholeBucket {
			ds.prefix = req.Source.Prefix
		}
		holdMu.Lock()
		last = ds
		holdMu.Unlock()
		for i, u := range units {
			if err := ds.unit(u); err != nil {
				for _, rest := range units[i:] {
					ds.remember(rest)
					r.Item(ItemResult{Key: rest.Item, VersionID: rest.VersionID, Status: ItemUnprocessed, Err: "not scanned: canceled"})
				}
				return err
			}
		}
		ds.mu.Lock()
		n := ds.files
		ds.mu.Unlock()
		r.SetDetail(scanDetail(n))
		return nil
	}
	spec.Retry = func(failed []ItemResult) (taskSpec, bool) {
		holdMu.Lock()
		prev := last
		holdMu.Unlock()
		if prev == nil {
			return taskSpec{}, false
		}
		prev.mu.Lock()
		next := req
		next.Keys, next.Versions, next.WholeBucket = nil, nil, false
		seen := map[string]bool{}
		for _, it := range failed {
			u, ok := prev.retry[it.Key+"\x00"+it.VersionID]
			if !ok || seen[u.id()] {
				continue
			}
			seen[u.id()] = true
			switch {
			case u.Whole:
				next.WholeBucket = true
			case u.VersionID != "":
				next.Versions = append(next.Versions, ObjectRef{Key: u.Key, VersionID: u.VersionID})
			default:
				next.Keys = append(next.Keys, u.Key)
			}
		}
		prev.mu.Unlock()
		if !next.WholeBucket && len(next.Keys) == 0 && len(next.Versions) == 0 {
			return taskSpec{}, false
		}
		return s.downloadScanSpec(group, next, "Rescan"), true
	}
	return spec
}

// remoteScan expands the selection of a download into per-file tasks.
type remoteScan struct {
	s      *Service
	r      *run
	ctx    context.Context
	c      *storage.Client
	req    DownloadRequest
	group  string
	prefix string // keys are made relative to it ("" for whole-bucket downloads)

	names *nameClaims // local names handed out by this scan (case-insensitive file systems)

	mu    sync.Mutex
	retry map[string]scanUnit
	files int
}

// collisionNote is the item note of a file renamed because its local path
// equals another file's path except for letter case.
const collisionNote = "renamed: case-insensitive name collision"

// nameClaims hands out the local relative paths of one download scan. On
// case-insensitive file systems (Windows, macOS) two keys that differ only in
// letter case map to the same file, so the later claimant gets a numbered name,
// whatever the conflict policy says. On other systems claim is the identity.
type nameClaims struct {
	fold   bool
	owners map[string]string // folded path -> owner that holds it
}

func newNameClaims(goos string) *nameClaims {
	return &nameClaims{fold: goos == "windows" || goos == "darwin", owners: map[string]string{}}
}

// claim returns the path owner may use for rel: rel itself, or the first
// numbered variant ("a (1).txt") that no other owner holds. The same owner
// claiming the same path twice (a key selected twice) keeps it; the conflict
// policy decides what happens then.
func (c *nameClaims) claim(rel, owner string) string {
	if !c.fold {
		return rel
	}
	k := strings.ToLower(rel)
	if o, taken := c.owners[k]; !taken {
		c.owners[k] = owner
		return rel
	} else if o == owner {
		return rel
	}
	for n := 1; ; n++ {
		cand := numberedKey(rel, n)
		if _, taken := c.owners[strings.ToLower(cand)]; !taken {
			c.owners[strings.ToLower(cand)] = owner
			return cand
		}
	}
}

func (ds *remoteScan) remember(u scanUnit) {
	ds.mu.Lock()
	ds.retry[u.id()] = u
	ds.mu.Unlock()
}

// unit scans one selection. It returns an error only when the task context
// ended; every other problem is a failed item.
func (ds *remoteScan) unit(u scanUnit) error {
	if err := ds.ctx.Err(); err != nil {
		return err
	}
	var err error
	switch {
	case u.Whole:
		err = scanList(ds.ctx, ds.c, ds.req.Source.Bucket, "", func(o remoteObject) error { return ds.object(o, false) })
	case u.Folder:
		err = scanList(ds.ctx, ds.c, ds.req.Source.Bucket, u.Key, func(o remoteObject) error { return ds.object(o, false) })
	default:
		var ro remoteObject
		if ro, err = headRemote(ds.ctx, ds.c, ds.req.Source.Bucket, u.Key, u.VersionID); err == nil {
			err = ds.object(ro, u.VersionID != "")
		}
	}
	if cerr := ds.ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		ds.remember(u)
		ds.r.Item(ItemResult{Key: u.Item, VersionID: u.VersionID, Status: ItemFailed, Err: err.Error()})
	}
	return nil
}

// object handles one discovered object: a folder placeholder creates a
// directory, anything else becomes a download task. A returned error ends the
// listing of the current unit (only the task context ending does that;
// per-object problems are reported as items).
func (ds *remoteScan) object(o remoteObject, versioned bool) error {
	if err := ds.ctx.Err(); err != nil {
		return err
	}
	rel := relativeKey(o.Key, ds.prefix)
	if rel == "" {
		return nil // the selected folder's own placeholder object
	}
	if strings.HasSuffix(rel, "/") {
		dir, err := localJoin("Download", ds.req.DestDir, strings.TrimSuffix(rel, "/"))
		if err == nil {
			err = os.MkdirAll(dir, 0o755)
		}
		if err != nil {
			ds.r.Item(ItemResult{Key: o.Key, Status: ItemFailed, Err: err.Error()})
			return nil
		}
		ds.r.Item(ItemResult{Key: o.Key, Status: ItemSucceeded})
		return nil
	}
	if versioned {
		rel = versionedKey(rel, o.VersionID)
	}
	if _, err := localJoin("Download", ds.req.DestDir, rel); err != nil {
		ds.r.Item(ItemResult{Key: o.Key, VersionID: o.VersionID, Status: ItemFailed, Err: err.Error()})
		return nil
	}
	note := ""
	if claimed := ds.names.claim(rel, o.Key+"\x00"+o.VersionID); claimed != rel {
		rel, note = claimed, collisionNote
	}
	if err := ds.s.waitGroupBacklog(ds.ctx, ds.group, ds.s.backlogMax(KindDownload)); err != nil {
		return err
	}
	ds.s.submit(ds.s.downloadTaskSpec(ds.group, downloadJob{
		Src: ds.req.Source, Key: o.Key, VersionID: o.VersionID, ETag: o.ETag, Size: o.Size, Modified: o.Modified,
		DestDir: ds.req.DestDir, Rel: rel, Policy: ds.req.Conflict, Decode: ds.req.Decode,
		PasswordRef: ds.req.PasswordRef, MaxDecoded: ds.req.MaxDecoded, Note: note,
	}))
	ds.mu.Lock()
	ds.files++
	n := ds.files
	ds.mu.Unlock()
	if n%50 == 0 {
		ds.r.SetDetail(scanDetail(n))
	}
	return nil
}

// downloadTaskSpec is the spec of the task that downloads one file.
func (s *Service) downloadTaskSpec(group string, j downloadJob) taskSpec {
	dir := j.DestDir
	if d := path.Dir(j.Rel); d != "." {
		dir = filepath.Join(j.DestDir, filepath.FromSlash(d))
	}
	var runs atomic.Int32
	return taskSpec{
		Kind: KindDownload, Group: group, Title: "Download " + path.Base(j.Rel),
		Detail: "→ " + dir,
		Work: func(ctx context.Context, r *run) error {
			job := j
			if runs.Add(1) > 1 && job.VersionID == "" && job.Sync == nil {
				// A retry: the object may have changed since the scan, so look it up again.
				if err := s.refreshDownloadJob(ctx, &job); err != nil {
					return err
				}
			}
			return s.runDownload(ctx, r, job)
		},
	}
}

// refreshDownloadJob updates the ETag, size and time of j from a fresh HEAD.
func (s *Service) refreshDownloadJob(ctx context.Context, j *downloadJob) error {
	c, err := s.bucketClient(ctx, "Download", j.Src)
	if err != nil {
		return err
	}
	ro, err := headRemote(ctx, c, j.Src.Bucket, j.Key, "")
	if err != nil {
		return err
	}
	j.ETag, j.Size, j.Modified = ro.ETag, ro.Size, ro.Modified
	return nil
}

func (s *Service) runDownload(ctx context.Context, r *run, j downloadJob) (err error) {
	const op = "Download"
	local, err := localJoin(op, j.DestDir, j.Rel)
	if err != nil {
		return err
	}
	// decide applies the skip-type policies against what exists locally now.
	// size < 0 means the final size is not known yet. absent reports that the
	// target did not exist, which the publish step must then respect (no-replace).
	decide := func(size int64) (skip bool, reason string, absent bool, derr error) {
		st, lerr := os.Lstat(local)
		if errors.Is(lerr, fs.ErrNotExist) {
			return false, "", true, nil
		}
		if lerr != nil {
			return false, "", false, classify(op, lerr)
		}
		if !st.Mode().IsRegular() && j.Policy != ConflictRename {
			return false, "", false, invalid(op, "%s exists and is not a regular file", local)
		}
		switch j.Policy {
		case ConflictSkip:
			return true, "destination exists", false, nil
		case ConflictRename:
			// A retried scan meets the files it already saved: never number those.
			same, err := hasIdenticalName(local, size, j.Modified)
			if err != nil {
				return false, "", false, classify(op, err)
			}
			if same {
				return true, "identical file exists", false, nil
			}
		case ConflictNewer:
			if !j.Modified.IsZero() && !j.Modified.After(st.ModTime()) {
				return true, "local file is not older", false, nil
			}
		case ConflictDifferentSize:
			if size >= 0 && size == st.Size() {
				return true, "same size", false, nil
			}
		}
		return false, "", false, nil
	}
	if j.Sync != nil {
		if changed, cerr := j.Sync.localChanged(local); cerr != nil {
			return classify(op, cerr)
		} else if changed {
			r.Item(ItemResult{Key: j.Key, Status: ItemSkipped, Err: "local file changed since preview"})
			return nil
		}
	}
	preSize := int64(-1)
	if j.Decode == DecodeRaw {
		preSize = j.Size
	}
	if skip, reason, _, derr := decide(preSize); derr != nil {
		return derr
	} else if skip {
		r.Item(ItemResult{Key: j.Key, VersionID: j.VersionID, Status: ItemSkipped, Err: reason})
		return nil
	}

	c, err := s.bucketClient(ctx, op, j.Src)
	if err != nil {
		return err
	}
	var hints decodeHints
	if j.Decode == DecodeAuto {
		if hints, err = headDecodeHints(ctx, c, j.Src.Bucket, j.Key, j.VersionID); err != nil {
			return err
		}
	}
	dir := filepath.Dir(local)
	f, err := safepath.CreateTemp(dir)
	if err != nil {
		return classify(op, err)
	}
	files := []*os.File{f}
	defer func() { // never leave temporary files behind, whatever happened
		for _, tf := range files {
			tf.Close()
		}
		for _, tf := range files {
			os.Remove(tf.Name())
		}
	}()

	r.SetTotal(j.Size, 1)
	in := &s3.GetObjectInput{Bucket: aws.String(j.Src.Bucket), Key: aws.String(j.Key)}
	if j.VersionID != "" {
		in.VersionId = aws.String(j.VersionID)
	}
	if j.ETag != "" {
		in.IfMatch = aws.String(j.ETag)
	}
	n, err := c.Download(ctx, in, f, storage.TransferOptions{PartSize: s.partSize(), Size: j.Size, Progress: r.SetBytes})
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		cerr := classify(op, err)
		if IsKind(cerr, ErrConflict) {
			return opErr(op, ErrConflict, "%q changed after it was listed; nothing was saved", j.Key)
		}
		return cerr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.Size >= 0 && n != j.Size {
		return opErr(op, ErrConflict, "%q has %d bytes instead of the %d listed; nothing was saved", j.Key, n, j.Size)
	}

	final, finalSize := f, n
	mode, err := detectDecode(f, n, j.Decode, hints)
	if err != nil {
		return classify(op, err)
	}
	if mode != DecodeRaw {
		out, err := safepath.CreateTemp(dir)
		if err != nil {
			return classify(op, err)
		}
		files = append(files, out)
		limit := j.MaxDecoded
		if limit <= 0 {
			limit = defaultMaxDecoded
		}
		if err := s.decodeInto(ctx, f, n, out, mode, j.PasswordRef, limit); err != nil {
			return err
		}
		if err := out.Sync(); err != nil {
			return classify(op, err)
		}
		st, err := out.Stat()
		if err != nil {
			return classify(op, err)
		}
		final, finalSize = out, st.Size()
	}
	if err := final.Close(); err != nil {
		return classify(op, err)
	}
	f.Close()
	if !j.Modified.IsZero() {
		_ = os.Chtimes(final.Name(), j.Modified, j.Modified) // best effort
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	switch {
	case j.Sync != nil:
		// Publish only over the exact local state the preview saw. The check and
		// the rename are not atomic; Publish without overwrite closes the gap for
		// a file that did not exist.
		if changed, cerr := j.Sync.localChanged(local); cerr != nil {
			return classify(op, cerr)
		} else if changed {
			r.Item(ItemResult{Key: j.Key, Status: ItemSkipped, Err: "local file changed since preview"})
			return nil
		}
		err = safepath.Publish(final.Name(), local, j.Sync.Exists)
		if errors.Is(err, safepath.ErrExists) {
			r.Item(ItemResult{Key: j.Key, Status: ItemSkipped, Err: "local file changed since preview"})
			return nil
		}
	case j.Policy == ConflictSkip:
		err = safepath.Publish(final.Name(), local, false)
		if errors.Is(err, safepath.ErrExists) {
			r.Item(ItemResult{Key: j.Key, VersionID: j.VersionID, Status: ItemSkipped, Err: "destination appeared during the download"})
			return nil
		}
	case j.Policy == ConflictRename:
		var skipped bool
		skipped, err = publishRenamed(final.Name(), local, finalSize, j.Modified)
		if err == nil && skipped {
			r.Item(ItemResult{Key: j.Key, VersionID: j.VersionID, Status: ItemSkipped, Err: "identical file exists"})
			return nil
		}
	default: // overwrite, newer, different_size
		// Decide against what exists right now. A target that was absent is
		// published without replacing: if a file appeared since, decide again.
		for attempt := 0; ; attempt++ {
			skip, reason, absent, derr := decide(finalSize)
			if derr != nil {
				return derr
			}
			if skip {
				r.Item(ItemResult{Key: j.Key, VersionID: j.VersionID, Status: ItemSkipped, Err: reason})
				return nil
			}
			err = safepath.Publish(final.Name(), local, !absent)
			if absent && errors.Is(err, safepath.ErrExists) && attempt < 3 {
				continue
			}
			break
		}
	}
	if err != nil {
		return classify(op, err)
	}
	r.SetBytes(j.Size)
	r.Item(ItemResult{Key: j.Key, VersionID: j.VersionID, Status: ItemSucceeded, Err: j.Note})
	return nil
}

// identicalLocal reports whether the local file st is the file a download of
// size bytes, last modified at modified, would produce: same size and a
// modification time within 2 s (downloads set it to the object's LastModified).
func identicalLocal(st fs.FileInfo, size int64, modified time.Time) bool {
	if !st.Mode().IsRegular() || size < 0 || modified.IsZero() || st.Size() != size {
		return false
	}
	d := st.ModTime().Sub(modified)
	if d < 0 {
		d = -d
	}
	return d <= 2*time.Second
}

// hasIdenticalName walks local, "name (1).ext", ... while the names exist and
// reports whether one of them is the identical file.
func hasIdenticalName(local string, size int64, modified time.Time) (bool, error) {
	for n := 0; n <= maxRenameAttempts; n++ {
		cand := local
		if n > 0 {
			cand = numberedPath(local, n)
		}
		st, err := os.Lstat(cand)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if identicalLocal(st, size, modified) {
			return true, nil
		}
	}
	return false, nil
}

// publishRenamed moves tmp to local, or to the first free "name (n).ext" when
// local exists. Existence is decided by Publish itself (no-overwrite), so two
// downloads racing for the same name never replace each other. A name that is
// taken by the identical file (see identicalLocal) ends the search with
// skipped = true and publishes nothing.
func publishRenamed(tmp, local string, size int64, modified time.Time) (skipped bool, err error) {
	for n := 0; n <= maxRenameAttempts; n++ {
		cand := local
		if n > 0 {
			cand = numberedPath(local, n)
		}
		err := safepath.Publish(tmp, cand, false)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, safepath.ErrExists) {
			return false, err
		}
		if st, lerr := os.Lstat(cand); lerr == nil && identicalLocal(st, size, modified) {
			return true, nil
		}
	}
	return false, opErr("Download", ErrConflict, "no free name for %q after %d attempts", local, maxRenameAttempts)
}
