package app

import (
	"context"
	"maps"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// This file implements Estimate, Copy / Move / Rename and Delete
// (APP_API.md section 6). The shared engine (selection expansion, worker pool,
// cancellation and Retry bookkeeping) lives in expand.go.
//
// Races that cannot be closed with S3's API, kept here so they are not
// forgotten:
//
//   - Move (and Rename) works on current objects only; explicit versions are
//     rejected. It copies first, verifies the destination by a HEAD (size, and
//     the ETag when both ETags are content MD5s),
//     then HEADs the source again: only if the source is still what was copied
//     (same VersionId, or in an unversioned bucket the same ETag) it is
//     removed with a plain DeleteObject WITHOUT a VersionId. A versioned
//     bucket thus gets a delete marker and keeps its whole history (deleting
//     the copied VersionId would make the previous version current again);
//     an unversioned bucket loses the key. A source that changed after the
//     copy is kept and the item fails. S3 DELETE has no usable If-Match
//     (MinIO ignores it), so the few milliseconds between the re-check and the
//     delete remain an unclosable window.
//   - Overwrite-style conflict policies write unconditionally, the others
//     send a conditional header when the provider honours it (caps.go) and
//     otherwise rely on the HEAD done by resolveRemoteConflict.

// bkEstimateLimit is where Estimate stops counting; a variable only so that
// tests can use a small limit.
var bkEstimateLimit = 100000

// Estimate counts the objects and bytes a selection covers, for confirmation
// dialogs. It stops at 100,000 objects and sets Truncated. Explicitly named
// keys are sized with a HEAD; a key that does not exist counts as nothing.
func (s *Service) Estimate(ctx context.Context, sel Selection) (Estimate, error) {
	const op = "Estimate"
	scope, err := bkBuildScope(op, sel, true, false)
	if err != nil {
		return Estimate{}, err
	}
	c, err := s.bucketClient(ctx, op, sel.Location)
	if err != nil {
		return Estimate{}, err
	}
	var est Estimate
	limit := bkEstimateLimit
	add := func(size int64) bool {
		if est.Objects >= limit {
			est.Truncated = true
			return false
		}
		est.Objects++
		est.Bytes += size
		return true
	}

	sizes := make([]int64, len(scope.Objs))
	errs := make([]error, len(scope.Objs))
	for i := range sizes {
		sizes[i] = -1
	}
	syParallel(ctx, len(scope.Objs), 8, func(i int) {
		o := scope.Objs[i]
		h, _, err := bkHead(ctx, c, sel.Bucket, o.Key, o.VersionID)
		switch {
		case err == nil:
			sizes[i] = h.Size
		case IsKind(err, ErrNotFound), bkStatus(err) == 405:
			// absent, or a delete marker: nothing to count
		default:
			errs[i] = err
		}
	})
	if err := ctx.Err(); err != nil {
		return Estimate{}, classify(op, err) // the sizes are incomplete
	}
	for i := range scope.Objs {
		if errs[i] != nil {
			return Estimate{}, classify(op, errs[i])
		}
		if sizes[i] >= 0 && !add(sizes[i]) {
			return est, nil
		}
	}
	list := bkListObjects(c, sel.Bucket)
	for _, scan := range scope.Scans {
		err := list(ctx, scan, func(page []bkObj, _ string) error {
			for _, o := range page {
				if !add(o.Size) {
					return errBkStop
				}
			}
			return nil
		})
		if err == errBkStop {
			return est, nil
		}
		if err != nil {
			return Estimate{}, classify(op, err)
		}
	}
	return est, nil
}

// ---- copy / move / rename -------------------------------------------------------------

type bkCopyPlan struct {
	op       string // "Copy" | "Move" | "Rename"
	src      Location
	dest     Location
	policy   ConflictPolicy
	move     bool
	rename   string // Rename: replaces the first path segment below src.Prefix
	failSkip bool   // Rename: an existing destination is a failure, not a skip
	title    string

	// copied remembers, per source key, the verified copies of a move whose
	// source has not been deleted yet. A retry of the task (same plan) finishes
	// such an item instead of treating its own copy as a conflict.
	copied sync.Map // source key -> bkCopied
}

// bkCopied is a copy made by an earlier run of the same move.
type bkCopied struct {
	srcETag  string // the source as copied
	destKey  string // where the copy went (the rename policy may have picked a free name)
	destETag string // the destination as verified after the copy
}

// destKey maps a source key to its destination key.
func (p *bkCopyPlan) destKey(key string) (string, bool) {
	if !strings.HasPrefix(key, p.src.Prefix) || len(key) <= len(p.src.Prefix) {
		return "", false
	}
	rel := key[len(p.src.Prefix):]
	if p.rename != "" {
		if i := strings.IndexByte(rel, '/'); i < 0 {
			rel = p.rename
		} else {
			rel = p.rename + rel[i:]
		}
	}
	return p.dest.Prefix + rel, true
}

func (p *bkCopyPlan) sameBucket() bool {
	return p.src.AccountID == p.dest.AccountID && p.src.Bucket == p.dest.Bucket
}

// crossAccount reports a copy between two accounts: the object then streams
// through Pail (GET from the source, upload to the destination).
func (p *bkCopyPlan) crossAccount() bool { return p.src.AccountID != p.dest.AccountID }

// Copy copies (or, with opt.Move, moves) a selection to dest as one Other
// task. The destination key is dest.Prefix plus the key relative to
// sel.Prefix, so a selected folder keeps its own name. Copying inside one
// bucket is allowed; copying onto itself and folders into themselves are
// rejected. Between two accounts the objects stream through Pail: each is read
// with a GET guarded by its ETag and uploaded with its content headers,
// metadata, storage class and tags. Existing destinations follow opt.Conflict
// (default: skip).
func (s *Service) Copy(sel Selection, dest Location, opt CopyOptions) (string, error) {
	op, verb := "Copy", "Copy"
	if opt.Move {
		op, verb = "Move", "Move"
	}
	if err := checkLocation(op, dest, true); err != nil {
		return "", err
	}
	if opt.Conflict == "" {
		opt.Conflict = ConflictSkip
	}
	if !opt.Conflict.Valid() {
		return "", invalid(op, "unknown conflict policy %q", opt.Conflict)
	}
	if opt.Move && len(sel.Versions) > 0 {
		return "", invalid(op, "move applies to current objects only, not to specific versions; copy the version, then delete it")
	}
	scope, err := bkBuildScope(op, sel, true, true)
	if err != nil {
		return "", err
	}
	plan := &bkCopyPlan{op: op, src: sel.Location, dest: dest, policy: opt.Conflict, move: opt.Move, title: bkTitle(verb, sel)}
	if plan.crossAccount() {
		if _, ok := s.account(dest.AccountID); !ok {
			return "", invalid(op, "unknown destination account %q", dest.AccountID)
		}
	}
	if plan.sameBucket() {
		if dest.Prefix == sel.Prefix {
			return "", invalid(op, "the destination is the same folder as the source")
		}
		for _, sc := range scope.Scans {
			if strings.HasPrefix(dest.Prefix, sc.Prefix) {
				return "", invalid(op, "cannot %s a folder into itself", strings.ToLower(verb))
			}
		}
	}
	return s.bkSubmitCopy(plan, scope), nil
}

// Rename renames an object, or a folder (key ending in "/") together with
// everything below it, inside its own folder. newName is one path segment.
// An existing target is a failure, never overwritten; renaming a folder onto
// an existing folder name merges the two and fails the colliding objects.
func (s *Service) Rename(ctx context.Context, loc Location, key, newName string) (string, error) {
	const op = "Rename"
	if err := checkLocation(op, loc, true); err != nil {
		return "", err
	}
	if err := bkValidKey(op, key); err != nil {
		return "", err
	}
	switch {
	case newName == "" || newName == "." || newName == "..":
		return "", invalid(op, "new name %q is not valid", newName)
	case strings.Contains(newName, "/"):
		return "", invalid(op, "the new name must be a single path segment without \"/\"")
	case strings.ContainsAny(newName, "\x00\r\n"):
		return "", invalid(op, "the new name contains control characters")
	}
	folder := strings.HasSuffix(key, "/")
	trimmed := strings.TrimSuffix(key, "/")
	if trimmed == "" {
		return "", invalid(op, "nothing to rename")
	}
	parent := ""
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		parent = trimmed[:i+1]
	}
	if newName == entryName(key) {
		return "", invalid(op, "the new name is the same as the old one")
	}
	if _, err := s.bucketClient(ctx, op, loc); err != nil {
		return "", err
	}
	here := Location{AccountID: loc.AccountID, Bucket: loc.Bucket, Prefix: parent}
	plan := &bkCopyPlan{op: op, src: here, dest: here, policy: ConflictSkip, move: true, rename: newName, failSkip: true,
		title: "Rename " + entryName(key) + " to " + newName}
	var scope bkScope
	if folder {
		scope.Scans = []bkScan{{Prefix: key}}
	} else {
		scope.Objs = []bkObj{{Key: key}}
	}
	return s.bkSubmitCopy(plan, scope), nil
}

