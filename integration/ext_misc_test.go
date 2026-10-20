package integration_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/rules"
)

// TestExtPresign: presigned GET (current object, unicode key) fetched with net/http, and a
// presigned PUT uploaded with net/http then verified through the app.
func TestExtPresign(t *testing.T) {
	x := newExt(t, "")
	body := payload(61, 200*1024)
	x.putk(t, "ps/get me.bin", body)
	x.putk(t, "ps/中文 文件.txt", []byte("unicode presign"))

	for key, want := range map[string][]byte{"ps/get me.bin": body, "ps/中文 文件.txt": []byte("unicode presign")} {
		u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k(key), "", "GET", 10*time.Minute)
		if err != nil {
			t.Fatalf("PresignURL GET %q: %v", key, err)
		}
		if strings.Contains(u, x.ecfg.SecretKey) {
			t.Fatal("presigned URL contains the secret key") // would be a serious leak; never print u
		}
		status, got, _, err := httpGet(u, 60*time.Second)
		if err != nil || status != http.StatusOK || !bytes.Equal(got, want) {
			t.Errorf("presigned GET %q: status=%d err=%v bytes-equal=%v", key, status, err, bytes.Equal(got, want))
		}
	}

	// HEAD URL.
	if u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/get me.bin"), "", "HEAD", 5*time.Minute); err != nil {
		t.Errorf("PresignURL HEAD: %v", err)
	} else {
		req, _ := http.NewRequest(http.MethodHead, u, nil)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil || resp.StatusCode != http.StatusOK || resp.ContentLength != int64(len(body)) {
			t.Errorf("presigned HEAD: err=%v resp=%v", err, resp)
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// PUT URL.
	put := []byte("uploaded through a presigned PUT")
	u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/put me.txt"), "", "PUT", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignURL PUT: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(put))
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("presigned PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT status %d", resp.StatusCode)
	}
	info, err := x.svc.Head(x.ctx, x.at("ps/"), x.k("ps/put me.txt"), "")
	if err != nil || info.Size != int64(len(put)) {
		t.Fatalf("Head after presigned PUT = %+v, %v", info, err)
	}
	if string(x.get(t, x.bucket, x.k("ps/put me.txt"), "")) != string(put) {
		t.Error("presigned PUT content mismatch")
	}
	// An expired / tampered signature must be refused (tamper: change the key in the path).
	if bad, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/get me.bin"), "", "GET", 10*time.Minute); err == nil {
		bad = strings.Replace(bad, "get%20me.bin", "put%20me.txt", 1)
		if status, _, _, err := httpGet(bad, 30*time.Second); err == nil && status == http.StatusOK {
			t.Error("a tampered presigned URL was accepted")
		} else {
			t.Logf("tampered presigned URL: status=%d", status)
		}
	}
}

// TestExtMultipartListAndAbort creates dangling uploads below the run prefix with the raw
// SDK, lists them through the app and aborts them through the app.
func TestExtMultipartListAndAbort(t *testing.T) {
	x := newExt(t, "")
	ids := map[string]string{}
	for _, k := range []string{"mp/a.bin", "mp/sub/b.bin", "other/c.bin"} {
		out, err := x.sdk.S3().CreateMultipartUpload(x.ctx, &s3.CreateMultipartUploadInput{Bucket: &x.bucket, Key: aws.String(x.k(k))})
		if err != nil {
			t.Fatal(err)
		}
		ids[x.k(k)] = aws.ToString(out.UploadId)
	}
	all, err := x.svc.MultipartUploads(x.ctx, x.at(""))
	if err != nil || len(all) != 3 {
		t.Fatalf("MultipartUploads(test prefix) = %+v, %v", all, err)
	}
	for _, u := range all {
		if ids[u.Key] != u.UploadID || u.Initiated.IsZero() {
			t.Errorf("unexpected upload entry %+v", u)
		}
	}
	mp, err := x.svc.MultipartUploads(x.ctx, x.at("mp/"))
	if err != nil || len(mp) != 2 {
		t.Fatalf("expected two uploads below mp/, got %+v (%v)", mp, err)
	}
	id, err := x.svc.AbortMultipartUploads(x.at("mp/"), mp)
	if err != nil {
		t.Fatal(err)
	}
	items := x.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 2 {
		t.Fatalf("abort items:%s", describeTasks(nil, items))
	}
	rest, _ := x.svc.MultipartUploads(x.ctx, x.at(""))
	if len(rest) != 1 || rest[0].Key != x.k("other/c.bin") {
		t.Fatalf("after abort: %+v", rest)
	}
}

