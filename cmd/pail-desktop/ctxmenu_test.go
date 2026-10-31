package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// The menus are drawn in the window on Windows and Linux and by the system on
// macOS (ctxmenu.go). The suites drive the in-window menus on every platform
// (TestMain turns nativeMenus off), through these helpers, which speak to the
// system menu of mygo's tester instead while nativeMenus is on.

// menu lists the items of the open menu's top level, "-" for separators, or
// nil while none is open.
func (h *harness) menu() []string {
	h.t.Helper()
	if nativeMenus {
		return h.tt.Menu()
	}
	h.tt.Frame()
	h.playOut()
	if !h.a.popup.open {
		return nil
	}
	out := []string{}
	for _, it := range h.a.popup.levels[0].items {
		if it.separator {
			out = append(out, "-")
		} else {
			out = append(out, it.label)
		}
	}
	return out
}

// chooseMenu clicks the item labelled with the last of path in the open menu,
// clicking the submenus the others label on the way, as a user would.
func (h *harness) chooseMenu(path ...string) error {
	h.t.Helper()
	if nativeMenus {
		return h.tt.ChooseMenuItem(path...)
	}
	if len(path) == 0 {
		return fmt.Errorf("no item to choose")
	}
	for depth, label := range path {
		h.tt.Frame()
		h.playOut() // the panel has come in: its rows are where they will stay
		m := &h.a.popup
		if !m.open || depth >= len(m.levels) {
			return fmt.Errorf("no menu open at %q", strings.Join(path[:depth+1], " > "))
		}
		lv := m.levels[depth]
		i := slices.IndexFunc(lv.items, func(it *menuItem) bool { return !it.separator && it.label == label })
		switch {
		case i < 0:
			return fmt.Errorf("the menu has no item %q", strings.Join(path[:depth+1], " > "))
		case lv.items[i].disabled:
			return fmt.Errorf("the item %q is disabled", strings.Join(path[:depth+1], " > "))
		case depth < len(path)-1 && !lv.items[i].submenu:
			return fmt.Errorf("the item %q has no submenu", strings.Join(path[:depth+1], " > "))
		case depth == len(path)-1 && lv.items[i].submenu:
			return fmt.Errorf("%q opens a submenu", strings.Join(path, " > "))
		}
		r := lv.rects[i]
		if r.W <= 0 {
			return fmt.Errorf("the item %q is not laid out", label)
		}
		h.tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}
	// The action runs in the frame after the menu closed.
	h.tt.Frame()
	h.tt.Frame()
	return nil
}

// closeMenu closes the open menu with Escape, a level at a time.
func (h *harness) closeMenu() {
	h.t.Helper()
	if nativeMenus {
		h.tt.CloseMenu()
		return
	}
	for i := 0; i < 4 && h.a.popup.open; i++ {
		h.tt.Key(0, ui.KeyEscape)
		h.tt.Frame()
	}
	if h.a.popup.open {
		h.t.Fatal("Escape did not close the menu")
	}
}

// openTestMenu opens a menu of items at (x, y), as a right-click there would.
func (h *harness) openTestMenu(x, y float32, items func(m *menuBuilder)) {
	h.t.Helper()
	mb := &menuBuilder{}
	items(mb)
	if !h.a.openMenu("", "Test", tidyMenu(mb.items), false) {
		h.t.Fatal("the test menu has no items")
	}
	h.a.popup.point, h.a.popup.x, h.a.popup.y = true, x, y
	h.tt.Frame()
	h.tt.Frame()
	h.playOut()
}

// menuRect is the box of the open menu's panel at level li, as last laid out.
func (h *harness) menuRect(li int) ui.Rect { return h.a.popup.levels[li].box }

func (h *harness) expectMenuInside(w, ht float32) {
	h.t.Helper()
	for li := range h.a.popup.levels {
		r := h.menuRect(li)
		if r.W <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > w || r.Y+r.H > ht {
			h.t.Errorf("menu level %d at %+v leaves the %vx%v window", li, r, w, ht)
		}
		if li == 0 && (r.W < menuMinW-1 || r.W > menuMaxW+1) {
			h.t.Errorf("menu width %v is outside %d..%d", r.W, menuMinW, menuMaxW)
		}
	}
}

