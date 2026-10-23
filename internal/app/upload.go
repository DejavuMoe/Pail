package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// uploadJob is the spec of one per-file (or empty-folder) upload task. Key is
// the key before rules add a ".zip" suffix.
type uploadJob struct {
	Dest    Location
	Local   string
	Key     string
	Policy  ConflictPolicy
	NoRules bool
	Folder  bool // zero-byte "name/" folder object for an empty local folder
	// Sync is set by RunSync (syncrun.go): the upload then replaces the conflict
	// policy with the preconditions observed by the preview.
	Sync *syncUploadPre
}

// Upload uploads files and folders (recursively) to req.Dest and returns the
// group ID of the tasks it creates. It returns as soon as the request is
// validated: a scan task (KindOther) walks the paths and submits one KindUpload
// task per file as it goes, so large trees start uploading while the scan is
// still running. Use CancelGroup to stop the whole action.
//
// Folders keep their structure below Dest.Prefix ("photos" into "a/" gives
// "a/photos/..."). Symbolic links, reparse points and other irregular files are
// skipped and listed as "skipped" items of the scan task; an empty folder
// becomes a zero-byte "name/" object. An empty req.Conflict means the default
// from Settings. Upload rules apply per file when the task starts unless
// req.NoRules is set.
//
// Cancelling a scan in the middle of a folder reports that whole root as
// unprocessed; retrying it re-queues the files already handled. Under the
// "rename" policy a destination that already holds the identical file (same
// size and recorded pail-mtime) is skipped instead of being numbered, so
// that does not create "name (1).ext" copies. The scan never keeps more than
// backlogMax tasks queued: it waits for the queue to drain (waitGroupBacklog).
func (s *Service) Upload(req UploadRequest) (groupID string, err error) {
	const op = "Upload"
	if err := checkLocation(op, req.Dest, true); err != nil {
		return "", err
	}
	if err := s.requireKnownAccount(op, req.Dest.AccountID); err != nil {
		return "", err
	}
	if len(req.Paths) == 0 {
		return "", invalid(op, "no files or folders selected")
	}
	policy, err := s.transferPolicy(op, req.Conflict)
	if err != nil {
		return "", err
	}
	roots := make([]scanRoot, 0, len(req.Paths))
	for _, p := range req.Paths {
		if strings.TrimSpace(p) == "" {
			return "", invalid(op, "empty path")
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", invalid(op, "%v", err)
		}
		name := filepath.Base(abs)
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
			return "", invalid(op, "%q has no name (a drive or file system root cannot be uploaded as a whole; select its contents)", p)
		}
		roots = append(roots, scanRoot{Local: abs, Key: req.Dest.Prefix + name})
	}
	group := s.newGroupID()
	s.submit(s.uploadScanSpec(group, req.Dest, roots, policy, req.NoRules, "Scan"))
	return group, nil
}

// scanTitle is the title of a scan task over n roots.
func scanTitle(verb string, n int) string {
	if n == 1 {
		return verb + " 1 item for upload"
	}
	return fmt.Sprintf("%s %d items for upload", verb, n)
}

// uploadScanSpec is the spec of the scan task over roots. Its Retry rescans the
// roots whose items failed or were left unprocessed, with the same keys.
func (s *Service) uploadScanSpec(group string, dest Location, roots []scanRoot, policy ConflictPolicy, noRules bool, verb string) taskSpec {
	var (
		holdMu sync.Mutex
		last   *localScan // state of the most recent run, for Retry
	)
	spec := taskSpec{
		Kind: KindOther, Group: group, Title: scanTitle(verb, len(roots)),
		Detail: "→ s3://" + dest.Bucket + "/" + dest.Prefix,
	}
	spec.Work = func(ctx context.Context, r *run) error {
		ls := &localScan{s: s, r: r, ctx: ctx, group: group, dest: dest, policy: policy, noRules: noRules, retry: map[string]scanRoot{}}
		holdMu.Lock()
		last = ls
		holdMu.Unlock()
		for i, rt := range roots {
			if err := ls.root(rt); err != nil {
				for _, rest := range roots[i:] {
					ls.remember(rest.Local, rest.Key)
					r.Item(ItemResult{Key: rest.Local, Status: ItemUnprocessed, Err: "not scanned: canceled"})
				}
				return err
			}
		}
		ls.mu.Lock()
		n := ls.files
		ls.mu.Unlock()
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
		var next []scanRoot
		seen := map[string]bool{}
		for _, it := range failed {
			if rt, ok := prev.retry[it.Key]; ok && !seen[it.Key] {
				seen[it.Key] = true
				next = append(next, rt)
			}
		}
		prev.mu.Unlock()
		if len(next) == 0 {
			return taskSpec{}, false
		}
		return s.uploadScanSpec(group, dest, next, policy, noRules, "Rescan"), true
	}
	return spec
}

