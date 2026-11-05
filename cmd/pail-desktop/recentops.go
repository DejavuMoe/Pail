package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The list of recent operations (docs/dev/design.md "Recent operations"):
// clicking the time in the status bar opens it above the bar. Each line is one
// operation the user started (loading the buckets, opening a folder, an upload,
// a download, a delete) with how it ended and how long it took. It is not the
// diagnostic log: it keeps no request details, lives in memory only, and is
// gone when Pail quits. How many lines it keeps, and whether it survives a
// change of account, are settings (optime.go has the recording).

const (
	opsPanelW = 400 // the popover's width
	opsHeadH  = 36  // its title row
	opsRowH   = 44  // one operation: two lines of text
	opsMaxH   = 360 // the most it grows to before its lines scroll
	opsEmptyH = 96  // the body of an empty list
)

// name says what the operation was, in the language shown now.
func (e opEntry) name() string {
	switch e.what {
	case tookBuckets:
		return locales.RecentOpsBuckets()
	case tookUpload:
		return locales.RecentOpsUpload()
	case tookDownload:
		return locales.RecentOpsDownload()
	case tookDelete:
		return locales.RecentOpsDelete()
	}
	return locales.RecentOpsList()
}

// stateText says how it ended.
func (e opEntry) stateText() string {
	switch e.state {
	case opFailed:
		return locales.RecentOpsStateFailed()
	case opCanceled:
		return locales.RecentOpsStateCanceled()
	}
	return locales.RecentOpsStateDone()
}

// took is how long it took; "—" for one that never ran.
func (e opEntry) took() string {
	if !e.ran {
		return "—"
	}
	return humanDuration(e.d)
}

// label is the line as assistive technology reads it.
func (e opEntry) label() string {
	if !e.ran {
		return locales.RecentOpsRowNoTime(e.name(), e.stateText())
	}
	return locales.RecentOpsRow(e.name(), e.stateText(), e.took())
}

// detail is the second line: how it ended when that is not plainly a success,
// what it did to how many items, where, and (with several accounts in the
// list) for which account. A failure that counted its failed items says so in
// that count; one that did not says "Failed" and why.
func (e opEntry) detail(withAccount bool) string {
	var parts []string
	if e.state == opCanceled {
		parts = append(parts, locales.RecentOpsStateCanceled())
	}
	if e.state == opFailed && e.bad == 0 {
		parts = append(parts, locales.RecentOpsStateFailed())
	}
	switch e.what {
	case tookList:
		if e.state == opDone {
			parts = append(parts, locales.CommonItems(e.n))
		}
	case tookUpload, tookDownload:
		if e.n > 0 {
			parts = append(parts, locales.CommonFiles(e.n))
		}
	case tookDelete:
		if e.n > 0 {
			parts = append(parts, locales.CommonObjects(e.n))
		}
	}
	if e.bad > 0 {
		parts = append(parts, locales.RecentOpsFailedN(strconv.Itoa(e.bad)))
	}
	if e.state == opFailed && e.err != "" {
		parts = append(parts, oneLine(e.err))
	}
	if e.where != "" {
		parts = append(parts, e.where)
	}
	if withAccount && e.accountName != "" {
		parts = append(parts, e.accountName)
	}
	return strings.Join(parts, " · ")
}

// oneLine folds the line breaks of an error into spaces, so that it reads as
// one line under a name.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clock is when it ended: the time today, the date and time on another day.
func (e opEntry) clock(now time.Time) string {
	t := e.at.Local()
	if y, m, d := t.Date(); now.Local().Year() == y && now.Local().Month() == m && now.Local().Day() == d {
		return t.Format("15:04:05")
	}
	return t.Format("01-02 15:04")
}

// opIcon is the icon and colour of how an operation ended: three shapes, so
// that the colour is not the only cue.
func opIcon(k tokens, s opState) (name string, col ui.Color) {
	switch s {
	case opFailed:
		return "circle-alert", k.Danger
	case opCanceled:
		return "x", k.TextMuted
	}
	return "circle-check", k.Success
}