// TestExtCompressionRules: GZIP / ZIP / AES-ZIP upload rules scoped to this test's prefix,
// then raw and auto-decoded downloads.
func TestExtCompressionRules(t *testing.T) {
	x := newExt(t, "")
	if err := x.svc.SetSecret("zip-pass", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	err := x.svc.SaveUploadRules([]rules.UploadRule{
		{Name: "gz", BucketPattern: x.bucket, KeyPattern: x.k("gz/*"), Compression: &rules.Compression{Method: "gzip", Level: 6}},
		{Name: "zip", BucketPattern: x.bucket, KeyPattern: x.k("zip/*"), Compression: &rules.Compression{Method: "zip", Level: 6}},
		{Name: "aes", BucketPattern: x.bucket, KeyPattern: x.k("aes/*"), Compression: &rules.Compression{Method: "zip", Level: 6, PasswordRef: "zip-pass"}},
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
		t.Run(strings.TrimSuffix(c.prefix, "/"), func(t *testing.T) {
			x.spend(t, 3*int64(len(body))/10)
			g, err := x.svc.Upload(app.UploadRequest{Dest: x.at(c.prefix), Paths: []string{filepath.Join(src, "doc.txt")}})
			if err != nil {
				t.Fatal(err)
			}
			x.groupOK(t, g)
			h := x.head(t, x.bucket, x.k(c.key))
			raw := x.get(t, x.bucket, x.k(c.key), "")
			if c.enc == "gzip" && aws.ToString(h.ContentEncoding) == "" && sum(raw) == sum(body) && aws.ToInt64(h.ContentLength) < int64(len(body)) {
				// HEAD says "N compressed bytes, no Content-Encoding" while GET delivers the 1.7 MB original:
				// the proxy in front of the gateway decompresses gzip objects for clients that send
				// Accept-Encoding: identity and strips the header. The app must refuse safely.
				dir := t.TempDir()
				g, err = x.svc.Download(app.DownloadRequest{Source: x.at(c.prefix), Keys: []string{x.k(c.key)}, DestDir: dir, Decode: app.DecodeAuto})
				if err != nil {
					t.Fatal(err)
				}
				ts, items := x.waitGroup(t, g)
				failed := false
				for _, tk := range ts {
					if tk.Status == app.StatusFailed && tk.Err != "" {
						failed = true
					}
				}
				t.Logf("PROXY DECOMPRESSES GZIP OBJECTS: stored %d bytes (HEAD) vs %d bytes (GET); app download outcome:%s", aws.ToInt64(h.ContentLength), len(raw), describeTasks(ts, items))
				if !failed && len(localFiles(t, dir)) != 0 {
					if _, ok := hasFileWithSHA(localFiles(t, dir), sum(body)); !ok {
						t.Errorf("a wrong file was published: %v", localFiles(t, dir))
					}
				} else if len(localFiles(t, dir)) != 0 {
					t.Errorf("failed download published files: %v", localFiles(t, dir))
				}
				t.Skip("server-side limitation: the proxy decompresses Content-Encoding: gzip objects, so the gzip round trip cannot be verified end to end here")
			}
			if aws.ToString(h.ContentEncoding) != c.enc {
				t.Errorf("%s: Content-Encoding %q, want %q", c.key, aws.ToString(h.ContentEncoding), c.enc)
			}
			if h.Metadata["pail-compression"] != c.marker && !anyMetaValue(h.Metadata, c.marker) {
				t.Errorf("%s: compression marker %q not found in metadata %v", c.key, c.marker, h.Metadata)
			}
			if aws.ToInt64(h.ContentLength) >= int64(len(body)) {
				t.Errorf("%s: stored %d bytes, not smaller than the %d byte original", c.key, aws.ToInt64(h.ContentLength), len(body))
			}
			if sum(raw) == sum(body) {
				t.Errorf("%s: stored bytes equal the original; nothing was compressed", c.key)
			}
			dir := t.TempDir()
			g, err = x.svc.Download(app.DownloadRequest{Source: x.at(c.prefix), Keys: []string{x.k(c.key)}, DestDir: dir, Decode: app.DecodeAuto, PasswordRef: "zip-pass"})
			if err != nil {
				t.Fatal(err)
			}
			x.groupOK(t, g)
			if p, ok := hasFileWithSHA(localFiles(t, dir), sum(body)); !ok {
				t.Errorf("%s: auto-decoded download does not match the original; files %v", c.key, localFiles(t, dir))
			} else {
				t.Logf("%s: stored %d bytes (original %d), decoded to %q", c.key, aws.ToInt64(h.ContentLength), len(body), p)
			}
			dir = t.TempDir()
			g, _ = x.svc.Download(app.DownloadRequest{Source: x.at(c.prefix), Keys: []string{x.k(c.key)}, DestDir: dir})
			x.groupOK(t, g)
			if _, ok := hasFileWithSHA(localFiles(t, dir), sum(raw)); !ok {
				t.Errorf("%s: raw download differs from the stored object", c.key)
			}
		})
	}
}

// TestExtGzipEncodingBehaviour records how the gateway/proxy treats an object stored with
// Content-Encoding: gzip (raw SDK calls): HEAD, GET with identity, GET asking for gzip.
func TestExtGzipEncodingBehaviour(t *testing.T) {
	x := newExt(t, "")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	plain := []byte(strings.Repeat("gzip behaviour probe line\n", 2000))
	_, _ = zw.Write(plain)
	_ = zw.Close()
	key := x.k("gzip/probe.txt")
	x.spend(t, int64(buf.Len()))
	_, err := x.sdk.S3().PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: bytes.NewReader(buf.Bytes()), ContentEncoding: aws.String("gzip"), ContentType: aws.String("text/plain")})
	if err != nil {
		t.Fatal(err)
	}
	h := x.head(t, x.bucket, key)
	t.Logf("stored %d gzip bytes (original %d); HEAD: Content-Length=%d Content-Encoding=%q", buf.Len(), len(plain), aws.ToInt64(h.ContentLength), aws.ToString(h.ContentEncoding))
	raw := x.get(t, x.bucket, key, "")
	t.Logf("SDK GET (Accept-Encoding: identity): %d bytes, equals original=%v, equals stored gzip=%v", len(raw), bytes.Equal(raw, plain), bytes.Equal(raw, buf.Bytes()))
	u, err := x.svc.PresignURL(x.ctx, x.at("gzip/"), key, "", "GET", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := (&http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("presigned GET asking for gzip: status=%d Content-Encoding=%q %d bytes, equals stored gzip=%v", resp.StatusCode, resp.Header.Get("Content-Encoding"), len(b), bytes.Equal(b, buf.Bytes()))
}

// ---- sync ---------------------------------------------------------------------------

func (x *extEnv) syncJob(dir, rel, direction, mode, compare string) app.SyncJob {
	return syncJob(x.appEnv, dir, x.bucket, x.prefix+rel, direction, mode, compare)
}

func TestExtSyncUploadAndMirror(t *testing.T) {
	x := newExt(t, "")
	dir := t.TempDir()
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha"))
	writeLocal(t, filepath.Join(dir, "sub", "b.bin"), payload(31, 200*1024))
	writeLocal(t, filepath.Join(dir, "sub", "deep", "c.txt"), []byte("gamma"))

	job := x.syncJob(dir, "mirror/", "upload", "new_changed", "size_time")
	p := x.preview(t, job)
	if p.Uploads != 3 || p.Deletes != 0 || p.Downloads != 0 {
		t.Fatalf("first preview: %s", previewSummary(p))
	}
	x.runSync(t, p)
	for rel, want := range localFiles(t, dir) {
		if sum(x.get(t, x.bucket, x.k("mirror/"+rel), "")) != want {
			t.Errorf("%s not uploaded correctly", rel)
		}
	}
	if p2 := x.preview(t, job); p2.Uploads != 0 || p2.Deletes != 0 || p2.Downloads != 0 {
		t.Fatalf("second preview must converge: %s", previewSummary(p2))
	}
	writeLocal(t, filepath.Join(dir, "a.txt"), []byte("alpha changed"))
	p = x.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("changed-file preview: %s", previewSummary(p))
	}
	x.runSync(t, p)
	if string(x.get(t, x.bucket, x.k("mirror/a.txt"), "")) != "alpha changed" {
		t.Fatal("changed file not synchronised")
	}
	job.Excludes = []string{"*.bin"}
	writeLocal(t, filepath.Join(dir, "sub", "skipped.bin"), []byte("x"))
	if p = x.preview(t, job); p.Uploads != 0 {
		t.Errorf("excluded file planned: %s", previewSummary(p))
	}
	job.Excludes = nil
	_ = os.Remove(filepath.Join(dir, "sub", "skipped.bin"))

	// Mirror: remote objects without a local file are deleted (inside the sync prefix only).
	x.putk(t, "outside.txt", []byte("outside"))
	x.putk(t, "mirror/extra.txt", []byte("extra"))
	mirror := job
	mirror.Mode = "mirror"
	p = x.preview(t, mirror)
	if p.Deletes != 1 || p.Uploads != 0 {
		t.Fatalf("mirror preview: %s", previewSummary(p))
	}
	if pn := x.preview(t, job); pn.Deletes != 0 {
		t.Errorf("new_changed planned deletes: %s", previewSummary(pn))
	}
	x.runSync(t, p)
	if x.exists(x.bucket, x.k("mirror/extra.txt")) || !x.exists(x.bucket, x.k("outside.txt")) || !x.exists(x.bucket, x.k("mirror/a.txt")) {
		t.Fatalf("mirror result wrong: %v", x.relKeys(t, ""))
	}
	if pf := x.preview(t, mirror); pf.Uploads+pf.Deletes+pf.Downloads != 0 {
		t.Fatalf("mirror did not converge: %s", previewSummary(pf))
	}
}

func TestExtSyncDownload(t *testing.T) {
	x := newExt(t, "")
	files := map[string][]byte{"r/one.txt": []byte("one"), "r/dir/two.bin": payload(41, 150*1024), "r/dir/x/three.txt": []byte("three")}
	for k, v := range files {
		x.putk(t, k, v)
	}
	dir := t.TempDir()
	job := x.syncJob(dir, "r/", "download", "new_changed", "size_time")
	p := x.preview(t, job)
	if p.Downloads != 3 {
		t.Fatalf("download preview: %s", previewSummary(p))
	}
	x.runSync(t, p)
	got := localFiles(t, dir)
	for k, v := range files {
		if got[strings.TrimPrefix(k, "r/")] != sum(v) {
			t.Errorf("%s not downloaded correctly: %v", k, got)
		}
	}
	if p2 := x.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("second download preview must converge: %s", previewSummary(p2))
	}
	x.putk(t, "r/one.txt", []byte("one, but longer"))
	p = x.preview(t, job)
	if p.Downloads != 1 {
		t.Fatalf("remote-change preview: %s", previewSummary(p))
	}
	x.runSync(t, p)
	if string(readLocal(t, filepath.Join(dir, "one.txt"))) != "one, but longer" {
		t.Fatal("remote change not downloaded")
	}
	// Mirror download plans the local delete; executing it would use the real OS trash,
	// so only the plan is verified (the execution is covered by internal/app unit tests).
	writeLocal(t, filepath.Join(dir, "local-only.txt"), []byte("not on the server"))
	mirror := job
	mirror.Mode = "mirror"
	p = x.preview(t, mirror)
	if p.Deletes != 1 || p.Downloads != 0 {
		t.Fatalf("mirror download preview: %s", previewSummary(p))
	}
	for _, ac := range p.Actions {
		if ac.Op == "delete" && ac.Path != "local-only.txt" {
			t.Errorf("unexpected delete %q", ac.Path)
		}
	}
	t.Log("SKIPPED (by design): execution of the local mirror delete (real OS trash)")
}

