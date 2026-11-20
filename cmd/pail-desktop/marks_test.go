package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The tests of what travels (motion.go: arrive, slideTo, fadeTo). The state is
// the new one in the frame of the change, and only its mark is on the way: it
// sets off from the item that held it, is between the two halfway, and rests
// exactly where the layout puts it, when the window asks for no more frames.

// leftOf is the x of the first pixel of row y in [x0, x1) that shows col.
func leftOf(img *image.RGBA, y, x0, x1 int, col ui.Color, tol int) (float32, bool) {
	for x := x0; x < x1; x++ {
		if near(img.RGBAAt(x, y), col, tol) {
			return float32(x), true
		}
	}
	return 0, false
}

// markStrip is four 40px items in a row, the last one farther from the others
// than markReach, with a 4px Accent line under the current one. cur < 0 draws
// no line.
type markStrip struct{ cur int }

var markStripX = []float32{20, 120, 220, 620}

func (s *markStrip) view(c *ui.Context) {
	k := applyTheme(c, "light")
	strip := ui.Box(c.Key("strip")).Fill().Background(k.Background)
	g := markOf(strip)
	strip.Children(func() {
		for i, x := range markStripX {
			item := ui.Box(c.Key(i)).Absolute().Left(x).Top(20).Size(40, 20)
			item.Children(func() {
				if i == s.cur {
					arrive(c, g, i, item, func(m ui.Element) {
						m.Children(func() { ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(4).Background(k.Accent) })
					})
				}
			})
		}
	})
}

func TestMarkTravelsToTheCurrentItem(t *testing.T) {
	t.Parallel()
	s := &markStrip{}
	tt := ui.NewTester(s.view, 700, 60)
	tt.Frame()
	at := func() float32 {
		t.Helper()
		x, ok := leftOf(tt.Image(), 38, 0, 700, tokensFor(false).Accent, 8)
		if !ok {
			t.Fatal("no mark is drawn")
		}
		return x
	}
	choose := func(i int) float32 {
		t.Helper()
		s.cur = i
		tt.Frame()
		return at()
	}
	rest := func(what string, want float32) {
		t.Helper()
		tt.Advance(time.Second)
		tt.Frame()
		if x := at(); !near32(x, want, 0.5) {
			t.Errorf("%s: the mark rests at %.1f, want %.1f", what, x, want)
		}
		if tt.FramePending() {
			t.Errorf("%s: the window asks for frames with the mark at rest", what)
		}
	}
	atOnce := func(what string, i int) {
		t.Helper()
		if x := choose(i); !near32(x, markStripX[i], 0.5) || tt.FramePending() {
			t.Errorf("%s: the mark is at %.1f in the frame of the change (pending %v), want %.1f at once", what, x, tt.FramePending(), markStripX[i])
		}
	}
	if x := at(); x != 20 || tt.FramePending() {
		t.Fatalf("the first frame draws the mark at %.1f (pending %v), want it at rest at 20", x, tt.FramePending())
	}

	// To a neighbour: it sets off from where it was and is on its way halfway.
	if x := choose(1); !near32(x, 20, 0.5) {
		t.Errorf("in the frame of the change the mark is at %.1f, want it where it was (20)", x)
	}
	if !tt.FramePending() {
		t.Error("the mark does not travel: the window asks for no frames")
	}
	tt.Advance(durMove / 4)
	tt.Frame()
	mid := at()
	if mid <= 21 || mid >= 119 {
		t.Errorf("a quarter of the way in the mark is at %.1f, want it between 20 and 120", mid)
	}

	// Another item chosen meanwhile takes the mark over from where it is.
	if x := choose(2); !near32(x, mid, 2) {
		t.Errorf("the mark was at %.1f and sets off for the next item from %.1f", mid, x)
	}
	tt.Advance(durMove / 4)
	tt.Frame()
	if x := at(); x <= mid || x >= 219 {
		t.Errorf("on its second way the mark is at %.1f, want it between %.1f and 220", x, mid)
	}
	rest("after two choices", 220)

	// Farther than markReach it does not cross the window.
	atOnce("beyond reach", 3)
	atOnce("back from beyond reach", 0)

	// A group that showed no mark in the frame before has none to send.
	s.cur = -1
	tt.Frame()
	atOnce("after a frame without a mark", 1)

	// The same item again starts nothing.
	atOnce("the current item", 1)

	// With reduced motion nothing travels.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	atOnce("reduced motion", 2)
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()

	// And it still travels after all that.
	if x := choose(1); !near32(x, 220, 0.5) || !tt.FramePending() {
		t.Errorf("the mark is at %.1f (pending %v) in the frame of a later change, want it setting off from 220", x, tt.FramePending())
	}
	rest("a later change", 120)
}

