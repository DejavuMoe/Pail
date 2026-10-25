package main

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The tests of the motion (motion.go): the tester's clock stands still unless
// the test moves it, so each one says exactly how far into an animation it
// looks. The behaviour tests run with the animations off (options.still); these
// run with them on.

// newMotionHarness is newHarness with the animations playing.
func newMotionHarness(t *testing.T, theme string) *harness {
	t.Helper()
	env, err := newDemoEnv(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	h := attachMotion(t, env, theme, false)
	h.a.openBucket("acme-assets")
	h.settle()
	return h
}

// step moves the clock by d, drawing the frames due, and draws once more at the
// new time, as a user's next input would.
func (h *harness) step(d time.Duration) {
	h.t.Helper()
	h.tt.Advance(d)
	h.tt.Frame()
}

// snapMotion writes the tester's last frame as <dir>/<name>.png, when
// PAIL_UI_SNAPSHOT_DIR is set: for looking at the frames of an animation.
func (h *harness) snapMotion(name string) {
	h.t.Helper()
	dir := os.Getenv("PAIL_UI_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	dir = filepath.Join(dir, "motion")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, h.tt.Image()); err != nil {
		h.t.Fatal(err)
	}
}

// frames calls look at each of the offsets (milliseconds from now, increasing),
// moving the clock between them.
func (h *harness) frames(offsets []int, look func(ms int)) {
	h.t.Helper()
	at := 0
	for _, ms := range offsets {
		h.step(time.Duration(ms-at) * time.Millisecond)
		at = ms
		look(ms)
	}
}

func TestMotionSnapshots(t *testing.T) {
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("PAIL_UI_SNAPSHOT_DIR is not set")
	}
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			h := newMotionHarness(t, theme)
			name := func(what string, ms int) string { return theme + "-" + what + "-" + strconv.Itoa(ms) }
			// A dialog coming in and going.
			h.a.openNewFolder()
			h.tt.Frame()
			h.snapMotion(name("dialog-in", 0))
			h.frames([]int{30, 60, 100, 160, 400}, func(ms int) { h.snapMotion(name("dialog-in", ms)) })
			h.a.newFolder = false
			h.tt.Frame()
			h.snapMotion(name("dialog-out", 0))
			h.frames([]int{30, 60, 100, 200}, func(ms int) { h.snapMotion(name("dialog-out", ms)) })

			// The command palette.
			h.tt.Key(ui.Cmd, ui.KeyK)
			h.snapMotion(name("palette-in", 0))
			h.frames([]int{30, 60, 100, 400}, func(ms int) { h.snapMotion(name("palette-in", ms)) })
			h.tt.Key(0, ui.KeyEscape)
			h.frames([]int{30, 60, 200}, func(ms int) { h.snapMotion(name("palette-out", ms)) })

			// A context menu and its submenu.
			var log []string
			h.a.openMenu("", "Test", func() []*menuItem {
				mb := &menuBuilder{}
				testItems(&log)(mb)
				return tidyMenu(mb.items)
			}(), false)
			h.a.popup.point, h.a.popup.x, h.a.popup.y = true, 500, 300
			h.tt.Frame()
			h.snapMotion(name("menu-in", 0))
			h.frames([]int{30, 60, 100, 400}, func(ms int) { h.snapMotion(name("menu-in", ms)) })
			for i, it := range h.a.popup.levels[0].items {
				if it.submenu {
					h.a.openSubmenu(0, i, true)
				}
			}
			if len(h.a.popup.levels) != 2 {
				t.Fatal("no submenu opened")
			}
			h.tt.Frame()
			h.snapMotion(name("submenu-in", 0))
			h.frames([]int{30, 60, 400}, func(ms int) { h.snapMotion(name("submenu-in", ms)) })
			h.a.closeMenu()
			h.tt.Frame()
			h.tt.Frame()
			h.snapMotion(name("menu-out", 0))
			h.frames([]int{30, 60, 100, 200}, func(ms int) { h.snapMotion(name("menu-out", ms)) })

			// The transfers panel closing and opening.
			h.a.transfersOpen = false
			h.tt.Frame()
			h.frames([]int{60, 130, 400}, func(ms int) { h.snapMotion(name("transfers-close", ms)) })
			h.a.transfersOpen = true
			h.tt.Frame()
			h.frames([]int{60, 130, 400}, func(ms int) { h.snapMotion(name("transfers-open", ms)) })

			// A toast.
			h.a.toast("Copied to the clipboard")
			h.tt.Frame()
			h.snapMotion(name("toast-in", 0))
			h.frames([]int{40, 80, 400}, func(ms int) { h.snapMotion(name("toast-in", ms)) })
		})
	}
}

// ---- the tests ---------------------------------------------------------------------------------

func near32(a, b, tol float32) bool { return a-b <= tol && b-a <= tol }

// dist is the largest difference of a channel between got and want.
func dist(got color.RGBA, want ui.Color) int {
	d := 0
	for _, p := range [][2]uint8{{got.R, want.R}, {got.G, want.G}, {got.B, want.B}} {
		d = max(d, max(int(p[0])-int(p[1]), int(p[1])-int(p[0])))
	}
	return d
}

// rectOf is where the element showing text is in the last frame, transforms
// included.
func (h *harness) rectOf(text string) ui.Rect {
	h.t.Helper()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no element shows %q; have %q", text, h.tt.Texts())
	}
	return r
}

// A dialog comes in on a spring: it starts small and transparent, grows to its
// place with a small overshoot at most, and rests exactly where a still frame
// puts it; once it has, the window asks for no more frames.
func TestDialogEntersOnASpringAndComesToRest(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.tt.Frame()
	var ws []float32
	for i := 0; i < 40; i++ {
		ws = append(ws, h.rectOf("Cancel").W)
		h.step(16 * time.Millisecond)
	}
	final := ws[len(ws)-1]
	if ws[0] > final*0.97 || ws[0] < final*0.9 {
		t.Errorf("the dialog starts at %.1f of %.1f: want about 0.94 of its size", ws[0], final)
	}
	peak := float32(0)
	for _, w := range ws {
		peak = max(peak, w)
	}
	if peak > final*1.03 {
		t.Errorf("the spring overshoots to %.1f of %.1f: more than a hair", peak, final)
	}
	if peak < final {
		t.Error("the spring never reached its place")
	}
	still := newHarness(t, "light")
	still.a.openNewFolder()
	still.settle()
	if got, want := h.rectOf("Cancel"), still.rectOf("Cancel"); !near32(got.X, want.X, 0.05) || !near32(got.Y, want.Y, 0.05) || !near32(got.W, want.W, 0.05) {
		t.Errorf("the dialog rests at %+v, a still frame has %+v", got, want)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if h.tt.FramePending() {
		t.Error("the window still asks for frames after the dialog settled")
	}
}

// With the system asking for less motion the card is where it rests from its
// first frame; only its opacity (and the scrim) fade.
func TestReduceMotionJumpsPositionAndKeepsTheFade(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.a.openNewFolder()
	h.tt.Frame()
	first := h.rectOf("Cancel")
	h.step(32 * time.Millisecond)
	if mid := h.rectOf("Cancel"); mid != first {
		t.Errorf("the card moved under reduced motion: %+v then %+v", first, mid)
	}
	// The scrim still fades: a pixel of the window beside the card is between
	// the plain window and the full scrim.
	mid := px(h.tt.Image(), 100, 500)
	h.tt.Advance(time.Second)
	h.tt.Frame()
	end := px(h.tt.Image(), 100, 500)
	if mid == end {
		t.Error("the scrim did not fade under reduced motion")
	}
	if near(end, tokensFor(false).Background, 1) {
		t.Error("no scrim at rest")
	}
}

// A button of a dialog works while the dialog is still coming in.
func TestDialogButtonsWorkMidEntrance(t *testing.T) {
	still := newHarness(t, "light")
	still.a.openNewFolder()
	still.settle()
	rest := still.rectOf("Cancel")
	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.tt.Frame()
	h.step(40 * time.Millisecond)
	if got := h.rectOf("Cancel"); got.W >= rest.W*0.99 {
		t.Fatalf("the dialog has already settled (%+v, at rest %+v): the test must click it moving", got, rest)
	}
	if err := h.tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.newFolder {
		t.Error("Cancel did nothing while the dialog was coming in")
	}
}

// A dialog that closes takes neither the pointer nor the keys while it fades:
// what is under it answers at once.
func TestClosingDialogReleasesTheWindowAtOnce(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.settle()
	card := h.rectOf("Cancel")
	probe := func() color.Color { return px(h.tt.Image(), card.X+card.W/2, card.Y+card.H/2) }
	open := probe()
	h.a.newFolder = false
	h.tt.Frame()
	h.step(16 * time.Millisecond)
	// The copy that leaves is inert: it has no text to find, so the pixels
	// tell whether it still shows.
	leaving := probe()
	if h.tt.HasText("Folder name") {
		t.Error("the leaving dialog still has text to find: it is not inert")
	}
	if h.tt.Focused("Folder name") {
		t.Error("the closing dialog still holds the focus")
	}
	if err := h.tt.Click("acme-backups"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.scope.Bucket != "acme-backups" {
		t.Errorf("a click under the leaving dialog did not reach the sidebar (bucket %q)", h.a.scope.Bucket)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	gone := probe()
	if leaving == open || leaving == gone {
		t.Errorf("16ms after closing the card shows %v: not between open %v and gone %v, so nothing exits", leaving, open, gone)
	}
	if h.tt.FramePending() {
		t.Error("the window still asks for frames after the dialog went")
	}
}

// A menu answers the keyboard and the pointer while it comes in, and one that
// closes fades without holding the layer.
func TestMenuWorksMidEntranceAndLeavesWithoutHolding(t *testing.T) {
	h := newMotionHarness(t, "light")
	var log []string
	h.openTestMenuMotion(500, 300, testItems(&log))
	h.step(30 * time.Millisecond)
	m := &h.a.popup
	if !m.open {
		t.Fatal("the menu is not open")
	}
	h.tt.Key(0, ui.KeyDown)
	h.tt.Frame()
	if m.levels[0].cur != 0 {
		t.Errorf("Down mid-entrance put the cursor on %d, want the first item", m.levels[0].cur)
	}
	h.tt.Key(0, ui.KeyDown)
	h.tt.Key(0, ui.KeyEnter)
	h.tt.Frame()
	h.tt.Frame()
	if want := []string{"charlie"}; len(log) != 1 || log[0] != want[0] {
		t.Errorf("Enter ran %q, want %q", log, want)
	}
	if m.open {
		t.Error("the menu stays open after a choice")
	}
	// While its copy fades out the window answers.
	if h.a.popGhost == nil {
		t.Error("no menu fades out")
	}
	if err := h.tt.Click("acme-backups"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.scope.Bucket != "acme-backups" {
		t.Errorf("the fading menu took the click: bucket %q", h.a.scope.Bucket)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if h.a.popGhost != nil || h.tt.FramePending() {
		t.Error("the menu's ghost stays, or the window keeps asking for frames")
	}
}

// openTestMenuMotion is openTestMenu without waiting for the entrance.
func (h *harness) openTestMenuMotion(x, y float32, items func(m *menuBuilder)) {
	h.t.Helper()
	mb := &menuBuilder{}
	items(mb)
	if !h.a.openMenu("", "Test", tidyMenu(mb.items), false) {
		h.t.Fatal("the test menu has no items")
	}
	h.a.popup.point, h.a.popup.x, h.a.popup.y = true, x, y
	h.tt.Frame()
	h.tt.Frame()
}

// A menu flipped above its click comes up from below, and one below goes down
// from above: the entrance is toward the anchor.
func TestMenuEntranceLeansTowardItsAnchor(t *testing.T) {
	for _, tc := range []struct {
		name string
		y    float32
		down bool // the menu hangs below the click
	}{{"below", 200, true}, {"above", 880, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMotionHarness(t, "light")
			var log []string
			h.openTestMenuMotion(500, tc.y, testItems(&log))
			start := h.rectOf("Alpha").Y
			h.tt.Advance(time.Second)
			h.tt.Frame()
			rest := h.rectOf("Alpha").Y
			if tc.down && start >= rest || !tc.down && start <= rest {
				t.Errorf("the menu %s the click starts at y=%.1f and rests at %.1f", tc.name, start, rest)
			}
		})
	}
}

// The underline of the inspector tabs slides from one tab to the next instead
// of jumping, and arrives exactly under the new one.
func TestInspectorUnderlineSlides(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		h := newMotionHarness(t, "light")
		h.mustClick("manifest.json")
		if reduce {
			h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		}
		k := tokensFor(false)
		underline := func() (x0, x1 float32, ok bool) {
			img := h.tt.Image()
			y := int(h.rectOf("Overview").Y + h.rectOf("Overview").H - 1)
			for x := 1120; x < 1440; x++ {
				if near(img.RGBAAt(x, y), k.Accent, 6) {
					if !ok {
						x0, ok = float32(x), true
					}
					x1 = float32(x)
				}
			}
			return
		}
		from, _, ok := underline()
		if !ok {
			t.Fatal("no underline under the Overview tab")
		}
		if err := h.tt.Click("Headers"); err != nil {
			t.Fatal(err)
		}
		h.step(40 * time.Millisecond)
		mid, _, _ := underline()
		h.tt.Advance(time.Second)
		h.tt.Frame()
		to, _, _ := underline()
		want := h.rectOf("Headers")
		if to < want.X-4 || to > want.X+4 || from == to {
			t.Fatalf("reduce=%v: the underline went %.0f to %.0f, the tab is at %.0f", reduce, from, to, want.X)
		}
		if reduce {
			if mid != to {
				t.Errorf("under reduced motion the underline is at %.0f, not at %.0f at once", mid, to)
			}
			continue
		}
		if !(mid > from+1 && mid < to-1) && !(mid > to+1) { // between, or a spring's overshoot
			t.Errorf("40ms into the slide the underline is at %.0f: not between %.0f and %.0f", mid, from, to)
		}
	}
}

// The transfers panel eases between its bar and its height, and a drag of its
// divider moves it with the pointer and no delay.
func TestTransfersPanelEasesOnToggleButNotOnDrag(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.tt.Advance(time.Second)
	h.tt.Frame()
	open := h.rectOf("Transfers").Y
	h.a.transfersOpen = false
	h.tt.Frame()
	h.step(60 * time.Millisecond)
	mid := h.rectOf("Transfers").Y
	h.tt.Advance(time.Second)
	h.tt.Frame()
	shut := h.rectOf("Transfers").Y
	if !(open < mid && mid < shut) {
		t.Errorf("the panel header goes %.0f, %.0f, %.0f: not eased", open, mid, shut)
	}
	h.a.transfersOpen = true
	h.tt.Frame()
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if y := h.rectOf("Transfers").Y; !near32(y, open, 0.5) {
		t.Fatalf("the panel reopened with its header at %.1f, was %.1f", y, open)
	}
	// Drag the divider up by 60: the header follows at once. The divider is
	// the line above the panel, a little above the header's title.
	divY := float32(testH-statusBarH) - h.a.transH - dividerW/2
	h.tt.Move(700, divY)
	h.tt.Press(700, divY)
	h.tt.Move(700, divY-60)
	h.tt.Frame()
	if y := h.rectOf("Transfers").Y; !near32(y, open-60, 1.5) {
		t.Errorf("a dragged divider left the header at %.1f, want %.1f: the drag must not be eased", y, open-60)
	}
	h.tt.Release(700, divY-60)
	h.tt.Frame()
	h.step(16 * time.Millisecond)
	if y := h.rectOf("Transfers").Y; !near32(y, open-60, 1.5) {
		t.Errorf("after the drop the header is at %.1f, want %.1f", y, open-60)
	}
}

// Resizing the window animates nothing: the layout is where a settled frame
// puts it in the very frame of the new size.
func TestResizeStartsNoTransitions(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.mustClick("manifest.json")
	h.tt.Advance(time.Second)
	h.tt.Frame()
	for _, text := range []string{"acme-backups", "Transfers", "Overview", "manifest.json", "Settings"} {
		h.rectOf(text)
	}
	h.tt.SetSize(1100, 700)
	got := map[string]ui.Rect{}
	for _, text := range []string{"acme-backups", "Transfers", "Overview", "manifest.json", "Settings"} {
		got[text] = h.rectOf(text)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	for text, r := range got {
		if end := h.rectOf(text); end != r {
			t.Errorf("%q was at %+v in the frame of the resize and %+v a second later: the resize animated", text, r, end)
		}
	}
	if h.tt.FramePending() {
		t.Error("the window still asks for frames after the resize settled")
	}
}

// With the system asking for less motion nothing moves: the transfers panel,
// a toast and a menu are where they rest in the frame that shows them, and
// the window asks for no frames once the fades are done.
func TestReduceMotionMovesNothing(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.tt.Advance(time.Second)
	h.tt.Frame()
	rested := func(what string, at func() float32) {
		t.Helper()
		first := at()
		h.step(40 * time.Millisecond)
		mid := at()
		h.tt.Advance(time.Second)
		h.tt.Frame()
		if end := at(); first != end || mid != end {
			t.Errorf("%s moved under reduced motion: %.1f, %.1f, then %.1f", what, first, mid, end)
		}
	}
	open := h.rectOf("Transfers").Y
	h.a.transfersOpen = false
	h.tt.Frame()
	rested("the transfers panel", func() float32 { return h.rectOf("Transfers").Y })
	if h.rectOf("Transfers").Y == open {
		t.Error("the transfers panel did not close")
	}
	h.a.toast("Hello motion")
	h.tt.Frame()
	rested("a toast", func() float32 { return h.rectOf("Hello motion").Y })
	var log []string
	h.openTestMenuMotion(500, 300, testItems(&log))
	rested("a menu", func() float32 { return h.rectOf("Alpha").Y })
	if h.tt.FramePending() {
		t.Error("the window still asks for frames under reduced motion")
	}
}

// The inspector's content fades in as the user opens it; the panel's place
// and width do not move.
func TestInspectorContentFadesInWhenOpened(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.mustClick("manifest.json")
	tab := h.rectOf("Overview")
	probe := func() color.Color { return px(h.tt.Image(), tab.X+tab.W/2, tab.Y+tab.H-1) } // the underline
	shown := probe()
	h.a.inspectorOpen = false
	h.tt.Frame()
	h.tt.Advance(time.Second)
	h.tt.Frame()
	hidden := probe()
	h.a.inspectorOpen = true
	h.tt.Frame()
	h.step(40 * time.Millisecond)
	if got := h.rectOf("Overview"); got != tab {
		t.Errorf("the inspector moved as it opened: %+v, at rest %+v", got, tab)
	}
	mid := probe()
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if end := probe(); end != shown {
		t.Fatalf("the inspector rests at %v, was %v", end, shown)
	}
	if mid == shown || mid == hidden {
		t.Errorf("40ms after opening the inspector shows %v: not between %v and %v", mid, hidden, shown)
	}
	if h.tt.FramePending() {
		t.Error("the window still asks for frames after the inspector came in")
	}
}

// Every motion comes to rest: once a palette, a toast, the transfers panel
// and a select's popup have come and gone, the window asks for no frames.
func TestMotionComesToRest(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.tt.Key(ui.Cmd, ui.KeyK)
	h.step(50 * time.Millisecond)
	if !h.a.pal.open {
		t.Fatal("the palette did not open")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.step(50 * time.Millisecond)
	h.a.toast("Hello motion")
	h.a.transfersOpen = !h.a.transfersOpen
	h.tt.Frame()
	h.step(100 * time.Millisecond)
	h.a.transfersOpen = !h.a.transfersOpen // turned around mid-way
	h.tt.Frame()
	if !h.tt.FramePending() {
		t.Error("nothing moves mid-way: the test looks at no motion")
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if h.tt.FramePending() {
		t.Error("the window still asks for frames once everything came to rest")
	}
}

// A toast comes in from a little below its place, and fades.
func TestToastRisesIntoPlace(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.toast("Hello motion")
	h.tt.Frame()
	start := h.rectOf("Hello motion").Y
	h.tt.Advance(600 * time.Millisecond)
	h.tt.Frame()
	end := h.rectOf("Hello motion").Y
	if start < end+3 || start > end+12 {
		t.Errorf("the toast starts %.1f below its place, want about %d", start-end, toastRise)
	}
}

// The progress bar's fill eases to a new fraction rather than jumping, then
// rests exactly there.
func TestProgressBarEasesToANewFraction(t *testing.T) {
	frac := 0.2
	view := func(c *ui.Context) {
		k := applyTheme(c, "light")
		ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { progressBar(c, frac, false) })
	}
	tt := ui.NewTester(view, 300, 100)
	tt.Frame()
	// The pixels nearer the fill's colour than the track's: the rounded ends
	// are anti-aliased, so an exact match would miss them.
	k := tokensFor(false)
	fill := func() int {
		img := tt.Image()
		n := 0
		for x := 20; x < 20+colProgress; x++ {
			if dist(img.RGBAAt(x, 22), k.Accent) < dist(img.RGBAAt(x, 22), k.Border) {
				n++
			}
		}
		return n
	}
	if got := fill(); got < 31 || got > 33 {
		t.Fatalf("the fill is %d wide at 0.2, want about 32", got)
	}
	frac = 0.8
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	tt.Frame()
	mid := fill()
	tt.Advance(time.Second)
	tt.Frame()
	end := fill()
	if !(mid > 33 && mid < end) || end < 127 || end > 129 {
		t.Errorf("the fill goes 32, %d, %d: want it between at 50ms and 128 at rest", mid, end)
	}
	if tt.FramePending() {
		t.Error("the progress bar keeps asking for frames at rest")
	}
}

// The knob of a switch slides to its new side on a spring, and with reduced
// motion jumps there (the track's colour still fades).
func TestSwitchKnobSlides(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		on := false
		view := func(c *ui.Context) {
			k := applyTheme(c, "light")
			ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { switchControl(c, &on, "Test switch") })
		}
		tt := ui.NewTester(view, 200, 80)
		tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
		tt.Frame()
		k := tokensFor(false)
		// The knob's centre: the mean x of the knob's pixels on the middle row.
		knob := func() float32 {
			var sum, n float32
			for x := 20; x < 20+switchW; x++ {
				if near(tt.Image().RGBAAt(x, 20+switchH/2), k.Knob, 3) {
					sum, n = sum+float32(x), n+1
				}
			}
			if n == 0 {
				t.Fatal("no knob drawn")
			}
			return sum / n
		}
		from := knob()
		on = true
		tt.Frame()
		tt.Advance(40 * time.Millisecond)
		tt.Frame()
		mid := knob()
		tt.Advance(time.Second)
		tt.Frame()
		to := knob()
		if to-from < switchW-switchH-2 {
			t.Fatalf("reduce=%v: the knob went from %.1f to %.1f", reduce, from, to)
		}
		if reduce && mid != to {
			t.Errorf("under reduced motion the knob is at %.1f 40ms in, not at %.1f", mid, to)
		}
		if !reduce && !(mid > from+1 && mid < to-1) {
			t.Errorf("40ms into the slide the knob is at %.1f: not between %.1f and %.1f", mid, from, to)
		}
		if tt.FramePending() {
			t.Errorf("reduce=%v: the switch keeps asking for frames at rest", reduce)
		}
	}
}

// The "Saved" mark fades in and fades out instead of popping.
func TestSavedMarkFades(t *testing.T) {
	a := &app{}
	a.sf.saved = map[string]time.Time{}
	view := func(c *ui.Context) {
		k := applyTheme(c, "light")
		ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { a.savedMark(c, "x") })
	}
	tt := ui.NewTester(view, 300, 100)
	tt.Frame()
	a.sf.saved["x"] = time.Now()
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	mid := px(tt.Image(), 24, 28)
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Saved") {
		t.Fatalf("no Saved mark; have %q", tt.Texts())
	}
	full := px(tt.Image(), 24, 28)
	if mid == full {
		t.Error("the mark popped in at full strength")
	}
	delete(a.sf.saved, "x")
	tt.Frame()
	tt.Advance(30 * time.Millisecond)
	tt.Frame()
	// The leaving copy is inert, with no text to find: the pixels tell.
	leaving := px(tt.Image(), 24, 28)
	if tt.HasText("Saved") {
		t.Error("the mark is still built after its time")
	}
	tt.Advance(time.Second)
	tt.Frame()
	gone := px(tt.Image(), 24, 28)
	if leaving == full || leaving == gone {
		t.Errorf("30ms after its time the mark shows %v: not between %v and %v, it vanished at once", leaving, full, gone)
	}
	if tt.FramePending() {
		t.Error("the window still asks for frames after the mark went")
	}
}

// What the pointer is over fades its fill in instead of switching it: a ghost
// button 30ms after the pointer came is between plain and hovered, and at rest
// it is the hover colour exactly.
func TestHoverFadesTheFill(t *testing.T) {
	h := newMotionHarness(t, "light")
	k := tokensFor(false)
	r := h.rectOf("Refresh") // the icon-only button: a 28px square, its label the text
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.step(30 * time.Millisecond)
	probe := func() color.Color { return px(h.tt.Image(), r.X+r.W/2, r.Y+3) }
	mid := probe()
	h.tt.Advance(time.Second)
	h.tt.Frame()
	end := probe()
	if !near(end, k.SurfaceHover, 2) {
		t.Fatalf("a hovered button rests at %v, want SurfaceHover %v", end, k.SurfaceHover)
	}
	if near(mid, k.SurfaceHover, 1) || near(mid, k.Background, 1) {
		t.Errorf("30ms in the fill is %v: not between %v and %v", mid, k.Background, k.SurfaceHover)
	}
}

// A notch of the mouse wheel glides the object table (mygo eases it over a
// moment, as browsers do) and rests exactly a notch further; with reduced
// motion the table is there at once.
func TestWheelGlidesTheObjectTable(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		t.Run("reduce="+strconv.FormatBool(reduce), func(t *testing.T) {
			h := newMotionHarness(t, "light")
			h.tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
			h.a.openPrefix("logs/access/")
			h.settle()
			const name, notch = "access-0010.log.gz", 90
			r := h.rectOf(name)
			h.tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, notch)
			h.step(30 * time.Millisecond)
			mid := r.Y - h.rectOf(name).Y
			h.step(time.Second)
			end := r.Y - h.rectOf(name).Y
			if math.Abs(float64(end-notch)) > 0.5 {
				t.Fatalf("the table rests %v up after a %v notch", end, notch)
			}
			if reduce {
				if mid != end {
					t.Errorf("with reduced motion the table was %v up 30ms in, %v at rest", mid, end)
				}
			} else if mid <= 0 || mid >= end {
				t.Errorf("30ms in the table was %v up: not between 0 and %v", mid, end)
			}
		})
	}
}

