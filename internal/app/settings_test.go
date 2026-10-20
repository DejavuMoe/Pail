package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSettingsSaveLoadRoundTrip(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	want := Settings{Theme: "dark", Limits: Limits{3, 4, 5}, BulkWorkers: 6, PartSizeMiB: 32,
		DefaultDownloadDir: t.TempDir(), Conflict: ConflictSkip, ShowVersions: true, LogToFile: true,
		SecretBackend: SecretBackendKeyring, VaultAutoLockMinutes: 20}
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got != want {
		t.Fatalf("got %+v", got)
	}
	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.Settings(); got != want {
		t.Fatalf("reloaded %+v", got)
	}
}

func TestSaveSettingsFillsZeroValuesAndValidates(t *testing.T) {
	s := newTestService(t, nil)
	if err := s.SaveSettings(Settings{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	got := s.Settings()
	d := DefaultSettings()
	d.Theme = "light"
	d.VaultAutoLockMinutes = 0 // 0 is a real value ("never"), not defaulted
	if got != d {
		t.Fatalf("zero fields not defaulted: %+v", got)
	}
	bad := []Settings{
		{Theme: "neon"},
		{Limits: Limits{Upload: -1}},
		{Limits: Limits{Other: 1000}},
		{BulkWorkers: 500},
		{PartSizeMiB: 1},
		{PartSizeMiB: 99999},
		{Conflict: "explode"},
		{DefaultDownloadDir: "relative/dir"},
	}
	for i, b := range bad {
		err := s.SaveSettings(b)
		if err == nil || KindOf(err) != ErrInvalid {
			t.Errorf("case %d: want Invalid, got %v", i, err)
		}
	}
	if s.Settings() != d {
		t.Fatal("rejected settings must not change the current ones")
	}
}

func TestSaveSettingsAppliesLimitsToQueue(t *testing.T) {
	s := newTestService(t, nil)
	st := s.Settings()
	st.Limits.Other = 1
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	running, maxRunning := 0, 0
	release := make(chan struct{})
	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, s.submit(taskSpec{Kind: KindOther, Title: "t", Work: func(ctx context.Context, _ *run) error {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			mu.Unlock()
			<-release
			mu.Lock()
			running--
			mu.Unlock()
			return nil
		}}))
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for _, id := range ids {
		if _, err := s.waitTask(testCtx(t), id); err != nil {
			t.Fatal(err)
		}
	}
	if maxRunning != 1 {
		t.Fatalf("limit 1 not applied: %d tasks ran concurrently", maxRunning)
	}
}

func TestWriteJSONAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.json")
	if err := writeJSONAtomic(p, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(p, map[string]int{"a": 2}); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if found, err := readJSON(p, &got); err != nil || !found || got["a"] != 2 {
		t.Fatalf("found=%v err=%v got=%v", found, err, got)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	// Unmarshalable value: error, previous file intact, no temp litter.
	if err := writeJSONAtomic(p, make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
	if found, _ := readJSON(p, &got); !found || got["a"] != 2 {
		t.Fatal("previous content lost")
	}
	entries, _ = os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestReadJSONMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	var v map[string]int
	if found, err := readJSON(filepath.Join(dir, "nope.json"), &v); found || err != nil {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
	p := filepath.Join(dir, "bad.json")
	os.WriteFile(p, []byte("[1,2"), 0o600)
	found, err := readJSON(p, &v)
	if !found || err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("corrupt: found=%v err=%v", found, err)
	}
}
