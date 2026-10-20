package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestShortcutKeysSplitsOneCapPerKey(t *testing.T) {
	for in, want := range map[string][]string{
		"":             nil,
		"F5":           {"F5"},
		"Del":          {"Del"},
		"Backspace":    {"Backspace"},
		"Ctrl+U":       {"Ctrl", "U"},
		"Ctrl+Shift+P": {"Ctrl", "Shift", "P"},
		"Alt+Up":       {"Alt", "Up"},
		"⌘U":           {"⌘", "U"},
		"⌘⇧P":          {"⌘", "⇧", "P"},
		"⌥Up":          {"⌥", "Up"},
		"⌘":            {"⌘"},
	} {
		if got := shortcutKeys(in); !slices.Equal(got, want) {
			t.Errorf("shortcutKeys(%q) = %q, want %q", in, got, want)
		}
	}
}

// The colour discipline of docs/dev/design.md: no view names a colour. Everything
// comes from theme.go through tk.
func TestNoColourLiteralsOutsideTheme(t *testing.T) {
	lit := regexp.MustCompile(`\bui\.(RGB|RGBA|Hex|Oklch)\(|ui\.Color\{`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "theme.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if lit.MatchString(line) {
				t.Errorf("%s:%d names a colour outside theme.go: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Overlay tokens: a light overlay is lifted by a shadow, a dark one (where a
// shadow cannot be seen) by an outline, and the radii step 6, 8, 10.
func TestOverlayTokens(t *testing.T) {
	if lightTokens.ShadowPopover.A == 0 || lightTokens.ShadowDialog.A == 0 {
		t.Error("the light theme needs both overlay shadows")
	}
	if lightTokens.ShadowDialog.A <= lightTokens.ShadowPopover.A {
		t.Error("a dialog floats higher than a popover: its shadow must be the stronger")
	}
	if darkTokens.ShadowPopover.A != 0 || darkTokens.ShadowDialog.A != 0 {
		t.Error("the dark theme draws an outline, not a shadow")
	}
	if !(RadiusKeycap < RadiusControl && RadiusControl < RadiusPopover && RadiusPopover < RadiusDialog) {
		t.Error("radii must step keycap < control < popover < dialog")
	}
	if RadiusControl != 6 || RadiusPopover != 8 || RadiusDialog != 10 || RadiusKeycap != 4 {
		t.Errorf("radii = %d/%d/%d/%d, design.md says 6/8/10 and 4", RadiusControl, RadiusPopover, RadiusDialog, RadiusKeycap)
	}
}

// px reads the colour at (x, y) of the tester's last frame.
func px(img *image.RGBA, x, y float32) color.Color { return img.At(int(x), int(y)) }

// The three kinds of overlayPanel, drawn over the canvas in both themes: the
// fill, and what holds the edge up.
func TestOverlayPanelEdges(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		for _, kind := range []overlayKind{overlayDialog, overlayPopover, overlayTip} {
			k := tokensFor(theme == "dark")
			view := func(c *ui.Context) {
				k := applyTheme(c, theme)
				ui.Column(c).Fill().Background(k.Background).Children(func() {
					overlayPanel(c, ui.Box(c).Absolute().Left(60).Top(60).Size(200, 100), kind)
				})
			}
			tt := ui.NewTester(view, 320, 240)
			tt.Frame()
			tt.Frame()
			img := tt.Image()
			fill := k.Surface
			if kind == overlayTip {
				fill = k.Inverse
			}
			if got := px(img, 160, 110); !near(got, fill, 2) {
				t.Errorf("%s kind %d: fill %v, want %v", theme, kind, got, fill)
			}
			below := px(img, 160, 163) // 3px under the bottom edge
			edge := px(img, 60, 110)   // the outermost column of the panel
			switch {
			case theme == "light":
				if near(below, k.Background, 4) {
					t.Errorf("light kind %d: no shadow under the panel (%v)", kind, below)
				}
				if !near(edge, fill, 2) {
					t.Errorf("light kind %d: the edge is %v, want the plain fill: a shadow, not an outline", kind, edge)
				}
			case kind == overlayTip:
				// The inverse chip stands out by itself: no outline in the dark.
				if !near(edge, fill, 2) {
					t.Errorf("dark tip: the edge is %v, want the plain fill", edge)
				}
			default:
				if !near(below, k.Background, 2) {
					t.Errorf("dark kind %d: a shadow under the panel (%v)", kind, below)
				}
				if !near(edge, k.BorderStrong, 2) {
					t.Errorf("dark kind %d: the edge is %v, want the 1px BorderStrong outline %v", kind, edge, k.BorderStrong)
				}
			}
		}
	}
}

// hasPixel reports whether any pixel of the rectangle is near want.
func hasPixel(img *image.RGBA, x0, y0, x1, y1 float32, want ui.Color) bool {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			if near(img.RGBAAt(x, y), want, 12) {
				return true
			}
		}
	}
	return false
}

