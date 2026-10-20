package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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
	"github.com/DejavuMoe/Pail/internal/rules"
)

func TestAppUploadDownloadRoundTrip(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	src := t.TempDir()
	small := []byte("hello from Pail\n")
	big := payload(1, 11*1024*1024+17) // three 5 MiB parts
	a.writeSmall(t, src, small, big)
	writeLocal(t, filepath.Join(src, "tree", "a.txt"), []byte("tree a"))
	treeB := payload(2, 300*1024)
	writeLocal(t, filepath.Join(src, "tree", "sub dir", "b+c.bin"), treeB)
	if err := os.MkdirAll(filepath.Join(src, "tree", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := a.loc(bucket, "up/")
	g, err := a.svc.Upload(app.UploadRequest{
		Dest:  dest,
		Paths: []string{filepath.Join(src, "single.txt"), filepath.Join(src, "big.bin"), filepath.Join(src, "tree")},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := a.groupOK(t, g)
	if n := countStatus(items, app.ItemFailed); n != 0 {
		t.Fatalf("failed items:%s", describeTasks(nil, items))
	}

	want := map[string][]byte{
		"up/single.txt": small, "up/big.bin": big, "up/tree/a.txt": []byte("tree a"), "up/tree/sub dir/b+c.bin": treeB,
	}
	for k, v := range want {
		if got := a.get(t, bucket, k, ""); sum(got) != sum(v) {
			t.Fatalf("%s: sha256 mismatch after upload", k)
		}
	}
	h := a.head(t, bucket, "up/big.bin")
	if !strings.Contains(aws.ToString(h.ETag), "-3") {
		t.Errorf("11 MiB upload with PartSizeMiB 5 should be a 3-part multipart object, ETag %s", aws.ToString(h.ETag))
	}
	if h.Metadata["pail-mtime"] == "" {
		t.Errorf("pail-mtime metadata missing: %v", h.Metadata)
	}
	if !a.exists(bucket, "up/tree/empty/") {
		t.Errorf("empty local folder did not become a placeholder object; keys: %v", a.keys(t, bucket, "up/"))
	}
	if left, _ := a.svc.MultipartUploads(a.ctx, a.loc(bucket, "")); len(left) != 0 {
		t.Errorf("unfinished multipart uploads left behind: %+v", left)
	}

	// Download: single object, folder, whole bucket.
	d1 := t.TempDir()
	g, err = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, "up/"), Keys: []string{"up/big.bin"}, DestDir: d1})
	if err != nil {
		t.Fatal(err)
	}
	a.groupOK(t, g)
	if got := localFiles(t, d1); got["big.bin"] != sum(big) || len(got) != 1 {
		t.Fatalf("single download: %v", got)
	}

	d2 := t.TempDir()
	g, _ = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, "up/"), Keys: []string{"up/tree/"}, DestDir: d2})
	a.groupOK(t, g)
	got := localFiles(t, d2)
	if got["tree/a.txt"] != sum([]byte("tree a")) || got["tree/sub dir/b+c.bin"] != sum(treeB) {
		t.Fatalf("folder download: %v", got)
	}
	if st, err := os.Stat(filepath.Join(d2, "tree", "empty")); err != nil || !st.IsDir() {
		t.Errorf("placeholder folder was not recreated as a directory: %v", err)
	}

	d3 := t.TempDir()
	g, _ = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, ""), WholeBucket: true, DestDir: d3})
	a.groupOK(t, g)
	got = localFiles(t, d3)
	if got["up/big.bin"] != sum(big) || got["up/single.txt"] != sum(small) || got["up/tree/sub dir/b+c.bin"] != sum(treeB) || len(got) != 4 {
		t.Fatalf("whole-bucket download: %v", got)
	}
}

func (a *appEnv) writeSmall(t *testing.T, dir string, small, big []byte) {
	writeLocal(t, filepath.Join(dir, "single.txt"), small)
	writeLocal(t, filepath.Join(dir, "big.bin"), big)
}

