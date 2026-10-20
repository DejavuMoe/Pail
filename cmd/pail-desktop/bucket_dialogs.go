package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// bucketDialogs draws the bucket dialogs.
func (a *app) bucketDialogs(c *ui.Context) {
	a.newBucketDialog(c)
	a.deleteBucketDialog(c)
	a.emptyBucketDialog(c)
	a.bucketSettingsDialog(c)
}

// ---- create ------------------------------------------------------------------------------

type newBucketForm struct {
	open   bool
	name   string
	region string
	lock   bool
	err    string
	busy   bool
}

var commonRegions = []string{
	"us-east-1", "us-east-2", "us-west-1", "us-west-2", "ca-central-1",
	"eu-west-1", "eu-west-2", "eu-west-3", "eu-central-1", "eu-north-1",
	"ap-south-1", "ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "ap-northeast-2", "sa-east-1",
}

var bucketIPv4 = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

// bucketNameError checks the general-purpose bucket naming rules the service
// enforces, so the dialog can say what is wrong while the name is typed. "" is
// a valid name.
func bucketNameError(name string) string {
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	switch {
	case name == "":
		return locales.BucketDialogsNameEnter()
	case len(name) < 3 || len(name) > 63:
		return locales.BucketDialogsNameLength()
	case !alnum(name[0]) || !alnum(name[len(name)-1]):
		return locales.BucketDialogsNameEdges()
	case strings.Contains(name, ".."):
		return locales.BucketDialogsNamePeriods()
	case bucketIPv4.MatchString(name):
		return locales.BucketDialogsNameIpLike()
	case strings.HasPrefix(name, "xn--"):
		return locales.BucketDialogsNameXn()
	case strings.HasSuffix(name, "-s3alias"), strings.HasSuffix(name, "--ol-s3"):
		return locales.BucketDialogsNameSuffix()
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !alnum(c) && c != '.' && c != '-' {
			return locales.BucketDialogsNameChars()
		}
	}
	return ""
}

func (a *app) openNewBucket() {
	if !a.hasAccount() {
		return
	}
	region := a.account().Region
	if region == "" {
		region = "us-east-1"
	}
	a.mg.nb = newBucketForm{open: true, region: region}
}

func (a *app) createBucket() {
	f := &a.mg.nb
	name := strings.TrimSpace(f.name)
	if f.busy || bucketNameError(name) != "" {
		return
	}
	region, lock, acct := strings.TrimSpace(f.region), f.lock, a.accountID
	f.busy, f.err = true, ""
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		err := a.svc.CreateBucket(ctx, acct, name, region, lock)
		var bs []core.Bucket
		var berr error
		if err == nil {
			bs, berr = a.svc.Buckets(ctx, acct)
		}
		return func() {
			f := &a.mg.nb
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				return
			}
			f.open = false
			a.toast(locales.BucketDialogsCreated(name))
			if acct != a.accountID {
				return
			}
			if berr == nil {
				a.buckets = bs
			} else {
				a.loadBuckets(false)
			}
			a.openBucket(name)
		}
	})
}

func (a *app) newBucketDialog(c *ui.Context) {
	f := &a.mg.nb
	dialogW(c, &f.open, locales.BucketDialogsNewTitle(), 480, func() {
		nameErr := ""
		if strings.TrimSpace(f.name) != "" {
			nameErr = bucketNameError(strings.TrimSpace(f.name))
		}
		formField(c, locales.CommonName(), nameErr, func() {
			in := inputBox(c, &f.name, locales.BucketDialogsNameInput(), "my-bucket", inputOpts{focus: true, mono: true, invalid: nameErr != ""})
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				a.createBucket()
			}
			if nameErr == "" {
				// A name rule line, not a "?": a focusable icon before this auto-focused input would take the focus back.
				mutedText(c, locales.BucketDialogsNameHint())
			}
		})
		formFieldHelp(c, locales.CommonRegion(), locales.BucketDialogsRegionHelp(), "", func() {
			if a.account().Provider == core.ProviderAWS {
				opts := make([]choice, 0, len(commonRegions)+1)
				found := false
				for _, r := range commonRegions {
					opts = append(opts, choice{r, r})
					found = found || r == f.region
				}
				if !found && f.region != "" {
					opts = append([]choice{{f.region, f.region}}, opts...)
				}
				selectBox(c, locales.CommonRegion(), &f.region, opts, 0)
				return
			}
			inputBox(c, &f.region, locales.CommonRegion(), "us-east-1", inputOpts{})
		})
		switchRowHelp(c, locales.BucketDialogsObjectLock(), locales.BucketDialogsObjectLockHelp(), &f.lock)
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			if primaryButton(c, locales.CommonCreate(), bucketNameError(strings.TrimSpace(f.name)) != "" || f.busy).Clicked() {
				a.createBucket()
			}
		})
	})
}

// ---- delete ------------------------------------------------------------------------------

type deleteBucketForm struct {
	open     bool
	bucket   string
	typed    string
	err      string
	notEmpty bool
	busy     bool
}

func (a *app) openDeleteBucket(name string) {
	a.mg.db = deleteBucketForm{open: true, bucket: name}
}

func isBucketNotEmpty(err error) bool {
	var oe *core.OpError
	return errors.As(err, &oe) && oe.Code == "BucketNotEmpty"
}

