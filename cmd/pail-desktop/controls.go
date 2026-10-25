package main

import (
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
// on, a white knob that moves with the state.
func switchControl(c *ui.Context, on *bool, label string) ui.Element {
	k := tk(c)
	sw := ui.SwitchBase(c, on).Label(label).Size(switchW, switchH).Radius(switchH / 2).FocusRing(false)
	target := float32(0)
	if *on {
		target = 1
	}
	// The knob moves on the slide spring (it may overshoot a hair); the track
	// changes colour by a fade of its own, which stays under reduced motion.
	pos := sw.AnimateTo("knob", target, slideSpring)
	track := k.ControlOff
	if *on {
		track = k.Accent
	}
	sw.Background(sw.AnimateColor("track", track, fadeAnim))
	if sw.Hovered() {
		sw.Border(1, k.BorderStrong.Alpha(0.5))
	}
	sw.Draw(func(p *ui.Painter, r ui.Rect) {
		const in = 2
		d := r.H - 2*in
		knob := ui.Rect{X: r.X + in + pos*(r.W-r.H), Y: r.Y + in, W: d, H: d}
		p.Shadow(knob, d/2, 0, 1, 3, 0, k.KnobShadow)
		p.Fill(knob, k.Knob, d/2)
	})
	// Read before drawing: a frame that only paints (a pose moving, motion.go)
	// draws again without building, when an Element handle no longer holds.
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

// markFrom is the scale the check mark or dash grows from as a box is checked.
const markFrom = 0.85

// checkBox is the 16px box of a check box in state st: a 1.5px BorderStrong
// outline on Surface while off (TextMuted under the pointer), an Accent fill
// with an AccentText check mark, or a dash while mixed, otherwise. The fill
// fades in durFast; the mark grows from markFrom and fades in over
// durStandard. ring draws the focus ring around it.
func checkBox(c *ui.Context, st checkState, hover, ring bool) ui.Element {
	k := tk(c)
	box := ui.Box(c).Size(checkSize, checkSize).Radius(4).Shrink(0).Transition(hoverFade())
	if st != checkOff {
		box.Background(k.Accent).Border(1.5, k.Accent)
	} else {
		box.Background(k.Surface).Border(1.5, k.BorderStrong)
		if hover {
			box.Border(1.5, k.TextMuted)
		}
	}
	// The value starts where it is first asked for, so a row scrolled into
	// view shows its mark at rest; only a change of state animates.
	shown := float32(0)
	if st != checkOff {
		shown = 1
	}
	m := box.AnimateTo("mark", shown, fadeAnim)
	box.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if m > 0 {
			s := markFrom + (1-markFrom)*m
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			at := func(fx, fy float32) (float32, float32) {
				return cx + (r.X+r.W*fx-cx)*s, cy + (r.Y+r.H*fy-cy)*s
			}
			var path ui.Path
			if st == checkMixed {
				path.MoveTo(at(0.27, 0.5)).LineTo(at(0.73, 0.5))
			} else {
				path.MoveTo(at(0.24, 0.52)).LineTo(at(0.43, 0.71)).LineTo(at(0.77, 0.31))
			}
			p.StrokePath(&path, 1.75, k.AccentText.Alpha(m))
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

// selectionBar is the 2px Accent bar on the left edge of a selected row, the
// second cue next to the fill. inset keeps it clear of the row's top and
// bottom. The row must be a positioned element (any button or row is). The
// rows of a list that share a group (not "") slide the bar from the row that
// lost it to the one that has it; "" leaves it where it appears.
func selectionBar(c *ui.Context, inset float32, group string) ui.Element {
	bar := ui.Box(c).Absolute().Left(1).Top(inset).Bottom(inset).Width(2).Radius(1).Background(tk(c).Accent)
	if group != "" {
		bar.Transition(slideTransition(group))
	}
	return bar
}

// markSelected paints the same bar over a selected row from outside, for rows
// whose children are added by the caller (toolListRow).
func markSelected(c *ui.Context, row ui.Element, inset float32) {
	acc := tk(c).Accent
	row.DrawOver(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X + 1, Y: r.Y + inset, W: 2, H: r.H - 2*inset}, acc, 1)
	})
}

// buttonRow is the right-aligned row of dialog buttons.
func buttonRow(c *ui.Context, children func()) ui.Element {
	return ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(children)
}
