package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// luminance is the WCAG 2 relative luminance of an opaque color.
func luminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrast(a, b ui.Color) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// The floors behind DESIGN.md "Contrast": essential text 4.5:1 (WCAG AA),
// tertiary text 3.5:1, control outlines and the switch-off track enough to
// see the control, and every state fill at least 1.17:1 against what it sits
// on (the second cue — outline, bar, weight — does the rest). Both palettes
// keep them.
func TestTokenContrastFloors(t *testing.T) {
	t.Parallel()
	for name, k := range map[string]tokens{"paper": lightTokens, "graphite": darkTokens} {
		checks := []struct {
			what   string
			fg, bg ui.Color
			min    float64
		}{
			{"Text on Background", k.Text, k.Background, 7},
			{"Text on Surface", k.Text, k.Surface, 7},
			{"TextMuted on Surface", k.TextMuted, k.Surface, 4.5},
			{"TextMuted on Background", k.TextMuted, k.Background, 4.5},
			{"TextMuted on Sidebar", k.TextMuted, k.Sidebar, 4.5},
			{"TextSubtle on Surface", k.TextSubtle, k.Surface, 3.5},
			{"TextSubtle on Sidebar", k.TextSubtle, k.Sidebar, 3.5},
			{"Accent on Background", k.Accent, k.Background, 4.5},
			{"AccentText on Accent", k.AccentText, k.Accent, 4.5},
			{"DangerText on Danger", k.DangerText, k.Danger, 4.5},
			{"Danger on Surface", k.Danger, k.Surface, 4.5},
			// A card of Settings and what stands on it.
			{"Text on Card", k.Text, k.Card, 7},
			{"TextMuted on Card", k.TextMuted, k.Card, 4.5},
			{"Danger on Card", k.Danger, k.Card, 4.5},
			{"ControlOff on Card (switch off)", k.ControlOff, k.Card, 2},
			{"Accent on Card (switch on)", k.Accent, k.Card, 3},
			{"Warning on Surface", k.Warning, k.Surface, 3},
			{"Success on Surface", k.Success, k.Surface, 3},
			{"Focus on Background", k.Focus, k.Background, 3},
			{"BorderStrong on Surface", k.BorderStrong, k.Surface, 1.8},
			{"ControlOff on Surface", k.ControlOff, k.Surface, 2},
			{"ActiveFill on Sidebar", k.ActiveFill, k.Sidebar, 1.17},
			{"ActiveFill on Background", k.ActiveFill, k.Background, 1.17},
			{"Raised on Track", k.Raised, k.Track, 1.2},
			{"Selection on Background", k.Selection, k.Background, 1.2},
			{"Selection on Surface (palette row)", k.Selection, k.Surface, 1.17},
			// Overlay rows (overlay.go): the highlight is the cursor's one look.
			{"ActiveFill on Surface (highlighted row)", k.ActiveFill, k.Surface, 1.2},
			{"Text on ActiveFill", k.Text, k.ActiveFill, 7},
			{"TextMuted on ActiveFill (icon, group, shortcut)", k.TextMuted, k.ActiveFill, 4.5},
			{"Accent on ActiveFill (check)", k.Accent, k.ActiveFill, 4.5},
			{"Danger on ActiveFill (destructive menu row)", k.Danger, k.ActiveFill, 4.5},
			// The current item of the sidebar and of Settings' rail: Selection.
			{"Selection on Sidebar (current item)", k.Selection, k.Sidebar, 1.17},
			{"Text on Selection", k.Text, k.Selection, 7},
			{"TextMuted on Selection (region)", k.TextMuted, k.Selection, 4.5},
			// The object table's check boxes: filled on chosen and hovered rows.
			{"Accent on Selection (chosen row's box)", k.Accent, k.Selection, 3},
			{"Accent on SurfaceHover (hovered row's box)", k.Accent, k.SurfaceHover, 3},
			// Toasts and tooltips: the inverse chip and its text.
			{"InverseText on Inverse", k.InverseText, k.Inverse, 7},
			{"Inverse on Background", k.Inverse, k.Background, 7},
		}
		for _, ch := range checks {
			if got := contrast(ch.fg, ch.bg); got < ch.min {
				t.Errorf("%s: %s = %.2f:1, want >= %.2f:1", name, ch.what, got, ch.min)
			}
		}
	}
}

// Views name radii through the constants of theme.go: a numeric literal in a
// .Radius( call outside theme.go is a bare radius (design.md principle 5).
func TestNoBareRadiusOutsideTheme(t *testing.T) {
	t.Parallel()
	bare := regexp.MustCompile(`\.Radius\(\s*[0-9]`)
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
			if bare.MatchString(line) {
				t.Errorf("%s:%d: bare radius: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
