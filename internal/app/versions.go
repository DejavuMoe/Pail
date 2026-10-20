package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// This file implements object versions (APP_API.md section 7).

// ListVersions returns one page of object versions and delete markers below
// loc.Prefix (one folder level: the delimiter is "/", sub-folders come from
// List). Versions and delete markers are merged into S3's order: by key, newest
// entry of a key first (the entry flagged latest always leads its key).
// Pass the returned markers to get the next page; empty markers mean the end.
func (s *Service) ListVersions(ctx context.Context, loc Location, keyMarker, versionMarker string) (VersionPage, error) {
	const op = "ListVersions"
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return VersionPage{}, err
	}
	in := &s3.ListObjectVersionsInput{
		Bucket: aws.String(loc.Bucket), Delimiter: aws.String("/"), MaxKeys: aws.Int32(bkPageSize), EncodingType: types.EncodingTypeUrl,
	}
	if loc.Prefix != "" {
		in.Prefix = aws.String(loc.Prefix)
	}
	if keyMarker != "" {
		in.KeyMarker = aws.String(keyMarker)
	}
	if versionMarker != "" {
		in.VersionIdMarker = aws.String(versionMarker)
	}
	out, err := c.S3().ListObjectVersions(ctx, in)
	if err != nil {
		return VersionPage{}, classify(op, err)
	}
	dec := bkDecoder(out.EncodingType)
	page := VersionPage{Versions: make([]Version, 0, len(out.Versions)+len(out.DeleteMarkers))}
	for _, v := range out.Versions {
		sc := string(v.StorageClass)
		if sc == "" {
			sc = string(types.ObjectVersionStorageClassStandard)
		}
		page.Versions = append(page.Versions, Version{
			Key: dec(aws.ToString(v.Key)), VersionID: aws.ToString(v.VersionId), Latest: aws.ToBool(v.IsLatest),
			Size: aws.ToInt64(v.Size), Modified: aws.ToTime(v.LastModified), ETag: aws.ToString(v.ETag), StorageClass: sc,
		})
	}
	for _, m := range out.DeleteMarkers {
		page.Versions = append(page.Versions, Version{
			Key: dec(aws.ToString(m.Key)), VersionID: aws.ToString(m.VersionId), Latest: aws.ToBool(m.IsLatest),
			DeleteMarker: true, Modified: aws.ToTime(m.LastModified),
		})
	}
	sort.SliceStable(page.Versions, func(i, j int) bool {
		a, b := page.Versions[i], page.Versions[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Latest != b.Latest {
			return a.Latest
		}
		return a.Modified.After(b.Modified)
	})
	if aws.ToBool(out.IsTruncated) {
		page.NextKeyMarker = dec(aws.ToString(out.NextKeyMarker))
		page.NextVersionMarker = aws.ToString(out.NextVersionIdMarker)
	}
	return page, nil
}

// RestoreVersion makes an old version current again by copying it onto the
// same key; the copy is a new version, no history is deleted. The copy is
// pinned to the ETag that was inspected. Delete markers cannot be restored
// (delete the marker with DeleteVersions to undelete, or use RestoreDeleted).
func (s *Service) RestoreVersion(ctx context.Context, loc Location, ref ObjectRef) error {
	const op = "RestoreVersion"
	if err := bkValidKey(op, ref.Key); err != nil {
		return err
	}
	if ref.VersionID == "" {
		return invalid(op, "a version ID is required")
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return err
	}
	o, _, err := bkHead(ctx, c, loc.Bucket, ref.Key, ref.VersionID)
	if err != nil {
		if bkStatus(err) == 405 {
			return invalid(op, "that version is a delete marker and has no content to restore")
		}
		return classify(op, err)
	}
	if cur, _, err := bkHead(ctx, c, loc.Bucket, ref.Key, ""); err == nil && cur.VersionID == ref.VersionID {
		return invalid(op, "that version is already the current one")
	}
	in := &s3.CopyObjectInput{
		Bucket: aws.String(loc.Bucket), Key: aws.String(ref.Key),
		CopySource:        aws.String(storage.CopySource(loc.Bucket, ref.Key, ref.VersionID)),
		CopySourceIfMatch: aws.String(o.ETag), MetadataDirective: types.MetadataDirectiveCopy,
		StorageClass: bkCopyClass(o.StorageClass),
	}
	return classify(op, c.Copy(ctx, in, o.Size, s.bkTransferOpts(nil)))
}

