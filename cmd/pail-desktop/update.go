package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/selfupdate"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// Updates (internal/selfupdate): the Updates section of Settings checks for a
// newer release, downloads and installs it, and restarts Pail into it. The
// update replaces the command line `pail` too. With a policy other than
// Manually, Pail checks once when it starts on a new day or week and only
// says what it found; downloading and restarting wait for a click.

// updPhase is where an update stands.
type updPhase int

const (
	updIdle        updPhase = iota
	updChecking             // a check runs
	updLatest               // the last check found nothing newer
	updFound                // up is newer than the running version
	updDownloading          // up is being downloaded and installed
	updReady                // up is installed; a restart runs it
	updFailed               // the last check or install failed: err
)

// updateUI is the state of the Updates section.
type updateUI struct {
	phase       updPhase
	up          *selfupdate.Update
	err         string
	done, total int64              // bytes downloaded of the update
	cancel      context.CancelFunc // stops the download
	// unsupported is why this installation cannot update itself, or nil.
	unsupported error

	// Hooks: the real ones are set by newApp; tests replace them.
	check    func(ctx context.Context) (*selfupdate.Update, error)
	install  func(ctx context.Context, up *selfupdate.Update, progress func(done, total int64)) error
	relaunch func()
}

// updater is the selfupdate.Updater for the saved source and proxy.
func (a *app) updater(ctx context.Context) (*selfupdate.Updater, error) {
	proxy, err := a.svc.ProxyURL(ctx)
	if err != nil {
		return nil, err
	}
	return &selfupdate.Updater{Current: version, Source: a.svc.Settings().Updates.Source, Proxy: proxy}, nil
}

// initUpdates sets the real hooks; the relaunch is set by main.
func (a *app) initUpdates() {
	u := &a.upd
	u.unsupported = selfupdate.Supported(version)
	u.check = func(ctx context.Context) (*selfupdate.Update, error) {
		up, err := a.updater(ctx)
		if err != nil {
			return nil, err
		}
		return up.Check(ctx)
	}
	u.install = func(ctx context.Context, up *selfupdate.Update, progress func(done, total int64)) error {
		return up.Install(ctx, progress)
	}
	u.relaunch = func() {}
}

// checkUpdatesAtStart checks once when the policy says this start is the
// first of its day or week. It says what it found in a toast, and nothing when
// the check fails: the next start tries again.
func (a *app) checkUpdatesAtStart() {
	st := a.svc.Settings().Updates
	if a.upd.unsupported != nil || !selfupdate.Due(st.Policy, st.LastCheck, time.Now()) {
		return
	}
	a.checkUpdates(true)
}

// checkUpdates looks for a newer release; quiet is the check at start, which
// reports a failure in the log only and an update in a toast.
func (a *app) checkUpdates(quiet bool) {
	u := &a.upd
	if u.phase == updChecking || u.phase == updDownloading || u.phase == updReady {
		return
	}
	u.phase, u.err = updChecking, ""
	a.busyOps.Add(1)
	go func() {
		up, err := u.check(a.ctx)
		if err == nil {
			if err := a.svc.SetUpdateCheck(time.Now()); err != nil {
				log.Printf("updates: record the check: %v", err)
			}
		}
		a.post(func() {
			a.busyOps.Add(-1)
			if u.phase != updChecking {
				return
			}
			switch {
			case err != nil:
				u.phase, u.err = updFailed, locales.UpdatesCheckFailed(updateError(err))
				if quiet {
					log.Printf("updates: %v", err)
				}
			case up == nil:
				u.phase = updLatest
			default:
				u.phase, u.up = updFound, up
				if quiet || a.tool != "settings" {
					a.toastAction(locales.UpdatesAvailableToast(up.Version), locales.UpdatesView(), a.openUpdates)
				}
			}
		})
	}()
}

