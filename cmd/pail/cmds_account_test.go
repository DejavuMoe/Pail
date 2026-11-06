package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

const (
	newSecret   = "NEWSECRET-9876543210"
	tokenSecret = "SESSIONTOKEN-abcdef0123"
)

// noLeak fails when any of the secrets shows up in r.
func noLeak(t *testing.T, r result, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(r.out, s) || strings.Contains(r.errOut, s) {
			t.Fatalf("output leaks %q:\nstdout:%s\nstderr:%s", s, r.out, r.errOut)
		}
	}
}

func (h *harness) addSecond(extra ...string) result {
	h.t.Helper()
	args := append([]string{"account", "add", "second", "--endpoint", h.srv.URL, "--region", "us-east-1", "--path-style",
		"--access-key-id", "AKIASECOND", "--secret-stdin"}, extra...)
	return h.runCtx(context.Background(), newSecret+"\n", false, args...)
}

func (h *harness) accountID(name string) string {
	h.t.Helper()
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		h.t.Fatal(err)
	}
	defer svc.Shutdown(context.Background())
	for _, a := range svc.Accounts() {
		if a.Name == name {
			return a.ID
		}
	}
	h.t.Fatalf("no account %q", name)
	return ""
}

func TestAccountAddShowEditTestRemove(t *testing.T) {
	h := newHarness(t)
	all := []string{newSecret, secretKey}

	r := h.want(h.addSecond(), 0)
	noLeak(t, r, all...)
	if !strings.Contains(r.out, "added account second") {
		t.Fatalf("add output: %s", r.out)
	}
	id := h.accountID("second")
	// The secret reached the keyring, and only there.
	if v, err := h.kr.Get("account/" + id + "/secret"); err != nil || v != newSecret {
		t.Fatalf("stored secret = %q, %v", v, err)
	}
	// The new account works (the secret is read back from the keyring).
	r = h.want(h.run("--account", "second", "buckets"), 0)
	noLeak(t, r, all...)
	if !strings.Contains(r.out, "bkt") {
		t.Fatalf("buckets: %s", r.out)
	}
	r = h.want(h.run("accounts"), 0)
	if !strings.Contains(r.out, "second") || !strings.Contains(r.out, "test") {
		t.Fatalf("accounts: %s", r.out)
	}
	r = h.want(h.run("account", "ls"), 0)
	if !strings.Contains(r.out, "second") {
		t.Fatalf("account ls: %s", r.out)
	}
	ev := jsonLines(t, h.want(h.run("--json", "account", "ls"), 0).out)
	if lastSummary(t, h.run("--json", "account", "ls").out)["count"] != float64(2) || ev[0]["type"] != "account" {
		t.Fatalf("account ls json: %v", ev)
	}

	// show: no secrets, in text and JSON.
	r = h.want(h.run("account", "show", "second"), 0)
	noLeak(t, r, all...)
	for _, w := range []string{"second", id, "AKIASECOND", "stored", h.srv.URL, "path-style:"} {
		if !strings.Contains(r.out, w) {
			t.Fatalf("show lacks %q:\n%s", w, r.out)
		}
	}
	r = h.want(h.run("--json", "account", "show", "second"), 0)
	noLeak(t, r, all...)
	o := jsonLines(t, r.out)[0]
	if o["type"] != "account" || o["name"] != "second" || o["secretStored"] != true || o["pathStyle"] != true || o["accessKeyId"] != "AKIASECOND" {
		t.Fatalf("show json: %v", o)
	}

	// edit changes only the flags given.
	r = h.want(h.run("account", "edit", "second", "--region", "eu-west-1", "--cdn-url", "https://cdn.example.com", "--cdn-bucket", "bkt"), 0)
	noLeak(t, r, all...)
	o = jsonLines(t, h.run("--json", "account", "show", "second").out)[0]
	if o["region"] != "eu-west-1" || o["cdnUrl"] != "https://cdn.example.com" || o["endpoint"] != h.srv.URL ||
		o["accessKeyId"] != "AKIASECOND" || o["pathStyle"] != true || len(o["cdnBuckets"].([]any)) != 1 {
		t.Fatalf("edit result: %v", o)
	}
	if v, _ := h.kr.Get("account/" + id + "/secret"); v != newSecret {
		t.Fatal("edit without --set-secret changed the secret")
	}
	h.want(h.run("account", "edit", "second", "--path-style=false", "--cdn-url", "", "--cdn-bucket", "", "--rename", "renamed"), 0)
	o = jsonLines(t, h.run("--json", "account", "show", "renamed").out)[0]
	if o["pathStyle"] == true || o["cdnUrl"] != "" || o["name"] != "renamed" || o["id"] != id {
		t.Fatalf("second edit: %v", o)
	}
	h.want(h.run("account", "edit", "renamed", "--path-style"), 0)

	// Replacing the secret: only with --set-secret, read from stdin or a prompt.
	h.want(h.run("account", "edit", "renamed"), 2)
	h.want(h.runCtx(context.Background(), "x\n", false, "account", "edit", "renamed", "--secret-stdin"), 2)
	r = h.want(h.runCtx(context.Background(), "ROTATED-SECRET-1\n"+tokenSecret+"\n", false,
		"account", "edit", "renamed", "--set-secret", "--secret-stdin", "--ask-session-token"), 0)
	noLeak(t, r, "ROTATED-SECRET-1", tokenSecret, newSecret)
	if v, err := h.kr.Get("account/" + id + "/secret.1"); err != nil || v != "ROTATED-SECRET-1" {
		t.Fatalf("rotated secret = %q, %v", v, err)
	}
	if v, err := h.kr.Get("account/" + id + "/session.1"); err != nil || v != tokenSecret {
		t.Fatalf("session token = %q, %v", v, err)
	}
	if _, err := h.kr.Get("account/" + id + "/secret"); err == nil {
		t.Fatal("the old secret entry was not removed")
	}
	asked := ""
	rp := func(p string) (string, error) { asked += p; return "PROMPTED-SECRET-2", nil }
	r = h.want(h.runWith("", rp, "account", "edit", "renamed", "--set-secret"), 0)
	noLeak(t, r, "PROMPTED-SECRET-2")
	if !strings.Contains(asked, "Secret access key") {
		t.Fatalf("prompt: %q", asked)
	}
	if v, _ := h.kr.Get("account/" + id + "/secret.2"); v != "PROMPTED-SECRET-2" {
		t.Fatalf("prompted secret = %q", v)
	}

	// test: success, then failure against a dead endpoint.
	r = h.want(h.run("account", "test", "renamed"), 0)
	if !strings.Contains(r.out, "ok") {
		t.Fatalf("test: %s", r.out)
	}
	lastSummary(t, h.want(h.run("--json", "account", "test", "renamed"), 0).out)
	h.want(h.run("account", "edit", "renamed", "--endpoint", "http://127.0.0.1:1"), 0)
	h.want(h.run("account", "test", "renamed"), 1)
	h.want(h.run("account", "edit", "renamed", "--endpoint", h.srv.URL), 0)

	// rm needs confirmation.
	r = h.want(h.run("account", "rm", "renamed"), 2)
	if !strings.Contains(r.errOut, "--yes") || !strings.Contains(r.errOut, "renamed") {
		t.Fatalf("rm refusal: %s", r.errOut)
	}
	h.accountID("renamed") // still there
	h.want(h.runWith("n\n", rp, "account", "rm", "renamed"), 1)
	h.accountID("renamed")
	h.want(h.runWith("y\n", rp, "account", "rm", "renamed"), 0)
	if _, err := h.kr.Get("account/" + id + "/secret.2"); err == nil {
		t.Fatal("rm left the secret in the keyring")
	}
	if r := h.want(h.run("accounts"), 0); strings.Contains(r.out, "renamed") {
		t.Fatalf("account still listed: %s", r.out)
	}
	h.want(h.run("account", "rm", "nobody", "--yes"), 2)
	// --yes skips the question; the surviving account is the default again.
	h.want(h.run("account", "rm", "--yes"), 0)
	r = h.want(h.run("account", "ls"), 0)
	if strings.Contains(r.out, "test ") {
		t.Fatalf("last account not removed: %s", r.out)
	}
	h.want(h.run("buckets"), 2) // no accounts any more
}

