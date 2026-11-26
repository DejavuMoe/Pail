//go:build darwin

package safepath

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// publishNoReplace renames with RENAME_EXCL (atomic); file systems without
// that flag fall back to Lstat+rename.
func publishNoReplace(tmp, final string) error {
	err := unix.RenamexNp(tmp, final, unix.RENAME_EXCL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%w: %s", ErrExists, final)
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EINVAL):
		return publishLstat(tmp, final)
	}
	return err
}
