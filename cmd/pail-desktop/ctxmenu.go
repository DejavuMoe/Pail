package main

import (
	"runtime"
	"strconv"
	"time"
	"unicode"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// Menus (docs/dev/design.md "Overlays", "Menus"): the context menus of the object
// table and the sidebar buckets, and the menus of the Upload, account and "⋯"
// buttons. A call site describes its menu once with a menuBuilder; macOS shows
// the description as the system's menu (the platform's convention), Windows
// and Linux draw it in the window, in the look of the other overlays, with
// icons and keycaps.

// nativeMenus says the system draws the menus: macOS. The tests turn it off,
// so that they drive the in-window menus on every platform, and back on for
// the native path.
var nativeMenus = runtime.GOOS == "darwin"

// The in-window menu's geometry and timing.
const (
	menuMinW = 200
	menuMaxW = 320
	// menuWindowMargin is mygo's room between an overlay and the window edge
	// (layout.go windowMargin): a menu taller than the window minus twice this
	// scrolls.
	menuWindowMargin = 4
	// submenuDelay is how long the pointer rests on a row before its submenu
	// opens, or before another row's submenu closes.
	submenuDelay = 150 * time.Millisecond
)

// ---- the description --------------------------------------------------------------------------

// menuItem is one row of a menu: an item, a separator or a submenu.
type menuItem struct {
	label     string
	icon      string // Lucide name, "" for none
	shortcut  *chord
	checkable bool // Checked or Radio was called: it shows a check when on
	radio     bool // Radio was called: a menuitemradio, one choice of a group
	checked   bool
	disabled  bool
	separator bool
	submenu   bool
	sub       []*menuItem
	action    func()
}

// menuBuilder collects a menu's items. Its methods mirror ui.Menu's so that a
// call site reads as it did with the native menu, with the action passed to
// Item instead of tested with Chosen.
type menuBuilder struct {
	items []*menuItem
	// keyboard is set while a menu opened by Shift+F10 or the menu key is
	// built: the build may then ignore where the pointer is.
	keyboard bool
}

// Item adds an item showing label; choosing it runs action.
func (m *menuBuilder) Item(label string, action func()) *menuItem {
	it := &menuItem{label: label, action: action}
	m.items = append(m.items, it)
	return it
}

// Separator adds a line between groups. Separators at either end or next to
// each other are dropped (tidy), so a call site may add one per group.
func (m *menuBuilder) Separator() { m.items = append(m.items, &menuItem{separator: true}) }

// Submenu adds an item showing label that opens a submenu, which build fills.
func (m *menuBuilder) Submenu(label string, build func(m *menuBuilder)) *menuItem {
	sub := &menuBuilder{keyboard: m.keyboard}
	build(sub)
	it := &menuItem{label: label, submenu: true, sub: tidyMenu(sub.items)}
	m.items = append(m.items, it)
	return it
}

// Command adds the command id of the command table: its label, icon and first
// shortcut, disabled while it cannot run. Choosing it runs the command; from
// names the source (see app.run).
func (m *menuBuilder) Command(a *app, id, from string) *menuItem {
	cmd := a.cmd(id)
	it := m.Item(cmd.text(), func() { a.run(id, from) }).Icon(cmd.Icon).Disabled(!a.enabled(cmd))
	if len(cmd.Chords) > 0 {
		it.Shortcut(cmd.Chords[0])
	}
	return it
}

// Icon shows the Lucide icon name before the label (in-window only: the
// system's menus show none).
func (it *menuItem) Icon(name string) *menuItem { it.icon = name; return it }

// Checked shows a check when on: the current value of a choice.
func (it *menuItem) Checked(on bool) *menuItem { it.checkable, it.checked = true, on; return it }

// Radio shows a check when on, as Checked does, for the item that is the
// current one of a group of which one is chosen (a theme, a language, an
// account): assistive technology hears a radio item, as in the native menu
// bar's Language menu.
func (it *menuItem) Radio(on bool) *menuItem {
	it.checkable, it.radio, it.checked = true, true, on
	return it
}

// Disabled dims the item: it cannot be chosen, and the keyboard skips it.
func (it *menuItem) Disabled(d bool) *menuItem { it.disabled = d; return it }

// Shortcut shows ch by the item, as the key that does the same; the window
// handles the key itself (pollShortcuts).
func (it *menuItem) Shortcut(ch chord) *menuItem { it.shortcut = &ch; return it }

// tidyMenu drops the separators at either end and the second of two in a row.
func tidyMenu(items []*menuItem) []*menuItem {
	out := items[:0:0]
	for _, it := range items {
		if it.separator && (len(out) == 0 || out[len(out)-1].separator) {
			continue
		}
		out = append(out, it)
	}
	for len(out) > 0 && out[len(out)-1].separator {
		out = out[:len(out)-1]
	}
	return out
}

// nativeItems fills a system menu with items. mygo runs the menu's function
// as the menu opens and again in the frame after a choice, where Chosen
// reports it: the action runs then.
func nativeItems(nm *ui.Menu, items []*menuItem) {
	for _, it := range items {
		switch {
		case it.separator:
			nm.Separator()
		case it.submenu:
			nm.Submenu(it.label, func(sm *ui.Menu) { nativeItems(sm, it.sub) }).Disabled(it.disabled)
		default:
			ni := nm.Item(it.label).Disabled(it.disabled)
			switch {
			case it.radio:
				ni.Radio(it.checked)
			case it.checkable:
				ni.Checked(it.checked)
			}
			if it.shortcut != nil {
				ni.Shortcut(it.shortcut.Mods, it.shortcut.Key)
			}
			if ni.Chosen() && it.action != nil {
				it.action()
			}
		}
	}
}

// ---- attaching a menu --------------------------------------------------------------------------

// menuSpec says how a context menu opens from the keyboard and what assistive
// technology calls it.
type menuSpec struct {
	name string // the menu's accessible name
	// keyAt is where Shift+F10 or the menu key opens it, in window DIPs; nil
	// opens it below the top of the element.
	keyAt func() (x, y float32, ok bool)
	// noKeys leaves Shift+F10 and the menu key to what has the focus inside the
	// element, as a text input with its own editing menu.
	noKeys bool
}

// contextMenu gives el a context menu, which build fills as it opens: where el
// is right-clicked, or, while the focus is in el, for Shift+F10 or the menu
// key.
func (a *app) contextMenu(el ui.Element, spec menuSpec, build func(m *menuBuilder)) {
	if nativeMenus {
		el.ContextMenu(func(nm *ui.Menu) {
			mb := &menuBuilder{}
			build(mb)
			nativeItems(nm, tidyMenu(mb.items))
		})
		return
	}
	// mygo still finds which element a right-click is for, opening on the
	// button's release on Windows and its press elsewhere, and asks for the menu
	// here a frame later. A menu without items shows nothing: the window draws
	// its own at the pointer.
	el.ContextMenu(func(*ui.Menu) {
		px, py, _ := el.PointerPosition()
		b := el.Bounds()
		a.openContextMenu(spec.name, build, b.X+px, b.Y+py, false)
	})
	if spec.noKeys {
		return
	}
	// Taking the keys keeps mygo from asking for a system menu for them.
	if el.Shortcut(ui.Shift, ui.KeyF10) || el.Shortcut(0, ui.KeyContextMenu) {
		b := el.Bounds()
		x, y := b.X, b.Y+min(b.H, 32)
		if spec.keyAt != nil {
			if kx, ky, ok := spec.keyAt(); ok {
				x, y = kx, ky
			}
		}
		a.openContextMenu(spec.name, build, x, y, true)
	}
}

func (a *app) openContextMenu(name string, build func(m *menuBuilder), x, y float32, keyboard bool) {
	mb := &menuBuilder{keyboard: keyboard}
	build(mb)
	if a.openMenu("", name, tidyMenu(mb.items), keyboard) {
		a.popup.point, a.popup.x, a.popup.y = true, x, y
	}
}

// buttonMenu makes el, a button, open a menu below it, which build fills as it
// opens: for a click, Enter or Space, or Down while it has the focus. owner
// tells this button's menu from the others; name is the menu's accessible
// name, the button's label.
func (a *app) buttonMenu(c *ui.Context, el ui.Element, owner, name string, build func(m *menuBuilder)) {
	if nativeMenus {
		el.Menu(func(nm *ui.Menu) {
			mb := &menuBuilder{}
			build(mb)
			nativeItems(nm, tidyMenu(mb.items))
		})
		return
	}
	m := &a.popup
	open := m.open && m.owner == owner
	el.Role(ui.RoleMenuButton).Expanded(open)
	if open {
		// The button stays pressed while its menu shows; the menu is the second cue.
		el.Background(tk(c).SurfacePressed)
		m.anchor, m.anchorSeen = el, true
	}
	clicked := el.Clicked()
	down := el.Shortcut(0, ui.KeyDown)
	if !clicked && !down {
		return
	}
	keyboard := down || el.FocusVisible()
	mb := &menuBuilder{keyboard: keyboard}
	build(mb)
	if a.openMenu(owner, name, tidyMenu(mb.items), keyboard) {
		a.popup.anchor, a.popup.anchorSeen = el, true
	}
}

// ---- the in-window menu ------------------------------------------------------------------------

// menuLevel is the root menu or an open submenu.
type menuLevel struct {
	items  []*menuItem
	cur    int // the highlighted row, -1 for none
	parent int // the row of the level above this submenu opened from
	scroll ui.ScrollState
	reveal bool // scroll the highlighted row into view (the keyboard moved it)
	// ptr is where the pointer last was over the level's list; only a pointer
	// that moves takes the highlight (as in the command palette). ptrSeen is
	// false until the level's first frame.
	ptr     [2]float32
	ptrSeen bool
	rects   []ui.Rect // the rows as last laid out, in window DIPs
	box     ui.Rect   // the panel as last laid out
}

// menuIntent is the row the pointer came to rest on: after submenuDelay its
// submenu opens, or the submenu of another row closes.
type menuIntent struct {
	on         bool
	level, row int
	at         time.Time
}

// menuState is the one in-window menu: opening another replaces it.
type menuState struct {
	open  bool
	owner string // the menu button it belongs to, "" for a context menu
	name  string
	// point is set for a context menu, which opens at (x, y); a button's menu
	// opens below anchor, which the button sets in every frame it is built
	// (anchorSeen): a button that goes closes its menu.
	point      bool
	x, y       float32
	anchor     ui.Element
	anchorSeen bool
	levels     []*menuLevel
	kbd        int        // the level the keyboard moves in
	win        [2]float32 // the window size it opened in: a resize closes it
	intent     menuIntent
	shown      bool // the layer was built: it has the focus from the next frame
}

// openMenu opens items as the in-window menu, replacing any menu open. A menu
// opened from the keyboard highlights its first item. It reports false for a
// menu without items, which does not open.
func (a *app) openMenu(owner, name string, items []*menuItem, keyboard bool) bool {
	if len(items) == 0 {
		return false
	}
	lv := &menuLevel{items: items, cur: -1}
	if keyboard {
		lv.cur = nextMenuItem(items, -1, 1)
	}
	a.popup = menuState{open: true, owner: owner, name: name, levels: []*menuLevel{lv}}
	return true
}

// closeMenu closes the menu chain. The layer is built once more in this frame
// and goes in the next, giving the focus back to what had it.
func (a *app) closeMenu() { a.popup.open = false }

// nextMenuItem is the item after cur in direction d (1 or -1) that can be
// chosen, going round and skipping separators and disabled items; cur -1
// starts before the first (d 1) or after the last (d -1). With none, it
// returns cur.
func nextMenuItem(items []*menuItem, cur, d int) int {
	n := len(items)
	start := cur
	if cur < 0 && d < 0 {
		start = 0
	}
	for i := 1; i <= n; i++ {
		j := ((start+d*i)%n + n) % n
		if it := items[j]; !it.separator && !it.disabled {
			return j
		}
	}
	return cur
}

// menuLetter is the next item after cur, going round, whose label starts with
// r (case-insensitively), or cur.
func menuLetter(items []*menuItem, cur int, r rune) int {
	n := len(items)
	for i := 1; i <= n; i++ {
		j := ((cur+i)%n + n) % n
		it := items[j]
		if it.separator || it.disabled {
			continue
		}
		for _, first := range it.label {
			if unicode.IsLetter(first) || unicode.IsDigit(first) {
				if unicode.ToLower(first) == r {
					return j
				}
				break
			}
		}
	}
	return cur
}

// menuHeight is the height of a level's rows with the panel padding.
func menuHeight(items []*menuItem) float32 {
	h := float32(8)
	for _, it := range items {
		if it.separator {
			h += 9
		} else {
			h += listRowH
		}
	}
	return h
}

// openSubmenu opens the submenu of row i of level li, closing what is open
// below li; from the keyboard, its first item is highlighted and the keyboard
// moves into it. Any other row closes what is open below li.
func (a *app) openSubmenu(li, i int, keyboard bool) {
	m := &a.popup
	it := m.levels[li].items[i]
	if !it.submenu || it.disabled || len(it.sub) == 0 {
		m.levels = m.levels[:li+1]
		m.kbd = min(m.kbd, li)
		return
	}
	if li+1 >= len(m.levels) || m.levels[li+1].parent != i {
		m.levels = append(m.levels[:li+1], &menuLevel{items: it.sub, cur: -1, parent: i})
	}
	if keyboard {
		sub := m.levels[li+1]
		if sub.cur < 0 {
			sub.cur = nextMenuItem(sub.items, -1, 1)
		}
		m.kbd = li + 1
	}
}

// chooseMenuItem is a click or Enter on row i of level li: a submenu opens; an
// item closes the whole chain and runs its action in the next frame, once the
// focus is back where it was, so a dialog the action opens keeps it.
func (a *app) chooseMenuItem(li, i int, keyboard bool) {
	m := &a.popup
	it := m.levels[li].items[i]
	if it.separator || it.disabled {
		return
	}
	m.intent.on = false
	if it.submenu {
		a.openSubmenu(li, i, keyboard)
		return
	}
	a.closeMenu()
	if it.action != nil {
		a.post(it.action)
	}
}

// menuLayer builds the open menu over the window, after the dialogs. A
// transparent layer under it takes the presses outside the menu, which close
// it and reach nothing else, as the system's menus do on Windows; it is modal,
// so the window's shortcuts wait and screen readers see only the menu.
func (a *app) menuLayer(c *ui.Context) {
	m := &a.popup
	a.menuGhostLayer(c)
	if !m.open {
		if m.levels != nil {
			a.menuLeaves(c)
			*m = menuState{}
		}
		return
	}
	w, h := c.Size()
	if m.win == ([2]float32{}) {
		m.win = [2]float32{w, h}
	}
	seen := m.anchorSeen
	m.anchorSeen = false
	if !m.point && !seen || m.win != [2]float32{w, h} {
		// Its button went, or the window was resized.
		*m = menuState{}
		return
	}
	if in := m.intent; in.on {
		if wait := submenuDelay - c.Now().Sub(in.at); wait > 0 {
			c.After(wait)
		} else if in.level < len(m.levels) {
			m.intent.on = false
			a.openSubmenu(in.level, in.row, false)
		}
	}
	ui.Overlay(c, func() {
		layer := ui.Box(c.Key("menu-layer")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Modal().KeepFocus()
		if layer.Pressed() || layer.RightClicked() {
			a.closeMenu()
			c.AnimationFrame()
		}
		layer.Children(func() { a.menuPanels(c, h) })
	})
}

// menuPanels builds the root panel and the open submenus, each beside the row
// it opened from. The root keeps the keyboard focus for all of them.
func (a *app) menuPanels(c *ui.Context, winH float32) {
	m := &a.popup
	target, at, self := m.anchor, ui.AnchorBottomStart, ui.AnchorTopStart
	if m.point {
		// A context menu hangs from the point clicked: mygo flips it to the
		// other side of the point where it would leave the window.
		target = ui.Box(c.Key("menu-point")).Absolute().Left(m.x).Top(m.y)
		at, self = ui.AnchorTopLeft, ui.AnchorTopLeft
	}
	var (
		rows   [][]ui.Element
		root   ui.Element
		active ui.Element
	)
	for li := 0; li < len(m.levels); li++ {
		panel := ui.Column(c.Key("menu-panel:" + strconv.Itoa(li)))
		if li == 0 {
			panel.AttachTo(target, at, self)
			if !m.point {
				panel.Margin(4, 0, 0, 0)
			}
			root = panel
			// The keys first, so that this frame draws where they moved.
			a.menuKeys(c, panel)
		} else {
			// Beside its row, the first rows lined up; on the other side where
			// there is no room.
			panel.AttachTo(rows[li-1][m.levels[li].parent], ui.AnchorTopEnd, ui.AnchorTopStart).Margin(0, 0, 0, 4).Top(-4)
		}
		rows = append(rows, a.menuPanel(c, panel, li, winH))
		if lv := m.levels[li]; li == m.kbd && lv.cur >= 0 && rows[li][lv.cur] != (ui.Element{}) {
			active = rows[li][lv.cur]
		}
	}
	root.Focusable()
	if active != (ui.Element{}) {
		root.ActiveDescendant(active)
	}
	if !m.shown {
		// The first frame leaves the focus where it is: mygo notes it as the
		// layer opens, to give it back as the layer goes, and then moves it into
		// the layer (a modal), onto the root panel.
		m.shown = true
		c.AnimationFrame()
		return
	}
	root.Focus()
	if !root.Focused() {
		// The window lost the keyboard: the menu closes, as the system's do.
		a.closeMenu()
	}
}

// menuKeys moves the keyboard through the menu: Up and Down (going round,
// skipping separators and disabled items), Home and End, Right or Enter into a
// submenu, Left out of it, Enter or Space to choose, Escape to close the
// innermost level, and a letter to the next item starting with it.
func (a *app) menuKeys(c *ui.Context, root ui.Element) {
	m := &a.popup
	var (
		down, up     = root.Shortcut(0, ui.KeyDown), root.Shortcut(0, ui.KeyUp)
		home, end    = root.Shortcut(0, ui.KeyHome), root.Shortcut(0, ui.KeyEnd)
		right, left  = root.Shortcut(0, ui.KeyRight), root.Shortcut(0, ui.KeyLeft)
		enter, space = root.Shortcut(0, ui.KeyEnter), root.Shortcut(0, ui.KeySpace)
		esc          = root.Shortcut(0, ui.KeyEscape)
		letter       rune
	)
	for k := ui.KeyA; k <= ui.KeyZ; k++ {
		if root.Shortcut(0, k) {
			letter = 'a' + rune(k-ui.KeyA)
		}
	}
	for k := ui.Key0; k <= ui.Key9; k++ {
		if root.Shortcut(0, k) {
			letter = '0' + rune(k-ui.Key0)
		}
	}
	if !down && !up && !home && !end && !right && !left && !enter && !space && !esc && letter == 0 {
		return
	}
	c.AnimationFrame()
	m.intent.on = false
	m.kbd = min(m.kbd, len(m.levels)-1)
	lv := m.levels[m.kbd]
	move := func(to int) { lv.cur, lv.reveal = to, true }
	switch {
	case esc:
		if len(m.levels) > 1 {
			m.levels = m.levels[:len(m.levels)-1]
			m.kbd = min(m.kbd, len(m.levels)-1)
		} else {
			a.closeMenu()
		}
	case down:
		move(nextMenuItem(lv.items, lv.cur, 1))
	case up:
		move(nextMenuItem(lv.items, lv.cur, -1))
	case home:
		move(nextMenuItem(lv.items, -1, 1))
	case end:
		move(nextMenuItem(lv.items, -1, -1))
	case left:
		if m.kbd > 0 {
			m.levels = m.levels[:m.kbd]
			m.kbd--
		}
	case right:
		if lv.cur >= 0 && lv.items[lv.cur].submenu {
			a.openSubmenu(m.kbd, lv.cur, true)
		}
	case enter, space:
		if lv.cur >= 0 {
			a.chooseMenuItem(m.kbd, lv.cur, true)
		}
	case letter != 0:
		move(menuLetter(lv.items, lv.cur, letter))
	}
}

// menuPanel builds level li in panel: an overlayPopover 200 to 320 wide, at
// most the window's height, scrolling (with the bar in its gutter) when its
// rows do not fit. It returns the rows, the zero Element for separators.
func (a *app) menuPanel(c *ui.Context, panel ui.Element, li int, winH float32) []ui.Element {
	m := &a.popup
	lv := m.levels[li]
	name := m.name
	if li > 0 {
		name = m.levels[li-1].items[lv.parent].label
	}
	maxH := winH - 2*menuWindowMargin
	panel.Role(ui.RoleMenu).Label(name).MinWidth(menuMinW).MaxWidth(menuMaxW).MaxHeight(maxH)
	overlayPanel(c, panel, overlayPopover)
	a.menuEnter(c, panel, li)
	panel.Clicked() // a press on the panel's padding stays in it
	tall := menuHeight(lv.items) > maxH
	if tall {
		// mygo does not hold an attached element to its MaxHeight: the panel
		// takes the room the window has and its rows scroll.
		panel.Height(maxH)
	}
	lv.box = panel.Bounds()
	slot := false
	for _, it := range lv.items {
		slot = slot || it.icon != ""
	}
	if len(lv.rects) != len(lv.items) {
		lv.rects = make([]ui.Rect, len(lv.items))
	}
	rows := make([]ui.Element, len(lv.items))
	panel.Children(func() {
		list := ui.Scroll(c).TrackScroll(&lv.scroll).Shrink(1).MinHeight(0)
		right := float32(4)
		if tall {
			right = scrollGutter
			list.ScrollbarInsets(0, scrollBarInset, 0, 0)
		}
		// The list does not move, so the pointer's place in it changes only when
		// the pointer moves: that, and not a menu opening under a resting
		// pointer, hands the highlight to the row under it.
		px, py, over := list.PointerPosition()
		moved := over && lv.ptrSeen && (px != lv.ptr[0] || py != lv.ptr[1])
		if over {
			lv.ptr = [2]float32{px, py}
		}
		lv.ptrSeen = true
		list.Children(func() {
			ui.Column(c).Padding(4, right, 4, 4).Children(func() {
				for i, it := range lv.items {
					if it.separator {
						listSeparator(c)
						continue
					}
					rows[i] = a.menuRow(c, li, i, slot, moved)
				}
			})
		})
	})
	return rows
}

// menuRow is row i of level li (see listRow): highlighted (SurfaceHover) where
// the keyboard or the pointer is, checked with an Accent check and weight 600,
// dimmed and skipped when disabled, with a chevron when it opens a submenu.
func (a *app) menuRow(c *ui.Context, li, i int, slot, moved bool) ui.Element {
	m := &a.popup
	lv := m.levels[li]
	it := lv.items[i]
	row := ui.Row(c.Key("menu-item:" + strconv.Itoa(i))).Label(it.label)
	switch {
	case it.radio:
		row.Role(ui.RoleMenuItemRadio).Checked(it.checked)
	case it.checkable:
		row.Role(ui.RoleMenuItemCheckBox).Checked(it.checked)
	default:
		row.Role(ui.RoleMenuItem)
	}
	if it.submenu {
		row.Expanded(li+1 < len(m.levels) && m.levels[li+1].parent == i)
	}
	if moved && !it.disabled && row.Hovered() && (lv.cur != i || m.kbd != li) {
		lv.cur, m.kbd = i, li
		for l := li; l > 0; l-- {
			m.levels[l-1].cur = m.levels[l].parent // the rows leading here stay lit
		}
		m.intent = menuIntent{on: true, level: li, row: i, at: c.Now()}
		c.After(submenuDelay)
		c.AnimationFrame() // the row that had the highlight was drawn already
	}
	var ic *ui.SVG
	if it.icon != "" {
		ic = icons.Get(it.icon)
	}
	shortcut := ""
	if it.shortcut != nil {
		shortcut = it.shortcut.String()
	}
	listRow(c, row, rowSpec{
		Icon: ic, IconSlot: slot, Label: it.label, Shortcut: shortcut,
		Checked: it.checked, Disabled: it.disabled, Highlighted: lv.cur == i && !it.disabled, Submenu: it.submenu,
	})
	if lv.cur == i && lv.reveal {
		row.ScrollIntoView()
		lv.reveal = false
	}
	if row.Clicked() {
		a.chooseMenuItem(li, i, false)
		c.AnimationFrame()
	}
	lv.rects[i] = row.Bounds()
	return row
}
