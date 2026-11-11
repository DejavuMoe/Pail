package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// noReplaceRename moves src to dst atomically and never replaces dst: when
// dst exists it fails with an error for which errors.Is(err, fs.ErrExist)
// holds, and both src and dst are left untouched.
type noReplaceRename func(src, dst string) error

// moveUnique moves path into dir under a name that is free at the moment of the
// move. Checking for a free name first and renaming afterwards is not enough:
// rename(2) replaces an existing destination file, so two moves of
// same-named files that race (two parallel mirror-delete tasks, or the CLI and
// the desktop app) would silently destroy the first trashed file. The
// reservation of the name is therefore part of the rename itself; a name that
// is taken is skipped and the next candidate is tried.
func moveUnique(dir, path string, rename noReplaceRename) error {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	for n := 0; n < 1000; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s %s %d%s", stem, time.Now().Format("15.04.05"), n, ext)
		}
		err := rename(path, filepath.Join(dir, name))
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EXDEV):
			return fmt.Errorf("%w: %s is on another volume than the trash", ErrUnavailable, path)
		case errors.Is(err, ErrUnavailable):
			return err
		case errors.Is(err, fs.ErrExist):
			continue
		default:
			return fmt.Errorf("trash: %w", err)
		}
	}
	return fmt.Errorf("trash: no free name in %s", dir)
}

// fallbackNoReplace is the no-replace move for file systems where the atomic
// rename flag is not supported. Every branch reserves dst with an operation
// that fails when dst exists, and only then takes src away:
//
//   - directory: mkdir dst, then rename over the empty directory we created;
//   - regular file: hard link (fails with EEXIST), then unlink src;
//   - symbolic link: create the same link at dst, then unlink src.
//
// Anything else (sockets, devices ...) or a file system that cannot link
// yields ErrUnavailable and the item stays where it is.
func fallbackNoReplace(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.IsDir():
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		// os.Rename refuses any existing directory as the target on Unix, so
		// call rename(2) itself: it replaces only the empty directory just
		// reserved, and fails if something was put into it meanwhile.
		if err := syscall.Rename(src, dst); err != nil {
			os.Remove(dst) // the empty directory reserved above
			return err
		}
		return nil
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
		return removeOrUndo(src, dst)
	case st.Mode().IsRegular():
		if err := os.Link(src, dst); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return err
			}
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return removeOrUndo(src, dst)
	default:
		return fmt.Errorf("%w: %s is not a regular file, directory or symbolic link", ErrUnavailable, src)
	}
}

// removeOrUndo unlinks src after dst has been created; if that fails dst is
// removed again so that exactly one copy remains.
func removeOrUndo(src, dst string) error {
	if err := os.Remove(src); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
