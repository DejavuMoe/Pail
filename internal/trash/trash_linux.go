//go:build linux

package trash

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// moveToTrash implements the freedesktop.org Trash specification 1.0:
//
//   - the home trash $XDG_DATA_HOME/Trash (default ~/.local/share/Trash) when the
//     item is on the same file system as it;
//   - otherwise the top directory trash of the item's file system:
//     $topdir/.Trash/$uid when .Trash is a sticky, non-symlink directory,
//     else $topdir/.Trash-$uid (created with mode 0700).
//
// If neither can be used the item is not moved and ErrUnavailable is returned.
// The top directory is found by walking up while the device number stays the
// same, which equals the mount point except for bind mounts.
func moveToTrash(path string) error {
	dev, err := deviceOf(path)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	var lastErr error
	if home := homeTrash(); home != "" {
		if err := os.MkdirAll(home, 0o700); err == nil {
			if hdev, err := deviceOf(home); err == nil && hdev == dev {
				return trashInto(home, "", path)
			}
		} else {
			lastErr = err
		}
	}
	top, err := topDir(path, dev)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	dir, err := volumeTrash(top)
	if err != nil {
		if lastErr != nil {
			err = fmt.Errorf("%v; home trash: %v", err, lastErr)
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return trashInto(dir, top, path)
}

func homeTrash() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "Trash")
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	return filepath.Join(h, ".local", "share", "Trash")
}

func deviceOf(p string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev is uint32 on some architectures
}

// topDir returns the highest ancestor of path on the same device.
func topDir(path string, dev uint64) (string, error) {
	cur := filepath.Dir(path)
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur, nil
		}
		pd, err := deviceOf(parent)
		if err != nil {
			return "", err
		}
		if pd != dev {
			return cur, nil
		}
		cur = parent
	}
}

// volumeTrash returns the usable trash directory for the file system whose top
// directory is top, creating it when needed.
func volumeTrash(top string) (string, error) {
	uid := fmt.Sprint(os.Getuid())
	shared := filepath.Join(top, ".Trash")
	if st, err := os.Lstat(shared); err == nil && st.IsDir() && st.Mode()&os.ModeSticky != 0 && st.Mode()&os.ModeSymlink == 0 {
		dir := filepath.Join(shared, uid)
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir, nil
		}
	}
	dir := filepath.Join(top, ".Trash-"+uid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("no usable trash directory on the file system of %s: %w", top, err)
	}
	return dir, nil
}

// trashInto moves path into trash (files/ and info/ below it). For a volume
// trash, top is the top directory and Path in the .trashinfo is relative to it;
// for the home trash top is "" and Path is absolute.
func trashInto(trash, top, path string) error {
	files, info := filepath.Join(trash, "files"), filepath.Join(trash, "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	recorded := path
	if top != "" {
		if rel, err := filepath.Rel(top, path); err == nil {
			recorded = rel
		}
	}
	content := "[Trash Info]\nPath=" + escapePath(recorded) + "\nDeletionDate=" + time.Now().Format("2006-01-02T15:04:05") + "\n"

	base := filepath.Base(path)
	for n := 0; n < 10000; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s.%d", base, n+1)
		}
		// The .trashinfo file is created first with O_EXCL: it reserves the name.
		infoPath := filepath.Join(info, name+".trashinfo")
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("trash: %w", err)
		}
		dst := filepath.Join(files, name)
		if _, err := os.Lstat(dst); err == nil { // stale file without info: pick another name
			f.Close()
			os.Remove(infoPath)
			continue
		}
		_, werr := f.WriteString(content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(infoPath)
			return fmt.Errorf("trash: %w", werr)
		}
		if err := os.Rename(path, dst); err != nil {
			os.Remove(infoPath)
			if errors.Is(err, syscall.EXDEV) {
				return fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			return fmt.Errorf("trash: %w", err)
		}
		return nil
	}
	return fmt.Errorf("trash: no free name in %s", files)
}

// escapePath percent-encodes each path segment, keeping "/" separators.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
