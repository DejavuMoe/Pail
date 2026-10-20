package main

import (
	"time"

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
	pos := sw.Animate("knob", target, 140*time.Millisecond)
	sw.Background(k.ControlOff.Mix(k.Accent, pos))
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
	sw.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if sw.FocusVisible() {
			p.FocusRing(r, [4]float32{switchH / 2, switchH / 2, switchH / 2, switchH / 2})
		}
	})
	return sw
}

// checkboxControl is a 16px check box with a label: a 1.5px BorderStrong box
// while unchecked, an Accent fill with a check mark once checked.
func checkboxControl(c *ui.Context, checked *bool, label string) ui.Element {
	k := tk(c)
	row := ui.CheckboxBase(c, checked).Label(label).Gap(8).AlignItems(ui.Center).FocusRing(false)
	on := *checked
	row.Children(func() {
		box := ui.Box(c).Size(checkSize, checkSize).Radius(4).Shrink(0)
		if on {
			box.Background(k.Accent).Border(1.5, k.Accent)
		} else {
			box.Background(k.Surface).Border(1.5, k.BorderStrong)
			if row.Hovered() {
				box.Border(1.5, k.TextMuted)
			}
		}
		box.DrawOver(func(p *ui.Painter, r ui.Rect) {
			if on {
				var path ui.Path
				path.MoveTo(r.X+r.W*0.24, r.Y+r.H*0.52).LineTo(r.X+r.W*0.43, r.Y+r.H*0.71).LineTo(r.X+r.W*0.77, r.Y+r.H*0.31)
				p.StrokePath(&path, 1.75, k.AccentText)
			}
			if row.FocusVisible() {
				p.FocusRing(r, [4]float32{4, 4, 4, 4})
			}
		})
		ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text)
	})
	return row
}

// selectionBar is the 2px Accent bar on the left edge of a selected row, the
// second cue next to the fill. inset keeps it clear of the row's top and
// bottom. The row must be a positioned element (any button or row is).
func selectionBar(c *ui.Context, inset float32) ui.Element {
	return ui.Box(c).Absolute().Left(1).Top(inset).Bottom(inset).Width(2).Radius(1).Background(tk(c).Accent)
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