// testItems is a menu with a disabled item, separators and a submenu.
func testItems(log *[]string) func(m *menuBuilder) {
	act := func(s string) func() { return func() { *log = append(*log, s) } }
	return func(m *menuBuilder) {
		m.Item("Alpha", act("alpha")).Icon("copy").Shortcut(chord{ui.Cmd, ui.KeyC, "Ctrl+C"})
		m.Item("Bravo", act("bravo")).Disabled(true)
		m.Separator()
		m.Item("Charlie", act("charlie")).Checked(true)
		m.Item("Cobalt", act("cobalt"))
		m.Separator()
		m.Separator() // dropped by tidyMenu
		m.Submenu("Delta", func(sm *menuBuilder) {
			sm.Item("Echo", act("echo"))
			sm.Item("Foxtrot", act("foxtrot")).Checked(false)
		}).Icon("layers")
		m.Item("Golf", act("golf"))
		m.Separator()
	}
}

func TestTidyMenuDropsStraySeparators(t *testing.T) {
	var log []string
	mb := &menuBuilder{}
	mb.Separator()
	testItems(&log)(mb)
	var got []string
	for _, it := range tidyMenu(mb.items) {
		if it.separator {
			got = append(got, "-")
		} else {
			got = append(got, it.label)
		}
	}
	want := []string{"Alpha", "Bravo", "-", "Charlie", "Cobalt", "-", "Delta", "Golf"}
	if !slices.Equal(got, want) {
		t.Fatalf("tidy = %q, want %q", got, want)
	}
}

// The arrows go round, skipping separators and disabled items; Home and End;
// a letter jumps to the next item starting with it.
func TestMenuKeyboardNavigation(t *testing.T) {
	h := newHarness(t, "light")
	var log []string
	h.openTestMenu(400, 200, testItems(&log))
	lv := h.a.popup.levels[0]
	if lv.cur != -1 {
		t.Fatalf("a menu opened by the pointer highlights row %d", lv.cur)
	}
	labelAt := func() string {
		if lv.cur < 0 {
			return ""
		}
		return lv.items[lv.cur].label
	}
	step := func(k ui.Key, want string) {
		t.Helper()
		h.tt.Key(0, k)
		h.tt.Frame()
		if got := labelAt(); got != want {
			t.Errorf("after %v the highlight is on %q, want %q", k, got, want)
		}
	}
	step(ui.KeyDown, "Alpha")
	step(ui.KeyDown, "Charlie") // Bravo is disabled, then a separator
	step(ui.KeyUp, "Alpha")
	step(ui.KeyUp, "Golf") // round to the end
	step(ui.KeyDown, "Alpha")
	step(ui.KeyEnd, "Golf")
	step(ui.KeyHome, "Alpha")
	step(ui.KeyC, "Charlie")
	step(ui.KeyC, "Cobalt")
	step(ui.KeyC, "Charlie") // round
	step(ui.KeyB, "Charlie") // Bravo cannot be chosen
	step(ui.KeyG, "Golf")
	if len(log) != 0 {
		t.Fatalf("moving ran %q", log)
	}
	// Enter chooses: the menu closes and the action runs once, after it.
	h.tt.Key(0, ui.KeyEnter)
	h.tt.Frame()
	h.tt.Frame()
	if h.a.popup.open {
		t.Error("the menu stayed open after Enter")
	}
	if !slices.Equal(log, []string{"golf"}) {
		t.Errorf("Enter ran %q, want golf once", log)
	}
	h.settle()
	if !slices.Equal(log, []string{"golf"}) {
		t.Errorf("the action ran again: %q", log)
	}
}

