package integration_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/app"
)

const extMiB = 1 << 20

// TestExtUploadDownloadRoundTrip: single file, special-character keys, folder with
// an empty sub-folder, a 20 MiB multipart object, then downloads with SHA-256 checks.
func TestExtUploadDownloadRoundTrip(t *testing.T) {
	x := newExt(t, "")
	src := t.TempDir()
	small := []byte("hello from Pail\n")
	big := payload(1, 20*extMiB) // exactly four 5 MiB parts
	x.spend(t, int64(len(big))*2)
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

	g, err := x.svc.Upload(app.UploadRequest{
		Dest:  x.at("up/"),
		Paths: []string{filepath.Join(src, "single.txt"), filepath.Join(src, "big.bin"), filepath.Join(src, "tree"), filepath.Join(src, "special")},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := x.groupOK(t, g)
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
		if got := x.get(t, x.bucket, x.k(k), ""); sum(got) != sum(v) {
			t.Fatalf("%s: sha256 mismatch after upload", k)
		}
	}
	h := x.head(t, x.bucket, x.k("up/big.bin"))
	etag := aws.ToString(h.ETag)
	if !strings.Contains(etag, "-4") {
		t.Errorf("20 MiB upload at 5 MiB parts should be a 4-part multipart object, ETag %s", etag)
	}
	if h.Metadata["pail-mtime"] == "" {
		t.Errorf("pail-mtime metadata missing: %v", h.Metadata)
	}
	if !x.exists(x.bucket, x.k("up/tree/empty/")) {
		t.Errorf("empty local folder did not become a placeholder object; keys: %v", x.relKeys(t, "up/"))
	}
	if left, err := x.svc.MultipartUploads(x.ctx, x.at("")); err != nil || len(left) != 0 {
		t.Errorf("unfinished multipart uploads left behind: %+v (%v)", left, err)
	}

	// The app lists the special keys exactly as stored.
	pg, err := x.svc.List(x.ctx, x.at("up/special/"), app.ListOptions{Flat: true})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range pg.Entries {
		listed[strings.TrimPrefix(e.Key, x.k("up/special/"))] = true
	}
	for name := range special {
		if !listed[name] {
			t.Errorf("List does not report %q exactly; got %v", name, listed)
		}
	}

	// Download: object, folder, special folder.
	d1 := t.TempDir()
	g, err = x.svc.Download(app.DownloadRequest{Source: x.at("up/"), Keys: []string{x.k("up/big.bin")}, DestDir: d1})
	if err != nil {
		t.Fatal(err)
	}
	x.groupOK(t, g)
	if got := localFiles(t, d1); got["big.bin"] != sum(big) || len(got) != 1 {
		t.Fatalf("single download: %v", got)
	}
	d2 := t.TempDir()
	g, _ = x.svc.Download(app.DownloadRequest{Source: x.at("up/"), Keys: []string{x.k("up/tree/")}, DestDir: d2})
	x.groupOK(t, g)
	got := localFiles(t, d2)
	if got["tree/a.txt"] != sum([]byte("tree a")) || got["tree/sub dir/b+c.bin"] != sum(treeB) {
		t.Fatalf("folder download: %v", got)
	}
	if st, err := os.Stat(filepath.Join(d2, "tree", "empty")); err != nil || !st.IsDir() {
		t.Errorf("placeholder folder was not recreated as a directory: %v", err)
	}
	d3 := t.TempDir()
	g, _ = x.svc.Download(app.DownloadRequest{Source: x.at("up/"), Keys: []string{x.k("up/special/")}, DestDir: d3})
	x.groupOK(t, g)
	got = localFiles(t, d3)
	for name, data := range special {
		if p, ok := hasFileWithSHA(got, sum(data)); !ok {
			t.Errorf("special key %q not downloaded intact; files %v", name, got)
		} else if filepath.ToSlash(p) != "special/"+name {
			t.Errorf("special key %q landed at %q", name, p)
		}
	}
	// A prefix-wide download (the whole run folder of this test) equals the sum of the parts.
	d4 := t.TempDir()
	g, _ = x.svc.Download(app.DownloadRequest{Source: x.at(""), Keys: []string{x.k("up/")}, DestDir: d4})
	x.spend(t, int64(len(big)))
	x.groupOK(t, g)
	if f := localFiles(t, d4); f["up/big.bin"] != sum(big) || f["up/single.txt"] != sum(small) {
		t.Errorf("prefix download mismatch: %d files", len(f))
	}
}

// TestExtQuestionMarkKey: a key with "?" (valid in S3, illegal in Windows file names).
func TestExtQuestionMarkKey(t *testing.T) {
	x := newExt(t, "")
	key := "qm/a b+c#?.txt"
	body := []byte("question mark body")
	x.putk(t, key, body)
	pg, err := x.svc.List(x.ctx, x.at("qm/"), app.ListOptions{})
	if err != nil || len(pg.Entries) != 1 || pg.Entries[0].Key != x.k(key) {
		t.Fatalf("List = %+v, %v", pg.Entries, err)
	}
	info, err := x.svc.Head(x.ctx, x.at("qm/"), x.k(key), "")
	if err != nil || info.Size != int64(len(body)) {
		t.Fatalf("Head = %+v, %v", info, err)
	}
	dir := t.TempDir()
	g, err := x.svc.Download(app.DownloadRequest{Source: x.at("qm/"), Keys: []string{x.k(key)}, DestDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ts, items := x.waitGroup(t, g)
	files := localFiles(t, dir)
	t.Logf("download of %q on %s: tasks=%d files=%v%s", key, runtime.GOOS, len(ts), files, describeTasks(ts, items))
	if runtime.GOOS != "windows" {
		if len(files) != 1 || sum(body) != files["a b+c#?.txt"] {
			t.Errorf("on %s the key must download under its exact name: %v", runtime.GOOS, files)
		}
		return
	}
	// Windows cannot hold the name: either a sanitised file with the right bytes, or a
	// failed item with an explanation; never a missing file reported as success.
	switch len(files) {
	case 1:
		for p, s := range files {
			if s != sum(body) {
				t.Errorf("sanitised file %q has wrong content", p)
			}
		}
	case 0:
		if countStatus(items, app.ItemFailed) == 0 || items[0].Err == "" {
			t.Errorf("no file and no explained failure:%s", describeTasks(ts, items))
		}
	default:
		t.Errorf("unexpected files: %v", files)
	}
}

func TestExtUploadConflictPolicies(t *testing.T) {
	x := newExt(t, "")
	x.putk(t, "c/x.txt", []byte("remote original"))
	src := t.TempDir()
	local := filepath.Join(src, "x.txt")
	upload := func(policy app.ConflictPolicy, content string) []app.ItemResult {
		t.Helper()
		if content != "" { // "" re-uploads the file as it is (same mtime)
			writeLocal(t, local, []byte(content))
		}
		g, err := x.svc.Upload(app.UploadRequest{Dest: x.at("c/"), Paths: []string{local}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		return x.groupOK(t, g)
	}
	cur := func() string { return string(x.get(t, x.bucket, x.k("c/x.txt"), "")) }

	items := upload(app.ConflictSkip, "local one")
	if countStatus(items, app.ItemSkipped) != 1 || cur() != "remote original" {
		t.Fatalf("skip must leave the remote object alone:%s", describeTasks(nil, items))
	}
	upload(app.ConflictRename, "local two")
	if cur() != "remote original" {
		t.Fatal("rename overwrote the original")
	}
	if got := x.relKeys(t, "c/"); len(got) != 2 || got[0] != "c/x (1).txt" || got[1] != "c/x.txt" {
		t.Fatalf("rename keys = %v", got)
	}
	if string(x.get(t, x.bucket, x.k("c/x (1).txt"), "")) != "local two" {
		t.Fatal("renamed copy has the wrong content")
	}
	upload(app.ConflictRename, "") // identical re-upload must not create "x (2).txt"
	if got := x.relKeys(t, "c/"); len(got) != 2 {
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

func TestExtDownloadConflictPolicies(t *testing.T) {
	x := newExt(t, "")
	x.putk(t, "d/f.txt", []byte("remote body"))
	dir := t.TempDir()
	local := filepath.Join(dir, "f.txt")
	body := "local body that is different"
	writeLocal(t, local, []byte(body))
	dl := func(p app.ConflictPolicy) []app.ItemResult {
		t.Helper()
		g, err := x.svc.Download(app.DownloadRequest{Source: x.at("d/"), Keys: []string{x.k("d/f.txt")}, DestDir: dir, Conflict: p})
		if err != nil {
			t.Fatal(err)
		}
		return x.groupOK(t, g)
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

// TestExtCreateOnlyRace records whether the server honours If-None-Match: * on
// CompleteMultipartUpload and then runs the race of the MinIO suite: a second client
// writes the destination while the app's multipart upload is in flight.
func TestExtCreateOnlyRace(t *testing.T) {
	x := newExt(t, "")
	s := x.sdk.S3()

	// Probe with raw calls: one tiny part, a racer, then a create-only completion.
	probeKey := x.k("race/probe.bin")
	cmu, err := s.CreateMultipartUpload(x.ctx, &s3.CreateMultipartUploadInput{Bucket: &x.bucket, Key: &probeKey})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	up, err := s.UploadPart(x.ctx, &s3.UploadPartInput{Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("part-one")})
	if err != nil {
		t.Fatalf("UploadPart: %v", err)
	}
	x.putk(t, "race/probe.bin", []byte("racer"))
	_, cerr := s.CompleteMultipartUpload(x.ctx, &s3.CompleteMultipartUploadInput{
		Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId, IfNoneMatch: aws.String("*"),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: up.ETag, PartNumber: aws.Int32(1)}}},
	})
	after := string(x.get(t, x.bucket, probeKey, ""))
	honoured := false
	switch {
	case cerr == nil && after == "part-one":
		t.Logf("RESULT: CompleteMultipartUpload ignores If-None-Match:* (the racer was overwritten)")
	case cerr != nil && after == "racer":
		honoured = true
		t.Logf("RESULT: CompleteMultipartUpload honours If-None-Match:* (%s); the racer survived", errCode(cerr))
	default:
		t.Logf("RESULT: inconclusive: err=%v body=%q", cerr, after)
	}
	if cerr != nil {
		_, _ = s.AbortMultipartUpload(x.ctx, &s3.AbortMultipartUploadInput{Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId})
	}

	src := t.TempDir()
	big := payload(7, 16*extMiB) // four parts: a window between the HEAD and the commit
	race := func(name string, policy app.ConflictPolicy) (app.Task, []app.ItemResult, string) {
		t.Helper()
		x.spend(t, int64(len(big)))
		writeLocal(t, filepath.Join(src, name), big)
		key := x.k("race/" + name)
		stop := make(chan struct{})
		raced := make(chan string, 1)
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				inFlight := false
				for _, tk := range x.svc.Tasks() {
					if tk.Kind == app.KindUpload && tk.Title == "Upload "+name && tk.Status == app.StatusRunning && tk.BytesDone > 0 && tk.BytesDone < tk.BytesTotal {
						inFlight = true
					}
				}
				if inFlight {
					body := "racer wrote " + name
					if _, err := s.PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: aws.String(key), Body: strings.NewReader(body)}); err == nil {
						raced <- body
					}
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
		g, err := x.svc.Upload(app.UploadRequest{Dest: x.at("race/"), Paths: []string{filepath.Join(src, name)}, Conflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		ts, items := x.waitGroup(t, g)
		close(stop)
		var body string
		select {
		case body = <-raced:
		case <-time.After(5 * time.Second):
			t.Fatalf("the racer never found the in-flight upload:%s", describeTasks(ts, items))
		}
		var upTask app.Task
		for _, tk := range ts {
			if tk.Kind == app.KindUpload {
				upTask = tk
			}
		}
		return upTask, items, body
	}

	// skip: create-only.
	tk, items, body := race("skip.bin", app.ConflictSkip)
	got := string(x.get(t, x.bucket, x.k("race/skip.bin"), ""))
	t.Logf("skip race: status=%s final-object=%s%s", tk.Status, map[bool]string{true: "racer", false: "upload/other"}[got == body], describeTasks([]app.Task{tk}, items))
	if honoured {
		if got != body {
			t.Fatalf("racer's object was overwritten although the server honours create-only: %.60q", got)
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
	got = string(x.get(t, x.bucket, x.k("race/rename.bin"), ""))
	t.Logf("rename race: status=%s%s", tk.Status, describeTasks([]app.Task{tk}, items))
	if honoured {
		if got != body {
			t.Fatalf("racer's object was overwritten (policy rename): %.60q", got)
		}
		if tk.Status != app.StatusSucceeded {
			t.Fatalf("rename should recover from the lost race:%s", describeTasks([]app.Task{tk}, items))
		}
		if b := x.get(t, x.bucket, x.k("race/rename (1).bin"), ""); !bytes.Equal(b, big) {
			t.Fatalf("renamed upload content mismatch; keys %v", x.relKeys(t, "race/"))
		}
	} else {
		t.Logf("create-only not enforced by the server: final keys %v", x.relKeys(t, "race/"))
	}
	if left, _ := x.svc.MultipartUploads(x.ctx, x.at("race/")); len(left) != 0 {
		t.Errorf("races left unfinished multipart uploads: %+v", left)
	}
}

// TestExtChecksumModes uploads small and multipart payloads through accounts with every
// ChecksumMode and checks that the server accepts them and the bytes survive.
func TestExtChecksumModes(t *testing.T) {
	extLoad(t) // skip early without environment
	small, big := []byte("checksum compatibility"), payload(3, 6*extMiB)
	for _, mode := range []string{"", "auto", "when_required", "when_supported"} {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			x := newExt(t, mode)
			x.spend(t, int64(len(big))*2)
			src := t.TempDir()
			writeLocal(t, filepath.Join(src, "s.txt"), small)
			writeLocal(t, filepath.Join(src, "b.bin"), big)
			g, err := x.svc.Upload(app.UploadRequest{Dest: x.at("ck/"), Paths: []string{filepath.Join(src, "s.txt"), filepath.Join(src, "b.bin")}})
			if err != nil {
				t.Fatal(err)
			}
			x.groupOK(t, g)
			if sum(x.get(t, x.bucket, x.k("ck/s.txt"), "")) != sum(small) || sum(x.get(t, x.bucket, x.k("ck/b.bin"), "")) != sum(big) {
				t.Fatal("content mismatch")
			}
			dir := t.TempDir()
			g, _ = x.svc.Download(app.DownloadRequest{Source: x.at("ck/"), Keys: []string{x.k("ck/s.txt"), x.k("ck/b.bin")}, DestDir: dir})
			x.groupOK(t, g)
			if f := localFiles(t, dir); f["b.bin"] != sum(big) || f["s.txt"] != sum(small) {
				t.Fatalf("download mismatch: %v", f)
			}
			h := x.head(t, x.bucket, x.k("ck/s.txt"))
			t.Logf("ChecksumMode %q: upload+download OK; server returned checksum headers on HEAD: crc32=%q sha256=%q", mode, aws.ToString(h.ChecksumCRC32), aws.ToString(h.ChecksumSHA256))
		})
	}
}

// TestExtServerChecksums records which explicit checksum algorithms the server accepts
// on PutObject (raw SDK calls below the run prefix).
func TestExtServerChecksums(t *testing.T) {
	x := newExt(t, "when_supported")
	s := x.sdk.S3()
	for _, algo := range []types.ChecksumAlgorithm{types.ChecksumAlgorithmCrc32, types.ChecksumAlgorithmCrc32c, types.ChecksumAlgorithmSha1, types.ChecksumAlgorithmSha256, types.ChecksumAlgorithmCrc64nvme} {
		key := x.k("sum/" + string(algo) + ".txt")
		out, err := s.PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: strings.NewReader("checksummed body"), ChecksumAlgorithm: algo})
		if err != nil {
			t.Logf("PutObject with ChecksumAlgorithm=%s: %s (%v)", algo, errCode(err), err)
			continue
		}
		t.Logf("PutObject with ChecksumAlgorithm=%s: accepted; response checksum crc32=%q crc32c=%q sha1=%q sha256=%q crc64nvme=%q", algo,
			aws.ToString(out.ChecksumCRC32), aws.ToString(out.ChecksumCRC32C), aws.ToString(out.ChecksumSHA1), aws.ToString(out.ChecksumSHA256), aws.ToString(out.ChecksumCRC64NVME))
		g, err := s.GetObject(x.ctx, &s3.GetObjectInput{Bucket: &x.bucket, Key: &key, ChecksumMode: types.ChecksumModeEnabled})
		if err != nil {
			t.Errorf("GetObject with checksum mode on: %v", err)
			continue
		}
		g.Body.Close()
	}
	// A wrong checksum must be refused (integrity actually checked).
	key := x.k("sum/bad.txt")
	_, err := s.PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: strings.NewReader("checksummed body"), ChecksumSHA256: aws.String("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")})
	t.Logf("PutObject with a wrong ChecksumSHA256: %v", map[bool]string{true: "REFUSED (" + errCode(err) + ")", false: "ACCEPTED (integrity not verified)"}[err != nil])
}
