package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings limits. There is deliberately no free-edition cap; the upper bounds
// only keep a typo from spawning thousands of goroutines.
const (
	minLimit      = 1
	maxLimit      = 64
	minPartMiB    = 5 // S3 minimum part size
	maxPartMiB    = 5120
	defaultLimit  = 8
	defaultWorker = 8
	defaultPart   = 16
)

// DefaultSettings returns the settings used when settings.json does not exist.
//
// The default ConflictPolicy is "rename": it never overwrites data and never
// silently drops a transfer, so it is the safest choice when the user has not
// stated a preference. The UI offers the other policies per operation.
func DefaultSettings() Settings {
	return Settings{
		Theme:       "system",
		Limits:      Limits{Upload: defaultLimit, Download: defaultLimit, Other: defaultLimit},
		BulkWorkers: defaultWorker,
		PartSizeMiB: defaultPart,
		Conflict:    ConflictRename,

		SecretBackend:        SecretBackendKeyring,
		VaultAutoLockMinutes: 15,
	}
}

// withDefaults fills zero values (fields absent from an older file) with defaults.
func (st Settings) withDefaults() Settings {
	d := DefaultSettings()
	if st.Theme == "" {
		st.Theme = d.Theme
	}
	if st.Limits.Upload == 0 {
		st.Limits.Upload = d.Limits.Upload
	}
	if st.Limits.Download == 0 {
		st.Limits.Download = d.Limits.Download
	}
	if st.Limits.Other == 0 {
		st.Limits.Other = d.Limits.Other
	}
	if st.BulkWorkers == 0 {
		st.BulkWorkers = d.BulkWorkers
	}
	if st.PartSizeMiB == 0 {
		st.PartSizeMiB = d.PartSizeMiB
	}
	if st.Conflict == "" {
		st.Conflict = d.Conflict
	}
	if st.SecretBackend == "" {
		st.SecretBackend = d.SecretBackend
	}
	// VaultAutoLockMinutes: 0 means "never", so it is not defaulted here. A file
	// that predates the field keeps the default because load() decodes onto
	// DefaultSettings.
	return st
}

// validate checks a fully defaulted Settings.
func (st Settings) validate() error {
	switch st.Theme {
	case "system", "light", "dark":
	default:
		return fmt.Errorf("unknown theme %q", st.Theme)
	}
	for name, v := range map[string]int{"upload limit": st.Limits.Upload, "download limit": st.Limits.Download, "other limit": st.Limits.Other, "bulk workers": st.BulkWorkers} {
		if v < minLimit || v > maxLimit {
			return fmt.Errorf("%s must be between %d and %d", name, minLimit, maxLimit)
		}
	}
	if st.PartSizeMiB < minPartMiB || st.PartSizeMiB > maxPartMiB {
		return fmt.Errorf("part size must be between %d and %d MiB", minPartMiB, maxPartMiB)
	}
	if !st.Conflict.Valid() {
		return fmt.Errorf("unknown conflict policy %q", st.Conflict)
	}
	switch st.SecretBackend {
	case SecretBackendKeyring, SecretBackendVault:
	default:
		return fmt.Errorf("unknown secret backend %q", st.SecretBackend)
	}
	if st.VaultAutoLockMinutes < 0 || st.VaultAutoLockMinutes > maxVaultAutoLockMinutes {
		return fmt.Errorf("vault auto-lock must be between 0 and %d minutes", maxVaultAutoLockMinutes)
	}
	if st.DefaultDownloadDir != "" && !filepath.IsAbs(st.DefaultDownloadDir) {
		return errors.New("default download directory must be an absolute path")
	}
	return nil
}

// Settings returns the current settings.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SaveSettings validates, persists and applies st. Limits take effect on the
// running queue immediately; lowering them never interrupts running tasks.
// Zero fields are replaced by their defaults.
func (s *Service) SaveSettings(st Settings) error {
	st = st.withDefaults()
	if err := st.validate(); err != nil {
		return invalid("SaveSettings", "%v", err)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	// The secret backend changes only through SetSecretBackend (it migrates the
	// secrets); whatever the caller passes is replaced by the current value.
	s.mu.Lock()
	st.SecretBackend = s.settings.SecretBackend
	s.mu.Unlock()
	if err := writeJSONAtomic(s.path("settings.json"), st); err != nil {
		return classify("SaveSettings", err)
	}
	s.mu.Lock()
	old := s.settings
	s.settings = st
	s.mu.Unlock()
	s.setLimits(st.Limits)
	if old.Limits != st.Limits {
		s.lgInfo("queue limits changed", "upload", st.Limits.Upload, "download", st.Limits.Download, "other", st.Limits.Other,
			"prev_upload", old.Limits.Upload, "prev_download", old.Limits.Download, "prev_other", old.Limits.Other)
	}
	s.applyLogging(st.LogToFile) // a log file that cannot be opened never fails the save
	return nil
}

// ---- atomic JSON files ----------------------------------------------------------

// writeJSONAtomic writes v as indented JSON to path: temp file in the same
// directory, fsync, rename over the target, permissions 0600. A crash leaves
// either the old or the new file, never a partial one.
func writeJSONAtomic(path string, v any) (err error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil && !isChmodUnsupported(err) {
		return err
	}
	err = nil
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// readJSON loads path into v. A missing file reports found=false and leaves v
// untouched (callers then use defaults). Unreadable or corrupt content is an
// error: a damaged file must never be silently replaced by defaults.
func readJSON(path string, v any) (found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, fmt.Errorf("%s is corrupt (fix or remove it): %w", filepath.Base(path), err)
	}
	return true, nil
}

func isChmodUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported)
}

// syncDir flushes the directory entry on platforms where that is possible
// (best effort; it fails harmlessly on Windows).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