func TestAccountAddSecretHandling(t *testing.T) {
	h := newHarness(t)
	// A secret can never be passed as a flag.
	h.want(h.run("account", "add", "x", "--endpoint", h.srv.URL, "--access-key-id", "A", "--secret", newSecret), 2)
	h.want(h.run("account", "add", "x", "--endpoint", h.srv.URL, "--access-key-id", "A", "--secret-access-key", newSecret), 2)
	// Neither stdin flag nor terminal: refuse, never guess.
	r := h.want(h.run("account", "add", "x", "--endpoint", h.srv.URL, "--access-key-id", "A"), 2)
	if !strings.Contains(r.errOut, "--secret-stdin") {
		t.Fatalf("message: %s", r.errOut)
	}
	// Empty line.
	h.want(h.runCtx(context.Background(), "\n", false, "account", "add", "x", "--endpoint", h.srv.URL, "--access-key-id", "A", "--secret-stdin"), 2)
	h.want(h.runCtx(context.Background(), "", false, "account", "add", "x", "--endpoint", h.srv.URL, "--access-key-id", "A", "--secret-stdin"), 2)
	// Hidden prompt.
	var prompts []string
	rp := func(p string) (string, error) { prompts = append(prompts, p); return newSecret, nil }
	r = h.want(h.runWith("", rp, "account", "add", "prompted", "--endpoint", h.srv.URL, "--region", "us-east-1", "--path-style", "--access-key-id", "AKIAP"), 0)
	noLeak(t, r, newSecret)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Secret access key") {
		t.Fatalf("prompts: %q", prompts)
	}
	// A failing prompt is reported.
	bad := func(string) (string, error) { return "", errors.New("terminal closed") }
	r = h.want(h.runWith("", bad, "account", "add", "p2", "--endpoint", h.srv.URL, "--access-key-id", "A"), 2)
	if !strings.Contains(r.errOut, "terminal closed") {
		t.Fatal(r.errOut)
	}
	// Duplicate names, missing key ID and unknown credentials are usage errors raised before any prompt.
	asked := false
	spy := func(string) (string, error) { asked = true; return newSecret, nil }
	h.want(h.runWith("", spy, "account", "add", "TEST", "--endpoint", h.srv.URL, "--access-key-id", "A"), 2)
	h.want(h.runWith("", spy, "account", "add", "nokey", "--endpoint", h.srv.URL), 2)
	h.want(h.runWith("", spy, "account", "add", "weird", "--endpoint", h.srv.URL, "--credentials", "magic"), 2)
	h.want(h.runWith("", spy, "account", "add", "ep", "--provider", "s3", "--access-key-id", "A"), 2)
	if asked {
		t.Fatal("asked for a secret before validating the account")
	}
	// A failed connection test saves nothing; --no-test saves anyway.
	n := len(app2accounts(t, h))
	r = h.want(h.runCtx(context.Background(), newSecret+"\n", false, "account", "add", "dead", "--endpoint", "http://127.0.0.1:1",
		"--access-key-id", "A", "--secret-stdin"), 1)
	noLeak(t, r, newSecret)
	if !strings.Contains(r.errOut, "nothing was saved") || len(app2accounts(t, h)) != n {
		t.Fatalf("failed test must not save: %s", r.errOut)
	}
	h.want(h.runCtx(context.Background(), newSecret+"\n", false, "account", "add", "dead", "--endpoint", "http://127.0.0.1:1",
		"--access-key-id", "A", "--secret-stdin", "--no-test"), 0)
	// Non-static accounts take no secret, and the stdin flags are refused for them.
	h.want(h.run("account", "add", "prof", "--profile", "work", "--region", "us-east-1", "--no-test", "--secret-stdin"), 2)
	r = h.want(h.run("--json", "account", "add", "prof", "--profile", "work", "--region", "us-east-1", "--no-test"), 0)
	if lastSummary(t, r.out)["name"] != "prof" {
		t.Fatalf("json add: %s", r.out)
	}
	o := jsonLines(t, h.run("--json", "account", "show", "prof").out)[0]
	if o["credentials"] != "profile" || o["profile"] != "work" {
		t.Fatalf("profile account: %v", o)
	}
	if _, ok := o["secretStored"]; ok {
		t.Fatal("secretStored only applies to static accounts")
	}
	// Flags that belong elsewhere are refused.
	h.want(h.run("account", "ls", "--region", "x"), 2)
	h.want(h.run("account", "rm", "prof", "--endpoint", "x"), 2)
	h.want(h.run("account", "edit", "prof", "--no-test", "--region", "x"), 2)
	h.want(h.run("account", "add", "q", "--rename", "z"), 2)
	// An existing account's credentials can switch away from static, which deletes the secret.
	id := h.accountID("prompted")
	h.want(h.run("account", "edit", "prompted", "--credentials", "anonymous"), 0)
	if _, err := h.kr.Get("account/" + id + "/secret"); err == nil {
		t.Fatal("secret survived the switch to anonymous")
	}
	h.want(h.run("account", "edit", "prompted", "--credentials", "static", "--access-key-id", "A"), 2) // needs --set-secret
}

