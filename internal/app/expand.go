package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// This file turns a Selection into a stream of objects and runs a worker pool
// over that stream (the engine under Copy, Move, Delete, the in-place rewrites
// and RestoreDeleted).
//
// Expansion happens inside the task: folders and "All" are listed page by page
// (1000 keys) and the pages flow through a bounded channel to BulkWorkers
// workers, so a million-key prefix never sits in memory. What the user
// selected explicitly (single keys, version refs) is resolved lazily with a
// HEAD by the operation that needs ETag, size or version ID.
//
// Cancellation: every listed-but-unprocessed object is reported as
// "unprocessed" one by one. The part of the selection that was never listed is
// reported as ONE unprocessed item with the key bkRemainderKey and remembered
// in bkState so that Retry resumes the listing exactly there.

// bkRemainderKey is the key of the pseudo item that stands for "everything
// that was not listed yet".
const bkRemainderKey = "(remaining objects not yet listed)"

// bkPageSize is the listing page size. It is a variable only so that tests can
// make listings span several pages cheaply.
var bkPageSize int32 = 1000

// errBkStop ends a listing early without being an error.
var errBkStop = errors.New("stop listing")

// bkObj is one expanded object. Resolved means ETag/Size/Modified are known
// (from a listing or a HEAD); explicitly selected keys start unresolved.
type bkObj struct {
	Key, VersionID, ETag string
	Size                 int64
	Modified             time.Time
	StorageClass         string
	Resolved             bool
}

// bkScan is a prefix listing that resumes after After.
type bkScan struct{ Prefix, After string }

// bkScope is the expanded form of a Selection.
type bkScope struct {
	Objs  []bkObj // explicit objects / versions, unresolved
	Scans []bkScan
}

func (sc bkScope) empty() bool { return len(sc.Objs) == 0 && len(sc.Scans) == 0 }

func bkValidKey(op, key string) error {
	switch {
	case key == "":
		return invalid(op, "an empty key was selected")
	case len(key) > 1024:
		return invalid(op, "key %.40q... is longer than 1024 bytes", key)
	case strings.IndexByte(key, 0) >= 0:
		return invalid(op, "key %q contains a NUL byte", key)
	}
	return nil
}

// bkBuildScope validates sel and expands it into explicit objects and prefix
// scans. Nested selections are collapsed (a key under a selected folder is
// dropped) so that no object is processed twice. requirePrefix demands that
// every key lies strictly below sel.Prefix (needed to compute relative paths).
func bkBuildScope(op string, sel Selection, allowVersions, requirePrefix bool) (bkScope, error) {
	if err := checkLocation(op, sel.Location, true); err != nil {
		return bkScope{}, err
	}
	if sel.All {
		if len(sel.Keys) > 0 || len(sel.Versions) > 0 {
			return bkScope{}, invalid(op, "a selection of everything cannot also list keys or versions")
		}
		return bkScope{Scans: []bkScan{{Prefix: sel.Prefix}}}, nil
	}
	if len(sel.Keys)+len(sel.Versions) == 0 {
		return bkScope{}, invalid(op, "nothing is selected")
	}
	if len(sel.Versions) > 0 && !allowVersions {
		return bkScope{}, invalid(op, "this operation applies to current objects only, not to specific versions")
	}
	below := func(key string) error {
		if err := bkValidKey(op, key); err != nil {
			return err
		}
		if requirePrefix && (!strings.HasPrefix(key, sel.Prefix) || len(key) <= len(sel.Prefix)) {
			return invalid(op, "%q is not inside %q", key, sel.Prefix)
		}
		return nil
	}
	var folders []string
	var objs []bkObj
	for _, k := range sel.Keys {
		if err := below(k); err != nil {
			return bkScope{}, err
		}
		if strings.HasSuffix(k, "/") {
			folders = append(folders, k)
		} else {
			objs = append(objs, bkObj{Key: k})
		}
	}
	for _, v := range sel.Versions {
		if err := below(v.Key); err != nil {
			return bkScope{}, err
		}
		if v.VersionID == "" {
			return bkScope{}, invalid(op, "version of %q has no version ID", v.Key)
		}
		objs = append(objs, bkObj{Key: v.Key, VersionID: v.VersionID})
	}
	sort.Slice(folders, func(i, j int) bool {
		if len(folders[i]) != len(folders[j]) {
			return len(folders[i]) < len(folders[j])
		}
		return folders[i] < folders[j]
	})
	var kept []string
	for _, f := range folders {
		nested := false
		for _, k := range kept {
			if strings.HasPrefix(f, k) {
				nested = true
				break
			}
		}
		if !nested {
			kept = append(kept, f)
		}
	}
	var sc bkScope
	for _, f := range kept {
		sc.Scans = append(sc.Scans, bkScan{Prefix: f})
	}
	seen := map[ObjectRef]bool{}
	for _, o := range objs {
		ref := ObjectRef{o.Key, o.VersionID}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if o.VersionID == "" {
			under := false
			for _, f := range kept {
				if strings.HasPrefix(o.Key, f) {
					under = true
					break
				}
			}
			if under {
				continue
			}
		}
		sc.Objs = append(sc.Objs, o)
	}
	return sc, nil
}

