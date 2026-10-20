package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// closedPortURL is an endpoint nothing listens on.
func closedPortURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}

func (h *harness) fillAccount(name, endpoint, access, secret string) {
	f := &h.a.af
	f.provider, f.name, f.endpoint, f.access, f.secret = 1, name, endpoint, access, secret
	h.settle()
}

func TestWelcomeShownWithoutAccounts(t *testing.T) {
	env, err := newDemoEnv(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	if err := env.Svc.DeleteAccount(env.Account.ID); err != nil {
		t.Fatal(err)
	}
	h := attach(t, env, "light")
	h.mustHave("Connect an S3 account", "Add account", "No account")
	h.mustNotHave("BUCKETS", "Name", "Transfers")
	h.snap("welcome-light")
	h.mustClick("Add account")
	if !h.a.af.open {
		t.Fatal("the welcome button did not open the account dialog")
	}
	h.mustHave("Add account", "Provider", "Test connection", "Save")
}

func TestAccountDialogValidation(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(false)
	h.settle()
	h.mustHave("Add account", "Provider", "Name", "Region", "Credentials", "Access key ID", "Secret access key", "Session token (optional)", "Advanced", "Test connection", "Save")
	h.mustNotHave("Endpoint", "Path-style addressing") // AWS has a fixed endpoint
	h.mustClick("Save")
	h.mustHave("Enter a name", "Enter the access key ID", "Enter the secret access key")
	if len(h.env.Svc.Accounts()) != 1 {
		t.Fatal("an invalid form was saved")
	}
	// S3-compatible shows the endpoint and path-style, and checks the URL.
	h.a.af.provider, h.a.af.endpoint = 1, "ftp://nope"
	h.settle()
	h.mustHave("Endpoint", "Path-style addressing", "Use an http:// or https:// URL")
	// Credentials follow the select.
	h.a.af.creds = core.CredProfile
	h.settle()
	h.mustHave("Profile", "Enter a profile name")
	h.mustNotHave("Secret access key")
	h.a.af.creds = core.CredAnonymous
	h.settle()
	h.mustNotHave("Profile", "Access key ID")
	// Advanced opens on click.
	h.mustClick("Advanced")
	h.mustHave("Role ARN", "External ID", "Session name", "Checksum mode", "Skip TLS verification")
	h.mustClick("Cancel")
	if h.a.af.open {
		t.Fatal("Cancel left the dialog open")
	}
}

func TestAccountTestConnectionSuccessAndFailure(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(false)
	h.fillAccount("Staging", h.env.Srv.URL, "demo", "demo-secret")
	h.mustClick("Test connection")
	h.waitFor("test result", func() bool { return h.a.af.test == testOK || h.a.af.test == testFailed })
	if h.a.af.test != testOK {
		t.Fatalf("test against s3test failed: %s", h.a.af.testMsg)
	}
	h.mustHave("Connected")
	h.snap("account-dialog-light")
	if len(h.env.Svc.Accounts()) != 1 {
		t.Fatal("testing must not save the account")
	}

	h.a.af.endpoint = closedPortURL(t)
	h.mustClick("Test connection")
	h.waitFor("failed test", func() bool { return h.a.af.test == testFailed })
	if !strings.Contains(h.a.af.testMsg, "Can't reach the server") {
		t.Errorf("failure text = %q", h.a.af.testMsg)
	}
	h.mustHave("Can't reach the server")
	if len(h.env.Svc.Accounts()) != 1 {
		t.Fatal("a failed test saved the account")
	}
}

func TestAccountSaveAddsAndSwitches(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(false)
	h.fillAccount("Staging", h.env.Srv.URL, "demo", "demo-secret")
	h.a.af.path = true
	h.mustClick("Save")
	h.waitFor("account saved", func() bool { return len(h.env.Svc.Accounts()) == 2 && !h.a.af.open })
	accts := h.env.Svc.Accounts()
	got := accts[1]
	if got.Name != "Staging" || got.Provider != core.ProviderS3 || got.Endpoint != h.env.Srv.URL || !got.PathStyle || got.AccessKeyID != "demo" {
		t.Fatalf("saved account = %+v", got)
	}
	if h.a.accountID != got.ID {
		t.Fatalf("the new account is not selected: %q", h.a.accountID)
	}
	h.waitFor("buckets of the new account", func() bool { return len(h.a.buckets) == len(demoBuckets) })
	h.mustHave("Staging")
	h.snap("accounts-switched-light")
}

func TestAccountSwitcherMenuListsAccounts(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick(demoAccountName)
	menu := h.menu()
	for _, want := range []string{demoAccountName, "Add account…", "Edit account…", "Remove account…"} {
		if !contains(menu, want) {
			t.Errorf("account menu %q missing %q", menu, want)
		}
	}
	if err := h.chooseMenu("Add account…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if !h.a.af.open || h.a.af.editing != "" {
		t.Fatalf("Add account… did not open an empty form: %+v", h.a.af)
	}
}

func TestEditAccountKeepsSecretWhenLeftEmpty(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openAccountForm(true)
	h.settle()
	if h.a.af.name != demoAccountName || h.a.af.secret != "" || !h.a.af.hasSec {
		t.Fatalf("form not filled from the account: %+v", h.a.af)
	}
	h.mustHave("Edit account")
	h.a.af.name = "Demo renamed"
	h.mustClick("Save")
	h.waitFor("renamed", func() bool { return h.env.Svc.Accounts()[0].Name == "Demo renamed" && !h.a.af.open })
	// The old secret still works: the buckets load again.
	h.waitFor("buckets reloaded", func() bool { return len(h.a.buckets) == len(demoBuckets) && h.a.bucketsErr == "" })
	h.mustHave("Demo renamed")
}

func TestRemoveAccountConfirmsAndExplains(t *testing.T) {
	h := newHarness(t, "light")
	second, err := h.env.Svc.SaveAccount(core.Account{Name: "Second", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL, PathStyle: true,
		Credentials: core.CredStatic, AccessKeyID: "x"}, &core.Secrets{SecretAccessKey: "y"})
	if err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.a.selectAccount(second.ID)
	h.settle()
	h.a.askRemoveAccount()
	h.settle()
	h.mustHave("Remove account?", "Second", "not affected", "Cancel", "Remove")
	h.snap("remove-account-light")
	h.mustClick("Cancel")
	if len(h.env.Svc.Accounts()) != 2 {
		t.Fatal("cancel removed the account")
	}
	h.a.askRemoveAccount()
	h.settle()
	h.mustClick("Remove")
	h.waitFor("account removed", func() bool { return len(h.env.Svc.Accounts()) == 1 })
	h.waitFor("fell back to the other account", func() bool { return h.a.accountID == h.env.Account.ID })
	// Buckets of the cloud are untouched.
	if got := h.env.Srv.Buckets(); len(got) != len(demoBuckets) {
		t.Errorf("server buckets changed: %v", got)
	}
}

// ---- settings --------------------------------------------------------------------------------------------

// settingField clicks into the text field of a Settings row: the label is a
// fixed 200px column and the control starts 16px after it.
func (h *harness) settingField(label string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(label)
	if !ok {
		h.t.Fatalf("no settings row %q; have %q", label, h.tt.Texts())
	}
	h.tt.ClickAt(r.X+200+16+24, r.Y+r.H/2)
	h.settle()
}

// editSetting focuses a field, replaces its text and presses Enter.
func (h *harness) editSetting(label, text string) {
	h.t.Helper()
	h.settingField(label)
	h.replaceAndEnter(text)
}

func TestSettingsApplyAsTheyChange(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("Settings")
	h.mustHave("APPEARANCE", "TRANSFERS", "DOWNLOADS", "Theme", "System", "Light", "Dark", "Concurrent uploads", "Concurrent downloads", "Bulk workers per task",
		"Part size (MiB)", "Default download folder", "Choose…")
	h.mustNotHave("Save", "Saved") // nothing to press, nothing saved yet
	h.snap("settings-light")

	// A segmented control applies on the click.
	h.mustClick("Dark")
	if h.a.opts.theme != "dark" {
		t.Fatalf("theme = %q, the switch must apply at once", h.a.opts.theme)
	}
	h.waitFor("theme saved", func() bool { return h.env.Svc.Settings().Theme == "dark" })
	h.mustHave("Saved")
	h.snap("settings-dark")

	// A number is checked when Enter is pressed: an invalid one is explained,
	// the field goes back to the saved value and nothing is written.
	h.editSetting("Part size (MiB)", "1")
	h.mustHave("Enter a whole number from 5 to 5120 MiB")
	if h.a.sf.part != "16" || h.env.Svc.Settings().PartSizeMiB != 16 {
		t.Fatalf("an invalid part size stuck: field %q, saved %d", h.a.sf.part, h.env.Svc.Settings().PartSizeMiB)
	}
	h.editSetting("Concurrent uploads", "abc")
	h.mustHave("Enter a whole number from 1 to 64")
	if h.a.sf.upload != "8" || h.env.Svc.Settings().Limits.Upload != 8 {
		t.Fatalf("an invalid upload limit stuck: field %q", h.a.sf.upload)
	}

	// A valid one saves on Enter...
	h.editSetting("Part size (MiB)", "32")
	h.waitFor("part size saved", func() bool { return h.env.Svc.Settings().PartSizeMiB == 32 })
	h.mustNotHave("Enter a whole number from 5 to 5120 MiB")
	// ...and on losing the focus: click the next field instead of pressing Enter.
	h.settingField("Concurrent uploads")
	h.replaceAndEnter("12") // Enter commits
	h.settingField("Concurrent downloads")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("6")
	h.settingField("Concurrent other tasks") // blur commits the downloads field
	h.waitFor("blur saved", func() bool { return h.env.Svc.Settings().Limits.Download == 6 })

	// A drop-down and the download folder apply too.
	h.mustClick("Keep both (rename)")
	h.mustClick("Overwrite")
	h.waitFor("policy saved", func() bool { return h.env.Svc.Settings().Conflict == core.ConflictOverwrite })

	picked := t.TempDir()
	h.a.pick = func(bool) []string { return []string{picked} }
	h.mustClick("Choose…")
	h.waitFor("folder saved", func() bool { return h.env.Svc.Settings().DefaultDownloadDir == picked })
	h.editSetting("Default download folder", "relative/path")
	h.mustHave("Use a full folder path")
	if h.a.sf.dir != picked || h.env.Svc.Settings().DefaultDownloadDir != picked {
		t.Fatalf("a relative folder stuck: %q", h.a.sf.dir)
	}
	h.mustClick("Clear download folder")
	h.waitFor("folder cleared", func() bool { return h.env.Svc.Settings().DefaultDownloadDir == "" })

	st := h.env.Svc.Settings()
	if st.PartSizeMiB != 32 || st.Limits.Upload != 12 || st.Limits.Download != 6 || st.Limits.Other != 8 || st.Theme != "dark" || st.Conflict != core.ConflictOverwrite {
		t.Fatalf("saved settings = %+v", st)
	}
	h.mustClick("Back")
	h.mustHave("Name", "Modified")
}

// A failing save is explained under its control and the control goes back.
func TestSettingsSaveFailureIsShownInline(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("Settings")
	cfg := filepath.Join(h.env.Dir, "settings.json")
	if err := os.Remove(cfg); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil { // a folder where the file goes: the write fails
		t.Fatal(err)
	}
	h.editSetting("Part size (MiB)", "64")
	h.waitFor("failure", func() bool { return strings.Contains(strings.Join(h.tt.Texts(), "\n"), "Could not save") })
	if h.a.sf.part != "16" {
		t.Errorf("the field kept %q after the save failed", h.a.sf.part)
	}
}
