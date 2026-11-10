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
	pos := target // the knob's place: 0 left, 1 right
	track := k.ControlOff
	if *on {
		track = k.Accent
	}
	sw.Background(track)
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
