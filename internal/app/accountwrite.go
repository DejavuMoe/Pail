package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
)

func (s *Service) readAccounts() ([]accountRecord, error) {
	var recs []accountRecord
	if _, err := readJSON(s.path("accounts.json"), &recs); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, r := range recs {
		if r.ID == "" || seen[r.ID] {
			return nil, errors.New("accounts.json is corrupt: missing or duplicate account id")
		}
		seen[r.ID] = true
	}
	return recs, nil
}

// beginAccountWrite starts an account transaction; see beginAccountWriteCtx.
func (s *Service) beginAccountWrite() (func(), error) {
	return s.beginAccountWriteCtx(context.Background())
}

// beginAccountWriteCtx takes acctMu and accounts.lock (filelock.go), waiting
// at most configLockTimeout or until ctx ends, then reloads accounts.json so
// the transaction starts from the latest state on disk.
// Lock the stable sidecar, not accounts.json: atomic replacement changes its inode.
// Keep the lock through credential cleanup so another writer cannot reuse a generation.
// Vault calls made while it is held take vault.lock after it (never the reverse).
func (s *Service) beginAccountWriteCtx(ctx context.Context) (func(), error) {
	deadline := configLockDeadline()
	// acctMu may be held across a keychain prompt, so its wait is bounded too.
	if err := lockMutex(ctx, &s.acctMu, deadline); err != nil {
		return nil, err
	}
	release, err := lockFileUntil(ctx, s.path(accountsLockFileName), deadline)
	if err != nil {
		s.acctMu.Unlock()
		return nil, err
	}
	end := func() { release(); s.acctMu.Unlock() }
	recs, err := s.readAccounts()
	if err != nil {
		end()
		return nil, err
	}
	st := DefaultSettings()
	if _, err := readJSON(s.path("settings.json"), &st); err != nil {
		end()
		return nil, err
	}
	if st.withDefaults().SecretBackend != s.SecretBackend() {
		end()
		return nil, &OpError{Kind: ErrConflict, Message: "the secret backend changed in another instance; reopen the application before editing accounts or credentials"}
	}
	s.mu.Lock()
	for _, old := range s.accounts {
		var current accountRecord
		for _, rec := range recs {
			if rec.ID == old.ID {
				current = rec
				break
			}
		}
		if !reflect.DeepEqual(old, current) {
			s.invalidateLocked(old.ID)
			for key := range s.bucketRegion {
				if strings.HasPrefix(key, old.ID+"/") {
					delete(s.bucketRegion, key)
				}
			}
		}
	}
	s.accounts = recs
	s.mu.Unlock()
	return end, nil
}