func app2accounts(t *testing.T, h *harness) []app.Account {
	t.Helper()
	svc, err := app.New(app.Options{ConfigDir: h.dir, Keyring: h.kr})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Shutdown(context.Background())
	return svc.Accounts()
}

func TestAccountsInVaultNeedThePassword(t *testing.T) {
	h := vaultHarness(t)
	// Writing a secret unlocks the vault first (password line, then secret line).
	r := h.want(h.runCtx(context.Background(), vaultTestPW+"\n"+newSecret+"\n", false,
		"account", "add", "second", "--endpoint", h.srv.URL, "--region", "us-east-1", "--path-style",
		"--access-key-id", "AKIASECOND", "--secret-stdin", "--vault-password-stdin"), 0)
	noLeak(t, r, newSecret, vaultTestPW, secretKey)
	r = h.want(h.run("account", "add", "third", "--endpoint", h.srv.URL, "--access-key-id", "A", "--secret-stdin"), 2)
	if !strings.Contains(r.errOut, "vault is locked") {
		t.Fatalf("locked vault: %s", r.errOut)
	}
	// Reading configuration does not need the vault.
	h.want(h.run("account", "show", "second"), 0)
	// Testing and removing a static account does.
	h.want(h.run("account", "test", "second"), 2)
	h.want(h.runCtx(context.Background(), vaultTestPW+"\n", false, "account", "test", "second", "--vault-password-stdin"), 0)
	h.want(h.runCtx(context.Background(), vaultTestPW+"\n", false, "account", "rm", "second", "--yes", "--vault-password-stdin"), 0)
}