// Right or Enter opens a submenu with its first item lit, Left and Escape close
// it, a second Escape the menu; the focus goes back where it was.
func TestMenuSubmenuByKeyboardAndEscape(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json") // the table has the focus
	table := locales.ObjectsTable()
	if !h.tt.Focused(table) {
		t.Fatal("the table does not have the focus")
	}
	var log []string
	h.openTestMenu(400, 200, testItems(&log))
	if h.tt.Focused(table) {
		t.Fatal("the menu did not take the focus")
	}
	for _, k := range []ui.Key{ui.KeyEnd, ui.KeyUp} { // to Delta
		h.tt.Key(0, k)
		h.tt.Frame()
	}
	h.tt.Key(0, ui.KeyRight)
	h.tt.Frame()
	m := &h.a.popup
	if len(m.levels) != 2 || m.kbd != 1 || m.levels[1].cur != 0 {
		t.Fatalf("Right: %d levels, keyboard on %d, cur %d", len(m.levels), m.kbd, m.levels[len(m.levels)-1].cur)
	}
	h.tt.Key(0, ui.KeyLeft)
	h.tt.Frame()
	if len(m.levels) != 1 || m.kbd != 0 || m.levels[0].items[m.levels[0].cur].label != "Delta" {
		t.Fatalf("Left did not go back to Delta: %d levels", len(m.levels))
	}
	h.tt.Key(0, ui.KeyEnter)
	h.tt.Frame()
	if len(m.levels) != 2 {
		t.Fatal("Enter on Delta did not open its submenu")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.tt.Frame()
	if !m.open || len(m.levels) != 1 {
		t.Fatalf("the first Escape must close the submenu only: open %v, %d levels", m.open, len(m.levels))
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.popup.open {
		t.Fatal("the second Escape did not close the menu")
	}
	if len(log) != 0 {
		t.Errorf("closing ran %q", log)
	}
	if !h.tt.Focused(table) {
		t.Error("the focus did not go back to the table")
	}
}

// A press outside the menu closes it and reaches nothing else.
func TestMenuClickOutsideClosesWithoutPassingThrough(t *testing.T) {
	h := newHarness(t, "light")
	var log []string
	insp := h.a.inspectorOpen
	r, ok := h.tt.Find(h.a.cmd("inspector").text())
	if !ok {
		t.Fatal("no Inspector toggle")
	}
	h.openTestMenu(400, 200, testItems(&log))
	h.tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	h.settle()
	if h.a.popup.open {
		t.Fatal("a click outside left the menu open")
	}
	if h.a.inspectorOpen != insp {
		t.Error("the click outside the menu went on to the Inspector toggle")
	}
	// A right-click outside closes it too.
	h.openTestMenu(400, 200, testItems(&log))
	h.tt.RightClickAt(900, 700)
	h.settle()
	if h.a.popup.open {
		t.Error("a right-click outside left the menu open")
	}
	if len(log) != 0 {
		t.Errorf("closing ran %q", log)
	}
}

// The pointer lights the row it moves onto; resting on a submenu row opens the
// submenu after the delay; a click on an item runs it once and closes the chain.
func TestMenuPointerHoverOpensSubmenuAndClickRuns(t *testing.T) {
	h := newHarness(t, "light")
	var log []string
	h.openTestMenu(400, 200, testItems(&log))
	lv := h.a.popup.levels[0]
	center := func(li, i int) (float32, float32) {
		r := h.a.popup.levels[li].rects[i]
		return r.X + r.W/2, r.Y + r.H/2
	}
	delta := slices.IndexFunc(lv.items, func(it *menuItem) bool { return it.label == "Delta" })
	h.tt.Move(center(0, delta))
	h.tt.Frame()
	if lv.cur != delta {
		t.Fatalf("the pointer lit row %d, want Delta (%d)", lv.cur, delta)
	}
	h.tt.Advance(submenuDelay / 2)
	h.tt.Frame()
	if len(h.a.popup.levels) != 1 {
		t.Fatal("the submenu opened before the delay")
	}
	h.tt.Advance(submenuDelay)
	h.tt.Frame()
	h.tt.Frame()
	if len(h.a.popup.levels) != 2 {
		t.Fatal("resting on Delta did not open its submenu")
	}
	// Into the submenu: its row lights, Delta stays lit.
	h.tt.Move(center(1, 1))
	h.tt.Frame()
	if sub := h.a.popup.levels[1]; sub.cur != 1 || lv.cur != delta {
		t.Fatalf("submenu cur %d, root cur %d", sub.cur, lv.cur)
	}
	// Disabled rows take no hover.
	h.tt.Move(center(0, 1))
	h.tt.Frame()
	if lv.cur == 1 {
		t.Error("the pointer lit the disabled Bravo")
	}
	h.tt.ClickAt(center(1, 0))
	h.tt.Frame()
	h.tt.Frame()
	if h.a.popup.open {
		t.Error("the chain stayed open after choosing Echo")
	}
	h.settle()
	if !slices.Equal(log, []string{"echo"}) {
		t.Errorf("ran %q, want echo once", log)
	}
	// A click on a disabled item does nothing and keeps the menu.
	h.openTestMenu(400, 200, testItems(&log))
	h.tt.ClickAt(center(0, 1))
	h.settle()
	if !h.a.popup.open || len(log) != 1 {
		t.Errorf("a click on a disabled item: open %v, ran %q", h.a.popup.open, log)
	}
}

// Opening another menu replaces the first; a resize or losing the window's
// keyboard closes it.
func TestMenuClosesOnResizeAndBlur(t *testing.T) {
	h := newHarness(t, "light")
	var log []string
	h.openTestMenu(400, 200, testItems(&log))
	h.resize(1200, 800)
	if h.a.popup.open {
		t.Error("a resize left the menu open")
	}
	h.openTestMenu(400, 200, testItems(&log))
	h.tt.SetFocused(false)
	h.tt.Frame()
	h.tt.SetFocused(true)
	h.settle()
	if h.a.popup.open {
		t.Error("losing the window's focus left the menu open")
	}
	// The menu would cover the row: the right-click goes beside it.
	h.openTestMenu(40, 40, testItems(&log))
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.a.popup.open {
		t.Error("a right-click outside the open menu must close it, not open a second")
	}
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if m := h.menu(); !contains(m, "Download") {
		t.Errorf("the object menu did not open: %q", m)
	}
}

// Checked items show an Accent check, disabled ones are dimmed and never lit.
func TestMenuCheckedAndDisabledCues(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			h := newHarness(t, theme)
			k := tokensFor(theme == "dark")
			var log []string
			h.openTestMenu(400, 200, testItems(&log))
			lv := h.a.popup.levels[0]
			h.tt.Frame()
			img := h.tt.Image()
			hasCheck := func(i int) bool {
				r := lv.rects[i]
				return hasPixel(img, r.X+r.W-30, r.Y+4, r.X+r.W-4, r.Y+r.H-4, k.Accent)
			}
			for i, it := range lv.items {
				if it.separator {
					continue
				}
				if got := hasCheck(i); got != it.checked {
					t.Errorf("%q: Accent check %v, want %v", it.label, got, it.checked)
				}
			}
			// The disabled row's text is fainter than an enabled row's.
			darkest := func(i int) float64 {
				r := lv.rects[i]
				best := 1.0
				for y := int(r.Y + 4); y < int(r.Y+r.H-4); y++ {
					for x := int(r.X + 30); x < int(r.X+90); x++ {
						c := img.RGBAAt(x, y)
						l := (0.3*float64(c.R) + 0.59*float64(c.G) + 0.11*float64(c.B)) / 255
						if theme == "dark" {
							l = 1 - l
						}
						best = min(best, l)
					}
				}
				return best
			}
			if dis, en := darkest(1), darkest(4); dis <= en+0.15 {
				t.Errorf("disabled text contrast %.2f is not dimmer than enabled %.2f", dis, en)
			}
			r := lv.rects[1]
			h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
			h.tt.Frame()
			h.tt.Move(r.X+r.W/2+2, r.Y+r.H/2)
			h.tt.Frame()
			if got := px(h.tt.Image(), r.X+2, r.Y+r.H/2); near(got, k.SurfaceHover, 2) {
				t.Errorf("the disabled row is lit (%v)", got)
			}
		})
	}
}

