package app

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/DejavuMoe/Pail/internal/safepath"
)

// This file holds what the upload and download halves of the transfer subsystem
// (upload.go, download.go) share. Layout of the subsystem:
//
//	scan.go          local folder walk (uploads) and remote listing (downloads)
//	upload.go        Upload: scan task -> one KindUpload task per file
//	download.go      Download: scan task -> one KindDownload task per file
//	decode.go        client-side gzip / ZIP / AES-ZIP decoding of downloads
//	upload_rules.go  persisted upload rules and how they become PutObject fields
//	mime.go          persisted MIME table and Content-Type selection
//
// Tasks report their outcome through the queue contract in queue.go: a
// per-file task returns an error when the transfer failed (the queue then
// retries it by running the same spec again) and reports one ItemResult when it
// succeeded or was skipped.

const (
	// mtimeMetaKey is the user metadata key that carries the local modification
	// time (RFC 3339, nanoseconds) of an uploaded file. Sync compares with it.
	mtimeMetaKey = "pail-mtime"
	// compressionMetaKey marks objects whose compression Pail applied on
	// upload: "gzip", "zip" or "aes-zip". Auto decode on download keys off it.
	compressionMetaKey = "pail-compression"
	// maxKeyBytes is the S3 limit for an object key.
	maxKeyBytes = 1024
	// defaultMaxDecoded bounds decoded output when the request sets no limit.
	defaultMaxDecoded = int64(1) << 30
	// folderContentType matches what CreateFolder writes for folder objects.
	folderContentType = "application/x-directory"
)

// internalMetaPrefix is the user-metadata prefix of Pail's own keys.
const internalMetaPrefix = "pail-"

// legacyMetaPrefix is the user-metadata prefix that builds before the rename to Pail wrote ("opens3-"); readers still accept it.
const legacyMetaPrefix = "opens3-"

// isInternalMeta reports whether a (lower-case) user-metadata key belongs to
// Pail's own bookkeeping, under the current or the legacy prefix.
func isInternalMeta(k string) bool {
	return strings.HasPrefix(k, internalMetaPrefix) || strings.HasPrefix(k, legacyMetaPrefix)
}

// metaGet reads an internal metadata value by its current name ("pail-...")
// and falls back to the legacy name ("opens3-...") that older builds wrote,
// so objects synced or compressed by an older build keep working.
func metaGet(meta map[string]string, key string) string {
	if v := meta[key]; v != "" {
		return v
	}
	if rest, ok := strings.CutPrefix(key, internalMetaPrefix); ok {
		return meta[legacyMetaPrefix+rest]
	}
	return ""
}

// transferPolicy resolves an empty request policy to the configured default.
func (s *Service) transferPolicy(op string, p ConflictPolicy) (ConflictPolicy, error) {
	if p == "" {
		p = s.Settings().Conflict
	}
	if !p.Valid() {
		return "", invalid(op, "unknown conflict policy %q", p)
	}
	return p, nil
}

// partSize is the configured multipart part size in bytes (0 = SDK default).
func (s *Service) partSize() int64 {
	return int64(s.Settings().PartSizeMiB) << 20
}

// requireKnownAccount fails with NotFound for an unknown account ID.
func (s *Service) requireKnownAccount(op, id string) error {
	if _, ok := s.account(id); !ok {
		return opErr(op, ErrNotFound, "no account %q", id)
	}
	return nil
}

// keyDir returns the "folder part" of an object key, with its trailing "/":
// "a/b/c.txt" -> "a/b/", "a/b/" -> "a/", "c.txt" -> "".
func keyDir(key string) string {
	trimmed := strings.TrimSuffix(key, "/")
	return trimmed[:len(trimmed)-len(path.Base(trimmed))]
}

// versionedName inserts " (v<first 8 chars of the version id>)" before the
// last extension of name, the way numberedName inserts a counter.
func versionedName(name, versionID string) string {
	short := versionID
	if len(short) > 8 {
		short = short[:8]
	}
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (v%s)%s", strings.TrimSuffix(name, ext), short, ext)
}

// versionedKey applies versionedName to the last segment of a key.
func versionedKey(key, versionID string) string {
	dir, name := path.Split(key)
	return dir + versionedName(name, versionID)
}

// numberedPath applies numberedName to the base name of a local path.
func numberedPath(p string, n int) string {
	return filepath.Join(filepath.Dir(p), numberedName(filepath.Base(p), n))
}

// localJoin is safepath.Join wrapped as an Invalid OpError.
func localJoin(op, root, key string) (string, error) {
	p, err := safepath.Join(root, key)
	if err != nil {
		return "", invalid(op, "%v", err)
	}
	return p, nil
}
