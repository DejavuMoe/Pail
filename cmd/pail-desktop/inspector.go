package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// inspectorTabs: name is the English identifier (tests and audits address the
// tabs by it); label says it in the language the app shows.
var inspectorTabs = []struct {
	name, icon string
	label      func() string
}{
	{"Overview", "info", locales.InspectorTabOverview}, {"Headers", "sliders-horizontal", locales.InspectorTabHeaders},
	{"Tags", "tag", locales.InspectorTabTags}, {"Permissions", "shield", locales.InspectorTabPermissions},
	{"Versions", "history", locales.InspectorTabVersions}, {"Preview", "eye", locales.InspectorTabPreview},
}

const tabPreview = 5

// inspectorTabBar is a 40px strip of 28x28 icon tabs; the active one is
// Text-colored with a 2px Accent underline.
func (a *app) inspectorTabBar(c *ui.Context) {
	k := tk(c)
	tabs := ui.TabsBase(c, &a.inspTab, len(inspectorTabs))
	tabs.List.Height(40).Gap(4).Padding(0, 12).AlignItems(ui.Center).Children(func() {
		for i, t := range inspectorTabs {
			tab := tabs.Tab(i).Size(28, 28).Radius(6).Label(t.label()).Tooltip(t.label()).Center()
			on := i == a.inspTab
			if !on && tab.Hovered() {
				tab.Background(k.SurfaceHover)
			}
			tab.Children(func() {
				col := k.TextMuted
				if on {
					col = k.Text
				}
				ui.Icon(c, icons.Get(t.icon)).FontSize(14).TextColor(col)
				if on {
					tabIndicator(c)
				}
			})
		}
	})
}

// inspector is the right-hand detail panel.
func (a *app) inspector(c *ui.Context) {
	k := tk(c)
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Background(k.Background).BorderWidth(0, 0, 0, 1).BorderColor(k.Border).Children(func() {
		a.inspectorTabBar(c)
		hairline(c)
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Padding(16).Gap(16).Children(func() {
				a.inspectorBody(c)
			})
		})
		a.inspectorFooter(c)
	})
}

func (a *app) inspectorBody(c *ui.Context) {
	keys := a.selectedKeys()
	switch {
	case a.mg.del.on && len(keys) > 0:
		a.deletedInspector(c)
	case len(keys) > 1 && a.inspTab == 1:
		a.multiHeaders(c)
	case len(keys) > 1:
		a.multiSummary(c)
	case len(keys) == 1 && !strings.HasSuffix(keys[0], "/"):
		a.objectTabs(c)
	case len(keys) == 1:
		a.folderOverview(c, a.rows[a.byKey[keys[0]]])
	default:
		a.currentFolderSummary(c)
	}
}

func (a *app) header(c *ui.Context, icon, title, sub string) {
	k := tk(c)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(40, 40).Radius(8).Background(k.Sidebar).Center().Shrink(0).Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(20).TextColor(k.TextMuted)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).SingleLine()
			mutedText(c, sub).SingleLine()
		})
	})
}

func (a *app) multiSummary(c *ui.Context) {
	folders, files, bytes := a.selectedInfo()
	a.header(c, "layers", locales.CommonItems(a.sel.Len())+" · "+humanBytes(bytes), locales.InspectorMultiple())
	rows := []KV{{locales.InspectorFolders(), groupInt(folders)}, {locales.InspectorObjects(), groupInt(files)}, {locales.InspectorTotalSize(), humanBytes(bytes)}}
	kvTable(c, rows, false)
}

func (a *app) currentFolderSummary(c *ui.Context) {
	folders, files, bytes := a.folderSummary()
	title := a.scope.Bucket
	if parts := splitPrefix(a.scope.Prefix); len(parts) > 0 {
		title = parts[len(parts)-1]
	}
	a.header(c, "folder-open", title, locales.InspectorCurrentFolder())
	rows := []KV{
		{locales.InspectorItems(), groupInt(folders + files)},
		{locales.InspectorFolders(), groupInt(folders)},
		{locales.InspectorObjects(), groupInt(files)},
		{locales.InspectorTotalSize(), humanBytes(bytes)},
	}
	if b, ok := a.bucket(); ok {
		if b.Region != "" {
			rows = append(rows, KV{locales.CommonRegion(), b.Region})
		}
		rows = append(rows, KV{locales.CommonAccount(), a.account().Name})
	}
	kvTable(c, rows, false)
	if a.loading {
		mutedText(c, locales.InspectorStillLoading())
	}
}