// bkState lets Retry continue a listing that was cut short.
type bkState struct {
	mu  sync.Mutex
	rem []bkScan
}

func (st *bkState) set(rem []bkScan) {
	st.mu.Lock()
	st.rem = rem
	st.mu.Unlock()
}

// retryScope builds the scope of a retry from the failed and unprocessed
// items of the finished task. Objects are re-resolved by HEAD, so a retry
// works on what the bucket holds now.
func (st *bkState) retryScope(items []ItemResult) (bkScope, bool) {
	st.mu.Lock()
	rem := append([]bkScan(nil), st.rem...)
	st.mu.Unlock()
	var sc bkScope
	remDone := false
	seen := map[ObjectRef]bool{}
	for _, it := range items {
		if it.Key == bkRemainderKey && it.VersionID == "" {
			if !remDone {
				sc.Scans = append(sc.Scans, rem...)
				remDone = true
			}
			continue
		}
		ref := ObjectRef{it.Key, it.VersionID}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		sc.Objs = append(sc.Objs, bkObj{Key: it.Key, VersionID: it.VersionID})
	}
	return sc, !sc.empty()
}

// ---- listing -----------------------------------------------------------------------------

// bkLister streams the objects of one scan. emit receives each page together
// with the key after which listing can resume once the page is dealt with.
type bkLister func(ctx context.Context, scan bkScan, emit func(page []bkObj, resumeAfter string) error) error

func bkDecoder(enc types.EncodingType) func(string) string {
	if enc != types.EncodingTypeUrl {
		return func(v string) string { return v }
	}
	return func(v string) string {
		if d, err := url.QueryUnescape(v); err == nil {
			return d
		}
		return v
	}
}

// bkListObjects lists the current objects below scan.Prefix (flat, so folder
// placeholders and everything nested are included).
func bkListObjects(c *storage.Client, bucket string) bkLister {
	return func(ctx context.Context, scan bkScan, emit func([]bkObj, string) error) error {
		in := &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket), MaxKeys: aws.Int32(bkPageSize), EncodingType: types.EncodingTypeUrl,
		}
		if scan.Prefix != "" {
			in.Prefix = aws.String(scan.Prefix)
		}
		if scan.After != "" {
			in.StartAfter = aws.String(scan.After)
		}
		after := scan.After
		for {
			out, err := c.S3().ListObjectsV2(ctx, in)
			if err != nil {
				return classify("List", err)
			}
			dec := bkDecoder(out.EncodingType)
			page := make([]bkObj, 0, len(out.Contents))
			for _, o := range out.Contents {
				sc := string(o.StorageClass)
				if sc == "" {
					sc = string(types.ObjectStorageClassStandard)
				}
				page = append(page, bkObj{
					Key: dec(aws.ToString(o.Key)), ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.Size),
					Modified: aws.ToTime(o.LastModified), StorageClass: sc, Resolved: true,
				})
			}
			if len(page) > 0 {
				after = page[len(page)-1].Key
			}
			if err := emit(page, after); err != nil {
				return err
			}
			if !aws.ToBool(out.IsTruncated) {
				return nil
			}
			tok := aws.ToString(out.NextContinuationToken)
			if tok == "" {
				return opErr("List", ErrOther, "the service reported a truncated listing without a continuation token")
			}
			in.ContinuationToken = aws.String(tok)
			in.StartAfter = nil
		}
	}
}

