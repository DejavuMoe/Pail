package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The shared controls every view builds from: buttons, labels, banners, wells
// and search boxes. Their colours and radii come from theme.go.

// errorBanner is the inline failure strip above a list or a tool body: a
// Danger wash with a Danger hairline, 12px outside, 8/12 inside, an alert icon,
// the message and a Retry button. margin overrides the outside margin (top,
// right, bottom, left) when it has four values.
func errorBanner(c *ui.Context, margin []float32, msg string, retryOff bool, retry func()) {
	k := tk(c)
	r := ui.Row(c).Margin(12).Padding(8, 12).Gap(10).AlignItems(ui.Center).Radius(RadiusControl).Shrink(0)
	if len(margin) == 4 {
		r.Margin(margin[0], margin[1], margin[2], margin[3])
	}
	r.Background(k.Danger.Alpha(0.10)).Border(1, k.Danger.Alpha(0.35)).Children(func() {
		ui.Icon(c, icons.Get("circle-alert")).TextColor(k.Danger)
		ui.Text(c, msg).Grow(1).MinWidth(0).TextColor(k.Text)
		secondaryButton(c, locales.CommonRetry(), retryOff).OnClick(retry)
	})
}

// sectionTitle is a 15px region heading.
func sectionTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(sizeTitle).FontWeight(600).SingleLine()
}

// sectionLabel is an 11px source-list group label ("BUCKETS", "TOOLS").
func sectionLabel(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(sizeLabel).FontWeight(600).TextColor(tk(c).TextSubtle).SingleLine()
}

// mutedText is 12px secondary text.
func mutedText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(sizeSecondary).TextColor(tk(c).TextMuted)
}

// subtleText is 12px tertiary text (regions, timestamps, hints).
func subtleText(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(sizeSecondary).TextColor(tk(c).TextSubtle)
}

// loadingLine is a 14px Spinner followed by muted text ("Loading…").
func loadingLine(c *ui.Context, s string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Size(14, 14)
		mutedText(c, s)
	})
}

// mono switches an element to the monospace family (keys, ETags, version ids).
func mono(e ui.Element) ui.Element { return e.Font("monospace") }

// iconButton is a 24x24 borderless icon button with hover and pressed
// states, for places outside a ui.Toolbar (rows, headers).
func iconButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	return iconButtonOff(c, icon, label, false)
}

// iconButtonOff is iconButton that can be disabled: 0.4 opacity and no hover.
func iconButtonOff(c *ui.Context, icon *ui.SVG, label string, disabled bool) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(24, 24).Radius(RadiusControl).Center().Disabled(disabled)
	switch {
	case disabled:
		b.Opacity(0.4)
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(k.TextMuted) })
}

// hairline is a 1px separator in the border color.
func hairline(c *ui.Context) ui.Element {
	return ui.Box(c).Height(1).Background(tk(c).Border)
}

// vline is a 1px vertical separator of a given height.
func vline(c *ui.Context, h float32) ui.Element {
	return ui.Box(c).Width(1).Height(h).Background(tk(c).Border)
}

// ghostButton is the toolbar-style button: no fill, no border, 28px high,
// a muted 14px icon and a 13px label. icon or label may be empty; a label
// less button is square. Hover and pressed use the neutral surfaces.
func ghostButton(c *ui.Context, icon *ui.SVG, label, tip string, disabled bool) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Radius(RadiusControl).Height(28).Gap(6).Center().Disabled(disabled)
	if label == "" {
		b.Width(28)
	} else {
		b.Padding(0, 8)
	}
	switch {
	case disabled:
		b.Opacity(0.4)
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	return b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(14).TextColor(k.TextMuted)
		}
		if label != "" {
			ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).SingleLine()
		}
	})
}

// toggleIconButton is an icon-only panel toggle: ActiveFill and a Text icon
// while on, a muted icon otherwise.
func toggleIconButton(c *ui.Context, icon *ui.SVG, label, tip string, on bool) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Size(28, 28).Radius(RadiusControl).Center()
	switch {
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case on:
		b.Background(k.ActiveFill)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	if on {
		b.Border(1, k.BorderStrong) // the second cue: a fill alone is about 1.2:1
	}
	return b.Children(func() {
		col := k.TextMuted
		if on {
			col = k.Text
		}
		ui.Icon(c, icon).FontSize(14).TextColor(col)
	})
}

// buttonShell is the shared 28px, radius 6 frame of the outlined and solid
// buttons.
func buttonShell(c *ui.Context, label string, disabled bool) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(28).Padding(0, 12).Radius(RadiusControl).Center().Disabled(disabled)
	if disabled {
		b.Opacity(0.4)
	}
	return b
}

func buttonLabel(c *ui.Context, label string, col ui.Color) {
	ui.Text(c, label).FontSize(sizeBody).FontWeight(500).TextColor(col).SingleLine()
}

