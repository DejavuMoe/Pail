package main

import (
	"image/color"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/egoist/mygo/ui"
)

// allPalettes is every palette of both themes, by name.
func allPalettes() map[string]palette {
	m := map[string]palette{}
	for _, dark := range []bool{false, true} {
		for _, p := range palettesOf(dark) {
			m[p.id] = p
		}
	}
	return m
}

// Every colour of the tokens is in the list easeTokens walks: one that is not
// would keep the colour of the first palette shown.
func TestEachTokenIsListed(t *testing.T) {
	t.Parallel()
	var k tokens
	v := reflect.ValueOf(&k).Elem()
	colorT := reflect.TypeOf(ui.Color{})
	n := 0
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Type() != colorT {
			t.Fatalf("tokens.%s is a %s: each lists colours only", v.Type().Field(i).Name, v.Field(i).Type())
		}
		n++
		v.Field(i).Set(reflect.ValueOf(ui.Color{R: uint8(n), A: 255}))
	}
	seen := map[uint8]bool{}
	k.each(func(i int, c *ui.Color) {
		if i != len(seen) {
			t.Errorf("each numbers a colour %d, want %d", i, len(seen))
		}
		seen[c.R] = true
	})
	if len(seen) != n {
		t.Errorf("each visits %d of the %d colours of tokens", len(seen), n)
	}
}

