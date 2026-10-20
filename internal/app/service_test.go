package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestService returns a Service on a temp config dir with an in-memory
// keyring (override with kr). It shuts the Service down when the test ends.
// Other test files in this package should use it.
func newTestService(t testing.TB, kr Keyring) *Service {
	t.Helper()
	s, dir := newTestServiceDir(t, "", kr)
	_ = dir
	return s
}

// newTestServiceDir is newTestService on an explicit (possibly reused) dir.
func newTestServiceDir(t testing.TB, dir string, kr Keyring) (*Service, string) {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	if kr == nil {
		kr = NewMemoryKeyring()
	}
	s, err := New(Options{ConfigDir: dir, Keyring: kr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return s, dir
}

// testCtx returns a context that ends with the test or after 30 seconds.
func testCtx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestNewCreatesDefaults(t *testing.T) {
	s := newTestService(t, nil)
	st := s.Settings()
	if st != DefaultSettings() {
		t.Fatalf("settings = %+v", st)
	}
	if st.Limits != (Limits{8, 8, 8}) || st.BulkWorkers != 8 || st.PartSizeMiB != 16 || st.Theme != "system" || st.Conflict != ConflictRename {
		t.Fatalf("unexpected defaults %+v", st)
	}
	if len(s.Accounts()) != 0 || len(s.Tasks()) != 0 || len(s.SecretRefs()) != 0 {
		t.Fatal("fresh service should be empty")
	}
}

func TestNewDefaultConfigDirAndCorruptFiles(t *testing.T) {
	for _, file := range []string{"settings.json", "accounts.json", "upload-rules.json", "mime-types.json", "sync-jobs.json", "secret-refs.json"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, file), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()})
			if err == nil || !strings.Contains(err.Error(), file) {
				t.Fatalf("want corrupt-file error naming %s, got %v", file, err)
			}
			// The damaged file must not have been replaced.
			b, _ := os.ReadFile(filepath.Join(dir, file))
			if string(b) != "{not json" {
				t.Fatal("corrupt file was modified")
			}
		})
	}
}

func TestNewRejectsInvalidPersistedValues(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"neon"}`), 0o600)
	if _, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()}); err == nil {
		t.Fatal("invalid theme in file must be an error")
	}
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(`[{"id":"a","name":"x"},{"id":"a","name":"y"}]`), 0o600)
	if _, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()}); err == nil {
		t.Fatal("duplicate account ids must be an error")
	}
}

func TestShutdownIdempotent(t *testing.T) {
	s, err := New(Options{ConfigDir: t.TempDir(), Keyring: NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
