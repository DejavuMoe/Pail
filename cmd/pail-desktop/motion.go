package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Motion (docs/dev/design.md "Motion"): every duration, curve and spring of the
// interface is defined here, and views name them instead of writing numbers.
// Motion is quick and quiet: a colour fades in 120ms, a panel or an indicator
// moves in 200 to 260ms, an overlay comes in on a spring with a small bounce
// and goes with a short fade, never a bounce.
//
// Where it is forbidden: the window being resized (nothing here depends on the
// window's size, and mygo does not animate layout changes made by a resize),
// the rows and scrolling of the virtualised object table, and loops that only
// decorate. With the system's "reduce motion" preference mygo lets values,
// poses and transitions arrive at once and keeps only the fades of colours and
// opacity, which is exactly what we want, so no view asks for it itself.
//
// Two rules of mygo v0.5.5 shape the helpers below. A transition's Enter
// plays only for an element whose parent was there in the frame before, and
// mygo lays the layer of overlays out again after the transitions, which puts
// back at once what a transition moved inside it: so overlays come in by a
// pose (enterPose), which mygo applies when it paints, and leave by a
// transition's Exit (a copy fading out under the layer, which is always
// there: the toast viewport builds it in every frame).

// Durations.
const (
	// durFast is the fade of a fill under the pointer.
	durFast = 120 * time.Millisecond
	// durStandard is the slide of an indicator, the entrance of a scrim or a
	// toast, the move of a progress bar and the fade of a panel's content.
	durStandard = 200 * time.Millisecond
	// durSlow is the collapse and expansion of a panel.
	durSlow = 260 * time.Millisecond
	// durExit is how long an overlay takes to go: a fade, with no bounce.
	durExit = 120 * time.Millisecond
)

// Overlay geometry at the start of an entrance and the end of an exit.
const (
	// overlayFrom is the scale an overlay comes in from, popRise how far
	// (DIPs) it comes along its axis, and exitTo the scale a dialog leaves at.
	overlayFrom = 0.94
	popRise     = 6
	exitTo      = 0.98
	// toastRise is how far a toast rises as it comes in.
	toastRise = 8
	// submenuSlide is how far a submenu slides out from its parent.
	submenuSlide = 6
)

// easeStandard is the one curve of the interface: quick start, long soft
// landing (CSS cubic-bezier(0.2, 0, 0, 1)).
var easeStandard = ui.CubicBezier(0.2, 0, 0, 1)

// The springs. Both are SwiftUI-style: Duration is about how long they take
// to arrive, Bounce how much they overshoot.
var (
	// overlaySpring is the entrance of dialogs, popovers, menus and toasts.
	overlaySpring = ui.Animation{Spring: ui.SpringOptions{Duration: 240 * time.Millisecond, Bounce: 0.18}}
	// slideSpring moves indicators (tab underline, sidebar bar, switch knob).
	slideSpring = ui.Animation{Spring: ui.SpringOptions{Duration: durStandard, Bounce: 0.12}}
	// fadeAnim is a plain fade or a move along the standard curve.
	fadeAnim = ui.Animation{Duration: durStandard, Ease: easeStandard}
	// colorAnim is a colour changing under the pointer.
	colorAnim = ui.Animation{Duration: durFast, Ease: easeStandard}
	// panelAnim is the collapse and expansion of a panel.
	panelAnim = ui.Animation{Duration: durSlow, Ease: easeStandard}
	// exitAnim is an overlay going.
	exitAnim = ui.Animation{Duration: durExit, Ease: easeStandard}
)

// countRoll rolls the digits of a count that changes (status bar, transfer
// counts): the characters that change move, those that stay stay; with reduced
// motion the text fades across instead.
var countRoll = ui.ContentTransition{Numeric: true}

// Motions shared by pointer: ElementTransition takes pointers, and a literal
// in a view would allocate in every frame.
var (
	// motionFade comes and goes by opacity alone.
	motionFade = ui.Motion{}
	// motionDialogExit goes with a fade and a slight shrink.
	motionDialogExit = ui.Motion{Scale: exitTo}
)

// hoverFade is the transition of a control whose fill changes under the
// pointer or with its state: it fades the background and border colours in
// durFast and animates nothing else, in particular not where the element is
// or how large, which a window resize changes.
func hoverFade() ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: durFast, Ease: easeStandard}
}

// mounted reports whether el was laid out in an earlier frame: false in the
// frame that builds it first. A pose starts where it is first given, so an
// element that should come in is given the pose it comes from in that frame
// and its resting pose in the next (enterPose).
func mounted(el ui.Element) bool {
	b := el.Bounds()
	return b.W > 0 || b.H > 0
}

// enterPose brings el in from the pose from, with spring, in the frame it
// first shows, and holds it at rest after. from.Fade is 1 (gone) so that it
// also fades in. With reduced motion the position and scale arrive at once
// and only the fade is left (mygo, AnimatePose).
func enterPose(c *ui.Context, el ui.Element, from ui.Pose, spring ui.Animation) {
	if !mounted(el) {
		el.AnimatePose(from, spring)
		c.AnimationFrame() // the next frame heads for rest
		return
	}
	el.AnimatePose(ui.Pose{}, spring)
}

