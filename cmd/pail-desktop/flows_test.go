package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	t.Parallel()
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
	t.Parallel()
	h := newHarness(t, "light")
	h.env.Srv.SetUnsupported("tagging")
	h.mustClick("manifest.json")
	h.mustClick("Tags")
	h.mustHave("Tags are not available")
}

func TestPreviewTextAndImage(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	h.mustClick("Completed 1") // the file; its succeeded scan is not listed or counted
	h.mustHave("Upload hello.bin")
	h.snap("transfers-completed-light")
}

func TestUploadFolderKeepsStructure(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	if h.a.transfersOpen { // no tasks left: the panel closes, and the strip has nothing to say
		t.Error("the panel stayed open with no tasks")
	}
	h.mustNotHave("Transfers", "0 active", "0 completed", "0 failed")
}

func TestTaskbarProgressOfActiveTasks(t *testing.T) {
	t.Parallel()
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
		if got := taskbarProgress(c.tasks, nil); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTaskbarProgressWithoutRendering(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// A create folder answer belongs to the dialog that asked for it. When the
// user cancels, reopens and types again, the old answer may not close the new
// dialog or write its error into it.
func TestNewFolderStaleAnswerKeepsReopenedDialog(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "light")
			gate, arrived := make(chan struct{}), make(chan struct{}, 1)
			h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
				if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/stale/") {
					return nil
				}
				select {
				case arrived <- struct{}{}:
				default:
				}
				<-gate
				if fail {
					return &s3test.Fault{Status: http.StatusForbidden, Code: "AccessDenied", Message: "no"}
				}
				return nil
			})
			h.a.run("newfolder", "test")
			h.settle()
			h.tt.Type("stale")
			h.settle()
			h.a.confirmNewFolder()
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("the create request never reached the server")
			}
			h.tt.Key(0, ui.KeyEscape)
			if h.a.newFolder {
				t.Fatal("Escape left the dialog open")
			}
			h.a.run("newfolder", "test")
			// The create is still held, so the UI is not idle: draw frames instead
			// of waiting for it to settle.
			h.tt.Frame()
			h.playOut()
			h.tt.Type("draft")
			h.tt.Frame()
			close(gate)
			if fail {
				h.waitFor("the failed create is reported", func() bool {
					return h.tt.HasText("Could not create folder stale")
				})
			} else {
				h.waitFor("the created folder is listed", func() bool { return h.has("stale/") })
			}
			if !h.a.newFolder {
				t.Error("the stale answer closed the reopened dialog")
			}
			if h.a.folderDlg.name != "draft" || h.a.folderDlg.busy || h.a.folderDlg.err != "" {
				t.Errorf("reopened dialog = %+v", h.a.folderDlg)
			}
			if fail && h.has("stale/") {
				t.Error("the failed folder is in the listing")
			}
			if !fail && h.a.selectAfter != "" {
				t.Errorf("stale success selected %q in the reopened dialog", h.a.selectAfter)
			}
		})
	}
}

// ---- copying ----------------------------------------------------------------------------------------------

func TestCopyKeyURIURLAndSigned(t *testing.T) {
	t.Parallel()
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

// ---- retry -------------------------------------------------------------------------------------

func TestRetryScanFollowsNewTransfers(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	p := filepath.Join(t.TempDir(), "retry-scan.txt")
	h.a.uploadPaths([]string{p})
	var failed Task
	h.waitFor("upload scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	if err := os.WriteFile(p, []byte("rescanned"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.retryTask(failed.ID)
	h.waitTask("Upload retry-scan.txt", core.StatusSucceeded)
	h.settle()
	if !h.has("retry-scan.txt") {
		t.Error("retried scan did not refresh uploaded children")
	}
	h.mustHave("Uploaded 1 file")
	h.mustNotHave("Uploaded 0 files")
}

func TestRetryScanOutcomeSurvivesHistoryEviction(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	st := h.a.svc.Settings()
	st.Limits.Upload = 1
	if err := h.a.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "retry-history")
	h.a.uploadPaths([]string{dir})
	var failed Task
	h.waitFor("upload scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1002; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/item-0000.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "first retry item denied"}
		}
		return nil
	})
	h.a.retryTask(failed.ID)
	h.waitFor("retried scan and uploads finished", func() bool {
		if len(h.a.watches) != 0 {
			return false
		}
		for _, task := range h.a.svc.Tasks() {
			if !task.Status.Finished() {
				return false
			}
		}
		_, exists := h.env.Srv.Object(bkt, "retry-history/item-1001.txt")
		return exists
	})
	h.settle()
	if len(h.a.svc.Tasks()) != 1000 {
		t.Fatalf("history bound changed: %d", len(h.a.svc.Tasks()))
	}
	h.mustHave("Upload finished with errors: 1001 uploaded, 1 failed")
	if !slices.ContainsFunc(h.a.ops, func(e opEntry) bool { return e.what == tookUpload && e.state == opFailed && e.n == 1001 && e.bad == 1 }) {
		t.Error("retry lost evicted outcomes from recent operations")
	}
}

