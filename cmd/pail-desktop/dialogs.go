package main

import (
	"slices"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

func (a *app) dialogs(c *ui.Context) {
	a.mgFrame(c)
	a.deleteDialog(c)
	a.folderDialog(c)
	a.accountDialog(c)
	a.bucketDialogs(c) // under the confirmation, which a bucket dialog can open
	a.objOpsDialogs(c)
	a.confirmDialog(c)
	a.syncDiscardDialog(c)
	a.vaultDialogs(c) // so the unlock dialog is above whatever asked for it
	a.keysDialog(c)
	a.aboutDialog(c)
	a.paletteDialog(c) // last: the palette is above everything
}

// Dialog geometry (DESIGN.md "Scrolling"): the card is at most the window
// height minus dialogMargin; the title and the footer never scroll; the body
// scrolls between them with its scroll bar in a right-hand gutter.
const (
	dialogMargin = 64
	dialogPad    = 20
	// scrollGutter is the room the body keeps on its right: the bar (6) plus 6.
	scrollGutter = 12
	// scrollBarInset moves the bar in from the card edge, into that gutter.
	scrollBarInset = 2
)

// dialogScrolls remembers, per dialog, whether its body overflowed in the
// previous frame, which decides whether the dividers show.
var dialogScrolls = map[string]*ui.ScrollState{}

// dialog shows one 440px card over the Scrim backdrop: a fixed title, a
// scrolling body and a fixed footer.
func dialog(c *ui.Context, open *bool, title string, body, footer func()) {
	dialogW(c, open, title, 440, body, footer)
}

// dialogW is dialog with a card width of 440 to 520.
//
// The footer holds the buttons (buttonRow) and any result line; it may be nil
// for a dialog that has none. Both dividers are 1px Border lines and appear
// only while the body can scroll; otherwise they are transparent so the layout
// does not jump.
func dialogW(c *ui.Context, open *bool, title string, width float32, body, footer func()) {
	k := tk(c)
	_, winH := c.Size()
	if body == nil {
		body = func() {}
	}
	st := dialogScrolls[title]
	if st == nil {
		st = &ui.ScrollState{}
		dialogScrolls[title] = st
	}
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.Scrim)
		dialogFrame(c, panel, width, winH)
		dialogHeader(c, title, nil)
		overflow := st.MaxY > 0
		divider(c, overflow)
		ui.Scroll(c).TrackScroll(st).Shrink(1).MinHeight(0).ScrollbarInsets(0, scrollBarInset, 0, 0).Children(func() {
			ui.Column(c).Padding(8, dialogPad, 4, dialogPad).Gap(16).Children(body)
		})
		divider(c, overflow)
		dialogFooter(c, footer)
	})
}

// dialogFrame styles the card: width, no padding (the parts pad themselves),
// a height cap of the window minus dialogMargin, and the overlayDialog
// surface (radius 10, shadow or outline from the tokens).
func dialogFrame(c *ui.Context, panel ui.Element, width, winH float32) {
	panel.Width(width).MaxHeight(max(240, winH-dialogMargin)).Clip()
	overlayPanel(c, panel, overlayDialog)
}

// dialogHeader is the fixed title row of a dialog; extra, when not nil, adds
// quiet text after the title (the bucket name in bucket settings).
func dialogHeader(c *ui.Context, title string, extra func()) {
	ui.Row(c).Padding(dialogPad, dialogPad, 12, dialogPad).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		sectionTitle(c, title)
		if extra != nil {
			extra()
		}
	})
}

// dialogFooter is the fixed bottom row of a dialog (its buttons and any result
// line); a nil footer draws nothing.
func dialogFooter(c *ui.Context, footer func()) {
	if footer != nil {
		ui.Column(c).Padding(12, dialogPad, dialogPad, dialogPad).Gap(8).Shrink(0).Children(footer)
	}
}

// divider is a 1px full-width separator that is only drawn when on.
func divider(c *ui.Context, on bool) {
	d := ui.Box(c).Height(1).Shrink(0)
	if on {
		d.Background(tk(c).Border)
	}
}

// fieldRow is one line of the scope grid: 12px subtle label, 13px value.
func fieldRow(c *ui.Context, label, value string, monospace bool) {
	fieldRowC(c, label, value, monospace, false)
}

// fieldRowC is fieldRow with the value optionally in the Danger color.
func fieldRowC(c *ui.Context, label, value string, monospace, danger bool) {
	k := tk(c)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(sizeSecondary).TextColor(k.TextMuted).Width(64).Shrink(0)
		col := k.Text
		if danger {
			col = k.Danger
		}
		v := ui.Text(c, value).FontSize(sizeBody).TextColor(col).Grow(1).MinWidth(0).SingleLine()
		if monospace {
			mono(v.FontSize(sizeSecondary))
		}
	})
}

