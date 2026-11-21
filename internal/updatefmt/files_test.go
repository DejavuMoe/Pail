// Copied from github.com/egoist/mygo v0.10.0 internal/update (MIT License,
// Copyright (c) 2026 MyGo contributors; see LICENSE.mygo). Keep the formats
// byte-for-byte compatible with what the mygo version in go.mod reads.

package updatefmt

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileList(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFileList(dir, []string{"b", FileList, "a", "../up", "sub/x", `sub\x`, "", ".", "b"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, FileList))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "a\nb\n" {
		t.Errorf("the list holds %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("writing the list left %v", entries)
	}
	// What is not an entry of the directory itself is never removed.
	got := ParseFileList([]byte("a\r\nb\n\n../up\n..\n.\nsub/x\nsub\\x\n" + FileList + "\nc"))
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("ParseFileList = %q, want %q", got, want)
	}
}

// The updater rewrites the installed app's list, so deltas never make the
// new one from it.
func TestDeltaFileList(t *testing.T) {
	list := "app.exe\nold.dll\nresources\n"
	oldDir := writeTree(t, map[string]string{"app.exe": "code", "old.dll": "old", FileList: list})
	newDir := writeTree(t, map[string]string{"app.exe": "new code", FileList: list})
	var delta bytes.Buffer
	if err := WriteDelta(&delta, "1.0.0", "1.1.0", oldDir, newDir, nil); err != nil {
		t.Fatal(err)
	}
	installed := writeTree(t, map[string]string{"app.exe": "code", "old.dll": "old", FileList: "app.exe\n"})
	out := filepath.Join(t.TempDir(), "app")
	if err := ApplyDelta(bytes.NewReader(delta.Bytes()), int64(delta.Len()), "1.0.0", "1.1.0", installed, out); err != nil {
		t.Fatal(err)
	}
	if got, want := readTree(t, out), readTree(t, newDir); !maps.Equal(got, want) {
		t.Errorf("applying the delta made %v, want %v", got, want)
	}
}
