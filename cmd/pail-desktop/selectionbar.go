package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// selBarH is the height of the selection bar.
const selBarH = 36

// selBarSummary is what the selection bar says about the choice.
type selBarSummary struct {
	n, folders int
	bytes      int64 // of the objects, not the folders
	loading    bool  // the listing has more pages to come
}

// selBarCommands are the commands the selection bar offers, before its "more"
// menu, which is the table's context menu.
var selBarCommands = []string{"download", "copy", "cut", "delete"}

// selectionBar is the 36px bar under the object table while two or more rows
// are chosen (DESIGN.md "选择条"): what is chosen, the commands that act on it
// and Clear selection. One chosen row has the toolbar and the inspector, so
// a click on a row never changes the table's height. The bar's height goes
// between 0 and 36 over durStandard as it comes and goes, and its content
// fades with it; folding away, it keeps showing what it showed last. The
// table above only gets shorter: the rows it shows stay where they are.
func (a *app) selectionBar(c *ui.Context) {
	k := tk(c)
	folders, files, bytes := a.selectedInfo()
	up := folders+files >= 2
	if up {
		a.selBar = selBarSummary{n: folders + files, folders: folders, bytes: bytes, loading: a.loading}
	}
	want := float32(0)
	if up {
		want = selBarH
	}
	bar := ui.Column(c.Key("selbar")).Shrink(0).MinWidth(0).Clip()
	h := bar.AnimateTo("h", want, fadeAnim)
	bar.Height(h)
	if h <= 0 {
		return
	}
	bar.Background(k.Background).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Label(locales.SelbarLabel())
	s := a.selBar
	// The commands show their names while the table has room for all its
	// columns; narrower, their icons alone, with the names as tooltips.
	named := a.lay.colTier == 0
	bar.Children(func() {
		row := ui.Row(c).Height(selBarH).Shrink(0).Padding(0, 12).Gap(6).MinWidth(0).AlignItems(ui.Center)
		if up {
			fadeIn(c, row)
		} else {
			row.AnimatePose(ui.Pose{Fade: 1}, fadeAnim)
		}
		row.Children(func() {
			dot := func() { ui.Text(c, "·").FontSize(sizeSecondary).TextColor(k.TextSubtle).Shrink(0) }
			// The summary gives way to the buttons: its parts shorten with an
			// ellipsis, the buttons keep their size.
			ui.Row(c).Gap(6).Shrink(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("n"), locales.SelbarSelected(s.n)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).
					SingleLine().Shrink(1).MinWidth(0).ContentTransition(countRoll)
				dot()
				ui.Text(c.Key("size"), humanBytes(s.bytes)).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).
					SingleLine().Shrink(1).MinWidth(0).ContentTransition(countRoll)
				if s.folders > 0 {
					dot()
					mutedText(c.Key("folders"), locales.SelbarFolders(s.folders)).SingleLine().Shrink(1).MinWidth(0).ContentTransition(countRoll)
				}
				if s.loading {
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
	})
}
