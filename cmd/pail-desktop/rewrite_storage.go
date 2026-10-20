package main

import (
	"regexp"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Change storage class and Change encryption: both rewrite each object in
// place (the service copies it onto itself), so they share one dialog frame:
// scope grid, key list, one control, the count and a primary button.

// rewriteScope is what a rewrite applies to: chosen keys of a folder, or the
// whole bucket.
type rewriteScope struct {
	loc  Scope
	keys []string
	all  bool
}

func (s rewriteScope) selection() core.Selection {
	return core.Selection{Location: s.loc, Keys: s.keys, All: s.all}
}

// describe is the scope row of the dialog.
func (s rewriteScope) describe() string {
	switch {
	case s.all && s.loc.Prefix == "":
		return locales.RewriteWholeBucket()
	case s.all:
		return s.loc.Prefix
	}
	return describeKeys(s.keys)
}

type rewriteForm struct {
	open  bool
	enc   bool // false: storage class, true: encryption
	scope rewriteScope
	est   estState

	class  string // a name of rewriteClassChoices, or customClass
	custom string // the free-text class
	mode   int    // index into encModes
	kms    string
	keyRef string

	busy bool
	err  string
}

const customClass = "·custom"

// rewriteClassChoices are the classes of the drop-down plus the free-text one.
func rewriteClassChoices() []choice {
	return append(classChoices(s3Classes...), choice{customClass, locales.RewriteOther()})
}

// encModes are the Encryption.Mode values in the order of the segments.
var encModes = []string{"none", "AES256", "aws:kms", "SSE-C"}

// encLabels are the segment labels in the order of encModes.
func encLabels() []string {
	return []string{locales.CommonNone(), "SSE-S3", "SSE-KMS", "SSE-C"}
}

const encSSEC = 3

var classNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)

// classValue is the storage class the dialog would apply.
func (f *rewriteForm) classValue() string {
	if f.class == customClass {
		return strings.ToUpper(strings.TrimSpace(f.custom))
	}
	return f.class
}

// classError words what is wrong with the class; "" when it is fine or still
// being typed.
func (f *rewriteForm) classError() string {
	if f.class != customClass || strings.TrimSpace(f.custom) == "" {
		return ""
	}
	if !classNameRe.MatchString(f.classValue()) {
		return locales.RewriteClassInvalid()
	}
	return ""
}

// ssecReason says why SSE-C cannot be chosen; "" when it can.
func (a *app) ssecReason() string {
	if !endpointHTTPS(a.account()) {
		return locales.RewriteSsecHttps()
	}
	return ""
}

// rewriteValid reports whether the dialog's choice can be submitted.
func (a *app) rewriteValid() bool {
	f := &a.objOps.rw
	if !f.enc {
		return classNameRe.MatchString(f.classValue())
	}
	if f.mode == encSSEC {
		return a.ssecReason() == "" && f.keyRef != ""
	}
	return true
}

// openRewrite opens the dialog for scope; enc picks encryption over class.
func (a *app) openRewrite(enc bool, scope rewriteScope) {
	f := &a.objOps.rw
	f.est.stop()
	*f = rewriteForm{open: true, enc: enc, scope: scope, class: "STANDARD", mode: 1}
	if refs := a.svc.SecretRefs(); len(refs) > 0 {
		f.keyRef = refs[0]
	}
	var known *core.Estimate
	if !scope.all {
		known = a.selectionEstimate(scope.keys)
	}
	a.countScope(&f.est, scope.selection(), known)
}

// openRewriteSelection opens it for the selected rows.
func (a *app) openRewriteSelection(enc bool) {
	keys := a.selectedKeys()
	if len(keys) == 0 || !a.browsing() {
		return
	}
	a.openRewrite(enc, rewriteScope{loc: a.scope, keys: keys})
}

func (a *app) closeRewrite() {
	f := &a.objOps.rw
	f.open = false
	f.est.stop()
}

// submitRewrite queues the task and follows it to its end.
func (a *app) submitRewrite() {
	f := &a.objOps.rw
	if f.busy || !f.open || !a.rewriteValid() {
		return
	}
	sel, loc, enc := f.scope.selection(), f.scope.loc, f.enc
	class := f.classValue()
	e := core.Encryption{Mode: encModes[f.mode]}
	switch e.Mode {
	case "aws:kms":
		e.KMSKeyID = strings.TrimSpace(f.kms)
	case "SSE-C":
		e.KeyRef = f.keyRef
	}
	f.busy, f.err = true, ""
	a.bg(func() func() {
		var id string
		var err error
		if enc {
			id, err = a.svc.SetEncryption(sel, e)
		} else {
			id, err = a.svc.SetStorageClass(sel, class)
		}
		return func() {
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				return
			}
			a.closeRewrite()
			a.transfersOpen, a.transTab = true, 0
			value := class
			if enc {
				value = encLabels()[indexOf(encModes, e.Mode)]
			}
			a.follow(id, func(t Task) {
				sum := a.bulkSummary(t)
				a.toast(rewriteResult(enc, t, sum, value))
				if loc.AccountID == a.scope.AccountID && loc.Bucket == a.scope.Bucket {
					a.headKey = "" // read the new values
				}
				a.reloadSame(loc)
			})
		}
	})
}

