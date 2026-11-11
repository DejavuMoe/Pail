package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestUploadCreateOnlyDoesNotClaimDifferentBody(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		name, data, match := "single", []byte("ours"), isPut("r.bin")
		if multipart {
			name, data, match = "multipart", trBigData(6<<20), isComplete
		}
		for _, policy := range []ConflictPolicy{ConflictSkip, ConflictRename} {
			t.Run(name+"/"+string(policy), func(t *testing.T) {
				e := newTrEnv(t)
				if err := e.s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
					t.Fatal(err)
				}
				src := filepath.Join(t.TempDir(), "r.bin")
				trWrite(t, src, data)
				other := bytes.Clone(data)
				other[0] ^= 1
				meta := map[string]string{mtimeMetaKey: mTimeMeta(t, src)}
				e.srv.Inject(trRaceOnce(match, func() { e.srv.PutObject("b", "r.bin", other, "", meta) }))
				tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: policy})
				up := trKind(tasks, KindUpload)[0]
				if policy == ConflictSkip {
					if up.Status != StatusFailed || !strings.Contains(up.Err, "nothing was overwritten") {
						t.Errorf("same size and metadata do not prove commit: %+v, items %+v", up, e.s.TaskItems(up.ID))
					}
				} else {
					it := e.s.TaskItems(up.ID)
					if up.Status != StatusSucceeded || len(it) != 1 || it[0].Status != ItemSucceeded || it[0].Key != "r (1).bin" {
						t.Errorf("rename must save our body under a free name: %+v, items %+v", up, it)
					}
					if got := trObject(t, e, "r (1).bin"); got != string(data) {
						t.Fatal("numbered object does not contain our bytes")
					}
				}
				if got := trObject(t, e, "r.bin"); got != string(other) {
					t.Fatal("racing object was overwritten")
				}
			})
		}
	}
}

func TestUploadRenameRequiresIdenticalContent(t *testing.T) {
	for _, method := range []string{"plain", "gzip", "zip", "aes-zip"} {
		t.Run(method, func(t *testing.T) {
			e := newTrEnv(t)
			key, password := "r.txt", ""
			if method != "plain" {
				comp := &rules.Compression{Method: method}
				if method == "zip" || method == "aes-zip" {
					key += ".zip"
				}
				if method == "aes-zip" {
					password = "zip-password"
					comp.Method, comp.PasswordRef = "zip", password
					if err := e.s.SetSecret(password, "test-password"); err != nil {
						t.Fatal(err)
					}
				}
				if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: method, Compression: comp}}); err != nil {
					t.Fatal(err)
				}
			}
			src := filepath.Join(t.TempDir(), "r.txt")
			trWrite(t, src, []byte("ours"))
			trChtimes(t, src, syBase)
			req := UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: ConflictRename}
			trExpect(t, e.upload(req), StatusSucceeded)
			original := trObject(t, e, key)
			tasks := e.upload(req)
			trExpect(t, tasks, StatusSucceeded)
			it := e.s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(it) != 1 || it[0].Status != ItemSkipped || it[0].Key != key {
				t.Fatalf("identical rescan must not duplicate: %+v", it)
			}
			trMissing(t, e, numberedKey(key, 1))
			trWrite(t, src, []byte("diff"))
			trChtimes(t, src, syBase)
			tasks = e.upload(req)
			trExpect(t, tasks, StatusSucceeded)
			it = e.s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(it) != 1 || it[0].Status != ItemSucceeded || it[0].Key != numberedKey(key, 1) {
				t.Fatalf("different content with same size/mtime must be numbered: %+v", it)
			}
			if trObject(t, e, key) != original {
				t.Fatal("rename overwrote the original")
			}
			dest := t.TempDir()
			trExpect(t, e.download(DownloadRequest{Source: e.loc("b", ""), Keys: []string{numberedKey(key, 1)}, DestDir: dest, Decode: DecodeAuto, PasswordRef: password}), StatusSucceeded)
			if got := trRead(t, filepath.Join(dest, numberedKey(key, 1))); got != "diff" {
				t.Fatalf("numbered upload content = %q", got)
			}
		})
	}
}

func TestUploadCreateOnlyRecoversLostCommitResponse(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		name, data, match := "single", []byte("ours"), isPut("r.bin")
		if multipart {
			name, data, match = "multipart", trBigData(6<<20), isComplete
		}
		t.Run(name, func(t *testing.T) {
			srv := s3test.New()
			t.Cleanup(srv.Close)
			srv.CreateBucket("b", false)
			target, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			var lost atomic.Bool
			proxy.ModifyResponse = func(resp *http.Response) error {
				if resp.StatusCode == http.StatusOK && match(resp.Request) && lost.CompareAndSwap(false, true) {
					resp.Body.Close()
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadGateway) }
			ps := httptest.NewServer(proxy)
			t.Cleanup(ps.Close)
			s := newTestService(t, nil)
			acc, err := s.SaveAccount(staticAccount("lost-response", ps.URL), &Secrets{SecretAccessKey: "sk"})
			if err != nil {
				t.Fatal(err)
			}
			e := &trEnv{&s3Env{t: t, s: s, srv: srv, acc: acc}}
			if err := s.SaveSettings(Settings{PartSizeMiB: 5}); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "r.bin")
			trWrite(t, src, data)
			tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}, Conflict: ConflictSkip})
			if !lost.Load() {
				t.Fatal("fixture did not discard a successful commit response")
			}
			trExpect(t, tasks, StatusSucceeded)
			it := s.TaskItems(trKind(tasks, KindUpload)[0].ID)
			if len(it) != 1 || it[0].Status != ItemSucceeded || it[0].Err != "committed; response was retried" {
				t.Fatalf("replayed upload: %+v", it)
			}
			if got := trObject(t, e, "r.bin"); got != string(data) {
				t.Fatal("committed body differs")
			}
			trMissing(t, e, "r (1).bin")
		})
	}
}
