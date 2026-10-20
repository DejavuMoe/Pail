package main

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// The command palette (DESIGN.md "Window appearance"): Ctrl/Cmd+K or
// Ctrl/Cmd+Shift+P opens a search box over a list of every command in the
// command table, plus a jump to each bucket. The keyboard drives it (arrows,
// Enter, Escape); the mouse works too.

const (
	paletteW    = 560
	paletteMaxH = 420
	paletteRowH = 32
)

type paletteState struct {
	open   bool
	query  string
	seen   string // the query the list was last filtered with
	cur    int    // the row Enter runs
	reveal bool   // scroll the current row into view (the keyboard moved it)
	scroll ui.ScrollState
}

// palEntry is one row of the palette. label is the localized text shown;
// english is searched too, so "upload" finds the command in a Chinese UI;
// group is the group id (commands.go) and heading its localized name; hay is
// everything the query is matched against, lower-cased.
type palEntry struct {
	label, english, group, heading, hay, shortcut string
	enabled, current                              bool // current: the active choice of a group, shown with a check
	run                                           func()
}

func (a *app) openPalette() { a.pal = paletteState{open: true} }

// paletteEntries lists what the palette can run now: the commands of the
// table that apply (a command with a Visible test that fails is left out) and
// a jump to each bucket of the account, in the order of the group headings.
// Commands that cannot run on the current screen are listed but disabled.
func (a *app) paletteEntries() []palEntry {
	m, en := locales.In(i18n.Locale()), locales.In("en")
	var out []palEntry
	for i := range a.commands {
		cmd := &a.commands[i]
		if cmd.Visible != nil && !cmd.Visible(a) {
			continue
		}
		id := cmd.ID
		e := palEntry{
			label: strings.TrimSuffix(cmd.text(), "…"), english: strings.TrimSuffix(cmd.english(), "…"),
			group: cmd.Group, heading: groupName(m, cmd.Group),
			shortcut: cmd.hint(), enabled: a.enabled(cmd),
			run: func() { a.run(id, "palette") },
		}
		// Every language of the app is searched, so "upload" and "上传" both find
		// the command whichever language is shown.
		words := []string{e.label, e.english, e.heading}
		for _, tag := range appLanguages {
			lm := locales.In(tag)
			words = append(words, cmd.Label(lm), groupName(lm, cmd.Group))
			if cmd.Aliases != nil {
				words = append(words, cmd.Aliases(lm))
			}
		}
		e.hay = searchText(words...)
		if mode, ok := strings.CutPrefix(id, "theme."); ok {
			e.current = a.themeMode() == mode
		}
		if tag, ok := strings.CutPrefix(id, "lang."); ok {
			e.current = a.lang == tag || tag == "system" && a.lang == ""
		}
		out = append(out, e)
	}
	if a.hasAccount() {
		for _, b := range a.buckets {
			name := b.Name
			e := palEntry{
				label: m.PaletteOpenBucket(name), english: en.PaletteOpenBucket(name),
				group: groupNavigate, heading: groupName(m, groupNavigate), enabled: true,
				current: a.tool == "" && a.scope.Bucket == name,
				run:     func() { a.openBucket(name) },
			}
			e.hay = searchText(e.label, e.english, e.heading, groupName(en, groupNavigate), b.Region, "browse")
			out = append(out, e)
		}
	}
	rank := func(g string) int {
		if i := slices.Index(paletteGroups, g); i >= 0 {
			return i
		}
		return len(paletteGroups)
	}
	slices.SortStableFunc(out, func(x, y palEntry) int { return rank(x.group) - rank(y.group) })
	return out
}

// searchText joins the words a query is matched against.
func searchText(parts ...string) string { return strings.ToLower(strings.Join(parts, " ")) }

