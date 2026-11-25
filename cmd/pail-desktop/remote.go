package main

import (
	"net/url"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Dialogs that delete data on the storage service open with remoteNotice, so
// that nobody takes them for a setting of Pail: it says that the change
// happens on the service and names the account and the service it goes to.
// Removing an account, by contrast, only touches this computer and says so.

// serviceLabel names the storage service of an account: the host of an
// S3-compatible endpoint, else AWS, followed by the region when there is one.
func serviceLabel(ac core.Account, region string) string {
	name := "AWS"
	if ac.Provider == core.ProviderS3 {
		name = ac.Endpoint
		if u, err := url.Parse(ac.Endpoint); err == nil && u.Host != "" {
			name = u.Host
		}
	}
	if region = strings.TrimSpace(region); region != "" {
		return name + " · " + region
	}
	return name
}

// bucketRegion is the region of one of the account's buckets as listed, else
// the account's own.
func (a *app) bucketRegion(bucket string) string {
	for _, b := range a.buckets {
		if b.Name == bucket && b.Region != "" {
			return b.Region
		}
	}
	return a.account().Region
}

// remoteNotice is a Danger-tinted box: a cloud icon, the heading "On the
// storage service", msg, and the account and service the change goes to.
// Three cues (icon, wash and heading) tell it from a warning about Pail itself.
func (a *app) remoteNotice(c *ui.Context, msg, bucket string) {
	k := tk(c)
	ac := a.account()
	ui.Column(c).Gap(8).Padding(10, 12).Radius(RadiusControl).
		Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			// 2 down puts the cloud's middle on the middle of the heading's
			// capitals (13px, 600), as the icons of the sidebar sit on theirs.
			ui.Icon(c, icons.Get("cloud")).FontSize(14).TextColor(k.Danger).Shrink(0).Margin(2, 0, 0, 0)
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, locales.RemoteHeading()).FontSize(sizeBody).FontWeight(600).TextColor(k.Text)
				ui.Text(c, msg).FontSize(sizeSecondary).TextColor(k.Text)
			})
		})
		ui.Column(c).Gap(6).Padding(0, 0, 0, 22).Children(func() {
			fieldRow(c, locales.CommonAccount(), ac.Name, false)
			fieldRow(c, locales.RemoteService(), serviceLabel(ac, a.bucketRegion(bucket)), false)
		})
	})
}
