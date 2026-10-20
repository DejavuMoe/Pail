package main

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// acctForm is the add/edit account dialog.
type acctForm struct {
	open    bool
	editing string // account ID, "" when adding
	hasSec  bool   // the edited account already has a stored secret
	missing bool   // the edited account lost its secret (vault reset): enter it again
	// noKeychain: saving failed because the system has no keychain.
	noKeychain bool

	provider   int // 0 AWS, 1 S3-compatible
	name       string
	endpoint   string
	region     string
	path       bool
	cdnURL     string // optional CDN base URL or template
	cdnBuckets string // comma-separated; empty = all buckets
	creds      string // core.Cred*
	access     string
	secret     string
	session    string
	profile    string

	advanced   bool
	roleARN    string
	externalID string
	roleName   string
	skipTLS    bool
	checksum   string

	tried   bool // Save was pressed: show every field error
	saving  bool
	saveErr string

	test    int // testIdle, testRunning, testOK, testFailed
	testMsg string
	testGen int
}

const (
	testIdle = iota
	testRunning
	testOK
	testFailed
)

func (f *acctForm) busy() bool { return f.saving || f.test == testRunning }

func credChoices() []choice {
	return []choice{
		{core.CredStatic, locales.AcctCredAccessKeys()},
		{core.CredProfile, locales.AcctCredProfile()},
		{core.CredDefault, locales.AcctCredDefault()},
		{core.CredAnonymous, locales.AcctCredAnonymous()},
	}
}

func checksumChoices() []choice {
	return []choice{
		{"auto", locales.AcctChecksumAuto()},
		{"when_required", locales.AcctChecksumWhenRequired()},
		{"when_supported", locales.AcctChecksumWhenSupported()},
	}
}

// openAccountForm opens the dialog to add an account, or to edit the current
// one.
func (a *app) openAccountForm(edit bool) {
	f := &a.af
	gen := f.testGen + 1
	// Path-style addressing is what most S3-compatible services need, so a
	// new account starts with it on.
	*f = acctForm{open: true, creds: core.CredStatic, region: "us-east-1", checksum: "auto", path: true, testGen: gen}
	if !edit {
		return
	}
	ac := a.account()
	if ac.ID == "" {
		f.open = false
		return
	}
	f.editing = ac.ID
	f.provider = 0
	if ac.Provider == core.ProviderS3 {
		f.provider = 1
	}
	f.name, f.endpoint, f.region, f.path = ac.Name, ac.Endpoint, ac.Region, ac.PathStyle
	f.cdnURL, f.cdnBuckets = ac.CDNURL, strings.Join(ac.CDNBuckets, ", ")
	f.creds, f.access, f.profile = ac.Credentials, ac.AccessKeyID, ac.Profile
	f.hasSec = ac.Credentials == core.CredStatic && !ac.SecretMissing
	f.missing = ac.SecretMissing
	f.roleARN, f.externalID, f.roleName = ac.RoleARN, ac.ExternalID, ac.RoleSessionName
	f.skipTLS = ac.InsecureSkipVerify
	f.checksum = ac.ChecksumMode
	if f.checksum == "" {
		f.checksum = "auto"
	}
	f.advanced = f.roleARN != "" || f.skipTLS || f.checksum != "auto"
}

// fieldErrors validates the form the way the service will, field by field.
func (f *acctForm) fieldErrors() map[string]string {
	e := map[string]string{}
	if strings.TrimSpace(f.name) == "" {
		e["name"] = locales.AcctErrName()
	}
	if f.provider == 1 {
		ep := strings.TrimSpace(f.endpoint)
		u, err := url.Parse(ep)
		switch {
		case ep == "":
			e["endpoint"] = locales.AcctErrEndpointEmpty()
		case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
			e["endpoint"] = locales.AcctErrEndpointScheme()
		}
	}
	switch f.creds {
	case core.CredStatic:
		if strings.TrimSpace(f.access) == "" {
			e["access"] = locales.AcctErrAccess()
		}
		if f.secret == "" && !(f.editing != "" && f.hasSec) {
			e["secret"] = locales.AcctErrSecret()
		}
		if f.secret == "" && f.session != "" {
			e["session"] = locales.AcctErrSession()
		}
	case core.CredProfile:
		if strings.TrimSpace(f.profile) == "" {
			e["profile"] = locales.AcctErrProfile()
		}
	}
	if _, err := core.NormalizeCDNURL(f.cdnURL); err != nil {
		e["cdn"] = cdnErrText(err)
	}
	if _, err := core.NormalizeCDNBuckets(splitList(f.cdnBuckets)); err != nil {
		e["cdnBuckets"] = strings.TrimPrefix(err.Error(), "CDN buckets: ")
	}
	if r := strings.TrimSpace(f.roleARN); r != "" && !strings.HasPrefix(r, "arn:") {
		e["role"] = locales.AcctErrRole()
	}
	return e
}

