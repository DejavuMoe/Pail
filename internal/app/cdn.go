package app

import (
	"errors"
	"net/url"
	"strings"
)

const (
	cdnPlaceBucket = "{bucket}"
	cdnPlaceKey    = "{key}"
)

// normalizeCDN trims and validates the CDN settings of a.
func normalizeCDN(a *Account) error {
	var err error
	if a.CDNURL, err = NormalizeCDNURL(a.CDNURL); err != nil {
		return err
	}
	a.CDNBuckets, err = NormalizeCDNBuckets(a.CDNBuckets)
	return err
}

// NormalizeCDNURL trims and validates a CDN base URL or template. A base URL
// ("https://cdn.example.com") yields <base>/<bucket>/<key>; a template must
// contain {key} and may contain {bucket}. Empty is valid (no CDN).
func NormalizeCDNURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	tmpl := strings.Contains(s, cdnPlaceKey)
	if !tmpl && strings.Contains(s, cdnPlaceBucket) {
		return s, errors.New("CDN URL template with {bucket} must also contain {key}")
	}
	if !tmpl {
		s = strings.TrimRight(s, "/")
	}
	sample := strings.NewReplacer(cdnPlaceBucket, "bucket", cdnPlaceKey, "key").Replace(s)
	u, err := url.Parse(sample)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(sample, "?#") {
		return s, errors.New("CDN URL must be an http(s) URL without credentials, query or fragment")
	}
	return s, nil
}

// NormalizeCDNBuckets trims the names, drops empty and duplicate entries and
// checks that each is a valid bucket name. An empty result is nil (all buckets).
func NormalizeCDNBuckets(list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, b := range list {
		b = strings.TrimSpace(b)
		if b == "" || seen[b] {
			continue
		}
		if err := bmValidateBucketName(b); err != nil {
			return nil, errors.New("CDN buckets: " + b + ": " + err.Error())
		}
		seen[b] = true
		out = append(out, b)
	}
	return out, nil
}

// CDNURL returns the CDN link of an object, or "" when the account has no CDN
// configured, the bucket is excluded by the account's CDN bucket list, the
// bucket is empty, the account is unknown, or key is a folder ("" or ends in "/").
// No request is made.
func (s *Service) CDNURL(loc Location, key string) string {
	s.mu.Lock()
	rec, ok := s.accountLocked(loc.AccountID)
	s.mu.Unlock()
	if !ok || rec.CDNURL == "" || loc.Bucket == "" || key == "" || strings.HasSuffix(key, "/") {
		return ""
	}
	if len(rec.CDNBuckets) > 0 {
		found := false
		for _, b := range rec.CDNBuckets {
			if b == loc.Bucket {
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	esc := escapeKey(key)
	if strings.Contains(rec.CDNURL, cdnPlaceKey) {
		return strings.NewReplacer(cdnPlaceBucket, loc.Bucket, cdnPlaceKey, esc).Replace(rec.CDNURL)
	}
	return strings.TrimRight(rec.CDNURL, "/") + "/" + loc.Bucket + "/" + esc
}

// HasCDN reports whether objects of loc's bucket get a CDN link (key-independent).
func (s *Service) HasCDN(loc Location) bool {
	return s.CDNURL(loc, "x") != ""
}
