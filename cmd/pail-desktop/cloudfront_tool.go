package main

import (
	"context"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/cloud"
	"github.com/egoist/mygo/ui"
)

// CloudFront tool (AWS accounts only): distributions, the invalidations of the
// chosen one, and a dialog that submits a new invalidation.

// cfAPI is the part of the Service this view uses; tests substitute a fake.
type cfAPI interface {
	Distributions(ctx context.Context, accountID string) ([]cloud.Distribution, error)
	DistributionsForBucket(ctx context.Context, accountID, bucket string) ([]cloud.Distribution, error)
	Invalidate(ctx context.Context, accountID, distID string, paths []string) (cloud.Invalidation, error)
	Invalidations(ctx context.Context, accountID, distID string) ([]cloud.Invalidation, error)
}

// cfPollEvery is how often the invalidations of the chosen distribution are
// refreshed while one of them is in progress.
var cfPollEvery = 10 * time.Second

type invalidateDlg struct {
	open      bool
	paths     string
	busy      bool
	err       string
	result    *cloud.Invalidation
	forBucket map[string]bool // distributions that serve the current bucket
	bucket    string
}

type cfState struct {
	api    cfAPI
	acct   string
	dists  []cloud.Distribution
	loaded bool
	load   bool
	err    string
	gen    int
	list   ui.ListState
	cursor int

	invFor  string
	invs    []cloud.Invalidation
	invLoad bool
	invErr  string
	invGen  int
	invList ui.ListState

	dlg     invalidateDlg
	pending *string // paths to offer once a distribution is chosen
	cancel  context.CancelFunc
}

func distColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "ID", ID: "id", Width: 130, MinWidth: 100},
		{Title: locales.CloudfrontDomain(), ID: "domain", MinWidth: 140},
		{Title: locales.CloudfrontAliases(), ID: "aliases", Width: 180, MinWidth: 100},
		{Title: locales.CommonStatus(), ID: "status", Width: 110, MinWidth: 80},
		{Title: locales.CommonEnabled(), ID: "enabled", Width: 90, MinWidth: 70},
	}
}

func invColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "ID", ID: "id", Width: 190, MinWidth: 100},
		{Title: locales.CommonStatus(), ID: "status", Width: 130, MinWidth: 80},
		{Title: locales.CloudfrontCreated(), ID: "created", MinWidth: 140},
	}
}

func (a *app) cfService() cfAPI {
	if a.tl.cf.api != nil {
		return a.tl.cf.api
	}
	return a.svc
}

func (a *app) cfAvailable() bool { return a.account().Provider == core.ProviderAWS }

func (a *app) cfEnter() {
	cf := &a.tl.cf
	api := cf.api
	*cf = cfState{api: api, acct: a.accountID, cursor: -1}
	if !a.cfAvailable() {
		return
	}
	a.cfLoad()
	ctx, cancel := context.WithCancel(a.ctx)
	cf.cancel = cancel
	every := cfPollEvery
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.post(a.cfTick)
			}
		}
	}()
}

func (a *app) cfLeave() {
	cf := &a.tl.cf
	if cf.cancel != nil {
		cf.cancel()
		cf.cancel = nil
	}
	cf.gen++
	cf.invGen++
}

// selectedDist is the chosen distribution, if any.
func (cf *cfState) selectedDist() (cloud.Distribution, bool) {
	if cf.cursor >= 0 && cf.cursor < len(cf.dists) {
		return cf.dists[cf.cursor], true
	}
	return cloud.Distribution{}, false
}

func (a *app) cfLoad() {
	cf := &a.tl.cf
	cf.gen++
	gen, acct, api := cf.gen, cf.acct, a.cfService()
	cf.load, cf.err = true, ""
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		ds, err := api.Distributions(ctx, acct)
		return func() {
			if gen != cf.gen {
				return
			}
			cf.load = false
			if err != nil {
				cf.err = humanError(err)
				return
			}
			prev, _ := cf.selectedDist()
			cf.dists, cf.loaded, cf.cursor = ds, true, -1
			for i, d := range ds {
				if d.ID == prev.ID && prev.ID != "" {
					cf.cursor = i
				}
			}
			if cf.cursor < 0 && len(ds) > 0 {
				cf.cursor = 0
			}
			a.cfSyncSelection()
		}
	})
}