func (s *Service) bkSubmitCopy(plan *bkCopyPlan, scope bkScope) string {
	return s.submit(s.bkCopySpec(plan, scope))
}

func (s *Service) bkCopySpec(plan *bkCopyPlan, scope bkScope) taskSpec {
	st := &bkState{}
	return taskSpec{
		Kind:   KindOther,
		Title:  plan.title,
		Detail: bkURL(plan.src) + " -> " + bkURL(plan.dest),
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, plan.op, plan.src)
			if err != nil {
				return err
			}
			dst := c
			if plan.crossAccount() {
				if dst, err = s.bucketClient(ctx, plan.op, plan.dest); err != nil {
					return err
				}
			}
			pipe := &bkPipe{
				workers: s.bkWorkers(), batch: 1, lister: bkListObjects(c, plan.src.Bucket),
				process: func(ctx context.Context, b []bkObj) []ItemResult {
					return []ItemResult{s.bkCopyOne(ctx, r, c, dst, plan, b[0])}
				},
			}
			rem, err := pipe.run(ctx, r, scope)
			st.set(rem)
			return err
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			sc, ok := st.retryScope(failed)
			if !ok {
				return taskSpec{}, false
			}
			return s.bkCopySpec(plan, sc), true
		},
	}
}

// bkCopyClass is the storage class to request for a copy of an object of
// class sc: CopyObject would otherwise reset it to STANDARD.
func bkCopyClass(sc string) types.StorageClass {
	if sc == "" || sc == string(types.StorageClassStandard) {
		return ""
	}
	return types.StorageClass(sc)
}

