package rules

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestValidateAllRules(t *testing.T) {
	for _, r := range []UploadRule{{Disabled: true, Compression: &Compression{Method: "bad"}}, {BucketPattern: "never", Compression: &Compression{Method: "gzip", Level: 10}}} {
		if err := Validate([]UploadRule{r}); err == nil {
			t.Fatal("invalid dormant rule accepted")
		}
		if _, err := Evaluate([]UploadRule{r}, "b", "k"); err == nil {
			t.Fatal("Evaluate skipped validation")
		}
	}
}
func TestFirstMatchPerCategory(t *testing.T) {
	rs := []UploadRule{{Name: "empty"}, {Name: "disabled", Disabled: true, StorageClass: "wrong"}, {Name: "metadata", Metadata: map[string]string{"first": "yes"}}, {Name: "class", Headers: map[string]string{"Content-Type": "ignored"}, StorageClass: "STANDARD"}, {Name: "sse", StorageClass: "wrong", SSE: &Encryption{Type: "AES256"}}, {Name: "zip", SSE: &Encryption{Type: "aws:kms"}, Compression: &Compression{Method: "zip", Level: 9}}, {Name: "later", Compression: &Compression{Method: "gzip", Level: 1}}}
	out, err := Evaluate(rs, "b", "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.MatchedRules) != 4 || len(out.Headers) != 0 || len(out.Metadata) != 1 || out.StorageClass != "STANDARD" || out.SSE.Type != "AES256" || out.Compression.Method != "zip" {
		t.Fatalf("per-category precedence failed: %+v", out)
	}
}
func TestZIPLevels(t *testing.T) {
	data := []byte(strings.Repeat("中文 content \x00 abc\n", 10000))
	for level := -2; level <= 9; level++ {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			var out bytes.Buffer
			n, err := CompressZIP(context.Background(), &out, bytes.NewReader(data), "文件.txt", level)
			if err != nil || n != int64(len(data)) {
				t.Fatalf("compress n=%d err=%v", n, err)
			}
			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if len(zr.File) != 1 || zr.File[0].Name != "文件.txt" {
				t.Fatal("bad ZIP members")
			}
			wantMethod := uint16(zip.Deflate)
			if level == 0 {
				wantMethod = zip.Store
			}
			if zr.File[0].Method != wantMethod {
				t.Fatal("wrong compression method")
			}
			rc, err := zr.File[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			decoded, readErr := io.ReadAll(rc)
			closeErr := rc.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(data, decoded) {
				t.Fatal("ZIP round trip failed", readErr, closeErr)
			}
		})
	}
}
func TestZIPFailures(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../file", "a/b", "a\\b", "C:file", "nul\x00x"} {
		var out bytes.Buffer
		if _, err := CompressZIP(context.Background(), &out, strings.NewReader("x"), name, 1); err == nil || out.Len() != 0 {
			t.Errorf("unsafe name %q accepted", name)
		}
	}
	for _, level := range []int{-3, 10} {
		if _, err := CompressZIP(context.Background(), io.Discard, strings.NewReader("x"), "x", level); err == nil {
			t.Fatal("invalid level")
		}
	}
	if _, err := CompressZIP(context.Background(), failedWriter{}, strings.NewReader("x"), "x", 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost writer error: %v", err)
	}
	if _, err := CompressZIP(context.Background(), io.Discard, failedReader{}, "x", 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost reader error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if _, err := CompressZIP(ctx, &out, strings.NewReader("x"), "x", 1); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal("pre-cancel wrote output")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, err := CompressZIP(ctx, io.Discard, &cancellingReader{cancel: cancel}, "x", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