// ---- vault ----------------------------------------------------------------------------------

func TestVaultInitPasswdBackendFlow(t *testing.T) {
	h := newHarness(t)
	const pw1, pw2 = "first vault password", "second vault password"
	leaks := []string{pw1, pw2, secretKey}

	h.want(h.runCtx(context.Background(), "short\n", false, "vault", "init", "--vault-password-stdin"), 2) // under 8 characters
	h.want(h.run("vault", "init"), 2)                                                                      // no terminal, no stdin flag
	r := h.want(h.runCtx(context.Background(), pw1+"\n", false, "vault", "init", "--vault-password-stdin"), 0)
	noLeak(t, r, leaks...)
	r = h.want(h.run("vault", "status"), 0)
	if !strings.Contains(r.out, "secret backend:  vault") || !strings.Contains(r.out, "present") {
		t.Fatalf("status after init: %s", r.out)
	}
	// Credentials now need the password.
	h.want(h.run("buckets"), 2)
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "buckets", "--vault-password-stdin"), 0)
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "vault", "init", "--vault-password-stdin"), 1) // already in use

	// unlock verifies; lock explains.
	r = h.want(h.runCtx(context.Background(), pw1+"\n", false, "vault", "unlock", "--vault-password-stdin"), 0)
	if !strings.Contains(r.out, "accepted") {
		t.Fatalf("unlock: %s", r.out)
	}
	r = h.want(h.runCtx(context.Background(), "nope nope nope\n", false, "vault", "unlock", "--vault-password-stdin"), 1)
	noLeak(t, r, "nope nope nope")
	h.want(h.run("vault", "unlock"), 2)
	r = h.want(h.run("vault", "lock"), 0)
	if !strings.Contains(r.out, "starts with the vault locked") {
		t.Fatalf("lock: %s", r.out)
	}
	if ev := lastSummary(t, h.want(h.run("--json", "vault", "lock"), 0).out); ev["command"] != "vault lock" || ev["ok"] != true {
		t.Fatalf("lock json: %v", ev)
	}

	// passwd: current line, then new line.
	h.want(h.runCtx(context.Background(), "wrong password!\n"+pw2+"\n", false, "vault", "passwd", "--vault-password-stdin", "--new-vault-password-stdin"), 1)
	h.want(h.runCtx(context.Background(), pw1+"\nshort\n", false, "vault", "passwd", "--vault-password-stdin", "--new-vault-password-stdin"), 2)
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "vault", "passwd", "--vault-password-stdin", "--new-vault-password-stdin"), 2)
	r = h.want(h.runCtx(context.Background(), pw1+"\n"+pw2+"\n", false, "vault", "passwd", "--vault-password-stdin", "--new-vault-password-stdin"), 0)
	noLeak(t, r, leaks...)
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "buckets", "--vault-password-stdin"), 1)
	h.want(h.runCtx(context.Background(), pw2+"\n", false, "buckets", "--vault-password-stdin"), 0)
	// Interactive passwd: current, new, repeat.
	var prompts []string
	answers := []string{pw2, pw1, pw1}
	rp := func(p string) (string, error) {
		prompts = append(prompts, p)
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	r = h.want(h.runWith("", rp, "vault", "passwd"), 0)
	noLeak(t, r, leaks...)
	if len(prompts) != 3 || !strings.Contains(prompts[0], "Current") || !strings.Contains(prompts[2], "Repeat") {
		t.Fatalf("prompts: %q", prompts)
	}
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "buckets", "--vault-password-stdin"), 0)
	// A repeat that differs changes nothing.
	answers = []string{pw1, pw2, "different password"}
	r = h.want(h.runWith("", rp, "vault", "passwd"), 2)
	if !strings.Contains(r.errOut, "differ") {
		t.Fatalf("mismatch: %s", r.errOut)
	}
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "buckets", "--vault-password-stdin"), 0)

	// backend: unknown names are usage errors; keychain moves the secrets back.
	h.want(h.run("vault", "backend", "cloud"), 2)
	h.want(h.run("vault", "backend"), 2)
	h.want(h.run("vault", "backend", "keychain"), 2) // locked, no password source
	r = h.want(h.runCtx(context.Background(), pw1+"\n", false, "vault", "backend", "keychain", "--vault-password-stdin"), 0)
	noLeak(t, r, leaks...)
	r = h.want(h.run("vault", "status"), 0)
	if !strings.Contains(r.out, "secret backend:  keyring") || !strings.Contains(r.out, "none") {
		t.Fatalf("status after switching back: %s", r.out)
	}
	h.want(h.run("buckets"), 0)
	h.want(h.run("vault", "backend", "keychain"), 0) // already the case
	h.want(h.run("vault", "unlock"), 1)              // no vault to unlock
	h.want(h.run("vault", "passwd"), 1)

	// backend vault creates the vault again, with a confirmed prompt.
	answers = []string{pw1, pw1}
	h.want(h.runWith("", rp, "vault", "backend", "vault"), 0)
	if r := h.want(h.run("vault", "status"), 0); !strings.Contains(r.out, "secret backend:  vault") {
		t.Fatalf("vault backend vault did not switch: %s", r.out)
	}
	h.want(h.runCtx(context.Background(), pw1+"\n", false, "buckets", "--vault-password-stdin"), 0)
}

