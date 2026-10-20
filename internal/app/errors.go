package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/aws/smithy-go"
)

// ErrKind is the coarse class of an OpError; the UI picks its message and
// recovery hint from it. Note that AccessDenied is never reported as
// Unsupported: a 403 means "not allowed", not "provider lacks the feature".
type ErrKind int

// Error kinds (APP_API.md section 0).
const (
	ErrOther ErrKind = iota
	ErrNotFound
	ErrAccessDenied
	ErrUnsupported
	ErrConflict
	ErrNetwork
	ErrCanceled
	ErrInvalid
	// ErrLocked: the secret store is the encrypted vault and it is locked. The
	// UI asks for the master password and retries. Appended last so that the
	// numeric values of the older kinds stay stable.
	ErrLocked
)

// Sentinels reachable with errors.Is through any *OpError that carries them.
var (
	// ErrVaultLocked marks every error of kind ErrLocked.
	ErrVaultLocked = errors.New("the encrypted vault is locked")
	// ErrKeyringUnavailable marks a failure of the operating system keychain
	// that means "there is none here" (no Secret Service / D-Bus session, as on
	// a bare Linux or WSL install), as opposed to a transient or per-entry
	// failure. The UI answers it by offering the encrypted vault.
	ErrKeyringUnavailable = errors.New("no system keychain available")
)

func (k ErrKind) String() string {
	switch k {
	case ErrLocked:
		return "Locked"
	case ErrNotFound:
		return "NotFound"
	case ErrAccessDenied:
		return "AccessDenied"
	case ErrUnsupported:
		return "Unsupported"
	case ErrConflict:
		return "Conflict"
	case ErrNetwork:
		return "Network"
	case ErrCanceled:
		return "Canceled"
	case ErrInvalid:
		return "Invalid"
	}
	return "Other"
}

// OpError wraps every error the Service returns. Op names the operation
// ("List", "SaveAccount", ...); Code is the S3/smithy error code when there is
// one; RequestID is the service request ID for support cases.
type OpError struct {
	Op        string
	Code      string
	Message   string
	RequestID string
	Kind      ErrKind
	err       error
}

func (e *OpError) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	var extra []string
	if e.Code != "" && !strings.Contains(e.Message, e.Code) {
		extra = append(extra, e.Code)
	}
	if e.RequestID != "" {
		extra = append(extra, "request id "+e.RequestID)
	}
	if len(extra) > 0 {
		b.WriteString(" (" + strings.Join(extra, ", ") + ")")
	}
	return b.String()
}

// Unwrap exposes the underlying error so errors.Is(err, context.Canceled) and
// errors.As against SDK types keep working.
func (e *OpError) Unwrap() error { return e.err }

// KindOf returns the ErrKind of err: that of the first OpError in the chain, or
// ErrOther for foreign errors and ErrOther for nil.
func KindOf(err error) ErrKind {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Kind
	}
	return ErrOther
}

// IsKind reports whether err carries the given kind.
func IsKind(err error, k ErrKind) bool {
	var oe *OpError
	return errors.As(err, &oe) && oe.Kind == k
}

