package main

import (
	"fmt"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// statusBar is the one-line summary under the window.
func (a *app) statusBar(c *ui.Context) {
	k := tk(c)
	// Clip: the digits of a roll move beyond the line, and the bar is where they
	// are cut, not the transfers strip above it.
	ui.Row(c).Height(statusBarH).Padding(0, 12).Gap(6).AlignItems(ui.Center).Clip().
		Background(k.Sidebar).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		part := func(s string) { ui.Text(c, s).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine() }
		// What counts (items, selected, running transfers) rolls its digits as
		// it changes, unless it is still on its way (rolling false); the keys
		// keep each one's identity as the parts around it come and go.
		count := func(key, s string, rolling bool) {
			countText(c, key, s, k.TextMuted, rolling)
		}
		dot := func() { ui.Text(c, "·").FontSize(sizeSecondary).TextColor(k.TextSubtle) }
		// The left side is about the folder only: the account is in the sidebar's
		// switcher and the region in the inspector. Tool views leave it empty.
		if a.tool == "" {
			a.statusBrowse(part, count, dot)
		}
		ui.Spacer(c)
		a.tookIndicator(c, count)
		a.lockIndicator(c)
		if !a.hasAccount() && len(a.tasks) == 0 {
			return
		}
		a.transferCounts(c, part)
	})
}

// transferCounts is the running uploads and downloads at the right end of the
// bar, as one small button that shows or hides the transfers panel (the
// "transfers" command). It is the way to the panel while no task shows its
// strip (transfersBar).
func (a *app) transferCounts(c *ui.Context, part func(string)) {
	k := tk(c)
	var up, down int
	for _, t := range a.tasks {
		if taskGroup(t) != 0 {
			continue
		}
		switch t.Kind {
		case core.KindUpload:
			up++
		case core.KindDownload:
			down++
		}
	}
	cmd := a.cmd("transfers")
	b := ui.ButtonBase(c.Key("transfers")).Label(cmd.text()).Tooltip(cmd.tooltip()).Expanded(a.transfersOpen).
		Height(20).Padding(0, 6).Gap(6).Radius(RadiusKeycap).AlignItems(ui.Center).Transition(hoverFade())
	switch {
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	b.Children(func() {
		for i, n := range []struct {
			icon  string
			count int
		}{{"arrow-up", up}, {"arrow-down", down}} {
			ui.Row(c.Key(i)).Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get(n.icon)).FontSize(12).TextColor(k.TextMuted)
				part(fmt.Sprint(n.count))
			})
		}
	})
	if b.Clicked() {
		a.run("transfers", "statusbar")
	}
}

// tookIndicator shows how long the last finished operation took, left of the
// transfer counts; hovering it says which operation that was, and clicking it
// opens the list of recent operations. Nothing shows before the first operation
// has finished. When only operations that failed or were canceled are on the
// list (the last success went with an account switch, or there was none), the
// timer shows without a time.
func (a *app) tookIndicator(c *ui.Context, count func(key, s string, rolling bool)) {
	if a.took.what == "" && len(a.ops) == 0 {
		a.opsOpen = false
		return
	}
	k := tk(c)
	text, tip := "", locales.RecentOpsTitle()
	if a.took.what != "" {
		text, tip = humanDuration(a.took.d), a.took.tip()
	}
	b := ui.ButtonBase(c.Key("took")).Label(tip).Tooltip(tip+" · "+locales.StatusbarRecentHint()).Expanded(a.opsOpen).
		Height(20).Padding(0, 6).Gap(3).Radius(RadiusKeycap).AlignItems(ui.Center).Transition(hoverFade())
	switch {
	case a.opsOpen:
		b.Background(k.SurfacePressed) // pressed while its list shows
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, icons.Get("timer")).FontSize(12).TextColor(k.TextMuted)
		if text != "" {
			count("took", text, true)
		}
	})
	if b.Clicked() {
		a.opsOpen = !a.opsOpen
	}
	a.recentOpsPopover(c, b)
}

func (a *app) statusBrowse(part func(string), count func(key, s string, rolling bool), dot func()) {
	if _, ok := a.bucket(); !ok {
		return
	}
	items := locales.CommonItems(len(a.rows))
	if a.search != "" {
		items = locales.StatusbarItemsOf(len(a.rows), groupInt(len(a.raw)))
	}
	if a.loading {
		items = locales.StatusbarLoading(items)
	}
	// While the folder loads, its count grows with every page: it changes at
	// once, and rolls again for the changes that follow (a filter, a refresh).
	count("items", items, !a.loading)
	if n := a.sel.Len(); n > 0 {
		_, _, bytes := a.selectedInfo()
		dot()
		count("selected", locales.StatusbarSelected(fmt.Sprint(n), humanBytes(bytes)), true)
	}
}
