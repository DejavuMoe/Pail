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
		SecretBackend: SecretBackendKeyring, VaultAutoLockMinutes: 20, RecentOps: 250, KeepRecentOps: true, CollapseTools: true}
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
		{RecentOps: -1},
		{RecentOps: MaxRecentOps + 1},
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

// CollapseTools is additive: a file from before it existed unfolds the group,
// and an unfolded group leaves no key in the file.
func TestCollapseToolsSettingIsAdditive(t *testing.T) {
	if DefaultSettings().CollapseTools {
		t.Fatal("the group starts folded")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"dark","hideTrayIcon":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServiceDir(t, dir, nil)
	st := s.Settings()
	if st.CollapseTools || !st.HideTrayIcon || st.Theme != "dark" {
		t.Fatalf("old file: %+v", st)
	}
	st.CollapseTools = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !strings.Contains(string(raw), `"collapseTools": true`) && !strings.Contains(string(raw), `"collapseTools":true`) {
		t.Fatalf("saved file lacks the field: err=%v %s", err, raw)
	}
	st.CollapseTools = false
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if raw, _ = os.ReadFile(filepath.Join(dir, "settings.json")); strings.Contains(string(raw), "collapseTools") {
		t.Fatalf("an unfolded group writes the key: %s", raw)
	}
}

func TestRecentOpsSettingDefaultsBoundsAndOldFiles(t *testing.T) {
	if d := DefaultSettings(); d.RecentOps != 100 || d.KeepRecentOps {
		t.Fatalf("defaults: %+v", d)
	}
	// A file from before the setting existed gets the default.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServiceDir(t, dir, nil)
	if got := s.Settings(); got.RecentOps != DefaultRecentOps || got.KeepRecentOps {
		t.Fatalf("old file: %+v", got)
	}
	// Both ends of the range are real values, and a saved choice survives a restart.
	for _, n := range []int{MinRecentOps, MaxRecentOps} {
		st := s.Settings()
		st.RecentOps, st.KeepRecentOps = n, true
		if err := s.SaveSettings(st); err != nil {
			t.Fatalf("%d: %v", n, err)
		}
		if got := s.Settings(); got.RecentOps != n || !got.KeepRecentOps {
			t.Fatalf("%d: %+v", n, got)
		}
	}
	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.Settings(); got.RecentOps != MaxRecentOps || !got.KeepRecentOps {
		t.Fatalf("reloaded: %+v", got)
	}
	// 0 is "not set", not "keep nothing": it becomes the default.
	st := s2.Settings()
	st.RecentOps = 0
	if err := s2.SaveSettings(st); err != nil || s2.Settings().RecentOps != DefaultRecentOps {
		t.Fatalf("0: err=%v got=%+v", err, s2.Settings())
	}
	// A value outside the range is refused and leaves the saved one alone.
	st.RecentOps = MaxRecentOps + 1
	if err := s2.SaveSettings(st); !IsKind(err, ErrInvalid) || s2.Settings().RecentOps != DefaultRecentOps {
		t.Fatalf("over the maximum: err=%v got=%+v", err, s2.Settings())
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
