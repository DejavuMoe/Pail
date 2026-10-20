package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// The app-layer suite drives internal/app exactly like the UI does (Service
// methods, task queue, ItemResults) against the same isolated MinIO as s3_test.go.
// A second, independent SDK client (appEnv.sdk) is used to seed and verify state,
// so assertions never go through the code under test.

type appEnv struct {
	ctx  context.Context
	svc  *app.Service
	acct app.Account
	cfg  storage.Config
	sdk  *storage.Client
}

func newAppEnvAt(t *testing.T, endpoint, checksumMode string) *appEnv {
	t.Helper()
	cfg := localConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	t.Cleanup(cancel)
	sdk, err := storage.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.New(app.Options{ConfigDir: t.TempDir(), Keyring: app.NewMemoryKeyring()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = svc.Shutdown(sctx)
	})
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	acct, err := svc.SaveAccount(app.Account{
		Name: "minio", Provider: app.ProviderS3, Endpoint: endpoint, Region: "us-east-1", PathStyle: true,
		Credentials: app.CredStatic, AccessKeyID: cfg.AccessKeyID, ChecksumMode: checksumMode,
	}, &app.Secrets{SecretAccessKey: cfg.SecretAccessKey})
	if err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	st := svc.Settings()
	st.PartSizeMiB = 5
	if err := svc.SaveSettings(st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	return &appEnv{ctx: ctx, svc: svc, acct: acct, cfg: cfg, sdk: sdk}
}

func newApp(t *testing.T) *appEnv {
	t.Helper()
	if v := os.Getenv("PAIL_TEST_SERVER_VERSION"); v != "" {
		t.Logf("server: %s", v)
	}
	return newAppEnvAt(t, "", "")
}

// bucket creates a test-owned bucket through the app (CreateBucket, and the
// versioning config for versioned ones) and registers a tolerant cleanup.
func (a *appEnv) bucket(t *testing.T, versioned bool) string {
	t.Helper()
	name := fmt.Sprintf("pail-app-%d", time.Now().UnixNano())
	if err := a.svc.CreateBucket(a.ctx, a.acct.ID, name, "", false); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() { a.forceClean(t, name) })
	if versioned {
		if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Enabled"}`); err != nil {
			t.Fatalf("enable versioning: %v", err)
		}
	}
	return name
}

func (a *appEnv) loc(bucket, prefix string) app.Location {
	return app.Location{AccountID: a.acct.ID, Bucket: bucket, Prefix: prefix}
}

func (a *appEnv) sel(bucket, prefix string, keys ...string) app.Selection {
	return app.Selection{Location: a.loc(bucket, prefix), Keys: keys}
}

// forceClean removes everything from a bucket (all versions, unfinished
// multipart uploads) and the bucket itself; a bucket that is already gone is fine.
func (a *appEnv) forceClean(t *testing.T, bucket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if mp, err := a.sdk.S3().ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &bucket}); err == nil {
		for _, u := range mp.Uploads {
			_, _ = a.sdk.S3().AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: &bucket, Key: u.Key, UploadId: u.UploadId})
		}
	}
	for pages := 0; pages < 200; pages++ {
		page, err := a.sdk.S3().ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: &bucket})
		if err != nil {
			if storage.ErrorCode(err) != "NoSuchBucket" {
				t.Errorf("cleanup list versions %s: %v", bucket, err)
			}
			return
		}
		var ids []storage.ObjectID
		for _, v := range page.Versions {
			ids = append(ids, storage.ObjectID{Key: aws.ToString(v.Key), VersionID: aws.ToString(v.VersionId)})
		}
		for _, m := range page.DeleteMarkers {
			ids = append(ids, storage.ObjectID{Key: aws.ToString(m.Key), VersionID: aws.ToString(m.VersionId)})
		}
		if len(ids) == 0 {
			if _, err := a.sdk.S3().DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: &bucket}); err != nil && storage.ErrorCode(err) != "NoSuchBucket" {
				t.Errorf("cleanup delete bucket %s: %v", bucket, err)
			}
			return
		}
		a.sdk.DeleteObjects(ctx, bucket, ids)
	}
	t.Errorf("cleanup of %s exceeded its page budget", bucket)
}

// ---- tasks ---------------------------------------------------------------------

func (a *appEnv) findTasks(group string) []app.Task {
	var out []app.Task
	for _, tk := range a.svc.Tasks() {
		if tk.GroupID == group {
			out = append(out, tk)
		}
	}
	return out
}

