package app

// Cross-process locks for configuration files.
//
// Several Pail processes (the desktop app, command-line runs) may share one
// configuration directory. Read-modify-write transactions on a shared file
// are serialised with an operating-system lock on a stable sidecar file (the
// data file itself is replaced by rename on every write, so its identity
// changes). The sidecars are never deleted; the OS drops a lock when its
// process exits.
//
//	accounts.lock  account writers: accounts.json plus the account secrets
//	               (accountwrite.go)
//	vault.lock     every read and write of vault.json (vault.go)
//
// Lock order: accounts.lock before vault.lock, never the reverse. The vault
// lock is a leaf: it is only held inside a vault method, which never calls
// back into the Service. So SaveAccount / SetSecretBackend / ResetVault
// (accounts.lock, then vault.lock for each vault call) and SetSecret or any
// secret read (vault.lock alone) cannot deadlock, within a process or across
// processes.
//
// Acquiring waits at most configLockTimeout and honours a context, so that a
// process stuck while holding a lock (for example behind a keychain prompt)
// makes the others fail with ErrConfigLocked instead of hanging forever.

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	accountsLockFileName = "accounts.lock"
	vaultLockFileName    = "vault.lock"

	// defaultConfigLockTimeout bounds every wait for a configuration lock.
	// Writers hold the locks for milliseconds, except an account writer
	// waiting for a keychain prompt; giving up after this long tells the user
	// what is going on instead of freezing the save.
	defaultConfigLockTimeout = 15 * time.Second

	// Polling interval while another holder keeps a lock (doubling, capped).
	lockPollMin = time.Millisecond
	lockPollMax = 20 * time.Millisecond
)

// configLockTimeout is the wait bound in nanoseconds; tests shorten it.
var configLockTimeout atomic.Int64

func init() { configLockTimeout.Store(int64(defaultConfigLockTimeout)) }

// ErrConfigLocked is reachable with errors.Is from the error of any write that
// gave up waiting for another Pail process to finish saving. The OpError
// carrying it has Kind ErrConflict; the operation can simply be retried.
var ErrConfigLocked = errors.New("another Pail process is still saving settings; try again")

func configLockedErr() *OpError {
	return &OpError{Kind: ErrConflict, Message: ErrConfigLocked.Error(), err: ErrConfigLocked}
}

func configLockDeadline() time.Time {
	return time.Now().Add(time.Duration(configLockTimeout.Load()))
}

// pollUntil calls try until it reports success, fails, ctx ends (ctx.Err()) or
// deadline passes (ErrConfigLocked). try runs at least once.
func pollUntil(ctx context.Context, deadline time.Time, try func() (bool, error)) error {
	delay := lockPollMin
	for {
		ok, err := try()
		if err != nil || ok {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return configLockedErr()
		}
		t := time.NewTimer(min(delay, wait))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		delay = min(2*delay, lockPollMax)
	}
}

// lockMutex takes an in-process mutex with the same bound as the file lock
// it guards, for mutexes that may be held across a keychain prompt.
func lockMutex(ctx context.Context, mu *sync.Mutex, deadline time.Time) error {
	return pollUntil(ctx, deadline, func() (bool, error) { return mu.TryLock(), nil })
}

// lockFileUntil takes the exclusive OS lock on path (created if missing) and
// returns its release function, which unlocks explicitly before closing.
func lockFileUntil(ctx context.Context, path string, deadline time.Time) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := pollUntil(ctx, deadline, func() (bool, error) { return tryLockFile(f) }); err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockFile(f)
			f.Close()
		})
	}, nil
}

// lockConfigFile is lockFileUntil with the default bound.
func lockConfigFile(ctx context.Context, path string) (func(), error) {
	return lockFileUntil(ctx, path, configLockDeadline())
}

// writeJSONAtomicRetry is writeJSONAtomic that retries, for well under a
// second, the replace failures Windows reports while another program has the
// target open for a moment, including another Pail process replacing the same
// file. Every configuration file is written through it. Accounts and the
// vault are also serialised by their file locks, so a retry never reorders
// two of their writers; the other files keep last-writer-wins semantics.
func writeJSONAtomicRetry(path string, v any) error {
	delay := 10 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := writeJSONAtomic(path, v)
		if err == nil || attempt >= 7 || !isTransientReplaceError(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
}
