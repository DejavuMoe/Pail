package main

import (
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

const (
	statusBarH   = 24
	collapsedBar = 32
)

// view builds one frame: sidebar | main | inspector above the transfers
// panel, then the status bar, and the dialogs over everything. Without an
// account the body is the welcome card.
func (a *app) view(c *ui.Context) {
	k := applyTheme(c, a.opts.theme)
	a.syncTheme()
	a.drain()
	if time.Since(a.vu.at) > time.Second {
		a.refreshVault() // an idle auto-lock shows up here
	}
	a.autoTransfers()
	a.flushOutput(c)
	if a.rowsDirty || a.sort != a.sortSeen {
		a.rebuildRows()
	}
	a.ensureHead()
	a.ensurePreview()
	a.pollShortcuts(c)
	a.syncMenu()

	winW, winH := c.Size()
	ui.Column(c).Fill().Background(k.Background).Children(func() {
		switch {
		case !a.hasAccount():
			ui.Column(c).Grow(1).MinHeight(0).Children(func() {
				a.topBarRow(c, locales.ShellWindow(), nil, func() { a.moreMenu(c) })
				a.welcome(c)
			})
		case a.transfersOpen:
			topH := winH - statusBarH - a.transH
			if ui.SplitVertical(c, &topH, func() { a.body(c, winW) }, func() { a.transfersPanel(c) }).Grow(1).MinHeight(0).Changed() {
				a.transH = clampF(winH-statusBarH-topH, 96, max(96, winH-statusBarH-200))
			}
		default:
			ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.body(c, winW) })
			a.transfersBar(c)
		}
		a.statusBar(c)
	})
	a.flushOutput(c)
	a.dialogs(c)
}

// welcome is the empty state shown until the first account exists.
func (a *app) welcome(c *ui.Context) {
	k := tk(c)
	ui.Column(c).Grow(1).MinHeight(0).Center().Gap(4).Children(func() {
		ui.Icon(c, icons.Get("database")).FontSize(32).TextColor(k.TextSubtle).MarginY(4)
		ui.Text(c, locales.ShellWelcomeTitle()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
		ui.Text(c, locales.ShellWelcomeSub()).FontSize(sizeSecondary).TextColor(k.TextMuted)
		primaryButton(c, locales.ShellAddAccount(), false).Margin(16, 0, 0, 0).OnClick(func() { a.openAccountForm(false) })
	})
}

// body is sidebar | main | inspector. The widths every view lays out by are
// worked out here, from this frame's window size (see layoutMetrics).
func (a *app) body(c *ui.Context, winW float32) {
	a.lay = a.layMem.metrics(layoutIn{
		winW: winW, sidebarW: a.sidebarW, inspW: a.inspW,
		inspector: a.inspectorOpen && a.tool == "", barRight: c.TitleBar().Right,
	})
	a.split(c, &a.fxSide, false, &a.sidebarW, func() { a.sidebar(c) }, func() { a.mainColumn(c) })
}

// mainColumn is everything right of the sidebar: the full-width top bar, and
// below it the center column next to the inspector.
func (a *app) mainColumn(c *ui.Context) {
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
		a.topBar(c)
		if a.lay.inspW > 0 {
			center := a.lay.centerW
			if a.split(c, &a.fxInsp, false, &center, func() { a.mainArea(c) }, func() { a.inspector(c) }) {
				a.inspW = clampF(a.lay.barW-dividerW-center, 260, 520)
			}
			return
		}
		ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.mainArea(c) })
	})
}

// mainArea is the path bar and object table, or a tool view.
func (a *app) mainArea(c *ui.Context) {
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
		if a.tool != "" {
			a.toolView(c)
			return
		}
		a.pathBar(c)
		a.objects(c)
	})
}

func clampF(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }
