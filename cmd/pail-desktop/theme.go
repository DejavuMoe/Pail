package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// tokens is the "Graphite & Paper" design-token set (docs/rewrite/DESIGN.md).
// It is the only place in cmd/pail-desktop that names colors; views read them
// through tk or the helpers below.
//
// Color discipline: neutrals carry the interface. The accent appears only in
// selection, focus, progress / active indicators and folder icons; Danger
// only on destructive actions. Icons are otherwise monochrome.
//
// State discipline (DESIGN.md "States"): a selected or "on" state never relies
// on a fill alone. Neutral fills differ by too little (about 1.2:1), so every
// on-state adds a second cue: a raised segment with border and shadow, an
// Accent indicator bar, or a heavier weight with Text instead of TextMuted.
// theme_test.go enforces the contrast floors below.
type tokens struct {
	Background, Sidebar, Surface         ui.Color // canvas, source list, raised cards/inputs
	SurfaceHover, SurfacePressed         ui.Color
	ActiveFill                           ui.Color // selected sidebar item, active toggle (always with a second cue)
	Track, Raised                        ui.Color // segmented-control track and its selected segment
	ControlOff                           ui.Color // switch track while off
	Border, BorderStrong                 ui.Color // hairlines; input and button outlines
	Text, TextMuted, TextSubtle          ui.Color // TextSubtle: placeholders, counts, separators only
	Accent, AccentHover, AccentPressed   ui.Color
	AccentText                           ui.Color
	Selection, Focus, FolderIcon         ui.Color
	Danger, DangerText, Warning, Success ui.Color
	Scrim, ScrimSoft                     ui.Color // modal backdrop; the lighter one behind the command palette
	Shadow                               ui.Color // raised segment shadow

	// Overlay surfaces (docs/dev/design.md "Overlays", overlay.go). A shadow token
	// is the colour of the shadow; its geometry lives in overlay.go. Where the
	// token is transparent (the dark theme, where a shadow is invisible on a
	// dark backdrop) overlayPanel draws a 1px BorderStrong outline instead.
	ShadowPopover, ShadowDialog ui.Color
	// Inverse and InverseText fill and colour tooltips and toasts: the theme
	// turned over, so that they stand out from what they float above.
	Inverse, InverseText ui.Color
	// Knob and KnobShadow are the thumb of a switch and its shadow.
	Knob, KnobShadow ui.Color
}

// Radii (docs/dev/design.md principle 5): 6 controls, 8 panels (select popups,
// menus, toasts), 10 dialogs; 4 for the small keycap.
const (
	RadiusControl = 6
	RadiusPopover = 8
	RadiusDialog  = 10
	RadiusKeycap  = 4
)

