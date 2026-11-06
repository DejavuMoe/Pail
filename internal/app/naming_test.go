package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigDirIsPail(t *testing.T) {
	base := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return base, nil }
	t.Cleanup(func() { userConfigDir = old })

	s, err := New(Options{Keyring: NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	if want := filepath.Join(base, "Pail"); s.dir != want {
		t.Fatalf("config dir = %s, want %s", s.dir, want)
	}
}

func TestOSKeyringServiceIsPail(t *testing.T) {
	if keyringService != "Pail" {
		t.Fatalf("keyring service = %q", keyringService)
	}
}

func TestInternalMetadataPrefix(t *testing.T) {
	for k, want := range map[string]bool{"pail-mtime": true, "opens3-mtime": false, "author": false, "x-pail-y": false} {
		if isInternalMeta(k) != want {
			t.Errorf("isInternalMeta(%q) != %v", k, want)
		}
	}
}

func TestRewriteRefusesInternalPrefix(t *testing.T) {
	for _, k := range []string{"pail-mtime", "pail-anything"} {
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

func TestTempNames(t *testing.T) {
	for name, want := range map[string]bool{
		".pail-123.tmp": true, ".opens3-123.tmp": false, ".pail-123": false, "pail-123.tmp": false, ".other-1.tmp": false,
	} {
		if syIsTempName(name) != want {
			t.Errorf("syIsTempName(%q) != %v", name, want)
		}
	}
}

func TestSweepSpoolFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	for name, aged := range map[string]bool{
		"pail-upload-1.tmp": true, "pail-upload-3.tmp.bak": true,
		"pail-upload-4.tmp-fresh": false, "fresh.tmp": true,
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
	if want := []string{"fresh.tmp", "pail-upload-3.tmp.bak", "pail-upload-4.tmp-fresh"}; !reflect.DeepEqual(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

func TestVaultWithAnotherFormatNameIsRefused(t *testing.T) {
	v := newTestVault(t)
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	v.lock()

	f, _ := readVaultFile(t, v)
	key := deriveKey(testPW, f.KDF)
	data, err := openFile(f, key)
	if err != nil {
		t.Fatal(err)
	}
	hdr := f.vaultHeader
	hdr.Format = "opens3-vault"
	other, err := sealFile(hdr, key, data)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(other)
	if err := os.WriteFile(v.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	err = newVault(v.path).unlock(testPW)
	if err == nil || !strings.Contains(err.Error(), "not a Pail vault") {
		t.Fatalf("unlock = %v", err)
	}
}
