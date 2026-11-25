package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Setup links (core.SetupLink) carry the settings of an account as a
// pail://account URL. Opening one, or pasting it into "Add account from
// link…", fills in the add-account form; nothing is saved until the person
// chooses Save, and the form checks the fields as it always does, so a link
// without keys cannot be saved until they are entered.

// linkDialogs are the two setup link dialogs.
type linkDialogs struct {
	share shareLinkForm
	paste pasteLinkForm
}

// shareLinkForm makes the link of the current account. The keys are for the
// recipient, typed here: the account's own are never put in a link.
type shareLinkForm struct {
	open      bool
	accountID string
	name      string
	access    string
	secret    string
}

type pasteLinkForm struct {
	open bool
	text string
	err  string
	// seeded: the clipboard was looked at. A setup link on it fills the field
	// when the dialog opens, since the usual way here is to copy the link
	// from a chat first; other clipboard text is left alone.
	seeded bool
}

// openShareLink opens the share dialog for the current account.
func (a *app) openShareLink() {
	ac := a.account()
	if ac.ID == "" {
		return
	}
	a.links.share = shareLinkForm{open: true, accountID: ac.ID, name: ac.Name}
}

// openPasteLink opens the dialog that takes a pasted setup link.
func (a *app) openPasteLink() {
	a.links.paste = pasteLinkForm{open: true}
}

// shareLink is the link the share dialog shows and copies.
func (a *app) shareLink() core.SetupLink {
	f := &a.links.share
	var ac core.Account
	for _, x := range a.accounts {
		if x.ID == f.accountID {
			ac = x
		}
	}
	l := core.SetupLinkFrom(ac)
	l.Name = strings.TrimSpace(f.name)
	if l.Credentials == core.CredStatic {
		l.AccessKeyID, l.SecretAccessKey = strings.TrimSpace(f.access), f.secret
	}
	return l
}

// openSetupLink handles a link that reached the app (a click on it, or the
// command line). One that is not a setup link only says so.
func (a *app) openSetupLink(raw string) {
	l, err := core.ParseSetupLink(raw)
	if err != nil {
		a.toast(locales.LinkNotSetup())
		return
	}
	a.applySetupLink(l)
}

// applySetupLink opens the add-account form filled in from l. A form that is
// saving is left alone: the link would replace what is being saved.
func (a *app) applySetupLink(l core.SetupLink) {
	if a.af.open && a.af.saving {
		a.toast(locales.LinkBusy())
		return
	}
	a.links.paste.open = false
	a.openAccountForm(false)
	a.af.applyLink(l)
}

// applyLink fills in a new account form from a setup link. What the link
// does not carry keeps the form's default; the field errors show at once, so
// the person sees what is still to enter.
func (f *acctForm) applyLink(l core.SetupLink) {
	f.fromLink, f.linkKeys = true, l.HasKeys()
	f.name = l.Name
	if l.Provider == core.ProviderS3 {
		f.provider, f.endpoint, f.path = 1, l.Endpoint, l.PathStyle
	}
	if l.Credentials != "" {
		f.creds = l.Credentials
	}
	f.profile = l.Profile
	f.access, f.secret = l.AccessKeyID, l.SecretAccessKey
	switch {
	case l.Region != "":
		f.region, f.regionEdited = l.Region, true
	case f.creds == core.CredProfile || f.creds == core.CredDefault:
		f.region = "" // the profile's region applies
	}
	f.buckets = strings.Join(l.Buckets, ", ")
	f.cdnURL, f.cdnBuckets = l.CDNURL, strings.Join(l.CDNBuckets, ", ")
	f.roleARN, f.externalID, f.roleName = l.RoleARN, l.ExternalID, l.RoleSessionName
	if l.ChecksumMode != "" {
		f.checksum = l.ChecksumMode
	}
	f.reqPays = l.RequesterPays
	f.advanced = f.roleARN != "" || f.externalID != "" || f.roleName != "" || f.checksum != "auto" || f.reqPays
	f.syncProfilePick()
	f.tried = true
}

// linkNotice is the line at the top of a form filled in from a link: where
// the keys go, which is what a link from a stranger could change.
func (a *app) linkNotice(c *ui.Context) {
	f := &a.af
	ac, _ := f.build()
	where := serviceLabel(ac, ac.Region)
	if f.linkKeys {
		warnNotice(c, locales.AcctFromLinkKeys(where))
		return
	}
	warnNotice(c, locales.AcctFromLink(where))
}