// severalAccounts reports whether the list holds operations of more than one
// account, which is when each line names its account.
func severalAccounts(ops []opEntry) bool {
	for _, e := range ops {
		if e.account != ops[0].account {
			return true
		}
	}
	return false
}

// recentOpsPopover shows the list over the status bar while a.opsOpen is set,
// anchored to the time (anchor), newest operation first. Clicking outside it
// or pressing Escape closes it.
func (a *app) recentOpsPopover(c *ui.Context, anchor ui.Element) {
	if !a.opsOpen {
		return
	}
	k := tk(c)
	winW, winH := c.Size()
	a.trimOps() // a lowered limit shows at once
	ops := a.ops
	body := float32(opsEmptyH)
	if len(ops) > 0 {
		body = float32(len(ops))*opsRowH + 8
	}
	maxH := min(opsMaxH, winH-statusBarH-16)
	h := min(opsHeadH+1+body, maxH)
	tall := opsHeadH+1+body > maxH
	ui.PopoverBase(c, anchor, &a.opsOpen, func(panel ui.Element) {
		panel.Width(min(opsPanelW, winW-16)).Height(h).Margin(4, 0, 0, 0).Label(locales.RecentOpsTitle())
		overlayPanel(c, panel, overlayPopover)
		// It grows out of the time, upward: the status bar is the window's bottom
		// edge, so mygo puts it above its anchor.
		b := anchor.Bounds()
		above := flips(b.Y+b.H+4, b.Y-h-4, h, winH)
		dy := float32(-popRise)
		if above {
			dy = popRise
		}
		overlayEnter(c, panel, 0, dy, 1, b2f(above))
		panel.Transition(overlayExit())

		ui.Row(c.Key("ops-head")).Height(opsHeadH).Padding(0, 8, 0, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, locales.RecentOpsTitle()).FontSize(sizeBody).FontWeight(600).TextColor(k.Text).SingleLine()
			if len(ops) > 0 {
				ui.Text(c, groupInt(len(ops))).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
			}
			ui.Spacer(c)
			if len(ops) > 0 {
				ghostButton(c, nil, locales.RecentOpsClear(), locales.RecentOpsClearTip(), false).Height(24).OnClick(a.clearOps)
			}
		})
		hairline(c).Shrink(0)
		if len(ops) == 0 {
			emptyBlock(c, "history", locales.RecentOpsEmpty(), "", 0)
			return
		}
		multi := severalAccounts(ops)
		now := time.Now()
		list := ui.Scroll(c.Key("ops-list")).Shrink(1).MinHeight(0)
		right := float32(4)
		if tall {
			right = scrollGutter
			list.ScrollbarInsets(0, scrollBarInset, 0, 0)
		}
		list.Children(func() {
			ui.Column(c).Padding(4, right, 4, 4).Children(func() {
				for i := len(ops) - 1; i >= 0; i-- { // the newest on top
					a.opRow(c, ops[i], multi, now)
				}
			})
		})
	})
}

// opRow is one operation of the list: how it ended (an icon), what it was and
// the details under it, how long it took and when it ended.
func (a *app) opRow(c *ui.Context, e opEntry, withAccount bool, now time.Time) {
	k := tk(c)
	detail := e.detail(withAccount)
	icon, col := opIcon(k, e.state)
	ui.Row(c.Key("op:"+strconv.FormatUint(e.seq, 10))).Height(opsRowH).Padding(0, 8).Gap(10).AlignItems(ui.Center).Shrink(0).
		Label(e.label()).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(col).Shrink(0)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, e.name()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine()
			if detail != "" {
				ui.Text(c, detail).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Tooltip(detail)
			}
		})
		ui.Column(c).Shrink(0).AlignItems(ui.End).Gap(2).Children(func() {
			ui.Text(c, e.took()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine()
			ui.Text(c, e.clock(now)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
		})
	})
}
