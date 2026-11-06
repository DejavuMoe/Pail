package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// This file scans the two sides of a sync: the local tree, the remote prefix and
// the per-object metadata. File contents are read only to compute a SHA-256 in
// checksum mode.

// syIsTempName matches the hidden temporary files of downloads in progress;
// sync never uploads, compares or trashes them.
func syIsTempName(name string) bool {
	return strings.HasPrefix(name, ".pail-") && strings.HasSuffix(name, ".tmp")
}

// syExcluder applies SyncJob.Excludes with rules.Match semantics (case
// sensitive, "*" crosses "/", anchored at the root of the sync).
//
//   - A pattern that ends with "/" excludes the directories whose relative path
//     plus "/" it matches, together with everything below them.
//   - Any other pattern is matched against the relative path of each file.
type syExcluder struct{ files, dirs []string }

func newSyExcluder(patterns []string) *syExcluder {
	e := &syExcluder{}
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			e.dirs = append(e.dirs, p)
		} else {
			e.files = append(e.files, p)
		}
	}
	return e
}

func (e *syExcluder) dir(rel string) bool {
	for _, p := range e.dirs {
		if rules.Match(p, rel+"/") {
			return true
		}
	}
	return false
}

// path reports whether the file rel is excluded and, if so, which path the
// preview reports: "dir/" when an ancestor directory is excluded (outermost
// first), else rel.
func (e *syExcluder) path(rel string) (string, bool) {
	if len(e.dirs) > 0 {
		for i := 0; i < len(rel); i++ {
			if rel[i] == '/' && e.dir(rel[:i]) {
				return rel[:i+1], true
			}
		}
	}
	if e.file(rel) {
		return rel, true
	}
	return "", false
}

// file reports whether a file pattern matches rel. A scan that already skips
// excluded directories (syScanLocal) needs nothing more than this.
func (e *syExcluder) file(rel string) bool {
	for _, p := range e.files {
		if rules.Match(p, rel) {
			return true
		}
	}
	return false
}

// ---- local side ---------------------------------------------------------------------------

// syLocal is one regular local file. Rel uses "/" separators.
type syLocal struct {
	Rel   string
	Abs   string
	Size  int64
	MTime time.Time
}

type syLocalScan struct {
	files []syLocal
	// protected holds the relative paths of files and entries that were seen but
	// are not synchronised (excluded files, symbolic links, irregular entries,
	// temporary files): their counterpart at the destination must never be
	// treated as extraneous. Files below an excluded directory are covered by
	// the exclusion check on the destination side.
	protected map[string]bool
	skips     []SyncAction
}

