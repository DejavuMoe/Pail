package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The shape of the Settings page (docs/dev/design.md "设置页"): a rail of
// sections beside one scrolling column of groups. A group is a titled card of
// rows. A row has its label at the left and its control at the right edge of
// the card, so the controls of a group end on one line whatever their widths.

const (
	settingsColW     = 720 // the column of groups, at most
	settingsRailW    = 176 // the rail of sections
	settingsRailNeed = 880 // the page width that has room for the rail beside the column
	settingsPad      = 24  // around the column; the scrollbar's slot is inside it
	settingsGroupGap = 20  // above a group's title
	settingsRowH     = 44  // a row with a 28px control and 8px above and below
	settingsRowPad   = 14  // the inset of a row in its card
	settingsWideW    = 320 // a path, an address
	settingsSelW     = 220 // a drop-down
	settingsNumW     = 112 // a number and its unit
	settingsSpyLead  = 32  // how far above the top of the page a section already counts as current
)

// settingsSec is one section of the page: a group and its row in the rail.
type settingsSec struct {
	id    string
	title func() string
}

// settingsSecs are the sections in the order of the page.
var settingsSecs = []settingsSec{
	{"appearance", locales.SettingsAppearance},
	{"transfers", locales.SettingsTransfers},
	{"downloads", locales.SettingsDownloads},
	{"network", locales.SettingsNetwork},
	{"security", locales.VaultSecurity},
	{"diagnostics", locales.SettingsDiagnostics},
	{"updates", locales.SettingsUpdates},
	{"about", locales.SettingsAbout},
}

// settingsRail is what the page remembers between frames: how far it is
// scrolled, whether the rail shows, and which section the rail marks.
type settingsRail struct {
	scroll ui.ScrollState
	bp     breakpoint
	// pin is the section last chosen in the rail. It stays the current one until
	// the page is scrolled by hand: the last sections cannot reach the top of
	// the page, and the rail must still mark the one that was asked for. pinY is
	// where the page came to rest for it.
	pin    string
	pinY   float32
	pinSet bool
	// want is a section to go to once the page is laid out (openUpdates).
	want string
	// tops is where each section starts in the content, as the last frame laid
	// it out (Element.Bounds). It says where the page scrolls to and which row
	// the rail marks; nothing is laid out from it.
	tops        map[string]float32
	viewY, lasY float32
	rows        int // the rows drawn so far in the card being built
}

// goTo brings a section to the top of the page, as far as the page goes, and
// marks it in the rail.
func (n *settingsRail) goTo(id string) {
	top, ok := n.tops[id]
	if !ok {
		return // not laid out yet: there was no frame to choose it in either
	}
	n.pin, n.pinSet = id, false
	glideTo(&n.scroll, top)
}

// current is the section the rail marks: the pinned one, or the one the page
// stands in.
func (n *settingsRail) current() string {
	y := n.scroll.Y
	if n.pin != "" {
		switch {
		case n.scroll.Scrolling():
			n.pinSet = false
		case !n.pinSet:
			n.pinSet, n.pinY = true, y
		case y < n.pinY-1 || y > n.pinY+1:
			n.pin = ""
		}
		if n.pin != "" {
			return n.pin
		}
	}
	return n.spied()
}

// spied is the section the page stands in: the last one that starts at or
// above the top of the page; at the end of the page, the last one.
func (n *settingsRail) spied() string {
	y := n.scroll.Y
	cur, last := settingsSecs[0].id, ""
	for _, s := range settingsSecs {
		top, ok := n.tops[s.id]
		if !ok {
			continue
		}
		last = s.id
		if top <= y+settingsSpyLead {
			cur = s.id
		}
	}
	if last != "" && n.scroll.MaxY > 0 && y >= n.scroll.MaxY-1 {
		cur = last
	}
	return cur
}

