package app

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestUploadWithRules(t *testing.T) {
	e := newTrEnv(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	trWrite(t, src, []byte("hello rules"))
	mt := time.Date(2024, 5, 6, 7, 8, 9, 123456700, time.UTC) // NTFS keeps 100 ns
	trChtimes(t, src, mt)

	if err := e.s.SaveUploadRules([]rules.UploadRule{{
		Name: "txt", BucketPattern: "b", KeyPattern: "*.txt",
		Headers:      map[string]string{"Cache-Control": "max-age=60", "Content-Language": "de"},
		Metadata:     map[string]string{"Team": "storage"},
		StorageClass: "STANDARD_IA",
		SSE:          &rules.Encryption{Type: "AES256"},
	}}); err != nil {
		t.Fatal(err)
	}
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", "p/"), Paths: []string{src}}), StatusSucceeded)

	info, err := e.s.Head(testCtx(t), e.loc("b", ""), "p/a.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Headers[4].Value; got != "max-age=60" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := info.Headers[2].Value; got != "de" {
		t.Errorf("Content-Language = %q", got)
	}
	if info.Metadata["team"] != "storage" {
		t.Errorf("metadata = %v", info.Metadata)
	}
	if info.StorageClass != "STANDARD_IA" || info.SSE != "AES256" {
		t.Errorf("class %q sse %q", info.StorageClass, info.SSE)
	}
	got, err := time.Parse(time.RFC3339Nano, info.Metadata[mtimeMetaKey])
	if err != nil || !got.Equal(mt) {
		t.Errorf("%s = %q (%v), want %v", mtimeMetaKey, info.Metadata[mtimeMetaKey], err, mt)
	}

	// NoRules uploads plain: no rule headers, but the mtime metadata stays.
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", "n/"), Paths: []string{src}, NoRules: true}), StatusSucceeded)
	info, err = e.s.Head(testCtx(t), e.loc("b", ""), "n/a.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Headers[4].Value != "" || info.SSE != "" || info.StorageClass != "STANDARD" || info.Metadata["team"] != "" {
		t.Errorf("NoRules upload still carries rule output: %+v", info)
	}
	if info.Metadata[mtimeMetaKey] == "" {
		t.Error("pail-mtime missing with NoRules")
	}
}

func TestUploadSSECNeedsHTTPS(t *testing.T) {
	e := newTrEnv(t) // the s3test endpoint is plain http
	src := filepath.Join(t.TempDir(), "s.txt")
	trWrite(t, src, []byte("secret"))
	if err := e.s.SetSecret("k", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "c", SSE: &rules.Encryption{Type: "SSE-C", CustomerKeyRef: "k"}}}); err != nil {
		t.Fatal(err)
	}
	tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}})
	up := trKind(tasks, KindUpload)
	if len(up) != 1 || up[0].Status != StatusFailed || !strings.Contains(up[0].Err, "https") {
		t.Fatalf("want failed upload mentioning https, got %+v", up)
	}
	trMissing(t, e, "s.txt")
}

func TestUploadCompression(t *testing.T) {
	const content = "the quick brown fox jumps over the lazy dog\n"
	for _, tc := range []struct {
		name     string
		comp     rules.Compression
		key      string
		encoding string
		decode   DecodeMode
		pwRef    string
	}{
		{"gzip", rules.Compression{Method: "gzip", Level: -1}, "d.txt", "gzip", DecodeGzip, ""},
		{"zip", rules.Compression{Method: "zip", Level: -1}, "d.txt.zip", "", DecodeZip, ""},
		{"aes-zip", rules.Compression{Method: "zip", Level: -1, PasswordRef: "zpw"}, "d.txt.zip", "", DecodeAuto, "zpw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SetSecret("zpw", "pa55word"); err != nil {
				t.Fatal(err)
			}
			if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "c", Compression: &tc.comp}}); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "d.txt")
			trWrite(t, src, []byte(content))
			tmpBefore := trSpoolFiles(t)
			trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}}), StatusSucceeded)
			if after := trSpoolFiles(t); after != tmpBefore {
				t.Errorf("spool files before %d, after %d", tmpBefore, after)
			}
			raw, ok := e.srv.Object("b", tc.key)
			if !ok {
				t.Fatalf("object %q missing; have %v", tc.key, e.srv.Buckets())
			}
			if string(raw) == content {
				t.Fatal("object was stored uncompressed")
			}
			info, err := e.s.Head(testCtx(t), e.loc("b", ""), tc.key, "")
			if err != nil {
				t.Fatal(err)
			}
			if enc := info.Headers[1].Value; enc != tc.encoding {
				t.Errorf("Content-Encoding = %q, want %q", enc, tc.encoding)
			}
			if tc.comp.Method == "zip" && info.ContentType != "application/zip" {
				t.Errorf("Content-Type = %q", info.ContentType)
			}
			if tc.comp.Method == "gzip" && info.ContentType != "text/plain; charset=utf-8" {
				t.Errorf("gzip keeps the original Content-Type, got %q", info.ContentType)
			}
			// Round trip through download decoding.
			out := t.TempDir()
			trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{tc.key}, DestDir: out,
				Decode: tc.decode, PasswordRef: tc.pwRef}), StatusSucceeded)
			if got := trRead(t, filepath.Join(out, filepath.FromSlash(tc.key))); got != content {
				t.Fatalf("decoded content = %q", got)
			}
		})
	}
}

