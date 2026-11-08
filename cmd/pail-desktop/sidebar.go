package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

var toolIDs = []string{"sync", "cloudfront", "rules", "mime", "multipart", "settings"}

// sidebar is the account switcher, the bucket list and the tool list.
func (a *app) sidebar(c *ui.Context) {
	k := tk(c)
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Background(k.Sidebar).Children(func() {
		a.accountSwitcher(c)
		a.bucketList(c)
		a.toolList(c)
	})
}

// accountSwitcher is the sidebar's 44px header, level with the toolbar. On
// macOS the traffic lights take the room c.TitleBar().Left at its left end;
// that room, like any free space here, drags the window.
func (a *app) accountSwitcher(c *ui.Context) {
	bar := c.TitleBar()
	ui.Row(c).Height(topBarH).Shrink(0).AlignItems(ui.Center).DragWindow().Children(func() {
		if bar.Left > 0 {
			ui.Box(c).Width(bar.Left).Height(topBarH).Shrink(0)
		}
		a.accountButton(c)
	})
}

// accountButton is one 44px hover target: initial tile, name over region,
// chevron. It opens the account menu.
func (a *app) accountButton(c *ui.Context) {
	k := tk(c)
	ac := a.account()
	name, sub := ac.Name, ac.Region
	if sub == "" {
		sub = ac.Endpoint
	}
	if ac.ID == "" {
		name, sub = locales.SidebarNoAccount(), locales.SidebarAddOneToStart()
	}
	btn := ui.ButtonBase(c.Key("account")).Label(locales.CommonAccount()).Tooltip(locales.SidebarSwitchAccount()).Height(topBarH).Grow(1).MinWidth(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Transition(hoverFade())
	if btn.Hovered() {
		btn.Background(k.SurfaceHover)
	}
	btn.Children(func() {
		ui.Box(c).Size(24, 24).Radius(RadiusControl).Background(k.ActiveFill).Center().Shrink(0).Children(func() {
			ui.Text(c, initialOf(name)).FontSize(sizeSecondary).FontWeight(600).TextColor(k.Text)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine()
			if ac.SecretMissing {
				secretBadge(c)
			} else {
				ui.Text(c, sub).FontSize(sizeLabel).TextColor(k.TextSubtle).SingleLine()
			}
		})
		ui.Icon(c, icons.Get("chevron-down")).FontSize(14).TextColor(k.TextMuted)
	})
	a.buttonMenu(c, btn, "account", locales.CommonAccount(), func(m *menuBuilder) {
		for _, x := range a.accounts {
			label := x.Name
			if x.SecretMissing {
				label = locales.SidebarAccountSecretMissing(x.Name)
			}
			m.Item(label, func() { a.selectAccount(x.ID) }).Icon("user").Radio(x.ID == a.accountID)
		}
		m.Separator()
		m.Item(locales.SidebarAddAccount(), func() { a.openAccountForm(false) }).Icon("plus")
		m.Item(locales.SidebarEditAccount(), func() { a.openAccountForm(true) }).Icon("pencil").Disabled(ac.ID == "")
		m.Item(locales.SidebarRemoveAccount(), a.askRemoveAccount).Icon("trash-2").Disabled(ac.ID == "")
	})
}

// secretBadge replaces the region line of an account that lost its secret: a
// Warning-tinted pill with a triangle icon (icon, fill and words; the text keeps
// the Text color for contrast).
func secretBadge(c *ui.Context) {
	k := tk(c)
	ui.Row(c).Height(16).Padding(0, 5).Gap(3).Radius(RadiusSmall).AlignItems(ui.Center).AlignSelf(ui.Start).
		Background(k.Warning.Alpha(0.14)).Border(1, k.Warning.Alpha(0.45)).Children(func() {
		ui.Icon(c, icons.Get("triangle-alert")).FontSize(10).TextColor(k.Warning)
		ui.Text(c, locales.SidebarSecretMissing()).FontSize(sizeLabel).FontWeight(500).TextColor(k.Text).SingleLine()
	})
}

func initialOf(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// sidebarLabel is a section label row: 16px side padding, 16 above, 6 below,
// with an optional count in TextSubtle.
func sidebarLabel(c *ui.Context, title string, count int, trailing func()) {
	k := tk(c)
	ui.Row(c).Padding(16, 16, 6, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		sectionLabel(c, title)
		if count > 0 {
			ui.Text(c, groupInt(count)).FontSize(sizeLabel).TextColor(k.TextSubtle)
		}
		ui.Spacer(c)
		if trailing != nil {
			trailing()
		}
	})
}

// sidebarItem is one 28px source-list row. The row sits 8px in from the
// sidebar edge with 8px padding, so icons line up with the section labels.
// A disabled row is dimmed and does nothing.
func (a *app) sidebarItem(c *ui.Context, id string, icon *ui.SVG, name, region string, disabled bool) ui.Element {
	k := tk(c)
	on := a.navSel == id
	row := ui.ButtonBase(c.Key(id)).Label(name).Height(28).Margin(0, 8).Padding(0, 8).Gap(8).Radius(RadiusControl).AlignItems(ui.Center).Disabled(disabled).Transition(hoverFade())
	switch {
	case disabled:
		row.Opacity(0.4)
	case on:
		row.Background(k.ActiveFill)
	case row.Hovered():
		row.Background(k.SurfaceHover)
	}
	row.Children(func() {
		ic := k.TextMuted
		w := 400
		if on && !disabled {
			ic, w = k.Accent, 600
			selectionBar(c, 6, "sidebar")
		}
		ui.Icon(c, icon).FontSize(14).TextColor(ic)
		ui.Text(c, name).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
		if region != "" {
			rc := k.TextSubtle
			if on && !disabled {
				rc = k.TextMuted // TextSubtle on ActiveFill is under 3:1
			}
			ui.Text(c, region).FontSize(sizeLabel).TextColor(rc).SingleLine().Shrink(0)
		}
	})
	if !disabled && row.Clicked() {
		a.navSel = id
		a.navigate()
	}
	return row
}

func (a *app) bucketList(c *ui.Context) {
	k := tk(c)
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		sidebarLabel(c, locales.SidebarBuckets(), len(a.buckets), func() {
			if a.bucketsLoad {
				ui.Spinner(c).Size(12, 12)
			}
			if a.hasAccount() {
				if iconButton(c, icons.Get("plus"), locales.SidebarNewBucket()).Clicked() {
					a.openNewBucket()
				}
			}
		})
		ui.Box(c).Margin(0, 12, 8, 12).Children(func() {
			searchBox(c, &a.bucketFilter, &a.filterH, locales.SidebarFilterBuckets(), locales.SidebarFilterBuckets(), 0, icons.Get("search"), icons.Get("x"))
		})
		if a.bucketsErr != "" {
			ui.Column(c).Margin(0, 12, 8, 12).Padding(8, 10).Gap(6).Radius(RadiusControl).
				Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.Get("circle-alert")).FontSize(14).TextColor(k.Danger).Shrink(0).MarginY(1)
					ui.Text(c, a.bucketsErrText()).FontSize(sizeSecondary).TextColor(k.Text).Grow(1).MinWidth(0)
				})
				ui.Row(c).Justify(ui.End).Children(func() {
					switch a.bucketsFix {
					case "unlock":
						secondaryButton(c, locales.SidebarUnlock(), a.bucketsLoad).OnClick(func() {
							id := a.accountID
							a.askUnlock("buckets", func() {
								if a.accountID == id {
									a.loadBuckets(true)
								}
							})
						})
					case "secret":
						secondaryButton(c, locales.SidebarEnterSecret(), false).OnClick(func() { a.openAccountForm(true) })
					default:
						secondaryButton(c, locales.CommonRetry(), a.bucketsLoad).OnClick(func() { a.loadBuckets(true) })
					}
				})
			})
		}
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Gap(0).Children(func() {
				filter := strings.ToLower(strings.TrimSpace(a.bucketFilter))
				shown := 0
				accountRegion := a.account().Region
				for _, b := range a.buckets {
					label := a.bucketLabel(b.Name)
					if filter != "" && !strings.Contains(strings.ToLower(b.Name), filter) && !strings.Contains(strings.ToLower(label), filter) {
						continue
					}
					shown++
					tip := a.bucketTip(b.Name)
					if b.Region != "" {
						tip += " · " + b.Region
					}
					item := a.sidebarItem(c, "b:"+b.Name, icons.Get("database"), label, rowRegion(b.Region, accountRegion), false).Tooltip(tip)
					a.contextMenu(item, menuSpec{name: locales.MenuBucketActions(b.Name)}, func(m *menuBuilder) {
						name := b.Name
						m.Item(locales.SidebarOpen(), func() { a.openBucket(name) }).Icon("folder-open")
						m.Item(locales.SidebarBucketSettings(), func() { a.openBucketSettings(name) }).Icon("sliders-horizontal")
						m.Item(locales.SidebarSetAlias(), func() { a.openAliasDialog(name) }).Icon("pencil")
						m.Separator()
						m.Item(locales.SidebarDownloadBucket(), func() { a.downloadBucket(name) }).Icon("download")
						m.Item(locales.SidebarCopyContents(), func() { a.openBucketCopy(name, false) }).Icon("copy")
						m.Item(locales.SidebarMoveContents(), func() { a.openBucketCopy(name, true) }).Icon("arrow-left-right")
						m.Separator()
						m.Item(locales.SidebarChangeClass(), func() {
							a.openRewrite(false, rewriteScope{loc: a.bucketRoot(name), all: true})
						}).Icon("layers")
						m.Item(locales.SidebarChangeEncryption(), func() {
							a.openRewrite(true, rewriteScope{loc: a.bucketRoot(name), all: true})
						}).Icon("file-lock")
						if a.cfAvailable() {
							m.Item(locales.SidebarInvalidateCloudFront(), func() { a.invalidateBucket(name) }).Icon("cloud")
						}
						m.Separator()
						m.Item(locales.SidebarEmptyBucket(), func() { a.openEmptyBucket(name) }).Icon("trash-x")
						m.Item(locales.SidebarDeleteBucket(), func() { a.openDeleteBucket(name) }).Icon("trash-2")
						m.Separator()
						m.Item(locales.SidebarCopyName(), func() {
							a.copyText(name)
							a.toast(locales.SidebarBucketNameCopied())
						}).Icon("tag")
					})
				}
				if shown == 0 && len(a.buckets) > 0 {
					ui.Text(c, locales.SidebarNoMatching()).FontSize(sizeSecondary).TextColor(k.TextMuted).Padding(8, 16)
				}
				if len(a.buckets) == 0 && !a.bucketsLoad && a.bucketsErr == "" && a.hasAccount() {
					ui.Text(c, locales.SidebarNoBuckets()).FontSize(sizeSecondary).TextColor(k.TextMuted).Padding(8, 16)
				}
			})
		})
	})
}

