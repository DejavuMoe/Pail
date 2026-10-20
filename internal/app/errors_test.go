package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func apiErr(status int, code, msg, reqID string) error {
	return &awshttp.ResponseError{
		ResponseError: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
			Err:      &smithy.GenericAPIError{Code: code, Message: msg},
		},
		RequestID: reqID,
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind ErrKind
		code string
	}{
		{"NoSuchKey", apiErr(404, "NoSuchKey", "gone", "r1"), ErrNotFound, "NoSuchKey"},
		{"NoSuchBucket", apiErr(404, "NoSuchBucket", "gone", "r1"), ErrNotFound, "NoSuchBucket"},
		{"NoSuchVersion", apiErr(404, "NoSuchVersion", "", "r1"), ErrNotFound, "NoSuchVersion"},
		{"HEAD NotFound", apiErr(404, "NotFound", "", "r1"), ErrNotFound, "NotFound"},
		{"404 unknown code", apiErr(404, "Weird", "", ""), ErrNotFound, "Weird"},
		{"NoSuchLifecycle", apiErr(404, "NoSuchLifecycleConfiguration", "", ""), ErrNotFound, "NoSuchLifecycleConfiguration"},
		{"AccessDenied", apiErr(403, "AccessDenied", "no", "r2"), ErrAccessDenied, "AccessDenied"},
		{"403 is not unsupported", apiErr(403, "SomethingElse", "no", ""), ErrAccessDenied, "SomethingElse"},
		{"SignatureDoesNotMatch", apiErr(403, "SignatureDoesNotMatch", "", ""), ErrAccessDenied, "SignatureDoesNotMatch"},
		{"ExpiredToken is a 400 but access", apiErr(400, "ExpiredToken", "", ""), ErrAccessDenied, "ExpiredToken"},
		{"401", apiErr(401, "", "", ""), ErrAccessDenied, ""},
		{"NotImplemented", apiErr(501, "NotImplemented", "", ""), ErrUnsupported, "NotImplemented"},
		{"501 no code", apiErr(501, "", "", ""), ErrUnsupported, ""},
		{"409 BucketNotEmpty", apiErr(409, "BucketNotEmpty", "", ""), ErrConflict, "BucketNotEmpty"},
		{"409 generic", apiErr(409, "Odd", "", ""), ErrConflict, "Odd"},
		{"412", apiErr(412, "PreconditionFailed", "", ""), ErrConflict, "PreconditionFailed"},
		{"412 no code", apiErr(412, "", "", ""), ErrConflict, ""},
		{"400 InvalidArgument", apiErr(400, "InvalidArgument", "", ""), ErrInvalid, "InvalidArgument"},
		{"400 generic", apiErr(400, "BadRequest", "", ""), ErrInvalid, "BadRequest"},
		{"416", apiErr(416, "InvalidRange", "", ""), ErrInvalid, "InvalidRange"},
		{"RequestTimeout is network", apiErr(400, "RequestTimeout", "", ""), ErrNetwork, "RequestTimeout"},
		{"503 SlowDown", apiErr(503, "SlowDown", "", ""), ErrOther, "SlowDown"},
		{"500", apiErr(500, "InternalError", "", ""), ErrOther, "InternalError"},
		{"canceled", context.Canceled, ErrCanceled, ""},
		// A timeout is a failure the user may retry, not a cancellation.
		{"deadline", context.DeadlineExceeded, ErrNetwork, ""},
		{"wrapped deadline", fmt.Errorf("operation error: %w", context.DeadlineExceeded), ErrNetwork, ""},
		{"deadline inside net error", &url.Error{Op: "Get", URL: "x", Err: context.DeadlineExceeded}, ErrNetwork, ""},
		{"deadline and canceled", fmt.Errorf("%w / %w", context.DeadlineExceeded, context.Canceled), ErrCanceled, ""},
		{"wrapped canceled", fmt.Errorf("operation error: %w", context.Canceled), ErrCanceled, ""},
		{"canceled inside net error", &url.Error{Op: "Get", URL: "x", Err: context.Canceled}, ErrCanceled, ""},
		{"net timeout", &net.OpError{Op: "read", Err: timeoutErr{}}, ErrNetwork, ""},
		{"conn reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, ErrNetwork, ""},
		{"conn reset text", errors.New("read tcp: wsarecv: An existing connection was forcibly closed by the remote host."), ErrNetwork, ""},
		{"conn refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, ErrNetwork, ""},
		{"unexpected EOF", fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), ErrNetwork, ""},
		{"dns", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, ErrNetwork, ""},
		{"plain error", errors.New("boom"), ErrOther, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify("Op", c.err)
			var oe *OpError
			if !errors.As(got, &oe) {
				t.Fatalf("not an OpError: %T", got)
			}
			if oe.Kind != c.kind || oe.Code != c.code || oe.Op != "Op" {
				t.Fatalf("got kind=%v code=%q op=%q, want kind=%v code=%q", oe.Kind, oe.Code, oe.Op, c.kind, c.code)
			}
			if !errors.Is(got, c.err) {
				t.Fatal("Unwrap chain lost the original error")
			}
		})
	}
}

func TestClassifyDetails(t *testing.T) {
	if classify("X", nil) != nil {
		t.Fatal("nil must stay nil")
	}
	err := classify("Head", apiErr(404, "NoSuchKey", "The specified key does not exist.", "REQ123"))
	var oe *OpError
	if !errors.As(err, &oe) || oe.RequestID != "REQ123" || oe.Message != "The specified key does not exist." {
		t.Fatalf("details lost: %+v", oe)
	}
	s := err.Error()
	for _, want := range []string{"Head:", "does not exist", "NoSuchKey", "REQ123"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() = %q, missing %q", s, want)
		}
	}
	// Re-classifying keeps the first classification and Op.
	again := classify("Other", err)
	if again != err {
		t.Fatal("an OpError must not be re-wrapped")
	}
	// Wrapped OpError is found by KindOf / IsKind.
	wrapped := fmt.Errorf("context: %w", err)
	if KindOf(wrapped) != ErrNotFound || !IsKind(wrapped, ErrNotFound) || IsKind(wrapped, ErrConflict) {
		t.Fatal("KindOf/IsKind on wrapped error")
	}
	if KindOf(errors.New("x")) != ErrOther || KindOf(nil) != ErrOther {
		t.Fatal("KindOf foreign error")
	}
	// An empty Op is filled in without mutating the original.
	empty := &OpError{Message: "m", Kind: ErrInvalid}
	filled := classify("Fill", empty).(*OpError)
	if filled.Op != "Fill" || empty.Op != "" {
		t.Fatal("empty Op handling")
	}
	for k, want := range map[ErrKind]string{ErrNotFound: "NotFound", ErrAccessDenied: "AccessDenied", ErrUnsupported: "Unsupported",
		ErrConflict: "Conflict", ErrNetwork: "Network", ErrCanceled: "Canceled", ErrInvalid: "Invalid", ErrOther: "Other"} {
		if k.String() != want {
			t.Errorf("%d.String() = %q", k, k.String())
		}
	}
}