// The names settings.json keeps are of the shape the service accepts, differ
// from one another, and an unknown or empty one is the default palette.
func TestPaletteNames(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^[a-z0-9-]+$`)
	seen := map[string]bool{}
	for _, dark := range []bool{false, true} {
		ps := palettesOf(dark)
		if len(ps) < 2 {
			t.Fatalf("dark=%v has %d palettes, want a choice", dark, len(ps))
		}
		for _, p := range ps {
			if !shape.MatchString(p.id) || seen[p.id] {
				t.Errorf("palette id %q is not a new lowercase name", p.id)
			}
			seen[p.id] = true
			if p.label() == "" {
				t.Errorf("palette %q has no name", p.id)
			}
			if got := paletteOf(dark, p.id); got.id != p.id {
				t.Errorf("paletteOf(%v, %q) = %q", dark, p.id, got.id)
			}
		}
		for _, id := range []string{"", "no-such", palettesOf(!dark)[1].id} {
			if got := paletteOf(dark, id); got.id != ps[0].id {
				t.Errorf("paletteOf(%v, %q) = %q, want the default %q", dark, id, got.id, ps[0].id)
			}
		}
	}
	if tokensFor(false) != lightTokens || tokensFor(true) != darkTokens {
		t.Error("the default palettes are not Paper and Graphite")
	}
}

// The command palette has a command for each palette. It names the theme the
// palette is for, sets that theme's choice whichever theme shows, and the two
// palettes in use carry the check. It changes the language, which every
// window of the test binary shares: not parallel.
func TestPaletteCommandsChooseColors(t *testing.T) {
	h := newHarness(t, "light")
	entry := func(label string) palEntry {
		t.Helper()
		for _, e := range h.a.paletteEntries() {
			if e.label == label {
				return e
			}
		}
		t.Fatalf("the palette has no %q", label)
		return palEntry{}
	}
	for label, current := range map[string]bool{
		"Light colors: Paper": true, "Light colors: Sky": false,
		"Dark colors: Graphite": true, "Dark colors: Night": false,
	} {
		if e := entry(label); e.current != current || !e.enabled {
			t.Errorf("%q: current=%v enabled=%v, want current=%v and enabled", label, e.current, e.enabled, current)
		}
	}

	entry("Dark colors: Night").run()
	h.settle()
	if h.a.lightColors != "" || h.a.darkColors != "night" || h.a.svc.Settings().DarkColors != "night" {
		t.Fatalf("after Dark colors: Night: light=%q dark=%q saved=%q", h.a.lightColors, h.a.darkColors, h.a.svc.Settings().DarkColors)
	}
	if got := px(h.tt.Image(), 700, 300); !near(got, lightTokens.Background, 1) {
		t.Errorf("the light window changed with the dark theme's palette: %v", got)
	}
	entry("Light colors: Sky").run()
	h.settle()
	if got := px(h.tt.Image(), 700, 300); !near(got, skyTokens.Background, 1) {
		t.Errorf("the canvas is %v after Light colors: Sky, want %v", got, skyTokens.Background)
	}
	if !entry("Light colors: Sky").current || entry("Light colors: Paper").current || !entry("Dark colors: Night").current {
		t.Error("the check is not on the palettes in use")
	}

	// A word for the thing finds all four, in either language.
	if got := len(filterPalette(h.a.paletteEntries(), "colors")); got != 4 {
		t.Errorf("\"colors\" finds %d commands, want the 4 palettes", got)
	}
	h.setLocale("zh-Hans")
	h.settle()
	entry("浅色配色：晴空")
	if got := labelsOf(filterPalette(h.a.paletteEntries(), "夜空")); len(got) != 1 || got[0] != "深色配色：夜空" {
		t.Errorf("\"夜空\" finds %q", got)
	}
}

// The window is drawn in the palette chosen for its theme, and the other
// theme's choice does not touch it.
func TestWindowShowsTheChosenPalette(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode, light, dark string
		want              tokens
	}{
		{"light", "", "", lightTokens},
		{"light", "sky", "night", skyTokens},
		{"light", "no-such", "night", lightTokens},
		{"dark", "sky", "", darkTokens},
		{"dark", "sky", "night", nightTokens},
	} {
		h := newRawHarness(t, tc.mode, true)
		h.a.lightColors, h.a.darkColors = tc.light, tc.dark
		h.tt.Frame()
		if got := px(h.tt.Image(), 700, 300); !near(got, tc.want.Background, 1) {
			t.Errorf("%s with %q/%q: the canvas is %v, want %v", tc.mode, tc.light, tc.dark, got, tc.want.Background)
		}
		if got := px(h.tt.Image(), 4, 300); !near(got, tc.want.Sidebar, 1) {
			t.Errorf("%s with %q/%q: the sidebar is %v, want %v", tc.mode, tc.light, tc.dark, got, tc.want.Sidebar)
		}
	}
}

// Settings choose a palette for each theme with a picture of each; the choice
// shows at once, is saved under the theme's own key, and comes back with the
// next start.
func TestSettingsChooseAPalette(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openTool("settings")
	h.settle()
	for _, s := range []string{"Light colors", "Dark colors", "Paper", "Sky", "Graphite", "Night"} {
		h.rectOf(s)
	}
	h.mustClick("Sky")
	h.settle()
	if h.a.lightColors != "sky" || h.a.darkColors != "" {
		t.Fatalf("after choosing Sky the app has %q/%q", h.a.lightColors, h.a.darkColors)
	}
	if got := px(h.tt.Image(), 4, 500); !near(got, skyTokens.Sidebar, 1) {
		t.Errorf("the sidebar is %v after choosing Sky, want %v", got, skyTokens.Sidebar)
	}
	h.mustClick("Night")
	h.settle()
	st := h.env.Svc.Settings()
	if st.LightColors != "sky" || st.DarkColors != "night" {
		t.Errorf("saved %q/%q, want sky/night", st.LightColors, st.DarkColors)
	}
	if got := px(h.tt.Image(), 4, 500); !near(got, skyTokens.Sidebar, 1) {
		t.Errorf("choosing the dark theme's palette changed the light window: sidebar %v", got)
	}
	// The dark theme shows its own choice, and the pictures of the theme row
	// follow both.
	h.a.setTheme("dark")
	h.settle()
	if got := px(h.tt.Image(), 4, 500); !near(got, nightTokens.Sidebar, 1) {
		t.Errorf("the dark sidebar is %v, want Night's %v", got, nightTokens.Sidebar)
	}
	// A new app over the same settings starts in them.
	b := newApp(h.env.Svc, options{width: testW, height: testH, demo: true, still: true})
	t.Cleanup(b.close)
	if b.lightColors != "sky" || b.darkColors != "night" || b.colorsOf(true).k != nightTokens {
		t.Errorf("a new app starts with %q/%q", b.lightColors, b.darkColors)
	}
	// Choosing the default again saves it by name.
	h.mustClick("Graphite")
	h.settle()
	if st := h.env.Svc.Settings(); st.DarkColors != "graphite" {
		t.Errorf("saved %q after choosing Graphite", st.DarkColors)
	}
}

// Another theme or palette comes in as a cross-fade of every colour: halfway
// the canvas is between the two, and at rest it is the new one and the window
// asks for no more frames. Nothing moves meanwhile.
func TestColoursCrossFade(t *testing.T) {
	t.Parallel()
	h := newMotionHarness(t, "light")
	h.rest()
	canvas := func() color.RGBA { return h.tt.Image().RGBAAt(700, 300) }
	if !near(canvas(), lightTokens.Background, 1) {
		t.Fatalf("the canvas starts as %v", canvas())
	}
	row := h.rectOf("manifest.json")
	change := func(what string, to tokens, do func()) {
		t.Helper()
		from := canvas()
		do()
		h.tt.Frame()
		h.step(durTint / 2)
		mid := canvas()
		if mid == from || near(mid, to.Background, 1) {
			t.Errorf("%s: halfway the canvas is %v (from %v to %v): it does not fade", what, mid, from, to.Background)
		}
		if got := h.rectOf("manifest.json"); got != row {
			t.Errorf("%s: a row moved while the colours changed: %+v, was %+v", what, got, row)
		}
		h.rest()
		if !near(canvas(), to.Background, 1) {
			t.Errorf("%s: at rest the canvas is %v, want %v", what, canvas(), to.Background)
		}
		if h.tt.FramePending() {
			t.Errorf("%s: the window still asks for frames after the colours arrived", what)
		}
	}
	change("light to dark", darkTokens, func() { h.a.opts.theme = "dark" })
	change("Graphite to Night", nightTokens, func() { h.a.darkColors = "night" })
	change("dark to light", lightTokens, func() { h.a.opts.theme = "light" })

	// Reduced motion keeps the fade (nothing moves in it); with the animations
	// off the colours are there in the frame of the change.
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.tt.Frame()
	change("reduced motion", skyTokens, func() { h.a.lightColors = "sky" })

	still := newHarness(t, "light")
	still.a.opts.theme = "dark"
	still.tt.Frame()
	if got := still.tt.Image().RGBAAt(700, 300); !near(got, darkTokens.Background, 1) || still.tt.FramePending() {
		t.Errorf("with the animations off the canvas is %v in the frame of the change (pending %v)", got, still.tt.FramePending())
	}
}

// TestPaletteSnapshots draws the browser and Settings in every palette, when
// PAIL_UI_SNAPSHOT_DIR is set: for looking at them.
func TestPaletteSnapshots(t *testing.T) {
	t.Parallel()
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("PAIL_UI_SNAPSHOT_DIR is not set")
	}
	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		for _, p := range palettesOf(dark) {
			h := newHarness(t, mode)
			h.a.lightColors, h.a.darkColors = p.id, p.id
			seedTasks(h)
			h.mustClick("manifest.json")
			h.settle()
			h.snap("palette-" + p.id + "-browse")
			h.a.openNewFolder()
			h.settle()
			h.snap("palette-" + p.id + "-dialog")
			h.a.newFolder = false
			h.a.openTool("settings")
			h.settle()
			h.snap("palette-" + p.id + "-settings")
		}
	}
}
