package app

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// RestoreTier is how fast an archived object is restored (S3 Glacier
// retrieval options).
type RestoreTier string

const (
	TierStandard  RestoreTier = "Standard"
	TierBulk      RestoreTier = "Bulk"
	TierExpedited RestoreTier = "Expedited"
)

// MaxRestoreDays bounds the days a restored copy stays available.
const MaxRestoreDays = 365

// ArchiveRestoreOptions configures RestoreArchived.
type ArchiveRestoreOptions struct {
	Days int         // how long the restored copy stays available, 1 to MaxRestoreDays
	Tier RestoreTier // default TierStandard
}

// IsArchiveClass reports whether objects of storage class sc must be restored
// before they can be read.
func IsArchiveClass(sc string) bool {
	return sc == string(types.StorageClassGlacier) || sc == string(types.StorageClassDeepArchive)
}

// RestoreState is the restore status of an object, parsed from x-amz-restore.
type RestoreState struct {
	InProgress bool
	Expiry     string // when the restored copy goes away, as the service wrote it
}

// ParseRestore reads an x-amz-restore value such as
// `ongoing-request="false", expiry-date="Fri, 21 Dec 2012 00:00:00 GMT"`.
// ok is false for an empty value (no restore requested).
func ParseRestore(v string) (st RestoreState, ok bool) {
	if v == "" {
		return st, false
	}
	st.InProgress = strings.Contains(v, `ongoing-request="true"`)
	if _, after, found := strings.Cut(v, `expiry-date="`); found {
		st.Expiry, _, _ = strings.Cut(after, `"`)
	}
	return st, true
}

// RestoreArchived asks the service to restore the archived objects of a
// selection (S3 Glacier Flexible Retrieval and Deep Archive) for opt.Days, as
// one Other task. The restore itself takes minutes to hours, depending on the
// tier and the class; the object shows its progress in x-amz-restore.
// Objects in other classes are skipped; so are objects whose restore is
// already running. Expedited is not available for Deep Archive.
func (s *Service) RestoreArchived(sel Selection, opt ArchiveRestoreOptions) (string, error) {
	const op = "RestoreArchived"
	if opt.Tier == "" {
		opt.Tier = TierStandard
	}
	switch opt.Tier {
	case TierStandard, TierBulk, TierExpedited:
	default:
		return "", invalid(op, "unknown restore tier %q", opt.Tier)
	}
	if opt.Days < 1 || opt.Days > MaxRestoreDays {
		return "", invalid(op, "days must be between 1 and %d", MaxRestoreDays)
	}
	scope, err := bkBuildScope(op, sel, true, false)
	if err != nil {
		return "", err
	}
	return s.submit(s.bkArchiveSpec(sel.Location, scope, opt, bkTitle("Restore from archive", sel))), nil
}

func (s *Service) bkArchiveSpec(loc Location, scope bkScope, opt ArchiveRestoreOptions, title string) taskSpec {
	st := &bkState{}
	return taskSpec{
		Kind:   KindOther,
		Title:  title,
		Detail: bkURL(loc),
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, "RestoreArchived", loc)
			if err != nil {
				return err
			}
			pipe := &bkPipe{
				workers: s.bkWorkers(), batch: 1, lister: bkListObjects(c, loc.Bucket),
				process: func(ctx context.Context, b []bkObj) []ItemResult {
					return []ItemResult{bkArchiveOne(ctx, c, loc, opt, b[0])}
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
			return s.bkArchiveSpec(loc, sc, opt, title), true
		},
	}
}

func bkArchiveOne(ctx context.Context, c *storage.Client, loc Location, opt ArchiveRestoreOptions, o bkObj) ItemResult {
	res := ItemResult{Key: o.Key, VersionID: o.VersionID}
	if strings.HasSuffix(o.Key, "/") {
		res.Status, res.Err = ItemSkipped, "folder placeholder"
		return res
	}
	h, err := headObj(ctx, c, "Head", loc.Bucket, o.Key, o.VersionID)
	if err != nil {
		return bkFail(ctx, res, err)
	}
	class := string(h.StorageClass)
	if !IsArchiveClass(class) {
		res.Status, res.Err = ItemSkipped, "not in an archive storage class"
		return res
	}
	if rs, ok := ParseRestore(aws.ToString(h.Restore)); ok && rs.InProgress {
		res.Status, res.Err = ItemSkipped, "a restore is already in progress"
		return res
	}
	if opt.Tier == TierExpedited && class == string(types.StorageClassDeepArchive) {
		return bkFailf(res, "Expedited retrieval is not available for Deep Archive; use Standard or Bulk")
	}
	// RestoreArchived validates Days; checked again here, next to the
	// conversion, so that no caller can pass a value int32 cannot hold.
	if opt.Days < 1 || opt.Days > MaxRestoreDays {
		return bkFailf(res, "days must be between 1 and %d", MaxRestoreDays)
	}
	days := int32(opt.Days)
	in := &s3.RestoreObjectInput{
		Bucket: aws.String(loc.Bucket), Key: aws.String(o.Key),
		RestoreRequest: &types.RestoreRequest{
			Days:                 aws.Int32(days),
			GlacierJobParameters: &types.GlacierJobParameters{Tier: types.Tier(opt.Tier)},
		},
	}
	if o.VersionID != "" {
		in.VersionId = aws.String(o.VersionID)
	}
	if _, err := c.S3().RestoreObject(ctx, in); err != nil {
		if storage.ErrorCode(err) == "RestoreAlreadyInProgress" {
			res.Status, res.Err = ItemSkipped, "a restore is already in progress"
			return res
		}
		return bkFail(ctx, res, err)
	}
	res.Status = ItemSucceeded
	return res
}
