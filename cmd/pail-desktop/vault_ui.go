package main

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The encrypted vault in the UI (APP_API.md section 1.1): the Security section
// of Settings, the dialogs that create, unlock, change and reset the vault, the
// lock indicator in the status bar and the retry of an action that failed
// because the vault was locked. Every service call runs on a goroutine; the
// answer comes back through post, like the rest of the app.

const (
	minPasswordRunes = 8
	maxPasswordBytes = 1024
	resetWord        = "RESET"
)

// goos is runtime.GOOS; tests change it to see the other platforms' wording.
var goos = runtime.GOOS

// keychainName is the system secret store of this platform.
func keychainName() string {
	switch goos {
	case "windows":
		return "Windows Credential Manager"
	case "darwin":
		return "macOS Keychain"
	}
	return "Secret Service"
}

// pwDialog is the state shared by the password dialogs. Which fields a dialog
// uses depends on the dialog; err belongs to one field, fail to the footer.
type pwDialog struct {
	open, busy, tried bool
	old, pw, pw2      string
	err, fail         string
	// existing: creating the vault found a vault file, so its password opens it.
	// needPW: moving back to the keychain needs the password because the vault is locked.
	existing, needPW bool
}

// vaultUI is the vault part of the app state.
type vaultUI struct {
	backend     string          // cached Service.SecretBackend
	state       core.VaultState // cached Service.VaultState
	at          time.Time       // when the cache was read
	keyringDown bool            // a secret operation reported ErrKeyringUnavailable

	create, unlock, change, toKeychain pwDialog
	reset                              resetState

	// retries are the actions that failed with ErrLocked, run once the vault is open.
	retries  map[string]func()
	order    []string
	lastAuto map[string]time.Time // when a key was last retried without asking
	// lastReset is the outcome of the last ResetVault, shown under the section.
	lastReset *core.VaultReset
}

type resetState struct {
	open, busy bool
	typed, err string
	accounts   []string // names that will need their secret again
	refs       []string
}

func (v *vaultUI) busyNow() bool {
	return v.create.busy || v.unlock.busy || v.change.busy || v.toKeychain.busy || v.reset.busy
}

func (v *vaultUI) modal() bool {
	return v.create.open || v.unlock.open || v.change.open || v.toKeychain.open || v.reset.open
}

// ---- state --------------------------------------------------------------------------------

// refreshVault reads the backend and the lock state again. Cheap: no keychain.
func (a *app) refreshVault() {
	v := &a.vu
	v.backend = a.svc.SecretBackend()
	v.state = a.svc.VaultState()
	v.at = time.Now()
}

func (a *app) vaultMode() bool   { return a.vu.backend == core.SecretBackendVault }
func (a *app) vaultLocked() bool { return a.vaultMode() && !a.vu.state.Unlocked }

// vaultPoll refreshes the lock indicator now and then, so an idle auto-lock
// shows without a click. It stops with the app.
func (a *app) vaultPoll() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			if a.svc.SecretBackend() == core.SecretBackendVault {
				a.post(a.refreshVault)
			}
		}
	}
}

// vaultAtStart asks for the master password when the vault is the backend, is
// locked and an account needs a secret from it.
func (a *app) vaultAtStart() {
	a.refreshVault()
	if !a.vaultLocked() {
		return
	}
	for _, ac := range a.accounts {
		if ac.Credentials == core.CredStatic && !ac.SecretMissing {
			a.askUnlock("start", nil)
			return
		}
	}
}

func kindOf(err error) (core.ErrKind, bool) {
	var oe *core.OpError
	if errors.As(err, &oe) {
		return oe.Kind, true
	}
	return 0, false
}

func isLocked(err error) bool {
	k, ok := kindOf(err)
	return err != nil && (errors.Is(err, core.ErrVaultLocked) || ok && k == core.ErrLocked)
}

func isDenied(err error) bool {
	k, ok := kindOf(err)
	return ok && k == core.ErrAccessDenied
}

// noteKeyring remembers that the system keychain is missing.
func (a *app) noteKeyring(err error) {
	if err != nil && errors.Is(err, core.ErrKeyringUnavailable) {
		a.vu.keyringDown = true
	}
}

// noteErr words err for the screen and reacts to the two vault failures: a
// locked vault opens the unlock dialog and, once unlocked, runs retry (under
// key, so one action is not retried twice); a missing keychain is remembered
// for Settings and the account dialog.
func (a *app) noteErr(err error, key string, retry func()) string {
	a.noteKeyring(err)
	if isLocked(err) {
		a.askUnlock(key, retry)
	}
	return humanError(err)
}