// With the animations off (options.still, --film and the behaviour tests) the
// mark is at its item in the frame of the change.
func TestMarkIsThereAtOnceWithAnimationsOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	k := tokensFor(false)
	y := int(h.rectOf("Overview").Y+h.rectOf("Overview").H) - 1
	if err := h.tt.Click("Headers"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	want := h.rectOf("Headers").X
	if x, ok := leftOf(h.tt.Image(), y, 1100, testW, k.Accent, 6); !ok || !near32(x, want, 1) || h.tt.FramePending() {
		t.Errorf("the underline is at %.0f (drawn %v, pending %v), want it under Headers at %.0f at once", x, ok, h.tt.FramePending(), want)
	}
}

// The inspector's underline goes from tab to tab; the tab itself is the new
// one in the frame of the click, and no tab moves.
func TestTabUnderlineTravels(t *testing.T) {
	t.Parallel()
	h := newMotionHarness(t, "light")
	h.mustClick("manifest.json")
	h.rest()
	k := tokensFor(false)
	tabs := map[string]ui.Rect{"Overview": h.rectOf("Overview"), "Headers": h.rectOf("Headers")}
	y := int(tabs["Overview"].Y+tabs["Overview"].H) - 1
	underline := func() float32 {
		t.Helper()
		x, ok := leftOf(h.tt.Image(), y, 1100, testW, k.Accent, 6)
		if !ok {
			t.Fatal("no underline under the tabs")
		}
		return x
	}
	from, to := tabs["Overview"].X, tabs["Headers"].X
	if x := underline(); !near32(x, from, 1) {
		t.Fatalf("the underline starts at %.0f, the Overview tab at %.0f", x, from)
	}
	if err := h.tt.Click("Headers"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.inspTab != 1 {
		t.Fatalf("the tab is %d in the frame after the click, want Headers", h.a.inspTab)
	}
	if x := underline(); !near32(x, from, 1) {
		t.Errorf("in the frame of the click the underline is at %.0f, want it setting off from %.0f", x, from)
	}
	h.snapMotion("light-underline-0")
	h.step(durMove / 4)
	h.snapMotion("light-underline-55")
	if x := underline(); x <= from+1 || x >= to-1 {
		t.Errorf("on its way the underline is at %.0f, want it between %.0f and %.0f", x, from, to)
	}
	for name, r := range tabs {
		if got := h.rectOf(name); got != r {
			t.Errorf("the %s tab moved with the underline: %+v, was %+v", name, got, r)
		}
	}
	h.rest()
	if x := underline(); !near32(x, to, 1) {
		t.Errorf("the underline rests at %.0f, the Headers tab is at %.0f", x, to)
	}
	if h.tt.FramePending() {
		t.Error("the window asks for frames with the underline at rest")
	}

	// With reduced motion it is under the tab at once.
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.tt.Frame()
	if err := h.tt.Click("Overview"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if x := underline(); !near32(x, from, 1) || h.tt.FramePending() {
		t.Errorf("with reduced motion the underline is at %.0f (pending %v), want %.0f at once", x, h.tt.FramePending(), from)
	}
}

// The fill of the sidebar's current row goes to the row chosen; the bucket is
// the new one in the frame of the click.
func TestSidebarFillTravels(t *testing.T) {
	t.Parallel()
	h := newMotionHarness(t, "light")
	h.rest()
	k := tokensFor(false)
	from, to := h.rectOf("acme-assets"), h.rectOf("acme-backups")
	// 12 from the window's edge is inside the row (8) and left of its icon (16).
	filled := func(r ui.Rect) bool { return near(h.tt.Image().RGBAAt(12, int(r.Y+r.H/2)), k.Selection, 2) }
	if !filled(from) || filled(to) {
		t.Fatalf("the fill is not on the open bucket: %v, %v", filled(from), filled(to))
	}
	if err := h.tt.Click("acme-backups"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.scope.Bucket != "acme-backups" {
		t.Fatalf("the bucket is %q in the frame after the click", h.a.scope.Bucket)
	}
	if !filled(from) {
		t.Error("in the frame of the click the fill is not where it was: it does not set off from there")
	}
	if got := h.rectOf("acme-backups"); got != to {
		t.Errorf("the row moved: %+v, was %+v", got, to)
	}
	h.snapMotion("light-sidebar-0")
	h.step(durMove / 4)
	h.snapMotion("light-sidebar-55")
	h.settle()
	h.rest()
	if filled(from) || !filled(to) {
		t.Errorf("at rest the fill is on the old row (%v) or not on the new one (%v)", filled(from), filled(to))
	}
	if h.tt.FramePending() {
		t.Error("the window asks for frames with the fill at rest")
	}
}

// A segmented control's value and labels change in the frame of the click; the
// raised thumb travels under them, and between segments of unlike widths it
// takes the width of the new one on the way.
func TestSegmentedThumbTravels(t *testing.T) {
	t.Parallel()
	sel := 0
	names := []string{"On", "Now and then", "Never"}
	view := func(c *ui.Context) {
		k := applyTheme(c, "light")
		ui.Row(c).Fill().Background(k.Background).Padding(20).AlignItems(ui.Start).Children(func() {
			segmented(c, &sel, "Kind", names...)
		})
	}
	tt := ui.NewTester(view, 420, 80)
	tt.Frame()
	k := tokensFor(false)
	rects := func() map[string]ui.Rect {
		t.Helper()
		m := map[string]ui.Rect{}
		for _, s := range names {
			r, ok := tt.Find(s)
			if !ok {
				t.Fatalf("no element shows %q", s)
			}
			m[s] = r
		}
		return m
	}
	labels := rects()
	if w0, w1 := labels["On"].W, labels["Now and then"].W; w1-w0 < 40 {
		t.Fatalf("the segments are %.0f and %.0f wide: the test needs unlike widths", w0, w1)
	}
	// The thumb's fill, on a row between its edge and the labels.
	y := int(labels["On"].Y) + 2
	thumb := func() (x0, x1 float32) {
		t.Helper()
		x0 = -1
		for x := 0; x < 420; x++ {
			if near(tt.Image().RGBAAt(x, y), k.Raised, 2) {
				if x0 < 0 {
					x0 = float32(x)
				}
				x1 = float32(x + 1)
			}
		}
		if x0 < 0 {
			t.Fatal("no thumb is drawn")
		}
		return x0, x1
	}
	on := func(s string) (x0, x1 float32) { r := labels[s]; return r.X + 1, r.X + r.W - 1 } // inside its 1px edge
	check := func(what, s string) {
		t.Helper()
		x0, x1 := thumb()
		w0, w1 := on(s)
		if !near32(x0, w0, 1.5) || !near32(x1, w1, 1.5) {
			t.Errorf("%s the thumb is %.1f to %.1f, the segment %q is %.1f to %.1f", what, x0, x1, s, w0, w1)
		}
	}
	check("at first", "On")
	if err := tt.Click("Now and then"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if sel != 1 {
		t.Fatalf("the value is %d in the frame after the click", sel)
	}
	check("in the frame of the click", "On")
	tt.Advance(durMove / 4)
	tt.Frame()
	x0, x1 := thumb()
	f0, f1 := on("On")
	t0, t1 := on("Now and then")
	if !(x0 > f0+1 && x0 < t0-1) || !(x1-x0 > f1-f0+1 && x1-x0 < t1-t0-1) {
		t.Errorf("on its way the thumb is %.1f to %.1f: want it between %.1f-%.1f and %.1f-%.1f, and its width between theirs", x0, x1, f0, f1, t0, t1)
	}
	for s, r := range rects() {
		if r != labels[s] {
			t.Errorf("the segment %q moved with the thumb: %+v, was %+v", s, r, labels[s])
		}
	}

	// Another segment chosen meanwhile takes the thumb from where it is.
	if err := tt.Click("Never"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if y0, y1 := thumb(); !near32(y0, x0, 2) || !near32(y1, x1, 2) {
		t.Errorf("the thumb was %.1f to %.1f and sets off for the next segment from %.1f to %.1f", x0, x1, y0, y1)
	}
	tt.Advance(time.Second)
	tt.Frame()
	check("at rest", "Never")
	if tt.FramePending() {
		t.Error("the control asks for frames with the thumb at rest")
	}
}

// A switch is on in the frame of the click; its knob slides across and its
// track turns to the accent, and both come to rest.
func TestSwitchKnobSlides(t *testing.T) {
	t.Parallel()
	on := false
	view := func(c *ui.Context) {
		k := applyTheme(c, "light")
		ui.Column(c).Fill().Background(k.Background).Padding(20).Children(func() { switchControl(c, &on, "Test switch") })
	}
	tt := ui.NewTester(view, 200, 80)
	tt.Frame()
	k := tokensFor(false)
	knob := func() float32 {
		t.Helper()
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
	// The track above the knob, halfway along.
	track := func() ui.Color {
		p := tt.Image().RGBAAt(20+switchW/2, 21)
		return ui.Color{R: p.R, G: p.G, B: p.B, A: p.A}
	}
	from := knob()
	if !near(tt.Image().RGBAAt(20+switchW/2, 21), k.ControlOff, 2) {
		t.Fatalf("the track of a switch that is off is %v", track())
	}
	if err := tt.Click("Test switch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !on {
		t.Fatal("the switch is not on in the frame after the click")
	}
	if x := knob(); !near32(x, from, 0.5) {
		t.Errorf("in the frame of the click the knob is at %.1f, want it setting off from %.1f", x, from)
	}
	tt.Advance(durMove / 4)
	tt.Frame()
	mid, midTrack := knob(), track()
	tt.Advance(time.Second)
	tt.Frame()
	to := knob()
	if !near32(to-from, switchW-switchH, 0.5) || !(mid > from+1 && mid < to-1) {
		t.Errorf("the knob goes %.1f, %.1f, %.1f: want it %d further at rest and between on its way", from, mid, to, switchW-switchH)
	}
	if !near(tt.Image().RGBAAt(20+switchW/2, 21), k.Accent, 2) {
		t.Errorf("the track of a switch that is on is %v, want the accent %v", track(), k.Accent)
	}
	if midTrack == k.ControlOff || midTrack == track() {
		t.Errorf("on its way the track is %v: it does not fade", midTrack)
	}
	if tt.FramePending() {
		t.Error("the switch asks for frames at rest")
	}

	// With reduced motion the knob is across at once; the colour still fades.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	on = false
	tt.Frame()
	if x := knob(); !near32(x, from, 0.5) {
		t.Errorf("with reduced motion the knob is at %.1f in the frame of the change, want %.1f", x, from)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if tt.FramePending() {
		t.Error("the switch asks for frames at rest under reduced motion")
	}
}

// Settings: the rail's fill and the ring of a theme's picture travel, the
// choice is made in the frame of the click, and all of it comes to rest.
func TestSettingsMarksComeToRest(t *testing.T) {
	t.Parallel()
	h := newMotionHarness(t, "light")
	h.a.openTool("settings")
	h.settle()
	h.rest()
	// The ring is the leftmost accent of a row through the two pictures.
	paper, sky := h.rectOf("Light"), h.rectOf("Dark")
	ring := func(accent ui.Color) float32 {
		t.Helper()
		x, ok := leftOf(h.tt.Image(), int(paper.Y)+20, int(paper.X)-8, int(sky.X+sky.W)+8, accent, 3)
		if !ok {
			t.Fatal("no ring around a picture")
		}
		return x
	}
	from := ring(lightTokens.Accent)
	if from > paper.X+4 {
		t.Fatalf("the ring starts at %.0f, the picture of Light at %.0f", from, paper.X)
	}
	if err := h.tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.themeMode() != "dark" {
		t.Fatalf("the theme is %q in the frame after the click", h.a.themeMode())
	}
	if x := ring(lightTokens.Accent); !near32(x, from, 1) {
		t.Errorf("in the frame of the click the ring is at %.0f, want it setting off from %.0f", x, from)
	}
	h.snapMotion("light-ring-0")
	h.step(durMove / 4)
	h.snapMotion("light-ring-55")
	h.settle()
	h.rest()
	h.snapMotion("light-ring-rest")
	if got := px(h.tt.Image(), 4, 500); !near(got, darkTokens.Sidebar, 1) {
		t.Errorf("at rest the sidebar is %v, want Graphite's %v", got, darkTokens.Sidebar)
	}
	if x := ring(darkTokens.Accent); !near32(x-from, sky.X-paper.X, 1) {
		t.Errorf("the ring rests at %.0f, want it %.0f from where it was (%.0f)", x, sky.X-paper.X, from)
	}
	if h.tt.FramePending() {
		t.Error("the window asks for frames with the theme chosen and at rest")
	}

	rail, ok := h.tt.Find("Sections")
	if !ok {
		t.Fatal("the settings rail is not shown")
	}
	if err := h.tt.Click("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.a.srail.pin != "diagnostics" || !h.tt.FramePending() {
		t.Errorf("the rail marks %q after the click (pending %v), want diagnostics on its way", h.a.srail.pin, h.tt.FramePending())
	}
	h.snapMotion("light-rail-0")
	h.step(durMove / 4)
	h.snapMotion("light-rail-55")
	h.settle()
	h.rest()
	h.snapMotion("light-rail-rest")
	if r, _ := h.tt.Find("Sections"); r != rail {
		t.Errorf("the rail moved: %+v, was %+v", r, rail)
	}
	if h.tt.FramePending() {
		t.Error("the window asks for frames with the rail's fill at rest")
	}
}
