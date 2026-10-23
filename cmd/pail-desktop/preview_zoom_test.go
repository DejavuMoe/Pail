package main

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// openImagePreview shows the 320×200 demo picture in the Preview tab.
func (h *harness) openImagePreview() ui.Rect {
	h.t.Helper()
	h.a.openPrefix("assets/images/")
	h.settle()
	h.mustClick("hero.png")
	h.mustClick("Preview")
	h.waitFor("image preview", func() bool { return h.a.prev.state == previewReady && h.a.prev.kind == "image" })
	h.settle()
	return h.rectOf("Preview of hero.png")
}

// under is where (px, py) is over the zoomed picture, as fractions of its
// size, from the frame r and the frame's scrolling.
func under(h *harness, r ui.Rect, px, py float32) (float32, float32) {
	p := &h.a.prev
	z := p.zoomed()
	return (p.pan.X + px - r.X) / (r.W * z), (p.pan.Y + py - r.Y) / (r.H * z)
}

func zoomText(h *harness, z float32) string {
	return fmt.Sprintf("320 × 200 · %.0f%%", math.Round(float64(h.a.previewImageW()*z/320*100)))
}

// The picture fills the inspector's width up to its padding, as before it
// could zoom, and its size line says how large it shows.
func TestImagePreviewFitsThePanel(t *testing.T) {
	h := newHarness(t, "light")
	r := h.openImagePreview()
	fw := h.a.previewImageW()
	if math.Abs(float64(r.W-fw)) > 0.5 || math.Abs(float64(r.H-fw*200/320)) > 0.5 {
		t.Fatalf("picture %v×%v, want %v×%v", r.W, r.H, fw, fw*200/320)
	}
	if right := r.X + r.W + 16; math.Abs(float64(right-testW)) > 0.5 {
		t.Errorf("picture ends %v from the window's right edge, want the inspector's padding 16", testW-r.X-r.W)
	}
	h.mustHave(zoomText(h, 1))
	h.mustClick("Zoom out") // off while the picture fits
	if h.a.prev.zoomed() != 1 {
		t.Errorf("Zoom out at the fitted size zoomed to %v", h.a.prev.zoomed())
	}
}

// A pinch zooms at the pointer: what was under it stays under it.
func TestPinchZoomsTheImageAtThePointer(t *testing.T) {
	h := newHarness(t, "light")
	r := h.openImagePreview()
	px, py := r.X+r.W*0.75, r.Y+r.H*0.25
	h.tt.Pinch(px, py, 2)
	h.settle()
	if h.a.prev.zoomed() != 2 {
		t.Fatalf("zoom %v after pinching by 2", h.a.prev.zoomed())
	}
	// The tester's box of the picture is clipped to its frame; where the
	// frame scrolled to says which part shows.
	u, v := under(h, r, px, py)
	if math.Abs(float64(u-0.75)) > 0.01 || math.Abs(float64(v-0.25)) > 0.01 {
		t.Errorf("the pointer is over %.3f, %.3f of the picture after the pinch, want 0.75, 0.25", u, v)
	}
	h.mustHave(zoomText(h, 2))
	h.snap("preview-image-zoomed-light")

	// The frame keeps the fitted size: the panel below does not move.
	f := h.rectOf("Zoom in")
	h.tt.Pinch(px, py, 0.1)
	h.settle()
	if h.a.prev.zoomed() != 1 || h.a.prev.pan.X != 0 || h.a.prev.pan.Y != 0 {
		t.Fatalf("pinching out past the fitted size left zoom %v, pan %v,%v", h.a.prev.zoomed(), h.a.prev.pan.X, h.a.prev.pan.Y)
	}
	if g := h.rectOf("Zoom in"); g.Y != f.Y {
		t.Errorf("the zoom buttons moved from %v to %v", f.Y, g.Y)
	}
}

