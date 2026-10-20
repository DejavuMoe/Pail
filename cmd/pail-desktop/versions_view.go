package main

import (
	"context"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// versionsForm is the Versions tab of a single object.
type versionsForm struct {
	key   string
	gen   int
	state int // 0 none, 1 loading, 2 ready, 3 failed
	rows  []core.Version
	err   string
	sel   map[string]bool // version IDs chosen for a group action
}

// keyMarkerBefore returns a key marker that sorts just below key, so listing
// versions from it starts at key instead of at the beginning of its folder.
func keyMarkerBefore(key string) string {
	if key == "" {
		return ""
	}
	last := key[len(key)-1]
	if last <= 0x21 || last >= 0x7f {
		return ""
	}
	return key[:len(key)-1] + string(rune(last-1))
}

// listKeyVersions pages through ListVersions for one key. The service lists a
// folder, so the key's folder is listed from just before the key and the paging
// stops at the first key past it.
func (a *app) listKeyVersions(ctx context.Context, loc Scope, key string) ([]core.Version, error) {
	dir := ""
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		dir = key[:i+1]
	}
	l := Scope{AccountID: loc.AccountID, Bucket: loc.Bucket, Prefix: dir}
	km, vm := keyMarkerBefore(key), ""
	var out []core.Version
	for pages := 0; pages < 200; pages++ {
		pg, err := a.svc.ListVersions(ctx, l, km, vm)
		if err != nil {
			return nil, err
		}
		beyond := false
		for _, v := range pg.Versions {
			switch {
			case v.Key == key:
				out = append(out, v)
			case v.Key > key:
				beyond = true
			}
		}
		if beyond || (pg.NextKeyMarker == "" && pg.NextVersionMarker == "") {
			break
		}
		km, vm = pg.NextKeyMarker, pg.NextVersionMarker
	}
	slices.SortStableFunc(out, func(x, y core.Version) int {
		if x.Latest != y.Latest {
			if x.Latest {
				return -1
			}
			return 1
		}
		return y.Modified.Compare(x.Modified)
	})
	return out, nil
}

// ensureVersions loads the versions of the selected object when the tab opens.
func (a *app) ensureVersions() {
	f := &a.mg.vers
	keys := a.selectedKeys()
	if a.inspTab != 4 || len(keys) != 1 || strings.HasSuffix(keys[0], "/") || a.mg.del.on {
		return
	}
	key := keys[0]
	if f.key == key && f.state != 0 {
		return
	}
	*f = versionsForm{key: key, state: 1, gen: f.gen + 1}
	gen, loc := f.gen, a.scope
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		rows, err := a.listKeyVersions(ctx, loc, key)
		return func() {
			f := &a.mg.vers
			if f.gen != gen {
				return
			}
			if err != nil {
				f.state, f.err = 3, plainError(err, locales.VersionsListWhat())
				return
			}
			f.state, f.rows = 2, rows
		}
	})
}

func (a *app) refreshVersions() { a.mg.vers.state = 0 }

// versionsTab lists every version of the selected object.
func (a *app) versionsTab(c *ui.Context) {
	a.ensureVersions()
	f := &a.mg.vers
	k := tk(c)
	switch f.state {
	case 0, 1:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.VersionsLoading())
		})
		return
	case 3:
		errorLine(c, f.err)
		ghostButton(c, iconOf("refresh-cw"), locales.CommonRetry(), locales.CommonRetry(), false).AlignSelf(ui.Start).OnClick(a.refreshVersions)
		return
	}
	if len(f.rows) == 0 {
		emptyBlock(c, "history", locales.VersionsNone(), "", 200)
		return
	}
	loc := a.scope
	ui.Column(c).Gap(8).Children(func() {
		if n := len(f.sel); n > 0 {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.VersionsSelected(n)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).Grow(1)
				refs := a.selectedVersionRefs()
				real := 0
				for _, r := range f.rows {
					if f.sel[r.VersionID] && !r.DeleteMarker {
						real++
					}
				}
				if real > 0 && iconButton(c, iconOf("download"), locales.VersionsDownloadChosen()).Clicked() {
					a.downloadVersions(loc, a.downloadableRefs())
				}
				if iconButton(c, iconOf("trash-2"), locales.VersionsDeleteChosen()).Clicked() {
					a.askDeleteVersions(loc, refs)
				}
			})
		}
		for _, v := range f.rows {
			a.versionRow(c, loc, v)
		}
	})
}

func (a *app) selectedVersionRefs() []core.ObjectRef {
	f := &a.mg.vers
	var out []core.ObjectRef
	for _, v := range f.rows {
		if f.sel[v.VersionID] {
			out = append(out, core.ObjectRef{Key: v.Key, VersionID: v.VersionID})
		}
	}
	return out
}

