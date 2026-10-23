package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func testConfig(server *httptest.Server) Config {
	return Config{Endpoint: server.URL, Region: "us-east-1", AccessKeyID: "test-access", SecretAccessKey: "test-secret", UsePathStyle: true, HTTPClient: server.Client()}
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(context.Background(), testConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSignedSpecialKeysAndPagination(t *testing.T) {
	key := "目录/a b+%?#.txt"
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-access/") || r.Header.Get("X-Amz-Date") == "" {
			t.Error("request is not signed by AWS SDK")
		}
		if r.Method == "PUT" {
			if r.URL.Path != "/bucket/"+key {
				t.Errorf("key changed: %q", r.URL.Path)
			}
			b, _ := io.ReadAll(r.Body)
			if string(b) != "hello" {
				t.Errorf("body %q", b)
			}
			w.Header().Set("ETag", `"etag"`)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>a+/=</NextContinuationToken><Contents><Key>first</Key></Contents></ListBucketResult>`)
		} else {
			if r.URL.Query().Get("continuation-token") != "a+/=" {
				t.Error("token changed")
			}
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>second</Key></Contents></ListBucketResult>`)
		}
	})
	if _, err := c.UploadBytes(context.Background(), "bucket", key, "text/plain", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	pager := s3.NewListObjectsV2Paginator(c.S3(), &s3.ListObjectsV2Input{Bucket: aws.String("bucket"), Prefix: aws.String("目录/")})
	var keys []string
	for pager.HasMorePages() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	if strings.Join(keys, ",") != "first,second" || requests != 3 {
		t.Fatalf("pagination not preserved: %v requests=%d", keys, requests)
	}
}

func TestTypedErrorAndPresign(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(404)
		fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
	})
	_, err := c.S3().GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String("bucket"), Key: aws.String("missing")})
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "NoSuchKey" || ErrorCode(err) != "NoSuchKey" {
		t.Fatalf("typed error lost: %v", err)
	}
	key := "目录/a +%?#"
	for _, method := range []string{"GET", "HEAD", "PUT"} {
		version := "v+/="
		if method == "PUT" {
			version = ""
		}
		p, err := c.Presign(context.Background(), method, "bucket", key, version, time.Minute)
		if err != nil {
			t.Fatal(method, err)
		}
		u, err := url.Parse(p.URL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Path != "/bucket/"+key || u.Query().Get("versionId") != version || u.Query().Get("X-Amz-Signature") == "" || u.Query().Get("X-Amz-Expires") != "60" || p.Method != method {
			t.Fatalf("invalid presigned %s request: %s", method, p.URL)
		}
	}
	for _, bad := range []time.Duration{0, -time.Second, 8 * 24 * time.Hour} {
		if _, err = c.Presign(context.Background(), "GET", "bucket", "key", "", bad); err == nil {
			t.Fatalf("invalid expiry %v accepted", bad)
		}
	}
	if _, err = c.Presign(context.Background(), "PUT", "bucket", "key", "v", time.Minute); err == nil {
		t.Fatal("PUT with version accepted")
	}
	if _, err = c.Presign(context.Background(), "DELETE", "bucket", "key", "", time.Minute); err == nil {
		t.Fatal("unsupported method accepted")
	}
}