// cdnErrText says the two CDN URL errors of the service in the language shown
// now; any other message stays as the service worded it.
func cdnErrText(err error) string {
	msg := err.Error()
	for _, known := range []struct {
		en  string
		say func() string
	}{
		{"CDN URL must be an http(s) URL without credentials, query or fragment", locales.AcctErrCDNURL},
		{"CDN URL template with {bucket} must also contain {key}", locales.AcctErrCDNTemplate},
	} {
		if msg == known.en {
			return known.say()
		}
	}
	return msg
}

// build turns the form into an account and, for access keys with a new
// secret, its secrets (nil keeps the stored ones).
func (f *acctForm) build() (core.Account, *core.Secrets) {
	ac := core.Account{
		ID: f.editing, Name: strings.TrimSpace(f.name), Provider: core.ProviderAWS,
		Region: strings.TrimSpace(f.region), Credentials: f.creds,
		RoleARN: strings.TrimSpace(f.roleARN), ExternalID: strings.TrimSpace(f.externalID),
		RoleSessionName: strings.TrimSpace(f.roleName), InsecureSkipVerify: f.skipTLS, ChecksumMode: f.checksum,
		CDNURL: strings.TrimSpace(f.cdnURL), CDNBuckets: splitList(f.cdnBuckets),
	}
	if f.provider == 1 {
		ac.Provider = core.ProviderS3
		ac.Endpoint = strings.TrimSpace(f.endpoint)
		ac.PathStyle = f.path
	}
	switch f.creds {
	case core.CredStatic:
		ac.AccessKeyID = strings.TrimSpace(f.access)
	case core.CredProfile:
		ac.Profile = strings.TrimSpace(f.profile)
	}
	var sec *core.Secrets
	if f.creds == core.CredStatic && f.secret != "" {
		sec = &core.Secrets{SecretAccessKey: f.secret, SessionToken: strings.TrimSpace(f.session)}
	}
	return ac, sec
}

// splitList splits a comma, semicolon or whitespace separated list.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' })
}

// testAccount checks the form's connection in the background.
func (a *app) testAccount() {
	f := &a.af
	if f.test == testRunning || f.saving {
		return
	}
	if errs := f.fieldErrors(); len(errs) > 0 {
		f.tried = true
		return
	}
	ac, sec := f.build()
	f.testGen++
	gen := f.testGen
	f.test, f.testMsg = testRunning, ""
	ctx, cancel := a.timeout(20 * time.Second)
	go func() {
		defer cancel()
		err := a.svc.TestAccount(ctx, ac, sec)
		a.post(func() {
			if gen != a.af.testGen || !a.af.open {
				return
			}
			if err != nil {
				a.af.test, a.af.testMsg = testFailed, a.noteErr(err, "acct-test", func() {
					if a.af.open {
						a.testAccount()
					}
				})
				return
			}
			a.af.test, a.af.testMsg = testOK, ""
		})
	}()
}

// saveAccount stores the form's account.
func (a *app) saveAccount() {
	f := &a.af
	if f.saving {
		return
	}
	f.tried = true
	if len(f.fieldErrors()) > 0 {
		return
	}
	ac, sec := f.build()
	editing := f.editing
	f.saving, f.saveErr = true, ""
	f.testGen++ // a running test no longer matters
	go func() {
		saved, err := a.svc.SaveAccount(ac, sec)
		a.post(func() {
			a.af.saving = false
			if saved.ID == "" && err != nil {
				a.af.saveErr = a.noteErr(err, "acct-save", func() {
					if a.af.open {
						a.saveAccount()
					}
				})
				a.af.noKeychain = errors.Is(err, core.ErrKeyringUnavailable)
				return
			}
			a.af.open = false
			a.accounts = a.svc.Accounts()
			if err != nil { // saved, but cleaning up the old secret failed
				a.toast(humanError(err))
			} else if editing == "" {
				a.toast(locales.AcctAdded(saved.Name))
			} else {
				a.toast(locales.AcctSavedAccount(saved.Name))
			}
			if editing == "" || editing != a.accountID {
				a.selectAccount(saved.ID)
				return
			}
			// The current account changed: its client was rebuilt, reload.
			id := a.accountID
			a.accountID = ""
			a.selectAccount(id)
		})
	}()
}

