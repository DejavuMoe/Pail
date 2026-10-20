package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The signed URL dialog: method, how long the URL stays valid, an optional
// version, then the URL (one per object for a multi-selection) to copy.

// maxSigned is the longest a signature can be valid (the S3 limit).
const maxSigned = 7 * 24 * time.Hour

var expiryPresets = []struct {
	label func() string
	d     time.Duration
}{
	{locales.SignedPreset15m, 15 * time.Minute}, {locales.SignedPreset1h, time.Hour}, {locales.SignedPreset1d, 24 * time.Hour},
	{locales.SignedPreset7d, maxSigned},
}

const expiryCustom = 4 // the segment after the presets

var (
	signedMethods = []string{"GET", "PUT"}
	customUnits   = []time.Duration{time.Minute, time.Hour, 24 * time.Hour}
)

type signedURL struct{ Key, URL string }

type signedForm struct {
	open    bool
	loc     Scope
	keys    []string
	method  int // index into signedMethods
	expiry  int // a preset, or expiryCustom
	amount  string
	unit    int // index into customUnits
	version string
	busy    bool
	err     string

	res     []signedURL // the answer; empty while the options show
	expires time.Time
	valid   time.Duration // how long the answer is valid
}

// ttl is the validity the options describe; 0 and a message when they are not
// valid (an empty custom amount is 0 without a message).
func (f *signedForm) ttl() (time.Duration, string) {
	if f.expiry < len(expiryPresets) {
		return expiryPresets[f.expiry].d, ""
	}
	s := strings.TrimSpace(f.amount)
	if s == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, locales.SignedEnterWhole()
	}
	if n > 10080*60 || time.Duration(n)*customUnits[f.unit] > maxSigned {
		return 0, locales.SignedAtMost()
	}
	return time.Duration(n) * customUnits[f.unit], ""
}

// humanTTL words a validity: "15 minutes", "1 hour", "7 days".
func humanTTL(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return locales.SignedTtlDays(int(d / (24 * time.Hour)))
	case d%time.Hour == 0:
		return locales.SignedTtlHours(int(d / time.Hour))
	}
	return locales.SignedTtlMinutes(int(d / time.Minute))
}

// signedObjects are the selected keys that are objects, in table order.
func (a *app) signedObjects() []string {
	var out []string
	for _, k := range a.selectedKeys() {
		if !isFolderKey(k) {
			out = append(out, k)
		}
	}
	return out
}

func hasObjectSelection(a *app) bool { return a.browsing() && len(a.signedObjects()) > 0 }

// openSigned opens the dialog for keys; version prefills the version field of
// a single object. Without one, the version chosen in the Versions tab is used.
func (a *app) openSigned(keys []string, version string) {
	if len(keys) == 0 || !a.browsing() {
		return
	}
	if version == "" && len(keys) == 1 {
		if v := &a.mg.vers; v.key == keys[0] && len(v.sel) == 1 {
			for id := range v.sel {
				version = id
			}
		}
	}
	a.objOps.sg = signedForm{open: true, loc: a.scope, keys: keys, expiry: 1, unit: 1, version: version}
}

// generateSigned signs every key with the chosen options.
func (a *app) generateSigned() {
	f := &a.objOps.sg
	ttl, _ := f.ttl()
	if !f.open || f.busy || ttl <= 0 || len(f.res) > 0 {
		return
	}
	method := signedMethods[f.method]
	ver := ""
	if method == "GET" && len(f.keys) == 1 {
		ver = strings.TrimSpace(f.version)
	}
	loc, keys := f.loc, f.keys
	f.busy, f.err = true, ""
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		var out []signedURL
		var err error
		for _, k := range keys {
			var u string
			if u, err = a.svc.PresignURL(ctx, loc, k, ver, method, ttl); err != nil {
				break
			}
			out = append(out, signedURL{k, u})
		}
		return func() {
			f.busy = false
			if !f.open {
				return
			}
			if err != nil {
				f.err = locales.SignedFailed(humanError(err))
				return
			}
			f.res, f.expires, f.valid = out, time.Now().Add(ttl), ttl
		}
	})
}

func (f *signedForm) urls() string {
	parts := make([]string, len(f.res))
	for i, r := range f.res {
		parts[i] = r.URL
	}
	return strings.Join(parts, "\n")
}

func (a *app) signedDialog(c *ui.Context) {
	f := &a.objOps.sg
	dialogW(c, &f.open, locales.SignedTitle(), 480, func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), f.loc.Bucket, false)
			if len(f.keys) == 1 {
				fieldRow(c, locales.SignedObject(), f.keys[0], true)
			} else {
				fieldRow(c, locales.SignedObjects(), locales.CommonObjects(len(f.keys)), false)
			}
		})
		if len(f.res) == 0 {
			a.signedOptions(c)
		} else {
			a.signedResult(c)
		}
	}, func() {
		if f.err != "" {
			errorLine(c, f.err)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if len(f.res) > 0 {
				if a.signedHasCDN() {
					if ghostButton(c, icons.Get("globe"), locales.SignedCopyCdn(), locales.SignedCopyCdnTip(), false).Clicked() {
						a.copyCDNURLs(c)
					}
				} else if ghostButton(c, icons.Get("globe"), locales.SignedCopyPublic(), locales.SignedCopyPublicTip(), false).Clicked() {
					a.copyPublicURLs(c)
				}
				ui.Spacer(c)
				if secondaryButton(c, locales.CommonClose(), false).Clicked() {
					f.open = false
				}
				label := locales.CommonCopy()
				if len(f.res) > 1 {
					label = locales.SignedCopyAll()
				}
				if primaryButton(c, label, false).Clicked() {
					c.WriteClipboard(f.urls())
					if len(f.res) == 1 {
						c.Toast(locales.SignedCopied())
					} else {
						c.Toast(locales.SignedCopiedMany(len(f.res)))
					}
				}
				return
			}
			ui.Spacer(c)
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				f.open = false
			}
			ttl, _ := f.ttl()
			if primaryButton(c, locales.SignedGenerate(), f.busy || ttl <= 0).Clicked() {
				a.generateSigned()
			}
		})
	})
}

