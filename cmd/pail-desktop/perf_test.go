package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// These tests pin the cost of the UI's bookkeeping by counting what it does,
// not by timing it: how many times a task list was indexed, how many batches
// reached the UI thread, how many times a derived value was made again.

// ---- task events --------------------------------------------------------------------------------

func finishedTasks(n int) []Task {
	ts := make([]Task, n)
	for i := range ts {
		ts[i] = Task{ID: fmt.Sprintf("t%06d", i), Kind: core.KindUpload, Status: core.StatusSucceeded, Created: time.Unix(int64(i), 0)}
	}
	return ts
}

// The removals of one drain (Clear finished, the retention dropping the oldest)
// are applied in one pass: the list is compacted and indexed once, however
// many tasks go.
func TestTaskRemovalsAreAppliedInOnePass(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	const n = 50000
	h.a.setTasks(finishedTasks(n))
	reindexed, applied := h.a.reindexes, h.a.applies
	for i := 0; i < n; i += 2 {
		h.a.onEvent(core.Event{Task: Task{ID: fmt.Sprintf("t%06d", i)}, Removed: true})
	}
	h.a.drain()
	if h.a.applies != applied+1 {
		t.Fatalf("%d batches applied, want 1", h.a.applies-applied)
	}
	if h.a.reindexes != reindexed {
		t.Fatalf("the task list was indexed from scratch %d times for one drain of removals", h.a.reindexes-reindexed)
	}
	if len(h.a.tasks) != n/2 || len(h.a.taskIdx) != n/2 {
		t.Fatalf("%d tasks, %d indexed, want %d", len(h.a.tasks), len(h.a.taskIdx), n/2)
	}
	for i, tk := range h.a.tasks {
		if want := fmt.Sprintf("t%06d", 2*i+1); tk.ID != want || h.a.taskIdx[tk.ID] != i {
			t.Fatalf("position %d holds %s (indexed at %d), want %s", i, tk.ID, h.a.taskIdx[tk.ID], want)
		}
	}
	// A task dropped and sent again in one drain is the new one.
	h.a.onEvent(core.Event{Task: Task{ID: "t000001"}, Removed: true})
	h.a.onEvent(core.Event{Task: Task{ID: "t000001", Status: core.StatusQueued, Kind: core.KindUpload}})
	h.a.drain()
	if i, ok := h.a.taskIdx["t000001"]; !ok || h.a.tasks[i].Status != core.StatusQueued || len(h.a.tasks) != n/2 {
		t.Fatalf("re-added task: idx %d %v, %d tasks", i, ok, len(h.a.tasks))
	}
}

// Progress events reach the UI thread at a bounded rate, as one batch with the
// latest numbers; anything else is applied at once.
func TestProgressEventsAreCoalesced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.settle()
	applied := h.a.applies
	run := func(done int64) core.Event {
		return core.Event{Task: Task{ID: "p", Kind: core.KindUpload, Status: core.StatusRunning, BytesTotal: 1000, BytesDone: done}}
	}
	for i := int64(1); i <= 1000; i++ {
		h.a.onEvent(run(i))
	}
	h.a.drain()
	if got := h.a.applies - applied; got != 1 {
		t.Fatalf("%d batches for 1000 progress events, want the first one at once", got)
	}
	// The rest waits for its slot, and is not lost.
	h.waitFor("the last progress", func() bool {
		i, ok := h.a.taskIdx["p"]
		return ok && h.a.tasks[i].BytesDone == 1000
	})
	if got := h.a.applies - applied; got > 3 {
		t.Fatalf("%d batches for 1000 progress events, want at most 3", got)
	}
	// A finish is not made to wait for the slot.
	applied = h.a.applies
	h.a.onEvent(run(1000))
	h.a.onEvent(core.Event{Task: Task{ID: "p", Kind: core.KindUpload, Status: core.StatusSucceeded, BytesTotal: 1000, BytesDone: 1000}})
	h.a.drain()
	if i := h.a.taskIdx["p"]; h.a.tasks[i].Status != core.StatusSucceeded || h.a.applies != applied+1 {
		t.Fatalf("the finish was not applied at once: %v, %d batches", h.a.tasks[i].Status, h.a.applies-applied)
	}
}

// ---- what a frame derives ----------------------------------------------------------------------