func (a *app) linkDialogs(c *ui.Context) {
	a.shareLinkDialog(c)
	a.pasteLinkDialog(c)
}

func (a *app) shareLinkDialog(c *ui.Context) {
	f := &a.links.share
	if f.open && a.accountID != f.accountID {
		f.open = false // the account it was about is no longer the current one
	}
	k := tk(c)
	dialogW(c, &f.open, locales.LinkShareTitle(), 480, func() {
		l := a.shareLink()
		mutedText(c, locales.LinkShareIntro())
		// The fields keep the account form's rhythm: 12 between fields, 4
		// between a label and its control.
		ui.Column(c).Gap(12).Children(func() {
			formField(c, locales.CommonName(), "", func() {
				inputBox(c, &f.name, locales.CommonName(), locales.AcctNamePlaceholder(), inputOpts{focus: true})
			})
			ui.Column(c).Gap(8).Children(func() {
				fieldRow(c, locales.RemoteService(), serviceLabel(core.Account{Provider: l.Provider, Endpoint: l.Endpoint}, l.Region), false)
				fieldRow(c, locales.AcctCredentials(), credLabel(l.Credentials), false)
				if l.Profile != "" {
					fieldRow(c, locales.AcctProfile(), l.Profile, false)
				}
				if len(l.Buckets) > 0 {
					fieldRow(c, locales.AcctBuckets(), strings.Join(l.Buckets, ", "), true)
				}
			})
			if l.Credentials == core.CredStatic {
				// Labelled like the account form's own key fields, with its
				// placeholders: an example key ID, nothing for the secret.
				formField(c, locales.LinkShareAccess(), "", func() {
					inputBox(c, &f.access, locales.LinkShareAccess(), "AKIA…", inputOpts{mono: true})
				})
				formField(c, locales.LinkShareSecret(), "", func() {
					inputBox(c, &f.secret, locales.LinkShareSecret(), "", inputOpts{password: true})
				})
				if l.HasKeys() {
					warnNotice(c, locales.LinkShareKeysWarning())
				} else {
					mutedText(c, locales.LinkShareKeysHelp())
				}
			}
			formField(c, locales.LinkShareLink(), "", func() {
				// The secret is not shown, only copied.
				shown := l
				shown.SecretAccessKey = ""
				text := shown.URL()
				if l.SecretAccessKey != "" {
					text += "&sk=••••••"
				}
				// Read-only, so a well (Border, not the inputs' BorderStrong),
				// but with an input's height and insets: its text starts where
				// the text of the fields above does.
				well(c, ui.Row(c).Height(28).Padding(0, 8).AlignItems(ui.Center)).Children(func() {
					mono(ui.Text(c, text).FontSize(sizeSecondary).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0))
				})
			})
		})
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonClose(), false).Clicked() {
				f.open = false
			}
			if primaryButton(c, locales.LinkCopy(), strings.TrimSpace(f.name) == "").Clicked() {
				l := a.shareLink()
				if l.HasKeys() {
					a.copySecret(c, l.URL()) // kept out of clipboard histories
				} else {
					c.WriteClipboard(l.URL())
				}
				a.toast(locales.LinkCopied())
			}
		})
	})
}

func (a *app) pasteLinkDialog(c *ui.Context) {
	f := &a.links.paste
	submit := func() {
		l, err := core.ParseSetupLink(f.text)
		if err != nil {
			f.err = locales.LinkNotSetup()
			return
		}
		a.applySetupLink(l)
	}
	if f.open && !f.seeded {
		f.seeded = true
		if clip := strings.TrimSpace(c.ReadClipboard()); f.text == "" && clip != "" {
			if _, err := core.ParseSetupLink(clip); err == nil {
				f.text = clip
			}
		}
	}
	dialogW(c, &f.open, locales.LinkPasteTitle(), 480, func() {
		mutedText(c, locales.LinkPasteIntro())
		formField(c, locales.LinkPasteLabel(), f.err, func() {
			in := inputBox(c, &f.text, locales.LinkPasteLabel(), "pail://account?…", inputOpts{focus: true, mono: true, invalid: f.err != ""})
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				submit()
			}
		})
	}, func() {
		confirmButtons(c, primaryButton, locales.LinkPasteContinue(), strings.TrimSpace(f.text) == "", func() { f.open = false }, submit)
	})
}

// credLabel is the name of a kind of credentials, as the account form says it.
func credLabel(cred string) string {
	for _, ch := range credChoices() {
		if ch.Value == cred {
			return ch.Label
		}
	}
	return locales.AcctCredAccessKeys()
}
