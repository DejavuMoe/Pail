// Package cloud wraps the AWS-only services Pail manages beyond S3. It
// currently covers CloudFront distributions and invalidations.
package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cft "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// Client talks to CloudFront with the same credential options as storage.
type Client struct{ cf *cloudfront.Client }

// Option customises New.
type Option func(*cloudfront.Options)

// WithBaseEndpoint redirects CloudFront calls to a mock server (tests). The
// storage.Config.Endpoint is deliberately ignored: it points at an S3-compatible
// service, never at CloudFront.
func WithBaseEndpoint(url string) Option {
	return func(o *cloudfront.Options) { o.BaseEndpoint = aws.String(url) }
}

// New builds a client from an account's storage.Config (static keys, profile or
// default chain, AssumeRole). It performs no network I/O.
func New(ctx context.Context, cfg storage.Config, opts ...Option) (*Client, error) {
	ac, err := cfg.AWSConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{cf: cloudfront.NewFromConfig(ac, func(o *cloudfront.Options) {
		for _, opt := range opts {
			opt(o)
		}
	})}, nil
}

// Origin is one distribution origin.
type Origin struct{ ID, DomainName, Path string }

// Distribution is the subset of a CloudFront distribution the UI needs.
type Distribution struct {
	ID, DomainName, Status, Comment string
	Aliases                         []string
	Enabled                         bool
	Origins                         []Origin
}

// Invalidation is a submitted invalidation. Paths is empty for list entries.
type Invalidation struct {
	ID, DistributionID, Status, CallerReference string
	CreateTime                                  time.Time
	Paths                                       []string
}

const maxListed = 1000

// ListDistributions returns every distribution, following pagination.
func (c *Client) ListDistributions(ctx context.Context) ([]Distribution, error) {
	var out []Distribution
	in := &cloudfront.ListDistributionsInput{}
	for {
		page, err := c.cf.ListDistributions(ctx, in)
		if err != nil {
			return nil, err
		}
		if page.DistributionList == nil {
			return out, nil
		}
		for _, s := range page.DistributionList.Items {
			out = append(out, mapDistribution(s))
		}
		if !aws.ToBool(page.DistributionList.IsTruncated) || aws.ToString(page.DistributionList.NextMarker) == "" {
			return out, nil
		}
		in.Marker = page.DistributionList.NextMarker
	}
}

func mapDistribution(s cft.DistributionSummary) Distribution {
	d := Distribution{ID: aws.ToString(s.Id), DomainName: aws.ToString(s.DomainName), Status: aws.ToString(s.Status), Comment: aws.ToString(s.Comment), Enabled: aws.ToBool(s.Enabled)}
	if s.Aliases != nil {
		d.Aliases = append(d.Aliases, s.Aliases.Items...)
	}
	if s.Origins != nil {
		for _, o := range s.Origins.Items {
			d.Origins = append(d.Origins, Origin{ID: aws.ToString(o.Id), DomainName: aws.ToString(o.DomainName), Path: aws.ToString(o.OriginPath)})
		}
	}
	return d
}

func validID(kind, v string) error {
	if v == "" {
		return fmt.Errorf("%s ID is required", kind)
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return fmt.Errorf("%s ID %q must be alphanumeric", kind, v)
		}
	}
	return nil
}

// NewCallerReference returns a unique reference for CreateInvalidation. Reuse
// the same value when retrying an ambiguous submission: CloudFront then returns
// the original invalidation instead of creating a second one.
func NewCallerReference() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("pail-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// CreateInvalidation normalizes paths and submits them. An empty callerReference
// is replaced by a fresh NewCallerReference.
func (c *Client) CreateInvalidation(ctx context.Context, distID string, paths []string, callerReference string) (*Invalidation, error) {
	if err := validID("distribution", distID); err != nil {
		return nil, err
	}
	norm, err := NormalizeInvalidationPaths(paths)
	if err != nil {
		return nil, err
	}
	if callerReference == "" {
		callerReference = NewCallerReference()
	}
	out, err := c.cf.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(distID),
		InvalidationBatch: &cft.InvalidationBatch{
			CallerReference: aws.String(callerReference),
			Paths:           &cft.Paths{Quantity: aws.Int32(int32(len(norm))), Items: norm},
		},
	})
	if err != nil {
		return nil, err
	}
	inv, err := mapInvalidation(distID, out.Invalidation)
	if err != nil {
		return nil, err
	}
	if inv.CallerReference != callerReference {
		return nil, errors.New("invalidation response does not match the submitted request")
	}
	return inv, nil
}

