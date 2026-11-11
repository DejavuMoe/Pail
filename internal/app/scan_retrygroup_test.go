package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestTransferScanRetryUsesFreshGroup(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		for _, failure := range []string{"items", "before items", "queued cancellation"} {
			t.Run(direction+"/"+failure, func(t *testing.T) {
				e := newTrEnv(t)
				setLimits(t, e.s, 1, 1, 1)
				group := e.s.newGroupID()
				local := filepath.Join(t.TempDir(), "folder")
				dest := t.TempDir()
				var spec taskSpec
				if direction == "upload" {
					spec = e.s.uploadScanSpec(group, e.loc("b", ""), []scanRoot{{Local: local, Key: "folder"}}, ConflictRename, false, "Scan")
				} else {
					e.srv.PutObject("b", "folder/f.txt", []byte("data"), "", nil)
					e.srv.Inject(func(r *http.Request) *s3test.Fault {
						if r.URL.Query().Get("list-type") == "2" {
							return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "injected"}
						}
						return nil
					})
					spec = e.s.downloadScanSpec(group, DownloadRequest{Source: e.loc("b", ""), Keys: []string{"folder/"}, DestDir: dest, Conflict: ConflictRename}, "Scan")
				}
				var blocker string
				if failure == "before items" {
					spec.Work = func(context.Context, *run) error { return errors.New("failed before the scanner was initialized") }
				} else if failure == "queued cancellation" {
					started := make(chan struct{})
					blocker = e.s.submit(taskSpec{Kind: KindOther, Work: func(ctx context.Context, _ *run) error {
						close(started)
						<-ctx.Done()
						return ctx.Err()
					}})
					<-started
				}
				id := e.s.submit(spec)
				if blocker != "" {
					if err := e.s.CancelGroup(group); err != nil {
						t.Fatal(err)
					}
					if err := e.s.Cancel(blocker); err != nil {
						t.Fatal(err)
					}
					wait(t, e.s, blocker)
				}
				initial := wait(t, e.s, id)
				if !initial.Status.Finished() || initial.Status == StatusSucceeded {
					t.Fatalf("fixture did not fail the scan: %+v", initial)
				}
				if got := len(e.s.TaskItems(id)); (got > 0) != (failure == "items") {
					t.Fatalf("fixture item count = %d for %s", got, failure)
				}
				if err := e.s.CancelGroup(group); err != nil {
					t.Fatal(err)
				}
				e.srv.Inject(nil)
				trWrite(t, filepath.Join(local, "f.txt"), []byte("data"))
				rid, err := e.s.Retry(id)
				if err != nil {
					t.Fatal(err)
				}
				retried := wait(t, e.s, rid)
				if retried.GroupID == "" || retried.GroupID == group {
					t.Fatalf("scan retry reused old group: old=%q new=%q", group, retried.GroupID)
				}
				tasks := trWaitGroup(t, e.s, retried.GroupID)
				trExpect(t, tasks, StatusSucceeded)
				if len(tasks) != 2 {
					t.Fatalf("fresh group must contain the scan and its one transfer: %+v", tasks)
				}
				for _, task := range e.s.Tasks() {
					if task.GroupID == group && task.ID != id {
						t.Fatalf("retry submitted descendant into old group: %+v", task)
					}
				}
				if direction == "upload" {
					if trObject(t, e, "folder/f.txt") != "data" {
						t.Fatal("retried upload body differs")
					}
				} else if trRead(t, filepath.Join(dest, "folder", "f.txt")) != "data" {
					t.Fatal("retried download body differs")
				}
				late := e.s.submit(taskSpec{Kind: KindOther, Group: group, Work: func(context.Context, *run) error {
					t.Error("retry revived the canceled original group")
					return nil
				}})
				if got := wait(t, e.s, late); got.Status != StatusCanceled {
					t.Fatalf("old group lost its cancellation: %+v", got)
				}
			})
		}
	}
}