func (a *app) folderOverview(c *ui.Context, e Entry) {
	a.header(c, "folder", e.Name, locales.InspectorFolder())
	kvTable(c, []KV{{locales.CommonPrefix(), e.Key}, {locales.InspectorUri(), a.objectURI(e.Key)}}, true,
		copyField(locales.CommonPrefix()), kvCopy{locales.InspectorCopyUri(), locales.InspectorCopiedUri()})
}

// objectTabs shows the tab chosen for the single selected object.
func (a *app) objectTabs(c *ui.Context) {
	e := a.rows[a.byKey[a.selectedKeys()[0]]]
	k := tk(c)
	// Overview, Headers and Tags need the HEAD answer; the rest only the key.
	needsHead := a.inspTab <= 2
	if needsHead {
		switch a.headState {
		case 1:
			a.header(c, icons.NameFor(e.Key, false), e.Name, humanBytes(e.Size))
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				mutedText(c, locales.InspectorLoadingDetails())
			})
			return
		case 3:
			a.header(c, icons.NameFor(e.Key, false), e.Name, humanBytes(e.Size))
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.Get("circle-alert")).FontSize(14).TextColor(k.Danger).Shrink(0).MarginY(1)
				ui.Text(c, locales.InspectorLoadFailed(a.headErr)).FontSize(sizeSecondary).TextColor(k.Text).Grow(1).MinWidth(0)
			})
			ghostButton(c, icons.Get("refresh-cw"), locales.CommonRetry(), locales.CommonRetry(), false).OnClick(func() {
				a.headKey = "" // load again
			})
			return
		}
	}
	sub := humanBytes(e.Size)
	if a.headState == 2 && a.head.ContentType != "" {
		sub = a.head.ContentType
	}
	a.header(c, icons.NameFor(e.Key, false), e.Name, sub)
	switch a.inspTab {
	case 0:
		a.overviewTab(c, e)
	case 1:
		a.headersTab(c)
	case 2:
		a.tagsTab(c)
	case 3:
		a.permissionsTab(c)
	case 4:
		a.versionsTab(c)
	case tabPreview:
		a.previewTab(c, e)
	}
}

func (a *app) overviewTab(c *ui.Context, e Entry) {
	uri, url := a.objectURI(e.Key), a.objectURL(e.Key)
	info := a.head
	size := e.Size
	if a.headState == 2 {
		size = info.Size
	}
	ui.Column(c).Gap(8).Children(func() {
		kvRow(c, locales.CommonSize(), locales.InspectorSizeBytes(humanBytes(size), groupInt(int(size))), false, kvCopy{})
		mod := e.Modified
		if a.headState == 2 && !info.Modified.IsZero() {
			mod = info.Modified
		}
		kvRow(c, locales.InspectorModified(), formatTime(mod), false, kvCopy{})
		cls := e.StorageClass
		if a.headState == 2 && info.StorageClass != "" {
			cls = info.StorageClass
		}
		kvRow(c, locales.InspectorStorageClass(), storageLabel(cls), false, kvCopy{})
		if a.headState == 2 {
			if info.ContentType != "" {
				kvRow(c, locales.InspectorContentType(), info.ContentType, false, kvCopy{})
			}
			if info.SSE != "" {
				enc := info.SSE
				if info.KMSKeyID != "" {
					enc += " · " + info.KMSKeyID
				}
				kvRow(c, locales.InspectorEncryption(), enc, false, kvCopy{})
			}
			if info.Restore != "" {
				kvRow(c, locales.InspectorRestoreStatus(), info.Restore, false, kvCopy{})
			}
			if info.LockMode != "" {
				kvRow(c, locales.InspectorObjectLock(), locales.InspectorLockUntil(info.LockMode, formatTime(info.RetainUntil)), false, kvCopy{})
			}
			if info.LegalHold {
				kvRow(c, locales.InspectorLegalHold(), locales.InspectorLegalHoldOn(), false, kvCopy{})
			}
			if info.ReadOnlyReason != "" {
				kvRow(c, locales.InspectorReadOnly(), info.ReadOnlyReason, false, kvCopy{})
			}
		}
		etag := e.ETag
		if a.headState == 2 && info.ETag != "" {
			etag = info.ETag
		}
		kvRow(c, locales.InspectorEtag(), etag, true, kvCopy{})
		if a.headState == 2 && info.VersionID != "" && info.VersionID != "null" {
			kvRow(c, locales.InspectorVersionId(), info.VersionID, true, kvCopy{locales.InspectorCopyVersionId(), locales.InspectorCopiedVersionId()})
		}
		kvRow(c, locales.CommonKey(), e.Key, true, kvCopy{locales.InspectorCopyKey(), locales.InspectorCopiedKey()})
		kvRow(c, locales.InspectorUri(), uri, true, kvCopy{locales.InspectorCopyUri(), locales.InspectorCopiedUri()})
		kvRow(c, locales.InspectorUrl(), url, true, kvCopy{locales.InspectorCopyUrl(), locales.InspectorCopiedUrl()})
		if cdn := a.objectCDN(e.Key); cdn != "" {
			kvLong(c, locales.InspectorCdnUrl(), cdn, func() {
				copyButton(c, kvCopy{locales.InspectorCopyCdnUrl(), locales.InspectorCopiedCdnUrl()}, cdn)
				if iconButton(c, icons.Get("external-link"), locales.InspectorOpenCdnUrl()).Clicked() {
					a.openExternal(cdn)
				}
			})
		}
	})
}

