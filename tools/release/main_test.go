package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseInputs(t *testing.T) {
	for _, version := range []string{"", "../0.2.0", "v0.2.0", "01.2.0", "0.2.0-01", "0.2.0;echo bad"} {
		if err := release(version, allTargets, t.TempDir(), false); err == nil {
			t.Fatalf("accepted invalid version %q", version)
		}
	}
	for _, version := range []string{"0.2.0", "1.2.3-rc.1", "1.2.3+build.7"} {
		if !versionRE.MatchString(version) {
			t.Fatalf("rejected version %q", version)
		}
	}
	for _, list := range []string{"", "linux/386", "windows/amd64,", "linux/amd64,linux/amd64", "../windows/amd64"} {
		if _, err := targets(list); err == nil {
			t.Fatalf("accepted targets %q", list)
		}
	}
	if ts, err := targets(" windows/amd64, linux/arm64 "); err != nil || strings.Join(ts, ",") != "windows/amd64,linux/arm64" {
		t.Fatalf("trim targets: %v %v", ts, err)
	}
}

func writeFixture(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishProtectsUnrelatedFiles(t *testing.T) {
	for _, name := range []string{"notes.txt", "Pail-v0.1.0-windows-amd64.zip"} {
		parent := t.TempDir()
		file := filepath.Join(parent, "0.2.0", name)
		writeFixture(t, file, "keep")
		source := filepath.Join(parent, "stage", "archives")
		writeFixture(t, filepath.Join(source, "SHA256SUMS"), "new")
		if err := publish(parent, "0.2.0", source); err == nil {
			t.Fatalf("replaced unrelated file %s", name)
		}
		if b, err := os.ReadFile(file); err != nil || string(b) != "keep" {
			t.Fatalf("original file changed: %q %v", b, err)
		}
	}
}

func TestPublishRejectsLinksAndOutsideSource(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(parent, "0.2.0", "SHA256SUMS"), "old")
	if err := publish(parent, "0.2.0", outside); err == nil {
		t.Fatal("accepted source outside output parent")
	}
	if b, err := os.ReadFile(filepath.Join(parent, "0.2.0", "SHA256SUMS")); err != nil || string(b) != "old" {
		t.Fatal("invalid source destroyed original output")
	}
	writeFixture(t, filepath.Join(outside, "keep"), "keep")
	if err := os.Symlink(outside, filepath.Join(parent, "0.3.0")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	source := filepath.Join(parent, "stage", "archives")
	writeFixture(t, filepath.Join(source, "SHA256SUMS"), "new")
	if err := publish(parent, "0.3.0", source); err == nil {
		t.Fatal("replaced linked output")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("linked directory was modified")
	}
}

func TestPublishReplacesOnlyCompletedRelease(t *testing.T) {
	parent := t.TempDir()
	writeFixture(t, filepath.Join(parent, "0.2.0", "SHA256SUMS"), "old")
	source := filepath.Join(parent, "stage", "archives")
	writeFixture(t, filepath.Join(source, "SHA256SUMS"), "new")
	if err := publish(parent, "0.2.0", filepath.Join(parent, "missing")); err == nil {
		t.Fatal("accepted missing staging output")
	}
	if b, _ := os.ReadFile(filepath.Join(parent, "0.2.0", "SHA256SUMS")); string(b) != "old" {
		t.Fatal("missing staging output destroyed previous release")
	}
	if err := publish(parent, "0.2.0", source); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(parent, "0.2.0", "SHA256SUMS")); string(b) != "new" {
		t.Fatal("new release was not installed")
	}
}

func TestLaunchAssets(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "assets", "brand", "generated", "pail.icns"), "icns")
	writeFixture(t, filepath.Join(root, "assets", "brand", "generated", "pail-256.png"), "png")
	for _, osName := range []string{"linux", "darwin"} {
		t.Run(osName, func(t *testing.T) {
			dir := t.TempDir()
			const version = "0.2.0-rc.1+test"
			exe := executable("pail-desktop", version, osName, "arm64")
			writeFixture(t, filepath.Join(dir, exe), "executable")
			if err := launchAssets(root, dir, version, osName, "arm64"); err != nil {
				t.Fatal(err)
			}
			if osName == "linux" {
				b, err := os.ReadFile(filepath.Join(dir, "pail.desktop"))
				if err != nil || !strings.Contains(string(b), "\nExec="+exe+"\n") {
					t.Fatalf("broken desktop entry: %q %v", b, err)
				}
			} else {
				b, err := os.ReadFile(filepath.Join(dir, "Pail.app", "Contents", "Info.plist"))
				if err != nil || !strings.Contains(string(b), "<key>CFBundleExecutable</key><string>"+exe+"</string>") ||
					!strings.Contains(string(b), "<key>CFBundleVersion</key><string>0.2.0</string>") {
					t.Fatalf("broken app bundle plist: %q %v", b, err)
				}
				if b, err := os.ReadFile(filepath.Join(dir, "Pail.app", "Contents", "MacOS", exe)); err != nil || string(b) != "executable" {
					t.Fatalf("lost bundle executable: %q %v", b, err)
				}
			}
		})
	}
}
