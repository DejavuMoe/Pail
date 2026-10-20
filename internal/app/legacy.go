package app

// Migration of data written by builds that were still called openS3: the
// configuration directory and the OS keychain entries. Nothing here deletes
// user data before its replacement has been verified.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// legacyConfigDirName is the per-user configuration directory name used before the rename to Pail.
const legacyConfigDirName = "openS3"

// configDirName is the per-user configuration directory name.
const configDirName = "Pail"

// userConfigDir locates the base of the default configuration directory; a variable so that tests can point it at a temp dir.
var userConfigDir = os.UserConfigDir

// renameDir and copyDir are the two ways resolveDefaultConfigDir moves the
// old directory; variables so that tests can make either fail.
var (
	renameDir = os.Rename
	copyDir   = copyTree
)

// configDirMigration reports what resolveDefaultConfigDir did, for the log
// (which cannot be opened before the directory is known).
type configDirMigration struct {
	Note string // what happened ("" = nothing to do)
	Err  error  // non-nil: the move failed and the old directory is used as is
}

// resolveDefaultConfigDir returns the directory to use below base. When
// base/Pail does not exist but base/openS3 does, the old directory is renamed
// (fallback: copied to a temporary sibling that is then renamed, the old one
// staying in place). If neither works the old directory itself is used for
// this run and the move is retried at the next start, so user data is never
// stranded.
func resolveDefaultConfigDir(base string) (string, configDirMigration) {
	newDir := filepath.Join(base, configDirName)
	oldDir := filepath.Join(base, legacyConfigDirName)
	if _, err := os.Stat(newDir); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return newDir, configDirMigration{}
	}
	if fi, err := os.Stat(oldDir); err != nil || !fi.IsDir() {
		return newDir, configDirMigration{}
	}
	renameErr := renameDir(oldDir, newDir)
	if renameErr == nil {
		return newDir, configDirMigration{Note: "config directory renamed from " + oldDir + " to " + newDir}
	}
	tmp := newDir + ".migrating"
	os.RemoveAll(tmp) // leftover of an interrupted earlier attempt
	copyErr := copyDir(oldDir, tmp)
	if copyErr == nil {
		copyErr = os.Rename(tmp, newDir)
	}
	if copyErr == nil {
		return newDir, configDirMigration{Note: "config directory copied from " + oldDir + " to " + newDir + " (rename failed: " + renameErr.Error() + "); the old directory was left in place"}
	}
	os.RemoveAll(tmp)
	return oldDir, configDirMigration{Err: fmt.Errorf("could not move config directory %s to %s (rename: %v; copy: %v); continuing with the old directory", oldDir, newDir, renameErr, copyErr)}
}

// copyTree copies regular files and directories from src to dst (created).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			return copyFile(p, target)
		default:
			return fmt.Errorf("cannot copy %s: not a regular file", p)
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- keyring ----------------------------------------------------------------

// migrateLegacyKeyring moves the secrets the configuration refers to from the
// legacy keyring service to the current one. For every known key that is
// missing in cur but present in legacy it copies the value, reads it back
// and compares, and only then deletes the legacy entry. Failures are logged
// and leave the legacy entry alone, so the next start retries. It returns
// the number of entries moved.
func (s *Service) migrateLegacyKeyring(ctx context.Context, cur, legacy Keyring) int {
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	s.refMu.Lock()
	defer s.refMu.Unlock()
	moved := 0
	for _, k := range s.secretKeys() {
		if ctx.Err() != nil {
			return moved
		}
		if _, err := keyringGet(ctx, cur, k); err == nil {
			continue // already there: the legacy copy (if any) stays untouched
		} else if !errors.Is(err, ErrSecretNotFound) {
			s.lgWarn("keyring migration stopped", "error", err.Error())
			return moved
		}
		v, err := keyringGet(ctx, legacy, k)
		if errors.Is(err, ErrSecretNotFound) {
			continue
		}
		if err != nil {
			s.lgWarn("keyring migration stopped", "key", k, "error", err.Error())
			return moved
		}
		if err := cur.Set(k, v); err != nil {
			s.lgWarn("keyring migration: copy failed", "key", k, "error", err.Error())
			continue
		}
		back, err := keyringGet(ctx, cur, k)
		if err != nil || back != v {
			s.lgWarn("keyring migration: copy did not verify; legacy entry kept", "key", k)
			continue
		}
		if err := legacy.Delete(k); err != nil {
			s.lgWarn("keyring migration: legacy entry not deleted", "key", k, "error", err.Error())
		}
		moved++
	}
	if moved > 0 {
		s.lgInfo("keyring migrated", "entries", moved)
	}
	return moved
}

// runLegacyKeyringMigration is called by New for the OS keyring only. The OS
// may prompt (Secret Service unlock), so New waits a bounded time; a slower
// migration finishes in the background under the writer locks.
func (s *Service) runLegacyKeyringMigration() {
	if s.SecretBackend() != SecretBackendKeyring {
		return
	}
	cur, legacy := s.osKr, Keyring(guardedKeyring{osKeyring{legacyKeyringService}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.migrateLegacyKeyring(ctx, cur, legacy)
	}()
	select {
	case <-done:
		cancel()
	case <-time.After(10 * time.Second):
		s.lgWarn("keyring migration is taking long; continuing in the background")
		go func() { <-s.vaultStop; cancel() }()
	}
}