// uploadTaskSpec is the spec of the task that uploads one file.
func (s *Service) uploadTaskSpec(group string, j uploadJob) taskSpec {
	name := path.Base(strings.TrimSuffix(j.Key, "/"))
	title := "Upload " + name
	if j.Folder {
		title += "/"
	}
	return taskSpec{
		Kind: KindUpload, Group: group, Title: title,
		Detail: "→ s3://" + j.Dest.Bucket + "/" + keyDir(j.Key),
		Work:   func(ctx context.Context, r *run) error { return s.runUpload(ctx, r, j) },
	}
}

func (s *Service) runUpload(ctx context.Context, r *run, j uploadJob) error {
	const op = "Upload"
	c, err := s.bucketClient(ctx, op, j.Dest)
	if err != nil {
		return err
	}
	if j.Folder {
		return s.uploadFolderObject(ctx, r, c, j)
	}
	f, err := os.Open(j.Local)
	if err != nil {
		return classify(op, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return classify(op, err)
	}
	if !fi.Mode().IsRegular() {
		return invalid(op, "%s is not a regular file", j.Local)
	}
	size, mtime := fi.Size(), fi.ModTime()
	if j.Sync != nil && (size != j.Sync.Size || mtime.UnixNano() != j.Sync.MTime) {
		r.Item(ItemResult{Key: j.Sync.Key, Status: ItemSkipped, Err: "local file changed since preview"})
		return nil
	}
	r.SetTotal(size, 1)

	var opts rules.UploadOptions
	if !j.NoRules {
		if opts, err = s.PreviewUploadRules(j.Dest.Bucket, j.Key); err != nil {
			return err
		}
	}
	in := &s3.PutObjectInput{Bucket: aws.String(j.Dest.Bucket)}
	if err := s.applyUploadOptions(ctx, in, opts, j.Dest.AccountID); err != nil {
		return err
	}
	in.Metadata[mtimeMetaKey] = mtime.UTC().Format(time.RFC3339Nano)
	if j.Sync != nil {
		for k, v := range j.Sync.Meta {
			in.Metadata[k] = v
		}
	}

	finalKey := j.Key
	comp := opts.Compression
	if comp != nil {
		enc := aws.ToString(in.ContentEncoding)
		switch comp.Method {
		case "gzip":
			if enc != "" && !strings.EqualFold(enc, "identity") {
				return invalid(op, "%v", errGzipEncodingConflict)
			}
			in.ContentEncoding = aws.String("gzip")
			in.Metadata[compressionMetaKey] = "gzip"
		case "zip":
			if enc != "" {
				return invalid(op, "a rule sets Content-Encoding and also asks for ZIP compression")
			}
			finalKey += ".zip"
			in.Metadata[compressionMetaKey] = "zip"
			if comp.PasswordRef != "" {
				in.Metadata[compressionMetaKey] = "aes-zip"
			}
		default:
			return invalid(op, "unsupported compression method %q", comp.Method)
		}
	}
	// Content-Type priority: ZIP artifact > rule > MIME table > extension > octet-stream.
	switch {
	case comp != nil && comp.Method == "zip":
		in.ContentType = aws.String("application/zip")
	case aws.ToString(in.ContentType) == "":
		in.ContentType = aws.String(s.ContentTypeFor(j.Local))
	}

	var (
		body     io.ReadSeeker = f
		bodySize               = size
		spooled  *os.File
	)
	defer func() {
		if spooled != nil {
			spooled.Close()
			os.Remove(spooled.Name())
		}
	}()
	compress := func() error {
		if comp == nil || spooled != nil {
			return nil
		}
		sp, n, err := s.spoolCompressed(ctx, f, j.Local, comp)
		if err != nil {
			return classify(op, err)
		}
		spooled, body, bodySize = sp, sp, n
		r.SetTotal(n, 1)
		return nil
	}
	// A size comparison needs the artifact size, so compress first for it; the
	// other policies decide first so that a skipped file costs no compression.
	if j.Policy == ConflictDifferentSize {
		if err := compress(); err != nil {
			return err
		}
	}
	if j.Sync != nil && finalKey != j.Sync.Key {
		return invalid(op, "the upload rules changed since the preview (%q instead of %q); preview again", finalKey, j.Sync.Key)
	}
	var (
		blind    int   // > 0: the destination cannot be inspected (HEAD denied); the number of the current create-only attempt
		blindErr error // the denial that led there
	)
	// identical reports whether an existing destination already holds this very
	// file: same artifact size and the same recorded modification time. The
	// rename policy never numbers such a file again (retries, rescans).
	identical := func(h remoteHead) (bool, error) {
		if err := compress(); err != nil {
			return false, err
		}
		return h.size == bodySize && metaGet(h.meta, mtimeMetaKey) == in.Metadata[mtimeMetaKey], nil
	}
	resolve := func() (conflictDecision, error) {
		if j.Sync != nil {
			return syncUploadDecision(ctx, c, j.Dest.Bucket, finalKey, j.Sync)
		}
		var (
			d   conflictDecision
			err error
		)
		if j.Policy == ConflictRename {
			d, err = resolveRenameIdentity(ctx, c, j.Dest.Bucket, finalKey, identical)
		} else {
			d, err = resolveRemoteConflict(ctx, c, j.Dest.Bucket, finalKey, j.Policy, bodySize, mtime)
		}
		if err != nil && s.canWriteBlind(j.Dest.AccountID, j.Policy, err) {
			// Write-only credentials: no GetObject, so no HEAD. Write create-only instead.
			blind, blindErr = 1, err
			return conflictDecision{Key: finalKey, CreateOnly: true}, nil
		}
		return d, err
	}
	dec, err := resolve()
	if err != nil {
		return err
	}
	// replayed reports whether key already holds exactly what this task wrote:
	// the SDK retryer may resend a PutObject / CompleteMultipartUpload that had
	// committed, and the replay then meets its own object with a 412.
	replayed := func(key string) bool {
		h, err := headRemoteMeta(ctx, c, j.Dest.Bucket, key)
		if err != nil || !h.exists || h.size != bodySize {
			return false
		}
		for k, v := range in.Metadata {
			if strings.HasPrefix(k, internalMetaPrefix) && h.meta[k] != v {
				return false
			}
		}
		for k := range h.meta {
			if _, ok := in.Metadata[k]; isInternalMeta(k) && !ok {
				return false
			}
		}
		return true
	}

	partSize := s.partSize()
	write := func(ifNoneMatch, ifMatch string) (string, error) {
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		put := *in
		put.Key = aws.String(dec.Key)
		put.Body = body
		if ifNoneMatch != "" {
			put.IfNoneMatch = aws.String(ifNoneMatch)
		}
		if ifMatch != "" {
			put.IfMatch = aws.String(ifMatch)
		}
		res, err := c.Upload(ctx, &put, storage.TransferOptions{PartSize: partSize, Progress: r.SetBytes})
		if err != nil {
			return "", err
		}
		return aws.ToString(res.VersionID), nil
	}
	for attempt := 0; ; attempt++ {
		if dec.Skip {
			r.Item(ItemResult{Key: dec.Key, Status: ItemSkipped, Err: dec.Reason})
			return nil
		}
		if err := compress(); err != nil {
			return err
		}
		var out string
		doWrite := func(ifNoneMatch, ifMatch string) (err error) {
			out, err = write(ifNoneMatch, ifMatch)
			return err
		}
		var err error
		if blind > 0 {
			err = s.writeCreateOnly(j.Dest.AccountID, blindErr, doWrite)
		} else {
			err = s.writeConditional(j.Dest.AccountID, dec, doWrite)
		}
		if err == nil {
			r.SetBytes(bodySize)
			r.Item(ItemResult{Key: dec.Key, VersionID: out, Status: ItemSucceeded})
			return nil
		}
		cerr := classify(op, err)
		if !IsKind(cerr, ErrConflict) {
			return cerr
		}
		if dec.CreateOnly && replayed(dec.Key) {
			r.SetBytes(bodySize)
			r.Item(ItemResult{Key: dec.Key, Status: ItemSucceeded, Err: "committed; response was retried"})
			return nil
		}
		switch {
		case blind > 0 && j.Policy == ConflictSkip:
			r.Item(ItemResult{Key: dec.Key, Status: ItemSkipped, Err: "destination exists"})
			return nil
		case blind > 0: // rename: without HEAD, probe the numbered names by writing
			if blind >= maxBlindAttempts {
				return opErr(op, ErrConflict, "no free name for %q after %d attempts (the destination cannot be inspected: %v)", finalKey, blind, blindErr)
			}
			dec = conflictDecision{Key: numberedKey(finalKey, blind), CreateOnly: true}
			blind++
			continue
		case j.Policy == ConflictRename && attempt < 2:
			// The free name was taken between the HEAD and the commit: pick the next one.
			if dec, err = resolve(); err != nil {
				return err
			}
			continue
		}
		if j.Sync != nil {
			ce := opErr(op, ErrConflict, "%q changed since the sync preview; nothing was overwritten", dec.Key)
			ce.Code, ce.err = "PreconditionFailed", err
			return ce
		}
		ce := opErr(op, ErrConflict, "%q changed while it was being uploaded; nothing was overwritten (policy %q)", dec.Key, j.Policy)
		ce.Code, ce.err = "PreconditionFailed", err
		return ce
	}
}

// maxBlindAttempts bounds the create-only attempts of the rename policy when the
// destination cannot be inspected: the key itself, then four numbered names.
const maxBlindAttempts = 5

// canWriteBlind reports whether a failed conflict check (err) may be answered
// with a create-only write: the policy needs only "does it exist" (skip,
// rename), the HEAD was denied (write-only credentials), and the account is not
// known to reject conditional writes, since without them the write could not
// protect an existing object.
func (s *Service) canWriteBlind(accountID string, policy ConflictPolicy, err error) bool {
	return (policy == ConflictSkip || policy == ConflictRename) &&
		IsKind(err, ErrAccessDenied) && !s.condWritesUnsupported(accountID)
}

// writeCreateOnly runs write with If-None-Match: * and never without it. If the
// provider rejects the header it records that and fails with denied (the
// original access error): an object that cannot be inspected is not overwritten.
func (s *Service) writeCreateOnly(accountID string, denied error, write func(ifNoneMatch, ifMatch string) error) error {
	if s.condWritesUnsupported(accountID) {
		return denied
	}
	err := write("*", "")
	if condHeaderRejected(err) {
		s.markCondWritesUnsupported(accountID)
		return denied
	}
	return err
}

// remoteHead is the HEAD of an upload destination, with its user metadata.
type remoteHead struct {
	exists bool
	size   int64
	meta   map[string]string // lower-case keys
}

// headRemoteMeta HEADs bucket/key; a missing key is not an error.
func headRemoteMeta(ctx context.Context, c *storage.Client, bucket, key string) (remoteHead, error) {
	out, err := c.S3().HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		cerr := classify("HeadObject", err)
		if KindOf(cerr) == ErrNotFound {
			return remoteHead{}, nil
		}
		return remoteHead{}, cerr
	}
	meta := make(map[string]string, len(out.Metadata))
	for k, v := range out.Metadata {
		meta[strings.ToLower(k)] = v
	}
	return remoteHead{exists: true, size: aws.ToInt64(out.ContentLength), meta: meta}, nil
}