// The selection bar grows to its height over durStandard as a second row is
// chosen and folds away as the choice is cleared; the rows above stay put.
func TestSelectionBarEasesInAndOut(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.tt.Advance(time.Second)
	h.tt.Frame()
	row := h.rectOf("README.txt").Y
	h.a.sel.Add("README.txt")
	h.a.sel.Add("manifest.json")
	h.tt.Frame()
	h.step(100 * time.Millisecond)
	mid, ok := h.tt.Find("Selection")
	if !ok || !(mid.H > 1 && mid.H < selBarH-1) {
		t.Fatalf("100ms in, the bar is %+v (shown %v): not eased", mid, ok)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if r, _ := h.tt.Find("Selection"); !near32(r.H, selBarH, 0.5) {
		t.Fatalf("the bar rests at height %.1f, want %d", r.H, selBarH)
	}
	if y := h.rectOf("README.txt").Y; !near32(y, row, 0.5) {
		t.Errorf("the bar moved the rows: README.txt at %.1f, was %.1f", y, row)
	}
	h.a.clearSelection()
	h.tt.Frame()
	h.step(100 * time.Millisecond)
	if r, ok := h.tt.Find("Selection"); !ok || !(r.H > 1 && r.H < selBarH-1) {
		t.Fatalf("100ms into folding, the bar is %+v (shown %v)", r, ok)
	}
	h.tt.Advance(time.Second)
	h.tt.Frame()
	if _, ok := h.tt.Find("Selection"); ok {
		t.Fatal("the bar is still there once folded")
	}
}
