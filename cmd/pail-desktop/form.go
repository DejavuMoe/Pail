package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// Form controls shared by the account dialog and the settings view. They all
// follow DESIGN.md: 28px high, Surface fill, BorderStrong outline, Focus ring,
// Danger outline when invalid.

// fieldLabel is the 12px subtle caption above a control.
func fieldLabel(c *ui.Context, s string) ui.Element {
	// Labels are essential text: TextMuted (5.6:1), weight 500, so a field's
	// label never reads like its placeholder.
	// The tooltip shows the whole caption when a narrow column cuts it.
	return ui.Text(c, s).FontSize(sizeSecondary).FontWeight(500).TextColor(tk(c).TextMuted).SingleLine().Tooltip(s)
}

// inputOpts tune inputBox.
type inputOpts struct {
	password bool
	mono     bool
	invalid  bool
	focus    bool // take the keyboard when it appears
	width    float32
	disabled bool
}

// inputBox is a 28px single-line text field. It returns the input element for
// Changed / Submitted.
func inputBox(c *ui.Context, value *string, label, placeholder string, o inputOpts) ui.Element {
	k := tk(c)
	row := ui.Row(c.Key("box:"+label)).Height(28).Padding(0, 8).Radius(6).AlignItems(ui.Center).Background(k.Surface)
	if o.width > 0 {
		row.Width(o.width).Shrink(0)
	}
	switch {
	case o.invalid:
		row.Border(1, k.Danger)
	case row.FocusWithin():
		row.Border(1, k.Focus)
	default:
		row.Border(1, k.BorderStrong)
	}
	if o.disabled {
		row.Opacity(0.5)
	}
	var in ui.Element
	row.Children(func() {
		in = ui.TextInputBase(c.Key("in:"+label), value).Placeholder(placeholder).Label(label).FontSize(sizeBody).Grow(1).MinWidth(0)
		if o.password {
			in.Password()
		}
		if o.mono {
			mono(in)
		}
		if o.focus {
			in.AutoFocus()
		}
		if o.disabled {
			in.Disabled(true)
		}
	})
	return in
}

// formField is a labeled control with an optional error line under it.
func formField(c *ui.Context, label, err string, control func()) {
	ui.Column(c.Key("field:" + label)).Gap(4).Children(func() {
		fieldLabel(c, label)
		control()
		if err != "" {
			ui.Text(c, err).FontSize(sizeSecondary).TextColor(tk(c).Danger)
		}
	})
}

// segmented is a two-to-four way choice. It returns true on a new choice.
func segmented(c *ui.Context, sel *int, label string, labels ...string) bool {
	return segmentedOff(c, sel, label, nil, labels...)
}

// segmentedOff is segmented with some segments switched off (dimmed, not
// choosable); off may be shorter than labels.
//
// The track is a recessed Track well with a hairline; the current segment is
// lifted out of it: Raised fill, BorderStrong outline, a soft shadow, Text at
// weight 600. Fill alone would differ by about 1.2:1, so the outline, shadow
// and weight carry the state (DESIGN.md "States").
func segmentedOff(c *ui.Context, sel *int, label string, off []bool, labels ...string) bool {
	k := tk(c)
	seg := ui.SegmentedBase(c.Key("seg:"+label), sel, len(labels))
	seg.Track.Height(30).Padding(2).Gap(2).Radius(7).Background(k.Track).Border(1, k.Border).Label(label).Shrink(0)
	seg.Track.Children(func() {
		for i, l := range labels {
			disabled := i < len(off) && off[i]
			segmentFace(c, seg.Segment(i), l, i == *sel, disabled)
		}
	})
	return seg.Track.Changed()
}

// labelWidth estimates the width of a 13px label at weight 600: Latin letters
// about 8.2px, wide (CJK, kana, full-width) characters about 14px.
func labelWidth(s string) float32 {
	var w float32
	for _, r := range s {
		if r >= 0x2E80 {
			w += 14
		} else {
			w += 8.2
		}
	}
	return w
}

// segmentFace styles one segment: see segmentedOff for the look.
func segmentFace(c *ui.Context, s ui.Element, label string, on, disabled bool) {
	k := tk(c)
	s.Grow(1).Shrink(0).Height(24).Padding(0, 14).Radius(5).Center().Label(label).Disabled(disabled).
		MinWidth(labelWidth(label) + 34) // never ellipsize a short label (600 weight is wider)
	w, col := 500, k.TextMuted
	switch {
	case disabled:
		s.Opacity(0.4)
	case on:
		s.Background(k.Raised).Border(1, k.BorderStrong).Shadow(0, 1, 2, 0, k.Shadow)
		w, col = 600, k.Text
	case s.Hovered():
		s.Background(k.SurfaceHover)
		col = k.Text
	}
	s.Children(func() {
		ui.Text(c, label).FontSize(sizeBody).FontWeight(w).TextColor(col).SingleLine()
	})
}