// trSpoolFiles counts the upload spool files in the OS temp dir.
func trSpoolFiles(t testing.TB) int {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "pail-upload-*.tmp"))
	return len(m)
}

func trBigData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

func TestUploadMultipart(t *testing.T) {
	e := newTrEnv(t)
	if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
		t.Fatal(err)
	}
	data := trBigData(11<<20 + 123)
	src := filepath.Join(t.TempDir(), "big.bin")
	trWrite(t, src, data)
	tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: ConflictSkip})
	trExpect(t, tasks, StatusSucceeded)
	if got, _ := e.srv.Object("b", "big.bin"); !bytes.Equal(got, data) {
		t.Fatal("content mismatch")
	}
	up := trKind(tasks, KindUpload)[0]
	if up.BytesDone != int64(len(data)) || up.BytesTotal != int64(len(data)) {
		t.Errorf("bytes %d/%d, want %d", up.BytesDone, up.BytesTotal, len(data))
	}
	var multipart, completeCond bool
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPost && strings.Contains(r.RawQuery, "uploads") {
			multipart = true
		}
		if r.Method == http.MethodPost && strings.Contains(r.RawQuery, "uploadId") && r.Header.Get("If-None-Match") == "*" {
			completeCond = true
		}
	}
	if !multipart {
		t.Error("no multipart upload was started")
	}
	if !completeCond {
		t.Error("CompleteMultipartUpload did not carry If-None-Match: *")
	}
}

// trRaceOnce returns an Inject function that runs act the first time match is
// true, before the request is handled.
func trRaceOnce(match func(r *http.Request) bool, act func()) func(*http.Request) *s3test.Fault {
	var done atomic.Bool
	return func(r *http.Request) *s3test.Fault {
		if match(r) && done.CompareAndSwap(false, true) {
			act()
		}
		return nil
	}
}

func isComplete(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Query().Has("uploadId")
}

func isPut(key string) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == http.MethodPut && !r.URL.Query().Has("partNumber") && strings.HasSuffix(r.URL.Path, "/"+key)
	}
}

