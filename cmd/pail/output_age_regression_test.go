package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAgeRejectsNonfiniteAndOverflow(t *testing.T) {
	for _, input := range []string{"NaNd", "+Infd", "-Infd", "106752d", "1e300d", "1e-100d", "0d", "-1d"} {
		t.Run(input, func(t *testing.T) {
			if age, err := parseAge(input); err == nil {
				t.Fatalf("parseAge(%q) = %s without error", input, age)
			}
		})
	}
	for input, want := range map[string]time.Duration{"0.5d": 12 * time.Hour, "1.25d": 30 * time.Hour, "106751d": 106751 * 24 * time.Hour, "36h": 36 * time.Hour} {
		if age, err := parseAge(input); err != nil || age != want {
			t.Errorf("parseAge(%q) = %s, %v; want %s", input, age, err, want)
		}
	}
}

func TestMultipartInvalidAgeNeverAborts(t *testing.T) {
	for _, input := range []string{"NaNd", "+Infd", "106752d", "1e-100d", "0.5d"} {
		t.Run(input, func(t *testing.T) {
			h := newHarness(t)
			h.startUpload("bkt", "fresh.bin")
			h.srv.ResetRequests()
			r := h.run("--json", "multipart", "abort", "s3://bkt/", "--older-than", input, "--yes")
			want := exitUsage
			if input == "0.5d" {
				want = exitOK
			}
			if r.code != want {
				t.Errorf("exit = %d, want %d; stdout=%s stderr=%s", r.code, want, r.out, r.errOut)
			}
			for _, req := range h.srv.Requests() {
				if req.Method == http.MethodDelete {
					t.Error("invalid/nonmatching age aborted a fresh upload")
				}
			}
			r = h.want(h.run("--json", "multipart", "ls", "s3://bkt/"), exitOK)
			if lastSummary(t, r.out)["count"] != float64(1) {
				t.Fatalf("fresh upload lost: %s", r.out)
			}
		})
	}
}

type failingJSONWriter struct {
	writes, failAt int
	bytes.Buffer
}

func (w *failingJSONWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errors.New("injected stdout failure")
	}
	return w.Buffer.Write(p)
}

func TestJSONWriteFailureReturnsNonzero(t *testing.T) {
	for _, tc := range []struct {
		command []string
		records int
	}{
		{[]string{"settings", "get"}, 2},
		{[]string{"accounts"}, 2},
		{[]string{"multipart", "abort", "s3://bkt/", "--yes"}, 1},
		{[]string{"rm", "s3://bkt/missing", "--yes"}, 1},
		{[]string{"rm", "s3://bkt/existing", "--yes"}, 2},
	} {
		for failAt := 1; failAt <= tc.records; failAt++ {
			t.Run(strings.Join(tc.command, "_")+string(rune('0'+failAt)), func(t *testing.T) {
				h := newHarness(t)
				h.srv.PutObject("bkt", "existing", []byte("remove"), "text/plain", nil)
				w := &failingJSONWriter{failAt: failAt}
				var stderr bytes.Buffer
				code := run(context.Background(), append([]string{"--config-dir", h.dir, "--json"}, tc.command...), IO{Stdout: w, Stderr: &stderr, Keyring: h.kr})
				if code == exitOK || !strings.Contains(stderr.String(), "injected stdout failure") {
					t.Errorf("write failure reported success or lacked diagnostic: exit=%d stderr=%s", code, &stderr)
				}
				if w.writes != failAt {
					t.Errorf("attempted %d writes after failure at %d", w.writes, failAt)
				}
			})
		}
	}
}

func TestJSONWriteFailurePreservesUsageExit(t *testing.T) {
	w := &failingJSONWriter{failAt: 1}
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--json", "unknown-command"}, IO{Stdout: w, Stderr: &stderr})
	if code != exitUsage || !strings.Contains(stderr.String(), "unknown command") || !strings.Contains(stderr.String(), "injected stdout failure") {
		t.Fatalf("exit=%d stderr=%s", code, &stderr)
	}
}

func TestJSONReadOnlyFileReturnsFailure(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--config-dir", t.TempDir(), "--json", "settings", "get"}, IO{Stdout: f, Stderr: &stderr})
	if code != exitFailure || !strings.Contains(stderr.String(), "write") {
		t.Fatalf("failed OS writer: exit=%d stderr=%s", code, &stderr)
	}
}

func TestJSONPreviewFailureDoesNotStartSync(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "new.txt"), "new content")
	w := &failingJSONWriter{failAt: 1}
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--config-dir", h.dir, "--json", "sync", dir, "s3://bkt/", "--yes"}, IO{Stdout: w, Stderr: &stderr, Keyring: h.kr})
	if code != exitFailure || !strings.Contains(stderr.String(), "injected stdout failure") {
		t.Errorf("exit=%d stderr=%s", code, &stderr)
	}
	if _, exists := h.srv.Object("bkt", "new.txt"); exists {
		t.Error("sync uploaded an object after its JSON preview failed")
	}
}
