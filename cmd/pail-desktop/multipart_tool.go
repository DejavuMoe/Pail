package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Multipart uploads tool: the unfinished uploads under a bucket and prefix,
// with a multi-select Abort.

type mpState struct {
	bucket, prefix string
	loc            Scope // what the list shows
	items          []core.MultipartUpload
	loaded         bool
	loading        bool
	err            string
	gen            int
	list           ui.ListState
	sel            ui.Selection[string]
	cursor         int
	abortTask      string // task being awaited after an Abort
	abortN         int
}

func mpColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: locales.CommonKey(), ID: "key", MinWidth: 160},
		{Title: locales.MultipartInitiated(), ID: "initiated", Width: 150, MinWidth: 100},
		{Title: locales.MultipartStorageClass(), ID: "class", Width: 130, MinWidth: 90},
		{Title: locales.MultipartInitiator(), ID: "initiator", Width: 130, MinWidth: 80},
		{Title: locales.MultipartUploadId(), ID: "id", Width: 110, MinWidth: 80},
	}
}

func (a *app) mpEnter() {
	m := &a.tl.mp
	*m = mpState{bucket: a.scope.Bucket, prefix: a.scope.Prefix, cursor: -1}
	if m.bucket != "" {
		a.mpLoad()
	}
}

// mpLoad lists the unfinished uploads of the bucket and prefix.
func (a *app) mpLoad() {
	m := &a.tl.mp
	if m.bucket == "" {
		return
	}
	m.gen++
	gen := m.gen
	loc := Scope{AccountID: a.accountID, Bucket: m.bucket, Prefix: strings.TrimSpace(m.prefix)}
	m.loading, m.err = true, ""
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		items, err := a.svc.MultipartUploads(ctx, loc)
		return func() {
			if gen != m.gen {
				return
			}
			m.loading = false
			if err != nil {
				m.err = humanError(err)
				return
			}
			slices.SortStableFunc(items, func(x, y core.MultipartUpload) int {
				if c := strings.Compare(x.Key, y.Key); c != 0 {
					return c
				}
				return x.Initiated.Compare(y.Initiated)
			})
			m.loc, m.items, m.loaded = loc, items, true
			for id := range m.sel.All() { // drop choices that are gone
				if !slices.ContainsFunc(items, func(u core.MultipartUpload) bool { return u.UploadID == id }) {
					m.sel.Remove(id)
				}
			}
		}
	})
}

// mpSelected returns the chosen uploads in table order.
func (m *mpState) selected() []core.MultipartUpload {
	var out []core.MultipartUpload
	for _, u := range m.items {
		if m.sel.Has(u.UploadID) {
			out = append(out, u)
		}
	}
	return out
}

func (a *app) mpAskAbort() {
	m := &a.tl.mp
	ups := m.selected()
	if len(ups) == 0 {
		return
	}
	loc := m.loc
	prefix := loc.Prefix
	if prefix == "" {
		prefix = locales.MultipartRoot()
	}
	a.confirm = confirmState{
		open: true, title: locales.MultipartAbortTitle(len(ups)),
		rows:    []KV{{locales.CommonAccount(), a.account().Name}, {locales.CommonBucket(), loc.Bucket}, {locales.CommonPrefix(), prefix}},
		message: locales.MultipartAbortMessage(len(ups)),
		action:  locales.MultipartAbortAction(len(ups)), danger: true,
		run: func() {
			a.bg(func() func() {
				id, err := a.svc.AbortMultipartUploads(loc, ups)
				return func() {
					if err != nil {
						a.toast(locales.MultipartAbortFailed(humanError(err)))
						return
					}
					m.sel.Clear()
					m.abortTask, m.abortN = id, len(ups)
					a.transfersOpen, a.transTab = true, 0
				}
			})
		},
	}
}

