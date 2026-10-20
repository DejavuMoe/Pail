package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DejavuMoe/Pail/internal/safepath"
	"github.com/DejavuMoe/Pail/internal/storage"
	"github.com/DejavuMoe/Pail/internal/trash"
)

// syncTrash moves a local file to the OS trash. It is a variable only so that
// tests can avoid the real trash. When it fails the item fails; sync never
// deletes a local file permanently.
var syncTrash = trash.MoveToTrash

// RunSync executes exactly the actions of a preview as ordinary queue tasks of
// one group and returns the group ID. Every action is validated first; if one
// is malformed nothing is started. The tasks are all registered when RunSync
// returns, so TaskItems of the group's tasks show one result per action.
//
// Each action re-checks what the preview observed:
//
//   - upload of a new object is create-only (HEAD check plus If-None-Match);
//     upload of a changed object overwrites only the ETag the preview saw
//     (HEAD check plus If-Match). A mismatch fails that task and overwrites
//     nothing. A local file whose size or time changed is skipped
//     ("local file changed since preview"). The upload rules must still yield
//     the previewed key.
//   - download is GET with If-Match of the previewed ETag and is published only
//     if the local file still has the size and time the preview saw (or still
//     does not exist); otherwise it is skipped.
//   - delete (mirror) removes remote objects after checking that the ETag is
//     still the previewed one (plain-key delete, see sync.go), and moves local
//     files to the OS trash if they still have the previewed size and time.
//     If the trash is unavailable the item fails and the file stays.
//
// Skipped actions of the preview ("unchanged", "excluded" ...) start nothing.
func (s *Service) RunSync(p SyncPreview) (string, error) {
	const op = "RunSync"
	job, err := syNormalize(op, p.Job)
	if err != nil {
		return "", err
	}
	if err := s.requireKnownAccount(op, job.Remote.AccountID); err != nil {
		return "", err
	}
	up := job.Direction == syUp
	prefix := job.Remote.Prefix
	var ups, downs, dels []SyncAction
	for _, a := range p.Actions {
		if a.Op == "skip" {
			continue
		}
		if _, err := safepath.Join(job.LocalDir, a.Path); err != nil {
			return "", invalid(op, "action %q: %v", a.Path, err)
		}
		switch a.Op {
		case "upload":
			if !up {
				return "", invalid(op, "upload action %q in a download job", a.Path)
			}
			if a.Key != prefix+a.Path && a.Key != prefix+a.Path+".zip" {
				return "", invalid(op, "upload action %q has the unrelated key %q", a.Path, a.Key)
			}
			if len(a.Key) > maxKeyBytes || a.LocalSize < 0 {
				return "", invalid(op, "upload action %q is malformed", a.Path)
			}
			ups = append(ups, a)
		case "download":
			if up {
				return "", invalid(op, "download action %q in an upload job", a.Path)
			}
			if a.Key != prefix+a.Path {
				return "", invalid(op, "download action %q has the unrelated key %q", a.Path, a.Key)
			}
			downs = append(downs, a)
		case "delete":
			if up && (!strings.HasPrefix(a.Key, prefix) || len(a.Key) == len(prefix) || strings.HasSuffix(a.Key, "/")) {
				return "", invalid(op, "delete action %q has the key %q outside the sync prefix", a.Path, a.Key)
			}
			if !up && !a.LocalExists {
				return "", invalid(op, "delete action %q has no observed local state", a.Path)
			}
			dels = append(dels, a)
		default:
			return "", invalid(op, "unknown action %q for %q", a.Op, a.Path)
		}
	}
	if len(ups)+len(downs)+len(dels) == 0 {
		return "", invalid(op, "nothing to synchronise")
	}

	group := s.newGroupID()
	for _, a := range ups {
		local, _ := safepath.Join(job.LocalDir, a.Path) // validated above
		meta := map[string]string{syMetaSize: strconv.FormatInt(a.LocalSize, 10)}
		if a.SHA256 != "" {
			meta[syMetaSHA] = a.SHA256
		}
		s.submit(s.uploadTaskSpec(group, uploadJob{
			Dest: job.Remote, Local: local, Key: prefix + a.Path, Policy: ConflictOverwrite, NoRules: job.NoRules,
			Sync: &syncUploadPre{Key: a.Key, ETag: a.ETag, Size: a.LocalSize, MTime: a.LocalMTime, Meta: meta},
		}))
	}
	for _, a := range downs {
		var mod time.Time
		if a.RemoteTime != 0 {
			mod = time.Unix(0, a.RemoteTime)
		}
		s.submit(s.downloadTaskSpec(group, downloadJob{
			Src: job.Remote, Key: a.Key, ETag: a.ETag, Size: a.Size, Modified: mod,
			DestDir: job.LocalDir, Rel: a.Path, Policy: ConflictOverwrite,
			Sync: &syncDownloadPre{Exists: a.LocalExists, Size: a.LocalSize, MTime: a.LocalMTime},
		}))
	}
	if len(dels) > 0 {
		if up {
			s.submit(s.syncDeleteRemoteSpec(group, job.Remote, dels))
		} else {
			s.submit(s.syncDeleteLocalSpec(group, job.LocalDir, dels))
		}
	}
	s.lgInfo("sync run started", "job", job.ID, "group", group, "direction", job.Direction, "uploads", len(ups), "downloads", len(downs), "deletes", len(dels))
	return group, nil
}