// At the smallest window, a context menu opened near the bottom right corner
// and the "⋯" menu with its submenu (by the right edge) stay inside.
func TestMenusStayInsideTheSmallestWindow(t *testing.T) {
	const w, ht = 960, 600
	for _, loc := range []string{"en", "zh-Hans", "en-XA"} {
		t.Run(loc, func(t *testing.T) {
			h := newHarness(t, "light")
			h.resize(w, ht)
			h.setLocale(loc)
			tbl, ok := h.tt.Find(locales.ObjectsTable())
			if !ok {
				t.Fatal("no object table")
			}
			h.tt.RightClickAt(tbl.X+tbl.W-6, tbl.Y+tbl.H-6)
			h.tt.Frame()
			h.tt.Frame()
			if !h.a.popup.open {
				t.Fatal("no context menu")
			}
			h.expectMenuInside(w, ht)
			for _, it := range h.a.popup.levels[0].items {
				if it.separator {
					continue
				}
				if r, ok := h.tt.Find(it.label); !ok || r.W <= 0 {
					t.Errorf("%q is not drawn", it.label)
				}
			}
			h.closeMenu()

			// The "⋯" menu and the Theme submenu, opened by the keyboard.
			if err := h.tt.Click(locales.ChromeMore()); err != nil {
				t.Fatal(err)
			}
			h.tt.Frame()
			h.tt.Frame()
			for i := 0; i < 8 && h.a.popup.levels[0].items[max(0, h.a.popup.levels[0].cur)].label != locales.ChromeTheme(); i++ {
				h.tt.Key(0, ui.KeyDown)
				h.tt.Frame()
			}
			h.tt.Key(0, ui.KeyRight)
			h.tt.Frame()
			h.tt.Frame()
			h.playOut() // the submenu has slid out
			if len(h.a.popup.levels) != 2 {
				t.Fatal("the Theme submenu did not open")
			}
			h.expectMenuInside(w, ht)
			if sub, root := h.menuRect(1), h.menuRect(0); sub.X+sub.W > root.X+4 {
				t.Errorf("by the right edge the submenu (%+v) must open on the left of the menu (%+v)", sub, root)
			}
			h.closeMenu()
		})
	}
}