func TestAppUploadConflictPolicies(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	a.put(t, bucket, "c/x.txt", []byte("remote original"))
	src := t.TempDir()
	local := filepath.Join(src, "x.txt")

	upload := func(policy app.ConflictPolicy, content string) []app.ItemResult {
		t.Helper()
		if content != "" { // "" re-uploads the file as it is (same mtime)
			writeLocal(t, local, []byte(content))
		}
		g, err := a.svc.Upload(app.UploadRequest{Dest: a.loc(bucket, "c/"), Paths: []string{local}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		return a.groupOK(t, g)
	}

	items := upload(app.ConflictSkip, "local one")
	if countStatus(items, app.ItemSkipped) != 1 || string(a.get(t, bucket, "c/x.txt", "")) != "remote original" {
		t.Fatalf("skip must leave the remote object alone:%s", describeTasks(nil, items))
	}
	upload(app.ConflictRename, "local two")
	if string(a.get(t, bucket, "c/x.txt", "")) != "remote original" {
		t.Fatal("rename overwrote the original")
	}
	if got := a.keys(t, bucket, "c/"); len(got) != 2 || got[0] != "c/x (1).txt" || got[1] != "c/x.txt" {
		t.Fatalf("rename keys = %v", got)
	}
	if string(a.get(t, bucket, "c/x (1).txt", "")) != "local two" {
		t.Fatal("renamed copy has the wrong content")
	}
	// Re-uploading the identical file under rename must not create "x (2).txt".
	upload(app.ConflictRename, "")
	if got := a.keys(t, bucket, "c/"); len(got) != 2 {
		t.Fatalf("identical re-upload under rename created copies: %v", got)
	}
	// different_size: the remote object is 15 bytes.
	items = upload(app.ConflictDifferentSize, "fifteen bytes!!")
	if countStatus(items, app.ItemSkipped) != 1 || string(a.get(t, bucket, "c/x.txt", "")) != "remote original" {
		t.Fatalf("different_size with equal sizes must skip:%s", describeTasks(nil, items))
	}
	upload(app.ConflictDifferentSize, "a much longer local body")
	if string(a.get(t, bucket, "c/x.txt", "")) != "a much longer local body" {
		t.Fatal("different_size with different sizes must overwrite")
	}
	upload(app.ConflictOverwrite, "overwritten")
	if string(a.get(t, bucket, "c/x.txt", "")) != "overwritten" {
		t.Fatal("overwrite did not replace the object")
	}
}

func TestAppDownloadConflictPolicies(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	a.put(t, bucket, "d/f.txt", []byte("remote body"))
	dir := t.TempDir()
	local := filepath.Join(dir, "f.txt")
	writeLocal(t, local, []byte("local body that is different"))
	dl := func(p app.ConflictPolicy) []app.ItemResult {
		t.Helper()
		g, err := a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, "d/"), Keys: []string{"d/f.txt"}, DestDir: dir, Conflict: p})
		if err != nil {
			t.Fatal(err)
		}
		return a.groupOK(t, g)
	}
	dl(app.ConflictSkip)
	if string(readLocal(t, local)) != "local body that is different" {
		t.Fatal("skip overwrote the local file")
	}
	dl(app.ConflictRename)
	if string(readLocal(t, local)) != "local body that is different" || string(readLocal(t, filepath.Join(dir, "f (1).txt"))) != "remote body" {
		t.Fatalf("rename: %v", localFiles(t, dir))
	}
	dl(app.ConflictOverwrite)
	if string(readLocal(t, local)) != "remote body" {
		t.Fatal("overwrite did not replace the local file")
	}
	for _, e := range mustReadDir(t, dir) {
		if strings.HasPrefix(e, ".") || strings.HasSuffix(e, ".tmp") {
			t.Errorf("temporary file left behind: %s", e)
		}
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestAppCreateOnlyRace writes the destination key from a second client after
// the app's HEAD found it free and before the multipart upload commits. The
// create-only commit (If-None-Match: *) must refuse, the racer's object must
// survive, and the policy must decide what the app does about it.
func TestAppCreateOnlyRace(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	src := t.TempDir()
	big := payload(7, 48*1024*1024) // ten parts: a wide window between HEAD and commit

	race := func(name string, policy app.ConflictPolicy) (app.Task, []app.ItemResult, string) {
		t.Helper()
		writeLocal(t, filepath.Join(src, name), big)
		key := "race/" + name
		stop := make(chan struct{})
		raced := make(chan string, 1)
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				// The app's own progress says the HEAD is long done and bytes are
				// flowing: the multipart commit is still ahead. (MinIO's prefix-filtered
				// ListMultipartUploads cannot be used as the trigger, see
				// TestAppMultipartUploadsListAndAbort.)
				inFlight := false
				for _, tk := range a.svc.Tasks() {
					if tk.Kind == app.KindUpload && tk.Title == "Upload "+name && tk.Status == app.StatusRunning && tk.BytesDone > 0 && tk.BytesDone < tk.BytesTotal {
						inFlight = true
					}
				}
				if inFlight {
					body := "racer wrote " + name
					if _, err := a.sdk.S3().PutObject(a.ctx, &s3.PutObjectInput{Bucket: &bucket, Key: aws.String(key), Body: strings.NewReader(body)}); err == nil {
						raced <- body
					}
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
		g, err := a.svc.Upload(app.UploadRequest{Dest: a.loc(bucket, "race/"), Paths: []string{filepath.Join(src, name)}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		ts, items := a.waitGroup(t, g)
		close(stop)
		var body string
		select {
		case body = <-raced:
		case <-time.After(5 * time.Second):
			t.Fatalf("the racer never found the in-flight multipart upload:%s", describeTasks(ts, items))
		}
		var up app.Task
		for _, tk := range ts {
			if tk.Kind == app.KindUpload {
				up = tk
			}
		}
		return up, items, body
	}

	// skip: create-only; the lost race is reported, nothing is overwritten.
	up, items, body := race("skip.bin", app.ConflictSkip)
	if got := string(a.get(t, bucket, "race/skip.bin", "")); got != body {
		t.Fatalf("racer's object was overwritten (policy skip): %.60q%s", got, describeTasks([]app.Task{up}, items))
	}
	if up.Status == app.StatusSucceeded && countStatus(items, app.ItemSkipped) == 0 {
		t.Errorf("a lost create-only race must not be reported as a plain success:%s", describeTasks([]app.Task{up}, items))
	}
	t.Logf("skip race outcome: status=%s%s", up.Status, describeTasks([]app.Task{up}, items))

	// rename: the racer keeps the name, the upload lands on "name (1).bin".
	up, items, body = race("rename.bin", app.ConflictRename)
	if got := string(a.get(t, bucket, "race/rename.bin", "")); got != body {
		t.Fatalf("racer's object was overwritten (policy rename): %.60q", got)
	}
	if up.Status != app.StatusSucceeded {
		t.Fatalf("rename should recover from the lost race:%s", describeTasks([]app.Task{up}, items))
	}
	if got := a.get(t, bucket, "race/rename (1).bin", ""); sum(got) != sum(big) {
		t.Fatalf("renamed upload content mismatch; keys %v", a.keys(t, bucket, "race/"))
	}
	if left, _ := a.svc.MultipartUploads(a.ctx, a.loc(bucket, "race/")); len(left) != 0 {
		t.Errorf("lost race left unfinished multipart uploads: %+v", left)
	}
}

// ---- compression rules --------------------------------------------------------------

func TestAppCompressionRulesRoundTrip(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	if err := a.svc.SetSecret("zip-pass", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	err := a.svc.SaveUploadRules([]rules.UploadRule{
		{Name: "gz", KeyPattern: "gz/*", Compression: &rules.Compression{Method: "gzip", Level: 6}},
		{Name: "zip", KeyPattern: "zip/*", Compression: &rules.Compression{Method: "zip", Level: 6}},
		{Name: "aes", KeyPattern: "aes/*", Compression: &rules.Compression{Method: "zip", Level: 6, PasswordRef: "zip-pass"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("Pail compressible line of text 0123456789\n", 40000)) // ~1.7 MB
	src := t.TempDir()
	writeLocal(t, filepath.Join(src, "doc.txt"), body)

	for _, c := range []struct{ prefix, key, enc, marker string }{
		{"gz/", "gz/doc.txt", "gzip", "gzip"},
		{"zip/", "zip/doc.txt.zip", "", "zip"},
		{"aes/", "aes/doc.txt.zip", "", "aes-zip"},
	} {
		g, err := a.svc.Upload(app.UploadRequest{Dest: a.loc(bucket, c.prefix), Paths: []string{filepath.Join(src, "doc.txt")}})
		if err != nil {
			t.Fatal(err)
		}
		a.groupOK(t, g)
		h := a.head(t, bucket, c.key)
		if aws.ToString(h.ContentEncoding) != c.enc {
			t.Errorf("%s: Content-Encoding %q, want %q", c.key, aws.ToString(h.ContentEncoding), c.enc)
		}
		if h.Metadata["pail-compression"] != c.marker && !anyMetaValue(h.Metadata, c.marker) {
			t.Errorf("%s: compression marker %q not found in metadata %v", c.key, c.marker, h.Metadata)
		}
		if aws.ToInt64(h.ContentLength) >= int64(len(body)) {
			t.Errorf("%s: stored %d bytes, not smaller than the %d byte original", c.key, aws.ToInt64(h.ContentLength), len(body))
		}
		raw := a.get(t, bucket, c.key, "")
		if sum(raw) == sum(body) {
			t.Errorf("%s: stored bytes equal the original; nothing was compressed", c.key)
		}

		// Auto decode restores the original byte for byte.
		dir := t.TempDir()
		g, err = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, c.prefix), Keys: []string{c.key}, DestDir: dir, Decode: app.DecodeAuto, PasswordRef: "zip-pass"})
		if err != nil {
			t.Fatal(err)
		}
		a.groupOK(t, g)
		files := localFiles(t, dir)
		if p, ok := hasFileWithSHA(files, sum(body)); !ok {
			t.Errorf("%s: auto-decoded download does not match the original; files %v", c.key, files)
		} else {
			t.Logf("%s decoded to %q", c.key, p)
		}

		// Raw download keeps the stored bytes.
		dir = t.TempDir()
		g, _ = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, c.prefix), Keys: []string{c.key}, DestDir: dir})
		a.groupOK(t, g)
		if _, ok := hasFileWithSHA(localFiles(t, dir), sum(raw)); !ok {
			t.Errorf("%s: raw download differs from the stored object", c.key)
		}
	}
}

func anyMetaValue(m map[string]string, want string) bool {
	for _, v := range m {
		if v == want {
			return true
		}
	}
	return false
}

// ---- checksum compatibility ---------------------------------------------------------

// recordingProxy forwards to the MinIO endpoint and records which checksum
// related headers the SDK sent. Requests are signed for the proxy's host, which
// the forwarded Host header preserves.
type recordingProxy struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen map[string]int
}

func newRecordingProxy(t *testing.T, target string) *recordingProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := &recordingProxy{seen: map[string]int{}}
	proxy := httputil.NewSingleHostReverseProxy(u)
	rp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			rp.mu.Lock()
			for name := range r.Header {
				ln := strings.ToLower(name)
				if strings.HasPrefix(ln, "x-amz-checksum") || ln == "x-amz-sdk-checksum-algorithm" || ln == "x-amz-trailer" {
					rp.seen[ln]++
				}
			}
			if r.Header.Get("X-Amz-Content-Sha256") == "STREAMING-UNSIGNED-PAYLOAD-TRAILER" {
				rp.seen["streaming-trailer-payload"]++
			}
			rp.mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(rp.srv.Close)
	return rp
}

