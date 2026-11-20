package integration_test

import (
	"bytes"
	"fmt"
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

// Transfer scenarios shared by TestApp* (MinIO) and TestExt* (external endpoint).

// uploadDownloadRoundTrip: single file, special-character keys, a folder with an
// empty sub-folder and a multipart object of bigSize bytes (parts parts at the
// 5 MiB part size), then downloads with SHA-256 checks.
func uploadDownloadRoundTrip(t *testing.T, tg *target, bigSize, parts int) {
	src := t.TempDir()
	small := []byte("hello from Pail\n")
	big := payload(1, bigSize)
	tg.spend(t, int64(len(big))*2)
	writeLocal(t, filepath.Join(src, "single.txt"), small)
	writeLocal(t, filepath.Join(src, "big.bin"), big)
	writeLocal(t, filepath.Join(src, "tree", "a.txt"), []byte("tree a"))
	treeB := payload(2, 300*1024)
	writeLocal(t, filepath.Join(src, "tree", "sub dir", "b+c.bin"), treeB)
	if err := os.MkdirAll(filepath.Join(src, "tree", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	special := map[string][]byte{
		"a b+c#.txt":            []byte("space plus hash"),
		"中文/文件.txt":             []byte("unicode folder"),
		"emoji-😀.txt":           []byte("emoji"),
		"percent%20x.txt":       []byte("literal percent"),
		"semi;colon&amp=eq.txt": []byte("reserved"),
	}
	for name, data := range special {
		writeLocal(t, filepath.Join(src, "special", filepath.FromSlash(name)), data)
	}

	g, err := tg.svc.Upload(app.UploadRequest{
		Dest:  tg.at("up/"),
		Paths: []string{filepath.Join(src, "single.txt"), filepath.Join(src, "big.bin"), filepath.Join(src, "tree"), filepath.Join(src, "special")},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := tg.groupOK(t, g)
	if n := countStatus(items, app.ItemFailed); n != 0 {
		t.Fatalf("failed items:%s", describeTasks(nil, items))
	}
	want := map[string][]byte{
		"up/single.txt": small, "up/big.bin": big, "up/tree/a.txt": []byte("tree a"), "up/tree/sub dir/b+c.bin": treeB,
	}
	for name, data := range special {
		want["up/special/"+name] = data
	}
	for k, v := range want {
		if got := tg.getk(t, k); sum(got) != sum(v) {
			t.Fatalf("%s: sha256 mismatch after upload", k)
		}
	}
	h := tg.head(t, tg.bucket, tg.k("up/big.bin"))
	if etag := aws.ToString(h.ETag); !strings.Contains(etag, fmt.Sprintf("-%d", parts)) {
		t.Errorf("%d byte upload at 5 MiB parts should be a %d-part multipart object, ETag %s", bigSize, parts, etag)
	}
	if h.Metadata["pail-mtime"] == "" {
		t.Errorf("pail-mtime metadata missing: %v", h.Metadata)
	}
	if !tg.existsk("up/tree/empty/") {
		t.Errorf("empty local folder did not become a placeholder object; keys: %v", tg.relKeys(t, "up/"))
	}
	if left, err := tg.svc.MultipartUploads(tg.ctx, tg.at("")); err != nil || len(left) != 0 {
		t.Errorf("unfinished multipart uploads left behind: %+v (%v)", left, err)
	}

	// The app lists the special keys exactly as stored.
	pg, err := tg.svc.List(tg.ctx, tg.at("up/special/"), app.ListOptions{Flat: true})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range pg.Entries {
		listed[strings.TrimPrefix(e.Key, tg.k("up/special/"))] = true
	}
	for name := range special {
		if !listed[name] {
			t.Errorf("List does not report %q exactly; got %v", name, listed)
		}
	}

	// Download: object, folder, special folder.
	d1 := t.TempDir()
	g, err = tg.svc.Download(app.DownloadRequest{Source: tg.at("up/"), Keys: []string{tg.k("up/big.bin")}, DestDir: d1})
	if err != nil {
		t.Fatal(err)
	}
	tg.groupOK(t, g)
	if got := localFiles(t, d1); got["big.bin"] != sum(big) || len(got) != 1 {
		t.Fatalf("single download: %v", got)
	}
	d2 := t.TempDir()
	g, _ = tg.svc.Download(app.DownloadRequest{Source: tg.at("up/"), Keys: []string{tg.k("up/tree/")}, DestDir: d2})
	tg.groupOK(t, g)
	got := localFiles(t, d2)
	if got["tree/a.txt"] != sum([]byte("tree a")) || got["tree/sub dir/b+c.bin"] != sum(treeB) {
		t.Fatalf("folder download: %v", got)
	}
	if st, err := os.Stat(filepath.Join(d2, "tree", "empty")); err != nil || !st.IsDir() {
		t.Errorf("placeholder folder was not recreated as a directory: %v", err)
	}
	d3 := t.TempDir()
	g, _ = tg.svc.Download(app.DownloadRequest{Source: tg.at("up/"), Keys: []string{tg.k("up/special/")}, DestDir: d3})
	tg.groupOK(t, g)
	got = localFiles(t, d3)
	for name, data := range special {
		if p, ok := hasFileWithSHA(got, sum(data)); !ok {
			t.Errorf("special key %q not downloaded intact; files %v", name, got)
		} else if filepath.ToSlash(p) != "special/"+name {
			t.Errorf("special key %q landed at %q", name, p)
		}
	}

	// A prefix-wide download (the whole folder of this test) equals the sum of the parts.
	d4 := t.TempDir()
	g, _ = tg.svc.Download(app.DownloadRequest{Source: tg.at(""), Keys: []string{tg.k("up/")}, DestDir: d4})
	tg.spend(t, int64(len(big)))
	tg.groupOK(t, g)
	checkFiles(t, "prefix download", localFiles(t, d4), want)

	// Whole-bucket download, only where the test owns the bucket.
	if tg.ownsBucket {
		d5 := t.TempDir()
		g, _ = tg.svc.Download(app.DownloadRequest{Source: tg.at(""), WholeBucket: true, DestDir: d5})
		tg.groupOK(t, g)
		checkFiles(t, "whole-bucket download", localFiles(t, d5), want)
	}
}

// checkFiles requires the local files (relative path -> sha256) to be exactly want.
func checkFiles(t *testing.T, what string, got map[string]string, want map[string][]byte) {
	t.Helper()
	ok := len(got) == len(want)
	for k, v := range want {
		if got[k] != sum(v) {
			ok = false
		}
	}
	if !ok {
		t.Errorf("%s mismatch: %d files, want %d: %v", what, len(got), len(want), got)
	}
}

func uploadConflictPolicies(t *testing.T, tg *target) {
	tg.putk(t, "c/x.txt", []byte("remote original"))
	src := t.TempDir()
	local := filepath.Join(src, "x.txt")
	upload := func(policy app.ConflictPolicy, content string) []app.ItemResult {
		t.Helper()
		if content != "" { // "" re-uploads the file as it is (same mtime)
			writeLocal(t, local, []byte(content))
		}
		g, err := tg.svc.Upload(app.UploadRequest{Dest: tg.at("c/"), Paths: []string{local}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		return tg.groupOK(t, g)
	}
	cur := func() string { return string(tg.getk(t, "c/x.txt")) }

	items := upload(app.ConflictSkip, "local one")
	if countStatus(items, app.ItemSkipped) != 1 || cur() != "remote original" {
		t.Fatalf("skip must leave the remote object alone:%s", describeTasks(nil, items))
	}
	upload(app.ConflictRename, "local two")
	if cur() != "remote original" {
		t.Fatal("rename overwrote the original")
	}
	if got := tg.relKeys(t, "c/"); len(got) != 2 || got[0] != "c/x (1).txt" || got[1] != "c/x.txt" {
		t.Fatalf("rename keys = %v", got)
	}
	if string(tg.getk(t, "c/x (1).txt")) != "local two" {
		t.Fatal("renamed copy has the wrong content")
	}
	upload(app.ConflictRename, "") // identical re-upload must not create "x (2).txt"
	if got := tg.relKeys(t, "c/"); len(got) != 2 {
		t.Fatalf("identical re-upload under rename created copies: %v", got)
	}
	items = upload(app.ConflictDifferentSize, "fifteen bytes!!") // remote has 15 bytes
	if countStatus(items, app.ItemSkipped) != 1 || cur() != "remote original" {
		t.Fatalf("different_size with equal sizes must skip:%s", describeTasks(nil, items))
	}
	upload(app.ConflictDifferentSize, "a much longer local body")
	if cur() != "a much longer local body" {
		t.Fatal("different_size with different sizes must overwrite")
	}

	// newer: compare the local mtime with the server's LastModified (clock skew < 1 h assumed).
	setMtime := func(d time.Duration) {
		t.Helper()
		mt := time.Now().Add(d)
		if err := os.Chtimes(local, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	writeLocal(t, local, []byte("older local"))
	setMtime(-2 * time.Hour)
	items = upload(app.ConflictNewer, "")
	if countStatus(items, app.ItemSkipped) != 1 || cur() != "a much longer local body" {
		t.Fatalf("newer: an older local file must be skipped:%s (server body %q)", describeTasks(nil, items), cur())
	}
	writeLocal(t, local, []byte("newer local"))
	setMtime(2 * time.Hour)
	upload(app.ConflictNewer, "")
	if cur() != "newer local" {
		t.Fatalf("newer: a newer local file must overwrite, remote is %q", cur())
	}

	upload(app.ConflictOverwrite, "overwritten")
	if cur() != "overwritten" {
		t.Fatal("overwrite did not replace the object")
	}
}

func downloadConflictPolicies(t *testing.T, tg *target) {
	tg.putk(t, "d/f.txt", []byte("remote body"))
	dir := t.TempDir()
	local := filepath.Join(dir, "f.txt")
	body := "local body that is different"
	writeLocal(t, local, []byte(body))
	dl := func(p app.ConflictPolicy) []app.ItemResult {
		t.Helper()
		g, err := tg.svc.Download(app.DownloadRequest{Source: tg.at("d/"), Keys: []string{tg.k("d/f.txt")}, DestDir: dir, Conflict: p})
		if err != nil {
			t.Fatal(err)
		}
		return tg.groupOK(t, g)
	}
	dl(app.ConflictSkip)
	if string(readLocal(t, local)) != body {
		t.Fatal("skip overwrote the local file")
	}
	dl(app.ConflictRename)
	if string(readLocal(t, local)) != body || string(readLocal(t, filepath.Join(dir, "f (1).txt"))) != "remote body" {
		t.Fatalf("rename: %v", localFiles(t, dir))
	}
	// different_size: sizes differ (28 vs 11) -> replaced; then equal -> skipped.
	dl(app.ConflictDifferentSize)
	if string(readLocal(t, local)) != "remote body" {
		t.Fatal("different_size with different sizes must replace the local file")
	}
	writeLocal(t, local, []byte("same size!!")) // 11 bytes
	items := dl(app.ConflictDifferentSize)
	if countStatus(items, app.ItemSkipped) != 1 || string(readLocal(t, local)) != "same size!!" {
		t.Fatalf("different_size with equal sizes must skip:%s", describeTasks(nil, items))
	}
	// newer: remote LastModified is "now"; a local file stamped in the future is kept, one in the past replaced.
	future := time.Now().Add(2 * time.Hour)
	_ = os.Chtimes(local, future, future)
	items = dl(app.ConflictNewer)
	if countStatus(items, app.ItemSkipped) != 1 || string(readLocal(t, local)) != "same size!!" {
		t.Fatalf("newer: a newer local file must be kept:%s", describeTasks(nil, items))
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(local, past, past)
	dl(app.ConflictNewer)
	if string(readLocal(t, local)) != "remote body" {
		t.Fatal("newer: an older local file must be replaced")
	}
	writeLocal(t, local, []byte("to be overwritten"))
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

// createOnlyRace writes the destination key from a second client after the app's
// HEAD found it free and before its multipart upload of bigSize bytes commits.
// When the server honours the create-only commit (If-None-Match: *), the commit
// must refuse, the racer's object must survive, and the policy decides what the
// app does about it; otherwise the outcome is only recorded.
func createOnlyRace(t *testing.T, tg *target, bigSize int, honoured bool) {
	s := tg.sdk.S3()
	src := t.TempDir()
	big := payload(7, bigSize)
	race := func(name string, policy app.ConflictPolicy) (app.Task, []app.ItemResult, string) {
		t.Helper()
		tg.spend(t, int64(len(big)))
		writeLocal(t, filepath.Join(src, name), big)
		key := tg.k("race/" + name)
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
				for _, tk := range tg.svc.Tasks() {
					if tk.Kind == app.KindUpload && tk.Title == "Upload "+name && tk.Status == app.StatusRunning && tk.BytesDone > 0 && tk.BytesDone < tk.BytesTotal {
						inFlight = true
					}
				}
				if inFlight {
					body := "racer wrote " + name
					if _, err := s.PutObject(tg.ctx, &s3.PutObjectInput{Bucket: &tg.bucket, Key: aws.String(key), Body: strings.NewReader(body)}); err == nil {
						raced <- body
					}
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
		g, err := tg.svc.Upload(app.UploadRequest{Dest: tg.at("race/"), Paths: []string{filepath.Join(src, name)}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		ts, items := tg.waitGroup(t, g)
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
	tk, items, body := race("skip.bin", app.ConflictSkip)
	got := string(tg.getk(t, "race/skip.bin"))
	t.Logf("skip race: status=%s final-object=%s%s", tk.Status, map[bool]string{true: "racer", false: "upload/other"}[got == body], describeTasks([]app.Task{tk}, items))
	if honoured {
		if got != body {
			t.Fatalf("racer's object was overwritten (policy skip): %.60q%s", got, describeTasks([]app.Task{tk}, items))
		}
		if tk.Status == app.StatusSucceeded && countStatus(items, app.ItemSkipped) == 0 {
			t.Errorf("a lost create-only race must not be reported as a plain success:%s", describeTasks([]app.Task{tk}, items))
		}
	} else if got != body && sum([]byte(got)) != sum(big) {
		t.Errorf("object is neither the racer's nor the upload's content (%d bytes)", len(got))
	} else if got != body {
		t.Logf("SERVER LIMITATION: the create-only guard did not stop the overwrite; the app reported status=%s", tk.Status)
	}

	// rename: the racer keeps the name; with a working guard the upload lands on "name (1).bin".
	tk, items, body = race("rename.bin", app.ConflictRename)
	got = string(tg.getk(t, "race/rename.bin"))
	t.Logf("rename race: status=%s%s", tk.Status, describeTasks([]app.Task{tk}, items))
	if honoured {
		if got != body {
			t.Fatalf("racer's object was overwritten (policy rename): %.60q", got)
		}
		if tk.Status != app.StatusSucceeded {
			t.Fatalf("rename should recover from the lost race:%s", describeTasks([]app.Task{tk}, items))
		}
		if b := tg.getk(t, "race/rename (1).bin"); !bytes.Equal(b, big) {
			t.Fatalf("renamed upload content mismatch; keys %v", tg.relKeys(t, "race/"))
		}
	} else {
		t.Logf("create-only not enforced by the server: final keys %v", tg.relKeys(t, "race/"))
	}
	if left, _ := tg.svc.MultipartUploads(tg.ctx, tg.at("race/")); len(left) != 0 {
		t.Errorf("races left unfinished multipart uploads: %+v", left)
	}
}

// compressionRules: GZIP / ZIP / AES-ZIP upload rules scoped to the target's bucket
// and prefix, then raw and auto-decoded downloads.
func compressionRules(t *testing.T, tg *target) {
	if err := tg.svc.SetSecret("zip-pass", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	err := tg.svc.SaveUploadRules([]rules.UploadRule{
		{Name: "gz", BucketPattern: tg.bucket, KeyPattern: tg.k("gz/*"), Compression: &rules.Compression{Method: "gzip", Level: 6}},
		{Name: "zip", BucketPattern: tg.bucket, KeyPattern: tg.k("zip/*"), Compression: &rules.Compression{Method: "zip", Level: 6}},
		{Name: "aes", BucketPattern: tg.bucket, KeyPattern: tg.k("aes/*"), Compression: &rules.Compression{Method: "zip", Level: 6, PasswordRef: "zip-pass"}},
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
			tg.spend(t, 3*int64(len(body))/10)
			g, err := tg.svc.Upload(app.UploadRequest{Dest: tg.at(c.prefix), Paths: []string{filepath.Join(src, "doc.txt")}})
			if err != nil {
				t.Fatal(err)
			}
			tg.groupOK(t, g)
			h := tg.head(t, tg.bucket, tg.k(c.key))
			raw := tg.getk(t, c.key)
			if tg.external && c.enc == "gzip" && aws.ToString(h.ContentEncoding) == "" && sum(raw) == sum(body) && aws.ToInt64(h.ContentLength) < int64(len(body)) {
				// HEAD says "N compressed bytes, no Content-Encoding" while GET delivers the 1.7 MB original:
				// the proxy in front of the gateway decompresses gzip objects for clients that send
				// Accept-Encoding: identity and strips the header. The app must refuse safely.
				dir := t.TempDir()
				g, err = tg.svc.Download(app.DownloadRequest{Source: tg.at(c.prefix), Keys: []string{tg.k(c.key)}, DestDir: dir, Decode: app.DecodeAuto})
				if err != nil {
					t.Fatal(err)
				}
				ts, items := tg.waitGroup(t, g)
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

			// Auto decode restores the original byte for byte.
			dir := t.TempDir()
			g, err = tg.svc.Download(app.DownloadRequest{Source: tg.at(c.prefix), Keys: []string{tg.k(c.key)}, DestDir: dir, Decode: app.DecodeAuto, PasswordRef: "zip-pass"})
			if err != nil {
				t.Fatal(err)
			}
			tg.groupOK(t, g)
			if p, ok := hasFileWithSHA(localFiles(t, dir), sum(body)); !ok {
				t.Errorf("%s: auto-decoded download does not match the original; files %v", c.key, localFiles(t, dir))
			} else {
				t.Logf("%s: stored %d bytes (original %d), decoded to %q", c.key, aws.ToInt64(h.ContentLength), len(body), p)
			}

			// Raw download keeps the stored bytes.
			dir = t.TempDir()
			g, _ = tg.svc.Download(app.DownloadRequest{Source: tg.at(c.prefix), Keys: []string{tg.k(c.key)}, DestDir: dir})
			tg.groupOK(t, g)
			if _, ok := hasFileWithSHA(localFiles(t, dir), sum(raw)); !ok {
				t.Errorf("%s: raw download differs from the stored object", c.key)
			}
		})
	}
}

// checksumModeRoundTrip uploads a small and a multipart payload through the
// target's account (the caller picks its ChecksumMode), then downloads both and
// compares the bytes.
func checksumModeRoundTrip(t *testing.T, tg *target, small, big []byte) {
	tg.spend(t, int64(len(big))*2)
	src := t.TempDir()
	writeLocal(t, filepath.Join(src, "s.txt"), small)
	writeLocal(t, filepath.Join(src, "b.bin"), big)
	g, err := tg.svc.Upload(app.UploadRequest{Dest: tg.at("ck/"), Paths: []string{filepath.Join(src, "s.txt"), filepath.Join(src, "b.bin")}})
	if err != nil {
		t.Fatal(err)
	}
	tg.groupOK(t, g)
	if sum(tg.getk(t, "ck/s.txt")) != sum(small) || sum(tg.getk(t, "ck/b.bin")) != sum(big) {
		t.Fatal("content mismatch")
	}
	dir := t.TempDir()
	req, rel := app.DownloadRequest{Source: tg.at("ck/"), Keys: []string{tg.k("ck/s.txt"), tg.k("ck/b.bin")}, DestDir: dir}, ""
	if tg.ownsBucket {
		req, rel = app.DownloadRequest{Source: tg.at(""), WholeBucket: true, DestDir: dir}, "ck/"
	}
	g, _ = tg.svc.Download(req)
	tg.groupOK(t, g)
	if f := localFiles(t, dir); f[rel+"b.bin"] != sum(big) || f[rel+"s.txt"] != sum(small) {
		t.Fatalf("download mismatch: %v", f)
	}
	h := tg.head(t, tg.bucket, tg.k("ck/s.txt"))
	t.Logf("upload+download OK; server returned checksum headers on HEAD: crc32=%q sha256=%q", aws.ToString(h.ChecksumCRC32), aws.ToString(h.ChecksumSHA256))
}

// multipartListAndAbort creates dangling uploads with the raw SDK, lists them
// through the app and aborts them through the app.
func multipartListAndAbort(t *testing.T, tg *target) {
	ids := map[string]string{}
	for _, k := range []string{"mp/a.bin", "mp/sub/b.bin", "other/c.bin"} {
		out, err := tg.sdk.S3().CreateMultipartUpload(tg.ctx, &s3.CreateMultipartUploadInput{Bucket: &tg.bucket, Key: aws.String(tg.k(k))})
		if err != nil {
			t.Fatal(err)
		}
		ids[tg.k(k)] = aws.ToString(out.UploadId)
	}
	all, err := tg.svc.MultipartUploads(tg.ctx, tg.at(""))
	if err != nil || len(all) != 3 {
		t.Fatalf("MultipartUploads(target root) = %+v, %v", all, err)
	}
	for _, u := range all {
		if ids[u.Key] != u.UploadID || u.Initiated.IsZero() {
			t.Errorf("unexpected upload entry %+v", u)
		}
	}
	// MinIO ignores folder prefixes on this call; the app filters client-side.
	mp, err := tg.svc.MultipartUploads(tg.ctx, tg.at("mp/"))
	if err != nil || len(mp) != 2 {
		t.Fatalf("expected two uploads below mp/, got %+v (%v)", mp, err)
	}
	id, err := tg.svc.AbortMultipartUploads(tg.at("mp/"), mp)
	if err != nil {
		t.Fatal(err)
	}
	items := tg.taskOK(t, id)
	if countStatus(items, app.ItemSucceeded) != 2 {
		t.Fatalf("abort items:%s", describeTasks(nil, items))
	}
	rest, _ := tg.svc.MultipartUploads(tg.ctx, tg.at(""))
	if len(rest) != 1 || rest[0].Key != tg.k("other/c.bin") {
		t.Fatalf("after abort: %+v", rest)
	}
}