func TestExtSyncChecksumMode(t *testing.T) {
	x := newExt(t, "")
	dir := t.TempDir()
	file := filepath.Join(dir, "same-size.bin")
	orig := payload(51, 64*1024)
	writeLocal(t, file, orig)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mtime := st.ModTime()
	job := x.syncJob(dir, "ck/", "upload", "new_changed", "checksum")
	p := x.preview(t, job)
	if p.Uploads != 1 {
		t.Fatalf("checksum preview: %s", previewSummary(p))
	}
	x.runSync(t, p)
	if p2 := x.preview(t, job); p2.Uploads+p2.Downloads+p2.Deletes != 0 {
		t.Fatalf("checksum round trip does not converge: %s", previewSummary(p2))
	}
	if sha := x.head(t, x.bucket, x.k("ck/same-size.bin")).Metadata["pail-sha256"]; sha != sum(orig) {
		t.Errorf("remote pail-sha256 = %q, want %s", sha, sum(orig))
	}
	changed := payload(52, 64*1024)
	writeLocal(t, file, changed)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if pc := x.preview(t, job); pc.Uploads != 1 {
		t.Fatalf("checksum mode missed a same-size same-mtime change: %s", previewSummary(pc))
	}
	x.runSync(t, x.preview(t, job))
	if sum(x.get(t, x.bucket, x.k("ck/same-size.bin"), "")) != sum(changed) {
		t.Fatal("changed content was not uploaded")
	}
	if p3 := x.preview(t, job); p3.Uploads != 0 {
		t.Fatalf("no convergence after checksum upload: %s", previewSummary(p3))
	}
	ddir := t.TempDir()
	dj := x.syncJob(ddir, "ck/", "download", "new_changed", "checksum")
	dp := x.preview(t, dj)
	t.Logf("checksum download preview: %s", previewSummary(dp))
	x.runSync(t, dp)
	if got := localFiles(t, ddir)["same-size.bin"]; got != sum(changed) {
		t.Fatalf("checksum download mismatch")
	}
	if dp2 := x.preview(t, dj); dp2.Uploads+dp2.Downloads+dp2.Deletes != 0 {
		t.Fatalf("checksum download does not converge: %s", previewSummary(dp2))
	}
}