// mpCheckAbort toasts and refreshes once the abort task has finished.
func (a *app) mpCheckAbort() {
	m := &a.tl.mp
	if m.abortTask == "" {
		return
	}
	found := false
	for _, t := range a.tasks {
		if t.ID != m.abortTask && t.GroupID != m.abortTask {
			continue
		}
		found = true
		if !t.Status.Finished() {
			return
		}
	}
	if !found {
		return
	}
	m.abortTask = ""
	a.toast(locales.MultipartAborted(m.abortN))
	a.mpLoad()
}

func (a *app) mpView(c *ui.Context) {
	k := tk(c)
	m := &a.tl.mp
	a.mpCheckAbort()
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			var names []choice
			for _, b := range a.buckets {
				names = append(names, choice{b.Name, b.Name})
			}
			if m.bucket != "" && !slices.ContainsFunc(names, func(o choice) bool { return o.Value == m.bucket }) {
				names = append(names, choice{m.bucket, m.bucket})
			}
			if len(names) > 0 {
				if selectBox(c, locales.CommonBucket(), &m.bucket, names, 220) {
					m.loaded, m.items = false, nil
					m.sel.Clear()
					a.mpLoad()
				}
			}
			ui.Box(c).Width(220).Shrink(0).Children(func() {
				if in := inputBox(c, &m.prefix, locales.CommonPrefix(), locales.CommonPrefix(), inputOpts{mono: true}); in.Submitted() {
					a.mpLoad()
				}
			})
			if ghostButton(c, icons.Get("refresh-cw"), locales.CommonRefresh(), locales.CommonRefresh(), m.bucket == "" || m.loading).Clicked() {
				a.mpLoad()
			}
			if m.loading {
				ui.Spinner(c).Size(14, 14)
			}
			ui.Spacer(c)
			helpIcon(c, locales.MultipartHelp())
			if n := m.sel.Len(); n > 0 {
				ui.Text(c, locales.MultipartSelected(strconv.Itoa(n))).FontSize(sizeSecondary).TextColor(k.TextMuted)
			}
			if secondaryButton(c, locales.MultipartAbort(), m.sel.Len() == 0).Clicked() {
				a.mpAskAbort()
			}
		})
		hairline(c)
		switch {
		case m.bucket == "":
			emptyBlock(c, "layers", locales.MultipartChoose(), "", 0)
		case m.err != "":
			ui.Row(c).Margin(12).Padding(8, 12).Gap(10).AlignItems(ui.Center).Radius(6).Shrink(0).
				Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
				ui.Icon(c, icons.Get("circle-alert")).TextColor(k.Danger)
				ui.Text(c, locales.MultipartListFailed(m.err)).Grow(1).MinWidth(0).TextColor(k.Text)
				secondaryButton(c, locales.CommonRetry(), false).OnClick(a.mpLoad)
			})
		case m.loading && !m.loaded:
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				ui.Spinner(c).Size(22, 22)
				mutedText(c, locales.CommonLoading())
			})
		case len(m.items) == 0:
			emptyBlock(c, "layers", locales.MultipartNone(), "", 0)
		default:
			items := m.items
			m.list.Key = func(i int) any { return items[i].UploadID }
			m.list.Label = func(i int) string { return items[i].Key }
			m.list.Selection = &m.sel
			m.list.Selected = &m.cursor
			toolTable(c, &m.list, mpColumns(), len(items), func(row, col int) {
				u := &items[row]
				switch col {
				case 0:
					mono(ui.Text(c, u.Key).FontSize(sizeSecondary).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0))
				case 1:
					secondaryCell(c, formatTime(u.Initiated), true, ui.Start)
				case 2:
					secondaryCell(c, storageLabel(u.StorageClass), true, ui.Start)
				case 3:
					secondaryCell(c, u.Initiator, u.Initiator != "", ui.Start)
				case 4:
					mono(ui.Text(c, shortID(u.UploadID)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Tooltip(u.UploadID))
				}
			}).Grow(1).MinHeight(0).Label(locales.MultipartTableLabel())
		}
	})
}

// shortID shortens an upload ID to its first characters.
func shortID(id string) string {
	if len(id) <= 10 {
		return id
	}
	return id[:10] + "…"
}