// cfSyncSelection loads the invalidations when the chosen distribution
// changed, and opens a waiting invalidation dialog.
func (a *app) cfSyncSelection() {
	cf := &a.tl.cf
	d, ok := cf.selectedDist()
	id := ""
	if ok {
		id = d.ID
	}
	if id != cf.invFor {
		cf.invFor, cf.invs, cf.invErr = id, nil, ""
		cf.invGen++
		if id != "" {
			a.cfLoadInvs(false)
		}
	}
	if ok && cf.pending != nil {
		paths := *cf.pending
		cf.pending = nil
		a.cfOpenDialog(paths)
	}
}

func (a *app) cfLoadInvs(silent bool) {
	cf := &a.tl.cf
	id := cf.invFor
	if id == "" {
		return
	}
	cf.invGen++
	gen, acct, api := cf.invGen, cf.acct, a.cfService()
	if !silent {
		cf.invLoad, cf.invErr = true, ""
	}
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		invs, err := api.Invalidations(ctx, acct, id)
		return func() {
			if gen != cf.invGen {
				return
			}
			cf.invLoad = false
			if err != nil {
				if !silent {
					cf.invErr = humanError(err)
				}
				return
			}
			cf.invs, cf.invErr = invs, ""
			if r := cf.dlg.result; r != nil && r.DistributionID == id {
				for _, v := range invs {
					if v.ID == r.ID {
						r.Status = v.Status
					}
				}
			}
		}
	})
}

// cfTick runs every cfPollEvery while the view is open: it refreshes the
// invalidations of the chosen distribution while one is still in progress.
func (a *app) cfTick() {
	cf := &a.tl.cf
	if a.tool != "cloudfront" || cf.invFor == "" || cf.invLoad {
		return
	}
	busy := cf.dlg.result != nil && cf.dlg.result.Status == "InProgress"
	for _, v := range cf.invs {
		busy = busy || v.Status == "InProgress"
	}
	if busy {
		a.cfLoadInvs(true)
	}
}

// ---- invalidation dialog ----------------------------------------------------------------------

// invalidationPaths turns browser keys into paths: "/key" for objects and
// "/prefix*" for a folder.
func invalidationPaths(keys []string) string {
	var out []string
	for _, k := range keys {
		if strings.HasSuffix(k, "/") {
			out = append(out, "/"+k+"*")
		} else {
			out = append(out, "/"+k)
		}
	}
	return strings.Join(out, "\n")
}

// openInvalidation opens CloudFront with the invalidation dialog, prefilled
// with paths (from the browser selection; "" for an empty list).
func (a *app) openInvalidation(paths string) {
	a.openTool("cloudfront")
	a.toolSync()
	if !a.cfAvailable() {
		return
	}
	a.tl.cf.pending = &paths
	if _, ok := a.tl.cf.selectedDist(); ok {
		a.cfSyncSelection()
	}
}

func (a *app) cfOpenDialog(paths string) {
	cf := &a.tl.cf
	d, ok := cf.selectedDist()
	if !ok {
		return
	}
	cf.dlg = invalidateDlg{open: true, paths: paths, bucket: a.scope.Bucket}
	if a.scope.Bucket == "" {
		return
	}
	bucket, acct, api, dist := a.scope.Bucket, cf.acct, a.cfService(), d.ID
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		ds, err := api.DistributionsForBucket(ctx, acct, bucket)
		return func() {
			if !cf.dlg.open || cf.dlg.bucket != bucket {
				return
			}
			set := map[string]bool{}
			if err == nil {
				for _, x := range ds {
					set[x.ID] = true
				}
			}
			_ = dist
			cf.dlg.forBucket = set
		}
	})
}

func splitPaths(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (a *app) cfSubmit() {
	cf := &a.tl.cf
	d, ok := cf.selectedDist()
	paths := splitPaths(cf.dlg.paths)
	if !ok || cf.dlg.busy || len(paths) == 0 || cf.dlg.result != nil {
		return
	}
	cf.dlg.busy, cf.dlg.err = true, ""
	acct, api := cf.acct, a.cfService()
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		inv, err := api.Invalidate(ctx, acct, d.ID, paths)
		return func() {
			cf.dlg.busy = false
			if err != nil {
				cf.dlg.err = humanError(err)
				return
			}
			cf.dlg.result = &inv
			if cf.invFor == d.ID {
				a.cfLoadInvs(true)
			}
		}
	})
}

func (a *app) invalidateDialog(c *ui.Context) {
	k := tk(c)
	cf := &a.tl.cf
	dg := &cf.dlg
	if !dg.open {
		return
	}
	d, _ := cf.selectedDist()
	paths := splitPaths(dg.paths)
	dialog(c, &dg.open, locales.CloudfrontNewInvalidation(), func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CloudfrontDistribution(), d.ID, true)
		})
		formFieldHelp(c, locales.CloudfrontPaths(), locales.CloudfrontPathsHelp(), "", func() {
			if in := textAreaBox(c, &dg.paths, locales.CloudfrontPathsLabel(), locales.CloudfrontPathsPh(), 112, inputOpts{mono: true}); in.Changed() {
				dg.err = ""
			}
			if dg.forBucket[d.ID] {
				ui.Row(c).Margin(4, 0, 0, 0).Children(func() {
					if ghostButton(c, icons.Get("globe"), locales.CloudfrontWholeBucket(), locales.CloudfrontWholeBucketTip(), dg.result != nil).Height(24).Clicked() {
						dg.paths = "/*"
					}
				})
			}
		})
	}, func() {
		if dg.err != "" {
			resultLine(c, "circle-alert", k.Danger, dg.err)
		}
		if r := dg.result; r != nil {
			resultLine(c, "circle-check", k.Success, locales.CloudfrontResult(r.ID, r.Status))
		}
		buttonRow(c, func() {
			label := locales.CommonCancel()
			if dg.result != nil {
				label = locales.CommonClose()
			}
			if secondaryButton(c, label, false).Clicked() {
				dg.open = false
			}
			if primaryButton(c, locales.CloudfrontInvalidate(), dg.busy || len(paths) == 0 || dg.result != nil).Clicked() {
				a.cfSubmit()
			}
		})
	})
}

// ---- view ---------------------------------------------------------------------------------------

