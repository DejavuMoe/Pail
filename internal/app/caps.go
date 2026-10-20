package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/DejavuMoe/Pail/internal/storage"
	"github.com/aws/smithy-go"
)

// Conditional writes (If-None-Match: * / If-Match on PutObject, CopyObject and
// CompleteMultipartUpload) protect create-only and overwrite-if-unchanged
// semantics. Providers differ: AWS and current MinIO honour them, some
// S3-compatible services reject the headers and a few silently ignore them.
//
// Pail never writes probe objects into a user's bucket to find out (on a
// versioned bucket a probe leaves versions behind). Instead every write that
// needs a precondition:
//
//  1. checks with HEAD first when the conflict policy needs a decision
//     (skip / rename / newer / different_size), then
//  2. sends the conditional header as an additional guard, unless
//     condWritesUnsupported(account) is already known, and
//  3. if the provider rejects the header (condHeaderRejected), calls
//     markCondWritesUnsupported and repeats the write once without it.
//
// A provider that ignores the header degrades to the HEAD check, with the
// race window documented in the user guide.

// condWritesUnsupported reports whether the account's provider is known to
// reject conditional write headers.
func (s *Service) condWritesUnsupported(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.condUnsupported[accountID]
}

// markCondWritesUnsupported records that the account's provider rejected a
// conditional write header. It is kept for the life of the Service; saving or
// deleting the account clears it (invalidateLocked), since the endpoint may
// have changed.
func (s *Service) markCondWritesUnsupported(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.condUnsupported[accountID] = true
}

// condHeaderRejected reports whether err means the provider does not support
// a conditional write header, as opposed to the precondition failing (412,
// which is a real conflict) or any other error.
//
// Only an error from an operation that actually carried the header counts
// (PutObject, CompleteMultipartUpload, CopyObject); a NotImplemented from
// CreateMultipartUpload, UploadPartCopy, tagging calls etc. says nothing about
// conditional writes and must not mark the account. The operation is taken from
// the smithy.OperationError in the error chain; without one the error is not
// attributed to a conditional write.
func condHeaderRejected(err error) bool {
	if err == nil {
		return false
	}
	var oe *smithy.OperationError
	if !errors.As(err, &oe) {
		return false
	}
	switch oe.OperationName {
	case "PutObject", "CompleteMultipartUpload", "CopyObject":
	default:
		return false
	}
	code := storage.ErrorCode(err)
	if k, ok := kindFromCode(code); ok && k == ErrUnsupported {
		return true
	}
	var hs interface{ HTTPStatusCode() int }
	if errors.As(err, &hs) && hs.HTTPStatusCode() == http.StatusNotImplemented {
		return true
	}
	switch code {
	case "InvalidArgument", "InvalidRequest", "BadRequest":
		var ae smithy.APIError
		if errors.As(err, &ae) {
			msg := strings.ToLower(ae.ErrorMessage())
			return strings.Contains(msg, "if-none-match") || strings.Contains(msg, "if-match") || strings.Contains(msg, "conditional")
		}
	}
	return false
}
