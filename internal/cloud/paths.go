package cloud

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxInvalidationPaths = 3000
	maxInvalidationBytes = 4000
)

// NormalizeInvalidationPaths validates and canonicalizes invalidation paths:
// a leading "/" is added when missing, paths are percent-escaped the way
// CloudFront expects (non-ASCII and spaces escaped, existing escapes kept),
// "*" is allowed only as the final character, duplicates are removed and the
// result is sorted. Fragments, backslashes, control characters, malformed
// escapes, URLs with a scheme or host, and paths over 4000 bytes are rejected,
// as are more than 3000 paths.
func NormalizeInvalidationPaths(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxInvalidationPaths {
		return nil, fmt.Errorf("provide 1 to %d invalidation paths", maxInvalidationPaths)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		p, err := normalizeOne(strings.Trim(raw, " \t"))
		if err != nil {
			return nil, err
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func normalizeOne(raw string) (string, error) {
	if !utf8.ValidString(raw) || raw == "" || strings.ContainsAny(raw, "#\\") {
		return "", fmt.Errorf("invalid invalidation path %q: use a URL path without a fragment or backslash", raw)
	}
	for _, c := range raw {
		if c < 32 || c == 127 {
			return "", errors.New("invalidation paths cannot contain control characters")
		}
	}
	if !strings.HasPrefix(raw, "/") {
		if first, _, _ := strings.Cut(raw, "/"); strings.Contains(first, ":") {
			return "", fmt.Errorf("invalid invalidation path %q: give the path only, not a URL", raw)
		}
		raw = "/" + raw
	}
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("invalid invalidation path %q: it starts with a host", raw)
	}
	if strings.Count(raw, "*") > 1 || (strings.Contains(raw, "*") && !strings.HasSuffix(raw, "*")) {
		return "", errors.New("a wildcard is only allowed as the last character")
	}
	wildcard := strings.HasSuffix(raw, "*")
	u, err := url.ParseRequestURI(strings.TrimSuffix(raw, "*"))
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", fmt.Errorf("invalid URL path %q", raw)
	}
	normalized := u.EscapedPath()
	if u.RawQuery != "" || u.ForceQuery {
		if _, err := url.QueryUnescape(u.RawQuery); err != nil {
			return "", errors.New("invalid percent escape in query string")
		}
		if strings.IndexFunc(u.RawQuery, func(r rune) bool { return r >= 127 }) >= 0 {
			return "", errors.New("URL-encode non-ASCII query strings")
		}
		if strings.ContainsAny(u.RawQuery, " \t") {
			return "", errors.New("URL-encode spaces in query strings")
		}
		normalized += "?" + u.RawQuery
	}
	if wildcard {
		normalized += "*"
	}
	if len(normalized) > maxInvalidationBytes {
		return "", fmt.Errorf("invalidation paths must be at most %d bytes", maxInvalidationBytes)
	}
	return normalized, nil
}
