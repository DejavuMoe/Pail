package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// The release's Windows desktop app reported 0.1.0.0 in its file properties:
// the committed resource had a fixed version. The release now writes the
// version it builds into the resource and puts the committed files back.
func TestReleaseWritesTheVersionIntoTheWindowsResource(t *testing.T) {
	if testing.Short() {
		t.Skip("runs genicons")
	}
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	syso := filepath.Join(root, "cmd", "pail-desktop", "rsrc_windows_amd64.syso")
	before, err := os.ReadFile(syso)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := versionSyso(root, t.TempDir(), "9.8.7-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	during, _ := os.ReadFile(syso)
	restore()
	after, _ := os.ReadFile(syso)
	var want bytes.Buffer
	for _, u := range utf16.Encode([]rune("9.8.7.0")) {
		want.WriteByte(byte(u))
		want.WriteByte(byte(u >> 8))
	}
	if !bytes.Contains(during, want.Bytes()) {
		t.Error("the resource built for 9.8.7 does not say 9.8.7.0")
	}
	if !bytes.Equal(before, after) {
		t.Error("the committed resource was not put back")
	}
}