// textRow is where the row of a palette entry is: its label's vertical centre,
// the row's left edge (the list pads 8px inside a 560px card) and right edge.
func paletteRowAt(h *harness, label string) (y float32) {
	h.t.Helper()
	r, ok := h.tt.Find(label)
	if !ok {
		h.t.Fatalf("no palette row %q; have %q", label, h.tt.Texts())
	}
	return r.Y + r.H/2
}

const palLeft, palRight = (testW-paletteW)/2 + 8 + 4, (testW+paletteW)/2 - scrollGutter

// The palette's rows have one look for "the cursor is here", whether the
// keyboard or the pointer put it there: SurfaceHover, never Selection. The
// active choice is a different cue: an Accent check (and weight 600).
func TestPaletteHighlightIsOneLookAndCurrentChoiceIsChecked(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			h := newHarness(t, theme)
			k := tokensFor(theme == "dark")
			h.openPaletteWith(ui.Cmd, ui.KeyK)
			h.tt.Type("theme")
			h.settle()
			rows := []string{"Theme: System", "Theme: Light", "Theme: Dark"}
			fill := func(label string) color.Color { return px(h.tt.Image(), palLeft, paletteRowAt(h, label)) }
			expect := func(step string, want map[string]ui.Color) {
				h.t.Helper()
				h.tt.Frame()
				for _, r := range rows {
					if got := fill(r); !near(got, want[r], 2) {
						t.Errorf("%s: %q fill %v, want %v", step, r, got, want[r])
					}
				}
			}
			// The keyboard's cursor starts on the first row.
			expect("cursor on the first row", map[string]ui.Color{rows[0]: k.SurfaceHover, rows[1]: k.Surface, rows[2]: k.Surface})

			// The pointer takes the cursor when it moves onto a row: the same fill.
			h.tt.Move(testW/2, paletteRowAt(h, rows[2]))
			h.settle()
			expect("pointer over the third row", map[string]ui.Color{rows[0]: k.Surface, rows[1]: k.Surface, rows[2]: k.SurfaceHover})
			if h.a.pal.cur != 2 {
				t.Errorf("the pointer left the cursor on row %d, want 2", h.a.pal.cur)
			}

			// A pointer at rest does not take the cursor back from the keyboard.
			h.tt.Key(0, ui.KeyUp)
			h.settle()
			h.tt.Frame()
			expect("keyboard Up with the pointer at rest", map[string]ui.Color{rows[0]: k.Surface, rows[1]: k.SurfaceHover, rows[2]: k.Surface})
			if h.a.pal.cur != 1 {
				t.Errorf("the cursor is on row %d after Up, want 1", h.a.pal.cur)
			}

			// The current theme is checked, whether or not the cursor is on it.
			img := h.tt.Image()
			for _, r := range rows {
				y := paletteRowAt(h, r)
				got := hasPixel(img, palRight-26, y-8, palRight, y+8, k.Accent)
				if want := r == "Theme: "+strings.ToUpper(theme[:1])+theme[1:]; got != want {
					t.Errorf("%q: Accent check shown = %v, want %v", r, got, want)
				}
			}
		})
	}
}

// A command that cannot run is dimmed, takes no hover and does not run.
func TestPaletteDisabledRowTakesNoHover(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("download")
	h.settle()
	y := paletteRowAt(h, "Download")
	h.tt.Move(testW/2, y)
	h.settle()
	h.tt.Frame()
	k := tokensFor(false)
	if got := px(h.tt.Image(), palLeft, y); near(got, k.SurfaceHover, 2) {
		t.Errorf("a disabled row is highlighted (%v)", got)
	}
}