// deleteDialog confirms deleting the selection: scope grid, key list,
// summary line and a Danger button.
func (a *app) deleteDialog(c *ui.Context) {
	k := tk(c)
	d := &a.del
	was := d.open
	dialog(c, &d.open, locales.DialogsDeleteTitle(locales.CommonItems(len(d.keys))), func() {
		ui.Column(c).Gap(8).Children(func() {
			fieldRow(c, locales.CommonAccount(), a.account().Name, false)
			fieldRow(c, locales.CommonBucket(), d.loc.Bucket, false)
			prefix := d.loc.Prefix
			if prefix == "" {
				prefix = locales.DialogsBucketRoot()
			}
			fieldRow(c, locales.CommonPrefix(), prefix, d.loc.Prefix != "")
		})
		ui.Column(c).Gap(2).Padding(8, 10).Radius(6).Background(k.Sidebar).Children(func() {
			for i, key := range d.keys {
				if i == 5 {
					ui.Text(c, locales.DialogsMore(groupInt(len(d.keys)-5))).FontSize(sizeSecondary).TextColor(k.TextMuted)
					break
				}
				mono(ui.Text(c, key).FontSize(sizeSecondary).TextColor(k.Text).SingleLine())
			}
		})
		ui.Column(c).Gap(2).Children(func() {
			row := ui.Row(c).Gap(8).AlignItems(ui.Center)
			row.Children(func() {
				if d.state == delCounting {
					ui.Spinner(c).Size(12, 12)
				}
				ui.Text(c, d.summary()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
			})
			if d.state == delFailed {
				mutedText(c, d.err)
			}
			mutedText(c, locales.CommonUndoneWarning())
		})
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				a.closeDelete()
			}
			if dangerButton(c, locales.DialogsDeleteButton(locales.CommonItems(len(d.keys))), d.state == delCounting).Clicked() {
				a.confirmDelete()
			}
		})
	})
	if was && !d.open { // closed by Escape or a click outside
		a.closeDelete()
	}
}

// folderDialog creates a folder with inline validation.
func (a *app) folderDialog(c *ui.Context) {
	k := tk(c)
	dialog(c, &a.newFolder, locales.DialogsNewFolder(), func() {
		fieldRow(c, locales.DialogsIn(), a.scope.Bucket+"/"+a.scope.Prefix, true)
		name := strings.TrimSpace(a.folderDlg.name)
		errText := ""
		if name != "" {
			errText = a.folderError(name)
		}
		if errText == "" {
			errText = a.folderDlg.err
		}
		ui.Column(c).Gap(4).Children(func() {
			box := ui.Row(c).Height(28).Padding(0, 8).Radius(6).AlignItems(ui.Center).Background(k.Surface)
			switch {
			case errText != "":
				box.Border(1, k.Danger)
			case box.FocusWithin():
				box.Border(1, k.Focus)
			default:
				box.Border(1, k.BorderStrong)
			}
			box.Children(func() {
				in := ui.TextInputBase(c, &a.folderDlg.name).Placeholder(locales.DialogsFolderName()).AutoFocus().Label(locales.DialogsFolderName()).FontSize(sizeBody).Grow(1).MinWidth(0)
				if in.Changed() {
					a.folderDlg.err = ""
				}
				if in.Submitted() {
					a.confirmNewFolder()
				}
			})
			ui.Text(c, errText).FontSize(sizeSecondary).TextColor(k.Danger).Height(16)
		})
	}, func() {
		name := strings.TrimSpace(a.folderDlg.name)
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				a.newFolder = false
			}
			if primaryButton(c, locales.CommonCreate(), a.folderError(name) != "" || a.folderDlg.busy).Clicked() {
				a.confirmNewFolder()
			}
		})
	})
}

// confirmDialog is the generic confirmation: scope grid, a message and a
// Danger (or primary) button.
func (a *app) confirmDialog(c *ui.Context) {
	k := tk(c)
	cf := &a.confirm
	dialog(c, &cf.open, cf.title, func() {
		if len(cf.rows) > 0 {
			ui.Column(c).Gap(8).Children(func() {
				for _, r := range cf.rows {
					fieldRowC(c, r.Key, r.Value, false, slices.Contains(cf.dangerRows, r.Key))
				}
			})
		}
		if cf.message != "" {
			ui.Text(c, cf.message).FontSize(sizeSecondary).TextColor(k.TextMuted)
		}
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				cf.open = false
			}
			var b ui.Element
			if cf.danger {
				b = dangerButton(c, cf.action, false)
			} else {
				b = primaryButton(c, cf.action, false)
			}
			if b.Clicked() {
				run := cf.run
				cf.open = false
				if run != nil {
					run()
				}
			}
		})
	})
}
