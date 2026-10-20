package app

// Secret backends: the OS keychain (default) or the encrypted vault (vault.go).
//
// Service.kr is a secretStore that forwards every Keyring call to the backend
// selected in Settings.SecretBackend, so the rest of the package (accounts,
// refs, client cache) neither knows nor cares which one is active. Backend
// switches take backendMu for writing only for the instant the setting changes;
// reads hold it shared, so no read ever sees a half-migrated state.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// VaultState describes the encrypted vault for the settings UI.
type VaultState struct {
	Exists          bool // vault.json is present
	Unlocked        bool // the key is in memory
	AutoLockMinutes int  // idle minutes before locking; 0 = never
}

// VaultReset lists what ResetVault could not recover.
type VaultReset struct {
	Accounts []string // IDs of accounts whose secret must be entered again
	Refs     []string // secret reference names (SSE-C keys, ZIP passwords) whose value is gone
}

const noKeychainMessage = "No system keychain available — use an encrypted vault"

// keyringUnavailableError carries the OS error text next to the sentinel.
type keyringUnavailableError struct{ cause error }

func (e *keyringUnavailableError) Error() string {
	return noKeychainMessage + ": " + e.cause.Error()
}
func (e *keyringUnavailableError) Unwrap() []error { return []error{ErrKeyringUnavailable, e.cause} }

func keyringDetail(err error) string {
	var ku *keyringUnavailableError
	if errors.As(err, &ku) {
		return ku.cause.Error()
	}
	return ""
}

// keyringUnavailableHints are fragments of the errors go-keyring returns when
// there is no Secret Service / D-Bus session (bare Linux, WSL, containers).
var keyringUnavailableHints = []string{
	"org.freedesktop.secrets", "org.freedesktop.dbus", "secret service", "secretservice",
	"dbus_session_bus_address", "dbus", "d-bus", "unlock correct collection", "no keyring",
}

// guardedKeyring wraps the OS keyring: "there is no keychain here" errors become
// ErrKeyringUnavailable; everything else (including ErrSecretNotFound) passes.
type guardedKeyring struct{ Keyring }

func (g guardedKeyring) Get(key string) (string, error) {
	v, err := g.Keyring.Get(key)
	return v, mapKeyringErr(err)
}
func (g guardedKeyring) Set(key, value string) error { return mapKeyringErr(g.Keyring.Set(key, value)) }
func (g guardedKeyring) Delete(key string) error     { return mapKeyringErr(g.Keyring.Delete(key)) }

func mapKeyringErr(err error) error {
	if err == nil || errors.Is(err, ErrSecretNotFound) || errors.Is(err, ErrKeyringUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := strings.ToLower(err.Error())
	for _, h := range keyringUnavailableHints {
		if strings.Contains(msg, h) {
			return &keyringUnavailableError{cause: err}
		}
	}
	return err
}

// secretStore is the Keyring the Service uses everywhere.
type secretStore struct{ s *Service }

func (k secretStore) Get(key string) (v string, err error) {
	err = k.s.withBackend(func(kr Keyring) error {
		var e error
		v, e = kr.Get(key)
		return e
	})
	return v, err
}

func (k secretStore) Set(key, value string) error {
	return k.s.withBackend(func(kr Keyring) error { return kr.Set(key, value) })
}

func (k secretStore) Delete(key string) error {
	return k.s.withBackend(func(kr Keyring) error { return kr.Delete(key) })
}

// withBackend runs f against the active backend. With the vault active and
// locked it fails at once with ErrLocked (no OS dialog, no waiting); any use of
// the vault counts as activity for the idle timer.
func (s *Service) withBackend(f func(Keyring) error) error {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	if s.SecretBackend() != SecretBackendVault {
		return f(s.osKr)
	}
	s.autoLockIfIdle()
	if !s.vlt.unlocked() {
		return lockedErr()
	}
	s.vaultTouch()
	return f(s.vlt)
}

// ---- queries --------------------------------------------------------------------------

// SecretBackend returns SecretBackendKeyring or SecretBackendVault.
func (s *Service) SecretBackend() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.SecretBackend
}

// VaultState reports the vault file and lock state. It does no keychain access.
func (s *Service) VaultState() VaultState {
	s.autoLockIfIdle()
	s.mu.Lock()
	min := s.settings.VaultAutoLockMinutes
	s.mu.Unlock()
	return VaultState{Exists: s.vlt.exists(), Unlocked: s.vlt.unlocked(), AutoLockMinutes: min}
}

// ---- lock / unlock ----------------------------------------------------------------------

func (s *Service) vaultTouch() { s.vaultLast.Store(s.now().UnixNano()) }

// autoLockIfIdle locks the vault when it has been idle for the configured time.
func (s *Service) autoLockIfIdle() {
	s.mu.Lock()
	min := s.settings.VaultAutoLockMinutes
	s.mu.Unlock()
	if min <= 0 {
		return
	}
	last := s.vaultLast.Load()
	if last == 0 || !s.vlt.unlocked() {
		return
	}
	if s.now().Sub(time.Unix(0, last)) >= time.Duration(min)*time.Minute {
		s.lockVault("idle")
	}
}

