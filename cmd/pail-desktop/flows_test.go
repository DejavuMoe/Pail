package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const bkt = "acme-assets"

func (h *harness) task(title string) (Task, bool) {
	for _, t := range h.a.tasks {
		if t.Title == title {
			return t, true
		}
	}
	return Task{}, false
}

func (h *harness) waitTask(title string, status core.Status) Task {
	h.t.Helper()
	var got Task
	h.waitFor("task "+title+" "+status.String(), func() bool {
		t, ok := h.task(title)
		got = t
		return ok && t.Status == status
	})
	return got
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- inspector ---------------------------------------------------------------------------------

func TestInspectorHeadersAndOverview(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustHave("Overview", "ETag", "Key", "URI", "URL", "application/json")
	h.mustClick("Headers")
	h.mustHave("Content-Type", "application/json", "build-id", "b-20261018.4", "Add metadata")
	h.snap("inspector-headers-light")
	h.mustClick("Tags")
	h.mustHave("Tags")
	h.mustClick("Versions")
	h.mustHave("Latest")
}

func TestTagsUnsupportedShowsMutedNote(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("tagging")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustHave("Tags are not available")
}

func TestPreviewTextAndImage(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.mustClick("Preview")
	if h.a.prev.state != previewReady || h.a.prev.kind != "json" || !strings.Contains(h.a.prev.text, "acme-storefront") {
		t.Fatalf("text preview = %+v", h.a.prev)
	}
	h.snap("preview-text-light")

	h.a.openPrefix("assets/images/")
	h.settle()
	h.mustClick("hero.png")
	h.waitFor("image preview", func() bool { return h.a.prev.state == previewReady })
	if h.a.prev.kind != "image" || h.a.prev.img == nil || h.a.prev.w != 320 || h.a.prev.h != 200 {
		t.Fatalf("image preview = %+v", h.a.prev)
	}
	h.snap("preview-image-light")

	// Anything else is a hex dump of its first bytes, not a download.
	h.a.openPrefix("assets/video/")
	h.settle()
	h.mustClick("launch-teaser.mp4")
	h.waitFor("hex preview", func() bool { return h.a.prev.state == previewReady && h.a.prev.kind == "hex" })
	h.mustHave("Showing first 4 KiB")
}

// ---- upload and download ------------------------------------------------------------------------

func TestUploadCreatesTaskAndObject(t *testing.T) {
	h := newHarness(t, "light")
	data := bytes.Repeat([]byte("Pail"), 4096)
	p := writeTemp(t, "hello.bin", data)
	h.a.transfersOpen = false
	h.a.uploadPaths([]string{p}) // what a drop onto the table calls
	h.settle()
	if !h.a.transfersOpen {
		t.Error("the transfers panel did not open when the upload started")
	}
	h.waitTask("Upload hello.bin", core.StatusSucceeded)
	h.waitFor("hello.bin listed", func() bool { return h.has("hello.bin") })
	got, ok := h.env.Srv.Object(bkt, "hello.bin")
	if !ok || !bytes.Equal(got, data) {
		t.Fatalf("object on the server: ok=%v len=%d", ok, len(got))
	}
	h.mustClick("Completed 2") // the scan task and the file
	h.mustHave("Upload hello.bin")
	h.snap("transfers-completed-light")
}

func TestUploadFolderKeepsStructure(t *testing.T) {
	h := newHarness(t, "light")
	dir := filepath.Join(t.TempDir(), "photos")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("bb"), 0o600)
	h.a.uploadPaths([]string{dir})
	h.waitFor("folder listed", func() bool { return h.has("photos/") })
	if b, ok := h.env.Srv.Object(bkt, "photos/sub/b.txt"); !ok || string(b) != "bb" {
		t.Fatalf("nested object missing: %q %v", b, ok)
	}
}

