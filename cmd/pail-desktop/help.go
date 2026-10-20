package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// The "?" help icon (DESIGN.md "文案与本地化"): a 14px circle-help icon at the
// right of a label that is not self-explanatory. TextSubtle at rest, TextMuted
// on hover and keyboard focus; the tooltip shows on hover and on keyboard
// focus, and the help text is the control's accessible label, so a screen
// reader reads it when the icon is reached.

const (
	helpIconSize = 14
	helpHitSize  = 20 // the clickable box around the icon
)

// helpIcon is the "?" for text (1–2 sentences, already localized). It is a
// focusable button without an action: Tab reaches it and shows the tip.
func helpIcon(c *ui.Context, text string) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c).Label(text).Tooltip(text).Size(helpHitSize, helpHitSize).Radius(helpHitSize / 2).Center().Shrink(0).
		Cursor(ui.CursorDefault)
	col := k.TextSubtle
	if b.Hovered() || b.FocusVisible() {
		col = k.TextMuted
	}
	return b.Children(func() {
		ui.Icon(c, icons.Get("circle-help")).FontSize(helpIconSize).TextColor(col)
	})
}

// labelHelp is a caption followed by its "?"; label draws the caption and
// help is the tip ("" draws the caption alone).
func labelHelp(c *ui.Context, help string, label func() ui.Element) {
	if help == "" {
		label()
		return
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		label()
		helpIcon(c, help)
	})
}

// fieldLabelHelp is fieldLabel with a "?" when help is not empty.
func fieldLabelHelp(c *ui.Context, label, help string) {
	labelHelp(c, help, func() ui.Element { return fieldLabel(c, label) })
}

// formFieldHelp is formField with a "?" next to the label.
func formFieldHelp(c *ui.Context, label, help, err string, control func()) {
	ui.Column(c.Key("field:" + label)).Gap(4).Children(func() {
		fieldLabelHelp(c, label, help)
		control()
		if err != "" {
			ui.Text(c, err).FontSize(sizeSecondary).TextColor(tk(c).Danger)
		}
	})
}

// switchRowHelp is switchRow with a "?" after the label.
func switchRowHelp(c *ui.Context, label, help string, on *bool) bool {
	k := tk(c)
	var changed bool
	ui.Row(c.Key("switch:" + label)).Height(28).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Row(c).Gap(2).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).SingleLine().Shrink(1).MinWidth(0)
			if help != "" {
				helpIcon(c, help)
			}
		})
		changed = switchControl(c, on, label).Changed()
	})
	return changed
}

// checkboxRowHelp is a checkbox with a "?" after its label.
func checkboxRowHelp(c *ui.Context, checked *bool, label, help string) ui.Element {
	var cb ui.Element
	ui.Row(c.Key("cbh:" + label)).Gap(2).AlignItems(ui.Center).Children(func() {
		cb = checkboxControl(c, checked, label)
		if help != "" {
			helpIcon(c, help)
		}
	})
	return cb
}
