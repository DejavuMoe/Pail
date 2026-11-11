package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountWritersPreserveOtherInstances(t *testing.T) {
	for _, operation := range []string{"save", "delete", "alias"} {
		t.Run(operation, func(t *testing.T) {
			kr := NewMemoryKeyring()
			a, dir := newTestServiceDir(t, "", kr)
			x, err := a.SaveAccount(Account{Name: "X", Credentials: CredAnonymous}, nil)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := newTestServiceDir(t, dir, kr)
			y, err := b.SaveAccount(Account{Name: "Y", Credentials: CredAnonymous}, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "save":
				x.Name = "X renamed"
				_, err = a.SaveAccount(x, nil)
			case "delete":
				err = a.DeleteAccount(x.ID)
			case "alias":
				err = a.SetBucketAlias(x.ID, "bucket", "photos")
			}
			if err != nil {
				t.Fatal(err)
			}
			reloaded, _ := newTestServiceDir(t, dir, kr)
			if _, ok := reloaded.account(y.ID); !ok {
				t.Fatalf("%s discarded the other instance's account Y: %+v", operation, reloaded.Accounts())
			}
			got, exists := reloaded.account(x.ID)
			if operation == "delete" && exists || operation == "save" && got.Name != x.Name || operation == "alias" && got.BucketAliases["bucket"] != "photos" {
				t.Fatalf("requested %s was not applied: %+v", operation, got)
			}
		})
	}
}

func TestAccountWriterUsesLatestSecretGeneration(t *testing.T) {
	kr := NewMemoryKeyring()
	a, dir := newTestServiceDir(t, "", kr)
	x, err := a.SaveAccount(staticAccount("X", "http://localhost:1"), &Secrets{SecretAccessKey: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newTestServiceDir(t, dir, kr)
	if _, err := b.SaveAccount(x, &Secrets{SecretAccessKey: "replacement"}); err != nil {
		t.Fatal(err)
	}
	x.Name = "renamed"
	if _, err := a.SaveAccount(x, nil); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := newTestServiceDir(t, dir, kr)
	rec, _ := reloaded.account(x.ID)
	sec, err := reloaded.readSecrets(testCtx(t), rec)
	if err != nil || sec.SecretAccessKey != "replacement" {
		t.Fatalf("stale writer lost the current credentials: generation=%d err=%v", rec.SecretGen, err)
	}
}

func TestAccountWriterRejectsChangedSecretBackend(t *testing.T) {
	kr := NewMemoryKeyring()
	a, dir := newTestServiceDir(t, "", kr)
	x, err := a.SaveAccount(staticAccount("X", "http://localhost:1"), &Secrets{SecretAccessKey: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newTestServiceDir(t, dir, kr)
	if err := b.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveAccount(x, &Secrets{SecretAccessKey: "stale-backend"}); !IsKind(err, ErrConflict) {
		t.Fatalf("stale backend writer should fail without touching credentials: %v", err)
	}
	if err := b.vlt.verify(map[string]string{accountSecretKey(x.ID, 0): "initial"}); err != nil {
		t.Fatal(err)
	}
}

func TestAccountBackendWritersPreserveOtherInstances(t *testing.T) {
	for _, operation := range []string{"migrate", "reset"} {
		t.Run(operation, func(t *testing.T) {
			kr := NewMemoryKeyring()
			a, dir := newTestServiceDir(t, "", kr)
			if operation == "reset" {
				if err := a.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := newTestServiceDir(t, dir, kr)
			if operation == "reset" {
				if err := b.UnlockVault(testPW); err != nil {
					t.Fatal(err)
				}
			}
			y, err := b.SaveAccount(staticAccount("Y", "http://localhost:1"), &Secrets{SecretAccessKey: "keep-this"})
			if err != nil {
				t.Fatal(err)
			}
			if operation == "migrate" {
				err = a.SetSecretBackend(testCtx(t), SecretBackendVault, testPW)
			} else {
				_, err = a.ResetVault()
			}
			if err != nil {
				t.Fatal(err)
			}
			reloaded, _ := newTestServiceDir(t, dir, kr)
			rec, ok := reloaded.account(y.ID)
			if !ok {
				t.Fatal("backend writer discarded account Y")
			}
			if operation == "migrate" {
				if err := a.vlt.verify(map[string]string{accountSecretKey(y.ID, rec.SecretGen): "keep-this"}); err != nil {
					t.Fatal(err)
				}
			} else if rec.HasSecret {
				t.Fatal("reset left an account claiming a destroyed secret")
			}
		})
	}
}

// Both children load the same empty snapshot before either writes anything.
func TestAccountWritersAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("PAIL_ACCOUNT_WRITER_TEST_DIR"); dir != "" {
		s, _ := newTestServiceDir(t, dir, nil)
		id := os.Getenv("PAIL_ACCOUNT_WRITER_TEST_ID")
		if err := os.WriteFile(filepath.Join(dir, "ready-"+id), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		waitAccountTestFile(t, filepath.Join(dir, "start"))
		for i := range 8 {
			if _, err := s.SaveAccount(Account{Name: fmt.Sprintf("%s-%d", id, i), Credentials: CredAnonymous}, nil); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	dir := t.TempDir()
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 2)
	for i := range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAccountWritersAcrossProcesses$")
		cmd.Env = append(os.Environ(), "PAIL_ACCOUNT_WRITER_TEST_DIR="+dir, fmt.Sprintf("PAIL_ACCOUNT_WRITER_TEST_ID=%d", i))
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		t.Cleanup(func() { cmd.Process.Kill(); <-finished })
		go func() { err := cmd.Wait(); done <- result{output.Bytes(), err}; close(finished) }()
		waitAccountTestFile(t, filepath.Join(dir, fmt.Sprintf("ready-%d", i)))
	}
	if err := os.WriteFile(filepath.Join(dir, "start"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case r := <-done:
			if r.err != nil {
				t.Errorf("child: %v\n%s", r.err, r.output)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("account writer child timed out")
		}
	}
	s, _ := newTestServiceDir(t, dir, nil)
	if got := len(s.Accounts()); got != 16 {
		t.Fatalf("concurrent writers lost accounts: got %d, want 16", got)
	}
}

func waitAccountTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", filepath.Base(path))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
