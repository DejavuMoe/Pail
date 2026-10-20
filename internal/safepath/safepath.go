// Package safepath maps untrusted S3 keys to local paths and publishes
// downloaded files without overwriting data the user did not approve.
package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrExists is returned by Publish when the target exists and overwrite is false.
var ErrExists = errors.New("safepath: target already exists")

// ErrUnsafe wraps every rejection made by Join.
var ErrUnsafe = errors.New("safepath: unsafe object key")

func unsafe(key, why string) error { return fmt.Errorf("%w %q: %s", ErrUnsafe, key, why) }

var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

func init() {
	for i := 1; i <= 9; i++ {
		windowsReserved[fmt.Sprintf("COM%d", i)] = true
		windowsReserved[fmt.Sprintf("LPT%d", i)] = true
	}
}

// ValidKey reports why key cannot be mapped to a local path under the current
// OS, or nil. Keys use "/" separators; every segment must be a plain name.
// A trailing "/" (a folder marker) is not accepted: strip it first.
func ValidKey(key string) error { return validKey(key, runtime.GOOS == "windows") }

func validKey(key string, windows bool) error {
	switch {
	case key == "":
		return unsafe(key, "empty key")
	case strings.ContainsRune(key, 0):
		return unsafe(key, "contains NUL")
	case strings.HasPrefix(key, "/"):
		return unsafe(key, "absolute path or UNC prefix")
	case len(key) >= 2 && key[1] == ':' && (key[0]|0x20 >= 'a' && key[0]|0x20 <= 'z'):
		return unsafe(key, "drive prefix")
	}
	if windows && strings.Contains(key, `\`) {
		return unsafe(key, "backslash is a path separator")
	}
	for _, seg := range strings.Split(key, "/") {
		switch seg {
		case "":
			return unsafe(key, "empty path segment")
		case ".", "..":
			return unsafe(key, "relative path segment")
		}
		if !windows {
			continue
		}
		if strings.ContainsAny(seg, `<>:"|?*`) || strings.IndexFunc(seg, func(r rune) bool { return r < 0x20 }) >= 0 {
			return unsafe(key, "character not allowed in Windows file names")
		}
		if last := seg[len(seg)-1]; last == '.' || last == ' ' {
			return unsafe(key, "Windows file name ends with a dot or space")
		}
		base, _, _ := strings.Cut(seg, ".")
		if windowsReserved[strings.ToUpper(strings.TrimRight(base, " "))] {
			return unsafe(key, "reserved Windows device name")
		}
	}
	return nil
}

// Join maps an S3 key to a path under root. It rejects keys that could escape
// root or that the local OS cannot store faithfully, and refuses to pass
// through an existing symlink, junction or other reparse point below root.
// The check is advisory against concurrent local modification (TOCTOU).
func Join(root, key string) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	if root == "" {
		return "", errors.New("safepath: empty root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	segs := strings.Split(key, "/")
	full := filepath.Join(append([]string{absRoot}, segs...)...)
	if rel, err := filepath.Rel(absRoot, full); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", unsafe(key, "resolves outside the root")
	}
	cur := absRoot
	for _, seg := range segs {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			break // nothing below a missing component can be a link
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || isReparsePoint(fi) {
			return "", unsafe(key, "passes through a symbolic link, junction or reparse point")
		}
	}
	return full, nil
}

// CreateTemp creates a hidden temporary file ".pail-*.tmp" in dir (created if
// missing) for a download in progress.
func CreateTemp(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, ".pail-*.tmp")
}

// Publish moves the finished temporary file tmp to final. The file is fsynced
// first; the caller must have closed its own handle (Windows cannot rename an
// open file). With overwrite the target is replaced; without it the move is
// atomic where the OS and file system allow and falls back to Lstat+rename on
// file systems that cannot (exFAT, SMB, FUSE). ErrExists is returned (wrapped)
// when the target exists and overwrite is false. tmp is left in place on error.
func Publish(tmp, final string, overwrite bool) error {
	if err := syncFile(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	var err error
	if overwrite {
		err = os.Rename(tmp, final)
	} else {
		err = publishNoReplace(tmp, final)
	}
	if err != nil {
		return err
	}
	syncDir(filepath.Dir(final))
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// publishLstat is the non-atomic fallback: it narrows but does not close the
// window between the existence check and the rename.
func publishLstat(tmp, final string) error {
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, final)
}
