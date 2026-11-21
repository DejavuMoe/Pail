// Copied from github.com/egoist/mygo v0.10.0 internal/update (MIT License,
// Copyright (c) 2026 MyGo contributors; see LICENSE.mygo). Keep the formats
// byte-for-byte compatible with what the mygo version in go.mod reads.

package updatefmt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FileList is the file of an app directory, on Linux and Windows, that
// names the entries of the directory that its version installed, one per
// line: `mygo build` writes it into the app, and the updater rewrites it
// with the entries of each update it installs. An update removes the
// entries that the list of the version it replaces names and its own
// version lacks, and leaves those that no list names, such as the data of
// a portable copy, or the installer's uninstaller. App bundles have none:
// updates replace the whole bundle.
const FileList = ".mygo-files"

// WriteFileList writes the FileList of dir, naming entries (FileList and
// what is not a name of dir's own entries left out), through a temporary
// file renamed into place, so that the list is never half written.
func WriteFileList(dir string, entries []string) error {
	var names []string
	for _, e := range entries {
		if fileListName(e) {
			names = append(names, e)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	f, err := os.CreateTemp(dir, FileList+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.WriteString(b.String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, FileList))
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// ParseFileList returns the entries that the content of a FileList names,
// leaving out FileList and lines that are not the name of an entry of the
// directory itself, which an update must never remove.
func ParseFileList(b []byte) []string {
	var names []string
	for line := range strings.Lines(string(b)) {
		if n := strings.TrimRight(line, "\r\n"); fileListName(n) {
			names = append(names, n)
		}
	}
	return names
}

// fileListName reports whether name can be in a FileList: the name of an
// entry of the directory, other than FileList.
func fileListName(name string) bool {
	return name != "" && name != "." && name != ".." && name != FileList &&
		!strings.ContainsAny(name, "/\\\x00\r\n")
}
