package app

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// conflictDecision is how a write to an S3 destination key proceeds under a
// ConflictPolicy, decided from a HEAD of the destination (see caps.go for why
// the HEAD comes first).
type conflictDecision struct {
	Key        string // final destination key (differs from the request for rename)
	Skip       bool   // do not write; report the item as "skipped" with Reason
	Reason     string
	CreateOnly bool   // send If-None-Match: * — the key must not exist at commit
	IfMatch    string // send If-Match: <etag> — overwrite exactly what was inspected
}

// maxRenameAttempts bounds the "name (n).ext" search of the rename policy.
const maxRenameAttempts = 999

// resolveRemoteConflict decides how to write an item of srcSize bytes, last
// modified at srcModified, to bucket/key under policy.
//
//	skip            exists → skip; else create-only
//	overwrite       write unconditionally (the user asked to replace)
//	rename          exists → first free "name (n).ext"; create-only on that key
//	newer           exists and source is newer → overwrite If-Match; else skip
//	different_size  exists and sizes differ    → overwrite If-Match; else skip
//
// A 412 at commit means the destination changed after the decision; callers
// report it as a failed item (for rename they may resolve once more).
func resolveRemoteConflict(ctx context.Context, c *storage.Client, bucket, key string, policy ConflictPolicy, srcSize int64, srcModified time.Time) (conflictDecision, error) {
	const op = "ResolveConflict"
	if policy == ConflictOverwrite {
		return conflictDecision{Key: key}, nil
	}
	dst, err := headTarget(ctx, c, bucket, key)
	if err != nil {
		return conflictDecision{}, err
	}
	if !dst.exists {
		return conflictDecision{Key: key, CreateOnly: true}, nil
	}
	switch policy {
	case ConflictSkip:
		return conflictDecision{Key: key, Skip: true, Reason: "destination exists"}, nil
	case ConflictNewer:
		if srcModified.After(dst.modified) {
			return conflictDecision{Key: key, IfMatch: dst.etag}, nil
		}
		return conflictDecision{Key: key, Skip: true, Reason: "destination is not older"}, nil
	case ConflictDifferentSize:
		if srcSize != dst.size {
			return conflictDecision{Key: key, IfMatch: dst.etag}, nil
		}
		return conflictDecision{Key: key, Skip: true, Reason: "same size"}, nil
	case ConflictRename:
		for n := 1; n <= maxRenameAttempts; n++ {
			cand := numberedKey(key, n)
			t, err := headTarget(ctx, c, bucket, cand)
			if err != nil {
				return conflictDecision{}, err
			}
			if !t.exists {
				return conflictDecision{Key: cand, CreateOnly: true}, nil
			}
		}
		return conflictDecision{}, opErr(op, ErrConflict, "no free name for %q after %d attempts", key, maxRenameAttempts)
	}
	return conflictDecision{}, invalid(op, "unknown conflict policy %q", policy)
}

type remoteTarget struct {
	exists   bool
	size     int64
	modified time.Time
	etag     string
}

// headTarget HEADs bucket/key; a missing key is not an error.
func headTarget(ctx context.Context, c *storage.Client, bucket, key string) (remoteTarget, error) {
	out, err := c.S3().HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		cerr := classify("HeadObject", err)
		if KindOf(cerr) == ErrNotFound {
			return remoteTarget{}, nil
		}
		return remoteTarget{}, cerr
	}
	return remoteTarget{
		exists:   true,
		size:     aws.ToInt64(out.ContentLength),
		modified: aws.ToTime(out.LastModified),
		etag:     aws.ToString(out.ETag),
	}, nil
}

// numberedName inserts " (n)" before the last extension, as file managers
// do: "report.pdf" → "report (1).pdf", "a.tar.gz" → "a.tar (1).gz",
// "README" → "README (1)", ".env" → ".env (1)".
func numberedName(name string, n int) string {
	ext := path.Ext(name)
	if ext == name { // dot-file without another dot
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// numberedKey applies numberedName to the last segment of an object key.
func numberedKey(key string, n int) string {
	dir, name := path.Split(key)
	return dir + numberedName(name, n)
}

// writeConditional runs write with the decision's conditional headers (empty
// string = omit). If the account's provider rejects such headers, it records
// that (caps.go) and runs write once more without them — the HEAD in
// resolveRemoteConflict remains the only guard in that case.
func (s *Service) writeConditional(accountID string, d conflictDecision, write func(ifNoneMatch, ifMatch string) error) error {
	ifNoneMatch, ifMatch := "", d.IfMatch
	if d.CreateOnly {
		ifNoneMatch = "*"
	}
	if (ifNoneMatch == "" && ifMatch == "") || s.condWritesUnsupported(accountID) {
		return write("", "")
	}
	err := write(ifNoneMatch, ifMatch)
	if condHeaderRejected(err) {
		s.markCondWritesUnsupported(accountID)
		return write("", "")
	}
	return err
}