// installUpdate downloads and installs the update found; a download can be
// cancelled and started again.
func (a *app) installUpdate() {
	u := &a.upd
	if u.phase != updFound && u.phase != updFailed || u.up == nil {
		return
	}
	up := u.up
	ctx, cancel := context.WithCancel(a.ctx)
	u.phase, u.err, u.cancel = updDownloading, "", cancel
	u.done, u.total = 0, up.Size
	a.busyOps.Add(1)
	go func() {
		var last time.Time
		err := u.install(ctx, up, func(done, total int64) {
			// About ten times a second, like the progress of a transfer.
			if now := time.Now(); now.Sub(last) >= 100*time.Millisecond || done == total {
				last = now
				a.post(func() {
					if u.phase == updDownloading {
						u.done, u.total = done, total
					}
				})
			}
		})
		cancelled := ctx.Err() != nil
		cancel()
		a.post(func() {
			a.busyOps.Add(-1)
			u.cancel = nil
			switch {
			case err != nil && cancelled:
				u.phase = updFound
			case err != nil:
				u.phase, u.err = updFailed, locales.UpdatesInstallFailed(updateError(err))
				if a.tool != "settings" {
					a.toast(u.err)
				}
			default:
				u.phase = updReady
				a.toastAction(locales.UpdatesReadyToast(up.Version), locales.UpdatesRestart(), a.restartForUpdate)
			}
		})
	}()
}

// cancelUpdate stops the download.
func (a *app) cancelUpdate() {
	if a.upd.cancel != nil {
		a.upd.cancel()
	}
}

// restartForUpdate starts the installed update; while transfers run it says
// so and waits for another click.
func (a *app) restartForUpdate() {
	if n := countTasks(a.tasks)[0]; n > 0 {
		a.toast(locales.UpdatesRestartBusy(n))
		return
	}
	a.upd.relaunch()
}

// openUpdates shows the Updates section of Settings.
func (a *app) openUpdates() {
	if a.tool != "settings" {
		a.openTool("settings")
	}
	a.srail.want = "updates"
}

// updateError words why a check or an install failed.
func updateError(err error) string {
	switch {
	case errors.Is(err, selfupdate.ErrDevBuild):
		return locales.UpdatesDevBuild()
	case errors.Is(err, selfupdate.ErrNotBundle):
		return locales.UpdatesNotBundle()
	case errors.Is(err, selfupdate.ErrAppImage):
		return locales.UpdatesAppImage()
	case errors.Is(err, selfupdate.ErrNotWritable):
		return locales.UpdatesNotWritable()
	}
	return humanError(err)
}

// changelogURL is the list of changes on the website, in the language shown.
func changelogURL() string {
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return "https://pail.download/zh/changelog/"
	}
	return "https://pail.download/changelog/"
}

// updatePolicies and updateSources are the choices of the Updates section.
func updatePolicies() []choice {
	return []choice{
		{string(selfupdate.PolicyManual), locales.UpdatesPolicyManual()},
		{string(selfupdate.PolicyDaily), locales.UpdatesPolicyDaily()},
		{string(selfupdate.PolicyWeekly), locales.UpdatesPolicyWeekly()},
	}
}

func updateSources() []choice {
	return []choice{
		{string(selfupdate.SourceGitHub), locales.UpdatesSourceGithub()},
		{string(selfupdate.SourceMirror), locales.UpdatesSourceMirror()},
	}
}

// setUpdatePolicy and setUpdateSource save a choice of the Updates section.
func (a *app) setUpdatePolicy(v string) {
	a.applySetting("updPolicy", func(st *core.Settings) { st.Updates.Policy = selfupdate.Policy(v) }, nil)
}

func (a *app) setUpdateSource(v string) {
	a.applySetting("updSource", func(st *core.Settings) { st.Updates.Source = selfupdate.Source(v) }, nil)
}

