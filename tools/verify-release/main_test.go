package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsMissingAndUnsafeArchiveEntries(t *testing.T) {
	for _, entry := range []string{"Pail-v1.2.3-windows-amd64/README.md", "../outside", "Pail-v1.2.3-windows-amd64/pail.exe"} {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		w, err := zw.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("fixture"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := verifyArchive(b.Bytes(), "Pail-v1.2.3-windows-amd64", "1.2.3", "windows", "amd64"); err == nil {
			t.Fatalf("accepted incomplete/unsafe archive %q", entry)
		}
	}
}

func TestRequiresChecksums(t *testing.T) {
	err := verify(t.TempDir(), "1.2.3", []string{"linux/amd64"})
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("missing checksum manifest: %v", err)
	}
}

func TestWindowsAndUnixChecksumLines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		dir := t.TempDir()
		name := "Pail-v1.2.3-windows-amd64.zip"
		data := []byte("not a zip: checksum should pass before archive validation")
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		sums := fmt.Sprintf("%x  %s%s%x  other.zip%s", sha256.Sum256(data), name, newline, sha256.Sum256(data), newline)
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o600); err != nil {
			t.Fatal(err)
		}
		err := verify(dir, "1.2.3", []string{"windows/amd64"})
		if err == nil || !strings.Contains(err.Error(), "not a valid zip") {
			t.Fatalf("checksum with %q line endings: %v", newline, err)
		}
	}
}