func TestUploadFailureCanBeRetried(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/boom.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied by test"}
		}
		return nil
	})
	h.a.uploadPaths([]string{writeTemp(t, "boom.txt", []byte("retry me"))})
	failed := h.waitTask("Upload boom.txt", core.StatusFailed)
	if failed.Err == "" {
		t.Error("failed task carries no error text")
	}
	h.mustClick("Failed 1")
	h.mustHave("Upload boom.txt", "Retry")
	h.snap("transfers-failed-light")
	h.env.Srv.Inject(nil)
	h.mustClick("Retry") // calls through to Service.Retry
	h.waitFor("retried upload on the server", func() bool {
		_, ok := h.env.Srv.Object(bkt, "boom.txt")
		return ok
	})
	if b, _ := h.env.Srv.Object(bkt, "boom.txt"); string(b) != "retry me" {
		t.Errorf("retried object = %q", b)
	}
}

func TestCancelButtonStopsRunningTransfer(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/slow.txt") {
			return &s3test.Fault{Delay: 4 * time.Second} // long enough to cancel mid-request
		}
		return nil
	})
	h.a.uploadPaths([]string{writeTemp(t, "slow.txt", []byte("never arrives"))})
	h.waitTask("Upload slow.txt", core.StatusRunning)
	h.mustHave("Active 1")
	h.mustClick("Cancel")
	h.waitTask("Upload slow.txt", core.StatusCanceled)
	h.mustHave("Failed 1")
	if _, ok := h.env.Srv.Object(bkt, "slow.txt"); ok {
		t.Error("a canceled upload still created the object")
	}
	h.env.Srv.Inject(nil)
	// Clear finished empties the finished tabs.
	h.mustClick("Clear finished")
	h.waitFor("tasks cleared", func() bool { return len(h.a.tasks) == 0 })
	h.tt.Frame()
	if h.a.transfersOpen { // no tasks left: the panel collapses to its header
		t.Error("the panel stayed open with no tasks")
	}
	h.mustHave("0 active · 0 completed · 0 failed")
}

func TestTaskbarProgressOfActiveTasks(t *testing.T) {
	bar := func(v float64) mygo.ProgressBar { return mygo.ProgressBar{State: mygo.ProgressNormal, Value: v} }
	for _, c := range []struct {
		name  string
		tasks []Task
		want  mygo.ProgressBar
	}{
		{"none", nil, mygo.ProgressBar{}},
		{"only finished", []Task{
			{Status: core.StatusSucceeded, BytesTotal: 10, BytesDone: 10},
			{Status: core.StatusFailed, BytesTotal: 10},
		}, mygo.ProgressBar{}},
		{"bytes of the active ones", []Task{
			{Status: core.StatusRunning, BytesTotal: 300, BytesDone: 100},
			{Status: core.StatusQueued, BytesTotal: 100},
			{Status: core.StatusSucceeded, BytesTotal: 1000, BytesDone: 1000},
		}, bar(0.25)},
		{"items when no bytes", []Task{{Status: core.StatusRunning, Items: 8, ItemsDone: 3}}, bar(0.375)},
		{"half-percent steps", []Task{{Status: core.StatusRunning, BytesTotal: 1000, BytesDone: 333}}, bar(0.335)},
		{"nothing counted yet", []Task{{Status: core.StatusQueued}}, mygo.ProgressBar{State: mygo.ProgressIndeterminate}},
	} {
		if got := taskbarProgress(c.tasks); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTaskbarProgressWithoutRendering(t *testing.T) {
	h := newHarness(t, "light")
	var shown []mygo.ProgressBar
	h.a.progressSet = func(p mygo.ProgressBar) { shown = append(shown, p) }
	for _, status := range []core.Status{core.StatusRunning, core.StatusSucceeded} {
		h.a.onEvent(core.Event{Task: Task{ID: "background", Status: status, BytesTotal: 100, BytesDone: 50}})
		h.a.drain()
	}
	if len(shown) != 2 || shown[0] != (mygo.ProgressBar{State: mygo.ProgressNormal, Value: 0.5}) || shown[1] != (mygo.ProgressBar{}) {
		t.Fatalf("progress without rendering = %+v, want half then cleared", shown)
	}
}

func TestTaskbarProgressFollowsATransfer(t *testing.T) {
	h := newHarness(t, "light")
	var shown []mygo.ProgressBar
	h.a.progressSet = func(p mygo.ProgressBar) { shown = append(shown, p) }
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/slow.txt") {
			return &s3test.Fault{Delay: 4 * time.Second}
		}
		return nil
	})
	h.a.uploadPaths([]string{writeTemp(t, "slow.txt", []byte("never arrives"))})
	task := h.waitTask("Upload slow.txt", core.StatusRunning)
	h.tt.Frame()
	if len(shown) == 0 || shown[len(shown)-1].State == mygo.ProgressNone {
		t.Fatalf("a running upload shows no taskbar progress: %+v", shown)
	}
	n := len(shown)
	h.tt.Frame()
	if len(shown) != n {
		t.Errorf("an unchanged progress was set again: %+v", shown[n:])
	}
	h.a.cancelTask(task.ID)
	h.waitTask("Upload slow.txt", core.StatusCanceled)
	h.tt.Frame()
	h.env.Srv.Inject(nil)
	if got := shown[len(shown)-1]; got != (mygo.ProgressBar{}) {
		t.Errorf("progress after the upload was canceled = %+v, want none", got)
	}
}

