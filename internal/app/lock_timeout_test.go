package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// shortConfigLockTimeout makes lock waits in this test end quickly.
func shortConfigLockTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := configLockTimeout.Swap(int64(d))
	t.Cleanup(func() { configLockTimeout.Store(old) })
}

// lockHolderChild is the body of a child process that takes a lock and keeps
// it until the parent creates "release" (or a minute passes).
func lockHolderChild(t *testing.T, dir, id string, take func() (func(), error)) {
	t.Helper()
	release, err := take()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready-"+id), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()
}

// startLockHolder starts the child of test that holds a lock in dir.
func startLockHolder(t *testing.T, test, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), n2EnvDir+"="+dir, n2EnvID+"=0", n2EnvMode+"="+test+":hold")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("lock holder: %v\n%s", err, output.String())
			}
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("lock holder did not exit")
		}
	})
	waitAccountTestFile(t, filepath.Join(dir, "ready-0"))
}

// callWithin runs f and fails the test if it has not returned after limit:
// before the fix the lock wait had no bound at all.
func callWithin(t *testing.T, limit time.Duration, f func() error) (error, time.Duration) {
	t.Helper()
	start := time.Now()
	ch := make(chan error, 1)
	go func() { ch <- f() }()
	select {
	case err := <-ch:
		return err, time.Since(start)
	case <-time.After(limit):
		t.Fatalf("still waiting for the lock after %v", limit)
		return nil, 0
	}
}

func TestLockAccountWriteGivesUpWhileAnotherProcessHoldsIt(t *testing.T) {
	const test = "TestLockAccountWriteGivesUpWhileAnotherProcessHoldsIt"
	if dir, id, _, ok := n2Child(test); ok {
		s, _ := newTestServiceDir(t, dir, nil)
		lockHolderChild(t, dir, id, s.beginAccountWrite)
		return
	}
	dir := t.TempDir()
	s, _ := newTestServiceDir(t, dir, nil)
	shortConfigLockTimeout(t, 400*time.Millisecond)
	startLockHolder(t, test, dir)

	err, took := callWithin(t, 10*time.Second, func() error {
		_, err := s.SaveAccount(Account{Name: "X", Credentials: CredAnonymous}, nil)
		return err
	})
	if !errors.Is(err, ErrConfigLocked) || !IsKind(err, ErrConflict) {
		t.Fatalf("want ErrConfigLocked (Conflict), got %v", err)
	}
	if took < 300*time.Millisecond {
		t.Fatalf("gave up after %v, before the timeout", took)
	}
	if len(s.Accounts()) != 0 {
		t.Fatal("an account was saved without the lock")
	}

	// A context ends the wait early, even with a long timeout.
	shortConfigLockTimeout(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err, _ = callWithin(t, 10*time.Second, func() error {
		return s.SetSecretBackend(ctx, SecretBackendVault, testPW)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the context error, got %v", err)
	}

	// Once the other process lets go, writes succeed again.
	os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
	err, _ = callWithin(t, 30*time.Second, func() error {
		_, err := s.SaveAccount(Account{Name: "X", Credentials: CredAnonymous}, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLockVaultWriteGivesUpWhileAnotherProcessHoldsIt(t *testing.T) {
	const test = "TestLockVaultWriteGivesUpWhileAnotherProcessHoldsIt"
	if dir, id, _, ok := n2Child(test); ok {
		lockHolderChild(t, dir, id, func() (func(), error) {
			return lockConfigFile(context.Background(), filepath.Join(dir, vaultLockFileName))
		})
		return
	}
	dir := t.TempDir()
	v := newVault(filepath.Join(dir, vaultFileName))
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	shortConfigLockTimeout(t, 400*time.Millisecond)
	startLockHolder(t, test, dir)

	err, took := callWithin(t, 10*time.Second, func() error { return v.Set("k2", "v2") })
	if !errors.Is(err, ErrConfigLocked) || !IsKind(err, ErrConflict) {
		t.Fatalf("want ErrConfigLocked (Conflict), got %v", err)
	}
	if took < 300*time.Millisecond {
		t.Fatalf("gave up after %v, before the timeout", took)
	}
	// Reads fall back to what this instance already holds.
	if got, err := v.Get("k"); err != nil || got != "v" {
		t.Fatalf("read while another process holds the lock: %q, %v", got, err)
	}
	os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
	err, _ = callWithin(t, 30*time.Second, func() error { return v.Set("k2", "v2") })
	if err != nil {
		t.Fatal(err)
	}
}
