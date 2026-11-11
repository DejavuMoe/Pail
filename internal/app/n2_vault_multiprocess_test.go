package app

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file start copies of the test binary, so that the writers
// really are separate processes with their own in-memory vault state. Each
// child loads its state, reports ready and waits for a common start signal.

const (
	n2EnvDir  = "PAIL_N2_TEST_DIR"
	n2EnvID   = "PAIL_N2_TEST_ID"
	n2EnvMode = "PAIL_N2_TEST_MODE"
	n2NewPW   = "a different master password"
)

// n2Child reports whether this process is a child of the named test and, if
// so, returns its directory, id and mode.
func n2Child(test string) (dir, id, mode string, ok bool) {
	if os.Getenv(n2EnvDir) == "" || !strings.HasPrefix(os.Getenv(n2EnvMode), test+":") {
		return "", "", "", false
	}
	return os.Getenv(n2EnvDir), os.Getenv(n2EnvID), strings.TrimPrefix(os.Getenv(n2EnvMode), test+":"), true
}

func n2Ready(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready-"+id), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitAccountTestFile(t, filepath.Join(dir, "start"))
}

// runN2Children starts one child per mode, waits until all are ready, starts
// them together and fails the test if any child fails or hangs.
func runN2Children(t *testing.T, test, dir string, modes []string, limit time.Duration) {
	t.Helper()
	type result struct {
		id     int
		output []byte
		err    error
	}
	done := make(chan result, len(modes))
	for i, mode := range modes {
		cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.count=1")
		cmd.Env = append(os.Environ(), n2EnvDir+"="+dir, fmt.Sprintf("%s=%d", n2EnvID, i), n2EnvMode+"="+test+":"+mode)
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		t.Cleanup(func() { cmd.Process.Kill(); <-finished })
		go func() { err := cmd.Wait(); done <- result{i, output.Bytes(), err}; close(finished) }()
	}
	for i := range modes {
		waitAccountTestFile(t, filepath.Join(dir, fmt.Sprintf("ready-%d", i)))
	}
	if err := os.WriteFile(filepath.Join(dir, "start"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(limit)
	for range modes {
		select {
		case r := <-done:
			if r.err != nil {
				t.Errorf("child %d (%s): %v\n%s", r.id, modes[r.id], r.err, r.output)
			}
		case <-deadline:
			t.Fatalf("a child did not finish within %v (deadlock or lost lock?)", limit)
		}
	}
}

// TestN2VaultWritersAcrossProcesses: every Set that returned nil in any
// process must be in the final vault.json, and no Set may fail spuriously
// (on Windows the unserialised rename failed with "Access is denied").
func TestN2VaultWritersAcrossProcesses(t *testing.T) {
	const test = "TestN2VaultWritersAcrossProcesses"
	const perChild = 30
	if dir, id, _, ok := n2Child(test); ok {
		v := newVault(filepath.Join(dir, vaultFileName))
		if err := v.unlock(testPW); err != nil {
			t.Fatal(err)
		}
		log, err := os.Create(filepath.Join(dir, "acked-"+id))
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		n2Ready(t, dir, id)
		for i := range perChild {
			k := fmt.Sprintf("w%s-%d", id, i)
			if err := v.Set(k, "value-"+k); err != nil {
				t.Errorf("Set %d: %v", i, err) // keep going: the parent also counts lost acknowledged writes
				continue
			}
			fmt.Fprintln(log, k)
		}
		return
	}
	dir := t.TempDir()
	v := newVault(filepath.Join(dir, vaultFileName))
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	runN2Children(t, test, dir, []string{"set", "set", "set"}, 60*time.Second)
	check := newVault(v.path)
	if err := check.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	acked, lost := 0, 0
	for c := range 3 {
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("acked-%d", c)))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range strings.Fields(string(raw)) {
			acked++
			if got, err := check.Get(k); err != nil || got != "value-"+k {
				lost++
			}
		}
	}
	if acked != 3*perChild || lost > 0 {
		t.Fatalf("%d of %d writes acknowledged; %d acknowledged writes lost", acked, 3*perChild, lost)
	}
}

