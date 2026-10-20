package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// bucketHasCDN is true while browsing a bucket the account serves through a CDN.
func bucketHasCDN(a *app) bool { return a.browsing() && a.svc.HasCDN(a.scope) }

// cdnSelection returns the CDN links of the selected objects (folders have none).
func (a *app) cdnSelection() []string {
	var out []string
	for _, k := range a.selectedKeys() {
		if u := a.objectCDN(k); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// hasCDNSelection is a live selection holding at least one object with a CDN link.
func hasCDNSelection(a *app) bool {
	return liveSelection(a) && len(a.cdnSelection()) > 0
}

// oneCDNObject is a single live object with a CDN link.
func oneCDNObject(a *app) bool {
	return oneObject(a) && !a.mg.del.on && a.objectCDN(a.selectedKeys()[0]) != ""
}

// copyCDN copies the CDN links of the selected objects, one per line.
func (a *app) copyCDN() {
	urls := a.cdnSelection()
	if len(urls) == 0 {
		return
	}
	a.copyText(strings.Join(urls, "\n"))
	if len(urls) == 1 {
		a.toast(locales.CdnUrlCopied())
		return
	}
	a.toast(locales.CdnUrlsCopied(len(urls)))
}

// openCDN opens the CDN link of the selected object in the browser.
func (a *app) openCDN() {
	keys := a.selectedKeys()
	if len(keys) != 1 {
		return
	}
	if u := a.objectCDN(keys[0]); u != "" {
		a.openExternal(u)
	}
}

// openExternal opens a web address in the default browser without blocking
// the UI; a failure is reported in a toast.
func (a *app) openExternal(u string) {
	go func() {
		if err := a.openURL(u); err != nil {
			a.post(func() { a.toast(locales.CdnOpenFailed(err.Error())) })
		}
	}()
}

// uploadedCDNLink returns the CDN link of the one file that the finished upload
// tasks ts stored, or "" when they stored several (or none), or the bucket has
// no CDN.
func (a *app) uploadedCDNLink(loc Scope, ts []Task) string {
	var key string
	n := 0
	for _, t := range ts {
		if t.Kind != core.KindUpload {
			continue
		}
		for _, it := range a.svc.TaskItems(t.ID) {
			if it.Status == core.ItemSucceeded && !strings.HasSuffix(it.Key, "/") {
				key = it.Key
				n++
			}
		}
	}
	if n != 1 {
		return ""
	}
	return a.svc.CDNURL(loc, key)
}