func TestUploadCreateOnlyRace(t *testing.T) {
	small := []byte("ours")
	big := trBigData(6 << 20)
	for _, tc := range []struct {
		name     string
		data     []byte
		match    func(*http.Request) bool
		policy   ConflictPolicy
		wantTask Status
		wantKeys map[string]string // key -> content ("" content check skipped for big)
	}{
		{"single skip", small, isPut("r.bin"), ConflictSkip, StatusFailed, map[string]string{"r.bin": "racer"}},
		{"multipart skip", big, isComplete, ConflictSkip, StatusFailed, map[string]string{"r.bin": "racer"}},
		{"single rename", small, isPut("r.bin"), ConflictRename, StatusSucceeded, map[string]string{"r.bin": "racer", "r (1).bin": "ours"}},
		{"multipart rename", big, isComplete, ConflictRename, StatusSucceeded, map[string]string{"r.bin": "racer", "r (1).bin": string(big)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			e.srv.Inject(trRaceOnce(tc.match, func() { e.srv.PutObject("b", "r.bin", []byte("racer"), "", nil) }))
			src := filepath.Join(t.TempDir(), "r.bin")
			trWrite(t, src, tc.data)
			tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: tc.policy})
			up := trKind(tasks, KindUpload)
			if len(up) != 1 || up[0].Status != tc.wantTask {
				t.Fatalf("upload task: %+v", up)
			}
			if tc.wantTask == StatusFailed {
				if !strings.Contains(up[0].Err, "nothing was overwritten") {
					t.Errorf("err = %q", up[0].Err)
				}
				// Retry resolves the conflict again and sees the object: skipped, not failed.
				id, err := e.s.Retry(up[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if tk := wait(t, e.s, id); tk.Status != StatusSucceeded {
					t.Errorf("retry: %+v", tk)
				}
				if it := e.s.TaskItems(id); len(it) != 1 || it[0].Status != ItemSkipped {
					t.Errorf("retry items: %+v", it)
				}
			}
			for k, want := range tc.wantKeys {
				if got := trObject(t, e, k); got != want {
					t.Errorf("%s: content mismatch (len %d, want %d)", k, len(got), len(want))
				}
			}
			if len(e.srv.Versions("b", "r.bin")) != 1 {
				t.Error("r.bin was overwritten")
			}
		})
	}
}

func TestUploadConflictPolicies(t *testing.T) {
	hour := time.Hour
	for _, tc := range []struct {
		name     string
		policy   ConflictPolicy
		content  string
		mtimeOff time.Duration // local mtime relative to now
		want     map[string]string
		item     string
	}{
		{"skip", ConflictSkip, "new content!", 0, map[string]string{"f.txt": "old"}, ItemSkipped},
		{"overwrite", ConflictOverwrite, "new content!", 0, map[string]string{"f.txt": "new content!"}, ItemSucceeded},
		{"rename", ConflictRename, "new content!", 0, map[string]string{"f.txt": "old", "f (1).txt": "new content!"}, ItemSucceeded},
		{"newer, local newer", ConflictNewer, "new content!", hour, map[string]string{"f.txt": "new content!"}, ItemSucceeded},
		{"newer, local older", ConflictNewer, "new content!", -hour, map[string]string{"f.txt": "old"}, ItemSkipped},
		{"different_size, differs", ConflictDifferentSize, "new content!", 0, map[string]string{"f.txt": "new content!"}, ItemSucceeded},
		{"different_size, same", ConflictDifferentSize, "new", 0, map[string]string{"f.txt": "old"}, ItemSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
			src := filepath.Join(t.TempDir(), "f.txt")
			trWrite(t, src, []byte(tc.content))
			trChtimes(t, src, time.Now().Add(tc.mtimeOff))
			tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: tc.policy})
			trExpect(t, tasks, StatusSucceeded)
			for k, want := range tc.want {
				if got := trObject(t, e, k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			items := e.s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(items) != 1 || items[0].Status != tc.item {
				t.Errorf("items = %+v, want status %s", items, tc.item)
			}
		})
	}
}

func TestUploadDefaultPolicyFromSettings(t *testing.T) {
	e := newTrEnv(t) // default policy is rename
	e.srv.PutObject("b", "f.txt", []byte("old"), "", nil)
	src := filepath.Join(t.TempDir(), "f.txt")
	trWrite(t, src, []byte("new"))
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}}), StatusSucceeded)
	if trObject(t, e, "f (1).txt") != "new" || trObject(t, e, "f.txt") != "old" {
		t.Fatal("default policy was not rename")
	}
}

