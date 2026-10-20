package app

import (
	"strconv"
	"strings"
	"time"
)

// This file holds the pure comparison rules of a sync preview (no I/O), so that
// they can be read and tested on their own.
//
// Logical metadata that sync uploads write on the objects:
//
//	pail-mtime   local modification time, RFC 3339 with nanoseconds (all uploads)
//	pail-size    logical size before compression (sync uploads)
//	pail-sha256  hex SHA-256 of the local file (sync uploads with Compare=checksum)
//
// ETags are never used as content hashes: a multipart ETag is not a digest of
// the content, and SSE-KMS / SSE-C ETags are not MD5 either. Objects written by
// other tools therefore compare as "no checksum metadata" in checksum mode.
const (
	syMetaSize = "pail-size"
	syMetaSHA  = "pail-sha256"

	// syTimeSlack is the tolerance for "remote is newer" in the download
	// direction: downloaded files are stamped with the remote time, and some file
	// systems (FAT/exFAT) store only 2 s resolution.
	syTimeSlack = 2 * time.Second
)

// Sync job modes, compare rules and directions.
const (
	syModeNew        = "new"
	syModeNewChanged = "new_changed"
	syModeMirror     = "mirror"

	syCmpSizeTime = "size_time"
	syCmpSize     = "size"
	syCmpChecksum = "checksum"

	syUp   = "upload"
	syDown = "download"
)

// syCmp selects the comparison.
type syCmp struct {
	Upload        bool
	Mode, Compare string
}

// syLocalInfo is what the preview knows about a local file.
type syLocalInfo struct {
	Size  int64
	MTime time.Time
	SHA   string // lower-case hex; only in checksum mode
}

// syRemoteInfo is what the preview knows about a remote object. Meta is the
// user metadata from a HEAD (lower-case keys), nil when no HEAD was made.
type syRemoteInfo struct {
	Size       int64
	Modified   time.Time
	Meta       map[string]string
	Compressed bool // the upload rules compress this path: Size is not the logical size
}

// syNeedsHead reports whether deciding about an existing pair requires the
// metadata of the remote object (so that the preview HEADs it).
//
// Download direction: every object that is compared or downloaded is HEADed
// unless the comparison is by size only, so that downloaded files can be given
// the original modification time (pail-mtime).
func syNeedsHead(c syCmp, l *syLocalInfo, r *syRemoteInfo) bool {
	if r == nil {
		return false
	}
	if c.Upload {
		if l == nil || c.Mode == syModeNew {
			return false
		}
		if r.Compressed {
			return true // the logical size lives in pail-size
		}
		if r.Size != l.Size {
			return false // size differs, nothing more to learn
		}
		return c.Compare != syCmpSize
	}
	if c.Compare == syCmpSize {
		return false
	}
	return !(l != nil && c.Mode == syModeNew) // an existing file is skipped without looking
}

// syDecide decides whether the source item has to be copied to the destination.
// Upload: l is the source and r the destination (nil = absent). Download: r is
// the source and l the destination (nil = absent). reason is one of "new",
// "exists", "size differs", "newer", "time differs", "checksum differs",
// "no checksum metadata", "no size metadata" or "unchanged".
func syDecide(c syCmp, l *syLocalInfo, r *syRemoteInfo) (copy bool, reason string) {
	if c.Upload && r == nil || !c.Upload && l == nil {
		return true, "new"
	}
	if c.Mode == syModeNew {
		return false, "exists"
	}
	if c.Upload {
		return syDecideUp(c, l, r)
	}
	return syDecideDown(c, l, r)
}

func syDecideUp(c syCmp, l *syLocalInfo, r *syRemoteInfo) (bool, string) {
	rsize := r.Size
	if v, err := strconv.ParseInt(metaGet(r.Meta, syMetaSize), 10, 64); err == nil && v >= 0 {
		rsize = v
	} else if r.Compressed {
		return true, "no size metadata"
	}
	if rsize != l.Size {
		return true, "size differs"
	}
	switch c.Compare {
	case syCmpSize:
		return false, "unchanged"
	case syCmpChecksum:
		return syChecksumDiffers(r.Meta, l.SHA)
	}
	if mt, ok := syMTime(r.Meta); ok {
		switch {
		case l.MTime.Equal(mt):
			return false, "unchanged"
		case l.MTime.After(mt):
			return true, "newer"
		}
		return true, "time differs"
	}
	if l.MTime.After(r.Modified) {
		return true, "newer"
	}
	return false, "unchanged"
}

func syDecideDown(c syCmp, l *syLocalInfo, r *syRemoteInfo) (bool, string) {
	if r.Size != l.Size {
		return true, "size differs"
	}
	switch c.Compare {
	case syCmpSize:
		return false, "unchanged"
	case syCmpChecksum:
		return syChecksumDiffers(r.Meta, l.SHA)
	}
	if syRemoteTime(r).After(l.MTime.Add(syTimeSlack)) {
		return true, "newer"
	}
	return false, "unchanged"
}

func syChecksumDiffers(meta map[string]string, local string) (bool, string) {
	want := strings.ToLower(strings.TrimSpace(metaGet(meta, syMetaSHA)))
	switch {
	case want == "":
		return true, "no checksum metadata"
	case want != local:
		return true, "checksum differs"
	}
	return false, "unchanged"
}

// syMTime parses pail-mtime.
func syMTime(meta map[string]string) (time.Time, bool) {
	v := metaGet(meta, mtimeMetaKey)
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	return t, err == nil
}

// syRemoteTime is the time of a remote object for "newer" decisions: pail-mtime
// when present, else the service's LastModified.
func syRemoteTime(r *syRemoteInfo) time.Time {
	if t, ok := syMTime(r.Meta); ok {
		return t
	}
	return r.Modified
}
