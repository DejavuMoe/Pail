package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	upRate, downRate                 string // bandwidth limits, KiB/s
	conflict                         string
	dir                              string
	logOn                            bool
	recentOps                        string               // how many recent operations to keep
	keepOps                          bool                 // keep them when another account is chosen
	proxy                            proxyForm            // the Network section (proxy_view.go)
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

// numSetting is a whole-number setting edited in a text field. The label and
// the help are functions: they say their text in the language shown now.
type numSetting struct {
	key        string
	label, tip func() string
	lo, hi     int
	unit       string
	text       func(f *settingsForm) *string
	get        func(core.Settings) int
	set        func(*core.Settings, int)
}

var numSettings = []numSetting{
	{"upload", locales.SettingsUploads, locales.SettingsUploadsHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.upload }, func(s core.Settings) int { return s.Limits.Upload }, func(s *core.Settings, n int) { s.Limits.Upload = n }},
	{"download", locales.SettingsDownloadsN, locales.SettingsDownloadsNHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.download }, func(s core.Settings) int { return s.Limits.Download }, func(s *core.Settings, n int) { s.Limits.Download = n }},
	{"other", locales.SettingsOther, locales.SettingsOtherHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.other }, func(s core.Settings) int { return s.Limits.Other }, func(s *core.Settings, n int) { s.Limits.Other = n }},
	{"workers", locales.SettingsWorkers, locales.SettingsWorkersHelp, 1, 64, "",
		func(f *settingsForm) *string { return &f.workers }, func(s core.Settings) int { return s.BulkWorkers }, func(s *core.Settings, n int) { s.BulkWorkers = n }},
	{"part", locales.SettingsPart, locales.SettingsPartHelp, 5, 5120, " MiB",
		func(f *settingsForm) *string { return &f.part }, func(s core.Settings) int { return s.PartSizeMiB }, func(s *core.Settings, n int) { s.PartSizeMiB = n }},
	{"upRate", locales.SettingsUploadRate, locales.SettingsRateHelp, 0, core.MaxBandwidthKiBps, " KiB/s",
		func(f *settingsForm) *string { return &f.upRate }, func(s core.Settings) int { return s.UploadKiBps }, func(s *core.Settings, n int) { s.UploadKiBps = n }},
	{"downRate", locales.SettingsDownloadRate, locales.SettingsRateHelp, 0, core.MaxBandwidthKiBps, " KiB/s",
		func(f *settingsForm) *string { return &f.downRate }, func(s core.Settings) int { return s.DownloadKiBps }, func(s *core.Settings, n int) { s.DownloadKiBps = n }},
	{"recentOps", locales.SettingsRecentOps, locales.SettingsRecentOpsHelp, core.MinRecentOps, core.MaxRecentOps, "",
		func(f *settingsForm) *string { return &f.recentOps }, func(s core.Settings) int { return s.RecentOps }, func(s *core.Settings, n int) { s.RecentOps = n }},
}

