package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// ---- local side: walking the files an upload selected ---------------------------------

// scanRoot is one path the user selected for upload and the object key it maps
// to ("a/photos" for the folder "photos" uploaded into prefix "a/").
type scanRoot struct{ Local, Key string }

// localScan walks upload roots and submits one upload task per regular file.
// Everything that is not uploaded is reported as an item of the scan task:
// symbolic links, reparse points and other irregular files are "skipped" with a
// reason; unreadable paths are "failed". The scan itself reports no item for a
// file it handed over (the file's own task reports the outcome).
type localScan struct {
	s       *Service
	r       *run
	ctx     context.Context
	group   string
	dest    Location
	policy  ConflictPolicy
	noRules bool

	mu    sync.Mutex
	retry map[string]scanRoot // item key (local path) -> what to rescan
	files int
}

func (ls *localScan) remember(local, key string) {
	ls.mu.Lock()
	ls.retry[local] = scanRoot{Local: local, Key: key}
	ls.mu.Unlock()
}

func (ls *localScan) skip(local, reason string) {
	ls.r.Item(ItemResult{Key: local, Status: ItemSkipped, Err: reason})
}

func (ls *localScan) fail(local, key string, err error) {
	ls.remember(local, key)
	ls.r.Item(ItemResult{Key: local, Status: ItemFailed, Err: err.Error()})
}

// root scans one selected path. It returns an error only when the task context
// ended; per-path problems are reported as items.
func (ls *localScan) root(rt scanRoot) error {
	fi, err := os.Lstat(rt.Local)
	if err != nil {
		ls.fail(rt.Local, rt.Key, err)
		return nil
	}
	return ls.visit(rt.Local, rt.Key, fi.Mode())
}

func (ls *localScan) visit(local, key string, mode fs.FileMode) error {
	if err := ls.ctx.Err(); err != nil {
		return err
	}
	switch {
	case mode&fs.ModeSymlink != 0:
		ls.skip(local, "symbolic link (not followed)")
	case mode&fs.ModeIrregular != 0:
		ls.skip(local, "reparse point or irregular file (not followed)")
	case mode.IsDir():
		return ls.dir(local, key)
	case mode.IsRegular():
		return ls.submit(local, uploadJob{Local: local, Key: key})
	default:
		ls.skip(local, "not a regular file (device, socket or pipe)")
	}
	return nil
}

func (ls *localScan) dir(local, key string) error {
	ents, err := os.ReadDir(local)
	if err != nil {
		ls.fail(local, key, err)
		return nil
	}
	if len(ents) == 0 {
		return ls.submit(local, uploadJob{Local: local, Key: key + "/", Folder: true})
	}
	for _, e := range ents {
		if err := ls.visit(filepath.Join(local, e.Name()), key+"/"+e.Name(), e.Type()); err != nil {
			return err
		}
	}
	return nil
}

// submit queues the upload task of one file (or empty folder). Per-path
// problems become failed items; the returned error is only ever the end of the
// task context (including while the scan waits for room in the queue).
func (ls *localScan) submit(local string, j uploadJob) error {
	if len(j.Key) > maxKeyBytes {
		ls.fail(local, j.Key, invalid("Upload", "object key is longer than %d bytes: %s", maxKeyBytes, j.Key))
		return nil
	}
	if err := ls.s.waitGroupBacklog(ls.ctx, ls.group, ls.s.backlogMax(KindUpload)); err != nil {
		return err
	}
	j.Dest, j.Policy, j.NoRules = ls.dest, ls.policy, ls.noRules
	ls.s.submit(ls.s.uploadTaskSpec(ls.group, j))
	ls.mu.Lock()
	ls.files++
	n := ls.files
	ls.mu.Unlock()
	if n%50 == 0 {
		ls.r.SetDetail(scanDetail(n))
	}
	return nil
}

func scanDetail(n int) string {
	if n == 1 {
		return "found 1 file"
	}
	return fmt.Sprintf("found %d files", n)
}

// ---- remote side: listing the objects a download selected -----------------------------

// scanList walks every object under prefix (no delimiter), page by page, and
// calls fn for each. Keys are URL-decoded when the service says it encoded them.
func scanList(ctx context.Context, c *storage.Client, bucket, prefix string, fn func(bkObj) error) error {
	return bkListObjects(c, bucket)(ctx, bkScan{Prefix: prefix}, func(page []bkObj, _ string) error {
		for _, o := range page {
			if err := fn(o); err != nil {
				return err
			}
		}
		return nil
	})
}

// headRemote HEADs one object (or version) for the size, ETag and time a
// download needs.
func headRemote(ctx context.Context, c *storage.Client, bucket, key, versionID string) (bkObj, error) {
	h, err := headObj(ctx, c, "Download", bucket, key, versionID)
	if err != nil {
		return bkObj{}, err
	}
	return bkObj{
		Key: key, VersionID: versionID, ETag: aws.ToString(h.ETag),
		Size: aws.ToInt64(h.ContentLength), Modified: aws.ToTime(h.LastModified),
	}, nil
}

// relativeKey is the key below the selected prefix: the local path of a
// download is DestDir joined with it.
func relativeKey(key, prefix string) string {
	if prefix != "" && strings.HasPrefix(key, prefix) {
		return key[len(prefix):]
	}
	return key
}
