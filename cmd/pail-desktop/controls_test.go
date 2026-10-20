package main

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

func near(got color.Color, want ui.Color, tol int) bool {
	r, g, b, _ := got.RGBA()
	d := func(a uint32, b uint8) int {
		x := int(a>>8) - int(b)
		if x < 0 {
			x = -x
		}
		return x
	}
	return d(r, want.R) <= tol && d(g, want.G) <= tol && d(b, want.B) <= tol
}

// TestSwitchAndCheckboxStatesAreDistinct renders the switch and the check box
// on and off in both themes and reads the pixels: the off switch track is
// ControlOff, the on track Accent, a checked box is Accent filled and an
// unchecked one is not (DESIGN.md "States").
func TestSwitchAndCheckboxStatesAreDistinct(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		on, off, checked, unchecked := true, false, true, false
		view := func(c *ui.Context) {
			k := applyTheme(c, theme)
			ui.Column(c).Fill().Background(k.Surface).Padding(20).Gap(12).Children(func() {
				switchControl(c, &on, "sw-on")
				switchControl(c, &off, "sw-off")
				checkboxControl(c, &checked, "cb-on")
				checkboxControl(c, &unchecked, "cb-off")
			})
		}
		tt := ui.NewTester(view, 240, 200)
		tt.Frame()
		tt.Frame()
		k := tokensFor(dark)
		img := tt.Image()
		find := func(label string) ui.Rect {
			r, ok := tt.Find(label)
			if !ok {
				t.Fatalf("%s: no element %q; have %q", theme, label, tt.Texts())
			}
			return r
		}
		// Sample the track on the side the knob is not on.
		ron, roff := find("sw-on"), find("sw-off")
		if got := img.At(int(ron.X)+5, int(ron.Y+ron.H/2)); !near(got, k.Accent, 6) {
			t.Errorf("%s: on switch track %v, want Accent %v", theme, got, k.Accent)
		}
		if got := img.At(int(roff.X+roff.W)-5, int(roff.Y+roff.H/2)); !near(got, k.ControlOff, 6) {
			t.Errorf("%s: off switch track %v, want ControlOff %v", theme, got, k.ControlOff)
		}
		if ron.W != switchW || ron.H != switchH {
			t.Errorf("%s: switch is %vx%v, want %dx%d", theme, ron.W, ron.H, switchW, switchH)
		}
		// The box sits at the left of its row: sample inside it, off the check mark.
		cbOn, cbOff := find("cb-on"), find("cb-off")
		if got := img.At(int(cbOn.X)+8, int(cbOn.Y)+3); !near(got, k.Accent, 6) {
			t.Errorf("%s: checked box %v, want Accent %v", theme, got, k.Accent)
		}
		if got := img.At(int(cbOff.X)+8, int(cbOff.Y+cbOff.H/2)); !near(got, k.Surface, 6) {
			t.Errorf("%s: unchecked box interior %v, want Surface %v", theme, got, k.Surface)
		}
		if got := img.At(int(cbOff.X), int(cbOff.Y+cbOff.H/2)); near(got, k.Surface, 4) {
			t.Errorf("%s: unchecked box has no outline", theme)
		}
	}
}