func (a *app) downloadableRefs() []core.ObjectRef {
	f := &a.mg.vers
	var out []core.ObjectRef
	for _, v := range f.rows {
		if f.sel[v.VersionID] && !v.DeleteMarker {
			out = append(out, core.ObjectRef{Key: v.Key, VersionID: v.VersionID})
		}
	}
	return out
}

func (a *app) versionRow(c *ui.Context, loc Scope, v core.Version) {
	k := tk(c)
	f := &a.mg.vers
	on := f.sel[v.VersionID]
	ref := core.ObjectRef{Key: v.Key, VersionID: v.VersionID}
	row := ui.ButtonBase(c.Key("ver:"+v.VersionID)).Label(locales.VersionsVersionLabel(shortVersion(v.VersionID))).Padding(4, 8).Radius(6).Gap(6).AlignItems(ui.Center).Shrink(0)
	switch {
	case on:
		row.Background(k.Selection)
	case row.Hovered():
		row.Background(k.SurfaceHover)
	}
	row.Children(func() {
		if on {
			selectionBar(c, 6)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				col := k.Text
				if v.DeleteMarker {
					col = k.TextMuted
					ui.Icon(c, iconOf("trash-x")).FontSize(14).TextColor(k.TextMuted)
				}
				text := humanBytes(v.Size)
				if v.DeleteMarker {
					text = locales.VersionsDeleteMarker()
				}
				ui.Text(c, text).FontSize(sizeBody).TextColor(col).SingleLine()
				if v.Latest {
					badge(c, locales.VersionsLatest())
				}
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, formatTime(v.Modified)).FontSize(sizeSecondary).TextColor(k.TextSubtle).SingleLine()
				mono(ui.Text(c, shortVersion(v.VersionID)).FontSize(sizeLabel).TextColor(k.TextSubtle).SingleLine())
			})
		})
		if !v.DeleteMarker {
			if iconButton(c, iconOf("download"), locales.VersionsDownloadThis()).Clicked() {
				a.downloadVersions(loc, []core.ObjectRef{ref})
			}
			if iconButton(c, iconOf("link"), locales.VersionsSignedFor()).Clicked() {
				a.openSigned([]string{v.Key}, v.VersionID)
			}
			if !v.Latest {
				if iconButton(c, iconOf("undo-2"), locales.VersionsRestoreThis()).Clicked() {
					a.restoreVersion(loc, ref)
				}
			}
		}
		if iconButton(c, iconOf("trash-2"), locales.VersionsDeleteThis()).Clicked() {
			a.askDeleteVersions(loc, []core.ObjectRef{ref})
		}
	})
	if row.Clicked() {
		if f.sel == nil {
			f.sel = map[string]bool{}
		}
		if on {
			delete(f.sel, v.VersionID)
		} else {
			f.sel[v.VersionID] = true
		}
	}
}

// downloadVersions saves specific versions into the download folder.
func (a *app) downloadVersions(loc Scope, refs []core.ObjectRef) {
	if len(refs) == 0 {
		return
	}
	dir := a.svc.Settings().DefaultDownloadDir
	go func() {
		d := dir
		if d == "" {
			ds := a.pick(true)
			if len(ds) == 0 {
				return
			}
			d = ds[0]
		}
		a.post(func() {
			a.bg(func() func() {
				gid, err := a.svc.Download(core.DownloadRequest{Source: loc, Versions: refs, DestDir: d, Conflict: a.svc.Settings().Conflict})
				return func() {
					if err != nil {
						a.toast(locales.VersionsDownloadFailed(humanError(err)))
						return
					}
					a.transfersOpen, a.transTab = true, 0
					a.addWatch(gid, &watch{loc: loc, kind: "download", dest: d})
				}
			})
		})
	}()
}

func (a *app) restoreVersion(loc Scope, ref core.ObjectRef) {
	a.bg(func() func() {
		ctx, cancel := a.shortCtx()
		defer cancel()
		err := a.svc.RestoreVersion(ctx, loc, ref)
		return func() {
			if err != nil {
				a.toast(locales.VersionsRestoreFailed(humanError(err)))
				return
			}
			a.toast(locales.VersionsRestored(shortVersion(ref.VersionID)))
			a.refreshVersions()
			if loc == a.scope {
				a.headKey = ""
			}
			a.reloadSame(loc)
		}
	})
}

