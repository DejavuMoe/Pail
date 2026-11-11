//go:build darwin

package trash

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// renameExcl is the real macOS primitive: it must refuse to replace anything,
// including an empty directory.
func TestRenameExclNeverReplaces(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "src.txt"), filepath.Join(d, "dst.txt")
	write(t, src, "mine")
	write(t, dst, "theirs")
	if err := renameExcl(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, dst) != "theirs" || read(t, src) != "mine" {
		t.Fatal("replaced")
	}
	free := filepath.Join(d, "free.txt")
	if err := renameExcl(src, free); err != nil || read(t, free) != "mine" {
		t.Fatalf("move to a free name: %v", err)
	}
	dir, empty := filepath.Join(d, "dir"), filepath.Join(d, "empty")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := renameExcl(dir, empty); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("directory over an empty directory: %v", err)
	}
}

func TestMoveToTrashSameNameKeepsBoth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, c := range []string{"one", "two"} {
		p := filepath.Join(t.TempDir(), "same.txt")
		write(t, p, c)
		if err := MoveToTrash(p); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(home, ".Trash"))
	if len(ents) != 2 {
		t.Fatalf("want 2 trashed files, got %d", len(ents))
	}
}