func TestVaultReset(t *testing.T) {
	h := vaultHarness(t)
	rp := func(string) (string, error) { return "", errors.New("must not be asked") }

	// Without a terminal and without --yes: refused, nothing happens.
	r := h.want(h.run("vault", "reset"), 2)
	if !strings.Contains(r.errOut, "--yes") || !strings.Contains(r.errOut, "test") {
		t.Fatalf("scope and refusal expected: %s", r.errOut)
	}
	if !strings.Contains(h.run("vault", "status").out, "secret backend:  vault") {
		t.Fatal("refused reset changed the backend")
	}
	// On a terminal the word RESET must be typed.
	r = h.want(h.runWith("yes\n", rp, "vault", "reset"), 1)
	if !strings.Contains(r.errOut, "Type RESET") || !strings.Contains(r.errOut, "aborted") {
		t.Fatalf("typed confirmation: %s", r.errOut)
	}
	h.want(h.runWith("reset\n", rp, "vault", "reset"), 1) // case matters
	r = h.want(h.runWith("RESET\n", rp, "vault", "reset"), 0)
	if !strings.Contains(r.out, "1 account(s) need their secret key") {
		t.Fatalf("reset output: %s", r.out)
	}
	r = h.want(h.run("vault", "status"), 0)
	if !strings.Contains(r.out, "secret backend:  keyring") || !strings.Contains(r.out, "none") {
		t.Fatalf("status after reset: %s", r.out)
	}
	if o := jsonLines(t, h.run("--json", "account", "show", "test").out)[0]; o["secretStored"] != false {
		t.Fatalf("the account must need its secret again: %v", o)
	}
	h.want(h.run("vault", "reset", "--yes"), 1) // nothing left to reset

	// --yes skips the question.
	h2 := vaultHarness(t)
	h2.want(h2.run("vault", "reset", "--yes"), 0)
	h2.want(h2.run("--json", "vault", "reset", "--yes"), 1)
}
