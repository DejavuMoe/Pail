package integration_test

// External-endpoint mode (TestExt*): drives internal/app against a REAL
// S3-compatible server (self-hosted VersityGW), configured only through
// PAIL_EXT_* environment variables. Safety rules (see README.md):
//
//   - every write goes below one unique run prefix "pail-e2e/<UTC>-<random>/";
//   - no bucket creation/deletion and no bucket-level configuration writes;
//   - the run prefix is swept (objects, versions, multipart uploads) and
//     verified empty in cleanup;
//   - credentials never reach files, logs or test output.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/storage"
)

const (
	extPrefixRoot  = "pail-e2e/"
	extBudgetBytes = 300 << 20 // total data volume per run, both directions
	extMaxObject   = 64 << 20
)

type extConfig struct {
	Endpoint, AccessKey, SecretKey, Bucket, Region, CDN string
	PathStyle                                           bool
}

var (
	extCfgOnce sync.Once
	extCfg     extConfig
	extCfgErr  string

	extRunOnce   sync.Once
	extRunPrefix string

	extSpent atomic.Int64

	extIdentityOnce sync.Once
	extIdentity     string

	extSnapMu   sync.Mutex
	extSnap     map[string]string // key -> etag|size, taken before the first write
	extSnapDone bool
	extSnapCap  bool
)

// extLoad reads the environment; the suite is skipped when no endpoint is set.
func extLoad(t testing.TB) extConfig {
	t.Helper()
	if os.Getenv("PAIL_EXT_ENDPOINT") == "" {
		t.Skip("set PAIL_EXT_ENDPOINT (and PAIL_EXT_ACCESS_KEY / PAIL_EXT_SECRET_KEY) to run the external-endpoint suite")
	}
	extCfgOnce.Do(func() {
		c := extConfig{
			Endpoint:  strings.TrimRight(os.Getenv("PAIL_EXT_ENDPOINT"), "/"),
			AccessKey: os.Getenv("PAIL_EXT_ACCESS_KEY"),
			SecretKey: os.Getenv("PAIL_EXT_SECRET_KEY"),
			Bucket:    os.Getenv("PAIL_EXT_BUCKET"),
			Region:    os.Getenv("PAIL_EXT_REGION"),
			CDN:       strings.TrimRight(os.Getenv("PAIL_EXT_CDN"), "/"),
			PathStyle: true,
		}
		if c.Bucket == "" {
			c.Bucket = "test"
		}
		if c.Region == "" {
			c.Region = "us-east-1"
		}
		if v := strings.ToLower(os.Getenv("PAIL_EXT_PATH_STYLE")); v == "0" || v == "false" || v == "no" {
			c.PathStyle = false
		}
		switch {
		case c.AccessKey == "" || c.SecretKey == "":
			extCfgErr = "PAIL_EXT_ACCESS_KEY and PAIL_EXT_SECRET_KEY are required"
		case c.Bucket == "smoji":
			extCfgErr = "the bucket smoji is the read-only CDN reference and must never be used by this suite"
		case !strings.HasPrefix(c.Endpoint, "https://"):
			extCfgErr = "PAIL_EXT_ENDPOINT must be an https:// URL"
		}
		extCfg = c
	})
	if extCfgErr != "" {
		t.Fatal(extCfgErr)
	}
	return extCfg
}

// extGuardPrefix panics unless p is a strict sub-prefix of the run area. Every
// delete path of the suite goes through it.
func extGuardPrefix(p string) {
	rest := strings.TrimPrefix(p, extPrefixRoot)
	if !strings.HasPrefix(p, extPrefixRoot) || strings.Trim(rest, "/") == "" || strings.Contains(p, "..") || strings.HasPrefix(rest, "/") {
		panic(fmt.Sprintf("refusing to operate on prefix %q: it must be below %s", p, extPrefixRoot))
	}
}

func extRunID() string {
	extRunOnce.Do(func() {
		var rnd [3]byte
		_, _ = rand.Read(rnd[:])
		extRunPrefix = extPrefixRoot + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:]) + "/"
	})
	return extRunPrefix
}

func extNewSDK(ctx context.Context, c extConfig, mode string) (*storage.Client, error) {
	return storage.New(ctx, storage.Config{
		Endpoint: c.Endpoint, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey,
		UsePathStyle: c.PathStyle, ChecksumMode: mode,
	})
}

