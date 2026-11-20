package app

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

var bmIPv4RE = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

// bmValidateBucketName checks the general-purpose bucket naming rules.
func bmValidateBucketName(name string) error {
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	switch {
	case len(name) < 3 || len(name) > 63:
		return errors.New("bucket name must be 3 to 63 characters long")
	case !alnum(name[0]) || !alnum(name[len(name)-1]):
		return errors.New("bucket name must start and end with a lowercase letter or digit")
	case strings.Contains(name, ".."):
		return errors.New("bucket name must not contain two adjacent periods")
	case bmIPv4RE.MatchString(name):
		return errors.New("bucket name must not look like an IP address")
	case strings.HasPrefix(name, "xn--"):
		return errors.New(`bucket name must not start with "xn--"`)
	case strings.HasSuffix(name, "-s3alias"), strings.HasSuffix(name, "--ol-s3"):
		return errors.New(`bucket name must not end with "-s3alias" or "--ol-s3"`)
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !alnum(c) && c != '.' && c != '-' {
			return errors.New("bucket name may only contain lowercase letters, digits, periods and hyphens")
		}
	}
	return nil
}

// CreateBucket creates a bucket. region "" or "us-east-1" sends no
// LocationConstraint. objectLock enables Object Lock (which also turns on
// versioning) at creation time; it cannot be added later through this call.
func (s *Service) CreateBucket(ctx context.Context, accountID, name, region string, objectLock bool) error {
	const op = "CreateBucket"
	if err := checkLocation(op, Location{AccountID: accountID}, false); err != nil {
		return err
	}
	if err := bmValidateBucketName(name); err != nil {
		return invalid(op, "%v", err)
	}
	region = strings.TrimSpace(region)
	c, err := s.storageClient(ctx, accountID)
	if err != nil {
		return classify(op, err)
	}
	in := &s3.CreateBucketInput{Bucket: aws.String(name)}
	if region != "" && region != "us-east-1" {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(region)}
	}
	if objectLock {
		in.ObjectLockEnabledForBucket = aws.Bool(true)
	}
	if _, err := c.S3().CreateBucket(ctx, in); err != nil {
		return classify(op, err)
	}
	if region != "" {
		s.mu.Lock()
		s.bucketRegion[accountID+"/"+name] = region
		s.mu.Unlock()
	}
	// Refresh the region cache from the service; best effort, the bucket exists.
	_, _ = s.Buckets(ctx, accountID)
	return nil
}

// DeleteBucket deletes an empty bucket.
func (s *Service) DeleteBucket(ctx context.Context, accountID, name string) error {
	const op = "DeleteBucket"
	c, err := s.bucketClient(ctx, op, Location{AccountID: accountID, Bucket: name})
	if err != nil {
		return err
	}
	_, err = c.S3().DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)})
	if err != nil {
		err = classify(op, err)
		var oe *OpError
		if errors.As(err, &oe) && oe.Code == "BucketNotEmpty" {
			cp := *oe
			cp.Message = "the bucket is not empty; empty it first (and remove all object versions and incomplete multipart uploads)"
			return &cp
		}
		return err
	}
	s.mu.Lock()
	delete(s.bucketRegion, accountID+"/"+name)
	s.mu.Unlock()
	_ = s.SetBucketAlias(accountID, name, "") // the name is gone; so is its label (best effort)
	return nil
}

// bmRemaining reports the placeholder item that makes a cancelled or failed
// EmptyBucket retryable: the objects not yet listed cannot be enumerated.
func bmRemaining(r *run, reason string) {
	r.Item(ItemResult{Key: "*", Status: ItemUnprocessed, Err: reason})
}

// EmptyBucket deletes every object of the bucket in a background task. With
// allVersions every version and delete marker is removed; otherwise only the
// current objects (a versioned bucket keeps its history and gains delete
// markers). Retry re-runs the whole operation, which is idempotent.
func (s *Service) EmptyBucket(accountID, name string, allVersions bool) (string, error) {
	const op = "EmptyBucket"
	if err := checkLocation(op, Location{AccountID: accountID, Bucket: name}, true); err != nil {
		return "", err
	}
	title := "Empty bucket " + name
	if allVersions {
		title += " (all versions)"
	}
	var spec taskSpec
	spec = taskSpec{
		Kind: KindOther, Title: title, Detail: "s3://" + name + "/",
		Work: func(ctx context.Context, r *run) error {
			c, err := s.storageClient(ctx, accountID)
			if err != nil {
				bmRemaining(r, err.Error())
				return classify(op, err)
			}
			list, what := bkListObjects(c, name), "objects"
			if allVersions {
				list, what = bkListVersions(c, name), "versions"
			}
			// Each page is deleted before the next is listed: deleting while
			// listing the current objects is what lets the listing end.
			err = list(ctx, bkScan{}, func(page []bkObj, _ string) error {
				if len(page) > 0 {
					ids := make([]storage.ObjectID, len(page))
					for i, o := range page {
						ids[i] = storage.ObjectID{Key: o.Key, VersionID: o.VersionID}
					}
					for _, d := range c.DeleteObjects(ctx, name, ids) {
						r.Item(deleteItem(d))
					}
				}
				return ctx.Err()
			})
			switch {
			case err == nil:
				return nil
			case ctx.Err() != nil:
				bmRemaining(r, "canceled before all "+what+" were deleted")
				return ctx.Err()
			default:
				bmRemaining(r, err.Error())
				return classify(op, err)
			}
		},
		Retry: func([]ItemResult) (taskSpec, bool) { return spec, true },
	}
	return s.submit(spec), nil
}
