package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/DejavuMoe/Pail/internal/app"
)

// vaultPasswordPrompt is shown on stderr before reading the master password.
const vaultPasswordPrompt = "Vault master password: "

// vaultMaxAttempts is how often an interactive prompt retries a wrong password.
const vaultMaxAttempts = 3

// needsVault reports whether the command about to run needs secrets: a static
// account's keys, or (cp, sync) secret references such as ZIP passwords used
// by upload rules. Accounts using profiles, default credentials or anonymous
// access never need the vault.
func (c *cli) needsVault(cmd string) bool {
	if c.svc.SecretBackend() != app.SecretBackendVault {
		return false
	}
	if c.acct != nil && c.acct.Credentials == app.CredStatic {
		return true
	}
	return (cmd == "cp" || cmd == "sync") && len(c.svc.SecretRefs()) > 0
}

// unlockVault opens the vault if the backend is the vault and it is locked. The
// password comes from the first line of stdin with --vault-password-stdin, or
// from a no-echo terminal prompt. It is never taken from a flag value or the
// environment (both are visible to other processes). Without either, the run
// fails with exit code 2 and a message saying how to supply it.
func (c *cli) unlockVault() error {
	if c.svc.SecretBackend() != app.SecretBackendVault || c.svc.VaultState().Unlocked {
		return nil
	}
	if !c.svc.VaultState().Exists {
		return &exitErr{exitFailure, "the encrypted vault file (vault.json) is missing; open the desktop app to reset it or switch back to the system keychain"}
	}
	if c.g.vaultPasswordStdin {
		line, err := c.in.ReadString('\n')
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			if err != nil || line == "" {
				return &exitErr{exitUsage, "--vault-password-stdin: no password on stdin (expected one line)"}
			}
			return &exitErr{exitUsage, "--vault-password-stdin: the password line is empty"}
		}
		return c.svc.UnlockVault(pw)
	}
	if c.io.ReadPassword == nil {
		return &exitErr{exitUsage, "the encrypted vault is locked and there is no terminal to ask for the master password; " +
			"pass --vault-password-stdin and send the password as the first line of stdin"}
	}
	var err error
	for i := 0; i < vaultMaxAttempts; i++ {
		var pw string
		if pw, err = c.io.ReadPassword(vaultPasswordPrompt); err != nil {
			return &exitErr{exitUsage, "could not read the master password: " + err.Error()}
		}
		if err = c.svc.UnlockVault(pw); err == nil || !app.IsKind(err, app.ErrAccessDenied) {
			return err
		}
		fmt.Fprintln(c.io.Stderr, "pail: wrong master password")
	}
	return err
}

func setupVault(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 || pos[0] != "status" {
			return usagef("usage: pail vault status [--vault-password-stdin]")
		}
		return c.vaultStatus()
	}
}

// vaultStatus reports the backend and the vault file. It never prints
// secrets. With --vault-password-stdin it also verifies the password (the
// vault is unlocked only for this process).
func (c *cli) vaultStatus() error {
	backend := c.svc.SecretBackend()
	st := c.svc.VaultState()
	verified := ""
	if c.g.vaultPasswordStdin {
		if !st.Exists {
			return &exitErr{exitFailure, "there is no vault to verify against"}
		}
		if err := c.unlockVault(); err != nil {
			return err
		}
		verified = "password accepted"
	}
	if c.g.json {
		o := map[string]any{"type": "vault", "backend": backend, "vaultExists": st.Exists,
			"autoLockMinutes": st.AutoLockMinutes}
		if verified != "" {
			o["passwordVerified"] = true
		}
		c.emit(o)
		c.emit(map[string]any{"type": "summary", "command": "vault status", "ok": true})
		return nil
	}
	out := c.io.Stdout
	fmt.Fprintf(out, "secret backend:  %s\n", backend)
	switch {
	case st.Exists:
		fmt.Fprintln(out, "vault file:      present (vault.json)")
	default:
		fmt.Fprintln(out, "vault file:      none")
	}
	if backend == app.SecretBackendVault {
		fmt.Fprintf(out, "auto-lock:       %s\n", autoLockText(st.AutoLockMinutes))
		fmt.Fprintln(out, "each run starts locked; commands that need credentials ask for the master password")
	}
	if verified != "" {
		fmt.Fprintln(out, verified)
	}
	return nil
}

func autoLockText(min int) string {
	if min == 0 {
		return "never (desktop app)"
	}
	return fmt.Sprintf("after %d idle minutes (desktop app)", min)
}
