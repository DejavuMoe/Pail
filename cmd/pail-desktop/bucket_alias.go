package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// A bucket alias is a name the app shows in place of the bucket's own: in the
// sidebar, the path bar and the command palette. It is kept per account in the
// configuration (Service.SetBucketAlias) and changes nothing on the storage
// service. Dialogs that act on a bucket (delete, empty, copy) and the transfer
// list keep the real name, so what is about to happen is never ambiguous.

// bucketLabel is what the app shows for a bucket of the open account: its alias
// when it has one, else its name.
func (a *app) bucketLabel(name string) string {
	if al := a.account().BucketAliases[name]; al != "" {
		return al
	}
	return name
}

// bucketTip is the tooltip of a bucket's label: the real name too when an alias
// stands in for it.
func (a *app) bucketTip(name string) string {
	if l := a.bucketLabel(name); l != name {
		return l + " · " + name
	}
	return name
}

// aliasState is the Set alias dialog.
type aliasState struct {
	open   bool
	bucket string
	text   string
	err    string // from the service
	busy   bool
}

func (a *app) openAliasDialog(bucket string) {
	a.alias = aliasState{open: true, bucket: bucket, text: a.account().BucketAliases[bucket]}
}

// aliasError explains why text cannot be saved as the alias of the dialog's
// bucket; "" means it can (an empty text removes the alias).
func (a *app) aliasError(text string) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > core.MaxAliasRunes {
		return locales.BucketAliasTooLong(fmt.Sprint(core.MaxAliasRunes))
	}
	if text == "" {
		return ""
	}
	for other, al := range a.account().BucketAliases {
		if other != a.alias.bucket && al == text {
			return locales.BucketAliasTaken(other)
		}
	}
	return ""
}

// saveAlias saves the dialog's alias and closes it; a failure is shown in the
// dialog, which stays open.
func (a *app) saveAlias() {
	d := &a.alias
	text := strings.TrimSpace(d.text)
	if d.busy || a.aliasError(text) != "" {
		return
	}
	acct, bucket := a.accountID, d.bucket
	d.busy, d.err = true, ""
	a.bg(func() func() {
		err := a.svc.SetBucketAlias(acct, bucket, text)
		return func() {
			d.busy = false
			if err != nil {
				d.err = locales.BucketAliasSaveFailed(humanError(err))
				return
			}
			d.open = false
			a.refreshAccounts()
			if text == "" || text == bucket {
				a.toast(locales.BucketAliasRemoved(bucket))
			} else {
				a.toast(locales.BucketAliasSaved(bucket))
			}
		}
	})
}

// aliasDialog edits the alias of one bucket.
func (a *app) aliasDialog(c *ui.Context) {
	d := &a.alias
	dialog(c, &d.open, locales.BucketAliasTitle(), func() {
		// Both fields are a small label over their value, so that they line up.
		formField(c, locales.CommonBucket(), "", func() {
			mono(ui.Text(c, d.bucket).FontSize(sizeBody).TextColor(tk(c).Text).SingleLine())
		})
		errText := a.aliasError(d.text)
		if errText == "" {
			errText = d.err
		}
		formField(c, locales.BucketAliasLabel(), errText, func() {
			in := inputBox(c, &d.text, locales.BucketAliasLabel(), locales.BucketAliasPlaceholder(), inputOpts{invalid: errText != "", focus: true})
			if in.Changed() {
				d.err = ""
			}
			if in.Submitted() {
				a.saveAlias()
			}
		})
		mutedText(c, locales.BucketAliasHelp())
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				d.open = false
			}
			if primaryButton(c, locales.CommonSave(), a.aliasError(d.text) != "" || d.busy).Clicked() {
				a.saveAlias()
			}
		})
	})
}
