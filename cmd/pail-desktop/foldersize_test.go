package main

import (
	"bytes"
	"image/png"
	"net/http"
	"runtime"
	"slices"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo"
)

const sizeBkt = "acme-assets"

// seedSizes puts a folder "fs/" with two sub-folders into the browsed bucket
// and opens it.
func (h *harness) seedSizes() {
	h.t.Helper()
	for k, n := range map[string]int{
		"fs/alpha/1.bin":     1024,
		"fs/alpha/sub/2.bin": 2048,
		"fs/beta/3.bin":      10 * 1024,
		"fs/top.txt":         7,
	} {
		h.env.Srv.PutObject(sizeBkt, k, make([]byte, n), "", nil)
	}
	h.a.openPrefix("fs/")
	h.settle()
}

// failListing denies the flat listings of prefix, as a folder size makes them;
// the table's own listings (with a delimiter) go through.
func failListing(prefix string) func(*http.Request) *s3test.Fault {
	return func(r *http.Request) *s3test.Fault {
		q := r.URL.Query()
		if r.Method == http.MethodGet && q.Has("list-type") && q.Get("prefix") == prefix && q.Get("delimiter") == "" {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "listing denied"}
		}
		return nil
	}
}

func (h *harness) sizeState(folder string) int {
	if fs := h.a.folderSizeOf(folder); fs != nil {
		return fs.state
	}
	return -1
}

func TestFolderSizesAreCalculatedOnRequest(t *testing.T) {
	h := newHarness(t, "light")
	h.seedSizes()
	// Nothing is listed below a folder until asked: its Size shows a dash.
	if h.sizeState("fs/alpha/") != -1 || h.sizeState("fs/beta/") != -1 {
		t.Fatal("a folder was sized without being asked")
	}
	h.mustHave("–", "7 B")

	// The folder's context menu sizes that folder only.
	h.rightClickChoose("alpha", "Calculate size")
	h.waitFor("alpha sized", func() bool { return h.sizeState("fs/alpha/") == sizeReady })
	h.settle()
	if fs := h.a.folderSizeOf("fs/alpha/"); fs.est != (core.Estimate{Objects: 2, Bytes: 3072}) {
		t.Fatalf("alpha: %+v", fs.est)
	}
	h.mustHave("3.0 KB")
	if h.sizeState("fs/beta/") != -1 {
		t.Fatal("beta was sized with alpha")
	}
	// A folder already sized has nothing left to calculate.
	h.a.setSingle("fs/alpha/")
	if h.a.enabled(h.a.cmd("foldersize")) {
		t.Fatal("Calculate size stays enabled for a sized folder")
	}
	h.a.setSingle("fs/top.txt")
	if h.a.enabled(h.a.cmd("foldersize")) {
		t.Fatal("Calculate size is enabled for a file")
	}

	// The toolbar command sizes every folder shown.
	h.runCmd("foldersizes")
	h.settle()
	if h.sizeState("fs/beta/") != sizeReady {
		t.Fatalf("beta: state %d", h.sizeState("fs/beta/"))
	}
	h.mustHave("10.0 KB")
	if h.a.enabled(h.a.cmd("foldersizes")) {
		t.Fatal("Calculate folder sizes stays enabled with every folder sized")
	}

	// Folders sort by their size among themselves, still before the files.
	h.a.sort.Column, h.a.sort.Descending = "size", true
	h.a.rebuildRows()
	var order []string
	for _, e := range h.a.rows {
		order = append(order, e.Key)
	}
	if !slices.Equal(order, []string{"fs/beta/", "fs/alpha/", "fs/top.txt"}) {
		t.Fatalf("size order %v", order)
	}

	// Browsing elsewhere keeps the sizes; a refresh drops them.
	h.a.openPrefix("fs/alpha/")
	h.settle()
	h.a.up()
	h.settle()
	if h.sizeState("fs/alpha/") != sizeReady {
		t.Fatal("sizes were dropped by navigation")
	}
	h.runCmd("refresh")
	h.settle()
	if h.sizeState("fs/alpha/") != -1 || h.sizeState("fs/beta/") != -1 {
		t.Fatal("a refresh kept sizes that may be out of date")
	}
}

func TestFolderSizesGoStaleWithChangesAboveAndBelow(t *testing.T) {
	h := newHarness(t, "light")
	h.seedSizes()
	h.runCmd("foldersizes")
	h.settle()
	// A change inside fs/alpha/sub/ makes fs/alpha/ (above it) wrong, not fs/beta/.
	sub := h.a.scope
	sub.Prefix = "fs/alpha/sub/"
	h.a.dropSizes(sub)
	if h.sizeState("fs/alpha/") != -1 || h.sizeState("fs/beta/") != sizeReady {
		t.Fatalf("alpha %d beta %d", h.sizeState("fs/alpha/"), h.sizeState("fs/beta/"))
	}
	// Another bucket's change touches none of them.
	other := h.a.scope
	other.Bucket, other.Prefix = "elsewhere", ""
	h.a.dropSizes(other)
	if h.sizeState("fs/beta/") != sizeReady {
		t.Fatal("a change in another bucket dropped a size")
	}
}

