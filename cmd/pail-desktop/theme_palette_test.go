package main

import (
	"image/color"
	"os"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
)

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

// Paper is the light theme's palette and Graphite the dark one's.
func TestTokensForTheme(t *testing.T) {
	t.Parallel()
	if tokensFor(false) != lightTokens || tokensFor(true) != darkTokens {
		t.Error("the palettes are not Paper and Graphite")
	}
}

// The other theme comes in as a cross-fade of every colour: halfway
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
	change("dark to light", lightTokens, func() { h.a.opts.theme = "light" })

	// Reduced motion keeps the fade (nothing moves in it); with the animations
	// off the colours are there in the frame of the change.
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.tt.Frame()
	change("reduced motion", darkTokens, func() { h.a.opts.theme = "dark" })

	still := newHarness(t, "light")
	still.a.opts.theme = "dark"
	still.tt.Frame()
	if got := still.tt.Image().RGBAAt(700, 300); !near(got, darkTokens.Background, 1) || still.tt.FramePending() {
		t.Errorf("with the animations off the canvas is %v in the frame of the change (pending %v)", got, still.tt.FramePending())
	}
}

// TestPaletteSnapshots draws the browser and Settings in both themes, when
// PAIL_UI_SNAPSHOT_DIR is set: for looking at them.
func TestPaletteSnapshots(t *testing.T) {
	t.Parallel()
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("PAIL_UI_SNAPSHOT_DIR is not set")
	}
	for _, mode := range []string{"light", "dark"} {
		{
			h := newHarness(t, mode)
			seedTasks(h)
			h.mustClick("manifest.json")
			h.settle()
			h.snap("palette-" + mode + "-browse")
			h.a.openNewFolder()
			h.settle()
			h.snap("palette-" + mode + "-dialog")
			h.a.newFolder = false
			h.a.openTool("settings")
			h.settle()
			h.snap("palette-" + mode + "-settings")
		}
	}
}
