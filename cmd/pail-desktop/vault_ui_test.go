package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The vault UI runs against a real vault.json in the harness's temp directory
// (Argon2id at its production cost, a few tenths of a second per open).

const vaultPW = "correct horse battery"

// toVault moves the harness's credentials into a new encrypted vault.
func (h *harness) toVault() {
	h.t.Helper()
	if err := h.a.svc.SetSecretBackend(context.Background(), core.SecretBackendVault, vaultPW); err != nil {
		h.t.Fatal(err)
	}
	h.a.refreshVault()
	h.tt.Frame()
}

// lockNow locks the vault the way the Service does after an idle time.
func (h *harness) lockNow() {
	h.t.Helper()
	h.a.svc.LockVault()
	h.a.refreshVault()
	h.settle()
}

func (h *harness) vaultState() core.VaultState { return h.a.svc.VaultState() }

func TestVaultSwitchDialogValidationAndSuccess(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.mustHave("SECURITY", "Credential storage", "System keychain", "Encrypted vault", "Stored in "+keychainName())
	h.mustNotHave("Lock now", "Change master password…", "Auto-lock")

	h.mustClick("Encrypted vault")
	if !h.a.vu.create.open {
		t.Fatal("choosing the vault did not open the dialog")
	}
	if h.a.svc.SecretBackend() != core.SecretBackendKeyring {
		t.Fatal("the backend changed before the password was confirmed")
	}
	h.mustHave("Create master password", "A forgotten master password cannot be recovered.", "Master password", "Confirm password", "Cancel", "Create vault")

	h.mustClick("Create vault")
	h.mustHave("Use at least 8 characters", "Enter the password again")
	h.a.vu.create.pw, h.a.vu.create.pw2 = "short", "short"
	h.tt.Frame()
	h.mustHave("Use at least 8 characters")
	h.a.vu.create.pw, h.a.vu.create.pw2 = vaultPW, "something else"
	h.tt.Frame()
	h.mustHave("The passwords do not match")
	h.mustNotHave("Use at least 8 characters")
	if h.a.svc.SecretBackend() != core.SecretBackendKeyring {
		t.Fatal("an invalid password switched the backend")
	}

	h.a.vu.create.pw2 = vaultPW
	h.mustClick("Create vault")
	h.waitFor("vault created", func() bool { return h.a.vaultMode() && !h.a.vu.create.open })
	h.waitToast("Credentials are now in the encrypted vault")
	if st := h.vaultState(); !st.Exists || !st.Unlocked || st.AutoLockMinutes != 15 {
		t.Fatalf("vault state = %+v", st)
	}
	h.mustHave("Encrypted with your master password", "(Argon2id, XChaCha20-Poly1305)", "Unlocked", "Lock now", "Change master password…", "Reset vault…", "Auto-lock", "15 minutes", "Vault unlocked")
	// The credentials moved with it: the account still connects.
	h.a.loadBuckets(false)
	h.settle()
	if h.a.bucketsErr != "" || len(h.a.buckets) != len(demoBuckets) {
		t.Fatalf("after the switch: %q, %d buckets", h.a.bucketsErr, len(h.a.buckets))
	}
}

func TestVaultCreateDialogWordingPerPlatform(t *testing.T) {
	for goosName, want := range map[string]string{"windows": "Windows Credential Manager", "darwin": "macOS Keychain", "linux": "Secret Service"} {
		was := goos
		goos = goosName
		got := keychainName()
		goos = was
		if got != want {
			t.Errorf("%s: %q, want %q", goosName, got, want)
		}
	}
}

func TestVaultCreateEscapeAndCancelLeaveTheBackend(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.mustClick("Encrypted vault")
	h.a.vu.create.pw = "typed but not saved"
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.vu.create.open || h.a.vu.create.pw != "" {
		t.Fatalf("Escape left the dialog: %+v", h.a.vu.create)
	}
	h.mustClick("Encrypted vault")
	h.mustClick("Cancel")
	if h.a.vu.create.open || h.a.svc.SecretBackend() != core.SecretBackendKeyring {
		t.Fatal("Cancel changed something")
	}
}

