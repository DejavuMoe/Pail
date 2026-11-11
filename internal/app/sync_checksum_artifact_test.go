package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestSyncChecksumDownloadArtifactConverges(t *testing.T) {
	for _, method := range []string{"plain", "zip", "gzip"} {
		for _, legacy := range []bool{false, true} {
			name := method + "/new"
			if legacy {
				name = method + "/legacy"
			}
			t.Run(name, func(t *testing.T) {
				e := syNewEnv(t)
				plain := strings.Repeat("hello checksum\n", 50)
				e.write("f.txt", plain, syBase)
				key, rel := "sync/f.txt", "f.txt"
				if method != "plain" {
					if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: method, KeyPattern: "*.txt", Compression: &rules.Compression{Method: method}}}); err != nil {
						t.Fatal(err)
					}
					if method == "zip" {
						key, rel = key+".zip", rel+".zip"
					}
				}
				up := e.job("upload", "new_changed", "checksum")
				trExpect(t, e.run(e.preview(up)), StatusSucceeded)
				syCounts(t, e.preview(up), 0, 0, 0, 1)
				raw := trObject(t, e.trEnv, key)
				info := bkHeadInfo(t, e.s3Env, "b", key)
				logical := sha256.Sum256([]byte(plain))
				if info.Metadata[syMetaSHA] != hex.EncodeToString(logical[:]) {
					t.Fatal("logical checksum format changed")
				}
				if legacy {
					delete(info.Metadata, "pail-body-sha256")
					e.srv.PutObject("b", key, []byte(raw), info.ContentType, info.Metadata)
				}
				job := e.job("download", "new_changed", "checksum")
				job.LocalDir = t.TempDir()
				trExpect(t, e.run(e.preview(job)), StatusSucceeded)
				local := filepath.Join(job.LocalDir, rel)
				if got := trRead(t, local); got != raw {
					t.Fatal("sync must retain the raw stored representation")
				}
				e.srv.ResetRequests()
				var guarded atomic.Bool
				e.srv.Inject(func(r *http.Request) *s3test.Fault {
					if r.Method == http.MethodGet && r.URL.Path == "/b/"+key {
						guarded.Store(r.Header.Get("If-Match") == info.ETag)
					}
					return nil
				})
				syCounts(t, e.preview(job), 0, 0, 0, 1)
				reads := countRequests(e.trEnv, func(r s3test.Request) bool {
					return r.Method == http.MethodGet && r.Path == "/b/"+key
				})
				wantReads := 0
				if legacy && method != "plain" {
					wantReads = 1
					if !guarded.Load() {
						t.Error("legacy digest GET must be pinned with If-Match")
					}
				}
				if reads != wantReads {
					t.Fatalf("checksum preview body GETs = %d, want %d", reads, wantReads)
				}
				changed := []byte(raw)
				changed[0] ^= 1
				trWrite(t, local, changed)
				trChtimes(t, local, syBase)
				syCounts(t, e.preview(job), 0, 1, 0, 0)
			})
		}
	}
}

func TestSyncLegacyCompressedChecksumRejectsChangedRemote(t *testing.T) {
	e := syNewEnv(t)
	raw := trZip(t, "f.txt", "logical bytes")
	key := "sync/f.txt.zip"
	meta := map[string]string{compressionMetaKey: "zip", syMetaSHA: "old-logical-digest", syMetaSize: "13"}
	e.srv.PutObject("b", key, raw, "application/zip", meta)
	e.write("f.txt.zip", string(raw), syBase)
	var raced atomic.Bool
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Path == "/b/"+key && raced.CompareAndSwap(false, true) {
			e.srv.PutObject("b", key, []byte("changed after HEAD"), "application/zip", meta)
		}
		return nil
	})
	pv := e.preview(e.job("download", "new_changed", "checksum"))
	if !raced.Load() || len(pv.Actions) != 1 || pv.Actions[0].Op != "skip" || !strings.Contains(pv.Actions[0].Reason, "cannot read the remote metadata") {
		t.Fatalf("legacy body drift must skip comparison: %+v", pv)
	}
	if got := trRead(t, filepath.Join(e.dir, "f.txt.zip")); got != string(raw) {
		t.Fatal("preview changed local bytes")
	}
}

func TestSyncCompressedChecksumStoresBodyDigest(t *testing.T) {
	e := syNewEnv(t)
	e.write("f.txt", "logical bytes", syBase)
	if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "zip", KeyPattern: "*.txt", Compression: &rules.Compression{Method: "zip"}}}); err != nil {
		t.Fatal(err)
	}
	trExpect(t, e.run(e.preview(e.job("upload", "new_changed", "checksum"))), StatusSucceeded)
	raw := trObject(t, e.trEnv, "sync/f.txt.zip")
	want := sha256.Sum256([]byte(raw))
	h := bkHeadInfo(t, e.s3Env, "b", "sync/f.txt.zip")
	if got := h.Metadata["pail-body-sha256"]; got != hex.EncodeToString(want[:]) {
		t.Fatalf("stored body checksum = %q, want %x", got, want)
	}
}
