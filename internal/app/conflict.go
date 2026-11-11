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
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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
	return decideConflict(ctx, c, bucket, key, policy, srcSize, srcModified, dst)
}

// decideConflict is resolveRemoteConflict for a caller that has HEADed the
// destination key already (dst). The rename policy still HEADs its candidates.
func decideConflict(ctx context.Context, c *storage.Client, bucket, key string, policy ConflictPolicy, srcSize int64, srcModified time.Time, dst remoteTarget) (conflictDecision, error) {
	const op = "ResolveConflict"
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
	head     *s3.HeadObjectOutput
	exists   bool
	size     int64
	modified time.Time
	etag     string
	keyed    bool // SSE-KMS or SSE-C: the ETag is not the MD5 of the content
}

// etagMD5 reports whether etag is the plain MD5 of an object's content: it has
// no "-" (a multipart upload or copy: hash of hashes and the part count) and
// the object is not encrypted with SSE-KMS or SSE-C (ETag unrelated to content).
func etagMD5(etag string, keyed bool) bool {
	return etag != "" && !keyed && !strings.Contains(etag, "-")
}

// sameObjectETag reports whether two objects of equal size are known to have
// equal content from their ETags: both ETags are content MD5s and equal. When
// either is not comparable the answer is false, not "different".
func sameObjectETag(a string, aKeyed bool, b string, bKeyed bool) bool {
	return etagMD5(a, aKeyed) && etagMD5(b, bKeyed) && strings.Trim(a, `"`) == strings.Trim(b, `"`)
}

// headKeyed reports whether a HEAD result is SSE-KMS or SSE-C encrypted.
func headKeyed(h *s3.HeadObjectOutput) bool {
	return aws.ToString(h.SSECustomerAlgorithm) != "" ||
		h.ServerSideEncryption == s3types.ServerSideEncryptionAwsKms ||
		h.ServerSideEncryption == s3types.ServerSideEncryptionAwsKmsDsse
}

// headTarget HEADs bucket/key; a missing key is not an error.
func headTarget(ctx context.Context, c *storage.Client, bucket, key string) (remoteTarget, error) {
	out, err := headObj(ctx, c, "HeadObject", bucket, key, "")
	if err != nil {
		if KindOf(err) == ErrNotFound {
			return remoteTarget{}, nil
		}
		return remoteTarget{}, err
	}
	return remoteTarget{
		head:     out,
		exists:   true,
		size:     aws.ToInt64(out.ContentLength),
		modified: aws.ToTime(out.LastModified),
		etag:     aws.ToString(out.ETag),
		keyed:    headKeyed(out),
	}, nil
}

// numberedName inserts " (n)" before the last extension, as file managers
// do: "report.pdf" → "report (1).pdf", "a.tar.gz" → "a.tar (1).gz",
// "README" → "README (1)", ".env" → ".env (1)".
func numberedName(name string, n int) string {
	return insertBeforeExt(name, fmt.Sprintf(" (%d)", n))
}

// insertBeforeExt puts suffix before the last extension of name. A dot-file
// without another dot (".env") has no extension.
func insertBeforeExt(name, suffix string) string {
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	return strings.TrimSuffix(name, ext) + suffix + ext
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
