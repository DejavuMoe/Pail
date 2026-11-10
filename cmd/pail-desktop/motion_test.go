package main

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The tests of the motion (motion.go): the tester's clock stands still unless
// the test moves it, so each one says exactly how far into an animation it
// looks. The behaviour tests run with the animations off (options.still); these
// run with them on. The rule they hold: overlays fade in where they rest and
// go at once, a progress bar's fill eases, and every other change of state is
// in place in the frame it happens.

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

// rest moves the clock well past every animation and draws.
func (h *harness) rest() {
	h.t.Helper()
	h.tt.Advance(time.Second)
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

func TestMotionSnapshots(t *testing.T) {
	if os.Getenv("PAIL_UI_SNAPSHOT_DIR") == "" {
		t.Skip("PAIL_UI_SNAPSHOT_DIR is not set")
	}
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			h := newMotionHarness(t, theme)
			name := func(what string, ms int) string { return theme + "-" + what + "-" + strconv.Itoa(ms) }
			shots := func(what string, offsets ...int) {
				at := 0
				for _, ms := range offsets {
					h.step(time.Duration(ms-at) * time.Millisecond)
					at = ms
					h.snapMotion(name(what, ms))
				}
			}
			h.a.openNewFolder()
			h.tt.Frame()
			h.snapMotion(name("dialog-in", 0))
			shots("dialog-in", 60, 125, 400)
			h.a.newFolder = false
			h.tt.Frame()
			h.snapMotion(name("dialog-out", 0))

			var log []string
			h.openTestMenuMotion(500, 300, testItems(&log))
			h.snapMotion(name("menu-in", 0))
			shots("menu-in", 60, 125, 400)

			h.a.closeMenu()
			h.a.toast("Copied to the clipboard")
			h.tt.Frame()
			h.snapMotion(name("toast-in", 0))
			shots("toast-in", 60, 125, 400)
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

// A dialog fades in where it rests: it neither grows nor moves, its pixels are
// on their way 60ms in, and once it has arrived the window asks for no more
// frames.
func TestDialogFadesInWhereItRests(t *testing.T) {
	still := newHarness(t, "light")
	still.a.openNewFolder()
	still.settle()
	want := still.rectOf("Cancel")

	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.tt.Frame()
	if got := h.rectOf("Cancel"); got != want {
		t.Errorf("the dialog's first frame puts Cancel at %+v, at rest it is %+v: it moved or scaled", got, want)
	}
	card := func() color.Color { return px(h.tt.Image(), want.X+want.W/2, want.Y-40) }
	h.step(60 * time.Millisecond)
	mid := card()
	if got := h.rectOf("Cancel"); got != want {
		t.Errorf("60ms in Cancel is at %+v, at rest %+v", got, want)
	}
	h.rest()
	if end := card(); mid == end {
		t.Error("the card is at full strength 60ms in: it did not fade")
	}
	if h.tt.FramePending() {
		t.Error("the window still asks for frames after the dialog came in")
	}
}

// With the system asking for less motion the dialog is still where it rests in
// its first frame, and it still fades (mygo keeps opacity).
func TestReduceMotionKeepsTheFade(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.a.openNewFolder()
	h.tt.Frame()
	first := h.rectOf("Cancel")
	h.step(60 * time.Millisecond)
	if mid := h.rectOf("Cancel"); mid != first {
		t.Errorf("the card moved under reduced motion: %+v then %+v", first, mid)
	}
	mid := px(h.tt.Image(), 100, 500)
	h.rest()
	end := px(h.tt.Image(), 100, 500)
	if mid == end {
		t.Error("the scrim did not fade under reduced motion")
	}
	if near(end, tokensFor(false).Background, 1) {
		t.Error("no scrim at rest")
	}
}

// A button of a dialog works while the dialog is still fading in.
func TestDialogButtonsWorkMidFade(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.tt.Frame()
	h.step(40 * time.Millisecond)
	if !h.tt.FramePending() {
		t.Fatal("the dialog has already come in: the test must click it fading")
	}
	if err := h.tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.newFolder {
		t.Error("Cancel did nothing while the dialog was coming in")
	}
}

// A dialog that closes is gone in that frame: nothing of it is drawn, it holds
// neither the focus nor the pointer, and the window asks for no frames.
func TestClosingDialogIsGoneAtOnce(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.openNewFolder()
	h.settle()
	card := h.rectOf("Cancel")
	probe := func() color.Color { return px(h.tt.Image(), card.X+card.W/2, card.Y-40) }
	h.a.newFolder = false
	h.tt.Frame()
	gone := probe()
	if h.tt.HasText("Folder name") || h.tt.Focused("Folder name") {
		t.Error("the closed dialog is still there or holds the focus")
	}
	if h.tt.FramePending() {
		t.Error("the window asks for frames after the dialog closed: something fades out")
	}
	h.rest()
	if end := probe(); end != gone {
		t.Errorf("the place of the card shows %v in the frame it closed and %v later", gone, end)
	}
	if err := h.tt.Click("acme-backups"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.scope.Bucket != "acme-backups" {
		t.Errorf("a click where the dialog was did not reach the sidebar (bucket %q)", h.a.scope.Bucket)
	}
}

// openTestMenuMotion is openTestMenu without waiting for the fade.
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

// A menu answers the keyboard while it fades in, shows where it rests from its
// first frame, and is gone in the frame it closes.
func TestMenuWorksMidFadeAndGoesAtOnce(t *testing.T) {
	h := newMotionHarness(t, "light")
	var log []string
	h.openTestMenuMotion(500, 300, testItems(&log))
	start := h.rectOf("Alpha")
	h.step(30 * time.Millisecond)
	m := &h.a.popup
	if !m.open {
		t.Fatal("the menu is not open")
	}
	if got := h.rectOf("Alpha"); got != start {
		t.Errorf("the menu moved as it came in: %+v then %+v", start, got)
	}
	h.tt.Key(0, ui.KeyDown)
	h.tt.Frame()
	if m.levels[0].cur != 0 {
		t.Errorf("Down mid-fade put the cursor on %d, want the first item", m.levels[0].cur)
	}
	h.tt.Key(0, ui.KeyDown)
	h.tt.Key(0, ui.KeyEnter)
	h.tt.Frame()
	h.tt.Frame()
	if want := []string{"charlie"}; len(log) != 1 || log[0] != want[0] {
		t.Errorf("Enter ran %q, want %q", log, want)
	}
	if m.open || h.tt.HasText("Alpha") {
		t.Error("the menu is still there after a choice")
	}
	if err := h.tt.Click("acme-backups"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.scope.Bucket != "acme-backups" {
		t.Errorf("the closed menu took the click: bucket %q", h.a.scope.Bucket)
	}
	if h.tt.FramePending() {
		t.Error("the window keeps asking for frames after the menu went")
	}
}

// What the user changes shows in the frame it changes, with no animation left
// running: the inspector's tab underline, the transfers panel, the selection
// bar, a switch's knob and the "Saved" mark.
func TestStateChangesShowAtOnce(t *testing.T) {
	t.Run("tab underline", func(t *testing.T) {
		h := newMotionHarness(t, "light")
		h.mustClick("manifest.json")
		k := tokensFor(false)
		underline := func() (x0 float32, ok bool) {
			img := h.tt.Image()
			r := h.rectOf("Overview")
			y := int(r.Y + r.H - 1)
			for x := 1120; x < 1440; x++ {
				if near(img.RGBAAt(x, y), k.Accent, 6) {
					return float32(x), true
				}
			}
			return 0, false
		}
		from, ok := underline()
		if !ok {
			t.Fatal("no underline under the Overview tab")
		}
		if err := h.tt.Click("Headers"); err != nil {
			t.Fatal(err)
		}
		h.tt.Frame()
		to, _ := underline()
		want := h.rectOf("Headers")
		if to == from || to < want.X-4 || to > want.X+4 {
			t.Errorf("in the frame after the click the underline is at %.0f, the Headers tab at %.0f", to, want.X)
		}
	})
	t.Run("transfers panel", func(t *testing.T) {
		h := newMotionHarness(t, "light")
		seedTasks(h)
		h.rest()
		open := h.rectOf("Transfers").Y
		h.a.transfersOpen = false
		h.tt.Frame()
		shut := h.rectOf("Transfers").Y
		h.rest()
		if y := h.rectOf("Transfers").Y; y != shut || shut <= open {
			t.Errorf("the panel header went %.0f, then %.0f, at rest %.0f: not at once", open, shut, y)
		}
		// A drag of the divider moves the panel with the pointer.
		h.a.transfersOpen = true
		h.tt.Frame()
		divY := float32(testH-statusBarH) - h.a.transH - dividerW/2
		h.tt.Move(700, divY)
		h.tt.Press(700, divY)
		h.tt.Move(700, divY-60)
		h.tt.Frame()
		if y := h.rectOf("Transfers").Y; !near32(y, open-60, 1.5) {
			t.Errorf("a dragged divider left the header at %.1f, want %.1f", y, open-60)
		}
		h.tt.Release(700, divY-60)
		h.tt.Frame()
	})
	t.Run("selection bar", func(t *testing.T) {
		h := newMotionHarness(t, "light")
		h.rest()
		row := h.rectOf("README.txt").Y
		h.a.sel.Add("README.txt")
		h.a.sel.Add("manifest.json")
		h.tt.Frame()
		if r, ok := h.tt.Find("Selection"); !ok || !near32(r.H, selBarH, 0.5) {
			t.Fatalf("the bar is %+v (shown %v) in the frame of the choice, want %d high", r, ok, selBarH)
		}
		if y := h.rectOf("README.txt").Y; !near32(y, row, 0.5) {
			t.Errorf("the bar moved the rows: README.txt at %.1f, was %.1f", y, row)
		}
		h.a.clearSelection()
		h.tt.Frame()
		if _, ok := h.tt.Find("Selection"); ok {
			t.Error("the bar is still there in the frame the choice was cleared")
		}
	})
	t.Run("switch", func(t *testing.T) {
		on := false
		view := func(c *ui.Context) {
			k := applyTheme(c, "light")
			ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { switchControl(c, &on, "Test switch") })
		}
		tt := ui.NewTester(view, 200, 80)
		tt.Frame()
		k := tokensFor(false)
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
		if to := knob(); to-from < switchW-switchH-2 {
			t.Errorf("in the frame it was turned on the knob went from %.1f to %.1f", from, to)
		}
		if tt.FramePending() {
			t.Error("the switch asks for frames: it animates")
		}
	})
	t.Run("saved mark", func(t *testing.T) {
		a := &app{}
		a.sf.saved = map[string]time.Time{}
		view := func(c *ui.Context) {
			k := applyTheme(c, "light")
			ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { a.savedMark(c, "x") })
		}
		tt := ui.NewTester(view, 300, 100)
		tt.Frame()
		before := px(tt.Image(), 24, 28)
		a.sf.saved["x"] = time.Now()
		tt.Frame()
		if !tt.HasText("Saved") || px(tt.Image(), 24, 28) == before {
			t.Fatal("the Saved mark is not drawn in the frame it is due")
		}
		delete(a.sf.saved, "x")
		tt.Frame()
		if tt.HasText("Saved") || px(tt.Image(), 24, 28) != before {
			t.Error("the Saved mark is still drawn after its time")
		}
	})
}

// What the pointer is over takes its fill in the next frame, not over time.
func TestHoverFillIsImmediate(t *testing.T) {
	h := newMotionHarness(t, "light")
	k := tokensFor(false)
	r := h.rectOf("Refresh") // the icon-only button: a 28px square, its label the text
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Frame()
	if got := px(h.tt.Image(), r.X+r.W/2, r.Y+3); !near(got, k.SurfaceHover, 2) {
		t.Errorf("a hovered button shows %v, want SurfaceHover %v at once", got, k.SurfaceHover)
	}
}

// Resizing the window animates nothing: the layout is where a settled frame
// puts it in the very frame of the new size.
func TestResizeStartsNoTransitions(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.mustClick("manifest.json")
	h.rest()
	texts := []string{"acme-backups", "Transfers", "Overview", "manifest.json", "Settings"}
	for _, text := range texts {
		h.rectOf(text)
	}
	h.tt.SetSize(1100, 700)
	got := map[string]ui.Rect{}
	for _, text := range texts {
		got[text] = h.rectOf(text)
	}
	h.rest()
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
	h.rest()
	rested := func(what string, at func() float32) {
		t.Helper()
		first := at()
		h.step(40 * time.Millisecond)
		mid := at()
		h.rest()
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

// Every motion comes to rest: once a palette, a toast and the transfers panel
// have come and gone, the window asks for no frames.
func TestMotionComesToRest(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.tt.Key(ui.Cmd, ui.KeyK)
	h.step(50 * time.Millisecond)
	if !h.a.pal.open {
		t.Fatal("the palette did not open")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.tt.Frame()
	h.a.toast("Hello motion")
	h.a.transfersOpen = !h.a.transfersOpen
	h.tt.Frame()
	h.step(100 * time.Millisecond)
	if !h.tt.FramePending() {
		t.Error("the toast is not fading in 100ms after it came: the test looks at no motion")
	}
	h.rest()
	if h.tt.FramePending() {
		t.Error("the window still asks for frames once everything came to rest")
	}
}

// A toast fades in where it rests.
func TestToastFadesInPlace(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.a.toast("Hello motion")
	h.tt.Frame()
	start := h.rectOf("Hello motion").Y
	h.tt.Advance(600 * time.Millisecond)
	h.tt.Frame()
	if end := h.rectOf("Hello motion").Y; start != end {
		t.Errorf("the toast moved from %.1f to %.1f as it came in", start, end)
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

// A count that changes is replaced in that frame: no digits roll.
func TestCountsDoNotRoll(t *testing.T) {
	h := newMotionHarness(t, "light")
	h.rest()
	if n := len(h.a.rows); n < 4 {
		t.Fatalf("the bucket shows %d rows, the test needs a few", n)
	}
	h.a.rows = h.a.rows[:len(h.a.rows)-1]
	h.tt.Frame()
	if h.tt.FramePending() {
		t.Error("the item count animates as it changes")
	}
}

// The strip of the collapsed transfers panel shows three counts, each its own
// element.
func TestCollapsedTransfersCountsAreSeparate(t *testing.T) {
	h := newMotionHarness(t, "light")
	seedTasks(h)
	h.a.transfersOpen = false
	h.tt.Frame()
	h.rest()
	for _, s := range []string{"3 active", "1 completed", "2 failed"} {
		h.rectOf(s)
	}
}

// Only motion.go animates, and the progress bar's fill: any other view that
// asks mygo for an animation, a transition or a rolling count breaks the rule
// of docs/dev/design.md "Motion" and must go through those two.
func TestOnlyTheTokenMoves(t *testing.T) {
	anim := regexp.MustCompile(`\.(AnimatePose|AnimateTo|AnimateColor|Transition|ContentTransition)\(|ui\.Animation\{|SpringOptions`)
	allowed := map[string]string{
		"motion.go":    "",
		"transfers.go": `AnimateTo("w", float32(frac*colProgress), baseAnim)`,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !anim.MatchString(line) {
				continue
			}
			if ok, listed := allowed[f]; listed && (ok == "" || strings.Contains(line, ok)) {
				continue
			}
			t.Errorf("%s:%d animates outside motion.go: %s", f, i+1, strings.TrimSpace(line))
		}
	}
}
