// Package storage is the S3 protocol layer built on the official AWS SDK v2.
// Simple calls go through Client.S3(); this package adds only the logic the SDK
// does not provide: safe transfers, large copies, per-item batch deletes,
// presigning and capability probes.
package storage

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

// Checksum modes for Config.ChecksumMode.
const (
	ChecksumAuto          = "auto"           // custom endpoint: when_required; AWS: SDK default
	ChecksumWhenRequired  = "when_required"  // only checksums the API mandates
	ChecksumWhenSupported = "when_supported" // SDK default behaviour
)

// Config describes one account. Secrets are plain strings here; keeping them
// out of files is the caller's job.
type Config struct {
	Endpoint              string
	Region                string
	AccessKeyID           string
	SecretAccessKey       string
	SessionToken          string
	Profile               string
	UseDefaultCredentials bool
	// Anonymous sends unsigned requests (public buckets). Mutually exclusive
	// with explicit credentials, the default chain and RoleARN.
	Anonymous          bool
	UsePathStyle       bool
	RoleARN            string
	ExternalID         string
	RoleSessionName    string
	InsecureSkipVerify bool
	ChecksumMode       string
	// HTTPClient overrides the transport (tests). When nil NewHTTPClient is used.
	HTTPClient aws.HTTPClient
}

// NewHTTPClient returns a client with connection-level timeouts but no overall
// request timeout, so multi-gigabyte transfers are never cut off.
func NewHTTPClient(insecureSkipVerify bool) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecureSkipVerify},
	}}
}

func (cfg Config) validate() error {
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("endpoint must be an HTTP(S) URL without credentials, query or fragment")
		}
	}
	if (cfg.AccessKeyID == "") != (cfg.SecretAccessKey == "") {
		return errors.New("access key ID and secret access key must both be supplied")
	}
	if cfg.Anonymous && (cfg.AccessKeyID != "" || cfg.UseDefaultCredentials || cfg.RoleARN != "") {
		return errors.New("anonymous access cannot be combined with credentials or a role")
	}
	switch cfg.ChecksumMode {
	case "", ChecksumAuto, ChecksumWhenRequired, ChecksumWhenSupported:
	default:
		return fmt.Errorf("unknown checksum mode %q", cfg.ChecksumMode)
	}
	return nil
}

// AWSConfig builds the SDK configuration shared by S3 and other AWS services:
// base credentials (static or default chain), then AssumeRole when RoleARN is set.
func (cfg Config) AWSConfig(ctx context.Context) (aws.Config, error) {
	if err := cfg.validate(); err != nil {
		return aws.Config{}, err
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	var hc aws.HTTPClient = cfg.HTTPClient
	if hc == nil {
		hc = NewHTTPClient(cfg.InsecureSkipVerify)
	}
	var ac aws.Config
	switch {
	case cfg.Anonymous:
		ac = aws.Config{Region: cfg.Region, HTTPClient: hc, Credentials: aws.AnonymousCredentials{}}
	case cfg.AccessKeyID != "":
		ac = aws.Config{Region: cfg.Region, HTTPClient: hc,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken))}
	case cfg.UseDefaultCredentials:
		opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region), awsconfig.WithHTTPClient(hc)}
		if cfg.Profile != "" {
			opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
		}
		var err error
		if ac, err = awsconfig.LoadDefaultConfig(ctx, opts...); err != nil {
			return aws.Config{}, fmt.Errorf("load AWS configuration: %w", err)
		}
	default:
		return aws.Config{}, errors.New("explicit credentials are required; enable UseDefaultCredentials to opt into the AWS credential chain")
	}
	if cfg.RoleARN != "" {
		// A custom endpoint (MinIO, Ceph) usually serves STS itself.
		stsClient := sts.NewFromConfig(ac, func(o *sts.Options) {
			if cfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(strings.TrimRight(cfg.Endpoint, "/"))
			}
		})
		ac.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(stsClient, cfg.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			if cfg.RoleSessionName != "" {
				o.RoleSessionName = cfg.RoleSessionName
			}
			if cfg.ExternalID != "" {
				o.ExternalID = aws.String(cfg.ExternalID)
			}
		}))
	}
	ac.AppID = appID // part of the User-Agent of every S3 and STS request
	return ac, nil
}

// appID identifies this application in the SDK User-Agent.
const appID = "Pail"

// Client wraps one configured S3 client.
type Client struct {
	sdk *s3.Client
	// reqChecksum mirrors the SDK client setting for the multipart uploader,
	// which otherwise defaults to adding CRC32 on its own.
	reqChecksum aws.RequestChecksumCalculation
}