func TestFolderSizeFailureIsShownAndCanBeRetried(t *testing.T) {
	h := newHarness(t, "light")
	h.seedSizes()
	h.env.Srv.Inject(failListing("fs/beta/"))
	h.a.setSingle("fs/beta/")
	h.runCmd("foldersize")
	h.settle()
	if h.sizeState("fs/beta/") != sizeFailed {
		t.Fatalf("state %d", h.sizeState("fs/beta/"))
	}
	h.mustHave("Failed")
	h.env.Srv.Inject(nil)
	if !h.a.enabled(h.a.cmd("foldersize")) {
		t.Fatal("a failed size cannot be asked for again")
	}
	h.runCmd("foldersize")
	h.settle()
	if h.sizeState("fs/beta/") != sizeReady {
		t.Fatalf("retry: state %d", h.sizeState("fs/beta/"))
	}
}

func TestTrayStatusFollowsTransfers(t *testing.T) {
	if got := trayStatus(nil); got != "No transfers in progress" {
		t.Fatalf("idle: %q", got)
	}
	running := []Task{
		{ID: "1", Kind: core.KindUpload, Status: core.StatusRunning, BytesTotal: 200, BytesDone: 50},
		{ID: "2", Kind: core.KindDownload, Status: core.StatusRunning, BytesTotal: 200, BytesDone: 50},
	}
	if got := trayStatus(running); got != "2 transfers · 25%" {
		t.Fatalf("running: %q", got)
	}
	if got := trayStatus([]Task{{ID: "1", Kind: core.KindUpload, Status: core.StatusQueued}}); got != "1 transfer in progress" {
		t.Fatalf("queued: %q", got)
	}
	if got := trayTitle(mygo.ProgressBar{State: mygo.ProgressNormal, Value: 0.07}); got != "  7%" {
		t.Fatalf("title %q", got)
	}
	if trayTitle(mygo.ProgressBar{}) != "" {
		t.Fatal("a title without transfers")
	}
}

func TestTrayGlyphIsATemplate(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayGlyph(32)))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("size %v", b)
	}
	opaque := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 && (r|g|bl) != 0 {
				t.Fatalf("(%d,%d) is not black: a template is black and alpha", x, y)
			}
			if a == 0xffff {
				opaque++
			}
		}
	}
	// The corners are clear, the three strata are drawn, and the gaps between
	// them stay clear at the 16px the taskbar shows.
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("corner drawn")
	}
	if opaque < 300 {
		t.Fatalf("only %d opaque pixels", opaque)
	}
	small, _ := png.Decode(bytes.NewReader(trayGlyph(16)))
	for _, y := range []int{5, 9} { // the gaps between the bars, at 16px
		if _, _, _, a := small.At(8, y).RGBA(); a > 0x4000 {
			t.Fatalf("gap at y=%d is filled (alpha %d)", y, a)
		}
	}
}

func TestToastsBecomeNotificationsWhileTheWindowIsHidden(t *testing.T) {
	h := newHarness(t, "light")
	var sent []string
	shown := false
	h.a.tray.visible = func() bool { return shown }
	h.a.tray.notify = func(msg string) { sent = append(sent, msg) }
	h.a.toast("Uploaded 3 files")
	if len(sent) != 1 || len(h.a.toasts) != 0 {
		t.Fatalf("hidden: sent %q toasts %d", sent, len(h.a.toasts))
	}
	shown = true
	h.a.toast("Key copied")
	if len(sent) != 1 || len(h.a.toasts) != 1 {
		t.Fatalf("shown: sent %q toasts %d", sent, len(h.a.toasts))
	}
}

func TestTraySettingIsSavedAndApplied(t *testing.T) {
	h := newHarness(t, "light")
	if !h.a.tray.on {
		t.Fatal("the icon is off by default")
	}
	var applied []bool
	h.a.tray.set = func(on bool) { applied = append(applied, on) }
	h.a.openTool("settings")
	h.settle()
	label := "Show in notification area"
	if runtime.GOOS == "darwin" {
		label = "Show in menu bar"
	}
	h.mustHave(label)
	h.a.setTrayShown(false)
	h.settle()
	if h.a.tray.on || !slices.Equal(applied, []bool{false}) || !h.a.svc.Settings().HideTrayIcon {
		t.Fatalf("on=%v applied=%v saved hide=%v", h.a.tray.on, applied, h.a.svc.Settings().HideTrayIcon)
	}
	h.a.setTrayShown(true)
	h.settle()
	if !h.a.tray.on || h.a.svc.Settings().HideTrayIcon {
		t.Fatal("turning it back on was not saved")
	}
}