// waitGroup waits until the group has tasks, all of them are finished and the set
// has not changed for a while (a scan task submits the per-file tasks while it runs).
func (a *appEnv) waitGroup(t *testing.T, group string) ([]app.Task, []app.ItemResult) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Minute)
	stable, last := 0, -1
	for {
		ts := a.findTasks(group)
		done := len(ts) > 0
		for _, tk := range ts {
			if !tk.Status.Finished() {
				done = false
			}
		}
		if done && len(ts) == last {
			stable++
		} else {
			stable = 0
		}
		last = len(ts)
		if stable >= 3 {
			var items []app.ItemResult
			for _, tk := range ts {
				items = append(items, a.svc.TaskItems(tk.ID)...)
			}
			return ts, items
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %s did not finish: %+v", group, ts)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (a *appEnv) waitTask(t *testing.T, id string) (app.Task, []app.ItemResult) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Minute)
	for {
		for _, tk := range a.svc.Tasks() {
			if tk.ID == id && tk.Status.Finished() {
				return tk, a.svc.TaskItems(id)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not finish", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func describeTasks(ts []app.Task, items []app.ItemResult) string {
	var b strings.Builder
	for _, tk := range ts {
		fmt.Fprintf(&b, "\n  task %q status=%s err=%q items=%d failed=%d", tk.Title, tk.Status, tk.Err, tk.Items, tk.ItemsFailed)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "\n  item %q [%s] %s", it.Key, it.Status, it.Err)
	}
	return b.String()
}

// groupOK runs the group to completion and requires every task to be Succeeded.
func (a *appEnv) groupOK(t *testing.T, group string) []app.ItemResult {
	t.Helper()
	ts, items := a.waitGroup(t, group)
	for _, tk := range ts {
		if tk.Status != app.StatusSucceeded {
			t.Fatalf("task did not succeed:%s", describeTasks(ts, items))
		}
	}
	return items
}

func (a *appEnv) taskOK(t *testing.T, id string) []app.ItemResult {
	t.Helper()
	tk, items := a.waitTask(t, id)
	if tk.Status != app.StatusSucceeded {
		t.Fatalf("task did not succeed:%s", describeTasks([]app.Task{tk}, items))
	}
	return items
}

func countStatus(items []app.ItemResult, status string) int {
	n := 0
	for _, it := range items {
		if it.Status == status {
			n++
		}
	}
	return n
}

// ---- data --------------------------------------------------------------------------

func payload(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func writeLocal(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLocal(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// localFiles returns relative slash path -> sha256 of every regular file below dir.
func localFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = sum(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hasFileWithSHA(files map[string]string, want string) (string, bool) {
	for p, s := range files {
		if s == want {
			return p, true
		}
	}
	return "", false
}

// ---- independent SDK helpers ---------------------------------------------------------

func (a *appEnv) put(t *testing.T, bucket, key string, data []byte) string {
	t.Helper()
	out, err := a.sdk.S3().PutObject(a.ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: bytes.NewReader(data)})
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	return aws.ToString(out.VersionId)
}

func (a *appEnv) get(t *testing.T, bucket, key, version string) []byte {
	t.Helper()
	b, err := a.tryGet(bucket, key, version)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return b
}

func (a *appEnv) tryGet(bucket, key, version string) ([]byte, error) {
	in := &s3.GetObjectInput{Bucket: &bucket, Key: &key}
	if version != "" {
		in.VersionId = &version
	}
	out, err := a.sdk.S3().GetObject(a.ctx, in)
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (a *appEnv) head(t *testing.T, bucket, key string) *s3.HeadObjectOutput {
	t.Helper()
	out, err := a.sdk.S3().HeadObject(a.ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		t.Fatalf("head %s: %v", key, err)
	}
	return out
}

func (a *appEnv) exists(bucket, key string) bool {
	_, err := a.sdk.S3().HeadObject(a.ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &key})
	return err == nil
}

func (a *appEnv) keys(t *testing.T, bucket, prefix string) []string {
	t.Helper()
	p := s3.NewListObjectsV2Paginator(a.sdk.S3(), &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix})
	var keys []string
	for p.HasMorePages() {
		page, err := p.NextPage(a.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	sort.Strings(keys)
	return keys
}

type verInfo struct {
	Key, ID string
	Marker  bool
	Latest  bool
}

func (a *appEnv) versions(t *testing.T, bucket, prefix string) []verInfo {
	t.Helper()
	var out []verInfo
	in := &s3.ListObjectVersionsInput{Bucket: &bucket, Prefix: &prefix}
	for {
		page, err := a.sdk.S3().ListObjectVersions(a.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.Versions {
			out = append(out, verInfo{aws.ToString(v.Key), aws.ToString(v.VersionId), false, aws.ToBool(v.IsLatest)})
		}
		for _, m := range page.DeleteMarkers {
			out = append(out, verInfo{aws.ToString(m.Key), aws.ToString(m.VersionId), true, aws.ToBool(m.IsLatest)})
		}
		if !aws.ToBool(page.IsTruncated) {
			break
		}
		in.KeyMarker, in.VersionIdMarker = page.NextKeyMarker, page.NextVersionIdMarker
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (a *appEnv) putMany(t *testing.T, bucket, prefix string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 24)
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			key := fmt.Sprintf("%s%05d.txt", prefix, i)
			if _, err := a.sdk.S3().PutObject(a.ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: strings.NewReader("x")}); err != nil {
				errc <- err
			}
		}(i)
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}
