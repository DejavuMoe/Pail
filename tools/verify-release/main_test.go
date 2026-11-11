package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/buildstamp"
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

func TestRejectsBundledReadmes(t *testing.T) {
	for _, name := range []string{"README.md", "README.zh-CN.md"} {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		w, err := zw.Create("Pail-v1.2.3-windows-amd64/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("documentation belongs on the website"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		err = verifyArchive(b.Bytes(), "Pail-v1.2.3-windows-amd64", "1.2.3", "windows", "amd64")
		if err == nil || !strings.Contains(err.Error(), "unexpected file") {
			t.Fatalf("bundled %s not rejected: %v", name, err)
		}
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

func releaseInfo(settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		Path: "github.com/DejavuMoe/Pail/cmd/pail",
		Settings: append([]debug.BuildSetting{
			{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "amd64"}, {Key: "CGO_ENABLED", Value: "0"},
		}, settings...),
	}
}

func TestBuildInfoMustCarryReleaseVersion(t *testing.T) {
	ld := func(v string) debug.BuildSetting { return debug.BuildSetting{Key: "-ldflags", Value: v} }
	body := versionFixture(t, "linux/amd64", "1.2.3", "-s -w")
	if err := checkBuildInfo(releaseInfo(ld("-s -w "+buildstamp.LDFlag("1.2.3"))), body, "pail", false, "1.2.3", "linux", "amd64"); err != nil {
		t.Fatalf("good build rejected: %v", err)
	}
	for name, v := range map[string]string{
		"no ldflags value": "",
		"no -X":            "-s -w",
		"other version":    "-s -w " + buildstamp.LDFlag("1.2.2"),
		"longer version":   "-s -w " + buildstamp.LDFlag("1.2.30"),
		"other variable":   "-s -w -X main.version=1.2.3",
		"suffixed version": "-s -w " + buildstamp.LDFlag("1.2.3-dirty"),
	} {
		err := checkBuildInfo(releaseInfo(ld(v)), body, "pail", false, "1.2.3", "linux", "amd64")
		if err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("%s: accepted %q: %v", name, v, err)
		}
	}
	// Without -trimpath the setting must be there.
	if err := checkBuildInfo(releaseInfo(), []byte("1.2.3"), "pail", false, "1.2.3", "linux", "amd64"); err == nil {
		t.Error("accepted a build with no recorded -ldflags")
	}
}

// Go does not record -ldflags for -trimpath builds, which is how releases are
// built; the version must then come from the stamp in the binary.
func TestTrimpathBuildInfoFallsBackToBinaryContents(t *testing.T) {
	trim := releaseInfo(debug.BuildSetting{Key: "-trimpath", Value: "true"})
	body := versionFixture(t, "linux/amd64", "1.2.3", "-s -w")
	if err := checkBuildInfo(trim, body, "pail", false, "1.2.3", "linux", "amd64"); err != nil {
		t.Fatalf("trimpath build with the version rejected: %v", err)
	}
	err := checkBuildInfo(trim, []byte("...dev..."), "pail", false, "1.2.3", "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("trimpath build without the version accepted: %v", err)
	}
}
