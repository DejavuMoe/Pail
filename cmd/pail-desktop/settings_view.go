package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Settings apply as they change: a switch, a segmented control or a drop-down
// saves at once; a number or a folder path saves when the field loses the
// focus or Enter is pressed. A value that does not validate is explained under
// its field and the field goes back to the saved value. Every save shows a
// quiet "Saved" mark next to its control for a moment; a failed save shows
// the reason there instead.

// settingsForm is the Settings tool view. Fields are text so that a
// half-typed number never snaps back; each is checked when it is committed.
type settingsForm struct {
	loaded                           bool
	upload, download, other, workers string
	part                             string
	conflict                         string
	dir                              string
	logOn                            bool
	errs                             map[string]string    // by setting key: why the last edit was not saved
	saved                            map[string]time.Time // by setting key: when it was last saved
	focus                            map[string]bool      // by setting key: the field had the focus last frame
}

// themeModes are the ids of the themes; their names are themeLabels.
var themeModes = []string{"system", "light", "dark"}

// themeLabels are the names of themeModes, in the language shown now.
func themeLabels() []string {
	return []string{locales.SettingsThemeSystem(), locales.SettingsThemeLight(), locales.SettingsThemeDark()}
}

// conflictChoices are the file-exists policies, in the language shown now.
func conflictChoices() []choice {
	return []choice{
		{string(core.ConflictRename), locales.SettingsConflictRename()},
		{string(core.ConflictSkip), locales.SettingsConflictSkip()},
		{string(core.ConflictOverwrite), locales.SettingsConflictOverwrite()},
		{string(core.ConflictNewer), locales.SettingsConflictNewer()},
		{string(core.ConflictDifferentSize), locales.SettingsConflictSize()},
	}
}

// numSetting is a whole-number setting edited in a text field. The labels and
// the help are functions: they say their text in the language shown now.
type numSetting struct {
	key                       string
	rowLabel, inputLabel, tip func() string
	lo, hi                    int
	unit                      string
	text                      func(f *settingsForm) *string
	get                       func(core.Settings) int
	set                       func(*core.Settings, int)
}

var numSettings = []numSetting{
	{"upload", locales.SettingsUploads, locales.SettingsUploads, locales.SettingsUploadsHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.upload }, func(s core.Settings) int { return s.Limits.Upload }, func(s *core.Settings, n int) { s.Limits.Upload = n }},
	{"download", locales.SettingsDownloadsN, locales.SettingsDownloadsN, locales.SettingsDownloadsNHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.download }, func(s core.Settings) int { return s.Limits.Download }, func(s *core.Settings, n int) { s.Limits.Download = n }},
	{"other", locales.SettingsOther, locales.SettingsOther, locales.SettingsOtherHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.other }, func(s core.Settings) int { return s.Limits.Other }, func(s *core.Settings, n int) { s.Limits.Other = n }},
	{"workers", locales.SettingsWorkers, locales.SettingsWorkers, locales.SettingsWorkersHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.workers }, func(s core.Settings) int { return s.BulkWorkers }, func(s *core.Settings, n int) { s.BulkWorkers = n }},
	{"part", locales.SettingsPartRow, locales.SettingsPart, locales.SettingsPartHelp, 5, 5120, " MiB",
		func(f *settingsForm) *string { return &f.part }, func(s core.Settings) int { return s.PartSizeMiB }, func(s *core.Settings, n int) { s.PartSizeMiB = n }},
}

// loadSettingsForm fills the form from the saved settings.
func (a *app) loadSettingsForm() {
	st := a.svc.Settings()
	f := &a.sf
	*f = settingsForm{loaded: true, errs: map[string]string{}, saved: map[string]time.Time{}, focus: map[string]bool{}}
	f.upload, f.download, f.other = strconv.Itoa(st.Limits.Upload), strconv.Itoa(st.Limits.Download), strconv.Itoa(st.Limits.Other)
	f.workers, f.part = strconv.Itoa(st.BulkWorkers), strconv.Itoa(st.PartSizeMiB)
	f.conflict, f.dir = string(st.Conflict), st.DefaultDownloadDir
	f.logOn = st.LogToFile
}

// sfFail files why a setting was not saved, under its field.
func (a *app) sfFail(key, msg string) {
	if a.sf.errs == nil {
		a.sf.errs = map[string]string{}
	}
	a.sf.errs[key] = msg
}

// applySetting saves one change: mutate edits a fresh copy of the saved
// settings, so a setting this change does not touch is never overwritten by a
// stale form. Changes are written one at a time. revert puts the control back
// when the save fails.
func (a *app) applySetting(key string, mutate func(*core.Settings), revert func()) {
	delete(a.sf.errs, key)
	a.busyOps.Add(1)
	go func() {
		a.settingsMu.Lock()
		st := a.svc.Settings()
		mutate(&st)
		err := a.svc.SaveSettings(st)
		a.settingsMu.Unlock()
		a.post(func() {
			a.busyOps.Add(-1)
			if err != nil {
				if revert != nil {
					revert()
				}
				msg := locales.SettingsErrSave(humanError(err))
				a.sfFail(key, msg)
				if a.tool != "settings" {
					a.toast(msg) // nothing on screen shows the inline reason
				}
				return
			}
			if a.sf.saved == nil {
				a.sf.saved = map[string]time.Time{}
			}
			a.sf.saved[key] = time.Now()
		})
	}()
}

