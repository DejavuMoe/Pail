package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// ---- scan state: kept across pauses and sessions ----------------------------------------

// scanState is the progress of one scan, shared by all its runs: pausing a
// scan ends its Work, resuming runs Work again and it continues at the
// position, and SaveTransfers keeps the position so that a later session
// continues there as well.
//
// The position is the last entry handled, in the scan's
// deterministic order: roots (units) in the order given; inside a local
// folder a depth-first walk with the entries of each folder sorted by name
// (os.ReadDir); inside a remote prefix the listing order (keys in UTF-8 binary
// order, resumed with StartAfter). An entry is handled when it was handed over
// as a task (the position moves in the critical section that registers the
// task, submitWith, so a saved file is either a saved task or still ahead of
// the position) or reported as an item of the scan: skipped, failed, or a
// download's folder placeholder (the position moves together with recording
// it). So every entry is reported once per scan, across pauses and sessions. Entries created behind the position in the
// meantime are not picked up, removed ones are simply not met again, and the
// walk never revisits a position, so it cannot skip what it did not handle
// or loop.
//
// Lock order: q.mu before mu. mu is never held while calling the queue.
type scanState struct {
	mu      sync.Mutex
	next    int      // first root or unit not finished
	path    []string // upload: inside roots[next], the path of the last entry handled
	after   string   // download: inside units[next], the last key handled
	gen     int      // bumped whenever the position moves
	started bool     // a run began, or the state was restored
	files   int      // files handed over

	roots  map[string]scanRoot // upload: failed or unprocessed item key -> what to rescan
	units  map[string]scanUnit // download: the same by scanUnit.id()
	failed []scanFailure
	prior  []scanFailure // restored failures, reported by the next run
	names  *nameClaims   // download: local names handed out
}

// scanFailure is a failed item of a scan and what a retry rescans for it.
type scanFailure struct {
	item ItemResult
	root *scanRoot
	unit *scanUnit
	gen  int // position generation when it failed; -1 = from an earlier session
}

func newScanState() *scanState {
	return &scanState{roots: map[string]scanRoot{}, units: map[string]scanUnit{}, names: newNameClaims(runtime.GOOS)}
}

// begin marks the start of a run and returns the restored failures it reports.
func (st *scanState) begin() []scanFailure {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.started = true
	prior := st.prior
	st.prior = nil
	return prior
}

// position returns the root or unit to continue with and the position in it.
func (st *scanState) position() (next int, path []string, after string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.next, append([]string(nil), st.path...), st.after
}

// finish marks root or unit i as done.
func (st *scanState) finish(i int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.next <= i {
		st.next, st.path, st.after = i+1, nil, ""
		st.gen++
	}
}

// handOver moves the position to the entry just submitted. Called inside
// submitWith.
func (st *scanState) handOver(i int, path []string, key string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.files++
	st.passLocked(i, path, key)
}

// pass moves the position past an entry the scan handled without a task; the
// caller reports its item afterwards.
func (st *scanState) pass(i int, path []string, key string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.passLocked(i, path, key)
}

// passLocked moves the position to entry (i, path or key). An upload entry
// with an empty path, or a download entry without a key, is a whole root or
// unit. Caller holds mu.
func (st *scanState) passLocked(i int, path []string, key string) {
	st.gen++
	if len(path) == 0 && key == "" {
		st.next, st.path, st.after = i+1, nil, ""
		return
	}
	st.next, st.path, st.after = i, append([]string(nil), path...), key
}

// remember records what a retry rescans for a failed or unprocessed item.
// Caller holds mu.
func (st *scanState) remember(f scanFailure) {
	if f.root != nil {
		st.roots[f.item.Key] = *f.root
	}
	if f.unit != nil {
		st.units[f.unit.id()] = *f.unit
	}
}

