package main

import (
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// Tooltips are mygo's own (Element.Tooltip): its delay, place, radius and
// shadow, in the theme's Inverse and InverseText (applyTheme). The tip waits
// for the pointer to rest, then shows on an Inverse chip.
func TestTooltipIsMygosOwnInTheInverseColours(t *testing.T) {
	t.Parallel()
	const tip = "Calculate folder sizes"
	for _, theme := range []string{"light", "dark"} {
		k := tokensFor(theme == "dark")
		view := func(c *ui.Context) {
			k := applyTheme(c, theme)
			ui.Column(c).Fill().Background(k.Background).Padding(80).Children(func() {
				ui.ButtonBase(c).Label("calc").Tooltip(tip).Size(28, 28).Background(k.Surface)
			})
		}
		tt := ui.NewTester(view, 320, 200)
		tt.Frame()
		r, ok := tt.Find("calc")
		if !ok {
			t.Fatalf("%s: no button", theme)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		tt.Advance(200 * time.Millisecond)
		tt.Frame()
		if _, shown := tt.Find(tip); shown {
			t.Fatalf("%s: the tip shows before the pointer has rested", theme)
		}
		tt.Advance(800 * time.Millisecond)
		tt.Frame()
		tr, shown := tt.Find(tip)
		if !shown {
			t.Fatalf("%s: the tip never showed; have %q", theme, tt.Texts())
		}
		// Inside the chip's padding, left of the text.
		if got := px(tt.Image(), tr.X-3, tr.Y+tr.H/2); !near(got, k.Inverse, 3) {
			t.Errorf("%s: tip fill %v, want Inverse %v", theme, got, k.Inverse)
		}
	}
}

// ---- help icons --------------------------------------------------------------

// helpIcons lists the help texts of the "?" icons on screen: a help icon is a
// button of exactly helpHitSize square whose accessible label is its text.
func (h *harness) helpIcons() []string {
	h.t.Helper()
	h.tt.Frame()
	var out []string
	seen := map[string]bool{}
	for _, s := range h.tt.Texts() {
		if seen[s] || len(s) < 20 {
			continue
		}
		seen[s] = true
		if r, ok := h.tt.Find(s); ok && r.W == helpHitSize && r.H == helpHitSize {
			out = append(out, s)
		}
	}
	return out
}

// hover rests the pointer on the element with text and lets the tooltip delay
// pass, so its tip is drawn.
func (h *harness) hover(text string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no element with text %q; have %q", text, h.tt.Texts())
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Frame()
	h.tt.Advance(800 * time.Millisecond)
	h.tt.Frame()
}

// tipShown reports whether text is drawn twice: once as the icon's accessible
// label and once in the open tooltip.
func (h *harness) tipShown(text string) bool {
	return count(h.tt.Texts(), text) >= 2
}

// hoverHelp hovers the "?" icon whose accessible label is help, fails unless
// its tip shows, and moves the pointer away again.
func (h *harness) hoverHelp(help string) {
	h.t.Helper()
	h.hover(help)
	if !h.tipShown(help) {
		h.t.Errorf("the tip %q is not shown on hover; have %q", help, h.tt.Texts())
	}
	h.tt.Move(0, 0)
	h.tt.Frame()
}

// A disabled toggle still says why it is off when the pointer rests on it:
// mygo gives a disabled element no tip, so the reason would never show.
func TestDisabledToggleShowsWhyItIsOff(t *testing.T) {
	h := newHarness(t, "light")
	h.settle()
	st, ok := h.a.versioningOf(h.a.accountID, h.a.scope.Bucket)
	if !ok || st == "Enabled" || st == "Suspended" {
		t.Fatalf("bucket %q: versioning %q (known %v), want a known unversioned bucket", h.a.scope.Bucket, st, ok)
	}
	h.hover(locales.PathbarShowDeleted())
	if !h.tipShown(locales.PathbarNoVersionsTip()) && !h.tt.HasText(locales.PathbarNoVersionsTip()) {
		t.Errorf("no tip says why %q is off; have %q", locales.PathbarShowDeleted(), h.tt.Texts())
	}
}
