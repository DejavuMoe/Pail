package storage

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// HeadExpires must give the value the SDK put in the deprecated Expires
// field for every header it receives, so replacing the field changes nothing.
func TestHeadExpiresMatchesTheSDKField(t *testing.T) {
	want := time.Date(2037, 10, 21, 7, 28, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		send   bool
		want   *time.Time
	}{
		{header: "Wed, 21 Oct 2037 07:28:00 GMT", send: true, want: &want},
		{header: "  Wed, 21 Oct 2037 07:28:00 GMT  ", send: true, want: &want},
		{header: "Wed, 1 Oct 2037 07:28:00 GMT", send: true, want: aws.Time(time.Date(2037, 10, 1, 7, 28, 0, 0, time.UTC))},
		{header: "Wednesday, 21-Oct-37 07:28:00 GMT", send: true, want: &want},
		{header: "Wed Oct 21 07:28:00 2037", send: true, want: &want},
		{header: "tomorrow", send: true},
		{header: "0", send: true},
		{header: "", send: true},
		{send: false},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if tc.send {
				w.Header()["Expires"] = []string{tc.header}
			}
			w.Header().Set("Content-Length", "0")
		})
		h, err := c.S3().HeadObject(context.Background(), &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
		if err != nil {
			t.Fatalf("%q: %v", tc.header, err)
		}
		got := HeadExpires(h)
		//lint:ignore SA1019 the deprecated field is the reference this test compares against.
		sdk := h.Expires
		if !sameTime(got, tc.want) || !sameTime(got, sdk) {
			t.Errorf("%q (sent %v): HeadExpires = %v, SDK field = %v, want %v", tc.header, tc.send, got, sdk, tc.want)
		}
	}
	if HeadExpires(nil) != nil || HeadExpires(&s3.HeadObjectOutput{}) != nil {
		t.Error("missing output or header must give nil")
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