// fail records a failed item at entry (i, path or key) and moves the position
// past it in the same step: the failure is then behind the position, so it is
// saved and the walk never meets it again. The caller reports it with r.Item
// afterwards.
func (st *scanState) fail(f scanFailure, i int, path []string, key string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	f.gen = st.gen
	st.remember(f)
	st.failed = append(st.failed, f)
	st.passLocked(i, path, key)
}

// fileCount returns the number of files handed over.
func (st *scanState) fileCount() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.files
}

// pos returns the saved form of the position. Caller holds mu.
func (st *scanState) pos() savedScanPos {
	return savedScanPos{Next: st.next, Path: append([]string(nil), st.path...), After: st.after, Files: st.files}
}

// savedFailures returns the failures behind the position (those ahead of it
// are met again by the walk) and the restored ones. Caller holds mu.
func (st *scanState) savedFailures() []savedScanFailure {
	var out []savedScanFailure
	for _, f := range st.failed {
		if f.gen < st.gen {
			out = append(out, savedScanFailure{Key: f.item.Key, VersionID: f.item.VersionID, Err: f.item.Err, Root: f.root, Unit: f.unit})
		}
	}
	return out
}

// restore loads a saved position and failures into a new state.
func (st *scanState) restore(pos savedScanPos, failed []savedScanFailure) {
	st.next, st.path, st.after, st.files, st.started = pos.Next, pos.Path, pos.After, pos.Files, true
	for _, f := range failed {
		sf := scanFailure{item: ItemResult{Key: f.Key, VersionID: f.VersionID, Status: ItemFailed, Err: f.Err}, root: f.Root, unit: f.Unit, gen: -1}
		st.remember(sf)
		st.prior = append(st.prior, sf)
		st.failed = append(st.failed, sf)
	}
}

// unfinished returns the unprocessed items of a canceled scan: the restored
// failures not reported yet and every root or unit from the position on
// (a partly scanned folder is rescanned as a whole by Retry). Nothing when
// the scan never started. Called with q.mu held.
func (st *scanState) unfinished(n int, item func(i int) (ItemResult, scanFailure)) []ItemResult {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.started {
		return nil
	}
	var out []ItemResult
	for _, f := range st.prior {
		out = append(out, f.item)
	}
	st.prior = nil
	for i := st.next; i < n; i++ {
		it, f := item(i)
		st.remember(f)
		out = append(out, it)
	}
	return out
}

// ---- local side: walking the files an upload selected ---------------------------------

// scanRoot is one path the user selected for upload and the object key it maps
// to ("a/photos" for the folder "photos" uploaded into prefix "a/").
type scanRoot struct{ Local, Key string }

// localScan is one run of an upload scan: it walks the roots from the
// position of st and submits one upload task per regular file. Everything
// that is not uploaded is reported as an item of the scan task: symbolic
// links, reparse points and other irregular files are "skipped" with a reason;
// unreadable paths are "failed". The scan itself reports no item for a file it
// handed over (the file's own task reports the outcome).
type localScan struct {
	s       *Service
	r       *run
	ctx     context.Context
	group   string
	dest    Location
	policy  ConflictPolicy
	noRules bool
	st      *scanState
}

// skip reports entry (i, path) as skipped and moves past it.
func (ls *localScan) skip(i int, path []string, local, reason string) {
	ls.st.pass(i, path, "")
	ls.r.Item(ItemResult{Key: local, Status: ItemSkipped, Err: reason})
}

// fail reports entry (i, path) as failed and moves past it.
func (ls *localScan) fail(i int, path []string, local, key string, err error) {
	it := ItemResult{Key: local, Status: ItemFailed, Err: err.Error()}
	ls.st.fail(scanFailure{item: it, root: &scanRoot{Local: local, Key: key}}, i, path, "")
	ls.r.Item(it)
}

