package main

import (
	"context"
	"errors"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/selfupdate"
)

// fakeUpdates replaces the update hooks: check finds up (or fails with
// checkErr) and install reports progress and returns installErr.
type fakeUpdates struct {
	up         *selfupdate.Update
	checkErr   error
	installErr error
	checks     int
	relaunched int
}

func (f *fakeUpdates) use(a *app) {
	a.upd.unsupported = nil
	a.upd.check = func(context.Context) (*selfupdate.Update, error) {
		f.checks++
		return f.up, f.checkErr
	}
	a.upd.install = func(_ context.Context, up *selfupdate.Update, progress func(done, total int64)) error {
		progress(up.Size/2, up.Size)
		progress(up.Size, up.Size)
		return f.installErr
	}
	a.upd.relaunch = func() { f.relaunched++ }
}

func TestUpdatesSectionUnsupported(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openTool("settings")
	h.settle()
	h.showSetting("Current version dev")
	h.mustHave("Updates", "Check for updates", "Manually", "Download from", "GitHub", "What's new", "Changelog",
		"This build is not a release, so it does not update itself.")
}

func TestUpdatesCheckInstallRestart(t *testing.T) {
	h := newRawHarness(t, "light", true)
	f := &fakeUpdates{up: &selfupdate.Update{Version: "0.7.1", Size: 3 << 20, Delta: true}}
	f.use(h.a)
	h.a.openTool("settings")
	h.settle()
	h.showSetting("Current version dev")
	h.mustClick("Check now")
	h.showSetting("Version 0.7.1 is available")
	h.mustHave("Download size: 3.0 MB (delta update)", "Download and install")
	if f.checks != 1 || h.env.Svc.Settings().Updates.LastCheck.IsZero() {
		t.Fatalf("checks %d, last check %v", f.checks, h.env.Svc.Settings().Updates.LastCheck)
	}
	h.snapBoth("settings-update-available")

	h.mustClick("Download and install")
	h.waitFor("installed", func() bool { return h.a.upd.phase == updReady })
	h.settle()
	h.showSetting("Version 0.7.1 is installed")
	h.mustHave("Restart Pail to use it.", "Restart")

	// A running transfer holds the restart back.
	h.a.tasks = []Task{{Status: core.StatusRunning}}
	h.a.restartForUpdate()
	h.tt.Frame()
	h.mustHave("A transfer is running. Restart when it has finished.")
	if f.relaunched != 0 {
		t.Fatalf("restarted during a transfer")
	}
	h.a.tasks = nil
	h.a.restartForUpdate()
	if f.relaunched != 1 {
		t.Fatalf("relaunched %d times", f.relaunched)
	}
}

func TestUpdatesFailures(t *testing.T) {
	h := newRawHarness(t, "light", true)
	f := &fakeUpdates{checkErr: errors.New("no route to host")}
	f.use(h.a)
	h.a.openTool("settings")
	h.settle()
	h.showSetting("Current version dev")
	h.mustClick("Check now")
	h.mustHave("Could not check for updates: no route to host")
	if !h.env.Svc.Settings().Updates.LastCheck.IsZero() {
		t.Error("a failed check was recorded")
	}

	f.checkErr, f.up = nil, nil
	h.mustClick("Check now")
	h.mustHave("Pail is up to date.")

	f.up, f.installErr = &selfupdate.Update{Version: "0.7.1", Size: 1 << 20}, errors.New("signature does not match")
	h.mustClick("Check now")
	h.showSetting("Version 0.7.1 is available")
	h.mustHave("Download size: 1.0 MB")
	h.mustClick("Download and install")
	h.mustHave("Could not install the update: signature does not match", "Download and install")
}

func TestUpdatesAtStart(t *testing.T) {
	h := newRawHarness(t, "light", true)
	f := &fakeUpdates{up: &selfupdate.Update{Version: "0.7.1", Size: 1 << 20}}
	f.use(h.a)

	// Manually (the default) never checks by itself.
	h.a.checkUpdatesAtStart()
	h.settle()
	if f.checks != 0 {
		t.Fatal("checked under the manual policy")
	}

	st := h.env.Svc.Settings()
	st.Updates.Policy = selfupdate.PolicyDaily
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	h.a.checkUpdatesAtStart()
	h.settle()
	if f.checks != 1 {
		t.Fatalf("checks %d", f.checks)
	}
	h.mustHave("Pail 0.7.1 is available")
	// The toast's View opens the Updates section.
	h.mustClick("View")
	h.waitFor("the updates section", func() bool { return h.a.tool == "settings" && h.a.srail.want == "" })
	h.settle()
	if cur := h.a.srail.current(); cur != "updates" {
		t.Errorf("the rail marks %q", cur)
	}
	h.mustHave("Version 0.7.1 is available")

	// The same day does not check again.
	h.a.upd.phase = updIdle
	h.a.checkUpdatesAtStart()
	h.settle()
	if f.checks != 1 {
		t.Errorf("checked twice on one day")
	}
}

func TestUpdateSettingsSave(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openTool("settings")
	h.settle()
	h.a.setUpdatePolicy("weekly")
	h.a.setUpdateSource("mirror")
	h.waitFor("saved", func() bool {
		u := h.env.Svc.Settings().Updates
		return u.Policy == selfupdate.PolicyWeekly && u.Source == selfupdate.SourceMirror
	})
	h.settle()
	h.showSetting("Check for updates")
	h.mustHave("Weekly, at first start", "pail.download")
}

func TestChangelogURL(t *testing.T) {
	h := newRawHarness(t, "light", true)
	if got := changelogURL(); got != "https://pail.download/changelog/" {
		t.Errorf("en: %s", got)
	}
	h.setLocale("zh-Hans")
	if got := changelogURL(); got != "https://pail.download/zh/changelog/" {
		t.Errorf("zh: %s", got)
	}
	h.setLocale("en")
}
