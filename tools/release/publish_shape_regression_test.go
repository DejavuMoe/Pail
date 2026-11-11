package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseShape(t *testing.T, tarball bool, desktop bool) string {
	t.Helper()
	files := []string{"pkg/LICENSE", "pkg/pail"}
	if desktop {
		files = append(files, "pkg/pail-desktop")
	}
	var b bytes.Buffer
	if tarball {
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		for _, file := range files {
			if err := tw.WriteHeader(&tar.Header{Name: file, Mode: 0o755, Size: 7}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		zw := zip.NewWriter(&b)
		for _, file := range files {
			w, err := zw.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

func TestPublishPreservesArchiveContents(t *testing.T) {
	for _, name := range []string{"Pail-v1.2.3-windows-amd64.zip", "Pail-v1.2.3-linux-arm64.tar.gz"} {
		for _, tc := range []struct {
			name                                string
			oldFull, newFull, malformed, reject bool
		}{
			{"full-to-cli", true, false, false, true},
			{"full-to-full", true, true, false, false},
			{"cli-to-full", false, true, false, false},
			{"cli-to-cli", false, false, false, false},
			{"unreadable-replacement", true, false, true, true},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				parent := t.TempDir()
				old, staged := filepath.Join(parent, "1.2.3"), filepath.Join(parent, "stage", "archives")
				before := releaseShape(t, strings.HasSuffix(name, ".gz"), tc.oldFull)
				after := releaseShape(t, strings.HasSuffix(name, ".gz"), tc.newFull)
				if tc.malformed {
					after = "truncated archive"
				}
				writeFixture(t, filepath.Join(old, name), before)
				writeFixture(t, filepath.Join(old, "SHA256SUMS"), "old sums")
				writeFixture(t, filepath.Join(staged, name), after)
				writeFixture(t, filepath.Join(staged, "SHA256SUMS"), "new sums")
				err := publish(parent, "1.2.3", staged)
				if (err != nil) != tc.reject {
					t.Errorf("publish error=%v, want rejected=%v", err, tc.reject)
				}
				want, sums := after, "new sums"
				if tc.reject {
					want, sums = before, "old sums"
					if _, err := os.Stat(staged); err != nil {
						t.Errorf("rejected staging directory moved: %v", err)
					}
				}
				for file, body := range map[string]string{name: want, "SHA256SUMS": sums} {
					got, err := os.ReadFile(filepath.Join(old, file))
					if err != nil || string(got) != body {
						t.Errorf("%s not preserved/installed correctly: %v", file, err)
					}
				}
			})
		}
	}
}