// GetInvalidation fetches one invalidation, including its paths.
func (c *Client) GetInvalidation(ctx context.Context, distID, id string) (*Invalidation, error) {
	if err := validID("distribution", distID); err != nil {
		return nil, err
	}
	if err := validID("invalidation", id); err != nil {
		return nil, err
	}
	out, err := c.cf.GetInvalidation(ctx, &cloudfront.GetInvalidationInput{DistributionId: aws.String(distID), Id: aws.String(id)})
	if err != nil {
		return nil, err
	}
	inv, err := mapInvalidation(distID, out.Invalidation)
	if err != nil {
		return nil, err
	}
	if inv.ID != id {
		return nil, errors.New("invalidation response ID mismatch")
	}
	return inv, nil
}

func mapInvalidation(distID string, v *cft.Invalidation) (*Invalidation, error) {
	if v == nil || aws.ToString(v.Id) == "" || v.InvalidationBatch == nil || v.InvalidationBatch.Paths == nil {
		return nil, errors.New("incomplete invalidation response")
	}
	status := aws.ToString(v.Status)
	if status != "InProgress" && status != "Completed" {
		return nil, fmt.Errorf("unexpected invalidation status %q", status)
	}
	return &Invalidation{ID: aws.ToString(v.Id), DistributionID: distID, Status: status, CallerReference: aws.ToString(v.InvalidationBatch.CallerReference),
		CreateTime: aws.ToTime(v.CreateTime), Paths: append([]string(nil), v.InvalidationBatch.Paths.Items...)}, nil
}

// ListInvalidations returns the most recent invalidations (at most 1000), newest
// first as CloudFront reports them. Entries carry no paths; use GetInvalidation.
func (c *Client) ListInvalidations(ctx context.Context, distID string) ([]Invalidation, error) {
	if err := validID("distribution", distID); err != nil {
		return nil, err
	}
	var out []Invalidation
	in := &cloudfront.ListInvalidationsInput{DistributionId: aws.String(distID)}
	for len(out) < maxListed {
		page, err := c.cf.ListInvalidations(ctx, in)
		if err != nil {
			return nil, err
		}
		if page.InvalidationList == nil {
			break
		}
		for _, s := range page.InvalidationList.Items {
			out = append(out, Invalidation{ID: aws.ToString(s.Id), DistributionID: distID, Status: aws.ToString(s.Status), CreateTime: aws.ToTime(s.CreateTime)})
		}
		if !aws.ToBool(page.InvalidationList.IsTruncated) || aws.ToString(page.InvalidationList.NextMarker) == "" {
			break
		}
		in.Marker = page.InvalidationList.NextMarker
	}
	if len(out) > maxListed {
		out = out[:maxListed]
	}
	return out, nil
}

func bucketOriginRE(bucket string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(strings.ToLower(bucket)) + `\.s3(?:-website)?(?:[.-](?:dualstack\.)?[a-z0-9-]+)?\.amazonaws\.com(?:\.cn)?$`)
}

func normalizeDomain(d string) string { return strings.TrimSuffix(strings.ToLower(d), ".") }

// OriginMatchesBucket reports whether an origin domain is an S3 REST or website
// endpoint of bucket: bucket.s3.amazonaws.com, bucket.s3.<region>.amazonaws.com,
// the legacy bucket.s3-<region> form, dualstack names, the China partition and
// bucket.s3-website[-.]<region> website endpoints. Look-alike hosts such as
// bucket.s3.amazonaws.com.evil.example do not match.
func OriginMatchesBucket(domain, bucket string) bool {
	return bucket != "" && bucketOriginRE(bucket).MatchString(normalizeDomain(domain))
}

// DistributionsForBucket returns the distributions with at least one origin that
// is an S3 endpoint of bucket, in input order.
func DistributionsForBucket(bucket string, ds []Distribution) []Distribution {
	if bucket == "" {
		return nil
	}
	re := bucketOriginRE(bucket)
	var out []Distribution
	for _, d := range ds {
		for _, o := range d.Origins {
			if re.MatchString(normalizeDomain(o.DomainName)) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}
