package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildSite(t *testing.T, version string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "site")
	if err := build(config{root: repoRoot(t), out: out, version: version, baseURL: "https://pail.download"}); err != nil {
		t.Fatal(err)
	}
	return out
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildWritesBothLanguages(t *testing.T) {
	out := buildSite(t, "1.2.3")
	for _, name := range []string{"index.html", "zh/index.html", "404.html", "robots.txt", "sitemap.xml"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	en := read(t, filepath.Join(out, "index.html"))
	zh := read(t, filepath.Join(out, "zh", "index.html"))
	for _, c := range []struct{ page, want string }{
		{en, `<html lang="en" data-lang="en"`},
		{en, `<link rel="canonical" href="https://pail.download/">`},
		{en, `href="/zh/" hreflang="zh-Hans"`},
		{en, "Every bucket."},
		{zh, `<html lang="zh-Hans" data-lang="zh"`},
		{zh, `<link rel="canonical" href="https://pail.download/zh/">`},
		{zh, `href="/" hreflang="en"`},
		{zh, "装下所有存储桶。"},
	} {
		if !strings.Contains(c.page, c.want) {
			t.Errorf("page lacks %q", c.want)
		}
	}
	// Only the English page sends Chinese browsers to /zh/.
	if !strings.Contains(en, "location.replace") || strings.Contains(zh, "location.replace") {
		t.Error("language redirect must be on the English page only")
	}
	for _, page := range []string{en, zh} {
		if strings.Contains(page, "{{") || strings.Contains(page, "data-i18n") || strings.Contains(page, "tweaks") {
			t.Error("page contains template or prototype leftovers")
		}
	}
}

// The download links must name exactly the archives scripts/release.ps1 builds.
func TestDownloadsMatchReleaseScript(t *testing.T) {
	out := buildSite(t, "1.2.3")
	page := read(t, filepath.Join(out, "index.html"))
	script := read(t, filepath.Join(repoRoot(t), "scripts", "release.ps1"))
	m := regexp.MustCompile(`\$Targets = @\(([^)]*)\)`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("release.ps1: $Targets not found")
	}
	var want []string
	for _, target := range regexp.MustCompile(`'(\w+)/(\w+)'`).FindAllStringSubmatch(m[1], -1) {
		ext := ".zip"
		if target[1] == "linux" {
			ext = ".tar.gz"
		}
		want = append(want, "Pail-1.2.3-"+target[1]+"-"+target[2]+ext)
	}
	var got []string
	for _, l := range regexp.MustCompile(`releases/download/v1\.2\.3/(Pail-[^"]+)"`).FindAllStringSubmatch(page, -1) {
		got = append(got, l[1])
	}
	slices.Sort(want)
	slices.Sort(got)
	if len(want) != 6 || !slices.Equal(got, want) {
		t.Errorf("downloads %v, release.ps1 builds %v", got, want)
	}
	if !strings.Contains(page, "releases/download/v1.2.3/SHA256SUMS") {
		t.Error("no SHA256SUMS link")
	}
}

// app.js may only ask for strings that are embedded in the page.
func TestRuntimeKeysCoverScript(t *testing.T) {
	js := read(t, filepath.Join(repoRoot(t), "site", "static", "app.js"))
	for _, m := range regexp.MustCompile(`\bt\('(\w+)'|g: '(\w+)'`).FindAllStringSubmatch(js, -1) {
		key := m[1] + m[2]
		if !slices.Contains(runtimeKeys, key) {
			t.Errorf("app.js uses %q, which is not in runtimeKeys", key)
		}
	}
}

func TestAssetsAreFingerprintedAndClean(t *testing.T) {
	out := buildSite(t, "1.2.3")
	entries, err := os.ReadDir(filepath.Join(out, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(assets) {
		t.Fatalf("%d assets, want %d", len(entries), len(assets))
	}
	hashed := regexp.MustCompile(`^[a-z-]+\.[0-9a-f]{10}\.(css|js|svg|png)$`)
	for _, e := range entries {
		if !hashed.MatchString(e.Name()) {
			t.Errorf("asset %s is not fingerprinted", e.Name())
		}
		if strings.HasSuffix(e.Name(), ".svg") && strings.Contains(read(t, filepath.Join(out, "assets", e.Name())), "<!--") {
			t.Errorf("%s keeps an XML comment", e.Name())
		}
	}
	// Same input, same names: rebuilding into the same directory is allowed and stable.
	if err := build(config{root: repoRoot(t), out: out, version: "1.2.3", baseURL: "https://pail.download"}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadDir(filepath.Join(out, "assets"))
	for i := range entries {
		if entries[i].Name() != second[i].Name() {
			t.Errorf("rebuild renamed %s to %s", entries[i].Name(), second[i].Name())
		}
	}
}

func TestRejectsBadInput(t *testing.T) {
	root := repoRoot(t)
	for _, v := range []string{"", "v1.2.3", "1.2", "1.2.3 ", "1.2.3-"} {
		if err := build(config{root: root, out: t.TempDir(), version: v, baseURL: "https://pail.download"}); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
	for _, u := range []string{"http://pail.download", "https://pail.download/sub", "pail.download"} {
		if err := build(config{root: root, out: t.TempDir(), version: "1.2.3", baseURL: u}); err == nil {
			t.Errorf("base URL %q accepted", u)
		}
	}
}

func TestKeepsUnrelatedOutputDirectory(t *testing.T) {
	out := t.TempDir()
	keep := filepath.Join(out, "notes.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(config{root: repoRoot(t), out: out, version: "1.2.3", baseURL: "https://pail.download"}); err == nil {
		t.Error("build replaced a directory it did not write")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("unrelated file removed: %v", err)
	}
}