func TestUploadFolder(t *testing.T) {
	e := newTrEnv(t)
	root := t.TempDir()
	trWrite(t, filepath.Join(root, "photos", "a.jpg"), []byte("jpg"))
	trWrite(t, filepath.Join(root, "photos", "sub", "b.txt"), []byte("bee"))
	if err := os.MkdirAll(filepath.Join(root, "photos", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	hasLink := os.Symlink(filepath.Join(root, "photos", "a.jpg"), filepath.Join(root, "photos", "link.jpg")) == nil
	single := filepath.Join(root, "single.txt")
	trWrite(t, single, []byte("one"))

	tasks := e.upload(UploadRequest{Dest: e.loc("b", "a/"), Paths: []string{filepath.Join(root, "photos"), single}})
	trExpect(t, tasks, StatusSucceeded)
	for key, want := range map[string]string{"a/photos/a.jpg": "jpg", "a/photos/sub/b.txt": "bee", "a/photos/empty/": "", "a/single.txt": "one"} {
		if got := trObject(t, e, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	trMissing(t, e, "a/photos/link.jpg")
	trMissing(t, e, "a/photos/sub/")
	if info, err := e.s.Head(testCtx(t), e.loc("b", ""), "a/photos/a.jpg", ""); err != nil || info.ContentType != "image/jpeg" {
		t.Errorf("a.jpg: %v %+v", err, info)
	}
	if hasLink {
		scan := trKind(tasks, KindOther)[0]
		var found bool
		for _, it := range e.s.TaskItems(scan.ID) {
			if strings.HasSuffix(it.Key, "link.jpg") && it.Status == ItemSkipped && strings.Contains(it.Err, "symbolic link") {
				found = true
			}
		}
		if !found {
			t.Errorf("symlink not reported as skipped: %+v", e.s.TaskItems(scan.ID))
		}
	} else {
		t.Log("os.Symlink unavailable; symlink part skipped")
	}
	if n := len(trKind(tasks, KindUpload)); n != 4 {
		t.Errorf("%d upload tasks, want 4", n)
	}
	for _, tk := range trKind(tasks, KindUpload) {
		if tk.GroupID != tasks[0].GroupID {
			t.Error("tasks of one action must share the group")
		}
	}

	// A second run with skip: every file and the folder object already exist.
	again := e.upload(UploadRequest{Dest: e.loc("b", "a/"), Paths: []string{filepath.Join(root, "photos")}, Conflict: ConflictSkip})
	trExpect(t, again, StatusSucceeded)
	for _, it := range trItems(e.s, trKind(again, KindUpload)) {
		if it.Status != ItemSkipped {
			t.Errorf("second run: %+v", it)
		}
	}
}

func TestUploadValidation(t *testing.T) {
	e := newTrEnv(t)
	src := filepath.Join(t.TempDir(), "f.txt")
	trWrite(t, src, []byte("x"))
	for name, req := range map[string]UploadRequest{
		"no paths":        {Dest: e.loc("b", ""), Paths: nil},
		"no bucket":       {Dest: e.loc("", ""), Paths: []string{src}},
		"unknown acct":    {Dest: Location{AccountID: "nope", Bucket: "b"}, Paths: []string{src}},
		"bad prefix":      {Dest: e.loc("b", "x"), Paths: []string{src}},
		"bad policy":      {Dest: e.loc("b", ""), Paths: []string{src}, Conflict: "maybe"},
		"empty path":      {Dest: e.loc("b", ""), Paths: []string{" "}},
		"filesystem root": {Dest: e.loc("b", ""), Paths: []string{filepath.VolumeName(src) + string(filepath.Separator)}},
	} {
		if _, err := e.s.Upload(req); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if len(e.s.Tasks()) != 0 {
		t.Error("rejected requests must not create tasks")
	}
}

func TestUploadMissingPathIsFailedItem(t *testing.T) {
	e := newTrEnv(t)
	tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{filepath.Join(t.TempDir(), "gone.txt")}})
	if len(tasks) != 1 || tasks[0].Status != StatusPartiallyFailed {
		t.Fatalf("%+v", tasks)
	}
	items := e.s.TaskItems(tasks[0].ID)
	if len(items) != 1 || items[0].Status != ItemFailed {
		t.Fatalf("%+v", items)
	}
	// Retry rescans the same root, now that the file exists.
	id0 := items[0].Key
	trWrite(t, id0, []byte("late"))
	id, err := e.s.Retry(tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, e.s, id)
	tk, _ := taskByID(e.s, id)
	for _, x := range trWaitGroup(t, e.s, tk.GroupID) {
		if x.ID != tasks[0].ID && x.Status != StatusSucceeded {
			t.Fatalf("%+v", x)
		}
	}
	if trObject(t, e, "gone.txt") != "late" {
		t.Fatal("retry did not upload the file")
	}
}

func TestUploadCancel(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{{"single", 1 << 20}, {"multipart", 11 << 20}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{}, 8)
			e.srv.Inject(func(r *http.Request) *s3test.Fault {
				if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/c.bin") {
					select {
					case started <- struct{}{}:
					default:
					}
					return &s3test.Fault{Delay: 2 * time.Second}
				}
				return nil
			})
			src := filepath.Join(t.TempDir(), "c.bin")
			trWrite(t, src, trBigData(tc.size))
			g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("upload never reached the server")
			}
			if err := e.s.CancelGroup(g); err != nil {
				t.Fatal(err)
			}
			tasks := trWaitGroup(t, e.s, g)
			up := trKind(tasks, KindUpload)
			if len(up) != 1 || up[0].Status != StatusCanceled {
				t.Fatalf("upload task: %+v", up)
			}
			trMissing(t, e, "c.bin")
			if tc.size > 5<<20 { // the multipart session must be aborted
				eventually(t, "AbortMultipartUpload", func() bool {
					for _, r := range e.srv.Requests() {
						if r.Method == http.MethodDelete && strings.Contains(r.RawQuery, "uploadId") {
							return true
						}
					}
					return false
				})
			}
			if n := trSpoolFiles(t); n != 0 {
				t.Errorf("%d spool files left", n)
			}
		})
	}
}