// rewriteResult is the toast that ends a rewrite task; value is the class or
// the encryption label that was applied.
func rewriteResult(enc bool, t Task, sum bulkSummary, value string) string {
	switch {
	case t.Status == core.StatusCanceled:
		if enc {
			return locales.RewriteEncCanceled()
		}
		return locales.RewriteClassCanceled()
	case sum.failed > 0 && sum.ok == 0:
		if enc {
			return locales.RewriteEncFailed(sum.firstErr)
		}
		return locales.RewriteClassFailed(sum.firstErr)
	case sum.failed > 0:
		if enc {
			return locales.RewriteEncPartial(sum.ok, sum.failed)
		}
		return locales.RewriteClassPartial(sum.ok, sum.failed)
	case sum.ok == 0 && sum.skipped > 0:
		return locales.RewriteNothing(sum.skipped)
	}
	if enc {
		return locales.RewriteEncDone(value, sum.ok)
	}
	return locales.RewriteClassDone(value, sum.ok)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

// rewriteDialog draws whichever rewrite dialog is open.
func (a *app) rewriteDialog(c *ui.Context) {
	f := &a.objOps.rw
	was := f.open
	title, action := locales.RewriteClassTitle(), locales.CommonApply()
	if f.enc {
		title = locales.RewriteEncTitle()
	}
	dialogW(c, &f.open, title, 440, func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), f.scope.loc.Bucket, false)
			fieldRow(c, locales.RewriteScope(), f.scope.describe(), f.scope.loc.Prefix != "" && f.scope.all)
		})
		if len(f.scope.keys) > 1 { // one key is already the Scope row
			keyListBlock(c, f.scope.keys)
		}
		if f.enc {
			a.encryptionControls(c)
		} else {
			a.classControls(c)
		}
		ui.Column(c).Gap(2).Children(func() {
			countRow(c, &f.est)
			if f.est.state == delFailed {
				mutedText(c, f.est.err)
			}
			mutedText(c, locales.RewriteNote())
		})
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			if primaryButton(c, action, f.busy || !a.rewriteValid()).Clicked() {
				a.submitRewrite()
			}
		})
	})
	if was && !f.open {
		a.closeRewrite()
	}
}

func (a *app) classControls(c *ui.Context) {
	f := &a.objOps.rw
	formFieldHelp(c, locales.RewriteClassLabel(), locales.RewriteClassHelp(), f.classError(), func() {
		if selectBox(c, locales.RewriteClassLabel(), &f.class, rewriteClassChoices(), 0) {
			f.err = ""
		}
		if f.class == customClass {
			in := inputBox(c, &f.custom, locales.RewriteCustomClass(), locales.RewriteCustomPlaceholder(), inputOpts{focus: true, mono: true, invalid: f.classError() != ""})
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				a.submitRewrite()
			}
		}
	})
}

func (a *app) encryptionControls(c *ui.Context) {
	f := &a.objOps.rw
	reason := a.ssecReason()
	formFieldHelp(c, locales.RewriteEncLabel(), locales.RewriteEncHelp(), "", func() {
		off := []bool{false, false, false, reason != ""}
		if segmentedOff(c, &f.mode, locales.RewriteEncLabel(), off, encLabels()...) {
			f.err = ""
		}
		if reason != "" {
			mutedText(c, reason)
		}
	})
	switch f.mode {
	case 2:
		formFieldHelp(c, locales.RewriteKmsKey(), locales.RewriteKmsKeyHelp(), "", func() {
			in := inputBox(c, &f.kms, locales.RewriteKmsKeyInput(), locales.RewriteKmsPlaceholder(), inputOpts{focus: true, mono: true})
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				a.submitRewrite()
			}
		})
	case encSSEC:
		refs := a.svc.SecretRefs()
		formFieldHelp(c, locales.RewriteSsecKey(), locales.RewriteSsecKeyHelp(), "", func() {
			if len(refs) == 0 {
				mutedText(c, locales.RewriteNoSecrets())
				return
			}
			opts := make([]choice, len(refs))
			for i, r := range refs {
				opts[i] = choice{r, r}
			}
			if selectBox(c, locales.RewriteSecretRef(), &f.keyRef, opts, 0) {
				f.err = ""
			}
		})
	}
}