// bkListDeleted streams the keys below scan.Prefix whose latest entry is a
// delete marker and which still have a non-marker version. Each emitted
// object carries the VersionID, ETag and size of the newest such version.
func bkListDeleted(c *storage.Client, bucket string) bkLister {
	return func(ctx context.Context, scan bkScan, emit func([]bkObj, string) error) error {
		in := &s3.ListObjectVersionsInput{
			Bucket: aws.String(bucket), MaxKeys: aws.Int32(bkPageSize), EncodingType: types.EncodingTypeUrl,
		}
		if scan.Prefix != "" {
			in.Prefix = aws.String(scan.Prefix)
		}
		if scan.After != "" {
			in.KeyMarker = aws.String(scan.After)
		}
		type group struct {
			deleted bool
			newest  *bkObj
		}
		groups := map[string]*group{}
		get := func(k string) *group {
			g := groups[k]
			if g == nil {
				g = &group{}
				groups[k] = g
			}
			return g
		}
		after := scan.After
		for {
			out, err := c.S3().ListObjectVersions(ctx, in)
			if err != nil {
				return classify("ListVersions", err)
			}
			dec := bkDecoder(out.EncodingType)
			for _, m := range out.DeleteMarkers {
				if aws.ToBool(m.IsLatest) {
					get(dec(aws.ToString(m.Key))).deleted = true
				}
			}
			for _, v := range out.Versions {
				k := dec(aws.ToString(v.Key))
				g := get(k)
				if g.newest == nil {
					sc := string(v.StorageClass)
					if sc == "" {
						sc = string(types.ObjectStorageClassStandard)
					}
					g.newest = &bkObj{Key: k, VersionID: aws.ToString(v.VersionId), ETag: aws.ToString(v.ETag), Size: aws.ToInt64(v.Size),
						Modified: aws.ToTime(v.LastModified), StorageClass: sc, Resolved: true}
				}
			}
			truncated := aws.ToBool(out.IsTruncated)
			nextKey := dec(aws.ToString(out.NextKeyMarker))
			// Keys strictly before NextKeyMarker are complete; that key itself may
			// continue on the next page.
			var done []string
			for k := range groups {
				if !truncated || k < nextKey {
					done = append(done, k)
				}
			}
			sort.Strings(done)
			var page []bkObj
			for _, k := range done {
				if g := groups[k]; g.deleted && g.newest != nil {
					page = append(page, *g.newest)
				}
				delete(groups, k)
				after = k
			}
			// after is the resume point: every key up to it is dealt with.
			if err := emit(page, after); err != nil {
				return err
			}
			if !truncated {
				return nil
			}
			if nextKey == "" {
				return opErr("ListVersions", ErrOther, "the service reported a truncated listing without a key marker")
			}
			in.KeyMarker = aws.String(nextKey)
			in.VersionIdMarker = nil
			if v := aws.ToString(out.NextVersionIdMarker); v != "" {
				in.VersionIdMarker = aws.String(v)
			}
		}
	}
}

// ---- head ----------------------------------------------------------------------------------

// bkHead resolves one object (or version) with a HEAD.
func bkHead(ctx context.Context, c *storage.Client, bucket, key, versionID string) (bkObj, *s3.HeadObjectOutput, error) {
	in := &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	h, err := c.S3().HeadObject(ctx, in)
	if err != nil {
		return bkObj{}, nil, classify("Head", err)
	}
	o := bkObj{
		Key: key, VersionID: aws.ToString(h.VersionId), ETag: aws.ToString(h.ETag), Size: aws.ToInt64(h.ContentLength),
		Modified: aws.ToTime(h.LastModified), StorageClass: string(h.StorageClass), Resolved: true,
	}
	if o.VersionID == "" {
		o.VersionID = versionID
	}
	if o.StorageClass == "" {
		o.StorageClass = string(types.StorageClassStandard)
	}
	return o, h, nil
}

// ---- worker pool ---------------------------------------------------------------------------

// bkPipe runs process over the objects of a scope with a pool of workers.
type bkPipe struct {
	workers int
	batch   int // objects per process call (1000 for DeleteObjects)
	lister  bkLister
	// process handles one batch and returns one result per object, in order.
	process func(ctx context.Context, batch []bkObj) []ItemResult
}

func bkUnprocessed(r *run, objs []bkObj, n *atomic.Int64) {
	for _, o := range objs {
		n.Add(1)
		r.Item(ItemResult{Key: o.Key, VersionID: o.VersionID, Status: ItemUnprocessed, Err: "canceled before it was processed"})
	}
}

