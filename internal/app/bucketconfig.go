package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// bmNotConfigured lists the error codes that mean "this sub-resource is not
// set". A 403 is deliberately absent: it is AccessDenied, never "not configured".
var bmNotConfigured = map[string]bool{
	"NoSuchBucketPolicy":                             true,
	"NoSuchCORSConfiguration":                        true,
	"NoSuchLifecycleConfiguration":                   true,
	"ServerSideEncryptionConfigurationNotFoundError": true,
	"NoSuchWebsiteConfiguration":                     true,
	"NoSuchPublicAccessBlockConfiguration":           true,
	"OwnershipControlsNotFoundError":                 true,
	"ObjectLockConfigurationNotFoundError":           true,
	"ReplicationConfigurationNotFoundError":          true,
	"NoSuchTagSet":                                   true,
	"NoSuchTagSetError":                              true,
}

// bmRequestPayment and bmAccelerate are the JSON shapes of the two
// configurations whose SDK inputs are not a plain configuration struct.
type bmRequestPayment struct{ Payer types.Payer }
type bmAccelerate struct{ Status types.BucketAccelerateStatus }

func bmValidConfig(op string, c BucketConfig) error {
	switch c {
	case ConfigVersioning, ConfigEncryption, ConfigPolicy, ConfigCORS, ConfigLifecycle, ConfigLogging, ConfigTagging,
		ConfigWebsite, ConfigPublicAccessBlock, ConfigOwnership, ConfigObjectLock, ConfigRequestPayment,
		ConfigAccelerate, ConfigReplication:
		return nil
	}
	return invalid(op, "unknown bucket configuration %q", string(c))
}

func bmIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

// GetBucketConfig returns the configuration as indented JSON in the shape of
// the AWS SDK structure, or "" when the bucket has none.
func (s *Service) GetBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig) (string, error) {
	const op = "GetBucketConfig"
	if err := bmValidConfig(op, c); err != nil {
		return "", err
	}
	cl, err := s.bucketClient(ctx, op, Location{AccountID: accountID, Bucket: bucket})
	if err != nil {
		return "", err
	}
	out, err := bmGet(ctx, cl.S3(), bucket, c)
	if err != nil {
		if bmNotConfigured[storage.ErrorCode(err)] {
			return "", nil
		}
		return "", classify(op, err)
	}
	if out == nil {
		return "", nil
	}
	if p, ok := out.(string); ok { // policy: the document itself
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(p), "", "  ") != nil {
			return p, nil
		}
		return buf.String(), nil
	}
	js, err := bmIndent(out)
	if err != nil {
		return "", classify(op, err)
	}
	return js, nil
}

