package trash

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolate points the trash of Linux and macOS at temporary directories so the
// tests do not fill the real trash. Windows always uses the real Recycle Bin.
func isolate(t *testing.T) (dataHome string) {
	t.Helper()
	dataHome = t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", t.TempDir())
	return dataHome
}

func TestMoveFileToTrash(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "Pail trash test ü.txt")
	if err := os.WriteFile(p, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(p); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Recycle Bin not usable in this session: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestMoveDirToTrash(t *testing.T) {
	isolate(t)
	d := filepath.Join(t.TempDir(), "pail-trash-dir")
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(d); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Recycle Bin not usable in this session: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := os.Lstat(d); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestMoveMissingFails(t *testing.T) {
	isolate(t)
	if err := MoveToTrash(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("moving a missing path must fail")
	}
	if err := MoveToTrash(""); err == nil {
		t.Fatal("empty path must fail")
	}
}
