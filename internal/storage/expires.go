package storage

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithytime "github.com/aws/smithy-go/time"
)

// HeadExpires returns the object's Expires header as a time, or nil when the
// header is absent or not an HTTP date. It replaces the deprecated
// HeadObjectOutput.Expires field and parses ExpiresString exactly as the SDK
// fills that field, so copies keep the same value.
func HeadExpires(h *s3.HeadObjectOutput) *time.Time {
	if h == nil || h.ExpiresString == nil {
		return nil
	}
	t, err := smithytime.ParseHTTPDate(*h.ExpiresString)
	if err != nil {
		return nil
	}
	return &t
}
