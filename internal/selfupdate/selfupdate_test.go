package selfupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

func TestPublicKey(t *testing.T) {
	if _, err := updatefmt.ParsePublicKey(PublicKey); err != nil {
		t.Fatal(err)
	}
}

func TestDue(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	now := at("2026-11-25 09:00") // a Wednesday
	for _, c := range []struct {
		p    Policy
		last time.Time
		want bool
	}{
		{PolicyManual, time.Time{}, false},
		{PolicyManual, at("2026-01-01 00:00"), false},
		{PolicyDaily, time.Time{}, true},
		{PolicyDaily, at("2026-11-25 00:01"), false},
		{PolicyDaily, at("2026-11-24 23:59"), true},
		{PolicyDaily, at("2026-11-26 08:00"), true}, // the clock went back
		{PolicyWeekly, time.Time{}, true},
		{PolicyWeekly, at("2026-11-23 00:00"), false}, // Monday
		{PolicyWeekly, at("2026-11-22 23:59"), true},  // Sunday before
		{PolicyWeekly, at("2025-11-25 09:00"), true},  // same week a year before
		{Policy(""), time.Time{}, false},
	} {
		if got := Due(c.p, c.last, now); got != c.want {
			t.Errorf("Due(%q, %v) = %v, want %v", c.p, c.last, got, c.want)
		}
	}
	// A check in another time zone counts in that of now.
	if Due(PolicyDaily, at("2026-11-25 08:30").UTC(), now) {
		t.Error("a check earlier the same day in UTC is due again")
	}
}

type testRelease struct {
	key     ed25519.PrivateKey
	pub     string
	target  string
	files   map[string][]byte // served by name
	sources map[Source]*httptest.Server
}

func newTestRelease(t *testing.T) *testRelease {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := &testRelease{key: key, pub: base64.StdEncoding.EncodeToString(pub), target: "linux-amd64", files: map[string][]byte{}, sources: map[Source]*httptest.Server{}}
	return r
}

// serve starts a source that serves the release's files, or fails every
// request when broken.
func (r *testRelease) serve(t *testing.T, s Source, broken bool) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if broken {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	r.sources[s] = srv
}

func (r *testRelease) updater(current string, s Source) *Updater {
	u := &Updater{Current: current, Source: s, key: r.pub, target: r.target, bases: map[Source]string{}}
	for src, srv := range r.sources {
		u.bases[src] = srv.URL + "/"
	}
	return u
}

func (r *testRelease) sign(b []byte) string {
	sum := sha256.Sum256(b)
	return updatefmt.Sign(r.key, sum[:])
}

// publish adds the archive of the app in dir as version, and its manifest,
// whose URLs point at base.
func (r *testRelease) publish(t *testing.T, base, version, dir string, deltas ...updatefmt.Delta) updatefmt.Manifest {
	t.Helper()
	entries := listDir(t, dir)
	var buf bytes.Buffer
	if err := updatefmt.WriteArchive(&buf, dir, entries); err != nil {
		t.Fatal(err)
	}
	name := ArchiveName(version, r.target)
	r.files[name] = buf.Bytes()
	m := updatefmt.Manifest{Version: version, URL: base + name, Size: int64(buf.Len()), Signature: r.sign(buf.Bytes()), Deltas: deltas}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r.files[ManifestName(r.target)] = b
	return m
}

func (r *testRelease) delta(t *testing.T, base, from, to, oldDir, newDir string) updatefmt.Delta {
	t.Helper()
	var buf bytes.Buffer
	if err := updatefmt.WriteDelta(&buf, from, to, oldDir, newDir, listDir(t, newDir)); err != nil {
		t.Fatal(err)
	}
	name := DeltaName(from, to, r.target)
	r.files[name] = buf.Bytes()
	return updatefmt.Delta{From: from, URL: base + name, Size: int64(buf.Len()), Signature: r.sign(buf.Bytes())}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		if e.Name() != updatefmt.FileList {
			names = append(names, e.Name())
		}
	}
	return names
}

func writeApp(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := updatefmt.WriteFileList(dir, names); err != nil {
		t.Fatal(err)
	}
}

func noRetries(t *testing.T) {
	old := retries
	retries = nil
	t.Cleanup(func() { retries = old })
}

func TestCheckFallsBack(t *testing.T) {
	noRetries(t)
	r := newTestRelease(t)
	r.serve(t, SourceGitHub, true)
	r.serve(t, SourceMirror, false)
	newDir := t.TempDir()
	writeApp(t, newDir, map[string]string{"pail": "new"})
	r.publish(t, r.sources[SourceMirror].URL+"/", "0.7.0", newDir)

	up, err := r.updater("0.6.4", SourceGitHub).check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if up == nil || up.Version != "0.7.0" || up.From != SourceMirror || up.Delta {
		t.Fatalf("update = %+v", up)
	}

	if up, err := r.updater("0.7.0", SourceMirror).check(context.Background()); err != nil || up != nil {
		t.Fatalf("the latest version got %+v, %v", up, err)
	}
	if up, err := r.updater("0.8.0", SourceMirror).check(context.Background()); err != nil || up != nil {
		t.Fatalf("a newer version got %+v, %v", up, err)
	}

	r.publish(t, r.sources[SourceMirror].URL+"/", "0.8.0-beta.1", newDir)
	if up, err := r.updater("0.7.0", SourceMirror).check(context.Background()); err != nil || up != nil {
		t.Fatalf("a prerelease was offered: %+v, %v", up, err)
	}

	r.sources[SourceMirror].Close()
	if _, err := r.updater("0.6.4", SourceMirror).check(context.Background()); err == nil {
		t.Fatal("no error when both sources fail")
	}
}