// leavePose is enterPose the other way: el rests in the frame it first shows
// and then heads for to as anim says. For a copy of something that just went,
// built to fade out where it was.
func leavePose(c *ui.Context, el ui.Element, to ui.Pose, anim ui.Animation) {
	if !mounted(el) {
		el.AnimatePose(ui.Pose{}, anim)
		c.AnimationFrame()
		return
	}
	el.AnimatePose(to, anim)
}

// flips reports whether mygo puts an overlay on the other side of its anchor
// (above instead of below, left instead of right): the place p it would take
// overflows the window along this axis (limit long) and q, the place on the
// other side, overflows less. It is alongTarget of mygo's layout, for
// choosing which way an entrance comes from before the first layout.
func flips(p, q, size, limit float32) bool {
	over := func(v float32) float32 {
		return max(0, menuWindowMargin-v) + max(0, v+size-(limit-menuWindowMargin))
	}
	return over(p) > 0 && over(q) < over(p)
}

// fadeIn fades el in over durStandard in the frame it first shows, as the
// content of a panel that just opened does. Nothing moves.
func fadeIn(c *ui.Context, el ui.Element) {
	enterPose(c, el, ui.Pose{Fade: 1}, fadeAnim)
}

// popPose is the pose of an overlay coming in: scaled from overlayFrom and
// moved dx, dy toward where it came from, fading in.
func popPose(dx, dy float32) ui.Pose {
	return ui.Pose{X: dx, Y: dy, Scale: overlayFrom, Fade: 1}
}

// overlayEnter brings el, an overlay panel, in with the overlay spring from
// popPose(dx, dy), growing out of (ox, oy) (fractions of its box: its anchor
// corner).
func overlayEnter(c *ui.Context, el ui.Element, dx, dy, ox, oy float32) {
	el.TransformOrigin(ox, oy)
	enterPose(c, el, popPose(dx, dy), overlaySpring)
}

// slideMatch is the identity two indicators of one group share: the one that
// appears where the other showed in the frame before slides from there.
type slideMatch struct{ group string }

// slideTransition makes an indicator (the underline of a tab, the bar of a
// sidebar row) slide from the place of the group's previous one to its own
// with slideSpring, and animates nothing else. With reduced motion mygo skips
// the slide.
func slideTransition(group string) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Animation: &slideSpring, Match: slideMatch{group}}
}

// exitTransition is the transition of an element that leaves: it fades (and
// shrinks as m says) in durExit and animates nothing else while it stays.
func exitTransition(m *ui.Motion) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: durExit, Ease: easeStandard, Exit: m}
}

// overlayExit is the transition of an overlay panel built straight under the
// layer of overlays (a select's popup): a fade, no bounce.
func overlayExit() ui.ElementTransition { return exitTransition(&motionFade) }

// fadeInOut is the transition of an element, built straight under a parent
// that stays, that fades in over durStandard as it appears and out over
// durExit as it goes, and animates nothing else (its colours fade). A
// transition has one duration for coming and going, so it takes the leaving
// one once the element has come in: laid out before, and not moving.
func fadeInOut(el ui.Element) ui.ElementTransition {
	t := ui.ElementTransition{Enter: &motionFade, Exit: &motionFade, Colors: true, Duration: durStandard, Ease: easeStandard}
	if mounted(el) && !el.Transitioning() {
		t.Duration = durExit
	}
	return t
}

// modalLayer shows a dialog as ui.DialogBase does (the backdrop takes the
// presses outside the card and Escape, the focus goes into it and back) with
// motion: the scrim fades in over durStandard and out over durExit; the card
// rises from popPose on the overlay spring; on leaving, the card fades and
// shrinks to exitTo in durExit, with no bounce.
//
// The scrim is a layer of its own under the backdrop, so that the shrink
// takes the card alone: a leaving element is a copy of the last frame that
// mygo moves as a whole. The leaving copies are inert, take no pointer and no
// focus, and the layer that took them is gone in the frame the dialog closes,
// so a closing dialog never holds the keyboard or the pointer. key tells the
// dialogs apart; fn styles the backdrop (it only lays the card out: its colour
// is the scrim) and the card and builds the card's content.
func modalLayer(c *ui.Context, key string, open *bool, scrim ui.Color, fn func(backdrop, panel ui.Element)) ui.Element {
	winW, winH := c.Size()
	if *open {
		ui.Overlay(c, func() {
			s := ui.Box(c.Key("scrim:" + key)).Absolute().Left(0).Top(0).Right(0).Bottom(0).Background(scrim)
			s.Transition(fadeInOut(s))
		})
	}
	return ui.DialogBase(c.Key("modal:"+key), open, func(backdrop, panel ui.Element) {
		// The copy that leaves shrinks about the card's centre, not the window's.
		ox, oy := float32(0.5), float32(0.5)
		if b := panel.Bounds(); b.W > 0 && winW > 0 && winH > 0 {
			ox, oy = (b.X+b.W/2)/winW, (b.Y+b.H/2)/winH
		}
		backdrop.TransformOrigin(ox, oy).Transition(exitTransition(&motionDialogExit))
		enterPose(c, panel, popPose(0, popRise), overlaySpring)
		fn(backdrop, panel)
	})
}
