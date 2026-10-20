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
	ui.Row(c).Height(statusBarH).Padding(0, 12).Gap(6).AlignItems(ui.Center).
		Background(k.Sidebar).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		part := func(s string) { ui.Text(c, s).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine() }
		// What counts (items, selected, running transfers) rolls its digits as
		// it changes; the keys keep each one's identity as the parts around it
		// come and go.
		count := func(key, s string) {
			ui.Text(c.Key(key), s).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().ContentTransition(countRoll)
		}
		dot := func() { ui.Text(c, "·").FontSize(sizeSecondary).TextColor(k.TextSubtle) }
		if ac := a.account(); ac.ID != "" {
			part(ac.Name)
		} else {
			part(locales.StatusbarNoAccount())
		}
		if a.tool == "" {
			a.statusBrowse(part, count, dot)
		}
		ui.Spacer(c)
		a.lockIndicator(c)
		if !a.hasAccount() && len(a.tasks) == 0 {
			return
		}
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
}

func (a *app) statusBrowse(part func(string), count func(key, s string), dot func()) {
	b, ok := a.bucket()
	if !ok {
		return
	}
	if b.Region != "" {
		dot()
		part(b.Region)
	}
	dot()
	items := locales.CommonItems(len(a.rows))
	if a.search != "" {
		items = locales.StatusbarItemsOf(len(a.rows), groupInt(len(a.raw)))
	}
	if a.loading {
		items = locales.StatusbarLoading(items)
	}
	count("items", items)
	if n := a.sel.Len(); n > 0 {
		_, _, bytes := a.selectedInfo()
		dot()
		count("selected", locales.StatusbarSelected(fmt.Sprint(n), humanBytes(bytes)))
	}
}