// rowRegion is the region a bucket row shows on its right: only one that
// differs from the account's own (which the account switcher already shows),
// and none when the account names no region. The row's tooltip says the
// region either way.
func rowRegion(bucket, account string) string {
	if account == "" || bucket == account {
		return ""
	}
	return bucket
}

// toolList is the TOOLS group at the foot of the sidebar. Its header folds the
// group (collapseTools in settings.json); folded, only the row of the tool
// that is open stays, so that the sidebar still says where the user is.
func (a *app) toolList(c *ui.Context) {
	a.toolSync()
	ui.Column(c).Padding(0, 0, 8, 0).Children(func() {
		a.toolsHeader(c)
		for _, id := range toolIDs {
			if a.collapseTools && id != a.tool {
				continue
			}
			cmd := a.cmd("tool." + id)
			a.sidebarItem(c, "t:"+id, icons.Get(cmd.Icon), cmd.text(), "", !a.enabled(cmd))
		}
	})
}

// toolsHeader is the "TOOLS" label as a button: the label, then a 12px chevron
// (down while the group shows, right while it is folded). The row is inset
// like the list rows (8 and 8), so the label stays level with their icons.
func (a *app) toolsHeader(c *ui.Context) {
	k := tk(c)
	tip, icon := locales.SidebarToolsCollapse(), "chevron-down"
	if a.collapseTools {
		tip, icon = locales.SidebarToolsExpand(), "chevron-right"
	}
	b := ui.ButtonBase(c.Key("tools-header")).Label(locales.SidebarTools()).Tooltip(tip).Expanded(!a.collapseTools).
		Height(24).Margin(10, 8, 2, 8).Padding(0, 8).Gap(4).Radius(RadiusControl).AlignItems(ui.Center).AlignSelf(ui.Start).Transition(hoverFade())
	if b.Hovered() {
		b.Background(k.SurfaceHover)
	}
	b.Children(func() {
		sectionLabel(c, locales.SidebarTools())
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(k.TextSubtle)
	})
	if b.Clicked() {
		a.setCollapseTools(!a.collapseTools)
	}
}

// setCollapseTools folds or unfolds the TOOLS group and saves the choice.
func (a *app) setCollapseTools(on bool) {
	a.collapseTools = on
	a.applySetting("collapseTools", func(st *core.Settings) { st.CollapseTools = on }, func() {
		a.collapseTools = !on
	})
}

// navigate reacts to a new sidebar choice.
func (a *app) navigate() {
	switch {
	case strings.HasPrefix(a.navSel, "b:"):
		a.openBucket(strings.TrimPrefix(a.navSel, "b:"))
	case strings.HasPrefix(a.navSel, "t:"):
		a.openTool(strings.TrimPrefix(a.navSel, "t:"))
	}
}

// bucketsErrText is the error line of the bucket list. The missing-secret
// message is said again every frame, so it follows a language change.
func (a *app) bucketsErrText() string {
	if a.bucketsFix == "secret" {
		return locales.StateBucketsSecretMissing()
	}
	return a.bucketsErr
}