func TestUploadContentTypePriority(t *testing.T) {
	e := newTrEnv(t)
	if err := e.s.SaveMIMETypes(map[string]string{".txt": "text/x-table", ".zzqq9": "application/x-zzqq"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range []string{"p.txt", "q.zzqq9", "r.unknownext1"} {
		trWrite(t, filepath.Join(dir, f), []byte("x"))
	}
	for _, tc := range []struct {
		name  string
		rules []rules.UploadRule
		file  string
		key   string
		want  string
	}{
		{"table beats extension", nil, "p.txt", "p.txt", "text/x-table"},
		{"table for unknown extension", nil, "q.zzqq9", "q.zzqq9", "application/x-zzqq"},
		{"extension", nil, "r.unknownext1", "r.unknownext1", "application/octet-stream"},
		{"rule beats table", []rules.UploadRule{{Name: "r", Headers: map[string]string{"Content-Type": "text/x-rule"}}}, "p.txt", "p.txt", "text/x-rule"},
		{"zip beats rule", []rules.UploadRule{{Name: "r", Headers: map[string]string{"Content-Type": "text/x-rule"}, Compression: &rules.Compression{Method: "zip", Level: -1}}}, "p.txt", "p.txt.zip", "application/zip"},
		{"gzip keeps table type", []rules.UploadRule{{Name: "r", Compression: &rules.Compression{Method: "gzip", Level: -1}}}, "p.txt", "p.txt", "text/x-table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.srv.DeleteObject("b", tc.key)
			if err := e.s.SaveUploadRules(tc.rules); err != nil {
				t.Fatal(err)
			}
			trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{filepath.Join(dir, tc.file)}, Conflict: ConflictOverwrite}), StatusSucceeded)
			info, err := e.s.Head(testCtx(t), e.loc("b", ""), tc.key, "")
			if err != nil {
				t.Fatal(err)
			}
			if info.ContentType != tc.want {
				t.Errorf("Content-Type = %q, want %q", info.ContentType, tc.want)
			}
		})
	}
}

func TestUploadGzipWithContentEncodingRuleFails(t *testing.T) {
	e := newTrEnv(t)
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "r",
		Headers:     map[string]string{"Content-Encoding": "br"},
		Compression: &rules.Compression{Method: "gzip", Level: -1}}}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "g.txt")
	trWrite(t, src, []byte("x"))
	up := trKind(e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}}), KindUpload)
	if len(up) != 1 || up[0].Status != StatusFailed {
		t.Fatalf("%+v", up)
	}
	trMissing(t, e, "g.txt")
}

func TestUploadedGzipIsValidGzip(t *testing.T) {
	e := newTrEnv(t)
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "r", Compression: &rules.Compression{Method: "gzip", Level: 6}}}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "g.txt")
	trWrite(t, src, []byte(strings.Repeat("abc", 1000)))
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}}), StatusSucceeded)
	raw, _ := e.srv.Object("b", "g.txt")
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if string(b) != strings.Repeat("abc", 1000) {
		t.Fatal("gzip content differs")
	}
}