func (a *app) signedOptions(c *ui.Context) {
	f := &a.objOps.sg
	formFieldHelp(c, locales.SignedMethod(), locales.SignedMethodHelp(), "", func() {
		if segmented(c, &f.method, locales.SignedMethod(), signedMethods...) {
			f.err = ""
		}
	})
	_, expErr := f.ttl()
	formFieldHelp(c, locales.SignedValidFor(), locales.SignedExpiryHelp(), expErr, func() {
		labels := make([]string, 0, len(expiryPresets)+1)
		for _, p := range expiryPresets {
			labels = append(labels, p.label())
		}
		labels = append(labels, locales.SignedCustom())
		if segmented(c, &f.expiry, locales.SignedValidFor(), labels...) {
			f.err = ""
		}
		if f.expiry == expiryCustom {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(4, 0, 0, 0).Children(func() {
				in := inputBox(c, &f.amount, locales.SignedCustomExpiry(), locales.SignedAmount(), inputOpts{width: 96, mono: true, invalid: expErr != "", focus: true})
				if in.Changed() {
					f.err = ""
				}
				if in.Submitted() {
					a.generateSigned()
				}
				segmented(c, &f.unit, locales.SignedUnit(), locales.SignedUnitMinutes(), locales.SignedUnitHours(), locales.SignedUnitDays())
			})
		}
	})
	if len(f.keys) == 1 && f.method == 0 {
		formFieldHelp(c, locales.SignedVersionIdOptional(), locales.SignedVersionHelp(), "", func() {
			in := inputBox(c, &f.version, locales.SignedVersionId(), locales.SignedLatestVersion(), inputOpts{mono: true})
			if in.Changed() {
				f.err = ""
			}
			if in.Submitted() {
				a.generateSigned()
			}
		})
	}
}

// signedResult shows the URL read-only (a list for several objects) and until
// when it works.
func (a *app) signedResult(c *ui.Context) {
	f := &a.objOps.sg
	k := tk(c)
	method := signedMethods[f.method]
	if len(f.res) == 1 {
		in := ui.TextAreaBase(c.Key("signed-url"), &f.res[0].URL).ReadOnly(true).Label(locales.SignedTitle()).FontSize(sizeSecondary).
			Height(152).Padding(8, 10).Radius(6).Background(k.Sidebar).TextColor(k.Text)
		mono(in)
	} else {
		rows := len(f.res)
		ui.Scroll(c).Height(float32(min(rows, 5))*44+8).Radius(6).Background(k.Sidebar).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Padding(4, scrollGutter+2, 4, 8).Children(func() {
				for i, r := range f.res {
					ui.Row(c.Key(i)).Height(44).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, r.Key).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
							mono(ui.Text(c, r.URL).FontSize(sizeLabel).TextColor(k.Text).SingleLine())
						})
						if iconButton(c, icons.Get("copy"), locales.SignedCopyUrlOf(baseName(r.Key))).Clicked() {
							c.WriteClipboard(r.URL)
							c.Toast(locales.SignedCopied())
						}
					})
				}
			})
		})
	}
	mutedText(c, locales.SignedSummary(method, formatTime(f.expires), humanTTL(f.valid)))
}

// signedHasCDN is true when every object of the dialog has a CDN link.
func (a *app) signedHasCDN() bool {
	f := &a.objOps.sg
	for _, r := range f.res {
		if a.svc.CDNURL(f.loc, r.Key) == "" {
			return false
		}
	}
	return len(f.res) > 0
}

// copyCDNURLs copies the CDN addresses of the objects.
func (a *app) copyCDNURLs(c *ui.Context) {
	f := &a.objOps.sg
	urls := make([]string, len(f.res))
	for i, r := range f.res {
		urls[i] = a.svc.CDNURL(f.loc, r.Key)
	}
	c.WriteClipboard(strings.Join(urls, "\n"))
	if len(urls) == 1 {
		c.Toast(locales.SignedCdnCopied())
	} else {
		c.Toast(locales.SignedCdnCopiedMany(len(urls)))
	}
}

// copyPublicURLs copies the addresses the objects have when they are public.
func (a *app) copyPublicURLs(c *ui.Context) {
	f := &a.objOps.sg
	urls := make([]string, len(f.res))
	for i, r := range f.res {
		urls[i] = a.svc.PublicURL(f.loc, r.Key)
	}
	c.WriteClipboard(strings.Join(urls, "\n"))
	c.Toast(locales.SignedPublicCopied())
}
