package main

import (
	"math"
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
// on (the second cue — outline, bar, weight — does the rest).
func TestTokenContrastFloors(t *testing.T) {
	for name, k := range map[string]tokens{"light": lightTokens, "dark": darkTokens} {
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
			{"BorderStrong on Surface", k.BorderStrong, k.Surface, 1.8},
			{"ControlOff on Surface", k.ControlOff, k.Surface, 2},
			{"ActiveFill on Sidebar", k.ActiveFill, k.Sidebar, 1.17},
			{"ActiveFill on Background", k.ActiveFill, k.Background, 1.17},
			{"Raised on Track", k.Raised, k.Track, 1.2},
			{"Selection on Background", k.Selection, k.Background, 1.2},
			{"Selection on Surface (palette row)", k.Selection, k.Surface, 1.17},
			// Overlay rows (overlay.go): the highlight is the cursor's one look.
			{"SurfaceHover on Surface (highlighted row)", k.SurfaceHover, k.Surface, 1.1},
			{"Text on SurfaceHover", k.Text, k.SurfaceHover, 7},
			{"TextMuted on SurfaceHover (icon, group)", k.TextMuted, k.SurfaceHover, 4.5},
			{"BorderStrong on SurfaceHover (keycap outline)", k.BorderStrong, k.SurfaceHover, 1.3},
			{"Accent on SurfaceHover (check)", k.Accent, k.SurfaceHover, 4.5},
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