func TestHTTPSOnly(t *testing.T) {
	u := &Updater{}
	if _, err := u.get(context.Background(), "http://example.com/update.json"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("plain HTTP: %v", err)
	}
}

func readApp(t *testing.T, dir string) map[string]string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got[e.Name()] = string(b)
	}
	return got
}

func TestInstall(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS replaces the app bundle after exit")
	}
	noRetries(t)
	for _, withDelta := range []bool{false, true} {
		t.Run(map[bool]string{false: "archive", true: "delta"}[withDelta], func(t *testing.T) {
			r := newTestRelease(t)
			r.serve(t, SourceGitHub, false)
			r.serve(t, SourceMirror, false)
			base := r.sources[SourceGitHub].URL + "/"

			// Incompressible, so that the delta is smaller than the archive.
			noise := make([]byte, 64<<10)
			rand.Read(noise)
			cli, app := string(noise[:32<<10]), string(noise[32<<10:])
			oldApp := map[string]string{"pail": cli, "pail-desktop": app, "gone.txt": "old"}
			newApp := map[string]string{"pail": cli[:100] + "new" + cli[103:], "pail-desktop": app, "added.txt": "new"}
			work := t.TempDir()
			oldRef, newDir := filepath.Join(work, "old"), filepath.Join(work, "new")
			writeApp(t, oldRef, oldApp)
			writeApp(t, newDir, newApp)
			var deltas []updatefmt.Delta
			if withDelta {
				deltas = append(deltas, r.delta(t, base, "0.6.4", "0.7.0", oldRef, newDir))
			}
			r.publish(t, base, "0.7.0", newDir, deltas...)

			target := filepath.Join(t.TempDir(), "pail")
			writeApp(t, target, oldApp)
			// A user's file in the installation stays.
			if err := os.WriteFile(filepath.Join(target, "mine.txt"), []byte("mine"), 0o644); err != nil {
				t.Fatal(err)
			}

			u := r.updater("0.6.4", SourceGitHub)
			up, err := u.check(context.Background())
			if err != nil || up == nil {
				t.Fatalf("check: %+v, %v", up, err)
			}
			if up.Delta != withDelta {
				t.Fatalf("Delta = %v", up.Delta)
			}
			var last int64
			err = installUpdate(context.Background(), up.open, up.manifest, u.publicKey(), u.Current, target, func(done, total int64) { last = done })
			if err != nil {
				t.Fatal(err)
			}
			if last != up.Size {
				t.Errorf("progress ended at %d of %d", last, up.Size)
			}
			got := readApp(t, target)
			files := updatefmt.ParseFileList([]byte(got[updatefmt.FileList]))
			slices.Sort(files)
			if !slices.Equal(files, []string{"added.txt", "pail", "pail-desktop"}) {
				t.Errorf("file list = %q", files)
			}
			delete(got, updatefmt.FileList)
			want := map[string]string{"mine.txt": "mine"}
			for k, v := range newApp {
				want[k] = v
			}
			if len(got) != len(want) {
				t.Errorf("installed %d entries, want %d: %v", len(got), len(want), keys(got))
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %.20q, want %.20q", k, got[k], v)
				}
			}
			// Nothing is left next to the installation.
			if es, _ := os.ReadDir(filepath.Dir(target)); len(es) != 1 {
				t.Errorf("left next to the app: %v", es)
			}
		})
	}
}

func TestInstallRejectsBadSignature(t *testing.T) {
	noRetries(t)
	r := newTestRelease(t)
	r.serve(t, SourceGitHub, false)
	base := r.sources[SourceGitHub].URL + "/"
	newDir := t.TempDir()
	writeApp(t, newDir, map[string]string{"pail": "new"})
	m := r.publish(t, base, "0.7.0", newDir)
	// Signed with another key.
	other := newTestRelease(t)
	m.Signature = other.sign(r.files[ArchiveName("0.7.0", r.target)])

	target := filepath.Join(t.TempDir(), "pail")
	writeApp(t, target, map[string]string{"pail": "old"})
	u := r.updater("0.6.4", SourceGitHub)
	get := func(ctx context.Context, url string) (io.ReadCloser, error) { return u.get(ctx, url) }
	if err := installUpdate(context.Background(), get, m, u.publicKey(), "0.6.4", target, nil); err == nil {
		t.Fatal("installed an archive with a wrong signature")
	}
	if b, _ := os.ReadFile(filepath.Join(target, "pail")); string(b) != "old" {
		t.Errorf("pail = %q", b)
	}
	if es, _ := os.ReadDir(filepath.Dir(target)); len(es) != 1 {
		t.Errorf("left next to the app: %v", es)
	}
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