// lockVault wipes the key and, when the vault is the active backend, drops the
// cached clients (they hold decrypted credentials) so the next use asks for the
// password. Running tasks keep the clients they already hold.
func (s *Service) lockVault(reason string) {
	if !s.vlt.lock() {
		return
	}
	s.vaultLast.Store(0)
	s.mu.Lock()
	if s.settings.SecretBackend == SecretBackendVault {
		for id := range s.clients {
			delete(s.clients, id)
		}
		for _, r := range s.accounts {
			s.acctEpoch[r.ID]++ // discard builds that read secrets before the lock
		}
	}
	s.mu.Unlock()
	s.lgInfo("vault locked", "reason", reason)
}

// vaultWatch locks an idle vault even when nobody calls the Service.
func (s *Service) vaultWatch() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.vaultStop:
			return
		case <-t.C:
			s.autoLockIfIdle()
		}
	}
}

// UnlockVault opens the vault with the master password. Errors: ErrNotFound
// (no vault file), ErrAccessDenied (wrong password), ErrInvalid (corrupt file;
// it is never modified). Unlocking an already unlocked vault re-checks the password.
func (s *Service) UnlockVault(password string) error {
	const op = "UnlockVault"
	lgAddSecret(password)
	if err := s.vlt.unlock(password); err != nil {
		return classify(op, err)
	}
	s.vaultTouch()
	s.lgInfo("vault unlocked")
	return nil
}

// LockVault wipes the vault key from memory. Secrets needed afterwards fail
// with ErrLocked until UnlockVault succeeds.
func (s *Service) LockVault() { s.lockVault("user") }

// ChangeVaultPassword re-encrypts the vault under a new master password (the
// old one must be correct). The vault is unlocked afterwards.
func (s *Service) ChangeVaultPassword(oldPassword, newPassword string) error {
	const op = "ChangeVaultPassword"
	lgAddSecret(oldPassword)
	lgAddSecret(newPassword)
	if err := s.vlt.changePassword(oldPassword, newPassword); err != nil {
		return classify(op, err)
	}
	s.vaultTouch()
	s.lgInfo("vault password changed")
	return nil
}

// ---- migration ----------------------------------------------------------------------------

// secretKeys lists every secret entry the configuration refers to: keyrings
// cannot be enumerated, so accounts.json (current generation) and
// secret-refs.json are the index.
func (s *Service) secretKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for _, r := range s.accounts {
		if r.HasSecret {
			keys = append(keys, accountSecretKey(r.ID, r.SecretGen))
		}
		if r.HasSession {
			keys = append(keys, accountSessionKey(r.ID, r.SecretGen))
		}
	}
	for _, n := range s.refs {
		keys = append(keys, refKey(n))
	}
	return keys
}