func TestDownloadWritesFileAndOffersShowInFolder(t *testing.T) {
	h := newHarness(t, "light")
	dest := t.TempDir()
	st := h.env.Svc.Settings()
	st.DefaultDownloadDir = dest
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	revealed := make(chan string, 1)
	h.a.reveal = func(p string) { revealed <- p }
	h.mustClick("manifest.json")
	h.a.run("download", "test") // no folder dialog: the default folder is used
	want, _ := h.env.Srv.Object(bkt, "manifest.json")
	path := filepath.Join(dest, "manifest.json")
	h.waitFor("downloaded file", func() bool {
		b, err := os.ReadFile(path)
		return err == nil && bytes.Equal(b, want)
	})
	h.waitFor("toast action", func() bool { return h.tt.HasText("Show in folder") })
	if err := h.tt.Click("Show in folder"); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-revealed:
		if p != path {
			t.Errorf("revealed %q, want %q", p, path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Show in folder did not reveal anything")
	}
}

func TestDownloadAsksForFolderWithoutDefault(t *testing.T) {
	h := newHarness(t, "light")
	dest := t.TempDir()
	asked := make(chan bool, 1)
	h.a.pick = func(dir bool) []string { asked <- dir; return []string{dest} }
	h.mustClick("README.txt")
	h.a.run("download", "test")
	if dir := <-asked; !dir {
		t.Error("download asked for files instead of a folder")
	}
	h.waitFor("downloaded file", func() bool {
		_, err := os.Stat(filepath.Join(dest, "README.txt"))
		return err == nil
	})
}

// ---- delete -------------------------------------------------------------------------------------------

func TestDeleteOpensConfirmAndCancelCloses(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.tt.Key(0, ui.KeyDelete)
	h.settle()
	if !h.a.del.open {
		t.Fatal("Delete did not open the confirmation")
	}
	size := h.a.rows[h.a.byKey["manifest.json"]].Size
	h.mustHave("Delete 1 item?", demoAccountName, bkt, "(bucket root)", "manifest.json", "1 object · "+humanBytes(size), "Cancel")
	h.mustClick("Cancel")
	if h.a.del.open || h.tt.HasText("Delete 1 item?") {
		t.Fatal("Cancel left the dialog open")
	}
	if _, ok := h.env.Srv.Object(bkt, "manifest.json"); !ok {
		t.Fatal("cancel must not delete anything")
	}
}

func TestDeleteConfirmedRemovesObject(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.tt.Key(0, ui.KeyDelete)
	h.settle()
	h.mustClick("Delete 1 item")
	h.waitFor("object gone from the listing", func() bool { return !h.has("manifest.json") })
	if _, ok := h.env.Srv.Object(bkt, "manifest.json"); ok {
		t.Fatal("object still on the server after a confirmed delete")
	}
	var found bool
	for _, tk := range h.a.tasks {
		found = found || strings.HasPrefix(tk.Title, "Delete")
	}
	if !found {
		t.Fatalf("delete created no task: %+v", h.a.tasks)
	}
}

func TestDeleteFolderCountsFirst(t *testing.T) {
	h := newHarness(t, "light")
	// Hold the count back so the "Counting…" state is observable.
	release := make(chan struct{})
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "list-type=2") && strings.Contains(r.URL.RawQuery, "reports") {
			<-release
		}
		return nil
	})
	h.mustClick("reports")
	h.tt.Key(0, ui.KeyDelete)
	for i := 0; i < 50; i++ {
		h.tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	h.mustHave("Delete 1 item?", "Counting…")
	if h.a.del.state != delCounting {
		t.Fatalf("state = %d while counting", h.a.del.state)
	}
	// The danger button waits for the count: clicking it must not delete.
	h.tt.Click("Delete 1 item")
	if !h.a.del.open {
		t.Fatal("the dialog closed before the count finished")
	}
	h.env.Srv.Inject(nil)
	close(release)
	h.settle()
	h.mustHave("8 objects")
	h.snap("delete-estimate-light")
	h.mustClick("Delete 1 item")
	h.waitFor("folder gone", func() bool { return !h.has("reports/") })
	if _, ok := h.env.Srv.Object(bkt, "reports/customers-export.csv"); ok {
		t.Error("objects below the folder survived")
	}
	if _, ok := h.env.Srv.Object(bkt, "reports/exports/2026-10-17.csv"); ok {
		t.Error("nested objects below the folder survived")
	}
}

