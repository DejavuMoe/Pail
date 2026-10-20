package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---- configuration directory ---------------------------------------------------

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		got[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// withUserConfigDir points the default configuration base at base.
func withUserConfigDir(t *testing.T, base string) {
	t.Helper()
	old := userConfigDir
	userConfigDir = func() (string, error) { return base, nil }
	t.Cleanup(func() { userConfigDir = old })
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

var legacyFiles = map[string]string{
	"secret-refs.json": `["zip-pass"]`,
	"settings.json":    `{"theme":"dark"}`,
	"logs/pail.log":    "older run",
}

func TestDefaultConfigDirIsRenamedFromLegacy(t *testing.T) {
	base := t.TempDir()
	writeTree(t, filepath.Join(base, legacyConfigDirName), legacyFiles)
	withUserConfigDir(t, base)

	s, err := New(Options{Keyring: NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	if s.dir != filepath.Join(base, "Pail") {
		t.Fatalf("config dir = %s", s.dir)
	}
	if exists(filepath.Join(base, legacyConfigDirName)) {
		t.Error("the old directory is still there after the rename")
	}
	if got := s.SecretRefs(); !reflect.DeepEqual(got, []string{"zip-pass"}) {
		t.Errorf("secret refs after migration = %v", got)
	}
	if th := s.Settings().Theme; th != "dark" {
		t.Errorf("theme after migration = %q", th)
	}
	if got := readTree(t, s.dir)["logs/pail.log"]; got != "older run" {
		t.Errorf("nested file lost: %q", got)
	}
}

func TestDefaultConfigDirLeavesExistingNewDirAlone(t *testing.T) {
	base := t.TempDir()
	writeTree(t, filepath.Join(base, legacyConfigDirName), legacyFiles)
	writeTree(t, filepath.Join(base, "Pail"), map[string]string{"settings.json": `{"theme":"light"}`})
	dir, mig := resolveDefaultConfigDir(base)
	if dir != filepath.Join(base, "Pail") || mig != (configDirMigration{}) {
		t.Fatalf("resolve = %s, %+v", dir, mig)
	}
	if !reflect.DeepEqual(readTree(t, filepath.Join(base, legacyConfigDirName)), legacyFiles) {
		t.Error("the old directory was modified")
	}
	if got := readTree(t, dir); len(got) != 1 {
		t.Errorf("the new directory was modified: %v", got)
	}
}

func TestDefaultConfigDirFreshInstallMakesNothingUp(t *testing.T) {
	base := t.TempDir()
	dir, mig := resolveDefaultConfigDir(base)
	if dir != filepath.Join(base, "Pail") || mig != (configDirMigration{}) || exists(dir) {
		t.Fatalf("resolve = %s, %+v", dir, mig)
	}
}

func TestDefaultConfigDirCopyFallbackKeepsOldDir(t *testing.T) {
	base := t.TempDir()
	writeTree(t, filepath.Join(base, legacyConfigDirName), legacyFiles)
	old := renameDir
	renameDir = func(string, string) error { return errors.New("rename refused") }
	t.Cleanup(func() { renameDir = old })

	dir, mig := resolveDefaultConfigDir(base)
	if dir != filepath.Join(base, "Pail") || mig.Err != nil || !strings.Contains(mig.Note, "copied") {
		t.Fatalf("resolve = %s, %+v", dir, mig)
	}
	if !reflect.DeepEqual(readTree(t, dir), legacyFiles) {
		t.Errorf("copy differs: %v", readTree(t, dir))
	}
	if !reflect.DeepEqual(readTree(t, filepath.Join(base, legacyConfigDirName)), legacyFiles) {
		t.Error("the old directory must stay in place after a copy")
	}
	if exists(dir + ".migrating") {
		t.Error("temporary copy left behind")
	}
}

func TestDefaultConfigDirFallsBackToOldDirWhenNothingWorks(t *testing.T) {
	base := t.TempDir()
	writeTree(t, filepath.Join(base, legacyConfigDirName), legacyFiles)
	oldRename, oldCopy := renameDir, copyDir
	renameDir = func(string, string) error { return errors.New("rename refused") }
	copyDir = func(src, dst string) error {
		os.MkdirAll(dst, 0o700) // a half-done copy
		return errors.New("disk full")
	}
	t.Cleanup(func() { renameDir, copyDir = oldRename, oldCopy })

	dir, mig := resolveDefaultConfigDir(base)
	if dir != filepath.Join(base, legacyConfigDirName) || mig.Err == nil {
		t.Fatalf("resolve = %s, %+v", dir, mig)
	}
	if exists(filepath.Join(base, "Pail")) || exists(filepath.Join(base, "Pail.migrating")) {
		t.Error("a partial new directory was left behind; the next start would not retry")
	}
	if !reflect.DeepEqual(readTree(t, dir), legacyFiles) {
		t.Error("the old directory was modified")
	}
}

// ---- keyring --------------------------------------------------------------------

// failSetKeyring refuses to store the listed keys.
type failSetKeyring struct {
	Keyring
	fail map[string]bool
}

func (k failSetKeyring) Set(key, v string) error {
	if k.fail[key] {
		return errors.New("keychain write refused")
	}
	return k.Keyring.Set(key, v)
}

// garbleKeyring stores a different value than it is given (a copy that cannot verify).
type garbleKeyring struct{ Keyring }

func (k garbleKeyring) Set(key, v string) error { return k.Keyring.Set(key, v+"x") }

func keyringHas(t *testing.T, kr Keyring, key, want string) {
	t.Helper()
	got, err := kr.Get(key)
	if err != nil || got != want {
		t.Errorf("Get(%q) = %q, %v; want %q", key, got, err, want)
	}
}

func keyringMissing(t *testing.T, kr Keyring, key string) {
	t.Helper()
	if v, err := kr.Get(key); !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("Get(%q) = %q, %v; want not found", key, v, err)
	}
}

// legacyService is a Service whose configuration names an account (generation
// 2, with session token), a plain account and one secret reference.
func legacyService(t *testing.T) *Service {
	t.Helper()
	s := newTestService(t, nil)
	s.mu.Lock()
	s.accounts = []accountRecord{
		{ID: "a1", HasSecret: true, HasSession: true, SecretGen: 2},
		{ID: "a2", HasSecret: true},
		{ID: "a3"}, // no secrets
	}
	s.refs = []string{"zip-pass"}
	s.mu.Unlock()
	return s
}

func TestMigrateLegacyKeyringMovesEveryKnownKey(t *testing.T) {
	s := legacyService(t)
	cur, legacy := NewMemoryKeyring(), NewMemoryKeyring()
	for k, v := range map[string]string{
		accountSecretKey("a1", 2):  "s1",
		accountSessionKey("a1", 2): "tok1",
		accountSecretKey("a2", 0):  "s2",
		refKey("zip-pass"):         "pw",
		accountSecretKey("a1", 1):  "stale generation", // not referenced: must stay put
		"unrelated":                "other",
	} {
		legacy.Set(k, v)
	}
	if n := s.migrateLegacyKeyring(context.Background(), cur, legacy); n != 4 {
		t.Fatalf("moved %d entries, want 4", n)
	}
	keyringHas(t, cur, accountSecretKey("a1", 2), "s1")
	keyringHas(t, cur, accountSessionKey("a1", 2), "tok1")
	keyringHas(t, cur, accountSecretKey("a2", 0), "s2")
	keyringHas(t, cur, refKey("zip-pass"), "pw")
	for _, k := range []string{accountSecretKey("a1", 2), accountSessionKey("a1", 2), accountSecretKey("a2", 0), refKey("zip-pass")} {
		keyringMissing(t, legacy, k)
	}
	keyringHas(t, legacy, accountSecretKey("a1", 1), "stale generation")
	keyringMissing(t, cur, accountSecretKey("a1", 1))
	keyringMissing(t, cur, "unrelated")

	// A second start has nothing left to do.
	if n := s.migrateLegacyKeyring(context.Background(), cur, legacy); n != 0 {
		t.Errorf("second run moved %d", n)
	}
}

func TestMigrateLegacyKeyringKeepsExistingNewEntry(t *testing.T) {
	s := legacyService(t)
	cur, legacy := NewMemoryKeyring(), NewMemoryKeyring()
	cur.Set(accountSecretKey("a2", 0), "new value")
	legacy.Set(accountSecretKey("a2", 0), "old value")
	if n := s.migrateLegacyKeyring(context.Background(), cur, legacy); n != 0 {
		t.Fatalf("moved %d", n)
	}
	keyringHas(t, cur, accountSecretKey("a2", 0), "new value")
	keyringHas(t, legacy, accountSecretKey("a2", 0), "old value")
}

func TestMigrateLegacyKeyringNeverDeletesBeforeVerifiedCopy(t *testing.T) {
	good, bad := accountSecretKey("a2", 0), refKey("zip-pass")
	for name, mk := range map[string]func(Keyring) Keyring{
		"write refused":    func(k Keyring) Keyring { return failSetKeyring{k, map[string]bool{good: true, bad: true}} },
		"readback differs": func(k Keyring) Keyring { return garbleKeyring{k} },
	} {
		t.Run(name, func(t *testing.T) {
			s := legacyService(t)
			legacy := NewMemoryKeyring()
			legacy.Set(good, "s2")
			legacy.Set(bad, "pw")
			if n := s.migrateLegacyKeyring(context.Background(), mk(NewMemoryKeyring()), legacy); n != 0 {
				t.Fatalf("moved %d", n)
			}
			keyringHas(t, legacy, good, "s2")
			keyringHas(t, legacy, bad, "pw")
		})
	}
}

func TestMigrateLegacyKeyringContinuesAfterOneFailure(t *testing.T) {
	s := legacyService(t)
	legacy := NewMemoryKeyring()
	legacy.Set(accountSecretKey("a2", 0), "s2")
	legacy.Set(refKey("zip-pass"), "pw")
	cur := failSetKeyring{NewMemoryKeyring(), map[string]bool{accountSecretKey("a2", 0): true}}
	if n := s.migrateLegacyKeyring(context.Background(), cur, legacy); n != 1 {
		t.Fatalf("moved %d, want 1", n)
	}
	keyringHas(t, legacy, accountSecretKey("a2", 0), "s2") // retried at the next start
	keyringMissing(t, legacy, refKey("zip-pass"))
	keyringHas(t, cur, refKey("zip-pass"), "pw")
}

func TestOSKeyringIsBoundToItsService(t *testing.T) {
	if (osKeyring{keyringService}) == (osKeyring{legacyKeyringService}) || keyringService != "Pail" {
		t.Fatal("the current and the legacy keyring service must differ")
	}
}

// ---- metadata -------------------------------------------------------------------

func TestMetaGetFallsBackToLegacyKeys(t *testing.T) {
	both := map[string]string{"pail-size": "1", "opens3-size": "2"}
	if got := metaGet(both, "pail-size"); got != "1" {
		t.Errorf("the new key must win: %q", got)
	}
	if got := metaGet(map[string]string{"opens3-size": "2"}, "pail-size"); got != "2" {
		t.Errorf("legacy fallback: %q", got)
	}
	if got := metaGet(map[string]string{"opens3-size": "2"}, "other"); got != "" {
		t.Errorf("non-internal key: %q", got)
	}
	for k, want := range map[string]bool{"pail-mtime": true, "opens3-mtime": true, "author": false, "x-pail-y": false} {
		if isInternalMeta(k) != want {
			t.Errorf("isInternalMeta(%q) != %v", k, want)
		}
	}
}

func TestSyncCompareReadsLegacyMetadata(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	legacy := map[string]string{
		"opens3-mtime":  base.Format(time.RFC3339Nano),
		"opens3-size":   "100",
		"opens3-sha256": "abc",
	}
	l := &syLocalInfo{Size: 100, MTime: base, SHA: "abc"}
	r := &syRemoteInfo{Size: 7, Modified: base.Add(time.Hour), Meta: legacy, Compressed: true}
	for _, cmp := range []string{syCmpSize, syCmpSizeTime, syCmpChecksum} {
		if tr, why := syDecide(syCmp{Upload: true, Mode: syModeMirror, Compare: cmp}, l, r); tr {
			t.Errorf("upload, %s: an object synced by an older build is transferred again (%s)", cmp, why)
		}
	}
	down := &syRemoteInfo{Size: 100, Modified: base.Add(time.Hour), Meta: legacy}
	if tr, why := syDecide(syCmp{Mode: syModeMirror, Compare: syCmpSizeTime}, l, down); tr {
		t.Errorf("download: legacy opens3-mtime ignored (%s)", why)
	}
}

func TestAutoDecodeHonoursLegacyCompressionMarker(t *testing.T) {
	gz := []byte{0x1f, 0x8b, 8, 0}
	hints := decodeHints{Compression: metaGet(map[string]string{"opens3-compression": "gzip"}, compressionMetaKey)}
	mode, err := detectDecode(bytes.NewReader(gz), int64(len(gz)), DecodeAuto, hints)
	if err != nil || mode != DecodeGzip {
		t.Fatalf("legacy gzip marker: %v, %v", mode, err)
	}
}

func TestRewriteRefusesBothInternalPrefixes(t *testing.T) {
	for _, k := range []string{"pail-mtime", "opens3-mtime", "opens3-anything"} {
		_, err := bkValidatePatch("SetProperties", PropertiesPatch{SetMetadata: map[string]string{k: "1"}})
		if !IsKind(err, ErrInvalid) {
			t.Errorf("SetMetadata %q: %v", k, err)
		}
		_, err = bkValidatePatch("SetProperties", PropertiesPatch{DelMetadata: []string{k}})
		if !IsKind(err, ErrInvalid) {
			t.Errorf("DelMetadata %q: %v", k, err)
		}
	}
	if _, err := bkValidatePatch("SetProperties", PropertiesPatch{SetMetadata: map[string]string{"owner": "me"}}); err != nil {
		t.Errorf("an ordinary key was refused: %v", err)
	}
}

// ---- temporary files --------------------------------------------------------------

func TestTempNamesOfBothGenerations(t *testing.T) {
	for name, want := range map[string]bool{
		".pail-123.tmp": true, ".opens3-123.tmp": true, ".pail-123": false, "pail-123.tmp": false, ".other-1.tmp": false,
	} {
		if syIsTempName(name) != want {
			t.Errorf("syIsTempName(%q) != %v", name, want)
		}
	}
}

func TestSweepSpoolFilesRemovesLegacyOrphans(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	for name, aged := range map[string]bool{
		"pail-upload-1.tmp": true, "opens3-upload-2.tmp": true, "opens3-upload-3.tmp.bak": true,
		"opens3-upload-4.tmp-fresh": false, "fresh.tmp": true,
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o600)
		if aged {
			os.Chtimes(p, old, old)
		}
	}
	sweepSpoolFiles(dir, 24*time.Hour, now)
	var left []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	if want := []string{"fresh.tmp", "opens3-upload-3.tmp.bak", "opens3-upload-4.tmp-fresh"}; !reflect.DeepEqual(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

// ---- vault ---------------------------------------------------------------------------

func TestLegacyVaultFileOpensAndIsUpgradedOnWrite(t *testing.T) {
	v := newTestVault(t)
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("account/a1/secret", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	v.lock()

	// Turn the file into what a pre-rename build wrote: legacy format name, authenticated with it.
	f, _ := readVaultFile(t, v)
	key := deriveKey(testPW, f.KDF)
	data, err := openFile(f, key)
	if err != nil {
		t.Fatal(err)
	}
	hdr := f.vaultHeader
	hdr.Format = legacyVaultFormat
	lf, err := sealFile(hdr, key, data)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(lf)
	if err := os.WriteFile(v.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"format":"`+legacyVaultFormat+`"`) {
		t.Fatalf("test setup: %s", raw)
	}

	v2 := newVault(v.path)
	if err := v2.unlock(testPW); err != nil {
		t.Fatalf("legacy vault does not unlock: %v", err)
	}
	if got, err := v2.Get("account/a1/secret"); err != nil || got != "s3cr3t" {
		t.Fatalf("legacy entry = %q, %v", got, err)
	}
	// Reading alone leaves the file as it was.
	if after, _ := readVaultFile(t, v2); after.Format != legacyVaultFormat {
		t.Errorf("unlock rewrote the file: %q", after.Format)
	}
	if err := v2.Set("ref/zip", "pw"); err != nil {
		t.Fatal(err)
	}
	up, _ := readVaultFile(t, v2)
	if up.Format != vaultFormat {
		t.Fatalf("format after write = %q, want %q", up.Format, vaultFormat)
	}

	// The upgraded file opens with the same password and holds both entries.
	v3 := newVault(v.path)
	if err := v3.unlock(testPW); err != nil {
		t.Fatalf("upgraded vault does not unlock: %v", err)
	}
	for k, want := range map[string]string{"account/a1/secret": "s3cr3t", "ref/zip": "pw"} {
		if got, err := v3.Get(k); err != nil || got != want {
			t.Errorf("%s = %q, %v", k, got, err)
		}
	}
	// And the unlocked v2 can keep writing (its header was upgraded in memory too).
	if err := v2.Set("ref/two", "2"); err != nil {
		t.Fatalf("write after upgrade: %v", err)
	}
}
