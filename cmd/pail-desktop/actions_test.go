package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestSkippedTransfersAreNotAnnouncedAsCompleted(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		t.Run(direction, func(t *testing.T) {
			h := newHarness(t, "light")
			st := h.a.svc.Settings()
			st.Conflict = core.ConflictSkip
			if err := h.a.svc.SaveSettings(st); err != nil {
				t.Fatal(err)
			}
			h.env.Srv.PutObject(bkt, "existing.txt", []byte("remote"), "", nil)
			local := writeTemp(t, "existing.txt", []byte("local"))
			if direction == "upload" {
				h.a.uploadPaths([]string{local})
			} else {
				h.a.downloadKeys(h.a.scope, []string{"existing.txt"}, filepath.Dir(local))
			}
			h.settle()
			h.waitFor("transfer finished", func() bool {
				return len(h.a.tasks) > 0 && len(h.a.watches) == 0 && h.a.idle()
			})
			skipped := 0
			for _, tk := range h.a.tasks {
				for _, it := range h.a.svc.TaskItems(tk.ID) {
					if it.Status == core.ItemSkipped {
						skipped++
					}
				}
			}
			body, _ := h.env.Srv.Object(bkt, "existing.txt")
			localBody, err := os.ReadFile(local)
			if skipped != 1 || string(body) != "remote" || err != nil || string(localBody) != "local" {
				t.Fatalf("skip changed data: skipped=%d remote=%q local=%q err=%v", skipped, body, localBody, err)
			}
			if direction == "upload" {
				h.mustHave("Nothing was uploaded")
				h.mustNotHave("Uploaded 1 file")
			} else {
				h.mustHave("Nothing was downloaded")
				h.mustNotHave("Downloaded 1 file")
			}
			if direction == "upload" {
				fresh := writeTemp(t, "new.txt", []byte("new local"))
				h.a.uploadPaths([]string{local, fresh})
			} else {
				h.env.Srv.PutObject(bkt, "new.txt", []byte("new remote"), "", nil)
				h.a.downloadKeys(h.a.scope, []string{"existing.txt", "new.txt"}, filepath.Dir(local))
			}
			h.settle()
			h.waitFor("mixed transfer finished", func() bool { return len(h.a.watches) == 0 })
			if direction == "upload" {
				h.mustHave("Uploaded 1 file")
				h.mustNotHave("Uploaded 2 files")
			} else {
				h.mustHave("Downloaded 1 file")
				h.mustNotHave("Downloaded 2 files")
			}
		})
	}
}

func TestUploadOutcomeSurvivesHistoryEviction(t *testing.T) {
	h := newHarness(t, "light")
	st := h.a.svc.Settings()
	st.Limits.Upload = 1
	if err := h.a.svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/bulk-0000.txt") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "first file fails"}
		}
		return nil
	})
	dir := t.TempDir()
	paths := make([]string, 1002)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("bulk-%04d.txt", i))
		if err := os.WriteFile(paths[i], []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.a.uploadPaths(paths)
	h.settle()
	h.waitFor("all uploads finished", func() bool {
		if len(h.a.tasks) == 0 || len(h.a.watches) > 0 {
			return false
		}
		for _, tk := range h.a.svc.Tasks() {
			if !tk.Status.Finished() {
				return false
			}
		}
		return true
	})
	h.settle()
	for i := range paths {
		_, exists := h.env.Srv.Object(bkt, fmt.Sprintf("bulk-%04d.txt", i))
		if exists != (i != 0) {
			t.Fatalf("object %d exists=%v", i, exists)
		}
	}
	if len(h.a.tasks) != 1000 || len(h.a.svc.Tasks()) != 1000 {
		t.Fatalf("history cap changed: UI=%d service=%d", len(h.a.tasks), len(h.a.svc.Tasks()))
	}
	h.mustHave("Upload finished with errors: 1001 uploaded, 1 failed")
}
