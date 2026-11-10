package app

import (
	"context"
	"testing"
	"time"
)

// A running or queued task keeps the vault open: locking under a long transfer
// would make the queued tasks that come next fail with "vault is locked".
func TestVaultDoesNotAutoLockWhileTasksRun(t *testing.T) {
	clk := newFakeClock()
	s := newClockService(t, "", newTestKeyring(), clk)
	st := s.Settings()
	st.VaultAutoLockMinutes = 5
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	id := s.submit(taskSpec{Kind: KindOther, Title: "long transfer", Work: func(ctx context.Context, r *run) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}})
	<-started

	clk.Advance(30 * time.Minute)
	if !s.VaultState().Unlocked {
		t.Fatal("the vault locked while a task was running")
	}
	clk.Advance(30 * time.Minute)
	if !s.VaultState().Unlocked {
		t.Fatal("the vault locked while a task was still running")
	}

	close(release)
	if _, err := s.waitTask(testCtx(t), id); err != nil {
		t.Fatal(err)
	}
	// The idle time counts from the end of the work.
	clk.Advance(4 * time.Minute)
	if !s.VaultState().Unlocked {
		t.Fatal("the idle time did not restart when the task ended")
	}
	clk.Advance(2 * time.Minute)
	if s.VaultState().Unlocked {
		t.Fatal("the vault did not auto-lock after the tasks were done")
	}
}

func TestVaultDoesNotAutoLockWhileTasksAreQueued(t *testing.T) {
	clk := newFakeClock()
	s := newClockService(t, "", newTestKeyring(), clk)
	st := s.Settings()
	st.VaultAutoLockMinutes = 5
	st.Limits.Other = 1
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	work := func(ctx context.Context, r *run) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	first := s.submit(taskSpec{Kind: KindOther, Title: "first", Work: func(ctx context.Context, r *run) error {
		close(started)
		return work(ctx, r)
	}})
	second := s.submit(taskSpec{Kind: KindOther, Title: "second", Work: work})
	<-started
	if tk, _ := taskByID(s, second); tk.Status != StatusQueued {
		t.Skipf("the second task is %v, not queued", tk.Status)
	}
	clk.Advance(time.Hour)
	if !s.VaultState().Unlocked {
		t.Fatal("the vault locked while a task was queued")
	}
	close(release)
	for _, id := range []string{first, second} {
		if _, err := s.waitTask(testCtx(t), id); err != nil {
			t.Fatal(err)
		}
	}
}