// ---- unlock -----------------------------------------------------------------------------------

// askUnlock opens the unlock dialog. retry (may be nil) runs on the UI thread
// after a successful unlock.
func (a *app) askUnlock(key string, retry func()) {
	a.refreshVault()
	u := &a.vu
	if !a.vaultLocked() { // a stale answer: the vault was opened meanwhile
		if retry != nil && time.Since(u.lastAuto[key]) > 2*time.Second {
			if u.lastAuto == nil {
				u.lastAuto = map[string]time.Time{}
			}
			u.lastAuto[key] = time.Now()
			a.post(retry)
			return
		}
		if retry == nil || !a.vaultMode() {
			return
		}
	}
	if retry != nil {
		if u.retries == nil {
			u.retries = map[string]func(){}
		}
		if _, ok := u.retries[key]; !ok {
			u.order = append(u.order, key)
		}
		u.retries[key] = retry
	}
	if !u.unlock.open {
		u.unlock = pwDialog{open: true}
	}
}

func (a *app) runRetries() {
	u := &a.vu
	order, rs := u.order, u.retries
	u.order, u.retries = nil, nil
	for _, k := range order {
		rs[k]()
	}
}

func (a *app) closeUnlock() {
	a.vu.unlock = pwDialog{}
	a.vu.retries, a.vu.order = nil, nil
}

func (a *app) submitUnlock() {
	d := &a.vu.unlock
	if d.busy {
		return
	}
	if d.pw == "" {
		d.err = locales.VaultEnterMaster()
		return
	}
	pw := d.pw
	d.busy, d.err = true, ""
	go func() {
		err := a.svc.UnlockVault(pw) // Argon2id: a few tenths of a second
		a.post(func() {
			d.busy = false
			if err != nil {
				d.setErr(err)
				return
			}
			a.vu.unlock = pwDialog{}
			a.refreshVault()
			a.toast(locales.VaultUnlocked())
			a.runRetries()
		})
	}()
}

// setErr files a service error under the password field (wrong password) or in
// the footer.
func (d *pwDialog) setErr(err error) {
	if isDenied(err) {
		d.err = locales.VaultWrongPassword()
		return
	}
	d.fail = humanError(err)
}

func (a *app) lockVault() {
	if !a.vaultMode() {
		return
	}
	a.bg(func() func() {
		a.svc.LockVault()
		return func() {
			a.refreshVault()
			a.toast(locales.VaultLocked())
		}
	})
}

// ---- switching the backend --------------------------------------------------------------------

func (a *app) askBackend(choice int) {
	if choice == 1 {
		a.refreshVault()
		a.vu.create = pwDialog{open: true, existing: a.vu.state.Exists}
		return
	}
	a.refreshVault()
	a.vu.toKeychain = pwDialog{open: true, needPW: !a.vu.state.Unlocked}
}

func pwProblem(pw string) string {
	switch {
	case utf8.RuneCountInString(pw) < minPasswordRunes:
		return locales.VaultMinChars(strconv.Itoa(minPasswordRunes))
	case len(pw) > maxPasswordBytes:
		return locales.VaultMaxBytes(strconv.Itoa(maxPasswordBytes))
	}
	return ""
}

// newPasswordProblems checks a new password and its confirmation.
func newPasswordProblems(pw, again string, tried bool) (pwErr, againErr string) {
	if tried || again != "" {
		pwErr = pwProblem(pw)
		switch {
		case again == "" && tried:
			againErr = locales.VaultEnterAgain()
		case again != "" && again != pw:
			againErr = locales.VaultMismatch()
		}
	}
	return
}

func (d *pwDialog) createProblems() (pwErr, againErr string) {
	if d.existing {
		if d.tried && d.pw == "" {
			pwErr = locales.VaultEnterMaster()
		}
	} else {
		pwErr, againErr = newPasswordProblems(d.pw, d.pw2, d.tried)
	}
	if pwErr == "" {
		pwErr = d.err
	}
	return
}