// resolveRenameIdentity is the rename policy of resolveRemoteConflict made
// identity-aware: the first free "name (n).ext" is chosen, but a name that is
// already taken by the identical file ends the search with a skip, so that
// retrying a scan never creates "name (1).ext" copies of what it uploaded.
func resolveRenameIdentity(ctx context.Context, c *storage.Client, bucket, key string, identical func(remoteHead) (bool, error)) (conflictDecision, error) {
	for n := 0; n <= maxRenameAttempts; n++ {
		cand := key
		if n > 0 {
			cand = numberedKey(key, n)
		}
		h, err := headRemoteMeta(ctx, c, bucket, cand)
		if err != nil {
			return conflictDecision{}, err
		}
		if !h.exists {
			return conflictDecision{Key: cand, CreateOnly: true}, nil
		}
		same, err := identical(h)
		if err != nil {
			return conflictDecision{}, err
		}
		if same {
			return conflictDecision{Key: cand, Skip: true, Reason: "identical file exists"}, nil
		}
	}
	return conflictDecision{}, opErr("ResolveConflict", ErrConflict, "no free name for %q after %d attempts", key, maxRenameAttempts)
}

// Name pattern of the compressed-upload spool files in os.TempDir().
const (
	spoolPrefix = "pail-upload-"
	spoolSuffix = ".tmp"
	// legacySpoolPrefix is the spool name of builds before the rename to Pail; the sweeper still removes those orphans.
	legacySpoolPrefix = "opens3-upload-"
)

