package main

import (
	"path/filepath"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Whole-bucket actions of the sidebar's bucket menu: download, copy or move
// the contents to another bucket, change class and encryption (rewrite_storage.go)
// and invalidate CloudFront.

// ---- download ------------------------------------------------------------------------------------

// downloadBucket asks for a folder and downloads every object of the bucket
// into a sub-folder of it named after the bucket.
func (a *app) downloadBucket(name string) {
	if !a.hasAccount() || name == "" {
		return
	}
	loc := a.bucketRoot(name)
	go func() {
		ds := a.pick(true)
		if len(ds) == 0 {
			return
		}
		dest := filepath.Join(ds[0], name)
		a.post(func() {
			a.bg(func() func() {
				gid, err := a.svc.Download(core.DownloadRequest{Source: loc, WholeBucket: true, DestDir: dest, Conflict: a.svc.Settings().Conflict})
				return func() {
					if err != nil {
						a.toast(locales.BucketCopyDownloadFailed(humanError(err)))
						return
					}
					a.transfersOpen, a.transTab = true, 0
					a.addWatch(gid, &watch{loc: loc, kind: "download", dest: dest})
				}
			})
		})
	}()
}

// ---- copy and move contents ------------------------------------------------------------------------

type bucketCopyForm struct {
	open       bool
	move       bool
	confirming bool // a move waits for its second, Danger confirmation
	src        Scope
	dest       string // target bucket
	prefix     string // target prefix, as typed
	conflict   string
	est        estState
	busy       bool
	err        string
}

// normPrefix turns what was typed into a folder prefix: no leading slash and
// one trailing slash, or "" for the bucket root.
func normPrefix(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "/")
	if s == "" {
		return ""
	}
	return s + "/"
}

// destChoices are the buckets of the account a bucket's contents can go to:
// every other bucket (copying into the same bucket's root can only collide).
func (a *app) destChoices(src string) []choice {
	var out []choice
	for _, b := range a.buckets {
		if b.Name != src {
			out = append(out, choice{b.Name, b.Name})
		}
	}
	return out
}

func (f *bucketCopyForm) destScope(acct string) Scope {
	return Scope{AccountID: acct, Bucket: f.dest, Prefix: normPrefix(f.prefix)}
}

// prefixError words what is wrong with the target prefix; "" is fine.
func (f *bucketCopyForm) prefixError() string {
	p := normPrefix(f.prefix)
	switch {
	case len(p) > 900:
		return locales.BucketCopyPrefixTooLong()
	case strings.Contains(p, "//"):
		return locales.BucketCopyPrefixEmptyName()
	case strings.ContainsAny(p, "\x00\r\n"):
		return locales.BucketCopyPrefixControl()
	}
	return ""
}

func (a *app) openBucketCopy(name string, move bool) {
	if !a.hasAccount() || name == "" {
		return
	}
	f := &a.objOps.bc
	f.est.stop()
	*f = bucketCopyForm{open: true, move: move, src: a.bucketRoot(name), conflict: string(a.svc.Settings().Conflict)}
	if ch := a.destChoices(name); len(ch) > 0 {
		f.dest = ch[0].Value
	}
	a.countScope(&f.est, core.Selection{Location: f.src, All: true}, nil)
}

func (a *app) closeBucketCopy() {
	f := &a.objOps.bc
	f.open = false
	f.est.stop()
}

func (f *bucketCopyForm) canSubmit() bool {
	return !f.busy && f.dest != "" && f.prefixError() == ""
}

