package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func init() {
	// Fast Argon2 (the minimum the format accepts) for every test in the
	// package; TestVaultProductionDefaults pins the real values.
	vaultNewKDF = vaultKDFParams{Time: 1, MemoryKiB: vaultMinMemKiB, Threads: 1}
}

const testPW = "correct horse battery"

func newTestVault(t *testing.T) *vault {
	t.Helper()
	return newVault(filepath.Join(t.TempDir(), vaultFileName))
}

func readVaultFile(t *testing.T, v *vault) (vaultFile, []byte) {
	t.Helper()
	raw, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	var f vaultFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f, raw
}

func rewriteVaultFile(t *testing.T, v *vault, edit func(m map[string]any)) {
	t.Helper()
	_, raw := readVaultFile(t, v)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(v.path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVaultProductionDefaults(t *testing.T) {
	if defaultVaultKDF != (vaultKDFParams{Time: 3, MemoryKiB: 65536, Threads: 4}) {
		t.Fatalf("production KDF parameters changed: %+v", defaultVaultKDF)
	}
	if vaultMinPassword != 8 || vaultMaxPassword != 1024 {
		t.Fatal("password policy changed")
	}
	if vaultMinMemKiB != 8*1024 || vaultMaxMemKiB != 1024*1024 || vaultMaxTime != 10 || vaultMaxThreads != 16 {
		t.Fatal("KDF bounds changed")
	}
	if got := DefaultSettings(); got.VaultAutoLockMinutes != 15 || got.SecretBackend != SecretBackendKeyring {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestVaultRoundTripAndFormat(t *testing.T) {
	v := newTestVault(t)
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("account/a/secret", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("ref/zip", "päss wörd"); err != nil {
		t.Fatal(err)
	}
	f, raw := readVaultFile(t, v)
	if f.Format != "pail-vault" || f.Version != 1 || f.Cipher != "xchacha20poly1305" || f.KDF.Name != "argon2id" {
		t.Fatalf("header: %+v", f.vaultHeader)
	}
	if bytes.Contains(raw, []byte("s3cr3t-value")) || bytes.Contains(raw, []byte("account/a/secret")) {
		t.Fatal("plaintext in vault.json")
	}
	if err := validateVaultFile(f); err != nil {
		t.Fatal(err)
	}
	if !v.lock() || v.lock() {
		t.Fatal("lock must report the transition once")
	}
	if _, err := v.Get("ref/zip"); !errors.Is(err, ErrVaultLocked) || KindOf(err) != ErrLocked {
		t.Fatalf("locked Get: %v", err)
	}
	if err := v.Set("x", "y"); KindOf(err) != ErrLocked {
		t.Fatalf("locked Set: %v", err)
	}
	// A fresh process (new object) opens the same file.
	v2 := newVault(v.path)
	if err := v2.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	if got, err := v2.Get("ref/zip"); err != nil || got != "päss wörd" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := v2.Get("nope"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := v2.Delete("ref/zip"); err != nil {
		t.Fatal(err)
	}
	if err := v2.Delete("ref/zip"); err != nil {
		t.Fatal("deleting an absent entry must succeed")
	}
	if _, err := v2.Get("ref/zip"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("still there")
	}
}

func TestVaultNonceFreshOnEveryWrite(t *testing.T) {
	v := newTestVault(t)
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	f0, _ := readVaultFile(t, v)
	seen[f0.Nonce] = true
	for i := 0; i < 5; i++ {
		if err := v.Set("k", "v"); err != nil { // same content every time
			t.Fatal(err)
		}
		f, _ := readVaultFile(t, v)
		if seen[f.Nonce] {
			t.Fatalf("nonce reused on write %d", i)
		}
		seen[f.Nonce] = true
	}
}

func TestVaultFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	v := newTestVault(t)
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	v.Set("k", "v")
	fi, err := os.Stat(v.path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
}

func TestVaultWrongPassword(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("k", "v")
	v.lock()
	_, before := readVaultFile(t, v)
	err := v.unlock("not the password")
	if KindOf(err) != ErrAccessDenied || !strings.Contains(err.Error(), "wrong master password") {
		t.Fatalf("got %v", err)
	}
	if v.unlocked() {
		t.Fatal("unlocked with the wrong password")
	}
	if err := v.unlock(""); KindOf(err) != ErrInvalid {
		t.Fatalf("empty password: %v", err)
	}
	if _, after := readVaultFile(t, v); !bytes.Equal(before, after) {
		t.Fatal("a failed unlock changed the file")
	}
}

func TestVaultTamperingIsRejected(t *testing.T) {
	flip := func(s string) string {
		b := []byte(s)
		b[len(b)/2] ^= 1
		out := string(b)
		if out == s || strings.ContainsAny(out, "\x00") {
			t.Fatal("flip failed")
		}
		return out
	}
	cases := map[string]func(m map[string]any){
		"data":       func(m map[string]any) { m["data"] = flip(m["data"].(string)) },
		"nonce":      func(m map[string]any) { m["nonce"] = flipB64(m["nonce"].(string)) },
		"kdf.time":   func(m map[string]any) { m["kdf"].(map[string]any)["time"] = float64(2) },
		"kdf.memory": func(m map[string]any) { m["kdf"].(map[string]any)["memoryKiB"] = float64(vaultMinMemKiB * 2) },
		"kdf.thread": func(m map[string]any) { m["kdf"].(map[string]any)["threads"] = float64(2) },
		"kdf.salt": func(m map[string]any) {
			m["kdf"].(map[string]any)["salt"] = flipB64(m["kdf"].(map[string]any)["salt"].(string))
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			v := newTestVault(t)
			v.create(testPW)
			v.Set("k", "v")
			v.lock()
			rewriteVaultFile(t, v, edit)
			_, before := readVaultFile(t, v)
			err := v.unlock(testPW)
			if err == nil || v.unlocked() {
				t.Fatalf("tampered %s accepted", name)
			}
			if k := KindOf(err); k != ErrAccessDenied && k != ErrInvalid {
				t.Fatalf("kind %v: %v", k, err)
			}
			if _, after := readVaultFile(t, v); !bytes.Equal(before, after) {
				t.Fatal("file modified by a failed unlock")
			}
		})
	}
}

// flipB64 changes the decoded bytes of a base64 string (keeping it valid).
func flipB64(s string) string {
	raw, _ := b64.DecodeString(s)
	raw[0] ^= 0x80
	return b64.EncodeToString(raw)
}

func TestVaultKDFBoundsRejectedBeforeDerivation(t *testing.T) {
	cases := map[string]func(k map[string]any){
		"memory too large": func(k map[string]any) { k["memoryKiB"] = float64(4 << 20) }, // 4 GiB
		"memory huge":      func(k map[string]any) { k["memoryKiB"] = float64(1<<32 - 1) },
		"memory too small": func(k map[string]any) { k["memoryKiB"] = float64(vaultMinMemKiB - 1) },
		"time zero":        func(k map[string]any) { k["time"] = float64(0) },
		"time too large":   func(k map[string]any) { k["time"] = float64(vaultMaxTime + 1) },
		"threads zero":     func(k map[string]any) { k["threads"] = float64(0) },
		"threads too many": func(k map[string]any) { k["threads"] = float64(vaultMaxThreads + 1) },
		"kdf name":         func(k map[string]any) { k["name"] = "scrypt" },
		"short salt":       func(k map[string]any) { k["salt"] = b64.EncodeToString([]byte("short")) },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			v := newTestVault(t)
			v.create(testPW)
			v.lock()
			rewriteVaultFile(t, v, func(m map[string]any) { edit(m["kdf"].(map[string]any)) })
			start := time.Now()
			err := v.unlock(testPW)
			if KindOf(err) != ErrInvalid {
				t.Fatalf("want Invalid, got %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("rejection must not run the KDF")
			}
		})
	}
	for name, edit := range map[string]func(m map[string]any){
		"format":  func(m map[string]any) { m["format"] = "other" },
		"version": func(m map[string]any) { m["version"] = float64(2) },
		"cipher":  func(m map[string]any) { m["cipher"] = "aes" },
		"nonce":   func(m map[string]any) { m["nonce"] = "AAAA" },
		"data":    func(m map[string]any) { m["data"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			v := newTestVault(t)
			v.create(testPW)
			v.lock()
			rewriteVaultFile(t, v, edit)
			if err := v.unlock(testPW); KindOf(err) != ErrInvalid {
				t.Fatalf("want Invalid, got %v", err)
			}
		})
	}
}

func TestVaultCorruptFileIsNeverOverwritten(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("k", "v")
	garbage := []byte("{not json at all")
	if err := os.WriteFile(v.path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	// Unlocked (held key) writes must refuse rather than replace the file.
	if err := v.Set("k2", "v2"); err == nil {
		t.Fatal("write over a corrupt file succeeded")
	}
	v.lock()
	if err := v.unlock(testPW); KindOf(err) != ErrInvalid {
		t.Fatalf("want Invalid, got %v", err)
	}
	if err := v.create(testPW); KindOf(err) != ErrConflict {
		t.Fatalf("create over existing file: %v", err)
	}
	if got, _ := os.ReadFile(v.path); !bytes.Equal(got, garbage) {
		t.Fatal("corrupt vault.json was modified")
	}
	// Valid JSON, valid header, wrong content: still not overwritten.
	v2 := newTestVault(t)
	v2.create(testPW)
	v2.Set("k", "v")
	_, good := readVaultFile(t, v2)
	var m map[string]any
	json.Unmarshal(good, &m)
	m["data"] = flipB64(m["data"].(string))
	bad, _ := json.Marshal(m)
	os.WriteFile(v2.path, bad, 0o600)
	if err := v2.Set("z", "z"); err == nil {
		t.Fatal("write over a tampered file succeeded")
	}
	if got, _ := os.ReadFile(v2.path); !bytes.Equal(got, bad) {
		t.Fatal("tampered vault.json was modified")
	}
}

func TestVaultWriteRefusesWhenFileReplacedByAnotherVault(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	other := newVault(v.path + ".other")
	other.create("another password")
	b, _ := os.ReadFile(other.path)
	os.WriteFile(v.path, b, 0o600) // someone changed the password / replaced the vault
	if err := v.Set("k", "v"); KindOf(err) != ErrConflict {
		t.Fatalf("want Conflict, got %v", err)
	}
	if got, _ := os.ReadFile(v.path); !bytes.Equal(got, b) {
		t.Fatal("foreign vault overwritten")
	}
}

func TestVaultMergesConcurrentExternalWrite(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("a", "1")
	v2 := newVault(v.path) // a second process with the same password
	if err := v2.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	v2.Set("b", "2")
	if err := v.Set("c", "3"); err != nil { // must not drop "b"
		t.Fatal(err)
	}
	v.lock()
	v.unlock(testPW)
	for k, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got, err := v.Get(k); err != nil || got != want {
			t.Fatalf("%s = %q, %v", k, got, err)
		}
	}
}

func TestVaultPasswordPolicy(t *testing.T) {
	v := newTestVault(t)
	for _, bad := range []string{"", "short", "1234567", strings.Repeat("x", 1025)} {
		if err := v.create(bad); KindOf(err) != ErrInvalid {
			t.Errorf("%d bytes: %v", len(bad), err)
		}
	}
	if v.exists() {
		t.Fatal("a vault was created with a bad password")
	}
	// Eight characters, not eight bytes; the limit is on bytes.
	if err := v.create("密码密码密码密码"); err != nil {
		t.Fatalf("8 CJK characters rejected: %v", err)
	}
	v2 := newTestVault(t)
	if err := v2.create(strings.Repeat("x", 1024)); err != nil {
		t.Fatalf("1024 bytes rejected: %v", err)
	}
}

func TestVaultChangePassword(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("k", "v")
	f0, _ := readVaultFile(t, v)
	if err := v.changePassword("wrong password!", "new password 123"); KindOf(err) != ErrAccessDenied {
		t.Fatalf("wrong old: %v", err)
	}
	if err := v.changePassword(testPW, "short"); KindOf(err) != ErrInvalid {
		t.Fatalf("weak new: %v", err)
	}
	if err := v.changePassword(testPW, "new password 123"); err != nil {
		t.Fatal(err)
	}
	f1, _ := readVaultFile(t, v)
	if f1.KDF.Salt == f0.KDF.Salt {
		t.Fatal("salt must change with the password")
	}
	v.lock()
	if err := v.unlock(testPW); KindOf(err) != ErrAccessDenied {
		t.Fatalf("old password still works: %v", err)
	}
	if err := v.unlock("new password 123"); err != nil {
		t.Fatal(err)
	}
	if got, _ := v.Get("k"); got != "v" {
		t.Fatalf("content lost: %q", got)
	}
}

func TestVaultLockWipesKey(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("k", "value")
	key, data := v.key, v.data["k"]
	v.lock()
	if v.key != nil || v.data != nil {
		t.Fatal("state kept")
	}
	for _, b := range key {
		if b != 0 {
			t.Fatal("key not zeroed")
		}
	}
	for _, b := range data {
		if b != 0 {
			t.Fatal("value not zeroed")
		}
	}
}

func TestVaultConcurrentReadsDuringLock(t *testing.T) {
	v := newTestVault(t)
	v.create(testPW)
	v.Set("k", "v")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := v.Get("k")
				switch {
				case err == nil && got == "v":
				case errors.Is(err, ErrVaultLocked):
				default:
					bad.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 15; i++ {
		v.lock()
		if err := v.unlock(testPW); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d reads returned a wrong value or error", bad.Load())
	}
}

// ---- Service level -----------------------------------------------------------------

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClockService(t *testing.T, dir string, kr Keyring, clk *fakeClock) *Service {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	opts := Options{ConfigDir: dir, Keyring: kr}
	if clk != nil {
		opts.Now = clk.Now
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})
	return s
}

// seedSecrets saves two static accounts (one with a session token, one
// re-saved so its secrets live in generation 1), a profile account and two refs.
func seedSecrets(t *testing.T, s *Service, endpoint string) (a1, a2 Account) {
	t.Helper()
	a1, err := s.SaveAccount(staticAccount("one", endpoint), &Secrets{SecretAccessKey: "secret-one-0", SessionToken: "token-one-0"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err = s.SaveAccount(staticAccount("two", endpoint), &Secrets{SecretAccessKey: "secret-two-0"})
	if err != nil {
		t.Fatal(err)
	}
	if a2, err = s.SaveAccount(a2, &Secrets{SecretAccessKey: "secret-two-1"}); err != nil { // generation 1
		t.Fatal(err)
	}
	if _, err := s.SaveAccount(Account{Name: "prof", Credentials: CredProfile, Profile: "dev", Region: "us-east-1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret("sse-key", "sse-key-value"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret("zip-pw", "zip-pw-value"); err != nil {
		t.Fatal(err)
	}
	return a1, a2
}

// wantSecrets are the current-generation entries seedSecrets leaves behind.
func wantSecrets(a1, a2 Account) map[string]string {
	return map[string]string{
		"account/" + a1.ID + "/secret":   "secret-one-0",
		"account/" + a1.ID + "/session":  "token-one-0",
		"account/" + a2.ID + "/secret.1": "secret-two-1",
		"ref/sse-key":                    "sse-key-value",
		"ref/zip-pw":                     "zip-pw-value",
	}
}

func checkKeyring(t *testing.T, kr Keyring, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got, err := kr.Get(k); err != nil || got != v {
			t.Errorf("%s = %q, %v; want %q", k, got, err, v)
		}
	}
}

func checkAbsent(t *testing.T, kr Keyring, keys map[string]string) {
	t.Helper()
	for k := range keys {
		if _, err := kr.Get(k); !errors.Is(err, ErrSecretNotFound) {
			t.Errorf("%s still present (%v)", k, err)
		}
	}
}

func TestMigrationKeyringToVaultAndBack(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, srv.URL)
	want := wantSecrets(a1, a2)
	// Stale older generations must not be migrated (they are not current).
	kr.Keyring.Set("account/"+a2.ID+"/secret", "stale")

	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, "short"); !IsKind(err, ErrInvalid) {
		t.Fatalf("weak password: %v", err)
	}
	if s.SecretBackend() != SecretBackendKeyring || s.VaultState().Exists {
		t.Fatal("a failed switch left state behind")
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	if st := s.VaultState(); s.SecretBackend() != SecretBackendVault || !st.Exists || !st.Unlocked || st.AutoLockMinutes != 15 {
		t.Fatalf("state %+v backend %s", st, s.SecretBackend())
	}
	checkAbsent(t, kr.Keyring, want) // moved, not copied
	raw, _ := os.ReadFile(filepath.Join(dir, vaultFileName))
	for _, v := range want {
		if bytes.Contains(raw, []byte(v)) {
			t.Fatalf("plaintext %q in vault.json", v)
		}
	}
	st, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(st), `"secretBackend": "vault"`) {
		t.Fatalf("setting not persisted: %s", st)
	}
	// Works unchanged through the vault.
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatalf("Buckets via vault: %v", err)
	}
	if v, err := s.ResolvePassword(testCtx(t), "zip-pw"); err != nil || v != "zip-pw-value" {
		t.Fatalf("ResolvePassword: %q %v", v, err)
	}
	// New secrets go to the vault, not the keyring.
	if err := s.SetSecret("later", "later-value-1"); err != nil {
		t.Fatal(err)
	}
	if kr.has("ref/later") {
		t.Fatal("SetSecret wrote to the keyring while the vault is active")
	}
	want["ref/later"] = "later-value-1"

	// A restart starts locked, without touching the keyring.
	s.Shutdown(testCtx(t))
	gets := kr.getCount()
	s2 := newClockService(t, dir, kr, nil)
	if s2.SecretBackend() != SecretBackendVault || s2.VaultState().Unlocked {
		t.Fatal("a vault backend must start locked")
	}
	if _, err := s2.Buckets(testCtx(t), a1.ID); !IsKind(err, ErrLocked) {
		t.Fatalf("locked Buckets: %v", err)
	}
	if kr.getCount() != gets {
		t.Fatal("locked vault fell through to the OS keyring")
	}
	if err := s2.UnlockVault("wrong password!"); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s2.UnlockVault(testPW); err != nil {
		t.Fatal(err)
	}

	// Back: needs nothing more because it is unlocked.
	if err := s2.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); err != nil {
		t.Fatal(err)
	}
	checkKeyring(t, kr.Keyring, want)
	if s2.VaultState().Exists || s2.SecretBackend() != SecretBackendKeyring {
		t.Fatal("vault not removed after switching back")
	}
	if _, err := os.Stat(filepath.Join(dir, vaultFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("vault.json remains")
	}
	if _, err := s2.Buckets(testCtx(t), a2.ID); err != nil {
		t.Fatalf("Buckets via keyring: %v", err)
	}
	if got := s2.SecretRefs(); len(got) != 3 {
		t.Fatalf("refs: %v", got)
	}
}

func TestMigrationBackFromLockedVaultNeedsPassword(t *testing.T) {
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	want := wantSecrets(a1, a2)
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	s.LockVault()
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); !IsKind(err, ErrLocked) {
		t.Fatalf("locked, no password: %v", err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, "wrong password!"); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("wrong password: %v", err)
	}
	if s.SecretBackend() != SecretBackendVault || !s.VaultState().Exists {
		t.Fatal("failed switch changed state")
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, testPW); err != nil {
		t.Fatal(err)
	}
	checkKeyring(t, kr.Keyring, want)
}

func TestMigrationToVaultFailureLeavesKeyringAuthoritative(t *testing.T) {
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	want := wantSecrets(a1, a2)
	// Persisting the setting fails: settings.json cannot be replaced.
	os.Remove(filepath.Join(dir, "settings.json"))
	blocker := filepath.Join(dir, "settings.json")
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(blocker, "x"), []byte("x"), 0o600)
	err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW)
	if err == nil {
		t.Fatal("expected failure")
	}
	if s.SecretBackend() != SecretBackendKeyring {
		t.Fatal("backend changed")
	}
	if _, err := os.Stat(filepath.Join(dir, vaultFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("half-created vault.json left behind")
	}
	checkKeyring(t, kr.Keyring, want) // source untouched
	_ = a1
	if v, err := s.ResolvePassword(testCtx(t), "zip-pw"); err != nil || v != "zip-pw-value" {
		t.Fatalf("keyring no longer usable: %q %v", v, err)
	}
	if err := s.SetSecret("after", "after-value-1"); err != nil || !kr.has("ref/after") {
		t.Fatalf("keyring not usable after failed switch: %v", err)
	}
}

func TestMigrationToKeyringFailsMidwayAndKeepsVault(t *testing.T) {
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	want := wantSecrets(a1, a2)
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= len(want); n++ {
		var sets atomic.Int32
		kr.onSet = func(string) error {
			if int(sets.Add(1)) == n {
				return errors.New("keychain write refused")
			}
			return nil
		}
		err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, "")
		if err == nil {
			t.Fatalf("n=%d: expected failure", n)
		}
		if s.SecretBackend() != SecretBackendVault || !s.VaultState().Exists {
			t.Fatalf("n=%d: backend changed", n)
		}
		checkAbsent(t, kr.Keyring, want) // partial target writes removed
		// The vault is still authoritative and complete.
		s.vlt.mu.Lock()
		unlocked := s.vlt.key != nil
		s.vlt.mu.Unlock()
		if !unlocked {
			t.Fatalf("n=%d: vault locked by a failed switch", n)
		}
		for k, v := range want {
			if got, err := s.vlt.Get(k); err != nil || got != v {
				t.Fatalf("n=%d: vault lost %s: %q %v", n, k, got, err)
			}
		}
	}
	kr.onSet = nil
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); err != nil {
		t.Fatalf("retry after failures: %v", err)
	}
	checkKeyring(t, kr.Keyring, want)
}