// navRow is one row of a list of sections (the Settings rail, the nav of the
// bucket settings dialog): 28px high, ActiveFill and Text for the current
// one, TextMuted for the others (two cues). The fill comes from
// the row that was current before; g is the list's mark.
func navRow(c *ui.Context, g *markState, key, label string, on bool) ui.Element {
	k := tk(c)
	row := ui.ButtonBase(c.Key(key)).Label(label).Height(28).Padding(0, 8).Radius(RadiusControl).AlignItems(ui.Center).Justify(ui.Start)
	if !on && row.Hovered() {
		row.Background(k.SurfaceHover)
	}
	return row.Children(func() {
		col := k.TextMuted
		if on {
			col = k.Text
			currentFill(c, g, key, row)
		}
		ui.Text(c, label).FontSize(sizeBody).TextColor(col).SingleLine()
	})
}

// settingsRailView is the rail: one navRow per section. Choosing one glides
// the page to it.
func (a *app) settingsRailView(c *ui.Context, cur string) {
	n := &a.srail
	rail := ui.Column(c.Key("settings-rail")).Width(settingsRailW).Shrink(0).Padding(settingsPad, 0, settingsPad, 16).Gap(2).Label(locales.SettingsSections())
	fill := markOf(rail)
	rail.Children(func() {
		for _, s := range settingsSecs {
			if navRow(c, fill, "srail:"+s.id, s.title(), s.id == cur).Clicked() {
				n.goTo(s.id)
			}
		}
	})
}

// settingsGroup is one section of the page: its title and what body draws
// under it (a settingsCard, and any notice above it).
func (a *app) settingsGroup(c *ui.Context, id string, body func()) {
	n := &a.srail
	title := id
	for _, s := range settingsSecs {
		if s.id == id {
			title = s.title()
		}
	}
	col := ui.Column(c.Key("ssec:"+id)).Padding(settingsGroupGap, 0, 0, 0).Gap(8)
	// Where the group starts in the content: its place in the last frame, less
	// the page's, plus how far the page was scrolled then. The sections do not
	// move while the page glides, so a gliding frame is not measured.
	if b := col.Bounds(); b.H > 0 && !n.scroll.Scrolling() {
		if n.tops == nil {
			n.tops = map[string]float32{}
		}
		n.tops[id] = b.Y - n.viewY + n.lasY
	}
	col.Children(func() {
		sectionLabel(c, title).MarginStart(2)
		body()
	})
}

// settingsCard is the card of a group: the Card fill with a hairline outline.
// Its rows part themselves with hairlines.
func (a *app) settingsCard(c *ui.Context, rows func()) {
	k := tk(c)
	a.srail.rows = 0
	ui.Column(c).Radius(RadiusPopover).Background(k.Card).Border(1, k.Border).Children(rows)
}

// settingsRow is a row of a card without a "?".
func (a *app) settingsRow(c *ui.Context, label, err string, control func()) {
	a.settingsRowHelp(c, label, "", err, control)
}

// settingsRowHelp is a row of a card: the label and its "?" (when help is set)
// at the left, what control draws at the right. notes are muted lines under
// the label, for a fact about the setting as it is now; err, why the last edit
// was not saved, comes under them.
func (a *app) settingsRowHelp(c *ui.Context, label, help, err string, control func(), notes ...string) {
	k := tk(c)
	if a.srail.rows > 0 {
		hairline(c)
	}
	a.srail.rows++
	row := ui.Row(c.Key("srow:"+label)).MinHeight(settingsRowH).Padding(8, settingsRowPad).Gap(16).AlignItems(ui.Center)
	row.Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(2).MinHeight(helpHitSize).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(sizeBody).TextColor(k.Text).SingleLine().Shrink(1).MinWidth(0).Tooltip(label)
				if help != "" {
					// The "?" of every row at once is noise: it shows on the row
					// under the pointer or with the keyboard on it, and keeps its
					// room meanwhile, so nothing moves.
					if q := helpIcon(c, help); !row.Hovered() && !q.FocusVisible() {
						q.Opacity(0)
					}
				}
			})
			for _, s := range notes {
				ui.Text(c, s).FontSize(sizeSecondary).TextColor(k.TextMuted)
			}
			if err != "" {
				ui.Text(c, err).FontSize(sizeSecondary).TextColor(k.Danger)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Shrink(1).MinWidth(0).Children(control)
	})
}