func (a *app) deleteBucket() {
	f := &a.mg.db
	if f.busy || f.typed != f.bucket {
		return
	}
	name, acct := f.bucket, a.accountID
	f.busy, f.err, f.notEmpty = true, "", false
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		err := a.svc.DeleteBucket(ctx, acct, name)
		return func() {
			f := &a.mg.db
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				f.notEmpty = isBucketNotEmpty(err)
				return
			}
			f.open = false
			a.invalidateVersioning(acct, name)
			a.toast(locales.BucketDialogsDeleted(name))
			if acct != a.accountID {
				return
			}
			if a.scope.Bucket == name {
				a.scope = Scope{AccountID: acct}
				a.navSel, a.tool = "", ""
				a.clearListing()
			}
			a.loadBuckets(true)
		}
	})
}

func (a *app) deleteBucketDialog(c *ui.Context) {
	f := &a.mg.db
	dialogW(c, &f.open, locales.BucketDialogsDeleteTitle(), 440, func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), f.bucket, true)
			for _, b := range a.buckets {
				if b.Name == f.bucket && b.Region != "" {
					fieldRow(c, locales.CommonRegion(), b.Region, false)
				}
			}
		})
		formField(c, locales.BucketDialogsTypeName(), "", func() {
			in := inputBox(c, &f.typed, locales.BucketDialogsNameInput(), f.bucket, inputOpts{focus: true, mono: true})
			if in.Changed() {
				f.err, f.notEmpty = "", false
			}
			if in.Submitted() {
				a.deleteBucket()
			}
		})
		if f.err != "" {
			ui.Column(c).Gap(4).Children(func() {
				errorLine(c, f.err)
				if f.notEmpty {
					ghostButton(c, nil, locales.BucketDialogsEmptyButton(), locales.BucketDialogsEmptyFirst(), false).AlignSelf(ui.Start).OnClick(func() {
						name := f.bucket
						f.open = false
						a.openEmptyBucket(name)
					})
				}
			})
		}
		mutedText(c, locales.BucketDialogsDeleteWarning(f.bucket))
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			if dangerButton(c, locales.BucketDialogsDeleteButton(), f.typed != f.bucket || f.busy).Clicked() {
				a.deleteBucket()
			}
		})
	})
}

// ---- empty -------------------------------------------------------------------------------

type emptyBucketForm struct {
	open   bool
	bucket string
	est    core.Estimate
	state  int // delCounting, delReady, delFailed
	gen    int
	all    bool
	err    string
	busy   bool
	cancel context.CancelFunc
}

func (a *app) openEmptyBucket(name string) {
	f := &a.mg.eb
	if f.cancel != nil {
		f.cancel()
	}
	*f = emptyBucketForm{open: true, bucket: name, state: delCounting, gen: f.gen + 1}
	a.ensureVersioning(a.accountID, name)
	gen, acct := f.gen, a.accountID
	ctx, cancel := a.timeout(2 * time.Minute)
	f.cancel = cancel
	sel := core.Selection{Location: Scope{AccountID: acct, Bucket: name}, All: true}
	a.bg(func() func() {
		defer cancel()
		est, err := a.svc.Estimate(ctx, sel)
		return func() {
			f := &a.mg.eb
			if f.gen != gen || !f.open {
				return
			}
			if err != nil {
				f.state, f.err = delFailed, humanError(err)
				return
			}
			f.est, f.state = est, delReady
		}
	})
}

func (f *emptyBucketForm) summary() string {
	switch f.state {
	case delCounting:
		return locales.BucketDialogsCounting()
	case delFailed:
		return locales.BucketDialogsCountFailed()
	}
	n := locales.CommonObjects(f.est.Objects)
	if f.est.Truncated {
		n = locales.BucketDialogsAtLeast(n)
	}
	return n + " · " + humanBytes(f.est.Bytes)
}

func (a *app) emptyBucket() {
	f := &a.mg.eb
	if f.busy || f.state == delCounting {
		return
	}
	name, acct, all := f.bucket, a.accountID, f.all && a.versioned(a.accountID, f.bucket)
	f.busy = true
	a.bg(func() func() {
		id, err := a.svc.EmptyBucket(acct, name, all)
		return func() {
			f := &a.mg.eb
			f.busy = false
			if err != nil {
				f.err = humanError(err)
				return
			}
			f.open = false
			a.transfersOpen, a.transTab = true, 0
			a.follow(id, func(t Task) {
				sum := a.bulkSummary(t)
				switch {
				case t.Status == core.StatusCanceled:
					a.toast(locales.BucketDialogsEmptyCanceled(name))
				case sum.failed > 0 || t.Status == core.StatusFailed:
					msg := sum.firstErr
					if msg == "" {
						msg = t.Err
					}
					a.toast(locales.BucketDialogsEmptyFailed(name, msg))
				default:
					a.toast(locales.BucketDialogsEmptied(name))
				}
				if acct == a.accountID && a.scope.Bucket == name {
					a.reload()
				}
			})
		}
	})
}

func (a *app) emptyBucketDialog(c *ui.Context) {
	f := &a.mg.eb
	k := tk(c)
	was := f.open
	dialogW(c, &f.open, locales.BucketDialogsEmptyTitle(), 440, func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), f.bucket, true)
		})
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if f.state == delCounting {
					ui.Spinner(c).Size(12, 12)
				}
				ui.Text(c, f.summary()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
			})
			mutedText(c, locales.BucketDialogsEmptyWarning())
		})
		if a.versioned(a.accountID, f.bucket) {
			checkboxRowHelp(c, &f.all, locales.BucketDialogsAllVersions(), locales.BucketDialogsAllVersionsHelp())
		}
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			if dangerButton(c, locales.BucketDialogsEmptyAction(), f.state == delCounting || f.busy).Clicked() {
				a.emptyBucket()
			}
		})
	})
	if was && !f.open && f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}