func (a *app) submitCreate() {
	d := &a.vu.create
	if d.busy {
		return
	}
	d.tried, d.err, d.fail = true, "", ""
	if p, q := d.createProblems(); p != "" || q != "" {
		return
	}
	pw := d.pw
	d.busy = true
	ctx, cancel := a.timeout(2 * time.Minute)
	go func() {
		defer cancel()
		err := a.svc.SetSecretBackend(ctx, core.SecretBackendVault, pw)
		a.post(func() {
			d.busy = false
			a.noteKeyring(err)
			a.refreshVault()
			// A cleanup failure after the switch still leaves the vault in use.
			if err != nil && !a.vaultMode() {
				d.setErr(err)
				return
			}
			a.vu.create = pwDialog{}
			if err != nil {
				a.toast(humanError(err))
				return
			}
			a.vu.keyringDown = false
			a.toast(locales.VaultNowInVault())
		})
	}()
}

func (a *app) submitToKeychain() {
	d := &a.vu.toKeychain
	if d.busy {
		return
	}
	d.tried, d.err, d.fail = true, "", ""
	if d.needPW && d.pw == "" {
		d.err = locales.VaultEnterMaster()
		return
	}
	pw := d.pw
	d.busy = true
	ctx, cancel := a.timeout(2 * time.Minute)
	go func() {
		defer cancel()
		err := a.svc.SetSecretBackend(ctx, core.SecretBackendKeyring, pw)
		a.post(func() {
			d.busy = false
			a.noteKeyring(err)
			a.refreshVault()
			if err != nil && a.vaultMode() {
				d.setErr(err)
				return
			}
			a.vu.toKeychain = pwDialog{}
			a.vu.retries, a.vu.order = nil, nil
			if err != nil {
				a.toast(humanError(err))
				return
			}
			a.toast(locales.VaultMovedTo(keychainName()))
		})
	}()
}

func (a *app) submitChange() {
	d := &a.vu.change
	if d.busy {
		return
	}
	d.tried, d.err, d.fail = true, "", ""
	_, againErr := newPasswordProblems(d.pw, d.pw2, true)
	if d.old == "" || pwProblem(d.pw) != "" || againErr != "" {
		return
	}
	old, pw := d.old, d.pw
	d.busy = true
	go func() {
		err := a.svc.ChangeVaultPassword(old, pw)
		a.post(func() {
			d.busy = false
			if err != nil {
				d.setErr(err)
				return
			}
			a.vu.change = pwDialog{}
			a.refreshVault()
			a.toast(locales.VaultPasswordChanged())
		})
	}()
}

func (a *app) setAutoLock(minutes int) {
	a.vu.state.AutoLockMinutes = minutes
	a.bg(func() func() {
		a.settingsMu.Lock()
		st := a.svc.Settings()
		st.VaultAutoLockMinutes = minutes
		err := a.svc.SaveSettings(st)
		a.settingsMu.Unlock()
		return func() {
			a.refreshVault()
			if err != nil {
				a.toast(locales.VaultSaveFailed(humanError(err)))
				return
			}
			if a.sf.saved != nil {
				a.sf.saved["autolock"] = time.Now()
			}
		}
	})
}

// openReset lists what a reset loses before asking for the word RESET.
func (a *app) openReset() {
	a.refreshVault()
	r := resetState{open: true}
	for _, ac := range a.accounts {
		if ac.Credentials == core.CredStatic && !ac.SecretMissing {
			r.accounts = append(r.accounts, ac.Name)
		}
	}
	r.refs = a.svc.SecretRefs()
	a.vu.reset = r
}

func (a *app) submitReset() {
	r := &a.vu.reset
	if r.busy || r.typed != resetWord {
		return
	}
	r.busy, r.err = true, ""
	a.bg(func() func() {
		res, err := a.svc.ResetVault()
		return func() {
			r.busy = false
			if err != nil {
				r.err = humanError(err)
				return
			}
			a.vu.reset = resetState{}
			a.closeUnlock()
			a.vu.lastReset = &res
			a.refreshVault()
			a.accounts = a.svc.Accounts()
			a.toast(locales.VaultResetDone())
			if a.account().SecretMissing {
				a.loadBuckets(true)
			}
		}
	})
}

// ---- Settings: Security -------------------------------------------------------------------------

// settingsNote is a muted note under a row's control, one text per line.
func settingsNote(c *ui.Context, lines ...string) {
	ui.Column(c.Key("snote:" + lines[0])).MarginStart(216).Children(func() {
		for _, s := range lines {
			ui.Text(c, s).FontSize(sizeSecondary).TextColor(tk(c).TextMuted)
		}
	})
}