// bkCopyOne copies (and for a move, deletes) one object. src and dst are the
// clients of the source and destination accounts (the same client within one
// account, where the copy is server-side).
func (s *Service) bkCopyOne(ctx context.Context, r *run, src, dst *storage.Client, p *bkCopyPlan, o bkObj) ItemResult {
	res := ItemResult{Key: o.Key, VersionID: o.VersionID}
	destKey, ok := p.destKey(o.Key)
	if !ok {
		return bkFailf(res, "%q is not inside %q", o.Key, p.src.Prefix)
	}
	if p.sameBucket() && destKey == o.Key {
		return bkFailf(res, "the destination is the same as the source")
	}
	if p.move && o.VersionID != "" {
		return bkFailf(res, "moving a specific version is not supported; move the current object")
	}
	// A move needs the exact version that is copied (and checked again before
	// the delete); an unresolved object needs its ETag and size.
	srcKeyed := false
	var srcHead *s3.HeadObjectOutput
	if p.move || !o.Resolved || p.crossAccount() {
		h, hout, err := bkHead(ctx, src, p.src.Bucket, o.Key, o.VersionID)
		if err != nil {
			return bkFail(ctx, res, err)
		}
		if o.Resolved && o.ETag != h.ETag {
			return bkFailf(res, "the object changed after it was listed; retry to use the current version")
		}
		if !o.Resolved {
			r.AddTotal(h.Size, 0)
		}
		o, srcKeyed, srcHead = h, headKeyed(hout), hout
		if !p.move { // a retried move must resolve the then-current object again
			res.VersionID = o.VersionID
		}
	}
	progress := bkProgress(r)
	sameAttributes := func(key string, h *s3.HeadObjectOutput) (bool, error) {
		if !bkCopyAttributesEqual(srcHead, h) {
			return false, nil
		}
		srcTags, err := bkCopyTags(ctx, src, p.src.Bucket, o.Key, o.VersionID)
		if err != nil {
			return false, err
		}
		dstTags, err := bkCopyTags(ctx, dst, p.dest.Bucket, key, aws.ToString(h.VersionId))
		return err == nil && maps.Equal(srcTags, dstTags), err
	}
	var dec conflictDecision
	copied := false // a retried move: an earlier run copied, but did not delete the source
	if p.move && p.policy != ConflictOverwrite {
		// The copy of an earlier run of this task: still the object it verified?
		// (A multipart copy has no ETag to compare with the source's.)
		if v, ok := p.copied.Load(o.Key); ok {
			if rec := v.(bkCopied); rec.srcETag == o.ETag {
				t, err := headTarget(ctx, dst, p.dest.Bucket, rec.destKey)
				if err != nil {
					return bkFail(ctx, res, err)
				}
				if t.exists && t.size == o.Size && t.etag == rec.destETag {
					if copied, err = sameAttributes(rec.destKey, t.head); err != nil {
						return bkFail(ctx, res, err)
					}
					dec = conflictDecision{Key: rec.destKey}
				}
			}
		}
		if !copied {
			target, err := headTarget(ctx, dst, p.dest.Bucket, destKey)
			if err != nil {
				return bkFail(ctx, res, err)
			}
			if target.exists && target.size == o.Size && sameObjectETag(o.ETag, srcKeyed, target.etag, target.keyed) {
				if copied, err = sameAttributes(destKey, target.head); err != nil {
					return bkFail(ctx, res, err)
				}
				dec = conflictDecision{Key: destKey}
			}
			if !copied {
				if dec, err = decideConflict(ctx, dst, p.dest.Bucket, destKey, p.policy, o.Size, o.Modified, target); err != nil {
					return bkFail(ctx, res, err)
				}
			}
		}
		if copied {
			progress(o.Size)
		}
	} else {
		var err error
		if dec, err = resolveRemoteConflict(ctx, dst, p.dest.Bucket, destKey, p.policy, o.Size, o.Modified); err != nil {
			return bkFail(ctx, res, err)
		}
	}
	if dec.Skip {
		if p.failSkip {
			return bkFailf(res, "%q already exists", destKey)
		}
		progress(o.Size)
		res.Status, res.Err = ItemSkipped, dec.Reason
		return res
	}
	if !copied {
		source := storage.CopySource(p.src.Bucket, o.Key, o.VersionID)
		err := s.writeConditional(p.dest.AccountID, dec, func(ifNoneMatch, ifMatch string) error {
			if p.crossAccount() {
				return s.bkStreamCopy(ctx, src, dst, p, o, srcHead, dec.Key, ifNoneMatch, ifMatch, progress)
			}
			in := &s3.CopyObjectInput{
				Bucket: aws.String(p.dest.Bucket), Key: aws.String(dec.Key), CopySource: aws.String(source),
				CopySourceIfMatch: aws.String(o.ETag), MetadataDirective: types.MetadataDirectiveCopy,
				StorageClass: bkCopyClass(o.StorageClass),
			}
			if ifNoneMatch != "" {
				in.IfNoneMatch = aws.String(ifNoneMatch)
			}
			if ifMatch != "" {
				in.IfMatch = aws.String(ifMatch)
			}
			return dst.Copy(ctx, in, o.Size, s.bkTransferOpts(progress))
		})
		if err != nil {
			return bkFail(ctx, res, err)
		}
		progress(o.Size)
	}
	if p.move {
		t, err := headTarget(ctx, dst, p.dest.Bucket, dec.Key)
		if err != nil {
			return bkFailf(res, "copied, but the destination could not be verified (%v); source kept", classify("", err))
		}
		// Size always; the ETag too when both ETags are content MD5s (not
		// multipart, not SSE-KMS/SSE-C), otherwise it says nothing.
		if !t.exists || t.size != o.Size || (etagMD5(o.ETag, srcKeyed) && etagMD5(t.etag, t.keyed) && !sameObjectETag(o.ETag, srcKeyed, t.etag, t.keyed)) {
			return bkFailf(res, "copied, but the destination does not match the source; source kept")
		}
		if same, err := sameAttributes(dec.Key, t.head); err != nil {
			return bkFailf(res, "copied, but the attributes could not be verified (%v); source kept", classify("", err))
		} else if !same {
			return bkFailf(res, "copied, but the destination attributes do not match the source; source kept")
		}
		p.copied.Store(o.Key, bkCopied{srcETag: o.ETag, destKey: dec.Key, destETag: t.etag})
		cur, curHead, err := bkHead(ctx, src, p.src.Bucket, o.Key, "")
		switch {
		case IsKind(err, ErrNotFound):
			// The source is already gone (or a delete marker is on top): the move is done.
			res.Status = ItemSucceeded
			return res
		case err != nil:
			return bkFailf(res, "copied, but the source could not be re-checked (%v); source kept", classify("", err))
		}
		same := cur.ETag == o.ETag
		if o.VersionID != "" {
			same = cur.VersionID == o.VersionID
		}
		if !same || !bkCopyAttributesEqual(srcHead, curHead) {
			return bkFailf(res, "copied, but source changed since; source kept")
		}
		// No VersionId: a versioned bucket gets a delete marker and keeps its
		// history, an unversioned one drops the key.
		din := &s3.DeleteObjectInput{Bucket: aws.String(p.src.Bucket), Key: aws.String(o.Key)}
		if _, err := src.S3().DeleteObject(ctx, din); err != nil {
			return bkFailf(res, "copied, source not deleted: %v", classify("", err))
		}
	}
	res.Status = ItemSucceeded
	return res
}