// A frame in which neither the choice nor the rows moved makes none of the
// values derived from them again; a click mygo's table handles does.
func TestFramesReuseWhatIsDerivedFromTheSelection(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	if stampField < 0 {
		t.Fatal("mygo's Selection has no stamp to read any more: every frame would recompute")
	}
	h.a.selectAll()
	h.tt.Frame()
	h.tt.Frame()
	b := h.a.vc.builds
	for range 5 {
		h.tt.Frame()
	}
	if got := h.a.vc.builds; got != b {
		t.Fatalf("five frames with nothing changed built %+v, want %+v", got, b)
	}
	// mygo's table changes the choice on a click, past the counters of ours.
	h.mustClick("manifest.json")
	h.tt.Frame()
	if keys := h.a.selectedKeys(); len(keys) != 1 || keys[0] != "manifest.json" {
		t.Fatalf("selection after the click: %v", keys)
	}
	if got := h.a.vc.builds; got.keys == b.keys || got.selSum == b.selSum {
		t.Fatalf("a click did not invalidate what is derived from the choice: %+v then %+v", b, got)
	}
	b = h.a.vc.builds
	for range 3 {
		h.tt.Frame()
	}
	if got := h.a.vc.builds; got != b {
		t.Fatalf("frames after the click built %+v, want %+v", got, b)
	}
}

func TestSelectionStampSeesEveryChangeOfMygo(t *testing.T) {
	t.Parallel()
	var s selection
	s.touch()
	gen := s.gen
	s.sync()
	if s.gen != gen {
		t.Fatal("a sync with nothing changed counts as a change")
	}
	// In this order: removing "a" changes nothing unless it was added first.
	for _, c := range []struct {
		name string
		f    func()
	}{
		{"add", func() { s.Selection.Add("a") }},
		{"remove", func() { s.Selection.Remove("a") }},
		{"clear", func() { s.Selection.Add("b"); s.sync(); s.Selection.Clear() }},
	} {
		before := s.gen
		c.f()
		s.sync()
		if s.gen == before {
			t.Errorf("%s through mygo's own methods went unnoticed", c.name)
		}
	}
}

func TestListingDerivedValuesAreKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.tt.Frame()
	a := h.a
	a.folderSummary()
	canSizeShown(a)
	b := a.vc.builds
	for range 4 {
		h.tt.Frame()
		a.folderSummary()
		canSizeShown(a)
	}
	if got := a.vc.builds; got != b {
		t.Fatalf("frames over an unchanged listing built %+v, want %+v", got, b)
	}
	// The listing changing is what makes them again.
	a.raw = append(a.raw, Entry{Key: "zzz/", Name: "zzz", Folder: true})
	a.rawGen++
	if f, _, _ := a.folderSummary(); f != 7 {
		t.Fatalf("%d folders after one was added, want 7", f)
	}
	// Sizing a folder changes the answer of canSizeShown without the rows moving.
	a.rebuildRows()
	if !canSizeShown(a) {
		t.Fatal("folders are shown and none is being sized")
	}
	for _, e := range a.rows {
		if e.Folder {
			s := a.scope
			s.Prefix = e.Key
			a.sizes.by = map[string]*folderSize{}
			for _, e2 := range a.rows {
				if e2.Folder {
					s.Prefix = e2.Key
					a.sizes.by[sizeKey(s.AccountID, s.Bucket, e2.Key)] = &folderSize{scope: s, state: sizeRunning}
				}
			}
			a.sizeGen++
			break
		}
	}
	if canSizeShown(a) {
		t.Fatal("every shown folder is being sized, nothing is left to ask for")
	}
}

func TestFilterDoesNotSortAgain(t *testing.T) {
	t.Parallel()
	a := benchApp(2000)
	a.vc.builds.sorted = 0
	for _, q := range []string{"0001", "0001 ", "report-0000012", "", "REPORT-00000"} {
		a.search = q
		a.rebuildRows()
	}
	if got := a.vc.builds.sorted; got != 0 {
		t.Fatalf("filtering sorted the listing %d times", got)
	}
	a.search = "report-0000019"
	a.rebuildRows()
	if len(a.rows) != 10 || a.rows[0].Name != "folder/report-00000190.json" {
		t.Fatalf("filtered rows %d, first %q", len(a.rows), a.rows[0].Name)
	}
	a.sort = ui.SortOrder{Column: "size", Descending: true}
	a.rebuildRows()
	if a.vc.builds.sorted != 1 || a.rows[0].Name != "folder/report-00000199.json" {
		t.Fatalf("a new sort order: %d sorts, first %q", a.vc.builds.sorted, a.rows[0].Name)
	}
	a.search = ""
	a.raw = append(a.raw, Entry{Key: "x", Name: "report-new", Size: 1 << 40})
	a.rawGen++
	a.rebuildRows()
	if a.vc.builds.sorted != 2 || a.rows[0].Name != "report-new" {
		t.Fatalf("a new listing: %d sorts, first %q", a.vc.builds.sorted, a.rows[0].Name)
	}
}