// askRemoveAccount opens the confirmation for the current account.
func (a *app) askRemoveAccount() {
	ac := a.account()
	if ac.ID == "" {
		return
	}
	where := ac.Region
	if ac.Endpoint != "" {
		where = ac.Endpoint
	}
	a.confirm = confirmState{
		open: true, title: locales.AcctRemoveTitle(), danger: true, action: locales.CommonRemove(),
		rows:    []KV{{locales.CommonAccount(), ac.Name}, {locales.AcctWhere(), where}},
		message: locales.AcctRemoveMessage(),
		run: func() {
			a.bg(func() func() {
				err := a.svc.DeleteAccount(ac.ID)
				return func() {
					a.refreshAccounts()
					if err != nil {
						a.toast(humanError(err))
						return
					}
					a.toast(locales.AcctRemoved(ac.Name))
				}
			})
		},
	}
}

// ---- view -----------------------------------------------------------------------------------

const acctDialogW = 480

func (a *app) accountDialog(c *ui.Context) {
	f := &a.af
	title := locales.AcctTitleAdd()
	if f.editing != "" {
		title = locales.AcctTitleEdit()
	}
	dialogW(c, &f.open, title, acctDialogW, func() {
		var errs map[string]string
		if f.tried {
			errs = f.fieldErrors()
		}
		ui.Column(c).Gap(12).Children(func() {
			a.accountFields(c, errs)
		})
	}, func() { a.accountFooter(c) })
	if !f.open && f.test == testRunning {
		f.testGen++
		f.test = testIdle
	}
}

