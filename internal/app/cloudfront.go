package app

import (
	"context"

	"github.com/DejavuMoe/Pail/internal/cloud"
)

// maxInvalidationPaths is the CloudFront limit per invalidation request.
const maxInvalidationPaths = 3000

// cloudTestEndpoint redirects CloudFront calls to a mock server. It is a test
// hook: production code never sets it.
var cloudTestEndpoint string

// cfClient returns a CloudFront client for an AWS account. CloudFront is an AWS
// service, so every other provider is rejected without any network traffic.
func (s *Service) cfClient(ctx context.Context, op, accountID string) (*cloud.Client, error) {
	if accountID == "" {
		return nil, invalid(op, "account is required")
	}
	s.mu.Lock()
	rec, ok := s.accountLocked(accountID)
	s.mu.Unlock()
	if !ok {
		return nil, opErr(op, ErrNotFound, "no account %q", accountID)
	}
	if rec.Provider != ProviderAWS {
		return nil, opErr(op, ErrUnsupported, "CloudFront is only available for AWS accounts; %q uses provider %q", rec.Name, rec.Provider)
	}
	if ep := cloudTestEndpoint; ep != "" {
		e, err := s.entry(ctx, accountID)
		if err != nil {
			return nil, classify(op, err)
		}
		c, err := cloud.New(ctx, e.cfg, cloud.WithBaseEndpoint(ep))
		return c, classify(op, err)
	}
	c, err := s.cloudClient(ctx, accountID)
	if err != nil {
		return nil, classify(op, err)
	}
	return c, nil
}

func bmCloudID(op, kind, v string) error {
	if v == "" {
		return invalid(op, "%s ID is required", kind)
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return invalid(op, "%s ID %q must be alphanumeric", kind, v)
		}
	}
	return nil
}

// Distributions lists the account's CloudFront distributions.
func (s *Service) Distributions(ctx context.Context, accountID string) ([]cloud.Distribution, error) {
	const op = "Distributions"
	c, err := s.cfClient(ctx, op, accountID)
	if err != nil {
		return nil, err
	}
	ds, err := c.ListDistributions(ctx)
	if err != nil {
		return nil, classify(op, err)
	}
	return ds, nil
}

// DistributionsForBucket lists the distributions that have an S3 origin of the bucket.
func (s *Service) DistributionsForBucket(ctx context.Context, accountID, bucket string) ([]cloud.Distribution, error) {
	const op = "DistributionsForBucket"
	if bucket == "" {
		return nil, invalid(op, "bucket is required")
	}
	ds, err := s.Distributions(ctx, accountID)
	if err != nil {
		return nil, classify(op, err)
	}
	return cloud.DistributionsForBucket(bucket, ds), nil
}

// Invalidate submits one invalidation. Paths are normalised (leading "/",
// escaping, de-duplication). More than 3000 paths are rejected rather than
// split, so one call is always exactly one CloudFront invalidation.
func (s *Service) Invalidate(ctx context.Context, accountID, distID string, paths []string) (cloud.Invalidation, error) {
	const op = "Invalidate"
	if err := bmCloudID(op, "distribution", distID); err != nil {
		return cloud.Invalidation{}, err
	}
	if len(paths) == 0 {
		return cloud.Invalidation{}, invalid(op, "provide at least one path")
	}
	if len(paths) > maxInvalidationPaths {
		return cloud.Invalidation{}, invalid(op, "at most %d paths per invalidation (got %d); split them into several requests", maxInvalidationPaths, len(paths))
	}
	norm, err := cloud.NormalizeInvalidationPaths(paths)
	if err != nil {
		return cloud.Invalidation{}, invalid(op, "%v", err)
	}
	c, err := s.cfClient(ctx, op, accountID)
	if err != nil {
		return cloud.Invalidation{}, err
	}
	inv, err := c.CreateInvalidation(ctx, distID, norm, cloud.NewCallerReference())
	if err != nil {
		return cloud.Invalidation{}, classify(op, err)
	}
	return *inv, nil
}

// Invalidation fetches one invalidation, including its paths and status.
func (s *Service) Invalidation(ctx context.Context, accountID, distID, id string) (cloud.Invalidation, error) {
	const op = "Invalidation"
	if err := bmCloudID(op, "distribution", distID); err != nil {
		return cloud.Invalidation{}, err
	}
	if err := bmCloudID(op, "invalidation", id); err != nil {
		return cloud.Invalidation{}, err
	}
	c, err := s.cfClient(ctx, op, accountID)
	if err != nil {
		return cloud.Invalidation{}, err
	}
	inv, err := c.GetInvalidation(ctx, distID, id)
	if err != nil {
		return cloud.Invalidation{}, classify(op, err)
	}
	return *inv, nil
}

// Invalidations lists the most recent invalidations of a distribution (no paths).
func (s *Service) Invalidations(ctx context.Context, accountID, distID string) ([]cloud.Invalidation, error) {
	const op = "Invalidations"
	if err := bmCloudID(op, "distribution", distID); err != nil {
		return nil, err
	}
	c, err := s.cfClient(ctx, op, accountID)
	if err != nil {
		return nil, err
	}
	out, err := c.ListInvalidations(ctx, distID)
	if err != nil {
		return nil, classify(op, err)
	}
	return out, nil
}