// The palette shows the group beside a command while searching, and headings
// as section labels while browsing.
func TestPaletteGroupHeadingsAreSectionLabelsWhileBrowsing(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.mustHave("TRANSFER", "NAVIGATE", "UNAVAILABLE HERE")
	h.tt.Type("upload")
	h.settle()
	h.mustNotHave("TRANSFER", "UNAVAILABLE HERE")
	h.mustHave("Transfer") // the group, now beside "Upload files"
}

// The popup of a select is an overlayPopover of listRows: the option under the
// pointer or the arrows is highlighted, the current value checked.
func TestSelectPopupUsesOverlayRows(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			value := "b"
			opts := []choice{{"a", "Alpha"}, {"b", "Beta"}, {"c", "Gamma"}}
			view := func(c *ui.Context) {
				k := applyTheme(c, theme)
				ui.Column(c).Fill().Background(k.Background).Padding(40).Children(func() {
					selectBox(c, "pick", &value, opts, 200)
				})
			}
			tt := ui.NewTester(view, 400, 300)
			tt.Frame()
			tt.Frame()
			k := tokensFor(theme == "dark")
			tt.ClickAt(100, 54) // the trigger
			tt.Frame()
			tt.Frame()
			tt.Advance(time.Second) // the popup has come in
			a, okA := tt.Find("Alpha")
			g, okG := tt.Find("Gamma")
			if !okA || !okG {
				t.Fatalf("the popup is not open; have %q", tt.Texts())
			}
			ya, yg := a.Y+a.H/2, g.Y+g.H/2
			yb := (ya + yg) / 2
			x := a.X - 4
			img := tt.Image()
			// The popup opens with the cursor on the current value.
			if got := px(img, x, yb); !near(got, k.SurfaceHover, 2) {
				t.Errorf("cursor row fill %v, want SurfaceHover %v", got, k.SurfaceHover)
			}
			for _, y := range []float32{ya, yg} {
				if got := px(img, x, y); !near(got, k.Surface, 2) {
					t.Errorf("other row fill %v, want Surface %v", got, k.Surface)
				}
			}
			right := a.X + 192 - 8 // popup width 200 less the padding
			checked := func(y float32) bool { return hasPixel(img, right-24, y-8, right, y+8, k.Accent) }
			if !checked(yb) || checked(ya) || checked(yg) {
				t.Errorf("check marks: Alpha %v Beta %v Gamma %v, want only Beta", checked(ya), checked(yb), checked(yg))
			}
			// The arrows move the highlight; the check stays on the value.
			tt.Key(0, ui.KeyDown)
			tt.Frame()
			tt.Advance(time.Second) // the highlight's fade
			img = tt.Image()
			if got := px(img, x, yg); !near(got, k.SurfaceHover, 2) {
				t.Errorf("after Down the Gamma row is %v, want SurfaceHover", got)
			}
			if got := px(img, x, yb); !near(got, k.Surface, 2) {
				t.Errorf("after Down the Beta row is still %v", got)
			}
			if !checked(yb) {
				t.Error("the current value lost its check when the highlight moved")
			}
		})
	}
}

// A toast is the inverse chip of overlayTip, with its action, and the action
// runs and closes it.
func TestToastIsAnInverseChipWithAnAction(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			h := newHarness(t, theme)
			k := tokensFor(theme == "dark")
			done := false
			h.a.toastAction("Item removed", "Undo", func() { done = true })
			h.tt.Frame()
			h.tt.Advance(time.Second) // past the fade-in
			h.tt.Frame()
			r, ok := h.tt.Find("Item removed")
			if !ok {
				t.Fatalf("no toast; have %q", h.tt.Texts())
			}
			if got := px(h.tt.Image(), r.X-8, r.Y+r.H/2); !near(got, k.Inverse, 3) {
				t.Errorf("toast fill %v, want Inverse %v", got, k.Inverse)
			}
			if !h.tt.HasText("Undo") {
				t.Fatal("the toast has no action")
			}
			if err := h.tt.Click("Undo"); err != nil {
				t.Fatal(err)
			}
			h.tt.Frame()
			if !done {
				t.Error("the action did not run")
			}
		})
	}
}