// filterPalette keeps the entries that contain every word of query in their
// label (in the shown language or in English), heading or aliases
// (case-insensitive). Better matches come first: a label that starts with the
// query, then one with a word that does, then one that contains it, then the
// rest; among equal ones, what can run now before what cannot. With no query
// the list keeps its group order, enabled commands first and the disabled
// ones after, dimmed.
func filterPalette(all []palEntry, query string) []palEntry {
	words := strings.Fields(strings.ToLower(query))
	type scored struct {
		e     palEntry
		score int
	}
	var hits []scored
	whole := strings.Join(words, " ")
	for _, e := range all {
		s := 0
		if len(words) > 0 {
			ok := true
			for _, w := range words {
				if !strings.Contains(e.hay, w) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			s = 3
			for _, label := range []string{strings.ToLower(e.label), strings.ToLower(e.english)} {
				switch {
				case strings.HasPrefix(label, whole):
					s = min(s, 0)
				case hasWordPrefix(label, whole):
					s = min(s, 1)
				case strings.Contains(label, whole):
					s = min(s, 2)
				}
			}
		}
		hits = append(hits, scored{e, s})
	}
	slices.SortStableFunc(hits, func(x, y scored) int {
		if x.score != y.score {
			return x.score - y.score
		}
		return b2i(!x.e.enabled) - b2i(!y.e.enabled)
	})
	out := make([]palEntry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// hasWordPrefix reports whether a word of s (a run of letters or digits) starts
// with prefix.
func hasWordPrefix(s, prefix string) bool {
	for i, r := range s {
		if i == 0 || !unicode.IsLetter(prevRune(s, i)) && !unicode.IsDigit(prevRune(s, i)) {
			if (unicode.IsLetter(r) || unicode.IsDigit(r)) && strings.HasPrefix(s[i:], prefix) {
				return true
			}
		}
	}
	return false
}

func prevRune(s string, i int) rune {
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// stepPalette moves the cursor by d rows (1 or -1), wrapping round and
// skipping the rows that cannot run; with none that can, it stays.
func stepPalette(entries []palEntry, cur, d int) int {
	n := len(entries)
	for i := 1; i <= n; i++ {
		if j := ((cur+d*i)%n + n) % n; entries[j].enabled {
			return j
		}
	}
	return cur
}

// firstEnabled is the first row that can run, 0 when none can.
func firstEnabled(entries []palEntry) int {
	for i, e := range entries {
		if e.enabled {
			return i
		}
	}
	return 0
}

// runPalette closes the palette and runs the entry on the next frame, after
// the dialog has given the keyboard back, so that a command that opens a
// dialog or takes the focus is not undone by the palette closing.
func (a *app) runPalette(e palEntry) {
	if !e.enabled {
		return
	}
	a.pal.open = false
	a.post(e.run)
}

func (a *app) paletteDialog(c *ui.Context) {
	p := &a.pal
	if !p.open {
		return
	}
	k := tk(c)
	winW, winH := c.Size()
	entries := filterPalette(a.paletteEntries(), p.query)
	if p.query != p.seen {
		p.seen, p.cur = p.query, firstEnabled(entries)
		p.scroll = ui.ScrollState{}
	}
	p.cur = max(0, min(p.cur, len(entries)-1))

	ui.DialogBase(c, &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.ScrimSoft).Justify(ui.Start).Padding(max(24, min(96, (winH-paletteMaxH)/4)), 16, 16, 16)
		dialogFrame(c, panel, min(paletteW, winW-32), winH)
		panel.MaxHeight(min(paletteMaxH, max(240, winH-96)))

		move := func(d int) {
			if len(entries) > 0 {
				p.cur, p.reveal = stepPalette(entries, p.cur, d), true
			}
		}
		switch {
		case c.Shortcut(0, ui.KeyDown):
			move(1)
		case c.Shortcut(0, ui.KeyUp):
			move(-1)
		case c.Shortcut(0, ui.KeyPageDown):
			for i := 0; i < 8; i++ {
				move(1)
			}
		case c.Shortcut(0, ui.KeyPageUp):
			for i := 0; i < 8; i++ {
				move(-1)
			}
		case c.Shortcut(ui.Cmd, ui.KeyK), c.Shortcut(ui.Cmd|ui.Shift, ui.KeyP):
			p.open = false // the key that opened it closes it
		}

		ui.Row(c).Height(48).Padding(0, 16).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, icons.Get("search")).FontSize(16).TextColor(k.TextMuted)
			in := ui.TextInputBase(c.Key("palette-input"), &p.query).Label(locales.PaletteLabel()).
				Placeholder(locales.PaletteSearch()).FontSize(15).Grow(1).MinWidth(0).AutoFocus()
			if in.Submitted() && len(entries) > 0 {
				a.runPalette(entries[p.cur])
			}
			ui.Text(c, locales.PaletteEsc()).FontSize(sizeLabel).TextColor(k.TextSubtle).Padding(1, 5).Radius(4).Border(1, k.Border).Shrink(0)
		})
		hairline(c).Shrink(0)
		if len(entries) == 0 {
			ui.Text(c, locales.PaletteNoMatch()).FontSize(sizeBody).TextColor(k.TextMuted).Padding(24, 16).TextAlign(ui.Center).Shrink(0)
			return
		}
		ui.Scroll(c).TrackScroll(&p.scroll).Shrink(1).MinHeight(0).ScrollbarInsets(0, scrollBarInset, 0, 0).KeepFocus().Children(func() {
			ui.Column(c).Padding(4, scrollGutter, 4, 8).Children(func() {
				for i, e := range entries {
					// The commands that cannot run here come after a quiet label.
					divide := strings.TrimSpace(p.query) == "" && !e.enabled && i > 0 && entries[i-1].enabled
					if divide {
						ui.Text(c, strings.ToUpper(locales.PaletteUnavailable())).FontSize(sizeLabel).FontWeight(600).TextColor(k.TextSubtle).SingleLine().Padding(10, 10, 4, 10).Shrink(0)
					}
					showGroup := i == 0 || divide || entries[i-1].group != e.group
					a.paletteRow(c, e, i == p.cur, showGroup)
				}
			})
		})
	})
}

// paletteRow is one row: the heading (on the first row of a run), the label,
// then the shortcut or a check. The current row carries the two cues: a
// Selection fill and a 2px Accent bar. A disabled command is dimmed.
func (a *app) paletteRow(c *ui.Context, e palEntry, current, showGroup bool) {
	k := tk(c)
	p := &a.pal
	row := ui.ButtonBase(c.Key("pal:"+e.group+":"+e.english)).Label(e.label).Height(paletteRowH).Padding(0, 10).Gap(8).Radius(6).AlignItems(ui.Center).Shrink(0)
	switch {
	case !e.enabled:
		row.Opacity(0.4)
	case current:
		row.Background(k.Selection)
	case row.Hovered():
		row.Background(k.SurfaceHover)
	}
	row.Children(func() {
		w := 400
		if current && e.enabled {
			w = 600
			selectionBar(c, 6)
		}
		heading := ""
		if showGroup {
			heading = e.heading
		}
		ui.Text(c, heading).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Width(88).Shrink(0)
		ui.Text(c, e.label).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
		switch {
		case e.current:
			ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(k.Accent).Shrink(0)
		case e.shortcut != "":
			ui.Text(c, e.shortcut).FontSize(sizeSecondary).TextColor(k.TextSubtle).SingleLine().Shrink(0)
		}
	})
	if current && p.reveal {
		row.ScrollIntoView()
		p.reveal = false
	}
	if row.Clicked() && e.enabled {
		a.runPalette(e)
	}
}