// New builds a client. No network traffic happens until the first call.
func New(ctx context.Context, cfg Config) (*Client, error) {
	ac, err := cfg.AWSConfig(ctx)
	if err != nil {
		return nil, err
	}
	whenRequired := cfg.ChecksumMode == ChecksumWhenRequired || (cfg.ChecksumMode == "" || cfg.ChecksumMode == ChecksumAuto) && cfg.Endpoint != ""
	c := &Client{reqChecksum: aws.RequestChecksumCalculationWhenSupported}
	if whenRequired || cfg.ChecksumMode != ChecksumWhenSupported && ac.RequestChecksumCalculation == aws.RequestChecksumCalculationWhenRequired {
		c.reqChecksum = aws.RequestChecksumCalculationWhenRequired
	}
	c.sdk = s3.NewFromConfig(ac, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(strings.TrimRight(cfg.Endpoint, "/"))
		}
		o.APIOptions = append(o.APIOptions, leaveAcceptEncodingUnsigned)
		switch {
		case whenRequired:
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		case cfg.ChecksumMode == ChecksumWhenSupported:
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenSupported
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenSupported
		}
	})
	return c, nil
}

// S3 returns the underlying SDK client for simple calls (Head, List, Put, Tagging, ...).
// It uses the standard SDK retryer (3 attempts).
func (c *Client) S3() *s3.Client { return c.sdk }

// ErrorCode returns the S3/smithy error code, or "" when err carries none.
func ErrorCode(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

// Bucket is one ListBuckets entry. Region is empty when the service does not
// report it; use BucketRegion to look it up lazily.
type Bucket struct {
	Name         string
	CreationDate time.Time
	Region       string
}

// ListBuckets returns every bucket, following continuation tokens.
func (c *Client) ListBuckets(ctx context.Context) ([]Bucket, error) {
	var out []Bucket
	in := &s3.ListBucketsInput{}
	for {
		page, err := c.sdk.ListBuckets(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, b := range page.Buckets {
			out = append(out, Bucket{Name: aws.ToString(b.Name), CreationDate: aws.ToTime(b.CreationDate), Region: aws.ToString(b.BucketRegion)})
		}
		if aws.ToString(page.ContinuationToken) == "" {
			return out, nil
		}
		in.ContinuationToken = page.ContinuationToken
	}
}

// BucketRegion resolves a bucket's region with GetBucketLocation.
func (c *Client) BucketRegion(ctx context.Context, bucket string) (string, error) {
	out, err := c.sdk.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil {
		return "", err
	}
	switch r := string(out.LocationConstraint); r {
	case "":
		return "us-east-1", nil
	case "EU":
		return "eu-west-1", nil
	default:
		return r, nil
	}
}

// CopySource builds an x-amz-copy-source value with correct escaping.
func CopySource(bucket, key, versionID string) string {
	source := escapeCopySource(bucket) + "/" + escapeCopySource(key)
	if versionID != "" {
		source += "?versionId=" + url.QueryEscape(versionID)
	}
	return source
}

// escapeCopySource percent-encodes every byte except unreserved characters
// and "/". url.PathEscape keeps sub-delimiters such as "+", which AWS and
// VersityGW decode as a space in x-amz-copy-source.
func escapeCopySource(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// PresignedRequest is a signed URL plus the headers that must accompany it.
type PresignedRequest struct {
	URL    string
	Method string
	Header http.Header
}

// Presign signs a GET, PUT or HEAD request for up to seven days.
func (c *Client) Presign(ctx context.Context, method, bucket, key, versionID string, expires time.Duration) (*PresignedRequest, error) {
	if expires <= 0 || expires > 7*24*time.Hour {
		return nil, errors.New("presign expiry must be between zero and seven days")
	}
	p := s3.NewPresignClient(c.sdk)
	opt := func(o *s3.PresignOptions) { o.Expires = expires }
	var version *string
	if versionID != "" {
		version = aws.String(versionID)
	}
	var out *v4.PresignedHTTPRequest
	var err error
	switch strings.ToUpper(method) {
	case http.MethodGet:
		out, err = p.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: version}, opt)
	case http.MethodHead:
		out, err = p.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: version}, opt)
	case http.MethodPut:
		if versionID != "" {
			return nil, errors.New("PUT presigning does not accept a version ID")
		}
		out, err = p.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, opt)
	default:
		return nil, errors.New("supported presign methods are GET, PUT and HEAD")
	}
	if err != nil {
		return nil, err
	}
	return &PresignedRequest{URL: out.URL, Method: out.Method, Header: out.SignedHeader}, nil
}