// The Theme and Language submenus of the "⋯" menu choose one of a group:
// their items are radio items, the current one checked, as the native menu
// bar's Language menu has them.
func TestMoreMenuChoicesAreRadioItems(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.tt.Click(locales.ChromeMore()); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	h.tt.Frame()
	if !h.a.popup.open {
		t.Fatal("the \"⋯\" menu did not open")
	}
	for _, name := range []string{locales.ChromeTheme(), locales.ChromeLanguage()} {
		var sub *menuItem
		for _, it := range h.a.popup.levels[0].items {
			if it.label == name {
				sub = it
			}
		}
		if sub == nil || !sub.submenu || len(sub.sub) == 0 {
			t.Fatalf("no %q submenu", name)
		}
		on := 0
		for _, it := range sub.sub {
			if !it.radio {
				t.Errorf("%s: %q is not a radio item", name, it.label)
			}
			if it.checked {
				on++
			}
		}
		if on != 1 {
			t.Errorf("%s: %d items checked, want 1", name, on)
		}
	}
	h.closeMenu()
}

// A menu taller than the window scrolls inside it, with the bar in its gutter.
func TestTallMenuScrollsInsideTheWindow(t *testing.T) {
	h := newHarness(t, "light")
	h.resize(960, 600)
	var log []string
	h.openTestMenu(300, 100, func(m *menuBuilder) {
		for i := range 40 {
			m.Item(fmt.Sprintf("Item %02d", i), func() { log = append(log, fmt.Sprint(i)) })
		}
	})
	h.expectMenuInside(960, 600)
	if r := h.menuRect(0); r.H > 600 {
		t.Errorf("the menu is %v high", r.H)
	}
	// End scrolls the last item into view.
	h.tt.Key(0, ui.KeyEnd)
	h.settle()
	last := h.a.popup.levels[0].rects[39]
	if last.Y < 0 || last.Y+last.H > 600 {
		t.Errorf("End left the last item at %+v", last)
	}
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if !slices.Equal(log, []string{"39"}) {
		t.Errorf("ran %q", log)
	}
}