// Zoomed in, scrolling pans the picture inside its frame; the buttons step
// the zoom about the frame's centre and stop at the ends.
func TestZoomedImagePansAndSteps(t *testing.T) {
	h := newHarness(t, "light")
	r := h.openImagePreview()
	h.mustClick("Zoom in")
	h.mustClick("Zoom in")
	if z := h.a.prev.zoomed(); math.Abs(float64(z-zoomStep*zoomStep)) > 1e-4 {
		t.Fatalf("zoom %v after two steps, want %v", z, zoomStep*zoomStep)
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	if u, v := under(h, r, cx, cy); math.Abs(float64(u-0.5)) > 0.01 || math.Abs(float64(v-0.5)) > 0.01 {
		t.Errorf("the buttons zoomed about %.3f, %.3f of the picture, want its centre", u, v)
	}
	x, y := h.a.prev.pan.X, h.a.prev.pan.Y
	h.tt.Scroll(cx, cy, 20, 10)
	h.settle()
	if h.a.prev.pan.X != x+20 || h.a.prev.pan.Y != y+10 {
		t.Errorf("scrolling by 20, 10 panned from %v,%v to %v,%v", x, y, h.a.prev.pan.X, h.a.prev.pan.Y)
	}
	h.tt.Move(cx, cy)
	h.tt.Press(cx, cy)
	for i := float32(1); i <= 3; i++ {
		h.tt.Move(cx+10*i, cy+5*i)
		h.tt.Frame()
	}
	h.tt.Release(cx+30, cy+15)
	h.settle()
	if h.a.prev.pan.X != x-10 || h.a.prev.pan.Y != y-5 {
		t.Errorf("dragging by 30, 15 panned from %v,%v to %v,%v", x+20, y+10, h.a.prev.pan.X, h.a.prev.pan.Y)
	}
	for range 20 {
		h.mustClick("Zoom in")
	}
	if h.a.prev.zoomed() != maxZoom {
		t.Errorf("zoom %v after many steps, want the limit %v", h.a.prev.zoomed(), maxZoom)
	}
	for range 20 {
		h.mustClick("Zoom out")
	}
	if h.a.prev.zoomed() != 1 || h.a.prev.pan.X != 0 || h.a.prev.pan.Y != 0 {
		t.Errorf("zooming all the way out left zoom %v, pan %v,%v", h.a.prev.zoomed(), h.a.prev.pan.X, h.a.prev.pan.Y)
	}

	// Another object starts fitted.
	h.mustClick("Zoom in")
	h.a.openPrefix("assets/images/")
	h.settle()
	h.mustClick("hero.png")
	h.settle()
	if h.a.prev.zoomed() != 1 {
		t.Errorf("a newly opened picture starts zoomed %v", h.a.prev.zoomed())
	}
}

// The wheel with Ctrl held zooms a step a notch, a touchpad as far as it
// moves; without Ctrl the scrolling is the frame's.
func TestCtrlWheelZoomsTheImage(t *testing.T) {
	var p previewState
	const fw, fh = 288, 180
	if !p.zoomInput(ui.InputEvent{Kind: ui.InputScroll, DY: -100, Mods: ui.Ctrl, X: 144, Y: 90}, fw, fh) || p.zoomed() != zoomStep {
		t.Fatalf("Ctrl and a notch up zoomed to %v, want %v", p.zoomed(), zoomStep)
	}
	if !p.zoomInput(ui.InputEvent{Kind: ui.InputScroll, DY: 100, Mods: ui.Ctrl, X: 144, Y: 90}, fw, fh) || p.zoomed() != 1 {
		t.Fatalf("Ctrl and a notch down zoomed to %v, want 1", p.zoomed())
	}
	if !p.zoomInput(ui.InputEvent{Kind: ui.InputScroll, DY: -40, Mods: ui.Ctrl, Precise: true}, fw, fh) {
		t.Fatal("Ctrl and a touchpad's scrolling were not taken")
	}
	if want := float32(math.Exp(0.2)); math.Abs(float64(p.zoomed()-want)) > 1e-4 {
		t.Errorf("a touchpad's 40 DIPs zoomed to %v, want %v", p.zoomed(), want)
	}
	if p.zoomInput(ui.InputEvent{Kind: ui.InputScroll, DY: -100}, fw, fh) {
		t.Error("scrolling without Ctrl was taken from the frame")
	}
}