// run feeds scope through the pool. It returns the not-yet-listed remainder
// (to be stored for Retry) and ctx.Err() when the run was cut short.
func (p *bkPipe) run(ctx context.Context, r *run, scope bkScope) ([]bkScan, error) {
	workers := max(p.workers, 1)
	batch := max(p.batch, 1)
	var unproc atomic.Int64
	ch := make(chan []bkObj, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range ch {
				if ctx.Err() != nil {
					bkUnprocessed(r, b, &unproc)
					continue
				}
				for i, res := range p.safeProcess(ctx, b) {
					if res.Status == ItemUnprocessed {
						unproc.Add(1)
					}
					if res.Key == "" {
						res.Key = b[i].Key
					}
					r.Item(res)
				}
			}
		}()
	}

	var cur []bkObj
	alive := true
	flush := func() {
		if len(cur) == 0 {
			return
		}
		b := cur
		cur = nil
		select {
		case ch <- b:
		case <-ctx.Done():
			bkUnprocessed(r, b, &unproc)
			alive = false
		}
	}
	add := func(o bkObj) bool {
		if !alive || ctx.Err() != nil {
			alive = false
			bkUnprocessed(r, []bkObj{o}, &unproc)
			return false
		}
		cur = append(cur, o)
		if len(cur) >= batch {
			flush()
		}
		return alive
	}

	var rem []bkScan
	var listErr error
	if len(scope.Objs) > 0 {
		r.AddTotal(0, len(scope.Objs))
		for i, o := range scope.Objs {
			if !add(o) {
				bkUnprocessed(r, scope.Objs[i+1:], &unproc)
				break
			}
		}
	}
	if !alive {
		rem = append(rem, scope.Scans...)
	} else {
		for idx, scan := range scope.Scans {
			resume := scan.After
			err := p.lister(ctx, scan, func(page []bkObj, after string) error {
				if len(page) > 0 {
					var sum int64
					for _, o := range page {
						sum += o.Size
					}
					r.AddTotal(sum, len(page))
				}
				for i, o := range page {
					if !add(o) {
						bkUnprocessed(r, page[i+1:], &unproc)
						resume = after
						return ctx.Err()
					}
				}
				resume = after
				return ctx.Err()
			})
			if err != nil {
				rem = append(rem, scope.Scans[idx:]...)
				rem[0].After = resume
				listErr = err
				break
			}
		}
	}
	flush()
	close(ch)
	wg.Wait()

	canceled := ctx.Err() != nil
	if len(rem) > 0 {
		it := ItemResult{Key: bkRemainderKey, Status: ItemUnprocessed, Err: "canceled before the rest of the selection was listed"}
		if !canceled && listErr != nil {
			it.Status = ItemFailed
			it.Err = "listing stopped: " + classify("", listErr).Error()
		}
		unproc.Add(1)
		r.Item(it)
	}
	if canceled && unproc.Load() > 0 {
		return rem, ctx.Err()
	}
	return rem, nil
}

func (p *bkPipe) safeProcess(ctx context.Context, b []bkObj) (res []ItemResult) {
	defer func() {
		if x := recover(); x != nil {
			res = make([]ItemResult, len(b))
			for i, o := range b {
				res[i] = ItemResult{Key: o.Key, VersionID: o.VersionID, Status: ItemFailed, Err: fmt.Sprintf("internal error: %v", x)}
			}
		}
	}()
	res = p.process(ctx, b)
	for len(res) < len(b) { // defensive: never lose an object silently
		o := b[len(res)]
		res = append(res, ItemResult{Key: o.Key, VersionID: o.VersionID, Status: ItemFailed, Err: "no result was produced"})
	}
	return res
}

// bkFail turns err into a failed item, or into an unprocessed one when the
// task was canceled (the object is safe to retry).
func bkFail(ctx context.Context, res ItemResult, err error) ItemResult {
	ce := classify("", err)
	if ctx.Err() != nil && IsKind(ce, ErrCanceled) {
		res.Status, res.Err = ItemUnprocessed, "canceled before it completed"
		return res
	}
	res.Status, res.Err = ItemFailed, ce.Error()
	return res
}

func bkFailf(res ItemResult, format string, args ...any) ItemResult {
	res.Status, res.Err = ItemFailed, fmt.Sprintf(format, args...)
	return res
}

// bkWorkers is the number of workers of one bulk task.
func (s *Service) bkWorkers() int {
	if n := s.Settings().BulkWorkers; n >= 1 {
		return n
	}
	return 8
}

// bkTransferOpts are the storage.Copy options derived from the settings.
func (s *Service) bkTransferOpts(progress func(int64)) storage.TransferOptions {
	o := storage.TransferOptions{Concurrency: 4, Progress: progress}
	if mib := s.Settings().PartSizeMiB; mib >= 5 {
		o.PartSize = int64(mib) << 20
	}
	return o
}

// bkProgress adapts storage's cumulative Progress callback to run.AddBytes.
// Calling it with the final size completes the item's share of the progress.
func bkProgress(r *run) func(int64) {
	var last atomic.Int64
	return func(done int64) {
		for {
			cur := last.Load()
			if done <= cur {
				return
			}
			if last.CompareAndSwap(cur, done) {
				r.AddBytes(done - cur)
				return
			}
		}
	}
}

// bkTitle names a task after its selection: "Copy report.pdf", "Delete 12 items".
func bkTitle(verb string, sel Selection) string {
	n := len(sel.Keys) + len(sel.Versions)
	switch {
	case sel.All:
		if sel.Prefix == "" {
			return verb + " everything in " + sel.Bucket
		}
		return verb + " everything in " + sel.Prefix
	case n == 1 && len(sel.Keys) == 1:
		return verb + " " + entryName(sel.Keys[0])
	case n == 1:
		return verb + " " + entryName(sel.Versions[0].Key) + " (one version)"
	}
	return fmt.Sprintf("%s %d items", verb, n)
}

func bkURL(loc Location) string { return "s3://" + loc.Bucket + "/" + loc.Prefix }