// commitNumber checks and saves one number field. An invalid number is
// explained and the field goes back to the saved value.
func (a *app) commitNumber(ns numSetting) {
	f := &a.sf
	txt := ns.text(f)
	cur := ns.get(a.svc.Settings())
	n, err := strconv.Atoi(strings.TrimSpace(*txt))
	if err != nil || n < ns.lo || n > ns.hi {
		a.sfFail(ns.key, locales.SettingsErrNumber(strconv.Itoa(ns.lo), strconv.Itoa(ns.hi), ns.unit))
		*txt = strconv.Itoa(cur)
		return
	}
	delete(f.errs, ns.key)
	*txt = strconv.Itoa(n)
	if n == cur {
		return
	}
	a.applySetting(ns.key, func(st *core.Settings) { ns.set(st, n) }, func() { *ns.text(&a.sf) = strconv.Itoa(cur) })
}

// commitDir checks and saves the default download folder ("" asks every time).
func (a *app) commitDir() {
	f := &a.sf
	cur := a.svc.Settings().DefaultDownloadDir
	d := strings.TrimSpace(f.dir)
	if d != "" && !filepath.IsAbs(d) {
		a.sfFail("dir", locales.SettingsErrDir())
		f.dir = cur
		return
	}
	delete(f.errs, "dir")
	f.dir = d
	if d == cur {
		return
	}
	a.applySetting("dir", func(st *core.Settings) { st.DefaultDownloadDir = d }, func() { a.sf.dir = cur })
}

// commitConflict saves the file-exists policy.
func (a *app) commitConflict() {
	cur := string(a.svc.Settings().Conflict)
	v := a.sf.conflict
	if v == cur {
		return
	}
	a.applySetting("conflict", func(st *core.Settings) { st.Conflict = core.ConflictPolicy(v) }, func() { a.sf.conflict = cur })
}

// setTheme applies a theme at once and saves the choice.
func (a *app) setTheme(mode string) {
	prev := a.themeMode()
	if mode == prev {
		return
	}
	a.opts.theme = mode
	a.applySetting("theme", func(st *core.Settings) { st.Theme = mode }, func() { a.opts.theme = prev })
}

// setLogToFile switches the diagnostic log at once and saves the choice.
func (a *app) setLogToFile(on bool) {
	a.applySetting("log", func(st *core.Settings) { st.LogToFile = on }, func() { a.sf.logOn = !on })
}

// openLogDir shows the folder of the diagnostic log files in the file manager.
func (a *app) openLogDir() {
	dir := a.svc.LogDir()
	if dir == "" {
		a.toast(locales.SettingsNoLogDir())
		return
	}
	go func() {
		err := os.MkdirAll(dir, 0o700) // nothing is logged yet on a fresh install
		if err == nil {
			err = a.openPath(dir)
		}
		if err != nil {
			a.post(func() { a.toast(locales.SettingsLogOpenFail(err.Error())) })
		}
	}()
}

func (a *app) chooseDownloadDir() {
	go func() {
		ds := a.pick(true)
		if len(ds) == 0 {
			return
		}
		a.post(func() {
			a.sf.dir = ds[0]
			a.commitDir()
		})
	}()
}

// ---- view -----------------------------------------------------------------------------------

// settingsRow is a 200px label column and a control, 40px apart.
func settingsRow(c *ui.Context, label, err string, control func()) {
	settingsRowHelp(c, label, "", err, control)
}

// settingsRowHelp is settingsRow with a "?" after the label when help is set.
func settingsRowHelp(c *ui.Context, label, help, err string, control func()) {
	k := tk(c)
	ui.Column(c.Key("srow:" + label)).Gap(4).Children(func() {
		ui.Row(c).MinHeight(28).Gap(16).AlignItems(ui.Center).Children(func() {
			if help == "" {
				ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).Width(200).Shrink(0)
			} else {
				ui.Row(c).Width(200).Shrink(0).Gap(2).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).SingleLine().Shrink(1).MinWidth(0).Tooltip(label)
					helpIcon(c, help)
				})
			}
			control()
		})
		if err != "" {
			ui.Text(c, err).FontSize(sizeSecondary).TextColor(k.Danger).MarginStart(216)
		}
	})
}

func settingsSection(c *ui.Context, title string) {
	ui.Column(c.Key("ssec:"+title)).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		sectionLabel(c, strings.ToUpper(title))
		hairline(c)
	})
}

// savedFor is how long the "Saved" mark stays beside its control.
const savedFor = 2500 * time.Millisecond

