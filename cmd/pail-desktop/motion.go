package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Motion (docs/dev/design.md "Motion"). A change of state is committed in the
// frame it happens: the text, the weight, the icon and the layout of the new
// state are all there, and nothing waits for an animation. What eases is what
// shows where the state went, and none of it moves a text or the layout:
//
//   - the mark of the current item travels to it from the one before
//     (arrive): the raised thumb of a segmented control, the fill of a row in
//     a list of sections or in the sidebar, the underline of a tab, the ring
//     of a picture in Settings; a switch's knob slides (slideTo);
//   - colours cross-fade: the whole window as the theme or its palette changes
//     (easeTokens), a switch's track as it turns on (fadeTo);
//   - an overlay coming in (dialog and its scrim, command palette, select
//     popup, menu, submenu, popover, toast) fades in where it rests (fadeIn);
//     nothing scales or slides, and it goes at once when it closes;
//   - the fill of a progress bar does not move over time: it snaps to each
//     report (an ease that the next report interrupts never settles, and the
//     window would draw for as long as a transfer runs);
//   - a page travels to the section chosen from its list of sections
//     (glideTo), so that where it went is seen.
//
// Hover and press fills, the selection of rows, check boxes, panels opening
// and counts show at once: they answer the pointer, and an answer that eases
// reads as lag.
//
// Marks and fades are poses, which mygo plays in frames that only paint: the
// view is not built again while they run, so they stay smooth over a large
// table. With the system's "reduce motion" preference nothing travels: marks
// and knobs are at their place at once, the page is at its section at once,
// and only the fades of colour and opacity remain. The glide of a mouse
// wheel's notches is mygo's own (as browsers scroll), not an animation of the
// app. TestOnlyTheTokenMoves keeps every other view from animating.

// The three tokens. baseAnim brings an overlay in and carries the page;
// moveAnim carries a mark, and starts faster because it answers
// a click; tintAnim changes a colour.
const (
	durBase = 250 * time.Millisecond
	durMove = 220 * time.Millisecond
	durTint = 180 * time.Millisecond
)

// easeOut is CSS ease-out, a quick start that settles softly. easeMove leaves
// at once and spends most of its time arriving, as a thing set down by hand.
var (
	easeOut  = ui.CubicBezier(0, 0, 0.58, 1)
	easeMove = ui.CubicBezier(0.2, 0, 0, 1)
)

var (
	baseAnim = ui.Animation{Duration: durBase, Ease: easeOut}
	moveAnim = ui.Animation{Duration: durMove, Ease: easeMove}
	// Oklab keeps two colours of unlike lightness from meeting in a muddy
	// middle, which is what a light theme turning dark would pass through.
	tintAnim = ui.Animation{Duration: durTint, Ease: easeOut, Oklab: true}
)

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

// glideTo scrolls the container s tracks until y is at its top, over baseAnim:
// the answer to choosing a section from a list of sections. s.Scrolling says
// that it is on its way; the user scrolling stops it, and with reduced motion
// it is there at once.
func glideTo(s *ui.ScrollState, y float32) { s.ScrollTo(s.X, y, baseAnim) }

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

// tokenKey tells the colours of the window apart for easeTokens.
type tokenKey int

// easeTokens returns the colours the window shows in this frame on its way to
// those of to: to itself, unless the theme or its palette changed a moment
// ago, when every colour is between the old one and the new over tintAnim. The
// window starts in the colours it is first given.
func easeTokens(c *ui.Context, to tokens) tokens {
	root := c.Root()
	to.each(func(i int, col *ui.Color) { *col = root.AnimateColor(tokenKey(i), *col, tintAnim) })
	return to
}

// markState is what arrive keeps of the mark of one group of items (the
// segments of a control, the rows of a list of sections, the tabs of a strip):
// which item holds it, and the way it is on.
type markState struct {
	id    any
	at    ui.Rect   // the box of that item, as last laid out
	d     ui.Rect   // where the mark last set off, less at: its left, top, width and height
	since time.Time // when it set off; zero if it did not travel
	pose  bool      // it travels as a pose: its size does not change on the way
	held  bool      // no frame has shown the pose where it sets off yet
	seen  uint64    // the build that drew it last
}

