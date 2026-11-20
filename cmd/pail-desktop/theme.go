package main

import (
	"sync/atomic"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// tokens is a set of design tokens (docs/dev/design.md): the colours of one
// palette. Each theme has two palettes to choose from in Settings: Paper and
// Sky for the light theme, Graphite and Night for the dark one (lightPalettes,
// darkPalettes). This file is the only place in cmd/pail-desktop that names
// colors; views read the ones their window shows through tk or the helpers
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
// theme_test.go enforces the contrast floors for every palette.
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

// Paper and Graphite are the palettes Pail starts with: warm paper whites under
// a graphite ink, and the same graphite turned over for the dark theme. Their
// accent is the steel blue of the icon. A palette name the app does not know
// falls back to them.
var lightTokens = tokens{
	Background: ui.Hex("#FBFAF8"), Sidebar: ui.Hex("#F3F1EC"), Surface: ui.Hex("#FFFFFF"), Card: ui.Hex("#FFFFFF"),
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
	// A shadow on paper is warm, as the paper is.
	ShadowPopover: ui.Hex("#2E2413").Alpha(0.16), ShadowDialog: ui.Hex("#2E2413").Alpha(0.22),
	Inverse: ui.Hex("#2A2926"), InverseText: ui.Hex("#F5F3EE"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

var darkTokens = tokens{
	Background: ui.Hex("#1A1A18"), Sidebar: ui.Hex("#151513"), Surface: ui.Hex("#21211F"), Card: ui.Hex("#1F1F1D"),
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
	ShadowPopover: ui.Transparent, ShadowDialog: ui.Transparent,
	Inverse: ui.Hex("#EAE8E3"), InverseText: ui.Hex("#1A1A18"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

// Sky and Night are the other pair: a clear day and the night after it. The
// canvas is white, not a grey, and every neutral leans to the one hue of the
// accent (a cerulean, about 205 degrees), which is what keeps so light a
// palette from looking washed out. Night is that hue after dark: an ink blue
// with the accent lit.
var skyTokens = tokens{
	Background: ui.Hex("#FFFFFF"), Sidebar: ui.Hex("#F1F7FB"), Surface: ui.Hex("#FFFFFF"), Card: ui.Hex("#F6FAFD"),
	SurfaceHover: ui.Hex("#E8F1F8"), SurfacePressed: ui.Hex("#DAE7F2"), ActiveFill: ui.Hex("#D5E6F3"),
	Track: ui.Hex("#E0EBF4"), Raised: ui.Hex("#FFFFFF"), ControlOff: ui.Hex("#9FB2C4"),
	Border: ui.Hex("#E1EAF2"), BorderStrong: ui.Hex("#A9BCCE"),
	Text: ui.Hex("#0C1B2A"), TextMuted: ui.Hex("#4A6074"), TextSubtle: ui.Hex("#6A7F93"),
	Accent: ui.Hex("#0B6FB8"), AccentHover: ui.Hex("#095F9E"), AccentPressed: ui.Hex("#084F84"),
	AccentText: ui.Hex("#FFFFFF"),
	Selection:  ui.Hex("#D2E8FA"), Focus: ui.Hex("#3D9BE0"), FolderIcon: ui.Hex("#3E92D2"),
	Danger: ui.Hex("#B93A2B"), DangerText: ui.Hex("#FFFFFF"),
	Warning: ui.Hex("#9A6412"), Success: ui.Hex("#1F7A5C"),
	Scrim: ui.Hex("#0A1622").Alpha(0.30), ScrimSoft: ui.Hex("#0A1622").Alpha(0.10),
	ShadowPopover: ui.Hex("#0C3054").Alpha(0.14), ShadowDialog: ui.Hex("#0C3054").Alpha(0.20),
	Inverse: ui.Hex("#12222F"), InverseText: ui.Hex("#EEF6FC"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.Hex("#0C3054").Alpha(0.3),
}

var nightTokens = tokens{
	Background: ui.Hex("#0F151C"), Sidebar: ui.Hex("#0A0F15"), Surface: ui.Hex("#161E28"), Card: ui.Hex("#131A23"),
	SurfaceHover: ui.Hex("#1E2935"), SurfacePressed: ui.Hex("#263342"), ActiveFill: ui.Hex("#283848"),
	Track: ui.Hex("#080C11"), Raised: ui.Hex("#2A3949"), ControlOff: ui.Hex("#4B5C6E"),
	Border: ui.Hex("#212C39"), BorderStrong: ui.Hex("#3D4E60"),
	Text: ui.Hex("#E4EEF6"), TextMuted: ui.Hex("#94A6B8"), TextSubtle: ui.Hex("#73869A"),
	Accent: ui.Hex("#6EC1F2"), AccentHover: ui.Hex("#8BCFF6"), AccentPressed: ui.Hex("#58AEE2"),
	AccentText: ui.Hex("#08131C"),
	Selection:  ui.Hex("#1A3852"), Focus: ui.Hex("#6EC1F2"), FolderIcon: ui.Hex("#62B2E6"),
	Danger: ui.Hex("#F0806E"), DangerText: ui.Hex("#0F151C"),
	Warning: ui.Hex("#DDB05C"), Success: ui.Hex("#7FCB9F"),
	Scrim: ui.Hex("#000000").Alpha(0.5), ScrimSoft: ui.Hex("#000000").Alpha(0.28),
	ShadowPopover: ui.Transparent, ShadowDialog: ui.Transparent,
	Inverse: ui.Hex("#E4EEF6"), InverseText: ui.Hex("#0F151C"),
	Knob: ui.Hex("#FFFFFF"), KnobShadow: ui.RGBA(0, 0, 0, 0.3),
}

// palette is one set of colours a theme can show. id is what settings.json
// keeps (lightColors, darkColors): a released id stays, or the files that name
// it lose their choice.
type palette struct {
	id   string
	name func(locales.Messages) string
	k    tokens
}

// label is the palette's name in the language the app shows.
func (p palette) label() string { return p.name(locales.In(i18n.Locale())) }

// lightPalettes and darkPalettes are the palettes of the two themes, in the
// order Settings shows them. The first of each is the default: what an empty or
// unknown name gets.
var (
	lightPalettes = []palette{
		{"paper", locales.Messages.ColorsPaper, lightTokens},
		{"sky", locales.Messages.ColorsSky, skyTokens},
	}
	darkPalettes = []palette{
		{"graphite", locales.Messages.ColorsGraphite, darkTokens},
		{"night", locales.Messages.ColorsNight, nightTokens},
	}
)

// palettesOf returns the palettes of the dark theme or of the light one.
func palettesOf(dark bool) []palette {
	if dark {
		return darkPalettes
	}
	return lightPalettes
}

// paletteOf returns the palette id of a theme: its first for a name it does
// not have.
func paletteOf(dark bool, id string) palette {
	ps := palettesOf(dark)
	for _, p := range ps {
		if p.id == id {
			return p
		}
	}
	return ps[0]
}

// tokensFor returns the default palette of a theme.
func tokensFor(dark bool) tokens { return palettesOf(dark)[0].k }

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
// one for as long as it is open (frameOf): applyColors fills it at the start
// of every frame and tk reads it for the rest of that frame.
type frame struct {
	c   *ui.Context
	k   tokens
	set bool   // applyColors has run for the window
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

// applyTheme is applyColors with the default palettes.
func applyTheme(c *ui.Context, mode string) tokens { return applyColors(c, mode, "", "") }

// applyColors sets the frame's ui.Theme and tokens. mode is "system", "light"
// or "dark"; "system" follows the desktop. light and dark name the palette of
// each theme. The theme is rebuilt on every frame, so a change of the system's
// appearance is picked up at once; the colours of another mode or palette come
// in over a moment (easeTokens).
func applyColors(c *ui.Context, mode, light, dark string) tokens {
	isDark := c.Theme().Dark
	switch mode {
	case "light":
		isDark = false
	case "dark":
		isDark = true
	}
	name := light
	if isDark {
		name = dark
	}
	k := easeTokens(c, paletteOf(isDark, name).k)
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

// tk returns the tokens of the frame: those applyColors set for the window, or
// the default palette of the frame's theme in a window it has not run for.
func tk(c *ui.Context) tokens {
	if f := frameOf(c); f.set {
		return f.k
	}
	return tokensFor(c.Theme().Dark)
}