// root scans root i. after is the position inside it (empty: from the start).
// It returns an error only when the task context ended; per-path problems are
// reported as items.
func (ls *localScan) root(i int, rt scanRoot, after []string) error {
	if len(after) > 0 {
		return ls.dir(i, rt.Local, rt.Key, nil, after)
	}
	fi, err := os.Lstat(rt.Local)
	if err != nil {
		ls.fail(i, nil, rt.Local, rt.Key, err)
		return nil
	}
	return ls.visit(i, rt.Local, rt.Key, nil, fi.Mode(), fi.Size())
}

// visit handles one path (path: its names below the root); size is its size
// when it is a regular file.
func (ls *localScan) visit(i int, local, key string, path []string, mode fs.FileMode, size int64) error {
	if err := ls.ctx.Err(); err != nil {
		return err
	}
	switch {
	case mode&fs.ModeSymlink != 0:
		ls.skip(i, path, local, "symbolic link (not followed)")
	case mode&fs.ModeIrregular != 0:
		ls.skip(i, path, local, "reparse point or irregular file (not followed)")
	case mode.IsDir():
		return ls.dir(i, local, key, path, nil)
	case mode.IsRegular():
		return ls.submit(i, path, local, uploadJob{Local: local, Key: key, Size: size})
	default:
		ls.skip(i, path, local, "not a regular file (device, socket or pipe)")
	}
	return nil
}

// dir walks a folder. after, when non-empty, is the position below it:
// entries sorted before after[0] were handled already, after[0] itself is
// handled (a file, or a folder handed over while empty) or resumed (a folder).
func (ls *localScan) dir(i int, local, key string, path, after []string) error {
	ents, err := os.ReadDir(local)
	if err != nil {
		ls.fail(i, path, local, key, err)
		return nil
	}
	if len(ents) == 0 {
		if len(after) > 0 {
			return nil
		}
		return ls.submit(i, path, local, uploadJob{Local: local, Key: key + "/", Folder: true})
	}
	for _, e := range ents {
		name := e.Name()
		child := append(path[:len(path):len(path)], name)
		if len(after) > 0 {
			switch c := strings.Compare(name, after[0]); {
			case c < 0:
				continue
			case c == 0:
				rest := after[1:]
				after = nil
				if !e.IsDir() || len(rest) == 0 {
					continue // handled: a file or other entry, or a folder handed over empty or that failed
				}
				if err := ls.dir(i, filepath.Join(local, name), key+"/"+name, child, rest); err != nil {
					return err
				}
				continue
			default:
				after = nil
			}
		}
		var size int64
		if e.Type().IsRegular() {
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
		}
		if err := ls.visit(i, filepath.Join(local, name), key+"/"+name, child, e.Type(), size); err != nil {
			return err
		}
	}
	return nil
}

// submit queues the upload task of one file (or empty folder). Per-path
// problems become failed items; the returned error is only ever the end of the
// task context (including while the scan waits for room in the queue).
func (ls *localScan) submit(i int, path []string, local string, j uploadJob) error {
	if len(j.Key) > maxKeyBytes {
		ls.fail(i, path, local, j.Key, invalid("Upload", "object key is longer than %d bytes: %s", maxKeyBytes, j.Key))
		return nil
	}
	if err := ls.s.waitGroupBacklog(ls.ctx, ls.group, ls.s.backlogMax(KindUpload)); err != nil {
		return err
	}
	j.Dest, j.Policy, j.NoRules = ls.dest, ls.policy, ls.noRules
	ls.s.submitWith(ls.s.uploadTaskSpec(ls.group, j), func() { ls.st.handOver(i, path, "") })
	if n := ls.st.fileCount(); n%50 == 0 {
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

// scanList walks every object under prefix (no delimiter) whose key sorts
// after after ("" = all), page by page, and calls fn for each. Keys are
// URL-decoded when the service says it encoded them.
func scanList(ctx context.Context, c *storage.Client, bucket, prefix, after string, fn func(bkObj) error) error {
	return bkListObjects(c, bucket)(ctx, bkScan{Prefix: prefix, After: after}, func(page []bkObj, _ string) error {
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
