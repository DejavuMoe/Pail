package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Motion (docs/dev/design.md "Motion"): the interface barely moves. A state
// change shows in the frame it happens: hover and press fills, selection,
// indicators, switches, check boxes, panels, counts. Two things ease, both on
// the one token below (250ms, ease-out):
//
//   - an overlay coming in (dialog and its scrim, command palette, select
//     popup, menu, submenu, popover, toast) fades in where it rests; nothing
//     scales or slides, and it goes at once when it closes;
//   - the fill of a progress bar follows the progress, so that it does not
//     jitter with every chunk.
//
// With the system's "reduce motion" preference mygo keeps only fades of
// opacity, which is all the fade-in is. The glide of a mouse wheel's notches is
// mygo's own (as browsers scroll), not an animation of the app. TestOnlyTheTokenMoves
// keeps every other view from animating.

// durBase is the one duration of the interface.
const durBase = 250 * time.Millisecond

// easeOut is the one curve: CSS ease-out, a quick start that settles softly.
var easeOut = ui.CubicBezier(0, 0, 0.58, 1)

// baseAnim is durBase on easeOut.
var baseAnim = ui.Animation{Duration: durBase, Ease: easeOut}

// countText is a count in the secondary size and colour col, with digits of
// equal width so that a changing number does not move what stands next to it.
func countText(c *ui.Context, key, s string, col ui.Color) ui.Element {
	return ui.Text(c.Key(key), s).FontSize(sizeSecondary).TextColor(col).SingleLine().FontFeatures("tnum")
}

// mounted reports whether el was laid out in an earlier frame: false in the
// frame that builds it first.
func mounted(el ui.Element) bool {
	b := el.Bounds()
	return b.W > 0 || b.H > 0
}

// fadeIn fades el, an overlay, in over baseAnim in the frame it first shows
// and holds it at rest after. It is a pose, which mygo applies when it paints:
// mygo lays the layer of overlays out again after transitions, and a
// transition's Enter plays only for an element whose parent was there in the
// frame before, so a transition could not bring an overlay in.
func fadeIn(c *ui.Context, el ui.Element) {
	if !mounted(el) {
		el.AnimatePose(ui.Pose{Fade: 1}, baseAnim)
		c.AnimationFrame() // the next frame heads for rest
		return
	}
	el.AnimatePose(ui.Pose{}, baseAnim)
}

// flips reports whether mygo puts an overlay on the other side of its anchor
// (above instead of below, left instead of right): the place p it would take
// overflows the window along this axis (limit long) and q, the place on the
// other side, overflows less. It is alongTarget of mygo's layout, for
// choosing a side before the first layout.
func flips(p, q, size, limit float32) bool {
	over := func(v float32) float32 {
		return max(0, menuWindowMargin-v) + max(0, v+size-(limit-menuWindowMargin))
	}
	return over(p) > 0 && over(q) < over(p)
}

// modalLayer shows a dialog as ui.DialogBase does (the backdrop takes the
// presses outside the card and Escape, the focus goes into it and back) with
// the scrim and the card fading in over baseAnim. A dialog that closes is gone
// in that frame, so it never holds the keyboard or the pointer. key tells the
// dialogs apart; fn styles the backdrop (it only lays the card out: its colour
// is the scrim) and the card and builds the card's content.
func modalLayer(c *ui.Context, key string, open *bool, scrim ui.Color, fn func(backdrop, panel ui.Element)) ui.Element {
	if *open {
		ui.Overlay(c, func() {
			s := ui.Box(c.Key("scrim:" + key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(scrim)
			fadeIn(c, s)
		})
	}
	return ui.DialogBase(c.Key("modal:"+key), open, func(backdrop, panel ui.Element) {
		fadeIn(c, panel)
		fn(backdrop, panel)
	})
}
