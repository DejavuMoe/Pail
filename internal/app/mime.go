package app

import (
	"fmt"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	mimeFile        = "mime-types.json"
	maxMIMEEntries  = 10000
	fallbackMIMEKey = "application/octet-stream"
)

// MIMETypes returns a copy of the user's extension -> Content-Type table
// (keys are lower-case with a leading dot).
func (s *Service) MIMETypes() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.mime))
	for k, v := range s.mime {
		out[k] = v
	}
	return out
}

// SaveMIMETypes validates, persists and applies the table. Keys are normalised
// to lower-case ".ext" (a missing dot is added); values must be concrete media
// types that mime.ParseMediaType accepts (no wildcards) and are stored in
// canonical form. Two keys that normalise to the same extension are an error.
func (s *Service) SaveMIMETypes(m map[string]string) error {
	const op = "SaveMIMETypes"
	norm, err := normalizeMIMETypes(m)
	if err != nil {
		return invalid(op, "%v", err)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := writeJSONAtomic(s.path(mimeFile), norm); err != nil {
		return classify(op, err)
	}
	s.mu.Lock()
	s.mime = norm
	s.mu.Unlock()
	return nil
}

func normalizeMIMETypes(in map[string]string) (map[string]string, error) {
	if len(in) > maxMIMEEntries {
		return nil, fmt.Errorf("too many entries (maximum %d)", maxMIMEEntries)
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic error reporting
	out := make(map[string]string, len(in))
	for _, k := range keys {
		ext, err := normalizeExtension(k)
		if err != nil {
			return nil, err
		}
		if _, dup := out[ext]; dup {
			return nil, fmt.Errorf("duplicate extension %q", ext)
		}
		ct, err := normalizeMediaType(in[k])
		if err != nil {
			return nil, fmt.Errorf("extension %q: %w", ext, err)
		}
		out[ext] = ct
	}
	return out, nil
}

func normalizeExtension(k string) (string, error) {
	if !utf8.ValidString(k) {
		return "", fmt.Errorf("extension %q is not valid UTF-8", k)
	}
	ext := strings.ToLower(strings.TrimSpace(k))
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if len(ext) < 2 || len(ext) > 64 {
		return "", fmt.Errorf("extension %q must name one final filename extension", k)
	}
	for _, c := range ext[1:] {
		if strings.ContainsRune(`./\:*?"<>|`, c) || unicode.IsSpace(c) || unicode.IsControl(c) {
			return "", fmt.Errorf("extension %q must name one final filename extension", k)
		}
	}
	return ext, nil
}

func normalizeMediaType(v string) (string, error) {
	if !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("content type %q contains invalid characters", v)
	}
	media, params, err := mime.ParseMediaType(strings.TrimSpace(v))
	if err != nil || !strings.Contains(media, "/") || strings.Contains(media, "*") {
		return "", fmt.Errorf("content type %q must be a concrete media type such as text/plain", v)
	}
	canonical := mime.FormatMediaType(media, params)
	if canonical == "" {
		return "", fmt.Errorf("content type %q is not valid", v)
	}
	return canonical, nil
}

// ContentTypeFor returns the Content-Type to upload path with when no upload
// rule overrides it: the user's MIME table, then the extension known to the
// platform and the Go standard library, then application/octet-stream.
func (s *Service) ContentTypeFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		s.mu.Lock()
		ct, ok := s.mime[ext]
		s.mu.Unlock()
		if ok {
			return ct
		}
		if inferred := mime.TypeByExtension(ext); inferred != "" {
			return inferred
		}
	}
	return fallbackMIMEKey
}