// bkHistory inspects the versions of exactly one key: deleted reports that its
// latest entry is a delete marker, newest is the newest non-marker version.
func bkHistory(ctx context.Context, c *storage.Client, bucket, key string) (known, deleted bool, newest bkObj, err error) {
	in := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), Prefix: aws.String(key), MaxKeys: aws.Int32(bkPageSize), EncodingType: types.EncodingTypeUrl}
	latestSeen := false
	for pages := 0; pages < 50; pages++ {
		out, e := c.S3().ListObjectVersions(ctx, in)
		if e != nil {
			return false, false, bkObj{}, classify("ListVersions", e)
		}
		dec := bkDecoder(out.EncodingType)
		beyond := false
		for _, m := range out.DeleteMarkers {
			if dec(aws.ToString(m.Key)) == key {
				known = true
				if aws.ToBool(m.IsLatest) {
					deleted, latestSeen = true, true
				}
			}
		}
		for _, v := range out.Versions {
			k := dec(aws.ToString(v.Key))
			if k > key {
				beyond = true
			}
			if k != key {
				continue
			}
			known = true
			if aws.ToBool(v.IsLatest) {
				latestSeen = true
			}
			if newest.VersionID == "" {
				sc := string(v.StorageClass)
				if sc == "" {
					sc = string(types.ObjectStorageClassStandard)
				}
				newest = bkObj{Key: key, VersionID: aws.ToString(v.VersionId), ETag: aws.ToString(v.ETag), Size: aws.ToInt64(v.Size),
					Modified: aws.ToTime(v.LastModified), StorageClass: sc, Resolved: true}
			}
		}
		if !aws.ToBool(out.IsTruncated) || beyond || (latestSeen && newest.VersionID != "") {
			break
		}
		if k := dec(aws.ToString(out.NextKeyMarker)); k > key {
			break
		}
		// The response was URL-encoded; request markers must be raw.
		in.KeyMarker = aws.String(dec(aws.ToString(out.NextKeyMarker)))
		in.VersionIdMarker = out.NextVersionIdMarker
	}
	return known, deleted, newest, nil
}

// RestoreDeleted undeletes keys: for every selected key whose latest entry is
// a delete marker, the newest earlier version is copied onto the key as the
// new current version. The write is create-only (If-None-Match: * where the
// provider honours it, a HEAD check otherwise), so an upload that happens
// meanwhile always wins and the item is skipped. Keys that are not deleted are
// skipped with "not deleted". Folders and "All" list the bucket's versions and
// restore every deleted key below; only explicitly named keys are reported when
// they are not deleted.
func (s *Service) RestoreDeleted(sel Selection) (string, error) {
	const op = "RestoreDeleted"
	scope, err := bkBuildScope(op, sel, false, false)
	if err != nil {
		return "", err
	}
	return s.submit(s.bkRestoreSpec(sel.Location, scope, bkTitle("Restore deleted", sel))), nil
}

func (s *Service) bkRestoreSpec(loc Location, scope bkScope, title string) taskSpec {
	st := &bkState{}
	return taskSpec{
		Kind:   KindOther,
		Title:  title,
		Detail: bkURL(loc),
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, "RestoreDeleted", loc)
			if err != nil {
				return err
			}
			pipe := &bkPipe{
				workers: s.bkWorkers(), batch: 1, lister: bkListDeleted(c, loc.Bucket),
				process: func(ctx context.Context, b []bkObj) []ItemResult {
					return []ItemResult{s.bkRestoreOne(ctx, r, c, loc, b[0])}
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
			return s.bkRestoreSpec(loc, sc, title), true
		},
	}
}