// kvRow is one label/value line. Long identifiers (monospace) take a line
// of their own with a copy button at the top right.
func kvRow(c *ui.Context, label, value string, monospace bool, cp kvCopy) {
	k := tk(c)
	if monospace {
		kvLong(c, label, value, func() {
			if cp.label != "" {
				copyButton(c, cp, value)
			}
		})
		return
	}
	ui.Row(c.Key(label)).Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Text(c, label).FontSize(sizeSecondary).TextColor(k.TextMuted).Width(96).Shrink(0)
		ui.Text(c, value).FontSize(sizeBody).TextColor(k.Text).Grow(1).MinWidth(0).Selectable()
	})
}

// kvLong is a long value on a line of its own: the label above it with the
// actions (icon buttons) at the right, the value below in 12px monospace.
func kvLong(c *ui.Context, label, value string, actions func()) {
	k := tk(c)
	ui.Column(c.Key(label)).Gap(2).Children(func() {
		ui.Row(c).Height(24).Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(sizeSecondary).TextColor(k.TextMuted).Grow(1)
			actions()
		})
		mono(ui.Text(c, value).FontSize(sizeSecondary).TextColor(k.Text).Selectable())
	})
}

// kvCopy says what a copy button is called and what the toast says after it
// copied (both in the language of the moment).
type kvCopy struct{ label, done string }

// copyField is the generic copy button of a value called name.
func copyField(name string) kvCopy {
	return kvCopy{locales.InspectorCopyField(name), locales.InspectorCopiedField(name)}
}

// copyButton is a small icon button that copies value and toasts.
func copyButton(c *ui.Context, cp kvCopy, value string) {
	if iconButton(c, icons.Get("copy"), cp.label).Clicked() {
		c.WriteClipboard(value)
		c.Toast(cp.done)
	}
}

// kvTable renders read-only label/value rows: no grid, 8px between rows.
// Values flagged monospace (or long ones) take their own line.
// copies[i], when given, names the copy button of a long value in rows[i].
func kvTable(c *ui.Context, rows []KV, monospaceValues bool, copies ...kvCopy) {
	if len(rows) == 0 {
		mutedText(c, locales.CommonNone())
		return
	}
	k := tk(c)
	ui.Column(c).Gap(8).Children(func() {
		for i, kv := range rows {
			ui.Column(c.Key(i)).Children(func() {
				if len(kv.Value) > 28 {
					cp := copyField(kv.Key)
					if i < len(copies) && copies[i].label != "" {
						cp = copies[i]
					}
					kvRow(c, kv.Key, kv.Value, true, cp)
					return
				}
				ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, kv.Key).FontSize(sizeSecondary).TextColor(k.TextMuted).Width(96).Shrink(0)
					v := ui.Text(c, kv.Value).FontSize(sizeBody).TextColor(k.Text).Grow(1).MinWidth(0).Selectable()
					if monospaceValues {
						mono(v.FontSize(sizeSecondary))
					}
				})
			})
		}
	})
}