func (rp *recordingProxy) summary() string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.seen) == 0 {
		return "no checksum headers"
	}
	var parts []string
	for k, v := range rp.seen {
		parts = append(parts, fmt.Sprintf("%s x%d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// TestAppChecksumModesCustomEndpoint uploads small and multipart payloads through
// the app with every account ChecksumMode (the default "" = auto included) and
// logs which x-amz-checksum headers the SDK sent. MinIO must accept all of them.
func TestAppChecksumModesCustomEndpoint(t *testing.T) {
	cfg := localConfig(t)
	small, big := []byte("checksum compatibility"), payload(3, 11*1024*1024)
	for _, mode := range []string{"", "auto", "when_required", "when_supported"} {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			rp := newRecordingProxy(t, cfg.Endpoint)
			a := newAppEnvAt(t, rp.srv.URL, mode)
			bucket := a.bucket(t, false)
			src := t.TempDir()
			writeLocal(t, filepath.Join(src, "s.txt"), small)
			writeLocal(t, filepath.Join(src, "b.bin"), big)
			g, err := a.svc.Upload(app.UploadRequest{Dest: a.loc(bucket, ""), Paths: []string{filepath.Join(src, "s.txt"), filepath.Join(src, "b.bin")}})
			if err != nil {
				t.Fatal(err)
			}
			a.groupOK(t, g)
			if sum(a.get(t, bucket, "s.txt", "")) != sum(small) || sum(a.get(t, bucket, "b.bin", "")) != sum(big) {
				t.Fatal("content mismatch")
			}
			dir := t.TempDir()
			g, _ = a.svc.Download(app.DownloadRequest{Source: a.loc(bucket, ""), WholeBucket: true, DestDir: dir})
			a.groupOK(t, g)
			if f := localFiles(t, dir); f["b.bin"] != sum(big) || f["s.txt"] != sum(small) {
				t.Fatalf("download mismatch: %v", f)
			}
			t.Logf("ChecksumMode %q: upload and download OK; request headers seen by the server: %s", mode, rp.summary())
		})
	}
}

// ---- multipart upload management ---------------------------------------------------

func TestAppMultipartUploadsListAndAbort(t *testing.T) {
	a := newApp(t)
	bucket := a.bucket(t, false)
	ids := map[string]string{}
	for _, k := range []string{"mp/a.bin", "mp/sub/b.bin", "other/c.bin"} {
		out, err := a.sdk.S3().CreateMultipartUpload(a.ctx, &s3.CreateMultipartUploadInput{Bucket: &bucket, Key: aws.String(k)})
		if err != nil {
			t.Fatal(err)
		}
		ids[k] = aws.ToString(out.UploadId)
	}
	all, err := a.svc.MultipartUploads(a.ctx, a.loc(bucket, ""))
	if err != nil || len(all) != 3 {
		t.Fatalf("MultipartUploads(bucket) = %+v, %v", all, err)
	}
	for _, u := range all {
		if ids[u.Key] != u.UploadID || u.Initiated.IsZero() {
			t.Errorf("unexpected upload entry %+v", u)
		}
	}
	mp, err := a.svc.MultipartUploads(a.ctx, a.loc(bucket, "mp/"))
	if err != nil {
		t.Fatal(err)
	}
	// MinIO ignores folder prefixes on this call; the app filters client-side.
	if len(mp) != 2 {
		t.Fatalf("expected two uploads below mp/, got %+v", mp)
	}
	id, err := a.svc.AbortMultipartUploads(a.loc(bucket, "mp/"), mp)
	if err != nil {
		t.Fatal(err)
	}
	items := a.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 2 {
		t.Fatalf("abort items:%s", describeTasks(nil, items))
	}
	rest, _ := a.svc.MultipartUploads(a.ctx, a.loc(bucket, ""))
	if len(rest) != 1 || rest[0].Key != "other/c.bin" {
		t.Fatalf("after abort: %+v", rest)
	}
}