func TestFolderSizesArriveInBatches(t *testing.T) {
	t.Parallel()
	a := benchApp(10)
	a.sort = ui.SortOrder{Column: "size"}
	a.rebuildRows()
	a.post = func(func()) {} // the wake-up timer finds nobody to wake
	a.sizeDirty = true
	if a.rowsDue() {
		t.Fatal("a folder size that just arrived rebuilt the rows at once, right after a rebuild")
	}
	a.rebuiltAt = time.Now().Add(-time.Second)
	if !a.rowsDue() {
		t.Fatal("the folder sizes that arrived were not applied after the wait")
	}
	a.rowsDirty = true
	a.rebuiltAt = time.Now()
	if !a.rowsDue() {
		t.Fatal("a finished listing waited")
	}
}

func TestClipboardLookupBuildsItsSetOnce(t *testing.T) {
	t.Parallel()
	a := benchApp(500)
	a.mg.clip = clipData{loc: a.scope, cut: true, keys: []string{a.rows[3].Key, a.rows[4].Key}}
	got := 0
	for range 4 {
		for _, e := range a.rows {
			if a.isCutRow(e.Key) {
				got++
			}
		}
	}
	if got != 8 || a.vc.builds.clip != 1 {
		t.Fatalf("%d cut rows over four passes (want 8), set built %d times (want 1)", got, a.vc.builds.clip)
	}
	a.mg.clip = clipData{loc: a.scope, cut: true, keys: []string{a.rows[7].Key}}
	if !a.isCutRow(a.rows[7].Key) || a.isCutRow(a.rows[3].Key) || a.vc.builds.clip != 2 {
		t.Fatal("a new clipboard did not replace the set")
	}
}

// The commands the native menu asks about every frame are answered from the
// summary, not from the keys.
func TestMenuPredicatesDoNotWalkTheSelection(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.tt.Frame()
	h.a.vc.builds.keys = 0
	for range 10 {
		if !hasObjectSelection(h.a) {
			t.Fatal("an object is chosen")
		}
		hasCDNSelection(h.a)
	}
	if h.a.vc.builds.keys != 0 {
		t.Fatalf("the predicates built the selected keys %d times", h.a.vc.builds.keys)
	}
}

// ---- preview ----------------------------------------------------------------------------------------

// pngHeader is just enough PNG for image.DecodeConfig: the signature and an
// IHDR of the given size, no pixels.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8 bits, RGBA
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestPreviewRefusesPicturesWithTooManyPixels(t *testing.T) {
	t.Parallel()
	res := renderPreview("image", "big.png", "image/png", false, pngHeader(9000, 9000), false, maxImagePreview)
	if !res.huge || res.img != nil || res.bad {
		t.Fatalf("an 81-megapixel picture: %+v", res)
	}
	// Within the budget it goes on to the decoder (which rejects this stub).
	res = renderPreview("image", "ok.png", "image/png", false, pngHeader(4000, 4000), false, maxImagePreview)
	if res.huge || !res.bad {
		t.Fatalf("a 16-megapixel header: %+v", res)
	}
}

func TestPreviewResultIsMadeOffTheUIThread(t *testing.T) {
	t.Parallel()
	res := renderPreview("json", "a.json", "application/json", false, []byte(`{"a":[1,2,3]}`), false, previewLimit)
	if res.ed == nil || res.body.kind != "json" || res.body.text == `{"a":[1,2,3]}` {
		t.Fatalf("json preview: %+v", res.body)
	}
	res = renderPreview("csv", "a.csv", "text/csv", false, []byte("a,b\n1,2\n"), false, previewLimit)
	if res.ed != nil || len(res.body.rows) != 2 || len(res.colW) != 2 {
		t.Fatalf("csv preview: %+v", res)
	}
}