func TestVaultLockNowShowsTheIndicator(t *testing.T) {
	h := newHarness(t, "light")
	h.mustNotHave("Vault locked", "Vault unlocked") // no vault, no indicator
	h.toVault()
	h.openToolView("settings")
	h.mustHave("Vault unlocked", "Unlocked", "Lock now")
	if !h.a.enabled(h.a.cmd("vault.lock")) || h.a.enabled(h.a.cmd("vault.unlock")) {
		t.Error("the menu must offer Lock Vault while unlocked")
	}

	h.mustClick("Lock now")
	h.waitFor("locked", func() bool { return !h.vaultState().Unlocked && h.tt.HasText("Vault locked") })
	h.mustHave("Vault locked", "Locked", "Unlock…")
	h.mustNotHave("Lock now")
	if h.a.enabled(h.a.cmd("vault.lock")) || !h.a.enabled(h.a.cmd("vault.unlock")) {
		t.Error("the menu must offer Unlock Vault while locked")
	}

	// The status bar lock opens the unlock dialog; unlocking flips it back.
	h.mustClick("Vault locked")
	if !h.a.vu.unlock.open {
		t.Fatal("the lock indicator did not open the unlock dialog")
	}
	h.mustHave("Unlock vault", "Master password")
	h.a.vu.unlock.pw = vaultPW
	h.mustClick("Unlock")
	h.waitFor("unlocked", func() bool { return h.vaultState().Unlocked && !h.a.vu.unlock.open })
	h.waitToast("Vault unlocked")
	h.mustHave("Vault unlocked")

	// And the indicator locks it again.
	h.mustClick("Vault unlocked")
	h.waitFor("locked again", func() bool { return !h.vaultState().Unlocked })
	// The menu command does the same from a locked state through the dialog.
	h.a.run("vault.unlock", "test")
	if !h.a.vu.unlock.open {
		t.Fatal("Unlock Vault… did not open the dialog")
	}
}

func TestVaultOperationHittingLockOpensUnlockWrongPasswordThenRetries(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.lockNow()

	h.a.loadBuckets(false) // an operation that needs the secret
	h.waitFor("unlock dialog", func() bool { return h.a.vu.unlock.open })
	h.settle()
	h.mustHave("Unlock vault", "Master password", "Unlock", "Cancel", "Unlock…")
	if h.a.bucketsFix != "unlock" || !strings.Contains(h.a.bucketsErr, "locked") {
		t.Fatalf("buckets error = %q (%s)", h.a.bucketsErr, h.a.bucketsFix)
	}

	// A wrong password is explained inline; the dialog and its focus stay.
	h.a.vu.unlock.pw = "not the password"
	h.mustClick("Unlock")
	h.mustHave("Wrong master password. Try again.", "Unlock vault")
	if !h.a.vu.unlock.open || h.vaultState().Unlocked {
		t.Fatal("a wrong password unlocked or closed the dialog")
	}
	if !h.tt.Focused("Master password") {
		t.Error("the password field lost the keyboard after a wrong password")
	}
	h.snap("vault-unlock-wrong")

	// Typing the right one and pressing Enter unlocks and retries the load.
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(vaultPW)
	h.tt.Key(0, ui.KeyEnter)
	h.waitFor("unlocked", func() bool { return !h.a.vu.unlock.open && h.vaultState().Unlocked })
	h.waitToast("Vault unlocked")
	h.waitFor("buckets reloaded", func() bool { return len(h.a.buckets) == len(demoBuckets) && h.a.bucketsErr == "" })
	h.settle()
	h.mustNotHave("Unlock vault")
}

func TestVaultLockedListingRetriesAfterUnlock(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.lockNow()
	h.a.reload()
	h.waitFor("unlock dialog", func() bool { return h.a.vu.unlock.open })
	h.waitFor("list error", func() bool { return h.a.listErr != "" })
	h.a.vu.unlock.pw = vaultPW
	h.mustClick("Unlock")
	h.waitFor("listing back", func() bool { return h.a.listErr == "" && !h.a.loading && h.has("manifest.json") })
}