// updatesSection is the Updates group of Settings: when Pail checks, where
// from, and the state of the update with what can be done next.
func (a *app) updatesSection(c *ui.Context) {
	f := &a.sf
	u := &a.upd
	st := a.svc.Settings().Updates
	a.settingsGroup(c, "updates", func() {
		a.settingsCard(c, func() {
			a.settingsRowHelp(c, locales.UpdatesPolicy(), locales.UpdatesPolicyHelp(), f.errs["updPolicy"], func() {
				a.savedMark(c, "updPolicy")
				v := string(st.Policy)
				if v == "" {
					v = string(selfupdate.PolicyManual)
				}
				if selectBox(c, locales.UpdatesPolicy(), &v, updatePolicies(), settingsSelW) {
					a.setUpdatePolicy(v)
				}
			})
			a.settingsRowHelp(c, locales.UpdatesSource(), locales.UpdatesSourceHelp(), f.errs["updSource"], func() {
				a.savedMark(c, "updSource")
				v := string(st.Source)
				if v == "" {
					v = string(selfupdate.SourceGitHub)
				}
				if selectBox(c, locales.UpdatesSource(), &v, updateSources(), settingsSelW) {
					a.setUpdateSource(v)
				}
			})
			a.updateStatusRow(c, st.LastCheck)
			a.settingsRow(c, locales.UpdatesChanges(), "", func() {
				a.link(c, "file-text", locales.UpdatesChangelog(), locales.UpdatesChangelogTip(), changelogURL()).Margin(0, -8, 0, 0)
			})
		})
		if u.unsupported != nil {
			ui.Text(c, updateError(u.unsupported)).FontSize(sizeSecondary).TextColor(tk(c).TextMuted).MarginStart(2)
		}
	})
}

// updateStatusRow says where the update stands and offers the next step.
func (a *app) updateStatusRow(c *ui.Context, last time.Time) {
	u := &a.upd
	k := tk(c)
	label := locales.UpdatesCurrent(version)
	var notes []string
	checked := ""
	if !last.IsZero() {
		checked = locales.UpdatesLastChecked(formatTime(last.Local()))
	}
	switch u.phase {
	case updIdle, updChecking:
		if checked != "" {
			notes = append(notes, checked)
		}
	case updLatest:
		notes = append(notes, locales.UpdatesUpToDate())
		if checked != "" {
			notes = append(notes, checked)
		}
	case updFound, updDownloading:
		label = locales.UpdatesAvailable(u.up.Version)
		size := locales.UpdatesSize(humanBytes(u.up.Size))
		if u.up.Delta {
			size = locales.UpdatesSizeDelta(humanBytes(u.up.Size))
		}
		notes = append(notes, size)
	case updReady:
		label = locales.UpdatesInstalled(u.up.Version)
		notes = append(notes, locales.UpdatesRestartNote())
	case updFailed:
		if u.up != nil {
			label = locales.UpdatesAvailable(u.up.Version)
		}
	}
	err := ""
	if u.phase == updFailed {
		err = u.err
	}
	a.settingsRowHelp(c, label, locales.UpdatesStatusHelp(), err, func() {
		if u.unsupported != nil {
			return
		}
		switch u.phase {
		case updChecking:
			secondaryButton(c, locales.UpdatesChecking(), true)
		case updFound:
			if primaryButton(c, locales.UpdatesInstall(), false).Clicked() {
				a.installUpdate()
			}
		case updDownloading:
			frac := 0.0
			if u.total > 0 {
				frac = float64(u.done) / float64(u.total)
			}
			ui.Column(c).Gap(4).AlignItems(ui.End).Shrink(0).Children(func() {
				progressBar(c, frac, false)
				ui.Text(c, locales.TransfersBytesOf(humanBytes(u.done), humanBytes(u.total))).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
			})
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				a.cancelUpdate()
			}
		case updReady:
			if primaryButton(c, locales.UpdatesRestart(), false).Clicked() {
				a.restartForUpdate()
			}
		case updFailed:
			if u.up != nil {
				if primaryButton(c, locales.UpdatesInstall(), false).Clicked() {
					a.installUpdate()
				}
			} else if secondaryButton(c, locales.UpdatesCheck(), false).Clicked() {
				a.checkUpdates(false)
			}
		default:
			if secondaryButton(c, locales.UpdatesCheck(), false).Clicked() {
				a.checkUpdates(false)
			}
		}
	}, notes...)
}
