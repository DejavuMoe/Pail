package app

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/rules"
)

const uploadRulesFile = "upload-rules.json"

// UploadRules returns a deep copy of the saved upload rules.
func (s *Service) UploadRules() []rules.UploadRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRules(s.rules)
}

// SaveUploadRules validates, persists and applies the rules. Besides
// rules.Validate it rejects headers that an S3 upload cannot store (everything
// except the five standard content headers and Expires; use Metadata for
// custom values) and an unparsable Expires.
func (s *Service) SaveUploadRules(list []rules.UploadRule) error {
	const op = "SaveUploadRules"
	if err := validateUploadRules(list); err != nil {
		return invalid(op, "%v", err)
	}
	cp := cloneRules(list)
	if cp == nil {
		cp = []rules.UploadRule{}
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := writeJSONAtomic(s.path(uploadRulesFile), cp); err != nil {
		return classify(op, err)
	}
	s.mu.Lock()
	s.rules = cp
	s.mu.Unlock()
	return nil
}

// PreviewUploadRules evaluates the saved rules for an object that would be
// uploaded to bucket/key (before any ".zip" suffix is added).
func (s *Service) PreviewUploadRules(bucket, key string) (rules.UploadOptions, error) {
	opts, err := rules.Evaluate(s.UploadRules(), bucket, key)
	if err != nil {
		return rules.UploadOptions{}, invalid("PreviewUploadRules", "%v", err)
	}
	return opts, nil
}

func validateUploadRules(list []rules.UploadRule) error {
	if err := rules.Validate(list); err != nil {
		return err
	}
	for i, r := range list {
		for k, v := range r.Headers {
			switch http.CanonicalHeaderKey(k) {
			case "Content-Type", "Content-Encoding", "Content-Disposition", "Content-Language", "Cache-Control":
			case "Expires":
				if _, err := http.ParseTime(v); err != nil {
					return fmt.Errorf("rule %d (%s): invalid Expires %q: %w", i, r.Name, v, err)
				}
			default:
				return fmt.Errorf("rule %d (%s): header %q cannot be stored on an object; use Metadata for custom values", i, r.Name, k)
			}
		}
	}
	return nil
}

func cloneRules(in []rules.UploadRule) []rules.UploadRule {
	if in == nil {
		return nil
	}
	out := make([]rules.UploadRule, len(in))
	for i, r := range in {
		r.Headers = cloneStringMap(r.Headers)
		r.Metadata = cloneStringMap(r.Metadata)
		if r.SSE != nil {
			v := *r.SSE
			r.SSE = &v
		}
		if r.Compression != nil {
			v := *r.Compression
			r.Compression = &v
		}
		out[i] = r
	}
	return out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// uploadRequireHTTPS enforces that customer-provided encryption keys travel
// only over TLS. An account without a custom endpoint is AWS (always https).
func (s *Service) uploadRequireHTTPS(op, accountID string) error {
	rec, ok := s.account(accountID)
	if !ok {
		return opErr(op, ErrNotFound, "no account %q", accountID)
	}
	if rec.Endpoint == "" {
		return nil
	}
	u, err := url.Parse(rec.Endpoint)
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return invalid(op, "SSE-C sends the encryption key with every request and requires an https endpoint; account %q uses %q", rec.Name, rec.Endpoint)
	}
	return nil
}

// applyUploadOptions copies rule output onto in: user metadata, storage class,
// headers and server-side encryption. The caller handles Content-Type
// priority, compression and the pail-mtime metadata.
func (s *Service) applyUploadOptions(ctx context.Context, in *s3.PutObjectInput, o rules.UploadOptions, accountID string) error {
	const op = "Upload"
	in.Metadata = make(map[string]string, len(o.Metadata)+1)
	for k, v := range o.Metadata {
		in.Metadata[strings.ToLower(k)] = v
	}
	if o.StorageClass != "" {
		in.StorageClass = s3types.StorageClass(o.StorageClass)
	}
	for k, v := range o.Headers {
		value := v
		switch http.CanonicalHeaderKey(k) {
		case "Content-Type":
			in.ContentType = &value
		case "Content-Encoding":
			in.ContentEncoding = &value
		case "Content-Disposition":
			in.ContentDisposition = &value
		case "Content-Language":
			in.ContentLanguage = &value
		case "Cache-Control":
			in.CacheControl = &value
		case "Expires":
			when, err := http.ParseTime(v)
			if err != nil {
				return invalid(op, "rule header Expires %q is not a valid HTTP date", v)
			}
			in.Expires = &when
		}
	}
	if o.SSE == nil {
		return nil
	}
	switch o.SSE.Type {
	case "AES256":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
		if o.SSE.KMSKeyID != "" {
			in.SSEKMSKeyId = aws.String(o.SSE.KMSKeyID)
		}
	case "SSE-C":
		if err := s.uploadRequireHTTPS(op, accountID); err != nil {
			return err
		}
		key, err := s.secretValue(ctx, o.SSE.CustomerKeyRef)
		if err != nil {
			return err
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
		if err != nil || len(raw) != 32 {
			return invalid(op, "the SSE-C key %q must be a base64 encoded 32-byte key", o.SSE.CustomerKeyRef)
		}
		sum := md5.Sum(raw)
		in.SSECustomerAlgorithm = aws.String("AES256")
		in.SSECustomerKey = aws.String(base64.StdEncoding.EncodeToString(raw))
		in.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(sum[:]))
	default:
		return invalid(op, "unsupported encryption type %q", o.SSE.Type)
	}
	return nil
}

var errGzipEncodingConflict = errors.New("a rule sets Content-Encoding and also asks for gzip compression")