func TestVaultStartLockedAsksBeforeAnythingFails(t *testing.T) {
	env, err := newDemoEnv(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	if err := env.Svc.SetSecretBackend(context.Background(), core.SecretBackendVault, vaultPW); err != nil {
		t.Fatal(err)
	}
	env.Svc.LockVault() // what a restart looks like to the UI
	h := attach(t, env, "light")
	if !h.a.vu.unlock.open {
		t.Fatal("a locked vault with a static account did not ask at start")
	}
	h.mustHave("Unlock vault", "Vault locked")
	h.a.vu.unlock.pw = vaultPW
	h.mustClick("Unlock")
	h.waitFor("buckets", func() bool { return len(h.a.buckets) == len(demoBuckets) && h.a.bucketsErr == "" })
}

func TestVaultUnlockCancelDropsTheRetry(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.lockNow()
	h.a.loadBuckets(false)
	h.waitFor("unlock dialog", func() bool { return h.a.vu.unlock.open })
	h.mustClick("Cancel")
	if h.a.vu.unlock.open || len(h.a.vu.retries) != 0 {
		t.Fatalf("Cancel left %+v", h.a.vu)
	}
	h.mustHave("Unlock…") // the sidebar offers it again
	h.mustClick("Unlock…")
	if !h.a.vu.unlock.open {
		t.Fatal("the sidebar button did not reopen the dialog")
	}
}

func TestVaultChangeMasterPassword(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.openToolView("settings")
	h.mustClick("Change master password…")
	h.mustHave("Change master password", "Current password", "New password", "Confirm new password", "Change password")

	h.mustClick("Change password")
	h.mustHave("Enter the current password", "Use at least 8 characters", "Enter the password again")

	const next = "a brand new password"
	h.a.vu.change.old, h.a.vu.change.pw, h.a.vu.change.pw2 = "wrong old one", next, next
	h.mustClick("Change password")
	h.mustHave("Wrong master password. Try again.")
	if !h.a.vu.change.open {
		t.Fatal("a wrong current password closed the dialog")
	}

	h.a.vu.change.old = vaultPW
	h.a.vu.change.err = ""
	h.mustClick("Change password")
	h.waitFor("changed", func() bool { return !h.a.vu.change.open })
	h.waitToast("Master password changed")
	h.lockNow()
	if err := h.a.svc.UnlockVault(vaultPW); err == nil {
		t.Fatal("the old password still opens the vault")
	}
	if err := h.a.svc.UnlockVault(next); err != nil {
		t.Fatalf("the new password does not open the vault: %v", err)
	}
}

func TestVaultSwitchBackToKeychainLocked(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.lockNow()
	h.openToolView("settings")
	h.mustClick("System keychain")
	h.mustHave("Use the system keychain?", "Master password", "Move to keychain", keychainName())
	h.a.vu.toKeychain.pw = "nope nope nope"
	h.mustClick("Move to keychain")
	h.mustHave("Wrong master password. Try again.")
	if !h.a.vaultMode() {
		t.Fatal("a wrong password moved the secrets")
	}
	h.a.vu.toKeychain.pw = vaultPW
	h.mustClick("Move to keychain")
	h.waitFor("moved", func() bool { return !h.a.vaultMode() && !h.a.vu.toKeychain.open })
	h.waitToast("Credentials moved to " + keychainName())
	h.mustHave("Stored in " + keychainName())
	h.mustNotHave("Vault locked", "Lock now")
	if h.vaultState().Exists {
		t.Error("vault.json is still there")
	}
	h.a.loadBuckets(false)
	h.settle()
	if h.a.bucketsErr != "" || len(h.a.buckets) != len(demoBuckets) {
		t.Fatalf("after moving back: %q, %d buckets", h.a.bucketsErr, len(h.a.buckets))
	}
}

func TestVaultSwitchBackToKeychainUnlockedNeedsNoPassword(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.openToolView("settings")
	h.mustClick("System keychain")
	h.mustHave("Use the system keychain?", "Move to keychain")
	if h.a.vu.toKeychain.needPW || h.tt.Focused("Master password") {
		t.Fatal("an unlocked vault must not ask for the password")
	}
	h.mustClick("Move to keychain")
	h.waitFor("moved", func() bool { return !h.a.vaultMode() })
}

func TestVaultResetNeedsTypingResetAndMarksAccounts(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.a.svc.SetSecret("signing-key", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	h.toVault()
	h.openToolView("settings")
	h.mustClick("Reset vault…")
	h.mustHave("Reset vault?", "This deletes the vault and every secret stored in it.", demoAccountName, "signing-key", "Type RESET to confirm", "Cancel", "Reset vault")

	for _, typed := range []string{"", "reset", "RESET "} {
		h.a.vu.reset.typed = typed
		h.tt.Frame()
		h.mustClick("Reset vault") // the button, disabled
		if !h.a.vu.reset.open || !h.a.vaultMode() {
			t.Fatalf("typing %q was enough to reset", typed)
		}
	}
	h.a.vu.reset.typed = "RESET"
	h.mustClick("Reset vault")
	h.waitFor("reset", func() bool { return !h.a.vu.reset.open && !h.a.vaultMode() })
	h.waitToast("Vault reset")
	if h.vaultState().Exists {
		t.Error("vault.json survived the reset")
	}
	if !h.a.account().SecretMissing {
		t.Fatal("the account is not marked as missing its secret")
	}
	h.mustHave("Secret missing", "Secret missing for "+demoAccountName, "signing-key", "Enter secret…")
	h.snapBoth("vault-reset-done")

	// The sidebar leads to the account dialog, which asks for the secret.
	h.mustClick("Enter secret…")
	h.mustHave("Edit account", "The secret access key of this account is missing. Enter it again to reconnect.")
	h.a.saveAccount() // the dialog's Save (the Settings page behind has its own)
	h.settle()
	h.mustHave("Enter the secret access key")
	h.a.af.secret = "demo-secret"
	h.tt.Frame()
	h.a.saveAccount()
	h.waitFor("saved", func() bool { return !h.a.af.open && !h.a.account().SecretMissing })
	h.waitFor("buckets", func() bool { return len(h.a.buckets) == len(demoBuckets) && h.a.bucketsErr == "" })
	h.mustNotHave("Secret missing")
}

func TestVaultAutoLockSelectPersists(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	h.openToolView("settings")
	h.mustClick("15 minutes")
	h.mustHave("Never", "5 minutes", "30 minutes", "60 minutes")
	h.mustClick("30 minutes")
	h.waitFor("saved 30", func() bool { return h.a.svc.Settings().VaultAutoLockMinutes == 30 })
	h.settle()
	h.mustHave("30 minutes")
	h.mustClick("30 minutes")
	h.mustClick("Never")
	h.waitFor("saved never", func() bool { return h.a.svc.Settings().VaultAutoLockMinutes == 0 })
	if st := h.vaultState(); st.AutoLockMinutes != 0 {
		t.Fatalf("vault state = %+v", st)
	}
	// Changing another setting keeps it: each change edits a fresh copy.
	h.a.sf.upload = "9"
	h.a.commitNumber(numSettings[0])
	h.settle()
	if st := h.a.svc.Settings(); st.Limits.Upload != 9 || st.VaultAutoLockMinutes != 0 {
		t.Fatalf("another setting reverted the auto-lock time: %+v", st)
	}
}

func TestKeyringUnavailableOffersTheVault(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.mustNotHave("No system keychain is available")
	h.a.noteErr(fmt.Errorf("save secret: %w", core.ErrKeyringUnavailable), "", nil)
	h.tt.Frame()
	h.mustHave("No system keychain is available on this computer. Use the encrypted vault to store your credentials.")
	// The account dialog offers the way out when saving hits it.
	h.a.openAccountForm(false)
	h.a.af.saveErr, h.a.af.noKeychain = "No system keychain available", true
	h.settle()
	h.mustClick("Set up encrypted vault…")
	if h.a.af.open || !h.a.vu.create.open || h.a.tool != "settings" {
		t.Fatalf("the link did not lead to the vault dialog: af=%v create=%v tool=%q", h.a.af.open, h.a.vu.create.open, h.a.tool)
	}
}

func TestVaultMenuIsInTheFileMenu(t *testing.T) {
	h := newHarness(t, "light")
	for _, id := range []string{"vault.lock", "vault.unlock"} {
		if h.a.cmd(id) == nil {
			t.Fatalf("command %s is missing", id)
		}
	}
	h.toVault()
	h.tt.Frame()
	h.a.run("vault.lock", "test")
	h.waitFor("locked by the menu", func() bool { return !h.vaultState().Unlocked })
}

// ---- audit cases ----------------------------------------------------------------------------------

// vaultCases are the vault screens of the UX audit and of the minimum-size
// reachability test.
func vaultCases() []auditCase {
	settings := func(h *harness, vault bool) {
		if vault {
			h.toVault()
		}
		h.openToolView("settings")
		h.settle()
		// Bring the Security section to the middle of the page.
		h.tt.Scroll(700, 300, 0, 520)
		h.tt.Frame()
	}
	return []auditCase{
		{name: "vault-settings-keychain", setup: func(h *harness) { settings(h, false) }},
		{name: "vault-settings-vault-unlocked", setup: func(h *harness) { settings(h, true) }},
		{name: "vault-settings-vault-locked", setup: func(h *harness) {
			h.toVault()
			h.lockNow()
			settings(h, false)
		}},
		{name: "vault-settings-keyring-missing", setup: func(h *harness) {
			h.a.noteKeyring(core.ErrKeyringUnavailable)
			settings(h, false)
		}},
		{name: "vault-create", footer: []string{"Cancel", "Create vault"}, setup: func(h *harness) {
			h.a.askBackend(1)
			h.settle()
		}},
		{name: "vault-create-error", footer: []string{"Cancel", "Create vault"}, setup: func(h *harness) {
			h.a.askBackend(1)
			h.a.vu.create.pw, h.a.vu.create.pw2 = "short", "shorter"
			h.settle()
			h.mustClick("Create vault")
		}},
		{name: "vault-unlock", footer: []string{"Cancel", "Unlock"}, setup: func(h *harness) {
			h.toVault()
			h.lockNow()
			h.a.askUnlock("test", nil)
			h.settle()
		}},
		{name: "vault-unlock-wrong", footer: []string{"Cancel", "Unlock"}, setup: func(h *harness) {
			h.toVault()
			h.lockNow()
			h.a.askUnlock("test", nil)
			h.a.vu.unlock.pw = "wrong guess"
			h.settle()
			h.mustClick("Unlock")
		}},
		{name: "vault-change", footer: []string{"Cancel", "Change password"}, setup: func(h *harness) {
			h.toVault()
			h.a.vu.change = pwDialog{open: true}
			h.settle()
		}},
		{name: "vault-to-keychain", footer: []string{"Cancel", "Move to keychain"}, setup: func(h *harness) {
			h.toVault()
			h.lockNow()
			h.a.askBackend(0)
			h.settle()
		}},
		{name: "vault-reset", footer: []string{"Cancel", "Reset vault"}, setup: func(h *harness) {
			if err := h.a.svc.SetSecret("signing-key", "x"); err != nil {
				h.t.Fatal(err)
			}
			if err := h.a.svc.SetSecret("cdn-token", "x"); err != nil {
				h.t.Fatal(err)
			}
			h.toVault()
			h.a.openReset()
			h.a.vu.reset.typed = "RESE"
			h.settle()
		}},
		{name: "vault-account-secret-missing", footer: []string{"Cancel", "Save"}, setup: func(h *harness) {
			h.toVault()
			if _, err := h.a.svc.ResetVault(); err != nil {
				h.t.Fatal(err)
			}
			h.a.refreshVault()
			h.a.accounts = h.a.svc.Accounts()
			h.a.loadBuckets(true)
			h.settle()
			h.a.openAccountForm(true)
			h.settle()
		}},
		{name: "vault-sidebar-secret-missing", setup: func(h *harness) {
			h.toVault()
			if _, err := h.a.svc.ResetVault(); err != nil {
				h.t.Fatal(err)
			}
			h.a.refreshVault()
			h.a.accounts = h.a.svc.Accounts()
			h.a.loadBuckets(true)
			h.settle()
		}},
		{name: "vault-sidebar-locked", setup: func(h *harness) {
			h.toVault()
			h.lockNow()
			h.a.loadBuckets(false)
			h.settle()
			h.a.closeUnlock()
			h.settle()
		}},
	}
}

// TestVaultDialogsReachableAndSnapshots renders the vault screens at both
// sizes; the footers must be on screen at 960x600. PNGs go to
// PAIL_UI_SNAPSHOT_DIR when it is set.
func TestVaultScreensAtBothSizes(t *testing.T) {
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("set PAIL_UI_SNAPSHOT_DIR to write the vault PNGs (the footers are checked by TestDialogFootersReachableAtMinSize)")
	}
	for _, sz := range auditSizes {
		for _, c := range vaultCases() {
			t.Run(sz.name+"/"+c.name, func(t *testing.T) {
				h := newHarness(t, "light")
				h.resize(sz.w, sz.h)
				c.setup(h)
				h.tt.Frame()
				h.expectOnScreen(sz, c.footer...)
				h.snapAudit(sz, c.name)
			})
		}
	}
}
