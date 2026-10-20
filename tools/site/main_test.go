package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

func TestSocialMetadata(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	const origin = "https://preview.example"
	if err := build(config{root: repoRoot(t), out: out, version: "0.2.0", baseURL: origin}); err != nil {
		t.Fatal(err)
	}
	for _, lang := range languages {
		paths := []string{lang.Path}
		for _, doc := range docPages {
			paths = append(paths, docURL(doc, lang))
		}
		locale, alternate := "en_US", "zh_CN"
		if lang.Code == "zh" {
			locale, alternate = alternate, locale
		}
		for _, path := range paths {
			page := read(t, filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(path, "/")), "index.html"))
			for _, want := range []string{
				`property="og:type" content="website"`,
				`property="og:url" content="` + origin + path + `"`,
				`property="og:locale" content="` + locale + `"`,
				`property="og:locale:alternate" content="` + alternate + `"`,
				`name="twitter:card" content="summary_large_image"`,
				`property="og:image:type" content="image/webp"`,
			} {
				if !strings.Contains(page, want) {
					t.Errorf("%s lacks %s", path, want)
				}
			}
			for _, pair := range [][2]string{
				{"og:title", "twitter:title"}, {"og:description", "twitter:description"},
				{"og:image", "twitter:image"}, {"og:image:alt", "twitter:image:alt"},
			} {
				a := regexp.MustCompile(`property="` + pair[0] + `" content="([^"]+)"`).FindStringSubmatch(page)
				b := regexp.MustCompile(`name="` + pair[1] + `" content="([^"]+)"`).FindStringSubmatch(page)
				if len(a) != 2 || len(b) != 2 || a[1] != b[1] {
					t.Fatalf("%s inconsistent %v", path, pair)
				}
			}
			imageURL := regexp.MustCompile(`property="og:image" content="([^"]+)"`).FindStringSubmatch(page)[1]
			if !strings.HasPrefix(imageURL, origin+"/assets/social.") {
				t.Fatalf("%s: noncanonical image URL %s", path, imageURL)
			}
			data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(imageURL, origin+"/"))))
			if err != nil {
				t.Fatal(err)
			}
			dim, err := webp.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]int{"width": dim.Width, "height": dim.Height} {
				if !strings.Contains(page, fmt.Sprintf(`property="og:image:%s" content="%d"`, key, value)) {
					t.Errorf("%s: wrong declared image %s", path, key)
				}
			}
		}
	}
	if page := read(t, filepath.Join(out, "404.html")); !strings.Contains(page, `content="noindex"`) || strings.Contains(page, `property="og:`) {
		t.Error("404 must remain nonindexable without social metadata")
	}
}

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
		{en, "A desktop client for S3"},
		{zh, `<html lang="zh-Hans" data-lang="zh"`},
		{zh, `<link rel="canonical" href="https://pail.download/zh/">`},
		{zh, `href="/" hreflang="en"`},
		{zh, "S3 兼容存储的桌面客户端"},
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

// The download links must name exactly the archives tools/release builds.
func TestDownloadsMatchReleaseScript(t *testing.T) {
	out := buildSite(t, "1.2.3")
	page := read(t, filepath.Join(out, "index.html"))
	script := read(t, filepath.Join(repoRoot(t), "tools", "release", "main.go"))
	m := regexp.MustCompile(`const allTargets = "([^"]+)"`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("tools/release: allTargets not found")
	}
	var want []string
	for _, target := range regexp.MustCompile(`(\w+)/(\w+)`).FindAllStringSubmatch(m[1], -1) {
		ext := ".zip"
		if target[1] == "linux" {
			ext = ".tar.gz"
		}
		want = append(want, "Pail-v1.2.3-"+target[1]+"-"+target[2]+ext)
	}
	var got []string
	for _, l := range regexp.MustCompile(`releases/download/v1\.2\.3/(Pail-[^"]+)"`).FindAllStringSubmatch(page, -1) {
		got = append(got, l[1])
	}
	slices.Sort(want)
	slices.Sort(got)
	if len(want) != 6 || !slices.Equal(got, want) {
		t.Errorf("downloads %v, tools/release builds %v", got, want)
	}
	if !strings.Contains(page, "releases/download/v1.2.3/SHA256SUMS") {
		t.Error("no SHA256SUMS link")
	}
}

// The mirror is offered only when the build is told the version is there, and
// then for every archive under the same file name.
func TestMirrorDownloads(t *testing.T) {
	page := read(t, filepath.Join(buildSite(t, "1.2.3"), "index.html"))
	if strings.Contains(page, "data-mirror-base") || strings.Contains(page, `name="source"`) {
		t.Error("mirror offered without -mirror-url")
	}

	out := filepath.Join(t.TempDir(), "site")
	if err := build(config{root: repoRoot(t), out: out, version: "1.2.3", baseURL: "https://pail.download", mirrorURL: "https://pan.zsh.moe/Pail/"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"index.html", "zh/index.html"} {
		page := read(t, filepath.Join(out, filepath.FromSlash(p)))
		for _, want := range []string{
			`data-github-base="https://github.com/DejavuMoe/Pail/releases/download/v1.2.3/"`,
			`data-mirror-base="https://pan.zsh.moe/Pail/v1.2.3/"`,
			`<a href="https://pan.zsh.moe/Pail/v1.2.3/">pan.zsh.moe/Pail/v1.2.3</a>`,
			`name="source" value="mirror"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks %s", p, want)
			}
		}
		if n := len(regexp.MustCompile(`data-asset="Pail-v1\.2\.3-[a-z0-9-]+\.(zip|tar\.gz)"`).FindAllString(page, -1)); n != 6 {
			t.Errorf("%s: %d archives carry data-asset, want 6", p, n)
		}
	}
}

func TestReleaseDate(t *testing.T) {
	for version, want := range map[string]string{"0.2.0": "2026-10-20", "0.1.0": "2026-10-20", "1.2.3": ""} {
		got, err := releaseDate(repoRoot(t), version)
		if err != nil || got != want {
			t.Errorf("releaseDate(%s) = %q, %v; want %q", version, got, err, want)
		}
	}
	if releasedText("{date} 发布", "2026-10-20") != "2026-10-20 发布" || releasedText("Released {date}", "") != "" {
		t.Error("releasedText")
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
	hashed := regexp.MustCompile(`^[a-z-]+\.[0-9a-f]{10}\.(css|js|svg|webp)$`)
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
	for _, u := range []string{"http://pan.zsh.moe/Pail", "pan.zsh.moe/Pail", "https://pan.zsh.moe/Pail?x=1"} {
		if err := build(config{root: root, out: t.TempDir(), version: "1.2.3", baseURL: "https://pail.download", mirrorURL: u}); err == nil {
			t.Errorf("mirror URL %q accepted", u)
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