// loadSettingsForm fills the form from the saved settings.
func (a *app) loadSettingsForm() {
	st := a.svc.Settings()
	f := &a.sf
	*f = settingsForm{loaded: true, errs: map[string]string{}, saved: map[string]time.Time{}, focus: map[string]bool{}}
	f.upload, f.download, f.other = strconv.Itoa(st.Limits.Upload), strconv.Itoa(st.Limits.Download), strconv.Itoa(st.Limits.Other)
	f.workers, f.part = strconv.Itoa(st.BulkWorkers), strconv.Itoa(st.PartSizeMiB)
	f.upRate, f.downRate = strconv.Itoa(st.UploadKiBps), strconv.Itoa(st.DownloadKiBps)
	f.conflict, f.dir = string(st.Conflict), st.DefaultDownloadDir
	f.proxy = proxyFormOf(st.Proxy)
	f.logOn = st.LogToFile
	f.recentOps, f.keepOps = strconv.Itoa(st.RecentOps), st.KeepRecentOps
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
	delete(a.sf.saved, key)
	if a.settingsPending == nil {
		a.settingsPending = map[string]chan struct{}{}
	}
	prev, done := a.settingsTail, make(chan struct{})
	a.settingsTail, a.settingsPending[key] = done, done
	a.busyOps.Add(1)
	go func() {
		if prev != nil {
			<-prev
		}
		a.settingsMu.Lock()
		st := a.svc.Settings()
		mutate(&st)
		err := a.svc.SaveSettings(st)
		a.settingsMu.Unlock()
		close(done)
		a.post(func() {
			a.busyOps.Add(-1)
			if a.settingsPending[key] != done {
				return
			}
			delete(a.settingsPending, key)
			if err != nil {
				if revert != nil {
					revert()
				}
				msg := locales.SettingsErrSave(humanError(err))
				if key == "autolock" {
					msg = locales.VaultSaveFailed(humanError(err))
				}
				a.sfFail(key, msg)
				if a.tool != "settings" || key == "autolock" {
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
	if n == cur && a.settingsPending[ns.key] == nil {
		return
	}
	a.applySetting(ns.key, func(st *core.Settings) { ns.set(st, n) }, func() { *ns.text(&a.sf) = strconv.Itoa(ns.get(a.svc.Settings())) })
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
	if d == cur && a.settingsPending["dir"] == nil {
		return
	}
	a.applySetting("dir", func(st *core.Settings) { st.DefaultDownloadDir = d }, func() { a.sf.dir = a.svc.Settings().DefaultDownloadDir })
}

// commitConflict saves the file-exists policy.
func (a *app) commitConflict() {
	cur := string(a.svc.Settings().Conflict)
	v := a.sf.conflict
	if v == cur && a.settingsPending["conflict"] == nil {
		return
	}
	a.applySetting("conflict", func(st *core.Settings) { st.Conflict = core.ConflictPolicy(v) }, func() { a.sf.conflict = string(a.svc.Settings().Conflict) })
}

// setTheme applies a theme at once and saves the choice.
func (a *app) setTheme(mode string) {
	prev := a.themeMode()
	if mode == prev {
		return
	}
	a.opts.theme = mode
	a.applySetting("theme", func(st *core.Settings) { st.Theme = mode }, func() { a.opts.theme = a.svc.Settings().Theme })
}

// colorsOf is the palette chosen for the dark theme or for the light one.
func (a *app) colorsOf(dark bool) palette {
	if dark {
		return paletteOf(true, a.darkColors)
	}
	return paletteOf(false, a.lightColors)
}

// setColors gives the dark theme or the light one the palette id, at once,
// and saves the choice.
func (a *app) setColors(dark bool, id string) {
	if a.colorsOf(dark).id == id {
		return
	}
	key := "lightColors"
	if dark {
		key = "darkColors"
		a.darkColors = id
	} else {
		a.lightColors = id
	}
	a.applySetting(key, func(st *core.Settings) {
		if dark {
			st.DarkColors = id
		} else {
			st.LightColors = id
		}
	}, func() { a.lightColors, a.darkColors = a.svc.Settings().LightColors, a.svc.Settings().DarkColors })
}

// setLogToFile switches the diagnostic log at once and saves the choice.
func (a *app) setLogToFile(on bool) {
	a.applySetting("log", func(st *core.Settings) { st.LogToFile = on }, func() { a.sf.logOn = a.svc.Settings().LogToFile })
}

// setKeepRecentOps chooses whether the list of recent operations survives a
// change of account, and saves the choice. It takes effect at the next change.
func (a *app) setKeepRecentOps(on bool) {
	a.applySetting("keepOps", func(st *core.Settings) { st.KeepRecentOps = on }, func() { a.sf.keepOps = a.svc.Settings().KeepRecentOps })
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
//
// The rows and cards are in settings_layout.go.

// savedFor is how long the "Saved" mark stays beside its control.
const savedFor = 2500 * time.Millisecond

// savedMark is the quiet "Saved" confirmation before a setting's control, for
// savedFor after the setting was saved. It stands at the left of the control,
// so the control keeps its place at the edge of the card.
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
	ui.Row(c.Key("saved:" + key)).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
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
	a.settingsRowHelp(c, ns.label(), ns.tip(), f.errs[ns.key], func() {
		a.savedMark(c, ns.key)
		in := inputBox(c, ns.text(f), ns.label(), "", inputOpts{width: settingsNumW, invalid: f.errs[ns.key] != "", suffix: strings.TrimSpace(ns.unit)})
		a.commitOnLeave(ns.key, in, func() { a.commitNumber(ns) })
	})
}

func (a *app) settingsView(c *ui.Context) {
	if !a.sf.loaded {
		a.loadSettingsForm()
	}
	n := &a.srail
	if !n.bp.set {
		n.bp.at = settingsRailNeed
	}
	rail := !n.bp.update(a.lay.barW)
	ui.Row(c.Key("settings")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if rail {
			a.settingsRailView(c, n.current())
		}
		left := float32(settingsPad)
		if rail {
			left = 8 // the rail's own padding parts it from the column
		}
		page := ui.Scroll(c).TrackScroll(&n.scroll).Grow(1).MinWidth(0).MinHeight(0).ScrollbarInsets(0, 2, 0, 0)
		n.viewY = page.Bounds().Y
		// A page wider than the column keeps it in the middle, so the free
		// space falls on both sides instead of all at the right.
		page.Children(func() {
			ui.Row(c).Justify(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MaxWidth(settingsColW+left+settingsPad).Padding(settingsPad-settingsGroupGap, settingsPad, settingsPad+8, left).Children(func() {
					a.appearanceSection(c)
					a.transfersSection(c)
					a.downloadsSection(c)
					a.networkSection(c)
					a.securitySection(c)
					a.diagnosticsSection(c)
					a.aboutSection(c)
				})
			})
		})
		n.lasY = n.scroll.Y
	})
}

// colorsRow is the row that chooses the palette of the dark theme or of the
// light one: a picture of each, as the theme row has.
func (a *app) colorsRow(c *ui.Context, dark bool, label, help string) {
	key := "lightColors"
	if dark {
		key = "darkColors"
	}
	a.settingsRowHelp(c, label, help, a.sf.errs[key], func() {
		a.savedMark(c, key)
		ps := palettesOf(dark)
		picks := make([]themePick, len(ps))
		sel := 0
		for i, p := range ps {
			picks[i] = themePick{p.label(), p.k, nil}
			if p.id == a.colorsOf(dark).id {
				sel = i
			}
		}
		if themePicker(c, &sel, label, picks) {
			a.setColors(dark, ps[sel].id)
		}
	})
}

func (a *app) appearanceSection(c *ui.Context) {
	f := &a.sf
	a.settingsGroup(c, "appearance", func() {
		a.settingsCard(c, func() {
			a.settingsRowHelp(c, locales.SettingsTheme(), locales.SettingsThemeHelp(), f.errs["theme"], func() {
				a.savedMark(c, "theme")
				// The mode lives in the app (the menu and the palette change it
				// too), so the control shows it each frame.
				sel := max(0, slices.Index(themeModes, a.themeMode()))
				light, dark := a.colorsOf(false).k, a.colorsOf(true).k
				labels := themeLabels()
				if themePicker(c, &sel, locales.SettingsTheme(), []themePick{{labels[0], light, &dark}, {labels[1], light, nil}, {labels[2], dark, nil}}) {
					a.setTheme(themeModes[sel])
				}
			})
			a.colorsRow(c, false, locales.SettingsLightColors(), locales.SettingsLightColorsHelp())
			a.colorsRow(c, true, locales.SettingsDarkColors(), locales.SettingsDarkColorsHelp())
			a.settingsRowHelp(c, locales.SettingsLanguage(), locales.SettingsLanguageHelp(), "", func() {
				// The choice lives in the app (a.lang); the select edits a copy so
				// that it shows the real language each frame.
				lang := a.lang
				if selectBox(c, locales.SettingsLanguage(), &lang, languageChoices(), settingsSelW) {
					a.setLanguage(lang)
				}
			})
			trayLabel := locales.SettingsTray()
			if runtime.GOOS == "darwin" {
				trayLabel = locales.SettingsTrayMac()
			}
			a.settingsRowHelp(c, trayLabel, locales.SettingsTrayHelp(), f.errs["tray"], func() {
				a.savedMark(c, "tray")
				// The choice lives in the app (a.tray.on); the switch edits a copy.
				on := a.tray.on
				if switchControl(c, &on, trayLabel).Changed() {
					a.setTrayShown(on)
				}
			})
			if a.tray.on {
				a.settingsRowHelp(c, locales.SettingsQuitIdle(), locales.SettingsQuitIdleHelp(), f.errs["quitIdle"], func() {
					a.savedMark(c, "quitIdle")
					on := a.tray.quitIdle
					if switchControl(c, &on, locales.SettingsQuitIdle()).Changed() {
						a.setQuitWhenIdle(on)
					}
				})
			}
		})
	})
}

func (a *app) transfersSection(c *ui.Context) {
	f := &a.sf
	a.settingsGroup(c, "transfers", func() {
		a.settingsCard(c, func() {
			for _, ns := range numSettings[:7] {
				a.numberRow(c, ns)
			}
			a.settingsRowHelp(c, locales.SettingsConflictRow(), locales.SettingsConflictHelp(), f.errs["conflict"], func() {
				a.savedMark(c, "conflict")
				if selectBox(c, locales.SettingsConflictLabel(), &f.conflict, conflictChoices(), settingsSelW) {
					a.commitConflict()
				}
			})
		})
	})
}

func (a *app) downloadsSection(c *ui.Context) {
	f := &a.sf
	a.settingsGroup(c, "downloads", func() {
		a.settingsCard(c, func() {
			a.settingsRowHelp(c, locales.SettingsDir(), locales.SettingsDirHelp(), f.errs["dir"], func() {
				a.savedMark(c, "dir")
				ui.Box(c).Basis(settingsWideW).Shrink(1).MinWidth(120).Children(func() {
					in := inputBox(c, &f.dir, locales.SettingsDir(), locales.SettingsDirPlaceholder(), inputOpts{invalid: f.errs["dir"] != "", mono: f.dir != ""})
					a.commitOnLeave("dir", in, a.commitDir)
				})
				if secondaryButton(c, locales.CommonChoose(), false).Shrink(0).Clicked() {
					a.chooseDownloadDir()
				}
				if f.dir != "" {
					ghostButton(c, icons.Get("x"), "", locales.SettingsClear(), false).Label(locales.SettingsClearLabel()).Shrink(0).OnClick(func() {
						f.dir = ""
						a.commitDir()
					})
				}
			})
		})
	})
}

func (a *app) diagnosticsSection(c *ui.Context) {
	f := &a.sf
	a.settingsGroup(c, "diagnostics", func() {
		a.settingsCard(c, func() {
			a.settingsRowHelp(c, locales.SettingsLog(), locales.SettingsLogHelp(), f.errs["log"], func() {
				a.savedMark(c, "log")
				if switchControl(c, &f.logOn, locales.SettingsLog()).Changed() {
					a.setLogToFile(f.logOn)
				}
			})
			a.settingsRow(c, locales.SettingsLogFiles(), "", func() {
				if secondaryButton(c, locales.SettingsOpenLogDir(), false).Tooltip(locales.SettingsOpenLogDirTip()).Clicked() {
					a.openLogDir()
				}
			})
			a.numberRow(c, numSettings[7])
			a.settingsRowHelp(c, locales.SettingsKeepRecentOps(), locales.SettingsKeepRecentOpsHelp(), f.errs["keepOps"], func() {
				a.savedMark(c, "keepOps")
				if switchControl(c, &f.keepOps, locales.SettingsKeepRecentOps()).Changed() {
					a.setKeepRecentOps(f.keepOps)
				}
			})
		})
	})
}
