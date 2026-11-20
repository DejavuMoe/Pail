package main

import (
	"sync/atomic"

	"github.com/egoist/mygo/ui"
)

// tokens is a set of design tokens (docs/dev/design.md): the colours of one
// theme. Pail has one palette per theme: Paper for the light theme (lightTokens)
// and Graphite for the dark one (darkTokens). This file is the only place in
// cmd/pail-desktop that names colors; views read the ones their window shows through tk or the helpers
// below.
//
// Color discipline: neutrals carry the interface. The accent appears only in
// selection, focus, progress / active indicators and folder icons; Danger
// only on destructive actions. Icons are otherwise monochrome.
//
// State discipline (DESIGN.md "States"): a selected or "on" state never relies
// on a fill alone. Neutral fills differ by too little (about 1.2:1), so every
// on-state adds a second cue: a raised segment with a border, an Accent icon,
// the Selection hue, or a heavier weight with Text instead of TextMuted.
// theme_test.go enforces the contrast floors for both palettes.
type tokens struct {
	Background, Sidebar, Surface         ui.Color // canvas, source list, inputs and buttons
	Card                                 ui.Color // a group of rows set on the canvas (Settings); controls on it stay Surface
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
// menus, toasts), 10 dialogs; 4 for the small keycap. A shape nested close
// inside a rounded one is concentric: its radius is the outer radius less the
// inset, so the two curves run parallel (RadiusRow in a popover).
const (
	RadiusControl = 6
	RadiusPopover = 8
	RadiusDialog  = 10
	RadiusKeycap  = 4

	// popoverPad is the inset of the rows of a popover (menu, select popup,
	// recent operations), and RadiusRow the radius of those rows: concentric
	// with the panel. The command palette's rows take it too.
	popoverPad = 4
	RadiusRow  = RadiusPopover - popoverPad

	// Small shapes that are not a control: badges, the check box, a drop-target
	// highlight (4); the progress bar (2); the About icon (12). Views name them
	// here instead of writing a number. A 2px indicator line is square.
	RadiusSmall = 4
	RadiusBar   = 2
	RadiusAbout = 12
)

// controlH is the height of a control: input, select, button, segmented track.
const controlH = 28

// Paper and Graphite are Pail's palettes: warm paper whites under a graphite
// ink, and the same graphite turned over for the dark theme. Their accent is
// the steel blue of the icon.
var lightTokens = tokens{
	Background: ui.Hex("#FBFAF8"), Sidebar: ui.Hex("#F3F1EC"), Surface: ui.Hex("#FFFFFF"), Card: ui.Hex("#FFFFFF"),
	SurfaceHover: ui.Hex("#EFEDE8"), SurfacePressed: ui.Hex("#E5E3DE"), ActiveFill: ui.Hex("#E1DFD9"),
	Track: ui.Hex("#E9E7E2"), Raised: ui.Hex("#FFFFFF"), ControlOff: ui.Hex("#B6B3AD"),
	Border: ui.Hex("#E6E4E0"), BorderStrong: ui.Hex("#C2BFB9"),
	Text: ui.Hex("#1D1C1A"), TextMuted: ui.Hex("#65615A"), TextSubtle: ui.Hex("#857F75"),
	Accent: ui.Hex("#34598F"), AccentHover: ui.Hex("#2B4B79"), AccentPressed: ui.Hex("#243F66"),
	AccentText: ui.Hex("#FFFFFF"),
	Selection:  ui.Hex("#D6E1EF"), Focus: ui.Hex("#5C82BA"), FolderIcon: ui.Hex("#6F8BB3"),
	Danger: ui.Hex("#A93A29"), DangerText: ui.Hex("#FFFFFF"),
	Warning: ui.Hex("#A0691A"), Success: ui.Hex("#3B7653"),
	Scrim: ui.Hex("#141311").Alpha(0.32), ScrimSoft: ui.Hex("#141311").Alpha(0.12),
	// A shadow on paper is warm, as the paper is.
	ShadowPopover: ui.Hex("#2E2413").Alpha(0.16), ShadowDialog: ui.Hex("#2E2413").Alpha(0.22),
	Inverse: ui.Hex("#2A2926"), InverseText: ui.Hex("#F5F3EE"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

var darkTokens = tokens{
	Background: ui.Hex("#1C1C1A"), Sidebar: ui.Hex("#151412"), Surface: ui.Hex("#252422"), Card: ui.Hex("#21201E"),
	SurfaceHover: ui.Hex("#2D2D2A"), SurfacePressed: ui.Hex("#353431"), ActiveFill: ui.Hex("#373734"),
	Track: ui.Hex("#151412"), Raised: ui.Hex("#3F3E3B"), ControlOff: ui.Hex("#57554F"),
	Border: ui.Hex("#33322F"), BorderStrong: ui.Hex("#51504B"),
	Text: ui.Hex("#EAE8E3"), TextMuted: ui.Hex("#A6A29A"), TextSubtle: ui.Hex("#85817A"),
	Accent: ui.Hex("#86A8D8"), AccentHover: ui.Hex("#9DBAE3"), AccentPressed: ui.Hex("#7096CB"),
	AccentText: ui.Hex("#12161C"),
	Selection:  ui.Hex("#2A3747"), Focus: ui.Hex("#86A8D8"), FolderIcon: ui.Hex("#7E9BC2"),
	Danger: ui.Hex("#EA8F7B"), DangerText: ui.Hex("#1A1A18"),
	Warning: ui.Hex("#D7A955"), Success: ui.Hex("#82BE98"),
	Scrim: ui.Hex("#000000").Alpha(0.5), ScrimSoft: ui.Hex("#000000").Alpha(0.28),
	ShadowPopover: ui.Transparent, ShadowDialog: ui.Transparent,
	Inverse: ui.Hex("#EAE8E3"), InverseText: ui.Hex("#1A1A18"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

// tokensFor returns the palette of a theme.
func tokensFor(dark bool) tokens {
	if dark {
		return darkTokens
	}
	return lightTokens
}

// each calls fn with every colour of k, in the order of the fields.
// TestEachTokenIsListed keeps the list whole.
func (k *tokens) each(fn func(i int, c *ui.Color)) {
	for i, c := range [...]*ui.Color{
		&k.Background, &k.Sidebar, &k.Surface, &k.Card, &k.SurfaceHover, &k.SurfacePressed, &k.ActiveFill,
		&k.Track, &k.Raised, &k.ControlOff, &k.Border, &k.BorderStrong, &k.Text, &k.TextMuted, &k.TextSubtle,
		&k.Accent, &k.AccentHover, &k.AccentPressed, &k.AccentText, &k.Selection, &k.Focus, &k.FolderIcon,
		&k.Danger, &k.DangerText, &k.Warning, &k.Success, &k.Scrim, &k.ScrimSoft,
		&k.ShadowPopover, &k.ShadowDialog, &k.Inverse, &k.InverseText, &k.Knob, &k.KnobShadow,
	} {
		fn(i, c)
	}
}

// Type scale: 13 body, 12 secondary, 11 section labels, 15 titles.
// Weights are 400 / 500 / 600 only.
const (
	sizeBody      = 13
	sizeSecondary = 12
	sizeLabel     = 11
	sizeTitle     = 15
)

// frame is the colours a window shows in the frame being built. A window keeps
// one for as long as it is open (frameOf): applyTheme fills it at the start
// of every frame and tk reads it for the rest of that frame.
type frame struct {
	c   *ui.Context
	k   tokens
	set bool   // applyTheme has run for the window
	n   uint64 // how many builds it has started: arrive tells from it what the build before drew
}

// lastFrame is the frame tk asked for last: the only one there is outside the
// tests, where every window of a parallel test has its own.
var lastFrame atomic.Pointer[frame]

type frameKey struct{}

// Close forgets the frame as its window closes (ui.Local closes what it owns).
func (f *frame) Close() error {
	lastFrame.CompareAndSwap(f, nil)
	return nil
}

// frameOf returns the frame of c's window.
func frameOf(c *ui.Context) *frame {
	if f := lastFrame.Load(); f != nil && f.c == c {
		return f
	}
	f := *ui.Local(c.Root(), frameKey{}, func() *frame { return &frame{c: c} })
	lastFrame.Store(f)
	return f
}

// applyTheme sets the frame's ui.Theme and tokens. mode is "system", "light"
// or "dark"; "system" follows the desktop. The theme is rebuilt on every frame,
// so a change of the system's appearance is picked up at once; the colours of
// the other mode come in over a moment (easeTokens).
func applyTheme(c *ui.Context, mode string) tokens {
	isDark := c.Theme().Dark
	switch mode {
	case "light":
		isDark = false
	case "dark":
		isDark = true
	}
	k := easeTokens(c, tokensFor(isDark))
	f := frameOf(c)
	f.k, f.set = k, true
	f.n++
	t := *ui.LightTheme()
	if isDark {
		t = *ui.DarkTheme()
	}
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = k.Background, k.Surface, k.SurfaceHover, k.SurfacePressed
	t.Border, t.Text, t.TextMuted = k.Border, k.Text, k.TextMuted
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = k.Accent, k.AccentHover, k.AccentPressed, k.AccentText
	t.InactiveAccent = k.Accent // keep the accent in an inactive window
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

// tk returns the tokens of the frame: those applyTheme set for the window, or
// the palette of the frame's theme in a window it has not run for.
func tk(c *ui.Context) tokens {
	if f := frameOf(c); f.set {
		return f.k
	}
	return tokensFor(c.Theme().Dark)
}