// secondaryButton is the outlined button: Surface fill, BorderStrong outline.
func secondaryButton(c *ui.Context, label string, disabled bool) ui.Element {
	k := tk(c)
	b := buttonShell(c, label, disabled).Border(1, k.BorderStrong)
	switch {
	case !disabled && b.Pressed():
		b.Background(k.SurfacePressed)
	case !disabled && b.Hovered():
		b.Background(k.SurfaceHover)
	default:
		b.Background(k.Surface)
	}
	return b.Children(func() { buttonLabel(c, label, k.Text) })
}

// primaryButton is the solid Accent button.
func primaryButton(c *ui.Context, label string, disabled bool) ui.Element {
	k := tk(c)
	b := buttonShell(c, label, disabled)
	switch {
	case !disabled && b.Pressed():
		b.Background(k.AccentPressed)
	case !disabled && b.Hovered():
		b.Background(k.AccentHover)
	default:
		b.Background(k.Accent)
	}
	return b.Children(func() { buttonLabel(c, label, k.AccentText) })
}

// dangerButton is the solid Danger button for destructive confirmations.
func dangerButton(c *ui.Context, label string, disabled bool) ui.Element {
	k := tk(c)
	b := buttonShell(c, label, disabled)
	switch {
	case !disabled && b.Pressed():
		b.Background(k.Danger.Alpha(0.8))
	case !disabled && b.Hovered():
		b.Background(k.Danger.Alpha(0.9))
	default:
		b.Background(k.Danger)
	}
	return b.Children(func() { buttonLabel(c, label, k.DangerText) })
}

// dangerOutlineButton is the outlined button of a destructive action that
// opens a confirmation: Danger text and outline on Surface. It is not a solid
// button, so it can sit beside the page's one solid button.
func dangerOutlineButton(c *ui.Context, label string, disabled bool) ui.Element {
	k := tk(c)
	b := buttonShell(c, label, disabled).Border(1, k.Danger)
	switch {
	case !disabled && b.Pressed():
		b.Background(k.SurfacePressed)
	case !disabled && b.Hovered():
		b.Background(k.SurfaceHover)
	default:
		b.Background(k.Surface)
	}
	return b.Children(func() { buttonLabel(c, label, k.Danger) })
}

// tabIndicator is the 2px Accent underline pinned to the bottom of tab, the
// current one of its strip, inside which it is built. It comes from the tab
// that was current before (arrive); g is the strip's mark and id tells its
// tabs apart.
func tabIndicator(c *ui.Context, g *markState, id any, tab ui.Element) {
	arrive(c, g, id, tab, func(m ui.Element) {
		m.Children(func() { ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(2).Background(tk(c).Accent) })
	})
}

// currentFill is the fill of the current row of a list (the sidebar, a list of
// sections), built inside row as its first child: ActiveFill, which comes from
// the row that was current before (arrive). The row goes under its neighbours
// and the fill under the row's own content, so that it passes under every
// label on its way.
func currentFill(c *ui.Context, g *markState, id any, row ui.Element) {
	row.ZIndex(-1)
	arrive(c, g, id, row, func(m ui.Element) { m.ZIndex(-1).Radius(RadiusControl).Background(tk(c).ActiveFill) })
}

// well styles el as a block of read-only content set into a surface (a list
// of keys, a URL, a preview): the surface's own fill and a 1px Border outline
// at RadiusControl. It never takes the Sidebar fill: a second neutral inside a
// card splits it into two tones.
func well(c *ui.Context, el ui.Element) ui.Element {
	return el.Radius(RadiusControl).Border(1, tk(c).Border)
}

// searchBox is a 28px text field: Surface fill, BorderStrong outline (Focus
// when the text has the keyboard), a leading icon and an inline clear
// button. It returns the input element for Changed / Submitted.
func searchBox(c *ui.Context, value *string, h *ui.Handle, label, placeholder string, width float32, icon *ui.SVG, clearIcon *ui.SVG) ui.Element {
	return searchBoxFit(c, value, h, label, placeholder, width, false, icon, clearIcon)
}

// searchBoxFit is searchBox that can keep its width: with fixed, the box does
// not give way to its neighbours (the filter someone just asked for).
func searchBoxFit(c *ui.Context, value *string, h *ui.Handle, label, placeholder string, width float32, fixed bool, icon *ui.SVG, clearIcon *ui.SVG) ui.Element {
	k := tk(c)
	row := ui.Row(c).Height(controlH).Padding(0, 4, 0, 8).Gap(6).Radius(RadiusControl).AlignItems(ui.Center).Background(k.Surface)
	switch {
	case width > 0 && fixed:
		row.Width(width).Shrink(0)
	case width > 0:
		// A preferred width that gives way in a narrow window.
		row.Basis(width).Shrink(1).MinWidth(min(width, 120))
	}
	if row.FocusWithin() {
		row.Border(1, k.Focus)
	} else {
		row.Border(1, k.BorderStrong)
	}
	var in ui.Element
	row.Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(k.TextMuted)
		in = ui.TextInputBase(c, value).Bind(h).Label(label).Placeholder(placeholder).FontSize(sizeBody).Grow(1).MinWidth(0)
		if *value != "" {
			if iconButton(c, clearIcon, locales.ThemeClear()).Size(20, 20).Clicked() {
				*value = ""
				in.SetChanged()
			}
		}
	})
	return in
}
