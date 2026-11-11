package app

import (
	"bytes"
	"os"
	"testing"
)

// A second process (here: a second vault instance on the same file) adds an
// entry; this instance must see it instead of answering from its stale copy.
func TestN2VaultGetSeesOtherWriters(t *testing.T) {
	a := newTestVault(t)
	if err := a.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("old", "1"); err != nil {
		t.Fatal(err)
	}
	b := newVault(a.path)
	if err := b.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	if err := b.Set("new", "2"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("old"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Get("new"); err != nil || got != "2" {
		t.Fatalf("entry written by the other instance: %q, %v", got, err)
	}
	if _, err := a.Get("old"); err != ErrSecretNotFound {
		t.Fatalf("entry deleted by the other instance is still returned: %v", err)
	}
}

// After another process changed the password, an instance still holding the
// old key fails clearly on reads and writes and leaves the file alone.
func TestN2VaultStaleKeyAfterPasswordChange(t *testing.T) {
	a := newTestVault(t)
	if err := a.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := a.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	b := newVault(a.path)
	if err := b.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	if err := b.changePassword(testPW, n2NewPW); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(a.path)
	if err := a.Set("k2", "v2"); !IsKind(err, ErrConflict) {
		t.Fatalf("write with the old key: want Conflict, got %v", err)
	}
	if err := a.Delete("k"); !IsKind(err, ErrConflict) {
		t.Fatalf("delete with the old key: want Conflict, got %v", err)
	}
	if _, err := a.Get("k"); !IsKind(err, ErrConflict) {
		t.Fatalf("read with the old key: want Conflict, got %v", err)
	}
	if err := a.changePassword(testPW, "yet another password"); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("password change with the old password: want AccessDenied, got %v", err)
	}
	if after, _ := os.ReadFile(a.path); !bytes.Equal(before, after) {
		t.Fatal("a stale instance modified vault.json")
	}
	// Locking and unlocking with the new password recovers.
	a.lock()
	if err := a.unlock(n2NewPW); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Get("k"); err != nil || got != "v" {
		t.Fatalf("after unlocking again: %q, %v", got, err)
	}
	if err := a.Set("k2", "v2"); err != nil {
		t.Fatal(err)
	}
}

// create must not replace a vault another process created in the meantime.
func TestN2VaultCreateRefusesExistingFile(t *testing.T) {
	a := newTestVault(t)
	b := newVault(a.path)
	if err := a.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := b.create(n2NewPW); !IsKind(err, ErrConflict) {
		t.Fatalf("second create: want Conflict, got %v", err)
	}
	if err := b.unlock(testPW); err != nil {
		t.Fatalf("the first vault was replaced: %v", err)
	}
}