// warnNotice is a Warning-tinted line with a triangle icon; the text keeps the
// Text color so it reads at full contrast.
func warnNotice(c *ui.Context, msg string) {
	k := tk(c)
	ui.Row(c).Gap(8).Padding(8, 10).Radius(6).AlignItems(ui.Start).
		Background(k.Warning.Alpha(0.10)).Border(1, k.Warning.Alpha(0.40)).Children(func() {
		ui.Icon(c, icons.Get("triangle-alert")).FontSize(14).TextColor(k.Warning).Shrink(0).MarginY(1)
		ui.Text(c, msg).FontSize(sizeSecondary).TextColor(k.Text).Grow(1).MinWidth(0)
	})
}

func autoLockChoices(cur int) []choice {
	mins := []int{0, 5, 15, 30, 60}
	found := false
	for _, m := range mins {
		found = found || m == cur
	}
	if !found {
		mins = append(mins, cur)
	}
	out := make([]choice, 0, len(mins))
	for _, m := range mins {
		label := locales.VaultNever()
		if m >= 1 {
			label = locales.VaultMinutes(m)
		}
		out = append(out, choice{strconv.Itoa(m), label})
	}
	return out
}

func (a *app) securitySection(c *ui.Context) {
	k := tk(c)
	u := &a.vu
	settingsSection(c, locales.VaultSecurity())
	if u.keyringDown && !a.vaultMode() {
		warnNotice(c, locales.VaultNoKeychain())
	}
	if r := u.lastReset; r != nil {
		a.resetNotice(c, r)
	}
	cur := 0
	if a.vaultMode() {
		cur = 1
	}
	settingsRowHelp(c, locales.VaultStorage(), locales.VaultStorageHelp(keychainName()), "", func() {
		// The control shows the real backend each frame; activating the other
		// segment only opens the dialog that does the move (segmentedAction:
		// nothing but a click, Enter or Space on it does).
		if sel, ok := segmentedAction(c, cur, locales.VaultStorage(), locales.VaultSystemKeychain(), locales.VaultEncryptedVault()); ok {
			a.askBackend(sel)
		}
	})
	if !a.vaultMode() {
		settingsNote(c, locales.VaultStoredIn(keychainName()))
		return
	}
	// Two lines, so the algorithm names never break at their hyphens.
	settingsNote(c, locales.VaultEncryptedWith(), "(Argon2id, XChaCha20-Poly1305)")
	settingsRow(c, locales.VaultRowVault(), "", func() {
		unlocked := u.state.Unlocked
		icon, text := "lock", locales.VaultStateLocked()
		if unlocked {
			icon, text = "lock-open", locales.VaultStateUnlocked()
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(88).Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(k.TextMuted)
			ui.Text(c, text).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
		})
		if unlocked {
			if secondaryButton(c, locales.VaultLockNow(), false).Clicked() {
				a.lockVault()
			}
		} else if secondaryButton(c, locales.VaultUnlockEllipsis(), false).Clicked() {
			a.askUnlock("settings", nil)
		}
	})
	settingsRow(c, locales.VaultMasterPassword(), "", func() {
		if secondaryButton(c, locales.VaultChangePasswordEllipsis(), false).Clicked() {
			a.vu.change = pwDialog{open: true}
		}
		if dangerOutlineButton(c, locales.VaultResetEllipsis(), false).Clicked() {
			a.openReset()
		}
	})
	settingsRowHelp(c, locales.VaultAutoLock(), locales.VaultAutoLockHelp(), "", func() {
		val := strconv.Itoa(u.state.AutoLockMinutes)
		if selectBox(c, locales.VaultAutoLock(), &val, autoLockChoices(u.state.AutoLockMinutes), 160) {
			n, _ := strconv.Atoi(val)
			a.setAutoLock(n)
		}
		a.savedMark(c, "autolock")
	})
}

// resetNotice names what the last reset left to enter again.
func (a *app) resetNotice(c *ui.Context, r *core.VaultReset) {
	var names []string
	for _, id := range r.Accounts {
		for _, ac := range a.accounts {
			if ac.ID == id && ac.SecretMissing {
				names = append(names, ac.Name)
			}
		}
	}
	if len(names) > 0 {
		warnNotice(c, locales.VaultSecretMissingFor(strings.Join(names, ", ")))
	}
	if len(r.Refs) > 0 {
		warnNotice(c, locales.VaultRefsLost(strings.Join(r.Refs, ", ")))
	}
}

