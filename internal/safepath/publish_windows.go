//go:build windows

package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"

	"golang.org/x/sys/windows"
)

func isReparsePoint(fi fs.FileInfo) bool {
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return ok && d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// publishNoReplace uses MoveFileEx without MOVEFILE_REPLACE_EXISTING, which fails
// atomically when the target exists.
func publishNoReplace(tmp, final string) error {
	from, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(final)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, 0)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return fmt.Errorf("%w: %s", ErrExists, final)
	}
	return err
}

// SyncDir flushes the directory entry to disk, best effort. A no-op on Windows,
// where directories cannot be fsynced.
func SyncDir(string) {}
