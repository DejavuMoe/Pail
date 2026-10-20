package app

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// testKeyring wraps a Keyring with fault and blocking hooks and call logs.
type testKeyring struct {
	Keyring
	mu     sync.Mutex
	onGet  func(key string) error // may block
	onSet  func(key string) error
	onDel  func(key string) error
	gets   []string
	sets   []string
	dels   []string
	getCnt map[string]int
}

func newTestKeyring() *testKeyring {
	return &testKeyring{Keyring: NewMemoryKeyring(), getCnt: map[string]int{}}
}

func (k *testKeyring) Get(key string) (string, error) {
	k.mu.Lock()
	k.gets = append(k.gets, key)
	k.getCnt[key]++
	hook := k.onGet
	k.mu.Unlock()
	if hook != nil {
		if err := hook(key); err != nil {
			return "", err
		}
	}
	return k.Keyring.Get(key)
}

func (k *testKeyring) Set(key, value string) error {
	k.mu.Lock()
	k.sets = append(k.sets, key)
	hook := k.onSet
	k.mu.Unlock()
	if hook != nil {
		if err := hook(key); err != nil {
			return err
		}
	}
	return k.Keyring.Set(key, value)
}

func (k *testKeyring) Delete(key string) error {
	k.mu.Lock()
	k.dels = append(k.dels, key)
	hook := k.onDel
	k.mu.Unlock()
	if hook != nil {
		if err := hook(key); err != nil {
			return err
		}
	}
	return k.Keyring.Delete(key)
}

func (k *testKeyring) has(key string) bool {
	_, err := k.Keyring.Get(key)
	return err == nil
}

func (k *testKeyring) getCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.getCnt {
		n += c
	}
	return n
}

func TestMemoryKeyring(t *testing.T) {
	k := NewMemoryKeyring()
	if _, err := k.Get("a"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	k.Set("a", "1")
	if v, _ := k.Get("a"); v != "1" {
		t.Fatal("Get after Set")
	}
	k.Delete("a")
	k.Delete("a") // deleting twice is fine
	if _, err := k.Get("a"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("still there")
	}
}

func TestSecretRefsLifecycle(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	if err := s.SetSecret("zip-pass", "pw1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret("sse-c", "k"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret("zip-pass", "pw2"); err != nil { // replace
		t.Fatal(err)
	}
	if got := s.SecretRefs(); !reflect.DeepEqual(got, []string{"sse-c", "zip-pass"}) {
		t.Fatalf("refs %v", got)
	}
	if v, _ := kr.Get("ref/zip-pass"); v != "pw2" {
		t.Fatalf("value %q", v)
	}
	// Names persist, values never touch the files.
	b, _ := os.ReadFile(s.path(secretRefsFile))
	if strings.Contains(string(b), "pw2") || !strings.Contains(string(b), "zip-pass") {
		t.Fatalf("refs file: %s", b)
	}
	s2, _ := newTestServiceDir(t, dir, kr)
	if got := s2.SecretRefs(); !reflect.DeepEqual(got, []string{"sse-c", "zip-pass"}) {
		t.Fatalf("reloaded refs %v", got)
	}
	// Resolver (used by ZIP rules) reads the keyring.
	if v, err := s2.ResolvePassword(testCtx(t), "zip-pass"); err != nil || v != "pw2" {
		t.Fatalf("resolve: %q %v", v, err)
	}
	if _, err := s2.ResolvePassword(testCtx(t), "missing"); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing ref: %v", err)
	}
	if err := s2.DeleteSecret("zip-pass"); err != nil {
		t.Fatal(err)
	}
	if err := s2.DeleteSecret("never-existed"); err != nil {
		t.Fatal(err)
	}
	if got := s2.SecretRefs(); !reflect.DeepEqual(got, []string{"sse-c"}) || kr.has("ref/zip-pass") {
		t.Fatalf("after delete: %v", got)
	}
}

func TestSetSecretValidationAndRollback(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	for _, bad := range []struct{ ref, val string }{{"", "x"}, {"a\nb", "x"}, {"ok", ""}} {
		if err := s.SetSecret(bad.ref, bad.val); !IsKind(err, ErrInvalid) {
			t.Errorf("%q/%q: %v", bad.ref, bad.val, err)
		}
	}
	// Keyring failure: nothing recorded.
	kr.onSet = func(string) error { return errors.New("keyring locked") }
	if err := s.SetSecret("r", "v"); err == nil || len(s.SecretRefs()) != 0 {
		t.Fatalf("err=%v refs=%v", err, s.SecretRefs())
	}
	kr.onSet = nil
	// Persisting the list fails: keyring entry is rolled back (new) or restored (old).
	if err := s.SetSecret("r", "old"); err != nil {
		t.Fatal(err)
	}
	os.Remove(s.path(secretRefsFile))
	if err := os.Mkdir(s.path(secretRefsFile), 0o700); err != nil { // makes the atomic rename fail
		t.Fatal(err)
	}
	if err := s.SetSecret("brand-new", "v"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if kr.has("ref/brand-new") {
		t.Fatal("new keyring entry not rolled back")
	}
	// An entry that exists in the keyring but not in the list (e.g. left over)
	// gets its previous value back when persisting the list fails.
	kr.Keyring.Set("ref/ghost", "old")
	if err := s.SetSecret("ghost", "newer"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if v, _ := kr.Get("ref/ghost"); v != "old" {
		t.Fatalf("old value not restored: %q", v)
	}
}
