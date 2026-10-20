package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

const vaultTestPW = "cli vault password 1"

// vaultHarness is a harness whose secrets live in the encrypted vault.
func vaultHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSecretBackend(context.Background(), app.SecretBackendVault, vaultTestPW); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown(context.Background())
	return h
}

func (h *harness) runWith(stdin string, readPW func(string) (string, error), args ...string) result {
	var out, errb bytes.Buffer
	code := run(context.Background(), append([]string{"--config-dir", h.dir}, args...), IO{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb, StdinTTY: readPW != nil, Keyring: h.kr, ReadPassword: readPW,
	})
	return result{code, out.String(), errb.String()}
}

func noSecretLeak(t *testing.T, r result) {
	t.Helper()
	for _, s := range []string{vaultTestPW, secretKey, "wrong vault password"} {
		if strings.Contains(r.out, s) || strings.Contains(r.errOut, s) {
			t.Fatalf("output leaks %q:\nstdout:%s\nstderr:%s", s, r.out, r.errOut)
		}
	}
}

func TestVaultLockedWithoutPasswordSourceExits2(t *testing.T) {
	h := vaultHarness(t)
	r := h.want(h.runWith("", nil, "buckets"), 2)
	if !strings.Contains(r.errOut, "vault is locked") || !strings.Contains(r.errOut, "--vault-password-stdin") {
		t.Fatalf("message: %s", r.errOut)
	}
	noSecretLeak(t, r)
	// JSON mode reports the same.
	r = h.want(h.runWith("", nil, "--json", "buckets"), 2)
	if ev := jsonLines(t, r.out); len(ev) != 1 || ev[0]["type"] != "error" || ev[0]["exitCode"] != float64(2) {
		t.Fatalf("json: %s", r.out)
	}
}

func TestVaultPasswordFromStdin(t *testing.T) {
	h := vaultHarness(t)
	r := h.want(h.runWith(vaultTestPW+"\n", nil, "--vault-password-stdin", "buckets"), 0)
	if !strings.Contains(r.out, "bkt") {
		t.Fatalf("buckets: %s", r.out)
	}
	noSecretLeak(t, r)
	// CRLF line ending (Windows pipes).
	h.want(h.runWith(vaultTestPW+"\r\n", nil, "--vault-password-stdin", "buckets"), 0)
	// The flag may follow the command.
	h.want(h.runWith(vaultTestPW+"\n", nil, "buckets", "--vault-password-stdin"), 0)

	r = h.want(h.runWith("wrong vault password\n", nil, "--vault-password-stdin", "buckets"), 1)
	if !strings.Contains(r.errOut, "access denied") || !strings.Contains(r.errOut, "wrong master password") {
		t.Fatalf("wrong password: %s", r.errOut)
	}
	noSecretLeak(t, r)
	r = h.want(h.runWith("", nil, "--vault-password-stdin", "buckets"), 2)
	if !strings.Contains(r.errOut, "no password on stdin") {
		t.Fatalf("empty stdin: %s", r.errOut)
	}
	h.want(h.runWith("\n", nil, "--vault-password-stdin", "buckets"), 2)
}

func TestVaultInteractivePrompt(t *testing.T) {
	h := vaultHarness(t)
	var prompts []string
	answers := []string{"wrong vault password", vaultTestPW}
	rp := func(p string) (string, error) {
		prompts = append(prompts, p)
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	r := h.want(h.runWith("", rp, "buckets"), 0)
	if len(prompts) != 2 || !strings.HasPrefix(prompts[0], "Vault master password") {
		t.Fatalf("prompts: %q", prompts)
	}
	if !strings.Contains(r.errOut, "wrong master password") {
		t.Fatalf("no retry hint: %s", r.errOut)
	}
	noSecretLeak(t, r)

	// Three wrong answers give up with access denied.
	n := 0
	rp = func(string) (string, error) { n++; return "wrong vault password", nil }
	r = h.want(h.runWith("", rp, "buckets"), 1)
	if n != 3 || !strings.Contains(r.errOut, "access denied") {
		t.Fatalf("attempts=%d: %s", n, r.errOut)
	}
	// A failing terminal read is reported, not retried.
	rp = func(string) (string, error) { return "", errors.New("terminal closed") }
	r = h.want(h.runWith("", rp, "buckets"), 2)
	if !strings.Contains(r.errOut, "terminal closed") {
		t.Fatalf("%s", r.errOut)
	}
}

func TestVaultNotAskedWhenNotNeeded(t *testing.T) {
	h := vaultHarness(t)
	asked := false
	rp := func(string) (string, error) { asked = true; return "", errors.New("must not be asked") }
	r := h.want(h.runWith("", rp, "accounts"), 0)
	if asked || !strings.Contains(r.out, "test") {
		t.Fatalf("accounts must not need the vault (asked=%v): %s", asked, r.out)
	}
	h.want(h.runWith("", rp, "vault", "status"), 0)
	if asked {
		t.Fatal("vault status asked for the password")
	}
}

func TestVaultStatus(t *testing.T) {
	h := vaultHarness(t)
	r := h.want(h.runWith("", nil, "vault", "status"), 0)
	for _, w := range []string{"secret backend:  vault", "present", "after 15 idle minutes"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("missing %q in:\n%s", w, r.out)
		}
	}
	r = h.want(h.runWith("", nil, "--json", "vault", "status"), 0)
	ev := jsonLines(t, r.out)
	if len(ev) != 2 || ev[0]["backend"] != "vault" || ev[0]["vaultExists"] != true || ev[0]["autoLockMinutes"] != float64(15) {
		t.Fatalf("json: %s", r.out)
	}
	// Verify the password without running anything else.
	r = h.want(h.runWith(vaultTestPW+"\n", nil, "vault", "status", "--vault-password-stdin"), 0)
	if !strings.Contains(r.out, "password accepted") {
		t.Fatalf("%s", r.out)
	}
	h.want(h.runWith("wrong vault password\n", nil, "vault", "status", "--vault-password-stdin"), 1)
	h.want(h.runWith("", nil, "vault", "bogus"), 2)

	// Keychain backend: status says so and nothing is asked.
	h2 := newHarness(t)
	r = h2.want(h2.runWith("", nil, "vault", "status"), 0)
	if !strings.Contains(r.out, "secret backend:  keyring") || !strings.Contains(r.out, "none") {
		t.Fatalf("%s", r.out)
	}
}

func TestKeychainBackendUnaffected(t *testing.T) {
	h := newHarness(t)
	// --vault-password-stdin with a keychain backend must not consume stdin.
	h.want(h.runWith("ignored\n", nil, "--vault-password-stdin", "buckets"), 0)
}