func TestDeleteWithFailedCountStaysPossible(t *testing.T) {
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "list-type=2") && strings.Contains(r.URL.RawQuery, "reports") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no list"}
		}
		return nil
	})
	h.mustClick("reports")
	h.tt.Key(0, ui.KeyDelete)
	h.settle()
	h.mustHave("Count unavailable")
	if h.a.del.state != delFailed {
		t.Fatalf("state = %d", h.a.del.state)
	}
	h.env.Srv.Inject(nil)
	h.mustClick("Delete 1 item")
	h.waitFor("folder gone", func() bool { return !h.has("reports/") })
}

// ---- new folder -----------------------------------------------------------------------------------------

func TestNewFolderValidationAndCreate(t *testing.T) {
	h := newHarness(t, "light")
	if h.a.folderError("assets") == "" || h.a.folderError("a/b") == "" || h.a.folderError("") == "" {
		t.Error("invalid folder names accepted")
	}
	h.a.run("newfolder", "test")
	h.settle()
	h.mustHave("New folder", "Create")
	h.tt.Type("a/b")
	h.settle()
	h.mustHave("Names cannot contain /")
	h.tt.Key(ui.Cmd, ui.KeyA) // replace the text
	h.tt.Type("reports2")
	h.settle()
	h.mustNotHave("Names cannot contain /")
	h.mustClick("Create")
	h.waitFor("folder created and selected", func() bool { return h.has("reports2/") && h.a.sel.Has("reports2/") })
	if _, ok := h.env.Srv.Object(bkt, "reports2/"); !ok {
		t.Error("the folder placeholder is not on the server")
	}
	if h.a.newFolder {
		t.Error("dialog still open")
	}
}

// ---- copying ----------------------------------------------------------------------------------------------

func TestCopyKeyURIURLAndSigned(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	for _, c := range []struct{ id, want string }{
		{"copykey", "manifest.json"},
		{"copyuri", "s3://acme-assets/manifest.json"},
		{"copyurl", h.env.Srv.URL + "/acme-assets/manifest.json"},
	} {
		h.a.run(c.id, "test")
		h.tt.Frame()
		if got := h.tt.Clipboard(); got != c.want {
			t.Errorf("%s copied %q, want %q", c.id, got, c.want)
		}
		time.Sleep(150 * time.Millisecond) // past the double-delivery guard
	}
	h.a.run("copysigned", "test")
	h.settle()
	h.mustClick("Generate") // GET, one hour: the dialog's defaults
	h.waitFor("signed url shown", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.clickBeside("Close", 20) // the primary "Copy" button right of Close
	h.waitFor("signed url", func() bool { return strings.Contains(h.tt.Clipboard(), "X-Amz-Signature") })
	u := h.tt.Clipboard()
	if !strings.Contains(u, "X-Amz-Expires=3600") || !strings.Contains(u, "/acme-assets/manifest.json") {
		t.Errorf("signed URL = %s", u)
	}
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("signed URL status %d", res.StatusCode)
	}
}