// TestN2VaultPasswordChangeAcrossProcesses: one process keeps writing while
// another changes the master password. The change must not be undone by a
// writer that still holds the old key, every write acknowledged before the
// change must survive it, and writes after it must fail with Conflict.
func TestN2VaultPasswordChangeAcrossProcesses(t *testing.T) {
	const test = "TestN2VaultPasswordChangeAcrossProcesses"
	if dir, id, mode, ok := n2Child(test); ok {
		v := newVault(filepath.Join(dir, vaultFileName))
		if err := v.unlock(testPW); err != nil {
			t.Fatal(err)
		}
		n2Ready(t, dir, id)
		switch mode {
		case "set":
			log, err := os.Create(filepath.Join(dir, "acked-"+id))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			// Keep writing until the password change is seen: a fixed number of
			// writes can finish before the other process changes the password.
			conflicts := 0
			deadline := time.Now().Add(30 * time.Second)
			for i := 0; conflicts < 5 && time.Now().Before(deadline); i++ {
				k := fmt.Sprintf("s%s-%d", id, i)
				err := v.Set(k, "v-"+k)
				switch {
				case err == nil:
					fmt.Fprintln(log, k)
				case IsKind(err, ErrConflict):
					conflicts++
				default:
					t.Fatalf("Set %s: %v", k, err)
				}
			}
			if conflicts == 0 {
				t.Fatal("writer never noticed the password change")
			}
		case "passwd":
			time.Sleep(150 * time.Millisecond)
			if err := v.changePassword(testPW, n2NewPW); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "changed"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	dir := t.TempDir()
	v := newVault(filepath.Join(dir, vaultFileName))
	if err := v.create(testPW); err != nil {
		t.Fatal(err)
	}
	runN2Children(t, test, dir, []string{"set", "set", "passwd"}, 60*time.Second)
	if t.Failed() {
		return
	}
	check := newVault(v.path)
	if err := check.unlock(n2NewPW); err != nil {
		t.Fatalf("the password change was lost (a stale writer overwrote it): %v", err)
	}
	for _, id := range []string{"0", "1"} {
		f, err := os.Open(filepath.Join(dir, "acked-"+id))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		n := 0
		for sc.Scan() {
			n++
			k := sc.Text()
			if got, err := check.Get(k); err != nil || got != "v-"+k {
				t.Errorf("acknowledged write %s lost: %q, %v", k, got, err)
			}
		}
		f.Close()
		t.Logf("writer %s: %d acknowledged writes", id, n)
	}
}

// TestN2AccountAndSecretWritersAcrossProcesses runs the two vault writer paths
// of the Service side by side in separate processes: SaveAccount (holds
// accounts.lock, then writes the vault) and SetSecret (writes the vault
// without accounts.lock). Neither may lose the other's vault entries, and the
// lock order accounts.lock -> vault lock must not deadlock.
func TestN2AccountAndSecretWritersAcrossProcesses(t *testing.T) {
	const test = "TestN2AccountAndSecretWritersAcrossProcesses"
	const perChild = 8
	if dir, id, mode, ok := n2Child(test); ok {
		s, _ := newTestServiceDir(t, dir, nil)
		if err := s.UnlockVault(testPW); err != nil {
			t.Fatal(err)
		}
		n2Ready(t, dir, id)
		for i := range perChild {
			switch mode {
			case "account":
				if _, err := s.SaveAccount(staticAccount(fmt.Sprintf("acct-%s-%d", id, i), "http://localhost:1"),
					&Secrets{SecretAccessKey: fmt.Sprintf("secret-%s-%d", id, i)}); err != nil {
					t.Fatalf("SaveAccount %d: %v", i, err)
				}
			case "secret":
				if err := s.SetSecret(fmt.Sprintf("ref-%s-%d", id, i), fmt.Sprintf("refval-%s-%d", id, i)); err != nil {
					t.Fatalf("SetSecret %d: %v", i, err)
				}
			}
		}
		return
	}
	dir := t.TempDir()
	s, _ := newTestServiceDir(t, dir, nil)
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	modes := []string{"account", "secret", "account", "secret"}
	runN2Children(t, test, dir, modes, 90*time.Second)
	if t.Failed() {
		return
	}
	check := newVault(filepath.Join(dir, vaultFileName))
	if err := check.unlock(testPW); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := newTestServiceDir(t, dir, nil)
	accounts := map[string]Account{}
	for _, a := range reloaded.Accounts() {
		accounts[a.Name] = a
	}
	for c, mode := range modes {
		for i := range perChild {
			switch mode {
			case "account":
				a, ok := accounts[fmt.Sprintf("acct-%d-%d", c, i)]
				if !ok {
					t.Errorf("account acct-%d-%d lost", c, i)
					continue
				}
				rec, _ := reloaded.account(a.ID)
				want := fmt.Sprintf("secret-%d-%d", c, i)
				if got, err := check.Get(accountSecretKey(rec.ID, rec.SecretGen)); err != nil || got != want {
					t.Errorf("secret of %s lost: %q, %v", a.Name, got, err)
				}
			case "secret":
				want := fmt.Sprintf("refval-%d-%d", c, i)
				if got, err := check.Get(refKey(fmt.Sprintf("ref-%d-%d", c, i))); err != nil || got != want {
					t.Errorf("vault entry of ref-%d-%d lost: %q, %v", c, i, got, err)
				}
			}
		}
	}
}
