package app

import (
	"os"
	"path/filepath"
	"testing"
)

// When SetSecretBackend unlocks an existing vault and then fails, the vault is
// locked again: the key must not stay in memory with no idle timer running.
func TestSetSecretBackendLocksTheVaultItUnlockedWhenItFails(t *testing.T) {
	dir := t.TempDir()
	s := newClockService(t, dir, newTestKeyring(), newFakeClock())
	if err := s.SetSecret("ref", "a secret value"); err != nil {
		t.Fatal(err)
	}
	// A vault file from an earlier life, locked.
	if err := s.vlt.create(testPW); err != nil {
		t.Fatal(err)
	}
	s.vlt.lock()
	if s.VaultState().Unlocked {
		t.Fatal("setup: the vault is unlocked")
	}
	// Persisting the setting fails: settings.json cannot be replaced.
	p := filepath.Join(dir, "settings.json")
	os.Remove(p)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err == nil {
		t.Fatal("the switch succeeded although settings.json cannot be written")
	}
	if s.SecretBackend() != SecretBackendKeyring {
		t.Fatalf("backend %s", s.SecretBackend())
	}
	if s.VaultState().Unlocked {
		t.Fatal("the vault the failed switch unlocked is still unlocked")
	}
}