// SetSecretBackend moves every stored secret to the other backend.
//
// Switching to the vault creates it with password (min. 8 characters); if a
// vault file already exists the password must open it. Switching back needs
// the vault unlocked or password to unlock it; the vault file is deleted after
// a successful move.
//
// Order: read all secrets from the current backend; write them to the target;
// read them back from the target and compare; persist the setting; only then
// delete them from the source. Any failure before the setting is persisted
// removes what was written to the target and leaves the current backend
// authoritative and untouched. If only the final cleanup of the source fails,
// the switch stands and the error (kind Other) says what is left behind.
// Secret entries missing from the source (an account that already needs its
// secret re-entered) are skipped. Account and secret-reference writers are
// blocked while this runs.
func (s *Service) SetSecretBackend(ctx context.Context, backend, password string) error {
	const op = "SetSecretBackend"
	if backend != SecretBackendKeyring && backend != SecretBackendVault {
		return invalid(op, "unknown secret backend %q", backend)
	}
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	s.refMu.Lock()
	defer s.refMu.Unlock()

	from := s.SecretBackend()
	if from == backend {
		return nil
	}
	lgAddSecret(password)
	keys := s.secretKeys()

	// Source.
	var src Keyring = s.osKr
	unlockedHere := false
	if from == SecretBackendVault {
		if !s.vlt.unlocked() {
			if password == "" {
				return classify(op, lockedErr())
			}
			if err := s.vlt.unlock(password); err != nil {
				return classify(op, err)
			}
			unlockedHere = true
		}
		src = s.vlt
	}
	failSource := func(err error) error {
		if unlockedHere {
			s.vlt.lock()
		}
		return classify(op, err)
	}
	entries := make(map[string]string, len(keys))
	missing := 0
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return failSource(err)
		}
		v, err := keyringGet(ctx, src, k)
		if errors.Is(err, ErrSecretNotFound) {
			missing++
			continue
		}
		if err != nil {
			return failSource(fmt.Errorf("read secrets from the current backend: %w", err))
		}
		entries[k] = v
	}
	written := make([]string, 0, len(entries))
	for k := range entries {
		written = append(written, k)
	}

	// Target.
	var dst Keyring = s.osKr
	createdVault := false
	rollback := func() {
		switch {
		case backend == SecretBackendKeyring:
			for _, k := range written {
				s.osKr.Delete(k)
			}
		case createdVault:
			s.vlt.destroy()
		default:
			s.vlt.deleteMany(written)
		}
	}
	fail := func(err error) error {
		rollback()
		if unlockedHere {
			s.vlt.lock()
		}
		return classify(op, err)
	}
	if backend == SecretBackendVault {
		switch {
		case !s.vlt.exists():
			if err := s.vlt.create(password); err != nil {
				return failSource(err)
			}
			createdVault = true
		case password != "" || !s.vlt.unlocked():
			if err := s.vlt.unlock(password); err != nil {
				return failSource(err)
			}
		}
		dst = s.vlt
		if err := s.vlt.setMany(entries); err != nil {
			return fail(fmt.Errorf("write secrets to the vault: %w", err))
		}
		if err := s.vlt.verify(entries); err != nil {
			return fail(fmt.Errorf("verify the vault: %w", err))
		}
	} else {
		for k, v := range entries {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if err := dst.Set(k, v); err != nil {
				return fail(fmt.Errorf("write secrets to the system keychain: %w", err))
			}
		}
		for k, v := range entries {
			got, err := dst.Get(k)
			if err != nil || got != v {
				return fail(errors.New("verify the system keychain: a stored secret could not be read back"))
			}
		}
	}

	// Commit: persist the setting while no reader is inside a backend call.
	s.wmu.Lock()
	s.mu.Lock()
	st := s.settings
	s.mu.Unlock()
	st.SecretBackend = backend
	s.backendMu.Lock()
	err := writeJSONAtomic(s.path("settings.json"), st)
	if err == nil {
		s.mu.Lock()
		s.settings = st
		s.mu.Unlock()
	}
	s.backendMu.Unlock()
	s.wmu.Unlock()
	if err != nil {
		return fail(err)
	}
	s.lgInfo("secret backend switched", "from", from, "to", backend, "entries", len(entries), "missing", missing)
	if backend == SecretBackendVault {
		s.vaultTouch()
	}

	// Cleanup of the source; the switch itself is done.
	if from == SecretBackendVault {
		if err := s.vlt.destroy(); err != nil {
			return opErr(op, ErrOther, "secrets were moved to the system keychain, but vault.json could not be removed: %v", err)
		}
		return nil
	}
	var errs []error
	for _, k := range keys {
		if err := s.osKr.Delete(k); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return opErr(op, ErrOther, "secrets were moved to the vault, but %d old entries could not be removed from the system keychain: %v", len(errs), errors.Join(errs...))
	}
	return nil
}

// ResetVault is the "forgot the master password" path: it deletes vault.json
// and, if the vault was the active backend, switches back to the system
// keychain and marks every static account that had a stored secret as missing
// (Account.SecretMissing) so the user enters it again; secret references stay
// listed but their values are gone.
//
// This destroys data. The caller MUST have shown the consequences and obtained
// explicit confirmation; the Service does not ask. Without a vault file this
// is a no-op for the secrets.
func (s *Service) ResetVault() (VaultReset, error) {
	const op = "ResetVault"
	var res VaultReset
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	s.refMu.Lock()
	defer s.refMu.Unlock()

	if s.SecretBackend() == SecretBackendVault {
		s.mu.Lock()
		next := make([]accountRecord, len(s.accounts))
		copy(next, s.accounts)
		res.Refs = append([]string(nil), s.refs...)
		s.mu.Unlock()
		for i := range next {
			if next[i].HasSecret || next[i].HasSession {
				res.Accounts = append(res.Accounts, next[i].ID)
			}
			next[i].HasSecret, next[i].HasSession = false, false
		}
		if err := writeJSONAtomic(s.path("accounts.json"), next); err != nil {
			return VaultReset{}, classify(op, err)
		}
		s.mu.Lock()
		s.accounts = next
		for id := range s.clients {
			delete(s.clients, id)
		}
		for _, r := range s.accounts {
			s.acctEpoch[r.ID]++
		}
		s.mu.Unlock()

		s.wmu.Lock()
		s.mu.Lock()
		st := s.settings
		s.mu.Unlock()
		st.SecretBackend = SecretBackendKeyring
		s.backendMu.Lock()
		err := writeJSONAtomic(s.path("settings.json"), st)
		if err == nil {
			s.mu.Lock()
			s.settings = st
			s.mu.Unlock()
		}
		s.backendMu.Unlock()
		s.wmu.Unlock()
		if err != nil {
			return VaultReset{}, classify(op, err)
		}
	}
	if err := s.vlt.destroy(); err != nil {
		return res, classify(op, fmt.Errorf("vault.json could not be removed: %w", err))
	}
	s.vaultLast.Store(0)
	s.lgInfo("vault reset", "accounts", len(res.Accounts), "refs", len(res.Refs))
	return res, nil
}