func (a *app) accountFields(c *ui.Context, errs map[string]string) {
	f := &a.af
	k := tk(c)
	if f.missing && f.creds == core.CredStatic {
		warnNotice(c, locales.AcctSecretMissing())
	}
	formFieldHelp(c, locales.AcctProvider(), locales.AcctProviderHelp(), "", func() {
		if segmented(c, &f.provider, locales.AcctProvider(), "AWS", locales.AcctS3Compatible()) {
			f.test = testIdle
		}
	})
	formField(c, locales.CommonName(), errs["name"], func() {
		inputBox(c, &f.name, locales.CommonName(), locales.AcctNamePlaceholder(), inputOpts{invalid: errs["name"] != "", focus: !f.missing})
	})
	if f.provider == 1 {
		formFieldHelp(c, locales.AcctEndpoint(), locales.AcctEndpointHelp(), errs["endpoint"], func() {
			inputBox(c, &f.endpoint, locales.AcctEndpoint(), "https://s3.example.com", inputOpts{invalid: errs["endpoint"] != "", mono: true})
		})
	}
	formFieldHelp(c, locales.CommonRegion(), locales.AcctRegionHelp(), "", func() {
		inputBox(c, &f.region, locales.CommonRegion(), "us-east-1", inputOpts{})
	})
	if f.provider == 1 {
		switchRowHelp(c, locales.AcctPathStyle(), locales.AcctPathStyleHelp(), &f.path)
	}
	formFieldHelp(c, locales.AcctCDNURL(), locales.AcctCDNURLHelp(), errs["cdn"], func() {
		inputBox(c, &f.cdnURL, locales.AcctCDNURL(), "https://cdn.example.com", inputOpts{invalid: errs["cdn"] != "", mono: true})
	})
	formFieldHelp(c, locales.AcctCDNBuckets(), locales.AcctCDNBucketsHelp(), errs["cdnBuckets"], func() {
		inputBox(c, &f.cdnBuckets, locales.AcctCDNBuckets(), locales.AcctAllBuckets(), inputOpts{invalid: errs["cdnBuckets"] != "", mono: true})
	})
	formFieldHelp(c, locales.AcctCredentials(), locales.AcctCredentialsHelp(), "", func() {
		selectBox(c, locales.AcctCredentials(), &f.creds, credChoices(), 0)
	})
	switch f.creds {
	case core.CredStatic:
		formField(c, locales.AcctAccessKeyID(), errs["access"], func() {
			inputBox(c, &f.access, locales.AcctAccessKeyID(), "AKIA…", inputOpts{invalid: errs["access"] != "", mono: true})
		})
		ph := ""
		if f.editing != "" && f.hasSec {
			ph = locales.AcctKeepSecret()
		}
		formField(c, locales.AcctSecretKey(), errs["secret"], func() {
			inputBox(c, &f.secret, locales.AcctSecretKey(), ph, inputOpts{invalid: errs["secret"] != "", password: true, focus: f.missing})
		})
		formFieldHelp(c, locales.AcctSessionToken(), locales.AcctSessionTokenHelp(), errs["session"], func() {
			inputBox(c, &f.session, locales.AcctSessionTokenLabel(), "", inputOpts{invalid: errs["session"] != "", mono: true})
		})
	case core.CredProfile:
		formFieldHelp(c, locales.AcctProfile(), locales.AcctProfileHelp(), errs["profile"], func() {
			inputBox(c, &f.profile, locales.AcctProfile(), "default", inputOpts{invalid: errs["profile"] != ""})
		})
	}
	// Advanced disclosure.
	toggle := ui.ButtonBase(c.Key("advanced")).Label(locales.AcctAdvanced()).Height(24).Gap(4).AlignItems(ui.Center).AlignSelf(ui.Start).Radius(6).Padding(0, 6, 0, 2).Transition(hoverFade())
	if toggle.Hovered() {
		toggle.Background(k.SurfaceHover)
	}
	toggle.Children(func() {
		icon := "chevron-right"
		if f.advanced {
			icon = "chevron-down"
		}
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(k.TextMuted)
		ui.Text(c, locales.AcctAdvanced()).FontSize(sizeSecondary).FontWeight(500).TextColor(k.TextMuted)
	})
	if toggle.Clicked() {
		f.advanced = !f.advanced
	}
	if !f.advanced {
		return
	}
	formFieldHelp(c, locales.AcctRoleARN(), locales.AcctRoleARNHelp(), errs["role"], func() {
		inputBox(c, &f.roleARN, locales.AcctRoleARN(), "arn:aws:iam::123456789012:role/…", inputOpts{invalid: errs["role"] != "", mono: true})
	})
	formFieldHelp(c, locales.AcctExternalID(), locales.AcctExternalIDHelp(), "", func() {
		inputBox(c, &f.externalID, locales.AcctExternalID(), "", inputOpts{mono: true})
	})
	formFieldHelp(c, locales.AcctSessionName(), locales.AcctSessionNameHelp(), "", func() {
		inputBox(c, &f.roleName, locales.AcctSessionName(), "Pail", inputOpts{})
	})
	formFieldHelp(c, locales.AcctChecksumMode(), locales.AcctChecksumModeHelp(), "", func() {
		selectBox(c, locales.AcctChecksumMode(), &f.checksum, checksumChoices(), 0)
	})
	switchRowHelp(c, locales.AcctSkipTLS(), locales.AcctSkipTLSHelp(), &f.skipTLS)
}

func (a *app) accountFooter(c *ui.Context) {
	f := &a.af
	k := tk(c)
	// Result line: the test outcome, or a save error.
	switch {
	case f.saveErr != "":
		resultLine(c, "circle-alert", k.Danger, f.saveErr)
	case f.test == testRunning:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.AcctTesting())
		})
	case f.test == testOK:
		resultLine(c, "check", k.Success, locales.AcctConnected())
	case f.test == testFailed:
		resultLine(c, "circle-alert", k.Danger, f.testMsg)
	}
	if f.noKeychain && f.saveErr != "" {
		ghostButton(c, icons.Get("lock"), locales.AcctSetupVault(), locales.AcctSetupVaultTip(), false).
			AlignSelf(ui.Start).Margin(0, 0, 0, -8).OnClick(func() {
			f.open = false
			a.openTool("settings")
			a.askBackend(1)
		})
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if secondaryButton(c, locales.AcctTestConnection(), f.busy()).Clicked() {
			a.testAccount()
		}
		ui.Spacer(c)
		if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
			f.open = false
		}
		if primaryButton(c, locales.CommonSave(), f.busy()).Clicked() {
			a.saveAccount()
		}
	})
}

// resultLine is an icon with a wrapping 12px message.
func resultLine(c *ui.Context, icon string, col ui.Color, msg string) {
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(col).Shrink(0).MarginY(1)
		ui.Text(c, msg).FontSize(sizeSecondary).TextColor(tk(c).Text).Grow(1).MinWidth(0)
	})
}