// bmGet returns the payload to marshal, or nil when the service answered
// successfully but the configuration is empty.
func bmGet(ctx context.Context, c *s3.Client, bucket string, cfg BucketConfig) (any, error) {
	b := aws.String(bucket)
	switch cfg {
	case ConfigVersioning:
		o, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: b})
		if err != nil {
			return nil, err
		}
		if o.Status == "" && o.MFADelete == "" {
			return nil, nil // never enabled
		}
		return types.VersioningConfiguration{Status: o.Status, MFADelete: types.MFADelete(o.MFADelete)}, nil
	case ConfigEncryption:
		o, err := c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: b})
		if err != nil || o.ServerSideEncryptionConfiguration == nil {
			return nil, err
		}
		return o.ServerSideEncryptionConfiguration, nil
	case ConfigPolicy:
		o, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: b})
		if err != nil || aws.ToString(o.Policy) == "" {
			return nil, err
		}
		return aws.ToString(o.Policy), nil
	case ConfigCORS:
		o, err := c.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: b})
		if err != nil || len(o.CORSRules) == 0 {
			return nil, err
		}
		return types.CORSConfiguration{CORSRules: o.CORSRules}, nil
	case ConfigLifecycle:
		o, err := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: b})
		if err != nil || len(o.Rules) == 0 {
			return nil, err
		}
		return types.BucketLifecycleConfiguration{Rules: o.Rules}, nil
	case ConfigLogging:
		o, err := c.GetBucketLogging(ctx, &s3.GetBucketLoggingInput{Bucket: b})
		if err != nil || o.LoggingEnabled == nil {
			return nil, err
		}
		return types.BucketLoggingStatus{LoggingEnabled: o.LoggingEnabled}, nil
	case ConfigTagging:
		o, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: b})
		if err != nil || len(o.TagSet) == 0 {
			return nil, err
		}
		return types.Tagging{TagSet: o.TagSet}, nil
	case ConfigWebsite:
		o, err := c.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{Bucket: b})
		if err != nil {
			return nil, err
		}
		return types.WebsiteConfiguration{ErrorDocument: o.ErrorDocument, IndexDocument: o.IndexDocument,
			RedirectAllRequestsTo: o.RedirectAllRequestsTo, RoutingRules: o.RoutingRules}, nil
	case ConfigPublicAccessBlock:
		o, err := c.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: b})
		if err != nil || o.PublicAccessBlockConfiguration == nil {
			return nil, err
		}
		return o.PublicAccessBlockConfiguration, nil
	case ConfigOwnership:
		o, err := c.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: b})
		if err != nil || o.OwnershipControls == nil {
			return nil, err
		}
		return o.OwnershipControls, nil
	case ConfigObjectLock:
		o, err := c.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: b})
		if err != nil || o.ObjectLockConfiguration == nil {
			return nil, err
		}
		return o.ObjectLockConfiguration, nil
	case ConfigRequestPayment:
		o, err := c.GetBucketRequestPayment(ctx, &s3.GetBucketRequestPaymentInput{Bucket: b})
		if err != nil || o.Payer == "" {
			return nil, err
		}
		return bmRequestPayment{Payer: o.Payer}, nil
	case ConfigAccelerate:
		o, err := c.GetBucketAccelerateConfiguration(ctx, &s3.GetBucketAccelerateConfigurationInput{Bucket: b})
		if err != nil || o.Status == "" {
			return nil, err
		}
		return bmAccelerate{Status: o.Status}, nil
	case ConfigReplication:
		o, err := c.GetBucketReplication(ctx, &s3.GetBucketReplicationInput{Bucket: b})
		if err != nil || o.ReplicationConfiguration == nil {
			return nil, err
		}
		return o.ReplicationConfiguration, nil
	}
	return nil, errors.New("unreachable")
}

// bmDecode parses js into v, rejecting unknown fields and trailing data.
func bmDecode(js string, v any) error {
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// PutBucketConfig applies a configuration given as JSON in the shape GetBucketConfig
// returns. Unknown fields are rejected so that typos are not silently dropped.
func (s *Service) PutBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig, js string) error {
	const op = "PutBucketConfig"
	if err := bmValidConfig(op, c); err != nil {
		return err
	}
	if strings.TrimSpace(js) == "" {
		return invalid(op, "configuration JSON is empty")
	}
	b := aws.String(bucket)
	var call func(cl *s3.Client) error
	parse := func(v any) error {
		if err := bmDecode(js, v); err != nil {
			return invalid(op, "invalid %s configuration: %v", string(c), err)
		}
		return nil
	}
	switch c {
	case ConfigVersioning:
		var v types.VersioningConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		if v.Status != types.BucketVersioningStatusEnabled && v.Status != types.BucketVersioningStatusSuspended {
			return invalid(op, `versioning Status must be "Enabled" or "Suspended"`)
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: b, VersioningConfiguration: &v})
			return err
		}
	case ConfigEncryption:
		var v types.ServerSideEncryptionConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{Bucket: b, ServerSideEncryptionConfiguration: &v})
			return err
		}
	case ConfigPolicy:
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(js), &doc); err != nil || doc == nil {
			return invalid(op, "the policy must be a JSON object")
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: b, Policy: aws.String(js)})
			return err
		}
	case ConfigCORS:
		var v types.CORSConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketCors(ctx, &s3.PutBucketCorsInput{Bucket: b, CORSConfiguration: &v})
			return err
		}
	case ConfigLifecycle:
		var v types.BucketLifecycleConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{Bucket: b, LifecycleConfiguration: &v})
			return err
		}
	case ConfigLogging:
		var v types.BucketLoggingStatus
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketLogging(ctx, &s3.PutBucketLoggingInput{Bucket: b, BucketLoggingStatus: &v})
			return err
		}
	case ConfigTagging:
		var v types.Tagging
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{Bucket: b, Tagging: &v})
			return err
		}
	case ConfigWebsite:
		var v types.WebsiteConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{Bucket: b, WebsiteConfiguration: &v})
			return err
		}
	case ConfigPublicAccessBlock:
		var v types.PublicAccessBlockConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{Bucket: b, PublicAccessBlockConfiguration: &v})
			return err
		}
	case ConfigOwnership:
		var v types.OwnershipControls
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketOwnershipControls(ctx, &s3.PutBucketOwnershipControlsInput{Bucket: b, OwnershipControls: &v})
			return err
		}
	case ConfigObjectLock:
		var v types.ObjectLockConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{Bucket: b, ObjectLockConfiguration: &v})
			return err
		}
	case ConfigRequestPayment:
		var v bmRequestPayment
		if err := parse(&v); err != nil {
			return err
		}
		if v.Payer != types.PayerRequester && v.Payer != types.PayerBucketOwner {
			return invalid(op, `Payer must be "Requester" or "BucketOwner"`)
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketRequestPayment(ctx, &s3.PutBucketRequestPaymentInput{Bucket: b,
				RequestPaymentConfiguration: &types.RequestPaymentConfiguration{Payer: v.Payer}})
			return err
		}
	case ConfigAccelerate:
		var v bmAccelerate
		if err := parse(&v); err != nil {
			return err
		}
		if v.Status != types.BucketAccelerateStatusEnabled && v.Status != types.BucketAccelerateStatusSuspended {
			return invalid(op, `accelerate Status must be "Enabled" or "Suspended"`)
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketAccelerateConfiguration(ctx, &s3.PutBucketAccelerateConfigurationInput{Bucket: b,
				AccelerateConfiguration: &types.AccelerateConfiguration{Status: v.Status}})
			return err
		}
	case ConfigReplication:
		var v types.ReplicationConfiguration
		if err := parse(&v); err != nil {
			return err
		}
		call = func(cl *s3.Client) error {
			_, err := cl.PutBucketReplication(ctx, &s3.PutBucketReplicationInput{Bucket: b, ReplicationConfiguration: &v})
			return err
		}
	}
	cl, err := s.bucketClient(ctx, op, Location{AccountID: accountID, Bucket: bucket})
	if err != nil {
		return err
	}
	return classify(op, call(cl.S3()))
}

