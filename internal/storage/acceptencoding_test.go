package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Accept-Encoding is sent but kept out of SignedHeaders, so a proxy that
// rewrites it cannot break the signature.
func TestAcceptEncodingSentButNotSigned(t *testing.T) {
	var mu sync.Mutex
	var auth, ae []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		ae = append(ae, r.Header.Get("Accept-Encoding"))
		first := len(auth) == 1
		mu.Unlock()
		if first { // force a retry: the header must survive it
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<Tagging><TagSet></TagSet></Tagging>`))
	}))
	defer srv.Close()
	c, err := New(context.Background(), Config{Endpoint: srv.URL, Region: "us-east-1", AccessKeyID: "AK", SecretAccessKey: "SK", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.S3().GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{Bucket: aws.String("b"), Key: aws.String("k")}); err != nil {
		t.Fatal(err)
	}
	if len(auth) != 2 {
		t.Fatalf("requests = %d, want 2 (one retry)", len(auth))
	}
	for i := range auth {
		if !strings.Contains(auth[i], "SignedHeaders=") || strings.Contains(strings.ToLower(auth[i]), "accept-encoding") {
			t.Errorf("attempt %d: Accept-Encoding is signed: %s", i, auth[i])
		}
		if ae[i] != "identity" {
			t.Errorf("attempt %d: Accept-Encoding = %q, want identity", i, ae[i])
		}
	}
}