// extServerIdentity does one unsigned HEAD of the bucket URL (no credentials,
// no data) and reports the response headers that identify the gateway.
func extServerIdentity(c extConfig) string {
	extIdentityOnce.Do(func() {
		cl := &http.Client{Timeout: 15 * time.Second}
		req, err := http.NewRequest(http.MethodHead, c.Endpoint+"/"+c.Bucket, nil)
		if err != nil {
			extIdentity = "identity probe failed: " + err.Error()
			return
		}
		resp, err := cl.Do(req)
		if err != nil {
			extIdentity = "identity probe failed: " + err.Error()
			return
		}
		resp.Body.Close()
		var parts []string
		for _, h := range []string{"Server", "Via", "X-Amz-Request-Id", "X-Amz-Id-2", "X-Amz-Bucket-Region", "Cf-Ray", "X-Powered-By"} {
			if v := resp.Header.Get(h); v != "" {
				parts = append(parts, h+"="+v)
			}
		}
		extIdentity = fmt.Sprintf("unsigned HEAD %s/%s -> %d; %s", c.Endpoint, c.Bucket, resp.StatusCode, strings.Join(parts, "; "))
	})
	return extIdentity
}

// ---- environment -------------------------------------------------------------

type extEnv struct {
	*appEnv
	ecfg   extConfig
	bucket string
	prefix string // "<run prefix><test name>/", every key of the test lives below it
}

func extSanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, strings.TrimPrefix(name, "TestExt"))
}