// opErr builds an OpError without an underlying error (validation failures etc.).
func opErr(op string, kind ErrKind, format string, args ...any) *OpError {
	return &OpError{Op: op, Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// invalid is shorthand for a validation error.
func invalid(op, format string, args ...any) *OpError {
	return opErr(op, ErrInvalid, format, args...)
}

// classify wraps err as an *OpError for operation op. nil stays nil; an error
// that already is an OpError is returned as is (its Op is filled in if empty).
func classify(op string, err error) (res error) {
	if err == nil {
		return nil
	}
	var status int
	var existing *OpError
	if errors.As(err, &existing) {
		if existing.Op == "" {
			cp := *existing
			cp.Op = op
			return &cp
		}
		return existing
	}
	oe := &OpError{Op: op, Message: err.Error(), err: err, Kind: ErrOther}
	defer func() { lgOpError(oe, status) }()

	if errors.Is(err, ErrVaultLocked) {
		oe.Kind, oe.Message = ErrLocked, lockedMessage
		return oe
	}
	if errors.Is(err, ErrKeyringUnavailable) {
		oe.Kind, oe.Message = ErrUnsupported, noKeychainMessage
		if d := keyringDetail(err); d != "" {
			oe.Message += " (" + d + ")"
		}
		return oe
	}

	if errors.Is(err, context.Canceled) {
		oe.Kind = ErrCanceled
		return oe
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// A timeout is a retryable failure, not something the user canceled.
		oe.Kind, oe.Message = ErrNetwork, "timed out"
		return oe
	}

	var api smithy.APIError
	if errors.As(err, &api) {
		oe.Code = api.ErrorCode()
		if m := api.ErrorMessage(); m != "" {
			oe.Message = m
		} else if oe.Code != "" {
			oe.Message = oe.Code
		}
	}
	var rid interface{ ServiceRequestID() string }
	if errors.As(err, &rid) {
		oe.RequestID = rid.ServiceRequestID()
	}
	var hs interface{ HTTPStatusCode() int }
	if errors.As(err, &hs) {
		status = hs.HTTPStatusCode()
	}

	if k, ok := kindFromCode(oe.Code); ok {
		oe.Kind = k
		return oe
	}
	if k, ok := kindFromStatus(status); ok {
		oe.Kind = k
		return oe
	}
	if status == 0 && isNetworkError(err) {
		oe.Kind = ErrNetwork
	}
	return oe
}

// kindFromCode maps S3 error codes. Codes are checked before the HTTP status
// because several arrive with a generic status (ExpiredToken is a 400).
func kindFromCode(code string) (ErrKind, bool) {
	switch code {
	case "NoSuchKey", "NoSuchBucket", "NoSuchVersion", "NotFound", "NoSuchUpload", "NoSuchLifecycleConfiguration",
		"NoSuchCORSConfiguration", "NoSuchWebsiteConfiguration", "NoSuchBucketPolicy", "NoSuchTagSet",
		"NoSuchPublicAccessBlockConfiguration", "NoSuchDistribution", "NoSuchInvalidation",
		"ServerSideEncryptionConfigurationNotFoundError", "ReplicationConfigurationNotFoundError",
		"ObjectLockConfigurationNotFoundError", "OwnershipControlsNotFoundError":
		return ErrNotFound, true
	case "AccessDenied", "AllAccessDisabled", "AccountProblem", "InvalidAccessKeyId", "SignatureDoesNotMatch",
		"ExpiredToken", "InvalidToken", "TokenRefreshRequired", "Forbidden", "InvalidSecurity", "RequestTimeTooSkewed",
		"AuthorizationHeaderMalformed", "Unauthorized", "UnrecognizedClientException":
		return ErrAccessDenied, true
	case "RequestTimeout", "RequestTimeoutException":
		return ErrNetwork, true
	case "NotImplemented", "XNotImplemented", "UnsupportedOperation", "UnsupportedAPI":
		return ErrUnsupported, true
	case "PreconditionFailed", "ConditionalRequestConflict", "ConflictingConditionalOperation", "BucketAlreadyExists",
		"BucketAlreadyOwnedByYou", "BucketNotEmpty", "OperationAborted", "InvalidBucketState", "ObjectLockedByBucketPolicy",
		"RestoreAlreadyInProgress":
		return ErrConflict, true
	case "InvalidArgument", "InvalidRequest", "MalformedXML", "MalformedPolicy", "InvalidBucketName", "InvalidRange",
		"EntityTooLarge", "EntityTooSmall", "InvalidPart", "InvalidPartOrder", "InvalidDigest", "BadDigest",
		"InvalidLocationConstraint", "InvalidStorageClass", "InvalidEncryptionAlgorithmError", "KeyTooLongError",
		"MetadataTooLarge", "MissingContentLength", "MissingRequestBodyError", "InvalidTag", "InvalidURI":
		return ErrInvalid, true
	}
	return ErrOther, false
}

func kindFromStatus(status int) (ErrKind, bool) {
	switch {
	case status == http.StatusNotFound:
		return ErrNotFound, true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAccessDenied, true
	case status == http.StatusNotImplemented:
		return ErrUnsupported, true
	case status == http.StatusConflict || status == http.StatusPreconditionFailed:
		return ErrConflict, true
	case status == http.StatusBadRequest, status == http.StatusLengthRequired, status == http.StatusRequestEntityTooLarge,
		status == http.StatusRequestURITooLong, status == http.StatusRequestedRangeNotSatisfiable, status == http.StatusUnprocessableEntity:
		return ErrInvalid, true
	}
	return ErrOther, false
}

func isNetworkError(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"connection reset", "connection refused", "forcibly closed", "broken pipe", "no such host", "i/o timeout", "tls handshake", "unexpected eof"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
