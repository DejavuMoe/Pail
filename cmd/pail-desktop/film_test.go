package main

// TestFilm renders the frames of the product demo video (tools/film). It drives
// the real app against the in-memory demo S3 server, like every other test
// here, but with the animations playing and the tester's clock moved one video
// frame at a time, and writes each distinct frame as a PNG together with a
// manifest of the cursor path and the events (clicks, drops, finished uploads)
// that the video's sound and motion are cut to. It does nothing unless
// PAIL_FILM_DIR is set:
//
//	$env:PAIL_FILM_DIR = "tools/film/public/capture"
//	go test ./cmd/pail-desktop -run TestFilm -count=1 -timeout 20m

import (
	"encoding/json"
	"fmt"
	"hash/maphash"
	"image"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/demo"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

const (
	filmFPS   = 60 // the timeline in tools/film is written in 30 fps frames and converts
	filmScale = 2
)

type filmEvent struct {
	Frame int     `json:"frame"`
	Kind  string  `json:"kind"`
	Name  string  `json:"name,omitempty"`
	X     float32 `json:"x,omitempty"`
	Y     float32 `json:"y,omitempty"`
}

type filmManifest struct {
	FPS     int               `json:"fps"`
	W       int               `json:"width"` // frame size in pixels
	H       int               `json:"height"`
	LogicW  int               `json:"logicalWidth"` // the window in DIPs, the unit of the cursor and events
	LogicH  int               `json:"logicalHeight"`
	Frames  []string          `json:"frames"` // the PNG shown at each video frame
	Cursor  [][]float32       `json:"cursor"` // per frame: x, y (DIPs), pressed, visible
	Events  []filmEvent       `json:"events"`
	Scenes  map[string][2]int `json:"scenes"` // first frame (inclusive) and last frame (exclusive)
	scene   string
	sceneAt int
}

type filmRec struct {
	t    *testing.T
	h    *harness
	dir  string
	m    filmManifest
	seed maphash.Seed
	sum  uint64
	have bool
	cur  string
	seq  int
	cx   float32
	cy   float32
	down bool
	show bool
	pace time.Duration
	wg   sync.WaitGroup
	sem  chan struct{}
}

// tick plays one video frame: the clock moves on, the app draws, and the frame
// is kept when it differs from the one before.
func (r *filmRec) tick() {
	r.h.tt.Advance(time.Second / filmFPS)
	r.h.tt.Frame()
	time.Sleep(r.pace) // the transfers and loads run in real time
	img := r.h.tt.Image()
	var mh maphash.Hash
	mh.SetSeed(r.seed)
	_, _ = mh.Write(img.Pix)
	if sum := mh.Sum64(); !r.have || sum != r.sum {
		r.have, r.sum = true, sum
		r.cur = fmt.Sprintf("f%05d.png", r.seq)
		r.seq++
		cp := image.NewRGBA(img.Rect)
		copy(cp.Pix, img.Pix)
		path := filepath.Join(r.dir, r.cur)
		r.sem <- struct{}{}
		r.wg.Add(1)
		go func() {
			defer func() { <-r.sem; r.wg.Done() }()
			f, err := os.Create(path)
			if err != nil {
				r.t.Error(err)
				return
			}
			defer f.Close()
			enc := png.Encoder{CompressionLevel: png.BestSpeed}
			if err := enc.Encode(f, cp); err != nil {
				r.t.Error(err)
			}
		}()
	}
	r.m.Frames = append(r.m.Frames, r.cur)
	r.m.Cursor = append(r.m.Cursor, []float32{r.cx, r.cy, b2f(r.down), b2f(r.show)})
}

func (r *filmRec) frame() int { return len(r.m.Frames) }

// f turns a duration written in frames of a 30 fps film into frames of this one.
func (r *filmRec) f(n int) int { return n * filmFPS / 30 }

// hold plays n frames of a 30 fps film (twice as many at 60 fps).
func (r *filmRec) hold(n int) {
	for range r.f(n) {
		r.tick()
	}
}

// until plays frames until cond holds; max is in frames of a 30 fps film.
func (r *filmRec) until(what string, max int, cond func() bool) {
	r.t.Helper()
	for range r.f(max) {
		if cond() {
			return
		}
		r.tick()
	}
	if !cond() {
		r.t.Fatalf("film: %s did not happen in %d frames; texts %q", what, max, r.h.tt.Texts())
	}
}

func (r *filmRec) event(kind, name string, x, y float32) {
	r.m.Events = append(r.m.Events, filmEvent{Frame: r.frame(), Kind: kind, Name: name, X: x, Y: y})
}

// scene starts a named scene; the previous one ends here.
func (r *filmRec) scene(name string) {
	r.endScene()
	r.m.scene, r.m.sceneAt = name, r.frame()
}

func (r *filmRec) endScene() {
	if r.m.scene != "" {
		r.m.Scenes[r.m.scene] = [2]int{r.m.sceneAt, r.frame()}
	}
}

// place puts the cursor somewhere without a path.
func (r *filmRec) place(x, y float32, show bool) {
	r.cx, r.cy, r.show = x, y, show
	r.h.tt.Move(x, y)
}

// moveTo glides the cursor to (x, y) along a slight arc, so that hover states
// come and go as they would under a hand.
func (r *filmRec) moveTo(x, y float32) {
	x0, y0 := r.cx, r.cy
	dist := math.Hypot(float64(x-x0), float64(y-y0))
	frames := r.f(int(math.Min(40, math.Max(12, dist/18))))
	nx, ny := -(float64(y - y0)), float64(x-x0) // the arc bends to the side
	if l := math.Hypot(nx, ny); l > 0 {
		nx, ny = nx/l, ny/l
	}
	bend := math.Min(40, dist*0.08)
	for i := 1; i <= frames; i++ {
		t := float64(i) / float64(frames)
		e := t * t * (3 - 2*t)
		arc := math.Sin(math.Pi*t) * bend
		r.cx = x0 + (x-x0)*float32(e) + float32(nx*arc)
		r.cy = y0 + (y-y0)*float32(e) + float32(ny*arc)
		r.h.tt.Move(r.cx, r.cy)
		r.tick()
	}
	r.cx, r.cy = x, y
	r.h.tt.Move(x, y)
}

// at finds the element showing text and returns its center.
func (r *filmRec) at(text string) (float32, float32) {
	r.t.Helper()
	rect, ok := r.h.tt.Find(text)
	if !ok {
		r.t.Fatalf("film: no element shows %q; texts %q", text, r.h.tt.Texts())
	}
	return rect.X + rect.W/2, rect.Y + rect.H/2
}

func (r *filmRec) press(x, y float32, name string) {
	r.event("click", name, x, y)
	r.down = true
	r.h.tt.Press(x, y)
	r.hold(2)
	r.h.tt.Release(x, y)
	r.down = false
	r.hold(1)
}

func (r *filmRec) clickAt(x, y float32, name string) {
	r.moveTo(x, y)
	r.hold(3)
	r.press(x, y, name)
}

func (r *filmRec) click(text string) {
	r.t.Helper()
	x, y := r.at(text)
	r.clickAt(x, y, text)
}

func (r *filmRec) doubleClick(text string) {
	r.t.Helper()
	x, y := r.at(text)
	r.moveTo(x, y)
	r.hold(3)
	r.press(x, y, text)
	r.press(x, y, text)
}

func (r *filmRec) typeSlow(s string, perChar int) {
	for _, c := range s {
		r.h.tt.Type(string(c))
		r.event("key", string(c), 0, 0)
		r.hold(perChar)
	}
}

func (r *filmRec) key(mods ui.Modifiers, k ui.Key, name string) {
	r.h.tt.Key(mods, k)
	r.event("key", name, 0, 0)
	r.hold(1)
}

func (r *filmRec) loaded() bool { return r.h.a.idle() && len(r.h.a.pending) == 0 }

// fillFile writes a deterministic file of size bytes.
func fillFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte((i*131 + i>>8*7) & 0xff)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for left := size; left > 0; left -= len(block) {
		n := min(left, len(block))
		if _, err := f.Write(block[:n]); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestFilm(t *testing.T) {
	dir := os.Getenv("PAIL_FILM_DIR")
	if dir == "" {
		t.Skip("PAIL_FILM_DIR is not set")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	env, err := newDemoEnv(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	h := attachMotion(t, env, "light", false)
	h.tt.SetScale(filmScale)
	h.tt.Frame()
	img := h.tt.Image()

	r := &filmRec{t: t, h: h, dir: dir, seed: maphash.MakeSeed(), pace: 12 * time.Millisecond, sem: make(chan struct{}, 4)}
	r.m = filmManifest{FPS: filmFPS, W: img.Rect.Dx(), H: img.Rect.Dy(), LogicW: testW, LogicH: testH, Scenes: map[string][2]int{}}
	t.Logf("frame size %dx%d", r.m.W, r.m.H)

	// The manifest is written even when a scene fails, to look at what was drawn.
	defer func() {
		r.endScene()
		r.wg.Wait()
		b, _ := json.MarshalIndent(r.m, "", " ")
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
			t.Error(err)
		}
		t.Logf("%d video frames, %d distinct pictures", r.frame(), r.seq)
	}()

	files := t.TempDir()
	filmBrowse(r)
	filmUpload(r, env, files)
	filmShare(r)
	filmVersions(r)
	filmTheme(r)
}

// ---- scenes ---------------------------------------------------------------------------------

func filmBrowse(r *filmRec) {
	r.scene("browse")
	h := r.h
	r.place(1180, 760, false)
	r.hold(24)
	r.show = true
	r.click("acme-assets")
	r.until("bucket", 60, r.loaded)
	r.hold(12)
	r.doubleClick("assets")
	r.until("assets/", 60, func() bool { return h.a.scope.Prefix == "assets/" && r.loaded() })
	r.hold(8)
	r.doubleClick("images")
	r.until("assets/images/", 60, func() bool { return h.a.scope.Prefix == "assets/images/" && r.loaded() })
	r.hold(8)
	r.click("hero.png")
	r.until("inspector", 60, r.loaded)
	r.hold(10)
	r.click("Preview")
	r.until("preview", 90, func() bool { return h.a.prev.state == previewReady })
	r.hold(60)
}

func filmUpload(r *filmRec, env *demoEnv, files string) {
	r.scene("upload")
	t, h := r.t, r.h
	paths := []string{
		writeTempIn(t, files, "team-photo.png", demo.Image("team-photo", 3)),
		writeTempIn(t, files, "sunset.png", demo.Image("sunset", 11)),
		fillFile(t, files, "launch-trailer.mp4", 96<<20),
	}
	env.Srv.Inject(func(rq *http.Request) *s3test.Fault {
		if rq.Method == http.MethodPut && rq.URL.Query().Get("partNumber") != "" {
			pn, _ := strconv.Atoi(rq.URL.Query().Get("partNumber")) // later parts take longer: the bar fills steadily
			return &s3test.Fault{Delay: time.Duration(pn) * 800 * time.Millisecond}
		}
		return nil
	})
	t.Cleanup(func() { env.Srv.Inject(nil) })
	r.hold(60) // the files fly in over the window
	r.event("drop", "files", 720, 450)
	if !h.tt.DropFiles(720, 450, paths...) {
		t.Fatal("film: nothing took the dropped files")
	}
	r.until("uploads", 900, func() bool {
		tk, ok := h.task("Upload launch-trailer.mp4")
		return ok && tk.Status == core.StatusSucceeded
	})
	r.event("done", "uploads", 0, 0)
	r.hold(45)
	for _, k := range []string{"team-photo.png", "sunset.png", "launch-trailer.mp4"} {
		if _, ok := env.Srv.Object("acme-assets", "assets/images/"+k); !ok {
			t.Errorf("film: %s was not uploaded", k)
		}
	}
	env.Srv.Inject(nil)
}

func writeTempIn(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// palette opens the command palette with the keyboard, types query and runs
// the first match.
func (r *filmRec) palette(query string) {
	r.key(ui.Cmd, ui.KeyK, "palette")
	r.hold(14)
	r.typeSlow(query, 3)
	r.hold(14)
	r.key(0, ui.KeyEnter, "enter")
	r.hold(4)
}

func filmShare(r *filmRec) {
	r.scene("share")
	h := r.h
	r.click("team-photo.png")
	r.until("inspector", 60, r.loaded)
	r.hold(14)
	r.palette("signed")
	r.until("signed dialog", 60, func() bool { return h.a.objOps.sg.open })
	r.hold(30)
	r.click("Generate")
	r.until("signed url", 90, func() bool { return len(h.a.objOps.sg.res) > 0 })
	r.hold(36)
	// The dialog's Copy button sits right of Close; the name "Copy" alone would
	// find the toolbar's.
	cx, cy := r.at("Close")
	r.clickAt(cx+64, cy, "Copy")
	r.event("copied", "signed url", 0, 0)
	r.hold(50)
	r.click("Close")
	r.hold(20)
}

func filmVersions(r *filmRec) {
	r.scene("versions")
	h := r.h
	r.click(demoVersioned)
	r.until("versioned bucket", 60, func() bool { return h.a.scope.Bucket == demoVersioned && r.loaded() })
	r.hold(14)
	r.click("notes.txt")
	r.until("inspector", 60, r.loaded)
	r.hold(8)
	r.click("Versions")
	r.until("versions", 90, func() bool { return h.a.mg.vers.state == 2 })
	r.hold(45)
	r.click("Restore this version")
	r.until("restored", 90, func() bool { return len(h.env.Srv.Versions(demoVersioned, "notes.txt")) == 4 })
	r.event("done", "restored", 0, 0)
	r.hold(60)
}

func filmTheme(r *filmRec) {
	r.scene("theme")
	r.show = false
	r.hold(20)
	r.palette("dark")
	r.until("dark theme", 60, func() bool { return r.h.a.themeMode() == "dark" })
	r.event("theme", "dark", 0, 0)
	r.hold(75)
}