// askDeleteVersions confirms the permanent removal of versions.
func (a *app) askDeleteVersions(loc Scope, refs []core.ObjectRef) {
	if len(refs) == 0 {
		return
	}
	rows := append(a.scopeRows(loc), KV{locales.VersionsObject(), refs[0].Key})
	title, message, action := locales.VersionsDeleteOneTitle(), locales.VersionsDeleteOneMsg(), locales.VersionsDeleteOneAction()
	if len(refs) == 1 {
		rows = append(rows, KV{locales.VersionsVersion(), shortVersion(refs[0].VersionID)})
	} else {
		title, message, action = locales.VersionsDeleteManyTitle(), locales.VersionsDeleteManyMsg(), locales.VersionsDeleteManyAction()
		rows = append(rows, KV{locales.VersionsVersions(), locales.CommonVersions(len(refs))})
	}
	a.confirm = confirmState{
		open: true, title: title, rows: rows, danger: true,
		message: message, action: action,
		run: func() {
			a.bg(func() func() {
				id, err := a.svc.DeleteVersions(loc, refs)
				return func() {
					if err != nil {
						a.toast(locales.VersionsDeleteFailed(humanError(err)))
						return
					}
					a.follow(id, func(t Task) {
						sum := a.bulkSummary(t)
						switch {
						case sum.failed > 0 && sum.ok == 0:
							a.toast(locales.VersionsDeleteFailed(sum.firstErr))
						case sum.failed > 0:
							a.toast(locales.VersionsDeletedPartial(sum.ok, sum.failed))
						default:
							a.toast(locales.VersionsDeletedCount(sum.ok))
						}
						a.mg.vers.sel = nil
						a.refreshVersions()
						if loc == a.scope {
							a.headKey = ""
						}
						a.reloadSame(loc)
					})
				}
			})
		},
	}
}

// ---- deleted items ------------------------------------------------------------------------

// deletedMode lists the keys of the open folder whose latest version is a
// delete marker, so they can be restored.
type deletedMode struct {
	on     bool
	bucket string
	keys   map[string]bool
}

func (a *app) isDeletedRow(key string) bool { return a.mg.del.on && a.mg.del.keys[key] }

// toggleDeleted switches between the live listing and the deleted items.
func (a *app) toggleDeleted() {
	d := &a.mg.del
	if !a.browsing() {
		return
	}
	d.on = !d.on
	if d.on {
		a.objOps.flat = false // the deleted listing is a folder view
	}
	d.bucket = a.scope.Bucket
	if !d.on {
		d.keys = nil
	}
	a.sel.Clear()
	a.cursor = -1
	a.resetHead()
	a.reload()
}

// reloadHook sends reload to the deleted listing while that mode is on; it
// reports whether it took the reload.
func (a *app) reloadHook() bool {
	d := &a.mg.del
	if !d.on {
		return false
	}
	if d.bucket != a.scope.Bucket {
		d.on, d.keys = false, nil
		return false
	}
	a.reloadDeleted()
	return true
}