func TestConfigValidation(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no credentials":    {},
		"half credentials":  {AccessKeyID: "a"},
		"bad endpoint":      {Endpoint: "ftp://x", AccessKeyID: "a", SecretAccessKey: "b"},
		"endpoint userinfo": {Endpoint: "http://u:p@x", AccessKeyID: "a", SecretAccessKey: "b"},
		"bad checksum mode": {AccessKeyID: "a", SecretAccessKey: "b", ChecksumMode: "always"},
	} {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestHTTPClientTimeouts(t *testing.T) {
	hc := NewHTTPClient(false)
	tr := hc.Transport.(*http.Transport)
	if hc.Timeout != 0 {
		t.Fatal("overall request timeout would cut off large transfers")
	}
	if tr.TLSHandshakeTimeout != 10*time.Second || tr.ResponseHeaderTimeout != 60*time.Second || tr.DisableKeepAlives || tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("unexpected transport: %+v", tr)
	}
	if !NewHTTPClient(true).Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify not applied")
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`)
	}))
	defer server.Close()
	cfg := Config{Endpoint: server.URL, AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListBuckets(context.Background()); err == nil {
		t.Fatal("untrusted certificate accepted by default")
	}
	cfg.InsecureSkipVerify = true
	if c, err = New(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListBuckets(context.Background()); err != nil {
		t.Fatalf("InsecureSkipVerify not honoured: %v", err)
	}
}

func TestListBucketsRegionAndPagination(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>a</Name><CreationDate>2024-01-02T03:04:05Z</CreationDate><BucketRegion>eu-west-1</BucketRegion></Bucket></Buckets><ContinuationToken>next</ContinuationToken></ListAllMyBucketsResult>`)
			return
		}
		fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>b</Name><CreationDate>2024-02-02T00:00:00Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>`)
	})
	got, err := c.ListBuckets(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Name != "a" || got[0].Region != "eu-west-1" || got[0].CreationDate.Year() != 2024 || got[1].Name != "b" || got[1].Region != "" {
		t.Fatalf("unexpected buckets: %+v", got)
	}
}

func TestBucketRegion(t *testing.T) {
	for in, want := range map[string]string{"": "us-east-1", "EU": "eu-west-1", "ap-south-1": "ap-south-1"} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.Query().Has("location") {
				t.Errorf("unexpected %s", r.URL)
			}
			fmt.Fprintf(w, `<LocationConstraint>%s</LocationConstraint>`, in)
		})
		got, err := c.BucketRegion(context.Background(), "bucket")
		if err != nil || got != want {
			t.Errorf("%q: got %q %v want %q", in, got, err, want)
		}
	}
}

func TestCopySourceEscaping(t *testing.T) {
	// "+" must be encoded: AWS and VersityGW decode a literal "+" as a space.
	if got := CopySource("b", "目录/a b+%?#", "v+1"); got != "b/%E7%9B%AE%E5%BD%95/a%20b%2B%25%3F%23?versionId=v%2B1" {
		t.Fatalf("got %s", got)
	}
	if got := CopySource("b", "x/!$&'()*,;=:@~-_.y", ""); got != "b/x/%21%24%26%27%28%29%2A%2C%3B%3D%3A%40~-_.y" {
		t.Fatalf("got %s", got)
	}
}

// recorder captures every request's headers so checksum behaviour can be asserted.
type recorder struct {
	mu      sync.Mutex
	headers []http.Header
}

func (r *recorder) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.headers = append(r.headers, req.Header.Clone())
		r.mu.Unlock()
		q := req.URL.Query()
		_, _ = io.Copy(io.Discard, req.Body)
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case req.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>u1</UploadId></InitiateMultipartUploadResult>`)
		case req.Method == "POST" && q.Get("uploadId") != "":
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"e"</ETag></CompleteMultipartUploadResult>`)
		default:
			w.Header().Set("ETag", `"part"`)
		}
	}
}

func checksumHeaders(h http.Header) []string {
	var found []string
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-checksum-") || lk == "x-amz-trailer" || lk == "x-amz-sdk-checksum-algorithm" || strings.Contains(strings.ToLower(strings.Join(v, ",")), "aws-chunked") && lk == "content-encoding" {
			found = append(found, k)
		}
	}
	return found
}

func runUploads(t *testing.T, mode string) []http.Header {
	rec := &recorder{}
	server := httptest.NewServer(rec.handler(t))
	defer server.Close()
	cfg := testConfig(server)
	cfg.ChecksumMode = mode
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("small"), Body: bytes.NewReader([]byte("hello"))}, TransferOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("big"), Body: bytes.NewReader(make([]byte, 11*1024*1024))}, TransferOptions{PartSize: 5 * 1024 * 1024, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	return rec.headers
}

func TestCustomEndpointSendsNoChecksums(t *testing.T) {
	for _, mode := range []string{"", ChecksumAuto, ChecksumWhenRequired} {
		hs := runUploads(t, mode)
		if len(hs) < 6 {
			t.Fatalf("mode %q: expected put + create + 3 parts + complete, got %d requests", mode, len(hs))
		}
		for _, h := range hs {
			if bad := checksumHeaders(h); len(bad) > 0 {
				t.Errorf("mode %q sent checksum headers %v", mode, bad)
			}
		}
	}
	// Control: the test must be able to see checksums when they are requested.
	seen := false
	for _, h := range runUploads(t, ChecksumWhenSupported) {
		if len(checksumHeaders(h)) > 0 {
			seen = true
		}
	}
	if !seen {
		t.Fatal("when_supported control sent no checksum; the assertion above proves nothing")
	}
}

func TestAssumeRole(t *testing.T) {
	var form url.Values
	var s3Auth, s3Token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		if r.Method == "POST" {
			_ = r.ParseForm()
			form = r.PostForm
			fmt.Fprint(w, `<AssumeRoleResponse><AssumeRoleResult><Credentials><AccessKeyId>ASIAROLE</AccessKeyId><SecretAccessKey>role-secret</SecretAccessKey><SessionToken>role-token</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`)
			return
		}
		s3Auth, s3Token = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Security-Token")
		fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`)
	}))
	defer server.Close()
	cfg := testConfig(server)
	cfg.RoleARN, cfg.ExternalID, cfg.RoleSessionName = "arn:aws:iam::123456789012:role/test", "ext-id", "pail-test"
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListBuckets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if form.Get("Action") != "AssumeRole" || form.Get("RoleArn") != cfg.RoleARN || form.Get("ExternalId") != "ext-id" || form.Get("RoleSessionName") != "pail-test" {
		t.Fatalf("unexpected STS request: %v", form)
	}
	if !strings.Contains(s3Auth, "Credential=ASIAROLE/") || s3Token != "role-token" {
		t.Fatalf("S3 call not signed with assumed credentials: %q %q", s3Auth, s3Token)
	}
}