// Content ETags exclude metadata, headers and tags; a move must keep those too.
func bkCopyAttributesEqual(a, b *s3.HeadObjectOutput) bool {
	return maps.Equal(a.Metadata, b.Metadata) &&
		aws.ToString(a.ContentType) == aws.ToString(b.ContentType) &&
		aws.ToString(a.ContentEncoding) == aws.ToString(b.ContentEncoding) &&
		aws.ToString(a.ContentDisposition) == aws.ToString(b.ContentDisposition) &&
		aws.ToString(a.ContentLanguage) == aws.ToString(b.ContentLanguage) &&
		aws.ToString(a.CacheControl) == aws.ToString(b.CacheControl) &&
		aws.ToString(a.ExpiresString) == aws.ToString(b.ExpiresString) &&
		aws.ToTime(a.Expires).Equal(aws.ToTime(b.Expires)) &&
		aws.ToString(a.WebsiteRedirectLocation) == aws.ToString(b.WebsiteRedirectLocation) &&
		bkCopyClass(string(a.StorageClass)) == bkCopyClass(string(b.StorageClass))
}

func bkCopyTags(ctx context.Context, c *storage.Client, bucket, key, version string) (map[string]string, error) {
	in := &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if version != "" {
		in.VersionId = aws.String(version)
	}
	out, err := c.S3().GetObjectTagging(ctx, in)
	if err != nil {
		if IsKind(classify("", err), ErrUnsupported) || bkStatus(err) == 405 {
			return nil, nil
		}
		return nil, err
	}
	tags := make(map[string]string, len(out.TagSet))
	for _, tag := range out.TagSet {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return tags, nil
}

// ---- delete ---------------------------------------------------------------------------------

// Delete removes a selection with batched DeleteObjects calls as one Other
// task. In versioned buckets objects get delete markers (normal S3
// semantics); explicit Versions are removed permanently. Per-object statuses
// are those of storage.DeleteObjects: an "unknown" outcome is never replayed
// by Retry.
func (s *Service) Delete(sel Selection) (string, error) {
	const op = "Delete"
	scope, err := bkBuildScope(op, sel, true, false)
	if err != nil {
		return "", err
	}
	return s.submit(s.bkDeleteSpec(sel.Location, scope, bkTitle("Delete", sel))), nil
}

func (s *Service) bkDeleteSpec(loc Location, scope bkScope, title string) taskSpec {
	st := &bkState{}
	return taskSpec{
		Kind:   KindOther,
		Title:  title,
		Detail: bkURL(loc),
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, "Delete", loc)
			if err != nil {
				return err
			}
			pipe := &bkPipe{
				workers: s.bkWorkers(), batch: 1000, lister: bkListObjects(c, loc.Bucket),
				process: func(ctx context.Context, b []bkObj) []ItemResult {
					ids := make([]storage.ObjectID, len(b))
					for i, o := range b {
						ids[i] = storage.ObjectID{Key: o.Key, VersionID: o.VersionID}
					}
					out := make([]ItemResult, len(b))
					for i, d := range c.DeleteObjects(ctx, loc.Bucket, ids) {
						it := ItemResult{Key: d.Key, VersionID: d.VersionID, Status: string(d.Status)}
						if d.Status != storage.DeleteSucceeded {
							it.Err = strings.TrimSpace(d.Code + ": " + d.Message)
						} else if b[i].Size > 0 {
							r.AddBytes(b[i].Size)
						}
						out[i] = it
					}
					return out
				},
			}
			rem, err := pipe.run(ctx, r, scope)
			st.set(rem)
			return err
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			sc, ok := st.retryScope(failed)
			if !ok {
				return taskSpec{}, false
			}
			return s.bkDeleteSpec(loc, sc, title), true
		},
	}
}