func (a *app) cfView(c *ui.Context) {
	k := tk(c)
	cf := &a.tl.cf
	if !a.cfAvailable() {
		emptyBlock(c, "cloud", locales.CloudfrontAwsOnly(), "", 0)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			if ghostButton(c, icons.Get("refresh-cw"), locales.CommonRefresh(), locales.CommonRefresh(), cf.load).Clicked() {
				a.cfLoad()
			}
			if cf.load {
				ui.Spinner(c).Size(14, 14)
			}
			ui.Spacer(c)
			_, ok := cf.selectedDist()
			if secondaryButton(c, locales.CloudfrontNew(), !ok).Clicked() {
				a.cfOpenDialog("")
			}
		})
		hairline(c)
		switch {
		case cf.err != "":
			ui.Row(c).Margin(12).Padding(8, 12).Gap(10).AlignItems(ui.Center).Radius(6).Shrink(0).
				Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
				ui.Icon(c, icons.Get("circle-alert")).TextColor(k.Danger)
				ui.Text(c, locales.CloudfrontListFailed(cf.err)).Grow(1).MinWidth(0).TextColor(k.Text)
				secondaryButton(c, locales.CommonRetry(), false).OnClick(a.cfLoad)
			})
		case cf.load && !cf.loaded:
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Spinner(c).Size(22, 22)
				mutedText(c, locales.CloudfrontLoading())
			})
		case len(cf.dists) == 0:
			emptyBlock(c, "cloud", locales.CloudfrontNone(), "", 0)
		default:
			ds := cf.dists
			cf.list.Key = func(i int) any { return ds[i].ID }
			cf.list.Label = func(i int) string { return ds[i].ID }
			cf.list.Selected = &cf.cursor
			tbl := toolTable(c, &cf.list, distColumns(), len(ds), func(row, col int) {
				d := &ds[row]
				switch col {
				case 0:
					mono(ui.Text(c, d.ID).FontSize(sizeSecondary).TextColor(k.Text).SingleLine())
				case 1:
					ui.Text(c, d.DomainName).FontSize(sizeSecondary).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
				case 2:
					secondaryCell(c, strings.Join(d.Aliases, ", "), len(d.Aliases) > 0, ui.Start)
				case 3:
					secondaryCell(c, d.Status, true, ui.Start)
				case 4:
					en := locales.CommonEnabled()
					if !d.Enabled {
						en = locales.CommonDisabled()
					}
					secondaryCell(c, en, true, ui.Start)
				}
			}).Grow(1).MinHeight(0).Label(locales.CloudfrontDistributions())
			_ = tbl
			a.cfSyncSelection()
			hairline(c)
			a.cfInvalidations(c)
		}
	})
}

func (a *app) cfInvalidations(c *ui.Context) {
	k := tk(c)
	cf := &a.tl.cf
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Height(40).Padding(0, 16, 0, 16).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			labelHelp(c, locales.CloudfrontInvalidationsHelp(), func() ui.Element { return sectionLabel(c, locales.CloudfrontSectionInvalidations()) })
			if cf.invFor != "" {
				mono(ui.Text(c, cf.invFor).FontSize(sizeLabel).TextColor(k.TextMuted))
			}
			if cf.invLoad {
				ui.Spinner(c).Size(12, 12)
			}
			ui.Spacer(c)
			if ghostButton(c, icons.Get("refresh-cw"), "", locales.CloudfrontRefreshInvs(), cf.invFor == "" || cf.invLoad).Label(locales.CloudfrontRefreshInvs()).Clicked() {
				a.cfLoadInvs(false)
			}
		})
		switch {
		case cf.invFor == "":
			emptyBlock(c, "cloud", locales.CloudfrontSelect(), "", 0)
		case cf.invErr != "":
			ui.Row(c).Margin(0, 12, 12, 12).Padding(8, 12).Gap(10).AlignItems(ui.Center).Radius(6).Shrink(0).
				Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
				ui.Icon(c, icons.Get("circle-alert")).TextColor(k.Danger)
				ui.Text(c, locales.CloudfrontInvListFailed(cf.invErr)).Grow(1).MinWidth(0).TextColor(k.Text)
				secondaryButton(c, locales.CommonRetry(), false).OnClick(func() { a.cfLoadInvs(false) })
			})
		case len(cf.invs) == 0 && !cf.invLoad:
			emptyBlock(c, "history", locales.CloudfrontNoInvs(), "", 0)
		default:
			invs := cf.invs
			cf.invList.Key = func(i int) any { return invs[i].ID }
			toolTable(c, &cf.invList, invColumns(), len(invs), func(row, col int) {
				v := &invs[row]
				switch col {
				case 0:
					mono(ui.Text(c, v.ID).FontSize(sizeSecondary).TextColor(k.Text).SingleLine())
				case 1:
					secondaryCell(c, v.Status, true, ui.Start)
				case 2:
					secondaryCell(c, formatTime(v.CreateTime), !v.CreateTime.IsZero(), ui.Start)
				}
			}).Grow(1).MinHeight(0).Label(locales.CloudfrontInvalidationsLabel())
		}
	})
}