// newExt builds an app Service with one static account for the external
// endpoint and a test-private key prefix. checksumMode is the account's
// ChecksumMode ("" = default).
func newExt(t *testing.T, checksumMode string) *extEnv {
	t.Helper()
	c := extLoad(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	t.Cleanup(cancel)
	extSnapshotOnce(c)
	sdk, err := extNewSDK(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	x := &extEnv{ecfg: c, bucket: c.Bucket, prefix: extRunID() + extSanitize(t.Name()) + "/"}
	extGuardPrefix(x.prefix)
	// Registered first, so it runs last (after the service has shut down).
	t.Cleanup(func() { x.sweep(t) })

	svc, err := app.New(app.Options{ConfigDir: t.TempDir(), Keyring: app.NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cl := context.WithTimeout(context.Background(), 60*time.Second)
		defer cl()
		_ = svc.Shutdown(sctx)
	})
	acct, err := svc.SaveAccount(app.Account{
		Name: "versitygw-ext", Provider: app.ProviderS3, Endpoint: c.Endpoint, Region: c.Region, PathStyle: c.PathStyle,
		Credentials: app.CredStatic, AccessKeyID: c.AccessKey, ChecksumMode: checksumMode,
	}, &app.Secrets{SecretAccessKey: c.SecretKey})
	if err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	st := svc.Settings()
	st.PartSizeMiB = 5 // the app's minimum part size: a 20 MiB object is a 4-part upload
	if err := svc.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	x.appEnv = &appEnv{ctx: ctx, svc: svc, acct: acct, cfg: storage.Config{Endpoint: c.Endpoint, Region: c.Region}, sdk: sdk}
	t.Logf("server identity: %s", extServerIdentity(c))
	t.Logf("run prefix: %s (bucket %s, region %s)", x.prefix, c.Bucket, c.Region)
	return x
}

// k is the full key for a path relative to the test prefix.
func (x *extEnv) k(rel string) string { return x.prefix + rel }

// at is the Location of a folder relative to the test prefix ("" = the test root).
func (x *extEnv) at(rel string) app.Location { return x.loc(x.bucket, x.prefix+rel) }

// pick is a Selection of keys relative to the test prefix, rooted at folder rel.
func (x *extEnv) pick(rel string, keys ...string) app.Selection {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = x.prefix + k
	}
	return app.Selection{Location: x.at(rel), Keys: full}
}

// putk uploads through the independent SDK client below the test prefix.
func (x *extEnv) putk(t *testing.T, rel string, data []byte) string {
	t.Helper()
	x.spend(t, int64(len(data)))
	return x.put(t, x.bucket, x.k(rel), data)
}

// spend accounts data volume against the per-run budget.
func (x *extEnv) spend(t *testing.T, n int64) {
	t.Helper()
	if n > extMaxObject {
		t.Fatalf("object of %d bytes exceeds the %d byte per-object limit", n, extMaxObject)
	}
	if total := extSpent.Add(n); total > extBudgetBytes {
		t.Fatalf("data budget exceeded: %d bytes spent of %d", total, extBudgetBytes)
	}
}

// relKeys lists the keys below prefix+rel, relative to the test prefix.
func (x *extEnv) relKeys(t *testing.T, rel string) []string {
	t.Helper()
	var out []string
	for _, k := range x.keys(t, x.bucket, x.prefix+rel) {
		out = append(out, strings.TrimPrefix(k, x.prefix))
	}
	return out
}

// ---- cleanup -----------------------------------------------------------------------

// sweep removes everything below the test prefix and verifies it is empty.
func (x *extEnv) sweep(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	left, err := extSweep(ctx, x.sdk, x.bucket, x.prefix)
	if err != nil {
		t.Errorf("LEFTOVER: cleanup of %s%s failed: %v (run scripts/test-external.ps1 -CleanupOnly -Prefix %s)", x.bucket+"/", x.prefix, err, x.prefix)
	}
	if len(left) > 0 {
		t.Errorf("LEFTOVER: %d item(s) remain below %s: %v (run scripts/test-external.ps1 -CleanupOnly -Prefix %s)", len(left), x.prefix, left[:min(len(left), 10)], x.prefix)
	}
}

// extSweep deletes (all versions of) every object below prefix, aborts the
// multipart uploads below it, and returns what is still there afterwards.
func extSweep(ctx context.Context, sdk *storage.Client, bucket, prefix string) (left []string, err error) {
	extGuardPrefix(prefix)
	s := sdk.S3()

	// Unfinished multipart uploads (some servers ignore the Prefix filter: filter again).
	abortAll := func() []string {
		var pending []string
		in := &s3.ListMultipartUploadsInput{Bucket: &bucket, Prefix: &prefix}
		for pages := 0; pages < 50; pages++ {
			out, e := s.ListMultipartUploads(ctx, in)
			if e != nil {
				return pending
			}
			for _, u := range out.Uploads {
				k := aws.ToString(u.Key)
				if !strings.HasPrefix(k, prefix) {
					continue
				}
				if _, e := s.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: &bucket, Key: u.Key, UploadId: u.UploadId}); e != nil {
					pending = append(pending, "multipart:"+k)
				}
			}
			if !aws.ToBool(out.IsTruncated) {
				break
			}
			in.KeyMarker, in.UploadIdMarker = out.NextKeyMarker, out.NextUploadIdMarker
		}
		return pending
	}
	abortAll()

	versionsOK := true
	for round := 0; round < 400; round++ {
		var ids []storage.ObjectID
		if versionsOK {
			page, e := s.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: &bucket, Prefix: &prefix})
			if e != nil {
				versionsOK = false
			} else {
				for _, v := range page.Versions {
					ids = append(ids, storage.ObjectID{Key: aws.ToString(v.Key), VersionID: nullVersion(aws.ToString(v.VersionId))})
				}
				for _, m := range page.DeleteMarkers {
					ids = append(ids, storage.ObjectID{Key: aws.ToString(m.Key), VersionID: nullVersion(aws.ToString(m.VersionId))})
				}
			}
		}
		if !versionsOK {
			page, e := s.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix})
			if e != nil {
				return nil, fmt.Errorf("list: %w", e)
			}
			for _, o := range page.Contents {
				ids = append(ids, storage.ObjectID{Key: aws.ToString(o.Key)})
			}
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if !strings.HasPrefix(id.Key, prefix) { // the server ignored the prefix filter
				return nil, fmt.Errorf("server returned key %q outside %q; aborting the sweep", id.Key, prefix)
			}
		}
		progress := 0
		for i, r := range sdk.DeleteObjects(ctx, bucket, ids) {
			if r.Status == storage.DeleteSucceeded {
				progress++
			} else if err == nil {
				err = fmt.Errorf("delete %q: %s %s %s", ids[i].Key, r.Status, r.Code, r.Message)
			}
		}
		if progress == 0 {
			break
		}
	}
	left = append(left, abortAll()...)
	// Verification: current objects, versions and multipart uploads.
	if page, e := s.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix}); e != nil {
		err = fmt.Errorf("verify list: %w", e)
	} else {
		for _, o := range page.Contents {
			left = append(left, aws.ToString(o.Key))
		}
	}
	if versionsOK {
		if page, e := s.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: &bucket, Prefix: &prefix}); e == nil {
			for _, v := range page.Versions {
				left = append(left, "version:"+aws.ToString(v.Key)+"@"+aws.ToString(v.VersionId))
			}
			for _, m := range page.DeleteMarkers {
				left = append(left, "marker:"+aws.ToString(m.Key)+"@"+aws.ToString(m.VersionId))
			}
		}
	}
	if mp, e := s.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &bucket, Prefix: &prefix}); e == nil {
		for _, u := range mp.Uploads {
			if strings.HasPrefix(aws.ToString(u.Key), prefix) {
				left = append(left, "multipart:"+aws.ToString(u.Key))
			}
		}
	}
	if err == nil && len(left) == 0 {
		return nil, nil
	}
	return left, err
}

// nullVersion maps the unversioned-bucket placeholder to "no version id", so the
// delete targets the current object instead of a version that does not exist.
func nullVersion(v string) string {
	if v == "null" {
		return ""
	}
	return v
}

// ---- outside-the-prefix snapshot --------------------------------------------------

