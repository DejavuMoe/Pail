package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/selfupdate"
	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

func TestSigningKeyMustMatchPublicKey(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := signingKey(base64.StdEncoding.EncodeToString(key)); err == nil {
		t.Fatal("accepted a key that is not the half of selfupdate.PublicKey")
	}
	if _, err := signingKey(""); err == nil {
		t.Fatal("accepted no key")
	}
}

// app writes an app of target with content that changes a little per version.
func app(t *testing.T, dir, target, version string, noise []byte) {
	t.Helper()
	files := map[string]string{"pail": string(noise[:32<<10]) + version, "pail-desktop": version + string(noise[32<<10:])}
	if strings.HasPrefix(target, "darwin-") {
		dir = filepath.Join(dir, "Pail.app", "Contents", "MacOS")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func archive(t *testing.T, dir, target string) []byte {
	t.Helper()
	entries := []string{"Pail.app"}
	if !strings.HasPrefix(target, "darwin-") {
		entries = []string{"pail", "pail-desktop"}
	}
	var b bytes.Buffer
	if err := updatefmt.WriteArchive(&b, dir, entries); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSignVerifyMirror(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	pubKey := base64.StdEncoding.EncodeToString(pub)
	noise := make([]byte, 64<<10)
	rand.Read(noise)
	targets := []string{"linux-amd64", "darwin-arm64"}

	// What GitHub serves: by path, and the latest release under /latest/.
	served := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	releaseURL := func(v string) string { return srv.URL + "/v" + v + "/" }
	latest := srv.URL + "/latest/"
	sign1 := func(b []byte) string { s := sha256.Sum256(b); return updatefmt.Sign(key, s[:]) }

	// 0.6.9 and 0.6.8 are published; 0.6.8 is the previous of 0.6.9.
	for _, target := range targets {
		var prev []updatefmt.Archive
		for _, v := range []string{"0.6.8", "0.6.9"} {
			dir := t.TempDir()
			app(t, dir, target, v, noise)
			b := archive(t, dir, target)
			name := selfupdate.ArchiveName(v, target)
			served["/v"+v+"/"+name] = b
			m := updatefmt.Manifest{Version: v, URL: releaseURL(v) + name, Size: int64(len(b)), Signature: sign1(b), Previous: prev}
			mb, _ := json.Marshal(m)
			served["/latest/"+selfupdate.ManifestName(target)] = mb
			prev = []updatefmt.Archive{{Version: v, URL: m.URL, Size: m.Size, Signature: m.Signature}}
		}
	}

	// 0.7.0 is being released.
	dist := t.TempDir()
	for _, target := range targets {
		dir := t.TempDir()
		app(t, dir, target, "0.7.0", noise)
		if err := os.WriteFile(filepath.Join(dist, selfupdate.ArchiveName("0.7.0", target)), archive(t, dir, target), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := sign(dist, "0.7.0", targets, key, latest, releaseURL("0.7.0")); err != nil {
		t.Fatal(err)
	}
	if err := verify(dist, "0.7.0", targets, pubKey, releaseURL("0.7.0")); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		m, err := readManifest(filepath.Join(dist, selfupdate.ManifestName(target)))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Deltas) != 2 || m.Deltas[0].From != "0.6.9" || m.Deltas[1].From != "0.6.8" {
			t.Errorf("%s: deltas %+v", target, m.Deltas)
		}
		if len(m.Previous) != 2 || m.Previous[0].Version != "0.6.9" {
			t.Errorf("%s: previous %+v", target, m.Previous)
		}
	}
	// Another key's signature does not verify.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := verify(dist, "0.7.0", targets, base64.StdEncoding.EncodeToString(other), releaseURL("0.7.0")); err == nil {
		t.Error("verified with another key")
	}

	// Publish 0.7.0 with its user archives, then mirror it.
	var sums strings.Builder
	for _, target := range targets {
		name := "Pail-v0.7.0-" + target + ".zip"
		body := []byte("user archive " + target)
		served["/v0.7.0/"+name] = body
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(body), name)
	}
	served["/v0.7.0/SHA256SUMS"] = []byte(sums.String())
	entries, _ := os.ReadDir(dist)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dist, e.Name()))
		served["/v0.7.0/"+e.Name()] = b
	}
	out, cache := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cache, "v0.6.9"), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second time from the cache
		if err := mirror("0.7.0", targets, releaseURL("0.7.0"), cache, out, "https://pail.download/update/", pubKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "v0.6.9")); err == nil {
		t.Error("the cache keeps an older release")
	}
	for _, name := range []string{"download/SHA256SUMS", "download/Pail-v0.7.0-linux-amd64.zip", "update/pail-update-0.7.0-darwin-arm64.tar.gz", "update/pail-update-0.6.8-to-0.7.0-linux-amd64.delta"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Error(err)
		}
	}
	m, err := readManifest(filepath.Join(out, "update", selfupdate.ManifestName("linux-amd64")))
	if err != nil {
		t.Fatal(err)
	}
	if m.URL != "https://pail.download/update/pail-update-0.7.0-linux-amd64.tar.gz" || m.Deltas[0].URL != "https://pail.download/update/pail-update-0.6.9-to-0.7.0-linux-amd64.delta" {
		t.Errorf("mirror manifest URLs: %s, %s", m.URL, m.Deltas[0].URL)
	}

	// A file that does not match SHA256SUMS fails the mirror.
	served["/v0.7.0/Pail-v0.7.0-linux-amd64.zip"] = []byte("tampered")
	if err := mirror("0.7.0", targets, releaseURL("0.7.0"), t.TempDir(), t.TempDir(), "https://pail.download/update/", pubKey); err == nil {
		t.Error("mirrored a tampered archive")
	}
}
