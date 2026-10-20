// Package trash moves local files and folders to the operating system's trash
// (Recycle Bin, freedesktop.org Trash, macOS ~/.Trash). It never deletes
// anything permanently: if the platform cannot recycle the item, MoveToTrash
// fails and the item stays where it is.
package trash

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrUnavailable means the item cannot be moved to a trash on this platform or
// file system (no trash directory is usable, cross-device without a per-volume
// trash, unsupported OS ...). The item is left untouched.
var ErrUnavailable = errors.New("trash: not available for this item")

// MoveToTrash moves the file or directory at path to the trash. A symbolic link
// is moved itself, never its target. On success the original path no longer
// exists; on any error the item has not been deleted.
func MoveToTrash(path string) error {
	if path == "" {
		return errors.New("trash: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	if _, err := os.Lstat(abs); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	if err := moveToTrash(abs); err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err == nil {
		return fmt.Errorf("trash: %s still exists after moving it to the trash", abs)
	}
	return nil
}
