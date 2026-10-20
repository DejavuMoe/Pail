package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestAnonymousSendsUnsignedRequests(t *testing.T) {
	var auth, date string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, date = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Date")
		fmt.Fprint(w, `<ListBucketResult><Name>pub</Name><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	defer srv.Close()
	c, err := New(context.Background(), Config{Endpoint: srv.URL, Region: "us-east-1", Anonymous: true, UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.S3().ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{Bucket: aws.String("pub")}); err != nil {
		t.Fatal(err)
	}
	if auth != "" || date != "" {
		t.Fatalf("anonymous request was signed: Authorization=%q X-Amz-Date=%q", auth, date)
	}
}

func TestAnonymousExcludesCredentials(t *testing.T) {
	for name, cfg := range map[string]Config{
		"keys":    {Anonymous: true, AccessKeyID: "a", SecretAccessKey: "b"},
		"default": {Anonymous: true, UseDefaultCredentials: true},
		"role":    {Anonymous: true, RoleARN: "arn:aws:iam::1:role/x"},
	} {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