// settingsActions is the last row of a card for what applies the rows above
// it: buttons at the right, under the controls.
func (a *app) settingsActions(c *ui.Context, key string, body func()) {
	if a.srail.rows > 0 {
		hairline(c)
	}
	a.srail.rows++
	ui.Column(c.Key(key)).Padding(8, settingsRowPad).Gap(4).AlignItems(ui.End).Children(body)
}

// The theme pictures: a window in miniature, in the colours of the theme it
// stands for. They are drawn from the tokens, so they follow the palette.
const (
	thumbW, thumbH = 60, 38
	thumbPad       = 3                        // between the picture and the ring around it
	thumbRadii     = RadiusControl - thumbPad // concentric with the ring
	thumbSide      = 16                       // the sidebar of the picture
)

// themePick is one picture of a themePicker: its name and the palette it is
// drawn in. With half it shows two, the left half in k and the right in half:
// the desktop's own theme, which is either.
type themePick struct {
	label string
	k     tokens
	half  *tokens
}

// themePicker is a choice among themes or palettes: a picture of each with its
// name under it, one radio group. The current one has an Accent ring, which
// comes from the picture chosen before (arrive), and its name in Text at
// weight 600 (two cues). It reports a new choice.
func themePicker(c *ui.Context, sel *int, label string, picks []themePick) bool {
	k := tk(c)
	seg := ui.SegmentedBase(c.Key("seg:"+label), sel, len(picks))
	seg.Track.Gap(8).Label(label).Shrink(0)
	ring := markOf(seg.Track)
	seg.Track.Children(func() {
		for i, pk := range picks {
			on := i == *sel
			s := seg.Segment(i).Label(pk.label).Column().Gap(4).AlignItems(ui.Center).Radius(RadiusControl).Shrink(0)
			edge := k.BorderStrong
			if !on && s.Hovered() {
				edge = k.TextSubtle
			}
			s.Children(func() {
				ui.Row(c).Size(thumbW+2*thumbPad, thumbH+2*thumbPad).Padding(thumbPad).Radius(RadiusControl).Border(1, edge).Children(func() {
					if on {
						// Over the 1px edge, which every picture keeps so that none
						// changes size as it is chosen, and over the pictures it
						// crosses.
						s.ZIndex(1)
						arrive(c, ring, i, s, func(m ui.Element) {
							m.Children(func() {
								ui.Box(c).Absolute().Left(-1).Top(-1).Right(-1).Bottom(-1).Radius(RadiusControl).Border(2, k.Accent)
							})
						})
					}
					ui.Row(c).Grow(1).Radius(thumbRadii).Clip().Children(func() {
						themeThumb(c, pk.k, true)
						if pk.half != nil {
							themeThumb(c, *pk.half, false)
						}
					})
				})
				col := k.TextMuted
				if on {
					col = k.Text
				}
				ui.Text(c, pk.label).FontSize(sizeSecondary).FontWeight(500).TextColor(col).SingleLine()
			})
		}
	})
	return seg.Track.Changed()
}

// themeThumb is a window in t's colours: a sidebar (with side) and a list
// with one row chosen, its mark in the palette's Accent.
func themeThumb(c *ui.Context, t tokens, side bool) {
	bar := func(w float32, col ui.Color) { ui.Box(c).Width(w).Height(3).Background(col) }
	// MinWidth(0) and Clip: two of them share the picture half and half,
	// whatever their content would take.
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).Clip().Background(t.Background).Children(func() {
		if side {
			ui.Column(c).Width(thumbSide).Shrink(0).Background(t.Sidebar).Padding(6, 4).Gap(3).Children(func() {
				bar(8, t.TextSubtle)
				bar(6, t.BorderStrong)
				bar(7, t.BorderStrong)
			})
		}
		ui.Column(c).Grow(1).MinWidth(0).Padding(6, 5).Gap(3).Children(func() {
			bar(14, t.TextMuted)
			ui.Row(c).Height(5).Background(t.Selection).Children(func() { ui.Box(c).Width(5).Background(t.Accent) })
			bar(18, t.BorderStrong)
			bar(11, t.BorderStrong)
		})
	})
}
