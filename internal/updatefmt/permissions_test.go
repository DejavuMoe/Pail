// Copied from github.com/egoist/mygo v0.10.0 internal/update (MIT License,
// Copyright (c) 2026 MyGo contributors; see LICENSE.mygo). Keep the formats
// byte-for-byte compatible with what the mygo version in go.mod reads.

package updatefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	modes := map[string]os.FileMode{"readonly": 0444, "executable": 0555, "shared": 0660}
	old := writeTree(t, map[string]string{"readonly": "old", "executable": "same", "shared": "old"})
	src := writeTree(t, map[string]string{"readonly": "new", "executable": "same", "shared": "new"})
	var entries []string
	for name, mode := range modes {
		entries = append(entries, name)
		if err := os.Chmod(filepath.Join(src, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"archive", "delta"} {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			dst := filepath.Join(t.TempDir(), "app")
			var err error
			if kind == "archive" {
				err = WriteArchive(&buf, src, entries)
				if err == nil {
					err = ExtractArchive(bytes.NewReader(buf.Bytes()), dst)
				}
			} else {
				err = WriteDelta(&buf, "1.0.0", "1.1.0", old, src, nil)
				if err == nil {
					err = ApplyDelta(bytes.NewReader(buf.Bytes()), int64(buf.Len()), "1.0.0", "1.1.0", old, dst)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, mode := range modes {
				info, err := os.Stat(filepath.Join(dst, name))
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != mode {
					t.Errorf("%s mode = %o, want %o", name, got, mode)
				}
			}
		})
	}
}