// ahead is how much of d the mark still has before it: 1 as it sets off, 0 at
// rest.
func (g *markState) ahead(now time.Time) float32 {
	if g.since.IsZero() {
		return 0
	}
	t := float32(now.Sub(g.since)) / float32(durMove)
	if t < 0 || t >= 1 {
		return 0
	}
	return 1 - easeMove(t)
}

type markKey struct{}

// markOf returns the markState that el, the element holding a group of items,
// keeps for it.
func markOf(el ui.Element) *markState {
	return ui.Local(el, markKey{}, func() markState { return markState{} })
}

// markReach is how far a mark travels. To an item farther off than this it
// does not cross the window: it is there at once.
const markReach = 320

// arrive is called in every frame inside item, the current one of its group,
// and builds the sign that it is: a box that fills item, which style gives its
// look (a fill behind the label, and inside it a line under the item or a ring
// around it). When another item of the group held the mark in the frame
// before, the mark sets off from that item's box and travels to its own over
// moveAnim; an item chosen while the mark is still on its way takes it over
// from where it is. The mark is a child of its item, so it rests exactly where
// the layout puts it, in every frame and at any window size.
//
// Between items of one size the way is a pose, which mygo plays without
// building the view. Between items of unlike sizes (the segments of a control
// with labels of unlike lengths) the mark also changes its size on the way, so
// its box is laid out in each of those frames.
//
// A caller whose mark is a fill gives item and the mark ZIndex(-1), so that
// the mark passes under the labels of the items it crosses.
func arrive(c *ui.Context, g *markState, id any, item ui.Element, style func(mark ui.Element)) {
	n, now := frameOf(c).n, c.Now()
	to := item.LayoutBounds()
	if g.id != id {
		// Where the mark is now: at its last item, or short of it by what is
		// left of the way it was on.
		a := g.ahead(now)
		d := ui.Rect{
			X: g.at.X + g.d.X*a - to.X, Y: g.at.Y + g.d.Y*a - to.Y,
			W: g.at.W + g.d.W*a - to.W, H: g.at.H + g.d.H*a - to.H,
		}
		dx, dy := d.X+d.W/2, d.Y+d.H/2 // from centre to centre
		// The mark was drawn in the build before, or earlier in this one: a
		// control that takes a click while it is built has both items current
		// for a build.
		drawn := g.seen == n || g.seen+1 == n
		g.d, g.since, g.pose, g.held = ui.Rect{}, time.Time{}, false, false
		if drawn && g.at.W > 0 && to.W > 0 && !c.Instant() && !c.Preferences().ReduceMotion &&
			max(dx, -dx) <= markReach && max(dy, -dy) <= markReach {
			g.d, g.since = d, now
			g.pose = max(d.W, -d.W) < 0.5 && max(d.H, -d.H) < 0.5
			g.held = g.pose
		}
	}
	g.id, g.seen = id, n
	if to.W > 0 {
		g.at = to
	}
	a := float32(0)
	if !g.pose {
		if a = g.ahead(now); a > 0 {
			c.AnimationFrame()
		}
	}
	// Its own key: without one mygo takes it for the child that stood first in
	// item the frame before, and it would not be new when it is.
	mark := ui.Box(c.Key(markKey{})).Absolute().Left(g.d.X * a).Top(g.d.Y * a).Right(-(g.d.X + g.d.W) * a).Bottom(-(g.d.Y + g.d.H) * a)
	// A pose starts where a frame first shows it, so the mark is posed at the
	// place it sets off from until a frame has drawn it there (mygo builds a
	// frame again when a control changed its value in it, and shows only the
	// last build), and at rest from the next one on.
	p := ui.Pose{}
	if g.held {
		if mounted(mark) {
			g.held = false
		} else {
			p = ui.Pose{X: g.d.X, Y: g.d.Y}
			c.AnimationFrame()
		}
	}
	mark.AnimatePose(p, moveAnim)
	style(mark)
}

// slideTo shows el, a switch's knob, x to the right of where the layout puts
// it, travelling there over moveAnim when x changes.
func slideTo(el ui.Element, x float32) { el.AnimatePose(ui.Pose{X: x}, moveAnim) }

// fadeTo shows or hides el, a fill laid over another colour, by fading it
// over tintAnim: the one colour turns into the other.
func fadeTo(el ui.Element, shown bool) {
	p := ui.Pose{}
	if !shown {
		p.Fade = 1
	}
	el.AnimatePose(p, tintAnim)
}
