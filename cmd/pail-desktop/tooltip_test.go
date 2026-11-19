package main

import (
	"testing"
	"time"

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