// syncDeleteRemoteSpec is the task that deletes the extraneous objects of a
// mirror upload.
func (s *Service) syncDeleteRemoteSpec(group string, loc Location, acts []SyncAction) taskSpec {
	return taskSpec{
		Kind: KindOther, Group: group,
		Title:  fmt.Sprintf("Sync: delete %d remote object(s)", len(acts)),
		Detail: "s3://" + loc.Bucket + "/" + loc.Prefix,
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, "Sync", loc)
			if err != nil {
				return err
			}
			r.SetTotal(0, len(acts))
			reported := make([]bool, len(acts))
			verified := make([]bool, len(acts))
			var mu sync.Mutex
			report := func(i int, it ItemResult) {
				mu.Lock()
				reported[i] = true
				mu.Unlock()
				r.Item(it)
			}
			// 1. The object must still be the one the preview listed.
			syParallel(ctx, len(acts), s.bkWorkers(), func(i int) {
				a := acts[i]
				t, err := headTarget(ctx, c, loc.Bucket, a.Key)
				switch {
				case ctx.Err() != nil:
				case err != nil:
					report(i, ItemResult{Key: a.Key, Status: ItemFailed, Err: err.Error()})
				case !t.exists:
					report(i, ItemResult{Key: a.Key, Status: ItemSkipped, Err: "already deleted"})
				case !syEtagEqual(t.etag, a.ETag):
					report(i, ItemResult{Key: a.Key, Status: ItemFailed, Err: "changed since preview; not deleted"})
				default:
					mu.Lock()
					verified[i] = true
					mu.Unlock()
				}
			})
			// 2. Delete the verified ones in batches.
			var idx []int
			var ids []storage.ObjectID
			for i, ok := range verified {
				if ok && ctx.Err() == nil {
					idx = append(idx, i)
					ids = append(ids, storage.ObjectID{Key: acts[i].Key})
				}
			}
			if len(ids) > 0 {
				for k, d := range c.DeleteObjects(ctx, loc.Bucket, ids) {
					it := ItemResult{Key: d.Key, Status: string(d.Status)}
					if d.Status != storage.DeleteSucceeded {
						it.Err = strings.TrimSpace(d.Code + ": " + d.Message)
					} else {
						r.AddBytes(acts[idx[k]].Size)
					}
					report(idx[k], it)
				}
			}
			if err := ctx.Err(); err != nil {
				for i, a := range acts {
					if !reported[i] {
						r.Item(ItemResult{Key: a.Key, Status: ItemUnprocessed, Err: "not attempted: canceled"})
					}
				}
				return err
			}
			return nil
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			want := map[string]bool{}
			for _, it := range failed {
				want[it.Key] = true
			}
			var next []SyncAction
			for _, a := range acts {
				if want[a.Key] {
					next = append(next, a)
				}
			}
			if len(next) == 0 {
				return taskSpec{}, false
			}
			return s.syncDeleteRemoteSpec(group, loc, next), true
		},
	}
}

// syncDeleteLocalSpec is the task that moves the extraneous local files of a
// mirror download to the trash.
func (s *Service) syncDeleteLocalSpec(group, dir string, acts []SyncAction) taskSpec {
	return taskSpec{
		Kind: KindOther, Group: group,
		Title:  fmt.Sprintf("Sync: move %d local file(s) to the trash", len(acts)),
		Detail: dir,
		Work: func(ctx context.Context, r *run) error {
			r.SetTotal(0, len(acts))
			for i, a := range acts {
				if err := ctx.Err(); err != nil {
					for _, rest := range acts[i:] {
						local, _ := safepath.Join(dir, rest.Path)
						r.Item(ItemResult{Key: local, Status: ItemUnprocessed, Err: "not attempted: canceled"})
					}
					return err
				}
				local, err := safepath.Join(dir, a.Path)
				if err != nil {
					r.Item(ItemResult{Key: a.Path, Status: ItemFailed, Err: err.Error()})
					continue
				}
				pre := syncDownloadPre{Exists: true, Size: a.LocalSize, MTime: a.LocalMTime}
				changed, err := pre.localChanged(local)
				switch {
				case err != nil:
					r.Item(ItemResult{Key: local, Status: ItemFailed, Err: err.Error()})
				case changed:
					r.Item(ItemResult{Key: local, Status: ItemSkipped, Err: "local file changed since preview"})
				default:
					if err := syncTrash(local); err != nil {
						msg := err.Error()
						if errors.Is(err, trash.ErrUnavailable) {
							msg = "the trash is not available; the file was kept: " + msg
						}
						r.Item(ItemResult{Key: local, Status: ItemFailed, Err: msg})
						continue
					}
					if _, err := os.Lstat(local); err == nil {
						r.Item(ItemResult{Key: local, Status: ItemFailed, Err: "the file is still there after the move to the trash"})
						continue
					}
					r.AddBytes(a.Size)
					r.Item(ItemResult{Key: local, Status: ItemSucceeded})
				}
			}
			return nil
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			want := map[string]bool{}
			for _, it := range failed {
				want[it.Key] = true
			}
			var next []SyncAction
			for _, a := range acts {
				if local, err := safepath.Join(dir, a.Path); err == nil && (want[local] || want[a.Path]) {
					next = append(next, a)
				}
			}
			if len(next) == 0 {
				return taskSpec{}, false
			}
			return s.syncDeleteLocalSpec(group, dir, next), true
		},
	}
}