// syScanLocal walks root. Regular files are collected; symbolic links, junctions
// and other irregular entries are reported as skips. Any read error aborts the
// scan: a partial tree must never drive deletions.
func syScanLocal(ctx context.Context, root string, ex *syExcluder) (*syLocalScan, error) {
	sc := &syLocalScan{protected: map[string]bool{}}
	var walk func(abs, rel string) error
	walk = func(abs, rel string) error {
		ents, err := os.ReadDir(abs)
		if err != nil {
			return classify("PreviewSync", err)
		}
		for _, e := range ents {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := e.Name()
			r := rel + name
			p := filepath.Join(abs, name)
			typ := e.Type()
			switch {
			case !utf8.ValidString(name):
				sc.skip(r, "name is not valid UTF-8")
			case typ&fs.ModeSymlink != 0:
				sc.skip(r, "symbolic link (not followed)")
			case typ&fs.ModeIrregular != 0:
				sc.skip(r, "reparse point or irregular file (not followed)")
			case typ.IsDir():
				if ex.dir(r) {
					sc.skips = append(sc.skips, SyncAction{Op: "skip", Path: r + "/", Reason: "excluded"})
					continue
				}
				if err := walk(p, r+"/"); err != nil {
					return err
				}
			case typ.IsRegular():
				if syIsTempName(name) {
					sc.skip(r, "Pail temporary file")
					continue
				}
				if ex.file(r) { // excluded directories were pruned above
					sc.protected[r] = true
					sc.skips = append(sc.skips, SyncAction{Op: "skip", Path: r, Reason: "excluded"})
					continue
				}
				fi, err := e.Info()
				if err != nil {
					return classify("PreviewSync", err)
				}
				sc.files = append(sc.files, syLocal{Rel: r, Abs: p, Size: fi.Size(), MTime: fi.ModTime()})
			default:
				sc.skip(r, "not a regular file (device, socket or pipe)")
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	sort.Slice(sc.files, func(i, j int) bool { return sc.files[i].Rel < sc.files[j].Rel })
	return sc, nil
}

func (sc *syLocalScan) skip(rel, reason string) {
	sc.protected[rel] = true
	sc.skips = append(sc.skips, SyncAction{Op: "skip", Path: rel, Reason: reason})
}

// hashBuffers holds the read buffers of syHashFile: a sync hashes thousands of
// files, eight at a time, and each would otherwise allocate 1 MiB.
var hashBuffers = sync.Pool{New: func() any { b := make([]byte, 1<<20); return &b }}

// syHashFile returns the lower-case hex SHA-256 of the file as it was scanned.
// It fails when the file changed (size or time) between the scan and the end of
// reading.
func syHashFile(ctx context.Context, l syLocal) (string, error) {
	f, err := os.Open(l.Abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	same := func() error {
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if fi.Size() != l.Size || !fi.ModTime().Equal(l.MTime) {
			return fmt.Errorf("the file changed while it was being read")
		}
		return nil
	}
	if err := same(); err != nil {
		return "", err
	}
	h := sha256.New()
	bp := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(bp)
	buf := *bp
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := f.Read(buf)
		h.Write(buf[:n])
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if err := same(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- remote side --------------------------------------------------------------------------

// syRemote is one remote object below the sync prefix. Rel is its key without
// the prefix.
type syRemote struct {
	Rel, Key, ETag string
	Size           int64
	Modified       time.Time
}

// syListRemote lists every object below loc.Prefix. Folder placeholders
// (keys ending in "/") are not part of a sync and are left out.
func syListRemote(ctx context.Context, c *storage.Client, loc Location) ([]syRemote, error) {
	var out []syRemote
	err := scanList(ctx, c, loc.Bucket, loc.Prefix, func(o bkObj) error {
		rel := relativeKey(o.Key, loc.Prefix)
		if rel == "" || strings.HasSuffix(rel, "/") || !strings.HasPrefix(o.Key, loc.Prefix) {
			return nil
		}
		out = append(out, syRemote{Rel: rel, Key: o.Key, ETag: o.ETag, Size: o.Size, Modified: o.Modified})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// syHead returns the user metadata (lower-case keys) of an object.
func syHead(ctx context.Context, c *storage.Client, bucket, key string) (map[string]string, error) {
	h, err := headObj(ctx, c, "HeadObject", bucket, key, "")
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(h.Metadata))
	for k, v := range h.Metadata {
		m[strings.ToLower(k)] = v
	}
	return m, nil
}

// syParallel calls fn(i) for i in [0,n) with up to workers goroutines and stops
// handing out work once ctx is done.
func syParallel(ctx context.Context, n, workers int, fn func(i int)) {
	if workers > n {
		workers = n
	}
	if workers < 1 {
		return
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				fn(i)
			}
		}()
	}
loop:
	for i := 0; i < n; i++ {
		select {
		case idx <- i:
		case <-ctx.Done():
			break loop
		}
	}
	close(idx)
	wg.Wait()
}

// syPhysicalKey maps the logical key (prefix + relative path) of an uploaded
// file to the object key the upload will really write: ZIP and AES-ZIP rules
// append ".zip", GZIP keeps the key. compressed reports that a compression rule
// applies. skip is non-empty when the upload task would refuse the file
// (conflicting rule). list is the saved rule set, which was validated when it
// was loaded or saved.
func syPhysicalKey(list []rules.UploadRule, bucket, key string, noRules bool) (phys string, compressed bool, skip string) {
	if noRules {
		return key, false, ""
	}
	opts := rules.Select(list, bucket, key)
	comp := opts.Compression
	if comp == nil {
		return key, false, ""
	}
	enc := opts.Headers["Content-Encoding"]
	switch comp.Method {
	case "gzip":
		if enc != "" && !strings.EqualFold(enc, "identity") {
			return "", true, errGzipEncodingConflict.Error()
		}
		return key, true, ""
	case "zip":
		if enc != "" {
			return "", true, "a rule sets Content-Encoding and also asks for ZIP compression"
		}
		return key + ".zip", true, ""
	}
	return "", true, fmt.Sprintf("unsupported compression method %q", comp.Method)
}
