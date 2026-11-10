package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// selBarH is the height of the selection bar.
const selBarH = 36

// selBarCommands are the commands the selection bar offers, before its "more"
// menu, which is the table's context menu.
var selBarCommands = []string{"download", "copy", "cut", "delete"}

// selectionBar is the 36px bar under the object table while two or more rows
// are chosen (DESIGN.md "选择条"): what is chosen, the commands that act on it
// and Clear selection. One chosen row has the toolbar and the inspector, so
// a click on a row never changes the table's height. The bar shows and goes in
// the frame the choice changes; the table above only gets shorter, so the rows
// it shows stay where they are.
func (a *app) selectionBar(c *ui.Context) {
	k := tk(c)
	folders, files, bytes := a.selectedInfo()
	n := folders + files
	if n < 2 {
		return
	}
	// The commands show their names while the table has room for all its
	// columns; narrower, their icons alone, with the names as tooltips.
	named := a.lay.colTier == 0
	ui.Row(c.Key("selbar")).Height(selBarH).Shrink(0).MinWidth(0).Padding(0, 12).Gap(6).AlignItems(ui.Center).
		Background(k.Background).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Label(locales.SelbarLabel()).Children(func() {
		dot := func() { ui.Text(c, "·").FontSize(sizeSecondary).TextColor(k.TextSubtle).Shrink(0) }
		// The summary gives way to the buttons: its parts shorten with an
		// ellipsis, the buttons keep their size.
		ui.Row(c).Gap(6).Shrink(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("n"), locales.SelbarSelected(n)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine().Shrink(1).MinWidth(0)
			dot()
			ui.Text(c.Key("size"), humanBytes(bytes)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine().Shrink(1).MinWidth(0)
			if folders > 0 {
				dot()
				mutedText(c.Key("folders"), locales.SelbarFolders(folders)).SingleLine().Shrink(1).MinWidth(0)
			}
			if a.loading {
				dot()
				subtleText(c.Key("loading"), locales.SelbarLoading()).SingleLine().Shrink(1).MinWidth(0)
			}
		})
		ui.Box(c).Width(6).Shrink(0)
		for _, id := range selBarCommands {
			a.commandButton(c, id, named, "selbar").Shrink(0)
		}
		more := ghostButton(c, icons.Get("ellipsis"), "", locales.MenuObjectActions(), false).Label(locales.MenuObjectActions()).Shrink(0)
		a.buttonMenu(c, more, "selbar", locales.MenuObjectActions(), a.buildMenu)
		ui.Spacer(c)
		a.commandButton(c, "selectnone", named, "selbar").Shrink(0)
	})
}
