package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// Overlays (docs/design.md "Overlays"): everything that floats above the
// window shares one surface style (overlayPanel) and, when it lists choices,
// one row (listRow). The dialogs, the command palette, the select popup, the
// toasts and the coming context menu all go through here, so a radius, a
// shadow or a highlight is decided in one place.

// overlayKind is the kind of surface overlayPanel draws.
type overlayKind int

const (
	// overlayDialog is a modal card: dialogs and the command palette. Radius 10.
	overlayDialog overlayKind = iota
	// overlayPopover is a panel anchored to an element: the select popup and
	// menus. Radius 8.
	overlayPopover
	// overlayTip is the inverse chip of a toast. Radius 8.
	overlayTip
)

// Shadow geometry (x, y, blur, spread). The colour is a token
// (tokens.ShadowPopover, tokens.ShadowDialog); the dark theme has none.
const (
	shadowPopoverY, shadowPopoverBlur = 8, 24
	shadowDialogY, shadowDialogBlur   = 12, 32
)

// overlayPanel styles panel as an overlay surface of the given kind and
// returns it: fill, radius, and the edge. In the light theme the edge is a
// soft shadow (ShadowPopover or ShadowDialog); in the dark theme, where a
// shadow cannot be seen, it is a 1px BorderStrong outline (the shadow token is
// transparent there). Callers add their own size and padding.
//
// A toast (overlayTip) is filled with Inverse in both themes, which already
// stands out from the window, so it gets no outline in the dark theme.
func overlayPanel(c *ui.Context, panel ui.Element, kind overlayKind) ui.Element {
	k := tk(c)
	var (
		radius float32
		shadow ui.Color
		y, blr float32
	)
	switch kind {
	case overlayDialog:
		radius, shadow, y, blr = RadiusDialog, k.ShadowDialog, shadowDialogY, shadowDialogBlur
	default:
		radius, shadow, y, blr = RadiusPopover, k.ShadowPopover, shadowPopoverY, shadowPopoverBlur
	}
	panel.Radius(radius)
	if kind == overlayTip {
		panel.Background(k.Inverse).TextColor(k.InverseText)
	} else {
		panel.Background(k.Surface)
	}
	switch {
	case shadow.A > 0:
		panel.Shadow(0, y, blr, 0, shadow)
	case kind != overlayTip:
		panel.Border(1, k.BorderStrong)
	}
	return panel
}

// listRowH is the height of a row of a list overlay.
const listRowH = 28

// rowSpec is what a listRow shows. Only Label is required.
type rowSpec struct {
	Icon      *ui.SVG // 14px, TextMuted; nil leaves none
	IconSlot  bool    // reserve the icon's room when Icon is nil, so labels of a list line up
	Label     string  // 13px Text
	Secondary string  // 12px TextMuted after the label (a group, a region)
	Shortcut  string  // "Ctrl+Shift+P" or "⌘⇧P": drawn as one keycap per key
	Checked   bool    // the current value: Accent check and weight 600 (two cues)
	Disabled  bool    // opacity 0.4, no hover, not clickable
	// Highlighted is the cursor: the row the keyboard is on or the pointer is
	// over, one look for both (SurfaceHover), as native menus do. It is not the
	// current value (Checked) and does not use Selection.
	Highlighted bool
	Submenu     bool // a trailing chevron: activating the row opens a submenu
}

// listRow styles row as a list row and fills it. row is an element the caller
// made, with its own key and behaviour: a ui.ButtonBase for a command or menu
// item, or sel.Item(value) for a select option. The row is 28px high with 8px
// side padding, radius 6, and from left to right shows the icon, the label,
// the secondary text, the keycaps, the check and the submenu chevron.
func listRow(c *ui.Context, row ui.Element, s rowSpec) ui.Element {
	k := tk(c)
	row.Height(listRowH).Padding(0, 8).Gap(8).Radius(RadiusControl).AlignItems(ui.Center).Shrink(0).Disabled(s.Disabled)
	switch {
	case s.Disabled:
		row.Opacity(0.4)
	case s.Highlighted:
		row.Background(k.SurfaceHover)
	}
	if s.Shortcut != "" {
		row.Description(s.Shortcut)
	}
	return row.Children(func() {
		switch {
		case s.Icon != nil:
			ui.Icon(c, s.Icon).FontSize(14).TextColor(k.TextMuted).Shrink(0)
		case s.IconSlot:
			ui.Box(c).Size(14, 14).Shrink(0)
		}
		w := 400
		if s.Checked {
			w = 600
		}
		ui.Text(c, s.Label).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
		if s.Secondary != "" {
			mutedText(c, s.Secondary).SingleLine().Shrink(1).MinWidth(0)
		}
		for _, key := range shortcutKeys(s.Shortcut) {
			keycap(c, key, s.Highlighted)
		}
		if s.Checked {
			ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(k.Accent).Shrink(0)
		}
		if s.Submenu {
			ui.Icon(c, icons.Get("chevron-right")).FontSize(14).TextColor(k.TextMuted).Shrink(0)
		}
	})
}