func extListOutside(ctx context.Context, sdk *storage.Client, bucket, runPrefix string, limit int) (map[string]string, bool, error) {
	out := map[string]string{}
	in := &s3.ListObjectsV2Input{Bucket: &bucket}
	for {
		page, err := sdk.S3().ListObjectsV2(ctx, in)
		if err != nil {
			return nil, false, err
		}
		for _, o := range page.Contents {
			k := aws.ToString(o.Key)
			if strings.HasPrefix(k, runPrefix) {
				continue
			}
			out[k] = fmt.Sprintf("%s|%d", aws.ToString(o.ETag), aws.ToInt64(o.Size))
			if len(out) >= limit {
				return out, true, nil
			}
		}
		if !aws.ToBool(page.IsTruncated) {
			return out, false, nil
		}
		in.ContinuationToken = page.NextContinuationToken
	}
}

// extSnapshotOnce records (read-only listing) what exists outside the run prefix
// before the first write, so TestMain can report any unexpected difference.
func extSnapshotOnce(c extConfig) {
	extSnapMu.Lock()
	defer extSnapMu.Unlock()
	if extSnapDone {
		return
	}
	extSnapDone = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sdk, err := extNewSDK(ctx, c, "")
	if err != nil {
		return
	}
	snap, capped, err := extListOutside(ctx, sdk, c.Bucket, extRunID(), 3000)
	if err != nil {
		return
	}
	extSnap, extSnapCap = snap, capped
}

func TestMain(m *testing.M) {
	code := m.Run()
	if extCfgErr == "" && extRunPrefix != "" {
		if rc := extFinish(); rc != 0 && code == 0 {
			code = rc
		}
	}
	os.Exit(code)
}

// extFinish is the last line of defence: it sweeps the whole run prefix, verifies it is
// empty, compares the area outside the prefix with the snapshot taken before the
// first write, and reports the data volume.
func extFinish() int {
	c := extCfg
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sdk, err := extNewSDK(ctx, c, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "EXT FINISH: cannot create client:", err)
		return 1
	}
	rc := 0
	left, err := extSweep(ctx, sdk, c.Bucket, extRunPrefix)
	if err != nil || len(left) > 0 {
		fmt.Fprintf(os.Stderr, "EXT FINISH: LEFTOVERS below %s: %v err=%v\n", extRunPrefix, left[:min(len(left), 10)], err)
		rc = 1
	} else {
		fmt.Printf("EXT FINISH: run prefix %s%s is empty (objects, versions, multipart uploads)\n", c.Bucket+"/", extRunPrefix)
	}
	if extSnap != nil {
		now, _, err := extListOutside(ctx, sdk, c.Bucket, extRunPrefix, 3000)
		if err != nil {
			fmt.Println("EXT FINISH: outside-prefix comparison skipped:", err)
		} else {
			var diff []string
			for k, v := range extSnap {
				if nv, ok := now[k]; !ok {
					diff = append(diff, "removed:"+k)
				} else if nv != v {
					diff = append(diff, "changed:"+k)
				}
			}
			if !extSnapCap {
				for k := range now {
					if _, ok := extSnap[k]; !ok {
						diff = append(diff, "added:"+k)
					}
				}
			}
			sort.Strings(diff)
			if len(diff) == 0 {
				fmt.Printf("EXT FINISH: %d key(s) outside the run prefix are unchanged (capped=%v)\n", len(extSnap), extSnapCap)
			} else {
				// Someone else may be using the bucket; report loudly but do not fail on it.
				fmt.Printf("EXT FINISH: WARNING %d difference(s) outside the run prefix (other writers?): %v\n", len(diff), diff[:min(len(diff), 10)])
			}
		}
	}
	fmt.Printf("EXT FINISH: data volume %.1f MiB of %d MiB budget\n", float64(extSpent.Load())/(1<<20), extBudgetBytes>>20)
	return rc
}

// TestExtCleanupOnly removes a stray run prefix (scripts/test-external.ps1 -CleanupOnly).
// It does nothing unless PAIL_EXT_CLEANUP_PREFIX is set.
func TestExtCleanupOnly(t *testing.T) {
	p := os.Getenv("PAIL_EXT_CLEANUP_PREFIX")
	if p == "" {
		t.Skip("PAIL_EXT_CLEANUP_PREFIX not set")
	}
	c := extLoad(t)
	if !strings.HasPrefix(p, extPrefixRoot) {
		t.Fatalf("refusing to clean %q: the prefix must start with %s", p, extPrefixRoot)
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	extGuardPrefix(p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sdk, err := extNewSDK(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	left, err := extSweep(ctx, sdk, c.Bucket, p)
	if err != nil || len(left) > 0 {
		t.Fatalf("cleanup of %s%s incomplete: left=%v err=%v", c.Bucket+"/", p, left[:min(len(left), 10)], err)
	}
	t.Logf("%s%s is empty", c.Bucket+"/", p)
}