var lightTokens = tokens{
	Background: ui.Hex("#FBFAF8"), Sidebar: ui.Hex("#F3F1EC"), Surface: ui.Hex("#FFFFFF"),
	SurfaceHover: ui.Hex("#EFEDE8"), SurfacePressed: ui.Hex("#E6E3DD"), ActiveFill: ui.Hex("#E3DFD6"),
	Track: ui.Hex("#EAE7E0"), Raised: ui.Hex("#FFFFFF"), ControlOff: ui.Hex("#B9B3A7"),
	Border: ui.Hex("#E7E4DE"), BorderStrong: ui.Hex("#C4BFB4"),
	Text: ui.Hex("#1D1C1A"), TextMuted: ui.Hex("#6B675F"), TextSubtle: ui.Hex("#857F75"),
	Accent: ui.Hex("#34598F"), AccentHover: ui.Hex("#2B4B79"), AccentPressed: ui.Hex("#243F66"),
	AccentText: ui.Hex("#FFFFFF"),
	Selection:  ui.Hex("#DCE4EF"), Focus: ui.Hex("#5C82BA"), FolderIcon: ui.Hex("#6F8BB3"),
	Danger: ui.Hex("#B3402E"), DangerText: ui.Hex("#FFFFFF"),
	Warning: ui.Hex("#A0691A"), Success: ui.Hex("#3B7653"),
	Scrim: ui.Hex("#141311").Alpha(0.32), ScrimSoft: ui.Hex("#141311").Alpha(0.12),
	Shadow:        ui.RGBA(20, 19, 17, 0.12),
	ShadowPopover: ui.RGBA(0, 0, 0, 0.14), ShadowDialog: ui.RGBA(0, 0, 0, 0.18),
	Inverse: ui.Hex("#2A2926"), InverseText: ui.Hex("#F5F3EE"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

var darkTokens = tokens{
	Background: ui.Hex("#1A1A18"), Sidebar: ui.Hex("#151513"), Surface: ui.Hex("#21211F"),
	SurfaceHover: ui.Hex("#292926"), SurfacePressed: ui.Hex("#31302D"), ActiveFill: ui.Hex("#33322F"),
	Track: ui.Hex("#121210"), Raised: ui.Hex("#34332F"), ControlOff: ui.Hex("#57554F"),
	Border: ui.Hex("#2E2D2A"), BorderStrong: ui.Hex("#4C4A45"),
	Text: ui.Hex("#EAE8E3"), TextMuted: ui.Hex("#A29E96"), TextSubtle: ui.Hex("#85817A"),
	Accent: ui.Hex("#86A8D8"), AccentHover: ui.Hex("#9DBAE3"), AccentPressed: ui.Hex("#7096CB"),
	AccentText: ui.Hex("#12161C"),
	Selection:  ui.Hex("#2A3747"), Focus: ui.Hex("#86A8D8"), FolderIcon: ui.Hex("#7E9BC2"),
	Danger: ui.Hex("#E0806C"), DangerText: ui.Hex("#1A1A18"),
	Warning: ui.Hex("#D7A955"), Success: ui.Hex("#82BE98"),
	Scrim: ui.Hex("#000000").Alpha(0.5), ScrimSoft: ui.Hex("#000000").Alpha(0.28),
	Shadow:        ui.RGBA(0, 0, 0, 0.4),
	ShadowPopover: ui.Transparent, ShadowDialog: ui.Transparent,
	Inverse: ui.Hex("#EAE8E3"), InverseText: ui.Hex("#1A1A18"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

func tokensFor(dark bool) tokens {
	if dark {
		return darkTokens
	}
	return lightTokens
}

// Type scale: 13 body, 12 secondary, 11 section labels, 15 titles.
// Weights are 400 / 500 / 600 only.
const (
	sizeBody      = 13
	sizeSecondary = 12
	sizeLabel     = 11
	sizeTitle     = 15
)

// applyTheme sets the frame's ui.Theme from the tokens. mode is "system",
// "light" or "dark"; "system" follows the desktop. The theme is rebuilt on
// every frame, so a system appearance change is picked up at once.
func applyTheme(c *ui.Context, mode string) tokens {
	dark := c.Theme().Dark
	switch mode {
	case "light":
		dark = false
	case "dark":
		dark = true
	}
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
	}
	k := tokensFor(dark)
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = k.Background, k.Surface, k.SurfaceHover, k.SurfacePressed
	t.Border, t.Text, t.TextMuted = k.Border, k.Text, k.TextMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = k.Accent, k.AccentHover, k.AccentPressed, k.AccentText
	t.Selection, t.Focus = k.Selection, k.Focus
	t.Danger, t.Warning, t.Success = k.Danger, k.Warning, k.Success
	// Tooltips (mygo's own, see overlay.go) and toasts take these two.
	t.Inverse, t.InverseText = k.Inverse, k.InverseText
	t.Scrollbar = k.TextSubtle.Alpha(0.5)
	t.Radius, t.Spacing = RadiusControl, 4
	t.FontSize = sizeBody * max(1, c.Preferences().TextScale)
	if uiFont != "" {
		t.Font = uiFont
	}
	c.SetTheme(&t)
	return k
}

// tk returns the tokens of the frame's theme.
func tk(c *ui.Context) tokens { return tokensFor(c.Theme().Dark) }

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
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(24, 24).Radius(6).Center().Disabled(disabled).Transition(hoverFade())
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
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Radius(6).Height(28).Gap(6).Center().Disabled(disabled).Transition(hoverFade())
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
	b := ui.ButtonBase(c).Label(label).Tooltip(tip).Size(28, 28).Radius(6).Center().Transition(hoverFade())
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
	b := ui.ButtonBase(c).Label(label).Height(28).Padding(0, 12).Radius(6).Center().Disabled(disabled).Transition(hoverFade())
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

// tabIndicator is the 2px Accent underline pinned to the bottom of its
// parent, which must be a positioned element (a tab or a tab strip item). The
// tabs of one strip share a group: the underline slides from the previous
// tab to the current one (motion.go).
func tabIndicator(c *ui.Context, group string) ui.Element {
	return ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(2).Radius(1).Background(tk(c).Accent).Transition(slideTransition(group))
}

// searchBox is a 28px text field: Surface fill, BorderStrong outline (Focus
// when the text has the keyboard), a leading icon and an inline clear
// button. It returns the input element for Changed / Submitted.
func searchBox(c *ui.Context, value *string, h *ui.Handle, label, placeholder string, width float32, icon *ui.SVG, clearIcon *ui.SVG) ui.Element {
	k := tk(c)
	row := ui.Row(c).Height(28).Padding(0, 4, 0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Background(k.Surface)
	if width > 0 {
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