// savedMark is the quiet "Saved" confirmation at the end of a setting's
// control, for savedFor after the setting was saved.
func (a *app) savedMark(c *ui.Context, key string) {
	t, ok := a.sf.saved[key]
	if !ok {
		return
	}
	left := savedFor - time.Since(t)
	if left <= 0 {
		delete(a.sf.saved, key)
		return
	}
	c.After(left)
	k := tk(c)
	// It fades in and out instead of popping: in over durStandard, and once it
	// is no longer built, out over durExit (motion.go, fadeInOut).
	mark := ui.Row(c.Key("saved:" + key)).Gap(4).AlignItems(ui.Center).Shrink(0)
	mark.Transition(fadeInOut(mark))
	mark.Children(func() {
		ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(k.Success)
		ui.Text(c, locales.CommonSaved()).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
	})
}

// commitOnLeave saves a text field when Enter is pressed in it or when it
// loses the keyboard focus; typing clears the explanation of the last failure.
func (a *app) commitOnLeave(key string, in ui.Element, commit func()) {
	f := &a.sf
	if in.Changed() {
		delete(f.errs, key)
	}
	focused := in.Focused()
	was := f.focus[key]
	f.focus[key] = focused
	if in.Submitted() || was && !focused {
		commit()
	}
}

func (a *app) numberRow(c *ui.Context, ns numSetting) {
	f := &a.sf
	settingsRowHelp(c, ns.rowLabel(), ns.tip(), f.errs[ns.key], func() {
		in := inputBox(c, ns.text(f), ns.inputLabel(), "", inputOpts{width: 96, invalid: f.errs[ns.key] != ""})
		a.commitOnLeave(ns.key, in, func() { a.commitNumber(ns) })
		a.savedMark(c, ns.key)
	})
}

func (a *app) settingsView(c *ui.Context) {
	if !a.sf.loaded {
		a.loadSettingsForm()
	}
	f := &a.sf
	ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
		ui.Column(c).Width(640).MaxWidth(640).Padding(24, 24, 32, 24).Margin(0, 0).Gap(12).Children(func() {
			settingsSection(c, locales.SettingsAppearance())
			settingsRowHelp(c, locales.SettingsTheme(), locales.SettingsThemeHelp(), f.errs["theme"], func() {
				// The mode lives in the app (the ⋯ menu and the palette change it
				// too), so the control shows it each frame.
				sel := slicesIndex(themeModes, a.themeMode())
				th := themeLabels()
				if segmented(c, &sel, locales.SettingsTheme(), th...) {
					a.setTheme(themeModes[sel])
				}
				a.savedMark(c, "theme")
			})
			settingsRowHelp(c, locales.SettingsLanguage(), locales.SettingsLanguageHelp(), "", func() {
				// The choice lives in the app (a.lang); the select edits a copy so
				// that it shows the real language each frame.
				lang := a.lang
				if selectBox(c, locales.SettingsLanguage(), &lang, languageChoices(), 160) {
					a.setLanguage(lang)
				}
			})
			settingsSection(c, locales.SettingsTransfers())
			for _, ns := range numSettings[:4] {
				a.numberRow(c, ns)
			}
			a.numberRow(c, numSettings[4])
			settingsRowHelp(c, locales.SettingsConflictRow(), locales.SettingsConflictHelp(), f.errs["conflict"], func() {
				if selectBox(c, locales.SettingsConflictLabel(), &f.conflict, conflictChoices(), 240) {
					a.commitConflict()
				}
				a.savedMark(c, "conflict")
			})
			settingsSection(c, locales.SettingsDownloads())
			settingsRowHelp(c, locales.SettingsDir(), locales.SettingsDirHelp(), f.errs["dir"], func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
					ui.Box(c).Grow(1).MinWidth(0).Children(func() {
						in := inputBox(c, &f.dir, locales.SettingsDir(), locales.SettingsDirPlaceholder(), inputOpts{invalid: f.errs["dir"] != "", mono: f.dir != ""})
						a.commitOnLeave("dir", in, a.commitDir)
					})
					if secondaryButton(c, locales.CommonChoose(), false).Clicked() {
						a.chooseDownloadDir()
					}
					if f.dir != "" {
						ghostButton(c, icons.Get("x"), "", locales.SettingsClear(), false).Label(locales.SettingsClearLabel()).OnClick(func() {
							f.dir = ""
							a.commitDir()
						})
					}
					a.savedMark(c, "dir")
				})
			})
			a.securitySection(c)
			settingsSection(c, locales.SettingsDiagnostics())
			settingsRowHelp(c, locales.SettingsLog(), locales.SettingsLogHelp(), f.errs["log"], func() {
				if switchControl(c, &f.logOn, locales.SettingsLog()).Changed() {
					a.setLogToFile(f.logOn)
				}
				a.savedMark(c, "log")
			})
			settingsRow(c, locales.SettingsLogFiles(), "", func() {
				ghostButton(c, icons.Get("folder-open"), locales.SettingsOpenLogDir(), locales.SettingsOpenLogDirTip(), false).Margin(0, 0, 0, -8).OnClick(a.openLogDir)
			})
			a.aboutSection(c)
		})
	})
}

// slicesIndex is the index of s in list, 0 when absent.
func slicesIndex(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return 0
}
