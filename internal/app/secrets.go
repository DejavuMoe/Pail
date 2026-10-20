package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// keyringService is the service name under which every entry is stored.
const keyringService = "Pail"

// legacyKeyringService is the service name used before the rename to Pail; entries are migrated away from it (keyringmigrate.go).
const legacyKeyringService = "openS3"

// ErrSecretNotFound is returned by Keyring.Get when the entry does not exist.
var ErrSecretNotFound = errors.New("secret not found")

// osKeyring adapts github.com/zalando/go-keyring (Windows Credential Manager,
// macOS Keychain, Secret Service) to Keyring.
type osKeyring struct{ service string }

func (k osKeyring) Get(key string) (string, error) {
	v, err := keyring.Get(k.service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	return v, err
}

func (k osKeyring) Set(key, value string) error { return keyring.Set(k.service, key, value) }

func (k osKeyring) Delete(key string) error {
	err := keyring.Delete(k.service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil // deleting something already gone is success
	}
	return err
}

// memoryKeyring is an in-process Keyring for tests.
type memoryKeyring struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemoryKeyring returns an in-memory Keyring. It exists for tests of this
// and other packages; secrets are not persisted anywhere.
func NewMemoryKeyring() Keyring { return &memoryKeyring{m: map[string]string{}} }

func (k *memoryKeyring) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (k *memoryKeyring) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

func (k *memoryKeyring) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

// Keyring entry keys.

// accountSecretKey names the secret access key entry. Generation 0 uses the
// documented name; every later SaveAccount that replaces secrets writes under
// a fresh generation so that the previous value stays intact until the new
// account record is safely persisted.
func accountSecretKey(id string, gen int) string  { return accountKey(id, "secret", gen) }
func accountSessionKey(id string, gen int) string { return accountKey(id, "session", gen) }

func accountKey(id, what string, gen int) string {
	if gen == 0 {
		return "account/" + id + "/" + what
	}
	return fmt.Sprintf("account/%s/%s.%d", id, what, gen)
}

func refKey(name string) string { return "ref/" + name }

// validRefName accepts the names used for SSE-C keys and ZIP passwords.
func validRefName(name string) error {
	if name == "" || len(name) > 200 {
		return errors.New("secret reference must be 1-200 characters")
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("secret reference contains control characters")
	}
	return nil
}

// secretRefsFile persists the list of reference names: OS keyrings cannot be
// enumerated, and the UI needs to offer the existing references.
const secretRefsFile = "secret-refs.json"

// SetSecret stores value under ref (an SSE-C key or a ZIP password) in the
// keyring and records the name in the reference list.
func (s *Service) SetSecret(ref, value string) error {
	const op = "SetSecret"
	if err := validRefName(ref); err != nil {
		return invalid(op, "%v", err)
	}
	if value == "" {
		return invalid(op, "secret value must not be empty")
	}
	s.refMu.Lock()
	defer s.refMu.Unlock()
	old, oldErr := s.kr.Get(refKey(ref))
	hadOld := oldErr == nil
	if err := s.kr.Set(refKey(ref), value); err != nil {
		return classify(op, err)
	}
	if err := s.addRefLocked(ref); err != nil {
		// Roll the keyring back so that it and the list agree.
		if hadOld {
			s.kr.Set(refKey(ref), old)
		} else {
			s.kr.Delete(refKey(ref))
		}
		return classify(op, err)
	}
	return nil
}

// DeleteSecret removes the keyring entry and the reference name. Deleting an
// unknown reference is not an error.
func (s *Service) DeleteSecret(ref string) error {
	const op = "DeleteSecret"
	if err := validRefName(ref); err != nil {
		return invalid(op, "%v", err)
	}
	s.refMu.Lock()
	defer s.refMu.Unlock()
	if err := s.kr.Delete(refKey(ref)); err != nil {
		return classify(op, err)
	}
	return classify(op, s.removeRefLocked(ref))
}

// SecretRefs lists the known reference names, sorted.
func (s *Service) SecretRefs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.refs...)
}

// ResolvePassword implements rules.PasswordResolver on top of the keyring, so
// ZIP rules can resolve their password reference at task time. It reads the
// keyring outside every lock.
func (s *Service) ResolvePassword(ctx context.Context, ref string) (string, error) {
	return s.secretValue(ctx, ref)
}

// secretValue reads ref from the keyring (honouring ctx while the OS prompts).
func (s *Service) secretValue(ctx context.Context, ref string) (string, error) {
	if err := validRefName(ref); err != nil {
		return "", invalid("Secret", "%v", err)
	}
	v, err := keyringGet(ctx, s.kr, refKey(ref))
	if errors.Is(err, ErrSecretNotFound) {
		return "", opErr("Secret", ErrNotFound, "secret reference %q does not exist", ref)
	}
	return v, classify("Secret", err)
}

// keyringGet runs kr.Get but returns early when ctx is done. The abandoned
// goroutine finishes whenever the keyring does (OS dialogs cannot be aborted).
func keyringGet(ctx context.Context, kr Keyring, key string) (string, error) {
	type res struct {
		v   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := kr.Get(key)
		if err == nil {
			lgAddSecret(v) // keyring values are always secret: never log them verbatim
		}
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// addRefLocked / removeRefLocked update the reference list and persist it.
// Callers hold refMu (not mu); the file write happens outside mu.
func (s *Service) addRefLocked(ref string) error {
	s.mu.Lock()
	refs := append([]string(nil), s.refs...)
	s.mu.Unlock()
	for _, r := range refs {
		if r == ref {
			return nil
		}
	}
	refs = append(refs, ref)
	sort.Strings(refs)
	return s.commitRefs(refs)
}

func (s *Service) removeRefLocked(ref string) error {
	s.mu.Lock()
	refs := append([]string(nil), s.refs...)
	s.mu.Unlock()
	out := refs[:0]
	found := false
	for _, r := range refs {
		if r == ref {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return nil
	}
	return s.commitRefs(out)
}

func (s *Service) commitRefs(refs []string) error {
	if err := writeJSONAtomic(s.path(secretRefsFile), refs); err != nil {
		return err
	}
	s.mu.Lock()
	s.refs = refs
	s.mu.Unlock()
	return nil
}
