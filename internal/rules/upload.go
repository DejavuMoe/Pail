package rules

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Encryption holds references, never raw customer key material.
type Encryption struct {
	Type           string `json:"type"`
	KMSKeyID       string `json:"kmsKeyId,omitempty"`
	CustomerKeyRef string `json:"customerKeyRef,omitempty"`
}
type Compression struct {
	Method      string `json:"method"`
	Level       int    `json:"level"`
	PasswordRef string `json:"passwordRef,omitempty"`
}
type UploadRule struct {
	Name          string            `json:"name"`
	BucketPattern string            `json:"bucketPattern,omitempty"`
	KeyPattern    string            `json:"keyPattern,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	StorageClass  string            `json:"storageClass,omitempty"`
	SSE           *Encryption       `json:"sse,omitempty"`
	Compression   *Compression      `json:"compression,omitempty"`
}
type UploadOptions struct {
	Headers      map[string]string `json:"headers,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StorageClass string            `json:"storageClass,omitempty"`
	SSE          *Encryption       `json:"sse,omitempty"`
	Compression  *Compression      `json:"compression,omitempty"`
	MatchedRules []string          `json:"matchedRules,omitempty"`
}

// globCache holds compiled patterns; rule sets are small and long-lived, and
// Match runs once per rule per object, so compiling each time is wasteful.
var globCache sync.Map // string -> *regexp.Regexp

func compileGlob(pattern string) *regexp.Regexp {
	if re, ok := globCache.Load(pattern); ok {
		return re.(*regexp.Regexp)
	}
	var b strings.Builder
	b.WriteString("(?s)^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	actual, _ := globCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp)
}

// Match is case sensitive: * crosses slash boundaries, ? matches one rune.
// Other characters are literal; an empty mask matches all. No key normalization.
func Match(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	return compileGlob(pattern).MatchString(value)
}

// Validate checks every rule including disabled and currently nonmatching rules.
func Validate(rules []UploadRule) error {
	for i, r := range rules {
		if err := validateRule(r); err != nil {
			return fmt.Errorf("rule %d (%s): %w", i, r.Name, err)
		}
	}
	return nil
}

// Evaluate selects first match independently for headers+metadata, storage
// class, SSE, and compression. Empty categories do not claim precedence.
// MatchedRules includes only rules contributing a selected category.
func Evaluate(rules []UploadRule, bucket, key string) (UploadOptions, error) {
	if err := Validate(rules); err != nil {
		return UploadOptions{}, err
	}
	out := UploadOptions{Headers: map[string]string{}, Metadata: map[string]string{}}
	headersSelected := false
	for _, r := range rules {
		if r.Disabled || !Match(r.BucketPattern, bucket) || !Match(r.KeyPattern, key) {
			continue
		}
		selected := false
		if !headersSelected && (len(r.Headers) > 0 || len(r.Metadata) > 0) {
			for k, v := range r.Headers {
				out.Headers[http.CanonicalHeaderKey(k)] = v
			}
			for k, v := range r.Metadata {
				out.Metadata[strings.ToLower(k)] = v
			}
			headersSelected, selected = true, true
		}
		if out.StorageClass == "" && r.StorageClass != "" {
			out.StorageClass = r.StorageClass
			selected = true
		}
		if out.SSE == nil && r.SSE != nil {
			v := *r.SSE
			out.SSE = &v
			selected = true
		}
		if out.Compression == nil && r.Compression != nil {
			v := *r.Compression
			out.Compression = &v
			selected = true
		}
		if selected {
			out.MatchedRules = append(out.MatchedRules, r.Name)
		}
	}
	return out, nil
}
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var token = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

func validateRule(r UploadRule) error {
	seen := map[string]bool{}
	for _, k := range sortedKeys(r.Headers) {
		canonical := http.CanonicalHeaderKey(k)
		lower := strings.ToLower(k)
		if !token.MatchString(k) || strings.ContainsAny(r.Headers[k], "\r\n\x00") {
			return fmt.Errorf("invalid header %q", k)
		}
		if seen[canonical] {
			return fmt.Errorf("duplicate case-insensitive header %q", k)
		}
		seen[canonical] = true
		if lower == "authorization" || lower == "host" || lower == "content-length" || lower == "transfer-encoding" || lower == "connection" || strings.HasPrefix(lower, "x-amz-") {
			return fmt.Errorf("header %q cannot override authentication or S3 policy", k)
		}
	}
	seen = map[string]bool{}
	for _, k := range sortedKeys(r.Metadata) {
		lower := strings.ToLower(k)
		if !token.MatchString(k) || strings.ContainsAny(r.Metadata[k], "\r\n\x00") {
			return fmt.Errorf("invalid metadata %q", k)
		}
		if seen[lower] {
			return fmt.Errorf("duplicate metadata %q", k)
		}
		seen[lower] = true
	}
	if strings.ContainsAny(r.StorageClass, "\r\n\x00") {
		return fmt.Errorf("invalid storage class")
	}
	if s := r.SSE; s != nil {
		switch s.Type {
		case "AES256":
			if s.KMSKeyID != "" || s.CustomerKeyRef != "" {
				return fmt.Errorf("SSE-S3 cannot include key fields")
			}
		case "aws:kms":
			if s.CustomerKeyRef != "" {
				return fmt.Errorf("SSE-KMS cannot include customer key")
			}
		case "SSE-C":
			if s.CustomerKeyRef == "" || s.KMSKeyID != "" {
				return fmt.Errorf("SSE-C requires only customer key reference")
			}
		default:
			return fmt.Errorf("unsupported encryption type %q", s.Type)
		}
		if strings.ContainsAny(s.KMSKeyID+s.CustomerKeyRef, "\r\n\x00") {
			return fmt.Errorf("invalid key reference")
		}
	}
	if c := r.Compression; c != nil {
		if c.Method != "gzip" && c.Method != "zip" {
			return fmt.Errorf("unsupported compression %q", c.Method)
		}
		if strings.ContainsAny(c.PasswordRef, "\r\n\x00") || (c.PasswordRef != "" && c.Method != "zip") {
			return fmt.Errorf("password reference is only valid for ZIP")
		}
		if c.Level < -2 || c.Level > 9 {
			return fmt.Errorf("compression level must be -2..9")
		}
	}
	return nil
}
