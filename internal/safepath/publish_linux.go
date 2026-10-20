//go:build linux

package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func isReparsePoint(fs.FileInfo) bool { return false }

// publishNoReplace tries renameat2(RENAME_NOREPLACE), then link+unlink, then
// Lstat+rename for file systems that support neither.
func publishNoReplace(tmp, final string) error {
	err := unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, final, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%w: %s", ErrExists, final)
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EPERM):
		return publishLink(tmp, final)
	}
	return err
}

func publishLink(tmp, final string) error {
	err := os.Link(tmp, final)
	switch {
	case err == nil:
		_ = os.Remove(tmp) // the link already published the file
		return nil
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %s", ErrExists, final)
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EXDEV), errors.Is(err, unix.EMLINK):
		return publishLstat(tmp, final)
	}
	return err
}

func syncDir(dir string) {
	if d, err := os.Open(filepath.Clean(dir)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
