package main

// TestSiteCLIDemo runs the commands that the terminal on pail.download shows, in
// process against the in-memory demo S3 server (the one behind the desktop app's
// --demo), and compares what they print with site/cli-demo.json. The site and its
// prototype are built from that file, so the examples are the CLI's real output;
// when the CLI's output changes this test fails until the file is regenerated:
//
//	$env:PAIL_UPDATE_GOLDEN = "1"; go test ./cmd/pail -run TestSiteCLIDemo
//
// Only three things are normalised, because they differ on every run: timestamps
// become one fixed date, and the credential and signature of a presigned URL
// become "…" (the signature is of throw-away keys; the rest of the URL is real).
// stdout and stderr keep the order in which the command wrote them.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/demo"
)

type demoLine struct {
	Kind string `json:"kind"` // cmd, out (stdout) or err (stderr)
	Text string `json:"text"`
}

type demoExample struct {
	ID      string     `json:"id"`
	Command string     `json:"command"`
	Lines   []demoLine `json:"lines"`
}

type demoFile struct {
	Examples []demoExample `json:"examples"`
}

// orderedWriter records lines of one stream into a log shared with the other.
type orderedWriter struct {
	mu   *sync.Mutex
	log  *[]demoLine
	kind string
	buf  bytes.Buffer
}

func (w *orderedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		b := w.buf.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return len(p), nil
		}
		*w.log = append(*w.log, demoLine{Kind: w.kind, Text: string(b[:i])})
		w.buf.Next(i + 1)
	}
}

var (
	reDemoTime   = regexp.MustCompile(`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d`)
	reDemoISO    = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ`)
	reDemoAmzDt  = regexp.MustCompile(`X-Amz-Date=\d{8}T\d{6}Z`)
	reDemoAmzCr  = regexp.MustCompile(`X-Amz-Credential=[^&]+`)
	reDemoAmzSig = regexp.MustCompile(`X-Amz-Signature=[0-9a-f]+`)
)

// normaliseDemo applies the three documented normalisations.
func normaliseDemo(s string) string {
	s = reDemoTime.ReplaceAllString(s, "2026-10-19 14:51:08")
	s = reDemoISO.ReplaceAllString(s, "2026-10-19T14:51:08Z")
	s = reDemoAmzDt.ReplaceAllString(s, "X-Amz-Date=20261019T145108Z")
	s = reDemoAmzCr.ReplaceAllString(s, "X-Amz-Credential=…")
	s = reDemoAmzSig.ReplaceAllString(s, "X-Amz-Signature=…")
	return s
}

func TestSiteCLIDemo(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("..", "..", "site", "cli-demo.json")) // before the working directory moves
	if err != nil {
		t.Fatal(err)
	}
	env, err := demo.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	kr := app.NewMemoryKeyring()

	account := func(a app.Account, sec *app.Secrets) string {
		t.Helper()
		dir := t.TempDir()
		svc, err := app.New(app.Options{ConfigDir: dir, Keyring: kr})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SaveAccount(a, sec); err != nil {
			t.Fatal(err)
		}
		if err := svc.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	s3dir := account(app.Account{Name: "demo", Provider: app.ProviderS3, Endpoint: env.Srv.URL, Region: "us-east-1",
		PathStyle: true, Credentials: app.CredStatic, AccessKeyID: "demo"}, &app.Secrets{SecretAccessKey: "demo-secret"})
	// Presigning is local, so an AWS account shows the address a real one gets.
	awsdir := account(app.Account{Name: "aws", Provider: app.ProviderAWS, Region: "us-east-1",
		Credentials: app.CredStatic, AccessKeyID: "AKIAIOSFODNN7EXAMPLE"}, &app.Secrets{SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"})

	// The folder the sync example reads, and what the bucket already holds.
	work := t.TempDir()
	t.Chdir(work)
	old := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	local := func(rel string, size int) {
		p := filepath.Join(work, "photos", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte{'p'}, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	local("2026/04/IMG_2231.heic", 3_355_443)
	local("2026/04/cover.jpg", 3_040_870)
	local("2026/04/IMG_2198.heic", 2_411_724)
	local("README.txt", 612)
	put := func(key string, size int) {
		env.Srv.PutObject("acme-media-archive", key, bytes.Repeat([]byte{'p'}, size), "application/octet-stream", nil)
	}
	put("photos/2026/04/cover.jpg", 2_516_582)
	put("photos/2026/04/IMG_2198.heic", 2_411_724)
	put("photos/README.txt", 612)
	put("photos/tmp/draft.png", 1_153_434)

	examples := []struct {
		id, dir string
		args    []string
	}{
		{"ls", s3dir, []string{"ls", "s3://acme-assets/"}},
		{"sync", s3dir, []string{"sync", "./photos", "s3://acme-media-archive/photos/", "--mode", "mirror", "--dry-run"}},
		{"presign", awsdir, []string{"presign", "s3://acme-assets/reports/customers-export.csv", "--expires", "1h"}},
		{"json", s3dir, []string{"ls", "s3://acme-assets/reports/exports/", "--json"}},
	}
	var got demoFile
	for _, ex := range examples {
		var mu sync.Mutex
		var log []demoLine
		out := &orderedWriter{mu: &mu, log: &log, kind: "out"}
		errw := &orderedWriter{mu: &mu, log: &log, kind: "err"}
		code := run(context.Background(), append([]string{"--config-dir", ex.dir}, ex.args...), IO{Stdout: out, Stderr: errw, Keyring: kr})
		if code != 0 {
			t.Fatalf("%s exited %d:\n%s", strings.Join(ex.args, " "), code, fmt.Sprint(log))
		}
		lines := []demoLine{{Kind: "cmd", Text: "pail " + strings.Join(ex.args, " ")}}
		for _, l := range log {
			lines = append(lines, demoLine{Kind: l.Kind, Text: normaliseDemo(l.Text)})
		}
		got.Examples = append(got.Examples, demoExample{ID: ex.id, Command: lines[0].Text, Lines: lines})
	}
	var enc bytes.Buffer
	e := json.NewEncoder(&enc)
	e.SetEscapeHTML(false) // keep & readable in the URLs
	e.SetIndent("", " ")
	if err := e.Encode(got); err != nil {
		t.Fatal(err)
	}
	want := enc.Bytes()

	if os.Getenv("PAIL_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, want, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", golden)
		return
	}
	have, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run with PAIL_UPDATE_GOLDEN=1 to create it", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), want) {
		t.Errorf("site/cli-demo.json is out of date with the CLI's output; regenerate it with PAIL_UPDATE_GOLDEN=1.\nnow:\n%s", want)
	}
}
