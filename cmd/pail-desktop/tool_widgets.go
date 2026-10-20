package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// Widgets shared by the tool views (rules, MIME, multipart, CloudFront, sync).

// toolTable is the object table's look for a tool: 30px rows, the Selection
// token for chosen rows, 12px cells. The rows are built under a theme whose
// Accent is that token; the rest of the frame keeps the real Accent.
func toolTable(c *ui.Context, st *ui.ListState, cols []ui.TableColumn, n int, cell func(row, col int)) ui.Element {
	k := tk(c)
	full := c.Theme()
	rowTheme := *full
	rowTheme.Accent, rowTheme.AccentText = k.Selection, k.Text
	rowTheme.Spacing = 3.75
	rowTheme.FontSize = sizeSecondary
	c.SetTheme(&rowTheme)
	defer c.SetTheme(full)
	return ui.Table(c, st, cols, n, cell)
}

// textAreaBox is a multi-line field styled like inputBox.
func textAreaBox(c *ui.Context, value *string, label, placeholder string, height float32, o inputOpts) ui.Element {
	k := tk(c)
	box := ui.Column(c.Key("tabox:"+label)).Height(height).Padding(6, 8).Radius(6).Background(k.Surface)
	switch {
	case o.invalid:
		box.Border(1, k.Danger)
	case box.FocusWithin():
		box.Border(1, k.Focus)
	default:
		box.Border(1, k.BorderStrong)
	}
	var in ui.Element
	box.Children(func() {
		in = ui.TextAreaBase(c.Key("ta:"+label), value).Placeholder(placeholder).Label(label).FontSize(sizeBody).Grow(1).MinHeight(0)
		if o.mono {
			mono(in)
		}
	})
	return in
}

const noneVal = "·none"

// selectNone is selectBox for a value that may be "" (the none choice).
func selectNone(c *ui.Context, label string, value *string, opts []choice, width float32) bool {
	v := *value
	if v == "" {
		v = noneVal
	}
	mapped := make([]choice, len(opts))
	for i, o := range opts {
		if o.Value == "" {
			o.Value = noneVal
		}
		mapped[i] = o
	}
	if !selectBox(c, label, &v, mapped, width) {
		return false
	}
	if v == noneVal {
		v = ""
	}
	*value = v
	return true
}

// paneBar is the 40px strip above a list or table: ghost buttons on the left.
func paneBar(c *ui.Context, key string, children func()) {
	ui.Row(c.Key(key)).Height(40).Padding(0, 8).Gap(2).AlignItems(ui.Center).Shrink(0).Children(children)
}

// footerBar is the 48px action strip under a form, with a hairline above it.
func footerBar(c *ui.Context, key string, children func()) {
	k := tk(c)
	ui.Row(c.Key(key)).Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).Shrink(0).
		BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(children)
}

// cell wraps a form field so that it shares a Row evenly with its siblings.
func cell(c *ui.Context, key string, children func()) {
	ui.Box(c.Key(key)).Grow(1).Basis(0).MinWidth(0).Children(children)
}

// chip is a small summary tag: an icon and a 12px label. col tints both; the
// fill is Sidebar, or a Danger wash when danger is set.
func chip(c *ui.Context, icon, text string, col ui.Color, danger bool) {
	k := tk(c)
	r := ui.Row(c.Key("chip:"+icon+text)).Height(24).Padding(0, 8).Gap(6).Radius(6).AlignItems(ui.Center).Shrink(0)
	if danger {
		r.Background(k.Danger.Alpha(0.10))
	} else {
		r.Background(k.Sidebar)
	}
	r.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(col)
		ui.Text(c, text).FontSize(sizeSecondary).TextColor(col).SingleLine()
	})
}

// listRow is the frame of a row in a tool's left-hand list: 8px outside, 12px
// inside, Selection when chosen. It returns the row for Clicked.
func listRow(c *ui.Context, key string, on bool) ui.Element {
	k := tk(c)
	r := ui.Row(c.Key(key)).Margin(0, 8).Padding(8, 12).Gap(8).Radius(6).AlignItems(ui.Center)
	switch {
	case on:
		r.Background(k.Selection)
		markSelected(c, r, 8)
	case r.Hovered():
		r.Background(k.SurfaceHover)
	}
	return r
}

// toolPane is a fixed-width left pane with a hairline on its right.
func toolPane(c *ui.Context, key string, w float32, children func()) {
	k := tk(c)
	ui.Column(c.Key(key)).Width(w).Shrink(0).MinHeight(0).BorderWidth(0, 1, 0, 0).BorderColor(k.Border).Children(children)
}