// DeleteBucketConfig removes a configuration. Only configurations that have a
// delete API can be removed; the others must be overwritten with Put.
func (s *Service) DeleteBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig) error {
	const op = "DeleteBucketConfig"
	if err := bmValidConfig(op, c); err != nil {
		return err
	}
	switch c {
	case ConfigPolicy, ConfigCORS, ConfigLifecycle, ConfigEncryption, ConfigWebsite, ConfigTagging,
		ConfigPublicAccessBlock, ConfigOwnership, ConfigReplication:
	default:
		return invalid(op, "%s cannot be deleted; change it with a new value instead", string(c))
	}
	cl, err := s.bucketClient(ctx, op, Location{AccountID: accountID, Bucket: bucket})
	if err != nil {
		return err
	}
	c3, b := cl.S3(), aws.String(bucket)
	switch c {
	case ConfigPolicy:
		_, err = c3.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: b})
	case ConfigCORS:
		_, err = c3.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{Bucket: b})
	case ConfigLifecycle:
		_, err = c3.DeleteBucketLifecycle(ctx, &s3.DeleteBucketLifecycleInput{Bucket: b})
	case ConfigEncryption:
		_, err = c3.DeleteBucketEncryption(ctx, &s3.DeleteBucketEncryptionInput{Bucket: b})
	case ConfigWebsite:
		_, err = c3.DeleteBucketWebsite(ctx, &s3.DeleteBucketWebsiteInput{Bucket: b})
	case ConfigTagging:
		_, err = c3.DeleteBucketTagging(ctx, &s3.DeleteBucketTaggingInput{Bucket: b})
	case ConfigPublicAccessBlock:
		_, err = c3.DeletePublicAccessBlock(ctx, &s3.DeletePublicAccessBlockInput{Bucket: b})
	case ConfigOwnership:
		_, err = c3.DeleteBucketOwnershipControls(ctx, &s3.DeleteBucketOwnershipControlsInput{Bucket: b})
	case ConfigReplication:
		_, err = c3.DeleteBucketReplication(ctx, &s3.DeleteBucketReplicationInput{Bucket: b})
	}
	return classify(op, err)
}
