package main

import (
	"strconv"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Restore from archive: objects in Glacier Flexible Retrieval or Deep
// Archive are copied back for a number of days before they can be read.

type archiveForm struct {
	open  bool
	scope rewriteScope
	est   estState
	days  string
	tier  int // index into archiveTiers
	busy  bool
	err   string
}

var archiveTiers = []core.RestoreTier{core.TierStandard, core.TierBulk, core.TierExpedited}

// archiveTierLabels are the segment labels in the order of archiveTiers.
func archiveTierLabels() []string {
	return []string{locales.ArchiveTierStandard(), locales.ArchiveTierBulk(), locales.ArchiveTierExpedited()}
}

// archiveDays is the parsed day count; ok is false while it is not valid.
func (f *archiveForm) archiveDays() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(f.days))
	return n, err == nil && n >= 1 && n <= core.MaxRestoreDays
}

func (a *app) openArchiveRestore() {
	keys := a.selectedKeys()
	if len(keys) == 0 || !a.browsing() {
		return
	}
	f := &a.objOps.ar
	f.est.stop()
	*f = archiveForm{open: true, scope: rewriteScope{loc: a.scope, keys: keys}, days: "7"}
	a.countScope(&f.est, f.scope.selection(), a.selectionEstimate(keys))
}

func (a *app) closeArchiveRestore() {
	f := &a.objOps.ar
	f.open = false
	f.est.stop()
}

func (a *app) submitArchiveRestore() {
	f := &a.objOps.ar
	days, ok := f.archiveDays()
	if f.busy || !f.open || !ok {
		return
	}
	sel, loc := f.scope.selection(), f.scope.loc
	opt := core.ArchiveRestoreOptions{Days: days, Tier: archiveTiers[f.tier]}
	f.busy, f.err = true, ""
	a.bg(func() func() {
		id, err := a.svc.RestoreArchived(sel, opt)
		return func() {
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				return
			}
			a.closeArchiveRestore()
			a.transfersOpen, a.transTab = true, 0
			a.follow(id, func(t Task) {
				sum := a.bulkSummary(t)
				switch {
				case t.Status == core.StatusCanceled:
					a.toast(locales.ArchiveCanceled())
				case t.Status == core.StatusFailed && sum.ok == 0 && sum.failed == 0:
					a.toast(locales.ArchiveFailed(t.Err))
				case sum.failed > 0 && sum.ok == 0:
					a.toast(locales.ArchiveFailed(sum.firstErr))
				case sum.failed > 0:
					a.toast(locales.ArchivePartial(sum.ok, sum.failed))
				case sum.ok == 0:
					a.toast(locales.ArchiveNothing(sum.skipped))
				default:
					a.toast(locales.ArchiveStarted(sum.ok))
				}
				a.headTouched(loc, false, sel.Keys...)
			})
		}
	})
}

// archiveDialog draws the Restore from archive dialog.
func (a *app) archiveDialog(c *ui.Context) {
	f := &a.objOps.ar
	was := f.open
	_, daysOK := f.archiveDays()
	dialogW(c, &f.open, locales.ArchiveTitle(), 460, func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), f.scope.loc.Bucket, false)
			fieldRow(c, locales.RewriteScope(), f.scope.describe(), false)
		})
		if len(f.scope.keys) > 1 {
			keyListBlock(c, f.scope.keys)
		}
		daysErr := ""
		if strings.TrimSpace(f.days) != "" && !daysOK {
			daysErr = locales.ArchiveDaysInvalid(strconv.Itoa(core.MaxRestoreDays))
		}
		formFieldHelp(c, locales.ArchiveDays(), locales.ArchiveDaysHelp(), daysErr, func() {
			in := inputBox(c, &f.days, locales.ArchiveDays(), "7", inputOpts{focus: true, invalid: daysErr != ""}).Width(120)
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				a.submitArchiveRestore()
			}
		})
		formFieldHelp(c, locales.ArchiveTier(), locales.ArchiveTierHelp(), "", func() {
			if segmented(c, &f.tier, locales.ArchiveTier(), archiveTierLabels()...) {
				f.err = ""
			}
		})
		ui.Column(c).Gap(2).Children(func() {
			countRow(c, &f.est)
			if f.est.state == delFailed {
				mutedText(c, f.est.err)
			}
			mutedText(c, locales.ArchiveNote())
		})
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			if primaryButton(c, locales.ArchiveSubmit(), f.busy || !daysOK).Clicked() {
				a.submitArchiveRestore()
			}
		})
	})
	if was && !f.open {
		a.closeArchiveRestore()
	}
}

// restoreStatusText words an x-amz-restore value for the inspector.
func restoreStatusText(raw string) string {
	st, ok := core.ParseRestore(raw)
	switch {
	case !ok:
		return ""
	case st.InProgress:
		return locales.ArchiveInProgress()
	case st.Expiry != "":
		return locales.ArchiveAvailableUntil(st.Expiry)
	}
	return raw
}