// submitBucketCopy queues the copy (or move) of the whole bucket.
func (a *app) submitBucketCopy() {
	f := &a.objOps.bc
	if !f.open || !f.canSubmit() {
		return
	}
	src, dest, move := f.src, f.destScope(a.accountID), f.move
	opt := core.CopyOptions{Conflict: core.ConflictPolicy(f.conflict), Move: move}
	f.busy, f.err = true, ""
	a.bg(func() func() {
		id, err := a.svc.Copy(core.Selection{Location: src, All: true}, dest, opt)
		return func() {
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				f.confirming = false
				return
			}
			a.closeBucketCopy()
			a.transfersOpen, a.transTab = true, 0
			verb, past := locales.BucketCopyVerbCopy(), locales.BucketCopyPastCopied()
			if move {
				verb, past = locales.BucketCopyVerbMove(), locales.BucketCopyPastMoved()
			}
			a.follow(id, a.copyDone(verb, past, src, dest))
		}
	})
}

func (a *app) bucketCopyDialog(c *ui.Context) {
	f := &a.objOps.bc
	was := f.open
	title := locales.BucketCopyCopyTitle()
	if f.move {
		title = locales.BucketCopyMoveTitle()
	}
	if f.confirming {
		title = locales.BucketCopyMoveConfirmTitle(f.src.Bucket)
	}
	dialogW(c, &f.open, title, 440, func() {
		dest := f.destScope(a.accountID)
		where := dest.Bucket + "/" + dest.Prefix
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.BucketCopyFrom(), f.src.Bucket, false)
			if f.confirming {
				fieldRow(c, locales.BucketCopyTo(), where, true)
			}
		})
		if f.confirming {
			ui.Column(c).Gap(2).Children(func() {
				countRow(c, &f.est)
				mutedText(c, locales.BucketCopyMoveWarning(f.src.Bucket))
			})
		} else {
			choices := a.destChoices(f.src.Bucket)
			formField(c, locales.BucketCopyTargetBucket(), "", func() {
				if len(choices) == 0 {
					mutedText(c, locales.BucketCopyNoOther())
					return
				}
				if selectBox(c, locales.BucketCopyTargetBucket(), &f.dest, choices, 0) {
					f.err = ""
				}
			})
			formFieldHelp(c, locales.BucketCopyTargetPrefix(), locales.BucketCopyTargetPrefixHelp(), f.prefixError(), func() {
				in := inputBox(c, &f.prefix, locales.BucketCopyTargetPrefix(), locales.BucketCopyPrefixPlaceholder(), inputOpts{mono: true, invalid: f.prefixError() != ""})
				if in.Changed() {
					f.err = ""
				}
				if in.Submitted() {
					a.bucketCopyGo()
				}
			})
			formField(c, locales.BucketCopyIfExists(), "", func() {
				selectBox(c, locales.BucketCopyConflictPolicy(), &f.conflict, conflictChoices(), 0)
			})
			countRow(c, &f.est)
		}
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		buttonRow(c, func() {
			switch {
			case f.confirming:
				if secondaryButton(c, locales.CommonBack(), f.busy).Clicked() {
					f.confirming = false
				}
				if dangerButton(c, locales.BucketCopyMoveEverything(), f.busy).Clicked() {
					a.submitBucketCopy()
				}
			default:
				if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
					f.open = false
				}
				label := locales.BucketCopyCopyContents()
				if f.move {
					label = locales.BucketCopyContinue()
				}
				if primaryButton(c, label, !f.canSubmit()).Clicked() {
					a.bucketCopyGo()
				}
			}
		})
	})
	if was && !f.open {
		a.closeBucketCopy()
	}
}

// bucketCopyGo is the primary button: a copy starts, a move goes on to its
// confirmation.
func (a *app) bucketCopyGo() {
	f := &a.objOps.bc
	if !f.canSubmit() {
		return
	}
	if f.move {
		f.confirming = true
		return
	}
	a.submitBucketCopy()
}

// ---- CloudFront ------------------------------------------------------------------------------------

// invalidateBucket opens the CloudFront invalidation dialog for a whole bucket
// ("/*"), AWS accounts only.
func (a *app) invalidateBucket(name string) {
	if !a.cfAvailable() || name == "" {
		return
	}
	if a.scope.Bucket != name {
		a.openBucket(name)
	}
	a.openInvalidation("/*")
}