func TestMigrationSourceCleanupFailureStillSwitches(t *testing.T) {
	kr := newTestKeyring()
	s := newClockService(t, "", kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	want := wantSecrets(a1, a2)
	kr.onDel = func(string) error { return errors.New("keychain delete refused") }
	err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW)
	if KindOf(err) != ErrOther || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("got %v", err)
	}
	if s.SecretBackend() != SecretBackendVault {
		t.Fatal("switch must stand")
	}
	for k, v := range want {
		if got, err := s.vlt.Get(k); err != nil || got != v {
			t.Fatalf("%s: %q %v", k, got, err)
		}
	}
}

func TestMigrationSkipsEntriesMissingFromSource(t *testing.T) {
	kr := newTestKeyring()
	s := newClockService(t, "", kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	kr.Keyring.Delete("account/" + a1.ID + "/secret") // already needs re-entering
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatalf("a missing entry must not block the switch: %v", err)
	}
	if got, err := s.vlt.Get("account/" + a2.ID + "/secret.1"); err != nil || got != "secret-two-1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestMigrationHonoursContext(t *testing.T) {
	kr := newTestKeyring()
	s := newClockService(t, "", kr, nil)
	seedSecrets(t, s, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SetSecretBackend(ctx, SecretBackendVault, testPW); !IsKind(err, ErrCanceled) {
		t.Fatalf("got %v", err)
	}
	if s.SecretBackend() != SecretBackendKeyring || s.VaultState().Exists {
		t.Fatal("canceled switch changed state")
	}
}

func TestSetSecretBackendValidation(t *testing.T) {
	s := newClockService(t, "", nil, nil)
	if err := s.SetSecretBackend(testCtx(t), "cloud", ""); !IsKind(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); err != nil {
		t.Fatalf("same backend must be a no-op: %v", err)
	}
	if err := s.UnlockVault(testPW); !IsKind(err, ErrNotFound) {
		t.Fatalf("unlock without vault: %v", err)
	}
	if err := s.ChangeVaultPassword(testPW, "another password"); !IsKind(err, ErrNotFound) {
		t.Fatalf("change without vault: %v", err)
	}
}

func TestSaveSettingsKeepsBackendAndStoresAutoLock(t *testing.T) {
	s := newClockService(t, "", nil, nil)
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	st := s.Settings()
	st.SecretBackend = SecretBackendKeyring // must be ignored
	st.VaultAutoLockMinutes = 3
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got.SecretBackend != SecretBackendVault || got.VaultAutoLockMinutes != 3 {
		t.Fatalf("%+v", got)
	}
	st.VaultAutoLockMinutes = -1
	if err := s.SaveSettings(st); !IsKind(err, ErrInvalid) {
		t.Fatalf("negative auto-lock: %v", err)
	}
	st.VaultAutoLockMinutes = 0
	if err := s.SaveSettings(st); err != nil || s.VaultState().AutoLockMinutes != 0 {
		t.Fatalf("0 = never must be storable: %v", err)
	}
}

func TestOldSettingsFileKeepsAutoLockDefault(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"dark"}`), 0o600)
	s := newClockService(t, dir, nil, nil)
	if got := s.Settings(); got.VaultAutoLockMinutes != 15 || got.SecretBackend != SecretBackendKeyring {
		t.Fatalf("%+v", got)
	}
}

func TestLockedVaultFailsFastWithErrLocked(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newClockService(t, "", kr, nil)
	a1, _ := seedSecrets(t, s, srv.URL)
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil { // fills the client cache
		t.Fatal(err)
	}
	s.LockVault()
	if s.VaultState().Unlocked {
		t.Fatal("still unlocked")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Buckets(testCtx(t), a1.ID); !IsKind(err, ErrLocked) || !errors.Is(err, ErrVaultLocked) {
			t.Errorf("Buckets: %v", err)
		}
		if err := s.TestAccount(testCtx(t), a1, nil); !IsKind(err, ErrLocked) {
			t.Errorf("TestAccount: %v", err)
		}
		if _, err := s.SaveAccount(a1, &Secrets{SecretAccessKey: "new-secret-9"}); !IsKind(err, ErrLocked) {
			t.Errorf("SaveAccount: %v", err)
		}
		if err := s.SetSecret("r", "value-value"); !IsKind(err, ErrLocked) {
			t.Errorf("SetSecret: %v", err)
		}
		if _, err := s.ResolvePassword(testCtx(t), "zip-pw"); !IsKind(err, ErrLocked) {
			t.Errorf("ResolvePassword: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a locked vault must not block")
	}
	// A static account whose other state is fine recovers after unlocking.
	if err := s.UnlockVault(testPW); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
}

func TestLockedErrorKindPlumbing(t *testing.T) {
	e := lockedErr()
	if KindOf(e) != ErrLocked || ErrLocked.String() != "Locked" || !errors.Is(e, ErrVaultLocked) {
		t.Fatal("ErrLocked plumbing")
	}
	// Existing kinds keep their numeric values; ErrLocked is appended.
	if ErrOther != 0 || ErrNotFound != 1 || ErrAccessDenied != 2 || ErrUnsupported != 3 || ErrConflict != 4 ||
		ErrNetwork != 5 || ErrCanceled != 6 || ErrInvalid != 7 || ErrLocked != 8 {
		t.Fatal("ErrKind values changed")
	}
	if got := classify("Op", e); KindOf(got) != ErrLocked {
		t.Fatalf("classify: %v", got)
	}
	if got := classify("Op", errors.Join(errors.New("x"), ErrVaultLocked)); KindOf(got) != ErrLocked {
		t.Fatalf("classify(sentinel): %v", got)
	}
}

func TestVaultAutoLockWithFakeClock(t *testing.T) {
	srv := stubS3(t, nil)
	clk := newFakeClock()
	kr := newTestKeyring()
	s := newClockService(t, "", kr, clk)
	a1, _ := seedSecrets(t, s, srv.URL)
	st := s.Settings()
	st.VaultAutoLockMinutes = 5
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	if !s.VaultState().Unlocked {
		t.Fatal("not unlocked after creation")
	}
	clk.Advance(4 * time.Minute)
	if !s.VaultState().Unlocked {
		t.Fatal("locked too early")
	}
	// A secret read counts as activity and restarts the timer.
	if _, err := s.ResolvePassword(testCtx(t), "zip-pw"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(4 * time.Minute)
	if !s.VaultState().Unlocked {
		t.Fatal("secret read did not count as activity")
	}
	// So does using an account (cached client, no secret read).
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatal(err)
	}
	clk.Advance(4 * time.Minute)
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatalf("active use must keep the vault open: %v", err)
	}
	clk.Advance(5 * time.Minute)
	if s.VaultState().Unlocked {
		t.Fatal("did not auto-lock after 5 idle minutes")
	}
	// Cached clients (they hold decrypted credentials) are gone with the key.
	if _, err := s.Buckets(testCtx(t), a1.ID); !IsKind(err, ErrLocked) {
		t.Fatalf("after auto-lock: %v", err)
	}
	if err := s.UnlockVault(testPW); err != nil {
		t.Fatal(err)
	}
	// The lazy check also fires on access, without anyone calling VaultState.
	clk.Advance(6 * time.Minute)
	if _, err := s.ResolvePassword(testCtx(t), "zip-pw"); !IsKind(err, ErrLocked) {
		t.Fatalf("access after idle: %v", err)
	}
	// 0 = never.
	st.VaultAutoLockMinutes = 0
	s.SaveSettings(st)
	s.UnlockVault(testPW)
	clk.Advance(1000 * time.Hour)
	if !s.VaultState().Unlocked {
		t.Fatal("auto-lock 0 must never lock")
	}
}

func TestChangeVaultPasswordService(t *testing.T) {
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, "http://127.0.0.1:1")
	s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW)
	if err := s.ChangeVaultPassword("nope nope nope", "brand new password"); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("%v", err)
	}
	if err := s.ChangeVaultPassword(testPW, "brand new password"); err != nil {
		t.Fatal(err)
	}
	s.LockVault()
	if err := s.UnlockVault(testPW); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("old password: %v", err)
	}
	if err := s.UnlockVault("brand new password"); err != nil {
		t.Fatal(err)
	}
	checkKeyring(t, s.vlt, wantSecrets(a1, a2))
}

func TestResetVault(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	dir := t.TempDir()
	s := newClockService(t, dir, kr, nil)
	a1, a2 := seedSecrets(t, s, srv.URL)
	s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW)
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatal(err)
	}
	s.LockVault()

	res, err := s.ResetVault()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accounts) != 2 || len(res.Refs) != 2 {
		t.Fatalf("report: %+v", res)
	}
	if s.SecretBackend() != SecretBackendKeyring || s.VaultState().Exists {
		t.Fatal("reset must delete the vault and fall back to the keychain")
	}
	if _, err := os.Stat(filepath.Join(dir, vaultFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("vault.json remains")
	}
	missing := 0
	for _, a := range s.Accounts() {
		if a.SecretMissing {
			missing++
		}
	}
	if missing != 2 {
		t.Fatalf("%d accounts flagged SecretMissing, want 2", missing)
	}
	// Re-entering the secret fixes the account; the old cached client is gone.
	if _, err := s.Buckets(testCtx(t), a1.ID); err == nil {
		t.Fatal("account without a secret must not work")
	}
	a1.SecretMissing = true // as the UI would echo it back
	if _, err := s.SaveAccount(a1, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("saving without a secret: %v", err)
	}
	if _, err := s.SaveAccount(a1, &Secrets{SecretAccessKey: "entered-again"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buckets(testCtx(t), a1.ID); err != nil {
		t.Fatalf("after re-entering: %v", err)
	}
	for _, a := range s.Accounts() {
		if a.ID == a1.ID && a.SecretMissing {
			t.Fatal("flag not cleared")
		}
		if a.ID == a2.ID && !a.SecretMissing {
			t.Fatal("other account lost its flag")
		}
	}
	// With nothing to reset it is a harmless no-op.
	if res, err := s.ResetVault(); err != nil || len(res.Accounts) != 0 {
		t.Fatalf("second reset: %+v %v", res, err)
	}
}

func TestKeyringUnavailableClassification(t *testing.T) {
	dbusErr := errors.New("The name org.freedesktop.secrets was not provided by any .service files")
	for _, e := range []error{
		dbusErr,
		errors.New(`dial unix /run/user/1000/bus: connect: no such file or directory (dbus)`),
		errors.New("exec: \"dbus-launch\": executable file not found in $PATH"),
		errors.New("failed to unlock correct collection '/org/freedesktop/secrets/collection/login'"),
	} {
		kr := newTestKeyring()
		kr.onSet = func(string) error { return e }
		kr.onGet = func(string) error { return e }
		s := newClockService(t, "", kr, nil)
		err := s.SetSecret("ref", "value-value")
		if !errors.Is(err, ErrKeyringUnavailable) || !IsKind(err, ErrUnsupported) || !strings.Contains(err.Error(), "No system keychain available") {
			t.Errorf("%v -> %v (kind %v)", e, err, KindOf(err))
		}
		if !strings.Contains(err.Error(), e.Error()) {
			t.Errorf("detail lost: %v", err)
		}
		if len(s.SecretRefs()) != 0 {
			t.Error("ref recorded despite failure")
		}
		if _, err := s.ResolvePassword(testCtx(t), "ref"); !errors.Is(err, ErrKeyringUnavailable) {
			t.Errorf("read: %v", err)
		}
	}
	// Ordinary failures keep their own classification.
	kr := newTestKeyring()
	kr.onSet = func(string) error { return errors.New("access denied by user") }
	s := newClockService(t, "", kr, nil)
	if err := s.SetSecret("ref", "value-value"); errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("misclassified: %v", err)
	}
	// "Not found" stays a plain miss.
	if _, err := s.ResolvePassword(testCtx(t), "ghost"); !IsKind(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	// And the vault is the way out on such a machine: the switch itself needs no keychain.
	kr2 := newTestKeyring()
	kr2.onSet, kr2.onGet = func(string) error { return dbusErr }, func(string) error { return dbusErr }
	s2 := newClockService(t, "", kr2, nil)
	if err := s2.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatalf("switch to vault without a keychain: %v", err)
	}
	if err := s2.SetSecret("ref", "value-value"); err != nil {
		t.Fatalf("SetSecret via vault: %v", err)
	}
}

func TestVaultNeverLogsSecrets(t *testing.T) {
	dir := t.TempDir()
	kr := newTestKeyring()
	s := newClockService(t, dir, kr, nil)
	st := s.Settings()
	st.LogToFile = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, s, "http://127.0.0.1:1")
	const pw = "log-me-not-please-0"
	s.SetSecretBackend(testCtx(t), SecretBackendVault, pw)
	s.UnlockVault("log-wrong-attempt-0")
	s.ChangeVaultPassword(pw, "log-new-password-0")
	s.LockVault()
	s.UnlockVault("log-new-password-0")
	s.SetSecretBackend(testCtx(t), SecretBackendKeyring, "")
	s.Shutdown(testCtx(t))
	b, err := os.ReadFile(filepath.Join(s.LogDir(), logFileName))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, secret := range []string{pw, "log-wrong-attempt-0", "log-new-password-0", "secret-one-0", "token-one-0", "zip-pw-value", "sse-key-value"} {
		if strings.Contains(log, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
	for _, ev := range []string{"secret backend switched", "vault unlocked", "vault locked"} {
		if !strings.Contains(log, ev) {
			t.Errorf("log lacks %q:\n%s", ev, log)
		}
	}
}

func TestConcurrentSecretReadsDuringLockAndSwitch(t *testing.T) {
	kr := newTestKeyring()
	s := newClockService(t, "", kr, nil)
	seedSecrets(t, s, "http://127.0.0.1:1")
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v, err := s.ResolvePassword(context.Background(), "zip-pw")
				if !(err == nil && v == "zip-pw-value") && !IsKind(err, ErrLocked) {
					bad.Add(1)
				}
				s.VaultState()
			}
		}()
	}
	for i := 0; i < 10; i++ {
		s.LockVault()
		s.UnlockVault(testPW)
	}
	// Switch backends while readers run: readers see the value or a clean lock.
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); err != nil {
		t.Error(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Error(err)
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d reads saw a missing or wrong secret", bad.Load())
	}
}