// ---- status bar -----------------------------------------------------------------------------------

// lockIndicator is the lock in the status bar: shown only with the vault
// backend, an icon and a word (the shape changes, not only the color); a click
// locks, or opens the unlock dialog.
func (a *app) lockIndicator(c *ui.Context) {
	if !a.vaultMode() {
		return
	}
	k := tk(c)
	unlocked := a.vu.state.Unlocked
	icon, text, tip := "lock", locales.VaultLocked(), locales.VaultLockedTip()
	if unlocked {
		icon, text, tip = "lock-open", locales.VaultUnlocked(), locales.VaultUnlockedTip()
	}
	b := ui.ButtonBase(c.Key("vaultlock")).Label(text).Tooltip(tip).Height(20).Padding(0, 6).Gap(5).Radius(4).AlignItems(ui.Center).Transition(hoverFade())
	if b.Hovered() {
		b.Background(k.SurfaceHover)
	}
	b.Children(func() {
		col := k.TextMuted
		if unlocked {
			col = k.Text
		}
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(col)
		ui.Text(c, text).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
	})
	if b.Clicked() {
		if unlocked {
			a.lockVault()
		} else {
			a.askUnlock("indicator", nil)
		}
	}
}

// ---- dialogs ------------------------------------------------------------------------------------------

func (a *app) vaultDialogs(c *ui.Context) {
	a.createVaultDialog(c)
	a.toKeychainDialog(c)
	a.changePasswordDialog(c)
	a.resetDialog(c)
	a.unlockDialog(c) // on top: any dialog can need it
}

// pwField is a password input with its label and error line; Enter submits.
func pwField(c *ui.Context, label string, value *string, err string, focus bool, onChange, submit func()) {
	formField(c, label, err, func() {
		in := inputBox(c, value, label, "", inputOpts{password: true, invalid: err != "", focus: focus})
		if in.Changed() && onChange != nil {
			onChange()
		}
		if in.Submitted() {
			submit()
		}
	})
}

// pwFooter is the footer of the password dialogs: failure line, busy line,
// buttons.
func pwFooter(c *ui.Context, d *pwDialog, busyText, action string, cancel, submit func()) {
	k := tk(c)
	if d.fail != "" {
		resultLine(c, "circle-alert", k.Danger, d.fail)
	}
	if d.busy {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, busyText)
		})
	}
	buttonRow(c, func() {
		if secondaryButton(c, locales.CommonCancel(), d.busy).Clicked() {
			cancel()
		}
		if primaryButton(c, action, d.busy).Clicked() {
			submit()
		}
	})
}

func (a *app) createVaultDialog(c *ui.Context) {
	d := &a.vu.create
	if !d.open {
		return
	}
	title := locales.VaultCreateTitle()
	if d.existing {
		title = locales.VaultExistingTitle()
	}
	clear := func() { d.err, d.fail = "", "" }
	was := d.open
	dialog(c, &d.open, title, func() {
		pe, ce := d.createProblems()
		if d.existing {
			mutedText(c, locales.VaultExistingNote())
			pwField(c, locales.VaultMasterPassword(), &d.pw, pe, true, clear, a.submitCreate)
			return
		}
		mutedText(c, locales.VaultCreateNote())
		warnNotice(c, locales.VaultForgotten())
		pwField(c, locales.VaultMasterPassword(), &d.pw, pe, true, clear, a.submitCreate)
		pwField(c, locales.VaultConfirmPassword(), &d.pw2, ce, false, clear, a.submitCreate)
	}, func() {
		action := locales.VaultCreateAction()
		if d.existing {
			action = locales.VaultUseAction()
		}
		pwFooter(c, d, locales.VaultEncrypting(), action, func() { a.vu.create = pwDialog{} }, a.submitCreate)
	})
	if was && !d.open {
		if d.busy {
			d.open = true // the move cannot be interrupted
		} else {
			a.vu.create = pwDialog{}
		}
	}
}

func (a *app) toKeychainDialog(c *ui.Context) {
	d := &a.vu.toKeychain
	if !d.open {
		return
	}
	clear := func() { d.err, d.fail = "", "" }
	was := d.open
	dialog(c, &d.open, locales.VaultToKeychainTitle(), func() {
		mutedText(c, locales.VaultToKeychainNote(keychainName()))
		if d.needPW {
			pwField(c, locales.VaultMasterPassword(), &d.pw, d.err, true, clear, a.submitToKeychain)
		}
	}, func() {
		pwFooter(c, d, locales.VaultMoving(), locales.VaultMoveAction(), func() { a.vu.toKeychain = pwDialog{} }, a.submitToKeychain)
	})
	if was && !d.open {
		if d.busy {
			d.open = true
		} else {
			a.vu.toKeychain = pwDialog{}
		}
	}
}

