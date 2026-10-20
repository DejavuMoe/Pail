package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func stage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"pail-desktop": "bin", "README.md": "doc", "Pail.app/Contents/MacOS/pail-desktop": "bin"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var wantModes = map[string]fs.FileMode{
	"pkg/":             0o755,
	"pkg/pail-desktop": 0o755,
	"pkg/README.md":    0o644,
	"pkg/Pail.app/":    0o755,
	"pkg/Pail.app/Contents/MacOS/pail-desktop": 0o755,
}

func TestTarGzCarriesUnixModes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := pack(stage(t), out, "pkg", []string{"pail-desktop"}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(out)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]fs.FileMode{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		got[h.Name] = fs.FileMode(h.Mode).Perm()
	}
	for name, mode := range wantModes {
		if got[name] != mode {
			t.Errorf("%s: mode %v, want %v (all: %v)", name, got[name], mode, got)
		}
	}
}

func TestZipCarriesUnixModes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.zip")
	if err := pack(stage(t), out, "pkg", []string{"pail-desktop"}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]fs.FileMode{}
	for _, f := range zr.File {
		got[f.Name] = f.Mode().Perm()
	}
	for name, mode := range wantModes {
		if got[name] != mode {
			t.Errorf("%s: mode %v, want %v", name, got[name], mode)
		}
	}
}
