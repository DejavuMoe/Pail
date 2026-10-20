//go:build darwin

package trash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// moveToTrash renames path into ~/.Trash under a unique name. Finder's "Put
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
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	for n := 0; n < 1000; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s %s %d%s", stem, time.Now().Format("15.04.05"), n, ext)
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		err := os.Rename(path, dst)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EXDEV):
			return fmt.Errorf("%w: %s is on another volume than the home folder", ErrUnavailable, path)
		case errors.Is(err, os.ErrExist):
			continue
		default:
			return fmt.Errorf("trash: %w", err)
		}
	}
	return fmt.Errorf("trash: no free name in %s", dir)
}
