package rules

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestEvaluateUnlimitedFirstMatchIsolation(t *testing.T) {
	var rs []UploadRule
	for i := 0; i < 8; i++ {
		rs = append(rs, UploadRule{Name: fmt.Sprint(i), BucketPattern: "photos*", KeyPattern: "*.jpg", Metadata: map[string]string{fmt.Sprint("field", i): "v"}, Headers: map[string]string{"content-type": "image/jpeg"}})
	}
	rs[7].StorageClass = "STANDARD_IA"
	rs[7].SSE = &Encryption{Type: "AES256"}
	rs[7].Compression = &Compression{Method: "gzip", Level: 9}
	rs[7].Headers["content-type"] = "last"
	out, err := Evaluate(rs, "photos-prod", "folder/猫.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.MatchedRules) != 2 || len(out.Metadata) != 1 || out.StorageClass != "STANDARD_IA" || out.Headers["Content-Type"] != "image/jpeg" {
		t.Fatalf("bad first-match options: %+v", out)
	}
	out.Metadata["field0"] = "changed"
	out.SSE.Type = "changed"
	out.Compression.Level = 0
	if rs[0].Metadata["field0"] != "v" || rs[7].SSE.Type != "AES256" || rs[7].Compression.Level != 9 {
		t.Fatal("output aliases input")
	}
	none, err := Evaluate(rs, "other", "file.jpg")
	if err != nil || len(none.MatchedRules) != 0 {
		t.Fatal("bucket filter failed")
	}
	rs[7].Disabled = true
	out, err = Evaluate(rs, "photos-prod", "file.jpg")
	if err != nil || out.StorageClass != "" {
		t.Fatal("disabled rule applied")
	}
}
func TestMatchS3Masks(t *testing.T) {
	for _, tc := range []struct {
		mask, value string
		want        bool
	}{{"", "any", true}, {"*.jpg", "a/b.jpg", true}, {"a?", "a猫", true}, {"A*", "abc", false}, {"[a]", "a", false}, {"[a]", "[a]", true}, {"*", "line\nkey", true}} {
		if got := Match(tc.mask, tc.value); got != tc.want {
			t.Errorf("Match(%q,%q)=%v", tc.mask, tc.value, got)
		}
	}
}
func TestEvaluateRejectsMalformedPolicy(t *testing.T) {
	for _, r := range []UploadRule{
		{Headers: map[string]string{"Authorization": "secret"}}, {Headers: map[string]string{"Content-Type": "a\r\nx: y"}}, {Headers: map[string]string{"X-Test": "a", "x-test": "b"}}, {Headers: map[string]string{"x-amz-server-side-encryption": "AES256"}}, {Metadata: map[string]string{"A": "1", "a": "2"}}, {SSE: &Encryption{Type: "SSE-C"}}, {SSE: &Encryption{Type: "AES256", KMSKeyID: "x"}}, {Compression: &Compression{Method: "gzip", Level: 10}}, {Compression: &Compression{Method: "unsupported"}},
	} {
		if _, err := Evaluate([]UploadRule{r}, "b", "k"); err == nil {
			t.Errorf("accepted invalid policy: %+v", r)
		}
	}
}

func TestGZIPLevels(t *testing.T) {
	data := []byte(strings.Repeat("中文 content \x00 abc\n", 10000))
	for level := -2; level <= 9; level++ {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			var out bytes.Buffer
			n, err := CompressGZIP(context.Background(), &out, bytes.NewReader(data), level)
			if err != nil || n != int64(len(data)) {
				t.Fatalf("compress n=%d err=%v", n, err)
			}
			zr, err := gzip.NewReader(&out)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := io.ReadAll(zr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, decoded) {
				t.Fatal("round trip changed data")
			}
			if err := zr.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var errInjected = errors.New("injected")

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errInjected }

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errInjected }

type cancellingReader struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "payload"), nil
}
func TestGZIPFailures(t *testing.T) {
	if _, err := CompressGZIP(context.Background(), io.Discard, strings.NewReader("x"), 10); err == nil {
		t.Fatal("invalid level accepted")
	}
	if _, err := CompressGZIP(context.Background(), failedWriter{}, strings.NewReader("x"), 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost writer error: %v", err)
	}
	if _, err := CompressGZIP(context.Background(), io.Discard, failedReader{}, 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost reader error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if _, err := CompressGZIP(ctx, &out, strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal("pre-cancel wrote output")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, err := CompressGZIP(ctx, io.Discard, &cancellingReader{cancel: cancel}, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("midstream cancellation lost: %v", err)
	}
}