// sweepSpoolFiles removes spool files of earlier runs that were killed before
// they could clean up: regular files in dir named like a spool file whose
// modification time is older than maxAge. Nothing else is touched.
func sweepSpoolFiles(dir string, maxAge time.Duration, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !e.Type().IsRegular() || !(strings.HasPrefix(name, spoolPrefix) || strings.HasPrefix(name, legacySpoolPrefix)) || !strings.HasSuffix(name, spoolSuffix) {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > maxAge {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// spoolCompressed writes the compressed form of src (positioned at 0) to a
// temporary file and returns it rewound, with its size. The caller deletes it.
func (s *Service) spoolCompressed(ctx context.Context, src *os.File, local string, comp *rules.Compression) (*os.File, int64, error) {
	tmp, err := os.CreateTemp("", spoolPrefix+"*"+spoolSuffix)
	if err != nil {
		return nil, 0, err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	switch comp.Method {
	case "gzip":
		_, err = rules.CompressGZIP(ctx, tmp, src, comp.Level)
	case "zip":
		name := filepath.Base(local)
		if comp.PasswordRef != "" {
			_, err = rules.CompressAES256ZIP(ctx, tmp, src, name, comp.Level, comp.PasswordRef, s)
		} else {
			_, err = rules.CompressZIP(ctx, tmp, src, name, comp.Level)
		}
	default:
		err = invalid("Upload", "unsupported compression method %q", comp.Method)
	}
	if err != nil {
		return nil, 0, err
	}
	fi, err := tmp.Stat()
	if err != nil {
		return nil, 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	ok = true
	return tmp, fi.Size(), nil
}

// uploadFolderObject writes the zero-byte "name/" object of an empty folder.
// An existing object is never replaced unless the policy is overwrite.
func (s *Service) uploadFolderObject(ctx context.Context, r *run, c *storage.Client, j uploadJob) error {
	const op = "Upload"
	dec := conflictDecision{Key: j.Key}
	var denied error // set when the destination cannot be inspected (write-only credentials)
	if j.Policy != ConflictOverwrite {
		d, err := resolveRemoteConflict(ctx, c, j.Dest.Bucket, j.Key, ConflictSkip, 0, time.Time{})
		if err != nil {
			if !s.canWriteBlind(j.Dest.AccountID, ConflictSkip, err) {
				return err
			}
			denied = err
			d = conflictDecision{Key: j.Key, CreateOnly: true}
		}
		dec = d
	}
	if dec.Skip {
		r.Item(ItemResult{Key: j.Key, Status: ItemSkipped, Err: "folder already exists"})
		return nil
	}
	write := func(ifNoneMatch, ifMatch string) error {
		in := &s3.PutObjectInput{
			Bucket: aws.String(j.Dest.Bucket), Key: aws.String(j.Key),
			Body: bytes.NewReader(nil), ContentType: aws.String(folderContentType),
		}
		if ifNoneMatch != "" {
			in.IfNoneMatch = aws.String(ifNoneMatch)
		}
		_, err := c.S3().PutObject(ctx, in)
		return err
	}
	var err error
	if denied != nil {
		err = s.writeCreateOnly(j.Dest.AccountID, denied, write)
	} else {
		err = s.writeConditional(j.Dest.AccountID, dec, write)
	}
	if err != nil {
		cerr := classify(op, err)
		if IsKind(cerr, ErrConflict) {
			r.Item(ItemResult{Key: j.Key, Status: ItemSkipped, Err: "folder already exists"})
			return nil
		}
		return cerr
	}
	r.Item(ItemResult{Key: j.Key, Status: ItemSucceeded})
	return nil
}