func TestRetryScanWatchSurvivesRemovalBeforeRegistration(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	st := h.a.svc.Settings()
	st.Limits.Upload = 2
	if err := h.a.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "late-watch")
	h.a.uploadPaths([]string{dir})
	var failed Task
	h.waitFor("scan failed", func() bool {
		failed, _ = h.task("Scan 1 item for upload")
		return failed.Status.Finished() && failed.Status != core.StatusSucceeded
	})
	h.settle()
	w := h.a.retryWatches[failed.GroupID]
	if w == nil {
		t.Fatal("missing original watch context")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut {
			if strings.HasSuffix(r.URL.Path, "/item-0000.txt") {
				return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "first retry item denied"}
			}
			if strings.HasSuffix(r.URL.Path, "/item-0001.txt") {
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}
		}
		return nil
	})
	scanned, removed := make(chan string, 1), make(chan string, 1)
	finished := make(chan struct{}, 3)
	unsub := h.a.svc.Subscribe(func(e core.Event) {
		if e.Task.Title == "Rescan 1 item for upload" {
			if e.Removed {
				removed <- e.Task.ID
			} else if e.Task.Status.Finished() {
				scanned <- e.Task.ID
			}
		}
		if !e.Removed && e.Task.Kind == core.KindUpload && e.Task.Status.Finished() {
			finished <- struct{}{}
		}
	})
	defer unsub()
	next, err := h.a.svc.Retry(failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-scanned:
	case <-time.After(10 * time.Second):
		t.Fatal("retry scan did not finish")
	}
	// Keep one descendant alive, so the group's complete counters survive removal.
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatal("retry siblings did not finish")
		}
	}
	h.a.svc.ClearFinished()
	select {
	case id := <-removed:
		if id != next {
			t.Fatalf("removed scan=%q want=%q", id, next)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retry scan was not removed")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("last retry child did not finish")
	}
	if slices.ContainsFunc(h.a.svc.Tasks(), func(t Task) bool { return t.ID == next }) {
		t.Fatal("test did not reach the lookup/retention window")
	}
	// No UI frames have drained inbox since Retry: its receipt is still there.
	h.a.addWatch(next, w)
	h.settle()
	if len(h.a.watches) != 0 {
		t.Errorf("removed scan left an unresolved watch: %v", h.a.watches)
	}
	h.mustHave("Upload finished with errors: 2 uploaded, 1 failed")
	if !h.has("late-watch/") {
		t.Error("late retry watch did not refresh listing")
	}
}

func TestRetryRefreshesListingAndRecentOperation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/retry-refresh.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "denied"}
		}
		return nil
	})
	h.a.uploadPaths([]string{writeTemp(t, "retry-refresh.txt", []byte("retry"))})
	failed := h.waitTask("Upload retry-refresh.txt", core.StatusFailed)
	h.settle()
	h.env.Srv.Inject(nil)
	h.a.retryTask(failed.ID)
	h.waitFor("retry completed", func() bool {
		for _, task := range h.a.tasks {
			if task.ID != failed.ID && task.Title == failed.Title && task.Status == core.StatusSucceeded {
				return true
			}
		}
		return false
	})
	h.settle()
	if body, ok := h.env.Srv.Object(bkt, "retry-refresh.txt"); !ok || string(body) != "retry" {
		t.Fatal("retry did not upload data")
	}
	if !h.has("retry-refresh.txt") {
		t.Error("successful retry did not refresh the listing")
	}
	h.mustHave("Uploaded 1 file")
	if !slices.ContainsFunc(h.a.ops, func(e opEntry) bool {
		return e.what == tookUpload && e.state == opDone && e.n == 1 && e.account == h.env.Account.ID
	}) {
		t.Error("successful retry missing from recent operations")
	}
	h.a.clearFinished()
	h.waitFor("finished task removal events", func() bool { return len(h.a.tasks) == 0 })
	if len(h.a.retryWatches) != 0 {
		t.Fatal("cleared task history retained retry contexts")
	}
}

func TestRetryDownloadKeepsOriginalDestination(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	dir := t.TempDir()
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manifest.json") && !r.URL.Query().Has("tagging") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "download denied"}
		}
		return nil
	})
	h.a.downloadKeys(h.a.scope, []string{"manifest.json"}, dir)
	failed := h.waitTask("Download manifest.json", core.StatusFailed)
	h.settle()
	h.a.openBucket("acme-backups")
	h.settle()
	h.env.Srv.Inject(nil)
	h.a.retryTask(failed.ID)
	h.waitFor("retry download finished", func() bool {
		for _, task := range h.a.tasks {
			if task.ID != failed.ID && task.Title == failed.Title && task.Status == core.StatusSucceeded {
				return true
			}
		}
		return false
	})
	h.settle()
	if body, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err != nil || !strings.Contains(string(body), "acme-storefront") {
		t.Fatalf("retried download body=%q err=%v", body, err)
	}
	if h.a.scope.Bucket != "acme-backups" {
		t.Fatal("retry changed the current bucket")
	}
	h.mustHave("Downloaded 1 file", "Show in folder")
	revealed := make(chan string, 1)
	h.a.reveal = func(p string) { revealed <- p }
	h.mustClick("Show in folder")
	select {
	case path := <-revealed:
		if path != filepath.Join(dir, "manifest.json") {
			t.Fatalf("retry reveal path=%q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry completion lost original reveal action")
	}
}
