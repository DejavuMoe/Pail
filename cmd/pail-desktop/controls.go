package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// Controls whose mygo look cannot carry the "States" table of DESIGN.md:
// ui.Switch draws its off track from Border, which is nearly the card color,
// and ui.Checkbox draws a 1px hairline box. These are built on the bases.

const (
	switchW, switchH = 36, 20
	checkSize        = 16
)

// switchControl is a 36x20 switch: ControlOff track while off, Accent while
// on, a white knob at the side of its state. *on changes in the frame of the
// click; the knob slides to its side and the Accent fades over the track
// (slideTo, fadeTo), both in frames that only paint.
func switchControl(c *ui.Context, on *bool, label string) ui.Element {
	k := tk(c)
	sw := ui.SwitchBase(c, on).Label(label).Size(switchW, switchH).Radius(switchH / 2).FocusRing(false)
	const in = 2 // between the track's edge and the knob
	const d = switchH - 2*in
	side := float32(0)
	if *on {
		side = switchW - switchH
	}
	hovered := sw.Hovered()
	sw.Background(k.ControlOff).Children(func() {
		full := func() ui.Element {
			return ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(switchH / 2)
		}
		fadeTo(full().Background(k.Accent), *on)
		if hovered {
			full().Border(1, k.BorderStrong.Alpha(0.5))
		}
		slideTo(ui.Box(c).Absolute().Left(in).Top(in).Size(d, d).Radius(d/2).Background(k.Knob).Shadow(0, 1, 3, 0, k.KnobShadow), side)
	})
	// Read before drawing: a frame that only paints (an overlay fading in,
	// motion.go) draws again without building, when an Element handle no longer
	// holds.
	focused := sw.FocusVisible()
	sw.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if focused {
			p.FocusRing(r, [4]float32{switchH / 2, switchH / 2, switchH / 2, switchH / 2})
		}
	})
	return sw
}

// checkState is what a check box shows.
type checkState int8

const (
	checkOff checkState = iota
	checkOn
	checkMixed // some of what it stands for: the select-all header
)

// checkBox is the 16px box of a check box in state st: a 1.5px BorderStrong
// outline on Surface while off (TextMuted under the pointer), an Accent fill
// with an AccentText check mark, or a dash while mixed, otherwise. ring draws
// the focus ring around it.
func checkBox(c *ui.Context, st checkState, hover, ring bool) ui.Element {
	k := tk(c)
	box := ui.Box(c).Size(checkSize, checkSize).Radius(RadiusSmall).Shrink(0)
	if st != checkOff {
		box.Background(k.Accent).Border(1.5, k.Accent)
	} else {
		box.Background(k.Surface).Border(1.5, k.BorderStrong)
		if hover {
			box.Border(1.5, k.TextMuted)
		}
	}
	box.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if st != checkOff {
			at := func(fx, fy float32) (float32, float32) { return r.X + r.W*fx, r.Y + r.H*fy }
			var path ui.Path
			if st == checkMixed {
				path.MoveTo(at(0.27, 0.5)).LineTo(at(0.73, 0.5))
			} else {
				path.MoveTo(at(0.24, 0.52)).LineTo(at(0.43, 0.71)).LineTo(at(0.77, 0.31))
			}
			p.StrokePath(&path, 1.75, k.AccentText)
		}
		if ring {
			p.FocusRing(r, [4]float32{4, 4, 4, 4})
		}
	})
	return box
}

// checkboxControl is a 16px check box with a label (checkBox).
func checkboxControl(c *ui.Context, checked *bool, label string) ui.Element {
	k := tk(c)
	row := ui.CheckboxBase(c, checked).Label(label).Gap(8).AlignItems(ui.Center).FocusRing(false)
	st := checkOff
	if *checked {
		st = checkOn
	}
	row.Children(func() {
		checkBox(c, st, row.Hovered(), row.FocusVisible())
		ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text)
	})
	return row
}

// buttonRow is the right-aligned row of dialog buttons.
func buttonRow(c *ui.Context, children func()) ui.Element {
	return ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(children)
}

// confirmButtons is the usual dialog footer: Cancel, then one solid button,
// primaryButton or dangerButton, labelled action and disabled while off.
// Cancel runs cancel; the solid button runs submit.
func confirmButtons(c *ui.Context, solid func(*ui.Context, string, bool) ui.Element, action string, off bool, cancel, submit func()) {
	buttonRow(c, func() {
		if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
			cancel()
		}
		if solid(c, action, off).Clicked() {
			submit()
		}
	})
}
