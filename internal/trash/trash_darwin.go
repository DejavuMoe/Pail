//go:build darwin

package trash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// moveToTrash moves path into ~/.Trash under a unique name. Finder's "Put
// Back" metadata is not written. A rename across volumes (EXDEV) is reported as
// ErrUnavailable because the per-volume .Trashes folders need privileges.
func moveToTrash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	dir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return moveUnique(dir, path, renameExcl)
}

// renameExcl is rename(2) that refuses to replace dst (renamex_np with
// RENAME_EXCL, atomic). File systems without that flag report ENOTSUP or
// EINVAL; those fall back to a reservation that is atomic as well.
func renameExcl(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return fallbackNoReplace(src, dst)
	}
	return err
}