// segmentedAction is the look of segmented for a choice that starts something
// (a dialog, a move) instead of recording a value. cur is the segment that is
// really in effect; it is shown every frame and never changed here. It returns
// the segment the user activated, if it is not cur.
//
// It is built from plain buttons, not from a radio group, on purpose: a radio
// group "selects" every segment the focus moves to, so an arrow key (or a
// stale Changed report from the throwaway local that such a control edits)
// would start the action without anyone having asked for it. Here only a
// click, Enter or Space on a segment does.
func segmentedAction(c *ui.Context, cur int, label string, labels ...string) (int, bool) {
	k := tk(c)
	chosen := -1
	ui.Row(c.Key("segact:"+label)).Height(30).Padding(2).Gap(2).Radius(7).Background(k.Track).Border(1, k.Border).Label(label).Shrink(0).Children(func() {
		for i, l := range labels {
			s := ui.ButtonBase(c.Key("segact:" + label + ":" + l))
			segmentFace(c, s, l, i == cur, false)
			if s.Clicked() && i != cur {
				chosen = i
			}
		}
	})
	return chosen, chosen >= 0
}

// choice is one option of selectBox.
type choice struct{ Value, Label string }

// selectBox is a drop-down styled like an input. It returns true on a new
// choice.
func selectBox(c *ui.Context, label string, value *string, opts []choice, width float32) bool {
	k := tk(c)
	sel := ui.SelectBase(c.Key("sel:"+label), value)
	shown := *value
	for _, o := range opts {
		if o.Value == *value {
			shown = o.Label
		}
	}
	sel.Trigger.Height(28).Padding(0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Background(k.Surface).Label(label)
	if width > 0 {
		sel.Trigger.Width(width).Shrink(0)
	}
	if sel.Trigger.Hovered() {
		sel.Trigger.Background(k.SurfaceHover)
	}
	if sel.Trigger.FocusWithin() {
		sel.Trigger.Border(1, k.Focus)
	} else {
		sel.Trigger.Border(1, k.BorderStrong)
	}
	sel.Trigger.Children(func() {
		ui.Text(c, shown).FontSize(sizeBody).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
		ui.Icon(c, icons.Get("chevron-down")).FontSize(14).TextColor(k.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Margin(2, 0, 0, 0).Padding(4).Radius(8).Background(k.Surface).Border(1, k.BorderStrong).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.14))
		for _, o := range opts {
			// The current option carries Selection, an Accent bar, a check and
			// weight 600; the pointer's option only a SurfaceHover fill.
			current := o.Value == *value
			item := sel.Item(o.Value).Padding(0, 8).Height(28).Radius(6).Gap(8).AlignItems(ui.Center)
			switch {
			case current:
				item.Background(k.Selection)
			case item.Highlighted():
				item.Background(k.SurfaceHover)
			}
			item.Children(func() {
				w := 400
				if current {
					w = 600
					selectionBar(c, 6)
				}
				ui.Text(c, o.Label).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
				if current {
					ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(k.Accent).Shrink(0)
				}
			})
		}
	})
	return sel.Trigger.Changed()
}

// switchRow is a label with a switch at the right.
func switchRow(c *ui.Context, label string, on *bool) bool {
	k := tk(c)
	var changed bool
	ui.Row(c.Key("switch:" + label)).Height(28).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).Grow(1).SingleLine()
		changed = switchControl(c, on, label).Changed()
	})
	return changed
}

// emptyBlock is a centered empty state: 32px icon, 13px title, 12px subtitle.
func emptyBlock(c *ui.Context, icon, title, sub string, height float32) {
	k := tk(c)
	col := ui.Column(c).Center().Gap(4)
	if height > 0 {
		col.Height(height)
	} else {
		col.Grow(1)
	}
	col.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(32).TextColor(k.TextSubtle).MarginY(4)
		ui.Text(c, title).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
		if sub != "" {
			ui.Text(c, sub).FontSize(sizeSecondary).TextColor(k.TextMuted).TextAlign(ui.Center)
		}
	})
}