func (s *Service) bkRestoreOne(ctx context.Context, r *run, c *storage.Client, loc Location, o bkObj) ItemResult {
	res := ItemResult{Key: o.Key}
	if !o.Resolved {
		known, deleted, newest, err := bkHistory(ctx, c, loc.Bucket, o.Key)
		switch {
		case err != nil:
			return bkFail(ctx, res, err)
		case !known:
			return bkFailf(res, "no such key")
		case !deleted:
			res.Status, res.Err = ItemSkipped, "not deleted"
			return res
		case newest.VersionID == "":
			res.Status, res.Err = ItemSkipped, "no earlier version to restore"
			return res
		}
		o = newest
		r.AddTotal(o.Size, 0)
	}
	progress := bkProgress(r)
	dec, err := resolveRemoteConflict(ctx, c, loc.Bucket, o.Key, ConflictSkip, o.Size, o.Modified)
	if err != nil {
		return bkFail(ctx, res, err)
	}
	if dec.Skip {
		progress(o.Size)
		res.Status, res.Err = ItemSkipped, "not deleted: a newer object exists"
		return res
	}
	err = s.writeConditional(loc.AccountID, dec, func(ifNoneMatch, ifMatch string) error {
		in := &s3.CopyObjectInput{
			Bucket: aws.String(loc.Bucket), Key: aws.String(o.Key),
			CopySource:        aws.String(storage.CopySource(loc.Bucket, o.Key, o.VersionID)),
			CopySourceIfMatch: aws.String(o.ETag), MetadataDirective: types.MetadataDirectiveCopy,
			StorageClass: bkCopyClass(o.StorageClass),
		}
		if ifNoneMatch != "" {
			in.IfNoneMatch = aws.String(ifNoneMatch)
		}
		return c.Copy(ctx, in, o.Size, s.bkTransferOpts(progress))
	})
	if err != nil {
		if bkStatus(err) == 412 || storage.ErrorCode(err) == "PreconditionFailed" {
			// Either the key was re-created (the upload wins) or the source
			// version changed; tell them apart.
			if t, herr := headTarget(ctx, c, loc.Bucket, o.Key); herr == nil && t.exists {
				res.Status, res.Err = ItemSkipped, "a new object was uploaded meanwhile; it was left alone"
				return res
			}
		}
		return bkFail(ctx, res, err)
	}
	progress(o.Size)
	res.Status = ItemSucceeded
	return res
}

// DeleteVersions permanently deletes the given versions (and delete markers)
// with batched DeleteObjects calls. There is no undo. The explicit version ID
// "null" is allowed: it names the null version of an unversioned or
// version-suspended bucket.
func (s *Service) DeleteVersions(loc Location, refs []ObjectRef) (string, error) {
	const op = "DeleteVersions"
	if err := checkLocation(op, loc, true); err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", invalid(op, "no versions were given")
	}
	var scope bkScope
	seen := map[ObjectRef]bool{}
	for _, ref := range refs {
		if err := bkValidKey(op, ref.Key); err != nil {
			return "", err
		}
		if ref.VersionID == "" {
			return "", invalid(op, "%q has no version ID; use Delete for the current object", ref.Key)
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		scope.Objs = append(scope.Objs, bkObj{Key: ref.Key, VersionID: ref.VersionID})
	}
	title := fmt.Sprintf("Delete %d versions", len(scope.Objs))
	if len(scope.Objs) == 1 {
		title = "Delete a version of " + entryName(scope.Objs[0].Key)
	}
	return s.submit(s.bkDeleteSpec(loc, scope, title)), nil
}