// Shift+F10 opens the table's menu below the row the keyboard is on, with its
// first item lit, and keeps the selection.
func TestShiftF10OpensTheTableMenu(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	sel := h.a.selectedKeys()
	h.tt.Key(ui.Shift, ui.KeyF10)
	h.tt.Frame()
	h.tt.Frame()
	m := &h.a.popup
	if !m.open || !m.point {
		t.Fatal("Shift+F10 did not open the context menu")
	}
	if lv := m.levels[0]; lv.cur < 0 || lv.items[lv.cur].disabled {
		t.Errorf("the keyboard's menu lights row %d", lv.cur)
	}
	row := h.a.rowRects["manifest.json"]
	if m.y < row.Y || m.y > row.Y+row.H+1 {
		t.Errorf("the menu opens at y %v, the row is at %+v", m.y, row)
	}
	if got := h.a.selectedKeys(); !slices.Equal(got, sel) {
		t.Errorf("the selection changed to %q", got)
	}
	h.closeMenu()
	h.tt.Key(0, ui.KeyContextMenu)
	h.tt.Frame()
	if !h.a.popup.open {
		t.Error("the menu key did not open the context menu")
	}
}

// The system's menus on macOS: the same descriptions fill mygo's menus, and
// the window draws none of its own.
func TestNativeMenusOnMacOS(t *testing.T) {
	was := nativeMenus
	nativeMenus = true
	t.Cleanup(func() { nativeMenus = was })
	h := newHarness(t, "light")
	if err := h.tt.RightClick("manifest.json"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	menu := h.tt.Menu()
	for _, want := range []string{"Download", "Copy key", "Delete", "Select all"} {
		if !contains(menu, want) {
			t.Errorf("native context menu %q lacks %q", menu, want)
		}
	}
	if h.a.popup.open {
		t.Error("an in-window menu opened next to the native one")
	}
	if err := h.tt.ChooseMenuItem("Copy key"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if got := h.tt.Clipboard(); got != "manifest.json" {
		t.Errorf("Copy key copied %q", got)
	}
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Theme", "Dark"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.a.opts.theme != "dark" {
		t.Errorf("theme %q after the native Theme > Dark", h.a.opts.theme)
	}
}

// Snapshots of the menus (PAIL_UI_SNAPSHOT_DIR): the object context menu, the
// "⋯" menu with its Theme submenu, in both themes, in English and Chinese.
func TestMenuSnapshots(t *testing.T) {
	for _, loc := range []string{"en", "zh-Hans"} {
		t.Run(loc, func(t *testing.T) {
			h := newHarness(t, "light")
			h.setLocale(loc)
			if err := h.tt.RightClick("manifest.json"); err != nil {
				t.Fatal(err)
			}
			h.tt.Frame()
			h.tt.Move(1, 1) // away from the menu: no row lit
			h.tt.Frame()
			h.tt.Key(0, ui.KeyDown)
			h.tt.Frame()
			h.snapBoth("menu-context-" + loc)
			h.closeMenu()
			if err := h.tt.Click(locales.ChromeMore()); err != nil {
				t.Fatal(err)
			}
			h.tt.Frame()
			h.snapBoth("menu-more-" + loc)
			lv := h.a.popup.levels[0]
			theme := slices.IndexFunc(lv.items, func(it *menuItem) bool { return it.label == locales.ChromeTheme() })
			r := lv.rects[theme]
			h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
			h.tt.Frame()
			h.tt.Advance(time.Second)
			h.tt.Frame()
			h.tt.Frame()
			if len(h.a.popup.levels) != 2 {
				t.Fatal("the Theme submenu did not open")
			}
			h.snapBoth("menu-submenu-" + loc)
			h.closeMenu()
		})
	}
}