func (a *app) reloadDeleted() {
	if a.cancelList != nil {
		a.cancelList()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancelList = cancel
	a.listGen++
	gen, scope := a.listGen, a.scope
	a.loading, a.listErr = true, ""
	go func() {
		entries, err := a.collectDeleted(ctx, scope)
		if ctx.Err() != nil {
			return
		}
		a.post(func() {
			if gen != a.listGen {
				return
			}
			a.loading = false
			if err != nil {
				a.listErr = plainError(err, locales.VersionsListDeletedWhat())
				return
			}
			keys := map[string]bool{}
			for _, e := range entries {
				if !e.Folder {
					keys[e.Key] = true
				}
			}
			a.mg.del.keys = keys
			a.raw = entries
			a.rowsDirty = true
			a.pruneSelection()
			a.applySelectAfter()
		})
	}()
}

// collectDeleted returns the sub-folders of scope and its deleted keys.
func (a *app) collectDeleted(ctx context.Context, scope Scope) ([]core.Entry, error) {
	var out []core.Entry
	token := ""
	for {
		pg, err := a.svc.List(ctx, scope, core.ListOptions{Token: token})
		if err != nil {
			return nil, err
		}
		for _, e := range pg.Entries {
			if e.Folder {
				out = append(out, e)
			}
		}
		if pg.NextToken == "" {
			break
		}
		token = pg.NextToken
	}
	type info struct {
		size     int64
		class    string
		hasSize  bool
		modified time.Time
		deleted  bool
	}
	keys := map[string]*info{}
	var order []string
	km, vm := "", ""
	for pages := 0; pages < 1000; pages++ {
		pg, err := a.svc.ListVersions(ctx, scope, km, vm)
		if err != nil {
			return nil, err
		}
		for _, v := range pg.Versions {
			if strings.HasSuffix(v.Key, "/") {
				continue
			}
			in := keys[v.Key]
			if in == nil {
				in = &info{}
				keys[v.Key] = in
				order = append(order, v.Key)
			}
			if v.DeleteMarker {
				if v.Latest {
					in.deleted, in.modified = true, v.Modified
				}
				continue
			}
			if !in.hasSize {
				in.size, in.class, in.hasSize = v.Size, v.StorageClass, true
			}
		}
		if pg.NextKeyMarker == "" && pg.NextVersionMarker == "" {
			break
		}
		km, vm = pg.NextKeyMarker, pg.NextVersionMarker
	}
	for _, k := range order {
		in := keys[k]
		if !in.deleted {
			continue
		}
		out = append(out, core.Entry{Key: k, Name: path.Base(k), Size: in.size, Modified: in.modified, StorageClass: in.class})
	}
	return out, nil
}

// restoreSelected undeletes the chosen rows.
func (a *app) restoreSelected() {
	var keys []string
	for _, k := range a.selectedKeys() {
		if a.isDeletedRow(k) || strings.HasSuffix(k, "/") {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	a.submitRestore(core.Selection{Location: a.scope, Keys: keys}, a.scope)
}

// askRestoreAll confirms restoring every deleted item below the open folder.
func (a *app) askRestoreAll() {
	loc := a.scope
	prefix := loc.Prefix
	if prefix == "" {
		prefix = locales.VersionsBucketRoot()
	}
	rows := append(a.scopeRows(loc), KV{locales.CommonPrefix(), prefix})
	a.confirm = confirmState{
		open: true, title: locales.VersionsRestoreAllTitle(), rows: rows, action: locales.CommonRestore(),
		message: locales.VersionsRestoreAllMsg(),
		run:     func() { a.submitRestore(core.Selection{Location: loc, All: true}, loc) },
	}
}

func (a *app) submitRestore(sel core.Selection, loc Scope) {
	a.bg(func() func() {
		id, err := a.svc.RestoreDeleted(sel)
		return func() {
			if err != nil {
				a.toast(locales.VersionsRestoreFailed(humanError(err)))
				return
			}
			a.follow(id, func(t Task) {
				sum := a.bulkSummary(t)
				switch {
				case sum.failed > 0 && sum.ok == 0:
					a.toast(locales.VersionsRestoreItemsFailed(sum.firstErr))
				case sum.failed > 0:
					a.toast(locales.VersionsRestoredPartial(sum.ok, sum.failed))
				case sum.ok == 0:
					a.toast(locales.VersionsNothingToRestore())
				default:
					a.toast(locales.VersionsRestoredItems(sum.ok))
				}
				a.sel.Clear()
				a.reloadSame(loc)
			})
		}
	})
}

// ghostToggle is a ghost button with a label that rests on ActiveFill while on.
func ghostToggle(c *ui.Context, icon *ui.SVG, label, tip string, on, disabled, iconOnly bool) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c.Key("toggle:"+label)).Label(label).Tooltip(tip).Radius(6).Height(28).Gap(6).Padding(0, 8).Center().Disabled(disabled)
	if iconOnly {
		b.Width(28).Padding(0)
	}
	switch {
	case disabled:
		b.Opacity(0.4)
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case on:
		b.Background(k.ActiveFill)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	if on && !disabled {
		b.Border(1, k.BorderStrong)
	}
	return b.Children(func() {
		col, w := k.TextMuted, 400
		if on {
			col, w = k.Text, 600
		}
		ui.Icon(c, icon).FontSize(14).TextColor(col)
		if !iconOnly {
			ui.Text(c, label).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine()
		}
	})
}

// deletedInspector is the detail panel while deleted items are listed.
func (a *app) deletedInspector(c *ui.Context) {
	keys := a.selectedKeys()
	k := tk(c)
	if len(keys) == 1 && !strings.HasSuffix(keys[0], "/") {
		e := a.rows[a.byKey[keys[0]]]
		a.header(c, "trash-2", e.Name, locales.VersionsDeletedLabel())
		kvTable(c, []KV{{locales.VersionsDeletedLabel(), formatTime(e.Modified)}, {locales.CommonSize(), humanBytes(e.Size)}, {locales.CommonKey(), e.Key}}, false,
			kvCopy{}, kvCopy{}, kvCopy{locales.InspectorCopyKey(), locales.InspectorCopiedKey()})
	} else {
		a.header(c, "trash-2", locales.CommonItems(len(keys)), locales.VersionsDeletedItems())
	}
	ui.Row(c).Gap(8).Children(func() {
		if secondaryButton(c, locales.CommonRestore(), false).Clicked() {
			a.restoreSelected()
		}
	})
	_ = k
}