// keycap is one key of a shortcut: 12px TextSubtle in a 20px-high box with a
// 1px outline and radius 4. A single character makes it square. on says the
// cap sits on a highlighted row: there the hairline Border would vanish into
// SurfaceHover and TextSubtle falls under its 3.5:1 floor, so the outline
// steps up to BorderStrong and the text to TextMuted.
func keycap(c *ui.Context, label string, on bool) ui.Element {
	k := tk(c)
	edge, ink := k.Border, k.TextSubtle
	if on {
		edge, ink = k.BorderStrong, k.TextMuted
	}
	return ui.Row(c).Height(20).MinWidth(20).Padding(0, 4).Radius(RadiusKeycap).Border(1, edge).Center().Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(sizeSecondary).TextColor(ink).SingleLine()
	})
}

// shortcutKeys splits a shortcut as the command table spells it into its
// keys: "Ctrl+Shift+P" into Ctrl, Shift, P; "⌘⇧P" (macOS) into ⌘, ⇧, P; a
// lone "F5", "Del" or "Backspace" stays whole.
func shortcutKeys(s string) []string {
	if s == "" {
		return nil
	}
	if strings.Contains(s, "+") && s != "+" {
		var keys []string
		for _, p := range strings.Split(s, "+") {
			if p != "" {
				keys = append(keys, p)
			}
		}
		return keys
	}
	var keys []string
	for s != "" {
		r, n := utf8.DecodeRuneInString(s)
		if !strings.ContainsRune("⌘⌥⇧⌃", r) {
			break
		}
		keys, s = append(keys, string(r)), s[n:]
	}
	if s != "" {
		keys = append(keys, s)
	}
	return keys
}

// listSeparator is the 1px Border line between groups of rows, with 4px above
// and below.
func listSeparator(c *ui.Context) ui.Element {
	return ui.Box(c).Height(1).Background(tk(c).Border).Margin(4, 0).Shrink(0)
}

// toastViewport builds the window's toasts in Pail's look in place of mygo's:
// an overlayTip chip at the bottom centre, 13px text, an optional action. It
// keeps mygo's timing, pausing and stacking (the newest three). Call it once
// per frame, after the dialogs, so toasts float above them.
//
// mygo v0.5.5 offers no such hook for tooltips: Element.Tooltip draws its own
// radius (5) and shadow. The tooltip takes the Inverse colours of the theme
// (applyTheme); its radius and shadow stay mygo's.
func toastViewport(c *ui.Context) {
	k := tk(c)
	winW, _ := c.Size()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Bottom(statusBarH + 8).AlignItems(ui.Center).Gap(8)
		for _, t := range toasts {
			toast := ui.ToastBase(c, t)
			toast.Root.Row().AlignItems(ui.Center).Gap(16).Padding(8, 16).MaxWidth(max(240, winW-48))
			overlayPanel(c, toast.Root, overlayTip)
			// The fade that mygo's own toast has: in and out over 200ms.
			toast.Root.Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
			toast.Root.Children(func() {
				if t.Description == "" {
					ui.Text(c, t.Title).FontSize(sizeBody).TextColor(k.InverseText).Shrink(1).MinWidth(0)
				} else {
					ui.Column(c).Shrink(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, t.Title).FontSize(sizeBody).FontWeight(600).TextColor(k.InverseText)
						ui.Text(c, t.Description).FontSize(sizeSecondary).TextColor(k.InverseText.Alpha(0.75))
					})
				}
				if t.Action == "" {
					return
				}
				b := toast.ActionButton().Height(24).Padding(0, 8).Radius(RadiusControl).Center().Shrink(0).Margin(0, -8, 0, 0)
				if b.Hovered() {
					b.Background(k.InverseText.Alpha(0.14))
				}
				b.Children(func() {
					ui.Text(c, t.Action).FontSize(sizeBody).FontWeight(600).TextColor(k.InverseText).SingleLine()
				})
			})
		}
	})
}