func (a *app) changePasswordDialog(c *ui.Context) {
	d := &a.vu.change
	if !d.open {
		return
	}
	clear := func() { d.err, d.fail = "", "" }
	was := d.open
	dialog(c, &d.open, locales.VaultChangeTitle(), func() {
		oldErr := d.err
		if oldErr == "" && d.tried && d.old == "" {
			oldErr = locales.VaultEnterCurrent()
		}
		pe, ce := newPasswordProblems(d.pw, d.pw2, d.tried)
		warnNotice(c, locales.VaultForgotten())
		pwField(c, locales.VaultCurrentPassword(), &d.old, oldErr, true, clear, a.submitChange)
		pwField(c, locales.VaultNewPassword(), &d.pw, pe, false, clear, a.submitChange)
		pwField(c, locales.VaultConfirmNewPassword(), &d.pw2, ce, false, clear, a.submitChange)
	}, func() {
		pwFooter(c, d, locales.VaultReencrypting(), locales.VaultChangeAction(), func() { a.vu.change = pwDialog{} }, a.submitChange)
	})
	if was && !d.open {
		if d.busy {
			d.open = true
		} else {
			a.vu.change = pwDialog{}
		}
	}
}

func (a *app) unlockDialog(c *ui.Context) {
	d := &a.vu.unlock
	if !d.open {
		return
	}
	was := d.open
	dialog(c, &d.open, locales.VaultUnlockTitle(), func() {
		mutedText(c, locales.VaultUnlockNote())
		pwField(c, locales.VaultMasterPassword(), &d.pw, d.err, true, func() { d.err = "" }, a.submitUnlock)
	}, func() {
		pwFooter(c, d, locales.VaultUnlocking(), locales.VaultUnlockAction(), a.closeUnlock, a.submitUnlock)
	})
	if was && !d.open {
		if d.busy {
			d.open = true
		} else {
			a.closeUnlock()
		}
	}
}

// nameList is the Sidebar-tinted list block of the dialogs: up to 5 names, then
// "and N more".
func nameList(c *ui.Context, names []string) {
	k := tk(c)
	ui.Column(c).Gap(2).Padding(8, 10).Radius(6).Background(k.Sidebar).Children(func() {
		for i, n := range names {
			if i == 5 {
				ui.Text(c, locales.VaultAndMore(groupInt(len(names)-5))).FontSize(sizeSecondary).TextColor(k.TextMuted)
				break
			}
			ui.Text(c, n).FontSize(sizeSecondary).TextColor(k.Text).SingleLine()
		}
	})
}

func (a *app) resetDialog(c *ui.Context) {
	k := tk(c)
	r := &a.vu.reset
	if !r.open {
		return
	}
	was := r.open
	dialog(c, &r.open, locales.VaultResetTitle(), func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, locales.VaultResetLine1()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
			mutedText(c, locales.VaultResetLine2())
		})
		if len(r.accounts) > 0 {
			ui.Column(c).Gap(6).Children(func() {
				fieldLabel(c, locales.VaultResetAccounts())
				nameList(c, r.accounts)
			})
		}
		if len(r.refs) > 0 {
			ui.Column(c).Gap(6).Children(func() {
				fieldLabel(c, locales.VaultResetRefs())
				nameList(c, r.refs)
			})
		}
		formField(c, locales.VaultTypeToConfirm(resetWord), "", func() {
			in := inputBox(c, &r.typed, locales.VaultConfirmation(), resetWord, inputOpts{mono: true, focus: true})
			if in.Changed() {
				r.err = ""
			}
			if in.Submitted() {
				a.submitReset()
			}
		})
	}, func() {
		if r.err != "" {
			resultLine(c, "circle-alert", k.Danger, r.err)
		}
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), r.busy).Clicked() {
				a.vu.reset = resetState{}
			}
			if dangerButton(c, locales.VaultResetAction(), r.busy || r.typed != resetWord).Clicked() {
				a.submitReset()
			}
		})
	})
	if was && !r.open {
		if r.busy {
			r.open = true
		} else {
			a.vu.reset = resetState{}
		}
	}
}
