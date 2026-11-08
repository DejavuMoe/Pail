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
	defer tableTheme(c)()
	return ui.Table(c, st, cols, n, cell)
}

// tableTheme makes the theme under which table rows are built: the Selection
// token as Accent, 8 spacing units (30px) per row, table font size. Call the
// returned func to restore the frame's real theme.
func tableTheme(c *ui.Context) (restore func()) { return tableThemeEdges(c, true) }

// tableThemeEdges is tableTheme that can leave the column edges out: mygo draws
// the resize edge of every resizable header cell in the theme's Border, at rest,
// and the anti-aliasing makes some of them look fainter than others. The object
// table (quiet) makes Border transparent so that no edge shows at rest, and draws
// the header's own 1px line itself (tableHeaderLine). The resize cursor and the
// drag stay; while the pointer is on an edge it shows in Selection.
func tableThemeEdges(c *ui.Context, edges bool) (restore func()) {
	k := tk(c)
	full := c.Theme()
	rowTheme := *full
	if !edges {
		rowTheme.Border = ui.Transparent
	}
	rowTheme.Accent, rowTheme.AccentText = k.Selection, k.Text
	// mygo fills chosen rows with InactiveAccent while the window is inactive;
	// left at the frame's Accent, they would turn solid under dark text.
	rowTheme.InactiveAccent = k.Selection
	rowTheme.Spacing = tableSpacing
	rowTheme.FontSize = sizeSecondary
	c.SetTheme(&rowTheme)
	return func() { c.SetTheme(full) }
}

// tableHeaderLine is the 1px line under a table's header, for a table built
// without the theme's Border (tableThemeEdges): it spans the table's width.
func tableHeaderLine(c *ui.Context) {
	ui.Box(c.Key("headline")).Absolute().Top(tableRowH).Left(0).Right(0).Height(1).Background(tk(c).Border).PassThrough()
}

// textAreaBox is a multi-line field styled like inputBox.
func textAreaBox(c *ui.Context, value *string, label, placeholder string, height float32, o inputOpts) ui.Element {
	k := tk(c)
	box := ui.Column(c.Key("tabox:"+label)).Height(height).Padding(6, 8).Radius(RadiusControl).Background(k.Surface)
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

// paneCreate is the create action of a list header: a ghost button with the
// plus icon and a visible label. Every tool's list header starts with one.
func paneCreate(c *ui.Context, label, tip string) ui.Element {
	return ghostButton(c, icons.Get("plus"), label, tip, false)
}

// paneIcon is any other action of a list header: an icon-only ghost button
// (28x28) with a Tooltip; label is its name for assistive technology.
func paneIcon(c *ui.Context, icon, label, tip string, disabled bool) ui.Element {
	return ghostButton(c, icons.Get(icon), "", tip, disabled).Label(label)
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
	r := ui.Row(c.Key("chip:"+icon+text)).Height(24).Padding(0, 8).Gap(6).Radius(RadiusControl).AlignItems(ui.Center).Shrink(0)
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

// toolListRow is the frame of a row in a tool's left-hand list: 8px outside, 12px
// inside, Selection when chosen. It returns the row for Clicked.
func toolListRow(c *ui.Context, key string, on bool) ui.Element {
	k := tk(c)
	r := ui.Row(c.Key(key)).Margin(0, 8).Padding(8, 12).Gap(8).Radius(RadiusControl).AlignItems(ui.Center).Transition(hoverFade())
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