// ---- CDN -----------------------------------------------------------------------------

// TestExtCDN: the public smoji reference (one read-only GET) and a unique object of this run
// fetched through the CDN base URL. The CDN link comes from the app (Account.CDNURL).
func TestExtCDN(t *testing.T) {
	x := newExt(t, "")
	if x.ecfg.CDN == "" {
		t.Skip("PAIL_EXT_CDN not set")
	}
	acct := x.acct
	acct.CDNURL = x.ecfg.CDN
	acct.CDNBuckets = []string{x.bucket, "smoji"}
	saved, err := x.svc.SaveAccount(acct, nil)
	if err != nil {
		t.Fatalf("SaveAccount with CDN: %v", err)
	}
	x.acct = saved

	t.Run("smoji reference", func(t *testing.T) {
		// Pure URL computation through the app for the reference bucket, then ONE GET.
		u := x.svc.CDNURL(app.Location{AccountID: x.acct.ID, Bucket: "smoji"}, "smoji.json")
		if u != x.ecfg.CDN+"/smoji/smoji.json" {
			t.Fatalf("CDNURL = %q", u)
		}
		status, body, hdr, err := httpGet(u, 20*time.Second)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		t.Logf("GET %s -> %d, %d bytes, content-type=%q cache-status=%q server=%q", u, status, len(body), hdr.Get("Content-Type"), hdr.Get("Cf-Cache-Status"), hdr.Get("Server"))
		if status != http.StatusOK {
			t.Fatalf("reference object not reachable: %d", status)
		}
		if !json.Valid(body) {
			t.Errorf("reference body is not valid JSON (%d bytes)", len(body))
		}
	})

	t.Run("run object", func(t *testing.T) {
		var rnd [8]byte
		_, _ = rand.Read(rnd[:])
		rel := "cdn/unique-" + hex.EncodeToString(rnd[:]) + " é.txt"
		want := []byte("cdn check " + hex.EncodeToString(rnd[:]))
		x.putk(t, rel, want)
		u := x.svc.CDNURL(x.at(""), x.k(rel))
		if u == "" || !strings.HasPrefix(u, x.ecfg.CDN+"/"+x.bucket+"/pail-e2e/") || strings.Contains(u, " ") {
			t.Fatalf("CDNURL = %q", u)
		}
		var status int
		var body []byte
		var hdr http.Header
		for attempt := 0; attempt < 3; attempt++ { // propagation / first-fetch grace, never retries a cached result
			status, body, hdr, err = httpGet(u, 20*time.Second)
			if err == nil && status == http.StatusOK {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		t.Logf("GET <cdn>/%s/<run key> -> %d, %d bytes, cache-status=%q", x.bucket, status, len(body), hdr.Get("Cf-Cache-Status"))
		switch {
		case status == http.StatusOK && bytes.Equal(body, want):
			t.Logf("CDN MAPPING VERIFIED: a unique object of this run is served through the CDN with identical bytes")
		case status == http.StatusOK:
			t.Errorf("CDN answered 200 with different bytes (%d bytes)", len(body))
		case status == http.StatusForbidden || status == http.StatusNotFound:
			t.Skipf("CDN answered %d for bucket %q: the bucket is not public via the CDN (not a test failure)", status, x.bucket)
		default:
			t.Errorf("unexpected CDN status %d", status)
		}
	})
}
