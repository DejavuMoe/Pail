package main

import (
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	idAttrRE   = regexp.MustCompile(`\sid="([^"]+)"`)
	headingRE  = regexp.MustCompile(`<h([23]) id="([^"]+)"`)
	linkAttrRE = regexp.MustCompile(`\s(src|href)="([^"]*)"`)
)

// mainOf is the content of <main>: the page chrome has headings of its own.
func mainOf(page string) string {
	a, b := strings.Index(page, "<main"), strings.Index(page, "</main>")
	if a < 0 || b < a {
		return ""
	}
	return page[a:b]
}

// docsFiles lists the docs pages a build must contain, per language.
func docsFiles() map[string][]string {
	out := map[string][]string{}
	for _, l := range languages {
		for _, p := range docPages {
			out[l.Code] = append(out[l.Code], strings.TrimPrefix(docURL(p, l), "/")+"index.html")
		}
	}
	return out
}

func TestBuildWritesAllPages(t *testing.T) {
	out := buildSite(t, "1.2.3")
	want := []string{"index.html", "zh/index.html", "404.html", "robots.txt", "sitemap.xml"}
	for _, files := range docsFiles() {
		want = append(want, files...)
	}
	if n := 5 + 2*len(docPages); len(want) != n {
		t.Fatalf("expected %d pages, test lists %d", n, len(want))
	}
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	sitemap := read(t, filepath.Join(out, "sitemap.xml"))
	for _, loc := range []string{"/docs/", "/docs/install/", "/docs/sync/", "/docs/cli/", "/changelog/", "/license/", "/zh/docs/", "/zh/docs/install/", "/zh/docs/cli/", "/zh/changelog/", "/zh/license/"} {
		if !strings.Contains(sitemap, "<loc>https://pail.download"+loc+"</loc>") {
			t.Errorf("sitemap lacks %s", loc)
		}
	}
	if n, want := strings.Count(sitemap, `hreflang="zh-Hans"`), 2*(1+len(docPages)); n != want {
		t.Errorf("sitemap has %d zh-Hans alternates, want %d", n, want)
	}
}

func TestDocsPagesHaveStableIDs(t *testing.T) {
	out := buildSite(t, "1.2.3")
	for _, dir := range []string{"docs", "zh/docs"} {
		page := mainOf(read(t, filepath.Join(out, filepath.FromSlash(dir), "index.html")))
		var got []string
		for _, m := range headingRE.FindAllStringSubmatch(page, -1) {
			if m[1] == "2" {
				got = append(got, m[2])
			}
		}
		want := []string{"what-you-download", "systems", "supported-services", "the-main-window", "where-to-go-next"}
		if !slices.Equal(got, want) {
			t.Errorf("%s: h2 ids %v, want %v", dir, got, want)
		}
	}
	// The Chinese pages reuse the English heading ids, in the same order.
	for _, p := range docPages {
		ids := func(l language) []string {
			var out2 []string
			for _, m := range headingRE.FindAllStringSubmatch(mainOf(read(t, filepath.Join(out, filepath.FromSlash(docURL(p, l)), "index.html"))), -1) {
				out2 = append(out2, m[1]+":"+m[2])
			}
			return out2
		}
		if en, zh := ids(languages[0]), ids(languages[1]); !slices.Equal(en, zh) {
			t.Errorf("%s: zh headings %v differ from en %v", p.Key, zh, en)
		}
	}
	for _, id := range []string{`id="s-0-2-0-2026-10-20"`, `id="s-0-1-0-2026-10-20"`, `id="fixed"`, `id="added"`, `id="known-limitations"`} {
		if !strings.Contains(read(t, filepath.Join(out, "zh", "changelog", "index.html")), id) {
			t.Errorf("zh changelog lacks %s", id)
		}
	}
}

func TestDocsPageStructure(t *testing.T) {
	out := buildSite(t, "1.2.3")
	get := func(name string) string { return read(t, filepath.Join(out, filepath.FromSlash(name), "index.html")) }

	// A page with two or more sections has "On this page".
	for _, name := range []string{"docs", "docs/install", "docs/cli", "zh/docs", "zh/docs/sync", "zh/docs/cli", "changelog", "zh/changelog"} {
		if !strings.Contains(get(name), `<aside class="toc" id="toc"`) {
			t.Errorf("%s has no table of contents", name)
		}
	}
	for _, name := range []string{"license", "zh/license"} {
		page := get(name)
		if strings.Contains(page, `class="toc"`) {
			t.Errorf("%s has a table of contents", name)
		}
		if !strings.Contains(page, `<p class="license-copyright">Copyright (c) 2026 Dejavu Moe</p>`) {
			t.Errorf("%s lacks the copyright line", name)
		}
	}
	// Only the Chinese license page carries the English-only note, and it marks the text as English.
	en, zh := get("license"), get("zh/license")
	if strings.Contains(en, `class="doc-note"`) || strings.Contains(en, `class="prose" lang="en"`) {
		t.Error("English license page has the English-only note")
	}
	if !strings.Contains(zh, `<p class="doc-note">`) || !strings.Contains(zh, `<div class="prose" lang="en">`) {
		t.Error("Chinese license page lacks the English-only note or lang=en on the text")
	}
	if strings.Contains(get("zh/docs"), `class="prose" lang="en"`) {
		t.Error("lang=en leaked onto a translated page")
	}
	// A guide page shows its first paragraph under the title and uses it as the description.
	root := repoRoot(t)
	for _, l := range languages {
		src, err := readSource(root, docPageByKey("install").Sources[l.Code])
		if err != nil {
			t.Fatal(err)
		}
		_, rest, _ := splitTitle(src)
		lede, _, err := splitLede(rest)
		if err != nil {
			t.Fatal(err)
		}
		page := get(strings.TrimPrefix(docURL(docPageByKey("install"), l), "/"))
		if !strings.Contains(page, `<p class="doc-lede">`+html.EscapeString(lede)+`</p>`) ||
			!strings.Contains(page, `<meta name="description" content="`+html.EscapeString(lede)+`">`) {
			t.Errorf("install (%s): the first paragraph is not the lede and description", l.Code)
		}
	}
	// Per page: canonical and hreflang alternates, the current doc nav entry, the language link.
	for _, p := range docPages {
		for _, l := range languages {
			page := get(strings.TrimPrefix(docURL(p, l), "/"))
			for _, want := range []string{
				`<link rel="canonical" href="https://pail.download` + docURL(p, l) + `">`,
				`<link rel="alternate" hreflang="en" href="https://pail.download` + docURL(p, languages[0]) + `">`,
				`<link rel="alternate" hreflang="zh-Hans" href="https://pail.download` + docURL(p, languages[1]) + `">`,
				`<link rel="alternate" hreflang="x-default" href="https://pail.download` + docURL(p, languages[0]) + `">`,
				`<a href="` + docURL(p, l) + `" aria-current="page">`,
				`<a class="lang" href="` + docURL(p, languages[1-indexOfLang(l)]) + `"`,
				`<html lang="` + l.HTMLLang + `" data-lang="` + l.Code + `"`,
			} {
				if !strings.Contains(page, want) {
					t.Errorf("%s (%s) lacks %s", p.Key, l.Code, want)
				}
			}
		}
	}
	// Tables are wrapped, labelled and use column headers.
	if page := get("docs/install"); !strings.Contains(page, `<div class="table-wrap" role="group" aria-label="Table: Choose an archive" tabindex="0"><table>`) ||
		!strings.Contains(page, `<th scope="col">System</th>`) {
		t.Error("install table is not wrapped and labelled")
	}
	if !strings.Contains(get("zh/docs/install"), `aria-label="表格: `) {
		t.Error("Chinese table label missing")
	}
	// Code blocks are focusable.
	if !strings.Contains(get("docs/install"), `<pre tabindex="0"><code class="language-sh">`) {
		t.Error("code block markup")
	}
}

func indexOfLang(l language) int {
	for i, x := range languages {
		if x.Code == l.Code {
			return i
		}
	}
	return -1
}

// Every internal link and image of every page resolves to a built file (and its
// fragment to an id on that page); every external link stays on the allowed hosts
// or is written verbatim in the Markdown sources.
func TestAllLinksResolve(t *testing.T) {
	out := buildSite(t, "1.2.3")
	root := repoRoot(t)

	var sources strings.Builder
	for _, p := range docPages {
		for _, src := range p.Sources {
			sources.WriteString(read(t, filepath.Join(root, filepath.FromSlash(src))))
		}
	}
	allowed := []string{githubURL, "https://dejavu.moe", "https://pail.download/", defaultMirrorURL}

	ids := map[string]map[string]bool{} // site path of file -> ids
	var files []string
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		rel = filepath.ToSlash(rel)
		files = append(files, rel)
		set := map[string]bool{}
		for _, m := range idAttrRE.FindAllStringSubmatch(read(t, p), -1) {
			set[m[1]] = true
		}
		ids[rel] = set
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := 3 + 2*len(docPages); len(files) != want {
		t.Fatalf("%d HTML files, want %d", len(files), want)
	}

	for _, name := range files {
		page := read(t, filepath.Join(out, filepath.FromSlash(name)))
		self := "/" + strings.TrimSuffix(name, "index.html")
		if name == "404.html" {
			self = "/404.html"
		}
		for _, m := range linkAttrRE.FindAllStringSubmatch(page, -1) {
			attr, raw := m[1], html.UnescapeString(m[2])
			switch {
			case strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://"):
				ok := slices.ContainsFunc(allowed, func(p string) bool { return strings.HasPrefix(raw, p) }) || strings.Contains(sources.String(), raw)
				if attr == "src" || !ok {
					t.Errorf("%s: %s=%q leaves the site", name, attr, raw)
				}
				continue
			case strings.HasPrefix(raw, "//") || strings.Contains(raw, ".md") || strings.Contains(raw, "docs.html") || strings.Contains(raw, ".html"):
				if raw != "/404.html" {
					t.Errorf("%s: %s=%q is not a site URL", name, attr, raw)
				}
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Errorf("%s: bad %s %q", name, attr, raw)
				continue
			}
			target := u.Path
			if target == "" {
				target = self // fragment only
			} else if !strings.HasPrefix(target, "/") {
				t.Errorf("%s: %s=%q is relative", name, attr, raw)
				continue
			}
			rel := strings.TrimPrefix(target, "/")
			file := rel
			if rel == "" || strings.HasSuffix(rel, "/") {
				file = rel + "index.html"
			}
			if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(file))); err != nil {
				t.Errorf("%s: %s=%q does not exist in the build", name, attr, raw)
				continue
			}
			if u.Fragment != "" && strings.HasSuffix(file, ".html") && !ids[file][u.Fragment] {
				t.Errorf("%s: %s=%q has no id %q on the target page", name, attr, raw, u.Fragment)
			}
		}
	}
}

func TestPagesHaveNoPrototypeLeftovers(t *testing.T) {
	out := buildSite(t, "1.2.3")
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		page := read(t, p)
		for _, bad := range []string{"data-i18n", "tweaks", "i18n.js", "PAIL_I18N", "{{", "data-screen-label", "data-backdrop", "pail-stage", "styles.css", "app.js", "docs.js"} {
			if strings.Contains(page, bad) {
				t.Errorf("%s contains %q", p, bad)
			}
		}
		return nil
	})
}

// The same inputs always give the same bytes.
func TestBuildIsDeterministic(t *testing.T) {
	a, b := buildSite(t, "1.2.3"), buildSite(t, "1.2.3")
	var names []string
	_ = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(a, p)
			names = append(names, rel)
		}
		return err
	})
	if len(names) < 20 {
		t.Fatalf("only %d files built", len(names))
	}
	for _, name := range names {
		if read(t, filepath.Join(a, name)) != read(t, filepath.Join(b, name)) {
			t.Errorf("%s differs between two builds", name)
		}
	}
}

func TestRuntimeStringsAreEmbedded(t *testing.T) {
	out := buildSite(t, "1.2.3")
	for _, name := range []string{"index.html", "zh/index.html", "docs/index.html", "zh/docs/cli/index.html"} {
		page := read(t, filepath.Join(out, filepath.FromSlash(name)))
		m := regexp.MustCompile(`<script type="application/json" id="site-strings">(.*?)</script>`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("%s: no embedded strings", name)
		}
		for _, k := range runtimeKeys {
			if !strings.Contains(m[1], `"`+k+`":`) {
				t.Errorf("%s: embedded strings lack %s", name, k)
			}
		}
	}
}

func TestMapHref(t *testing.T) {
	cases := []struct {
		srcDir, lang, href string
		want               string
		page               string
		err                bool
	}{
		{"", "en", "docs/cli.md", "/docs/cli/", "cli", false},
		{"", "zh", "docs/cli.md", "/zh/docs/cli/", "cli", false},
		{"", "zh", "docs/cli.zh-CN.md#exit-codes", "/zh/docs/cli/#exit-codes", "cli", false},
		{"docs", "en", "../README.md#install", githubURL + "/blob/master/README.md#install", "", false},
		{"docs/guide", "en", "install.md#check-the-download", "/docs/install/#check-the-download", "install", false},
		{"docs/guide", "zh", "sync.zh-CN.md", "/zh/docs/sync/", "sync", false},
		{"docs/guide", "en", "../cli.md#exit-codes", "/docs/cli/#exit-codes", "cli", false},
		{"", "en", "CHANGELOG.zh-CN.md", "/changelog/", "changelog", false},
		{"", "en", "LICENSE", "/license/", "license", false},
		{"", "en", "docs/status.md", githubURL + "/blob/master/docs/status.md", "", false},
		{"docs", "zh", "images/x.txt#a", githubURL + "/blob/master/docs/images/x.txt#a", "", false},
		{"", "en", "https://example.com/a?b=c", "https://example.com/a?b=c", "", false},
		{"", "en", "mailto:a@example.com", "mailto:a@example.com", "", false},
		{"", "en", "#install", "#install", "", false},
		{"docs", "en", "../../outside.md", "", "", true},
		{"", "en", "../outside.md", "", "", true},
		{"", "en", "/etc/passwd", "", "", true},
		{"", "en", "javascript:alert(1)", "", "", true},
		{"", "en", "//evil.example/x", "", "", true},
	}
	for _, c := range cases {
		ctx := newMDContext(c.lang, c.srcDir, nil, nil)
		got, page, err := ctx.mapHref(c.href)
		if c.err {
			if err == nil {
				t.Errorf("mapHref(%q) = %q, want an error", c.href, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("mapHref(%q, %s) = %q, %v; want %q", c.href, c.lang, got, err, c.want)
		}
		if (page == nil) != (c.page == "") || (page != nil && page.Key != c.page) {
			t.Errorf("mapHref(%q) page = %v, want %q", c.href, page, c.page)
		}
	}
}

func TestMarkdownRendering(t *testing.T) {
	labels := map[string]string{"docAnchor": "Link", "docTable": "Table", "pageCli": "Command line reference"}
	render := func(srcDir, md string) (string, *mdContext, error) {
		ctx := newMDContext("en", srcDir, labels, labels)
		out, err := ctx.convert([]byte(md))
		return out, ctx, err
	}

	out, ctx, err := render("", "## [Unreleased]\n\nText.\n\n### 2 Fast & loose\n\n| A | B |\n| --- | --- |\n| `<x>` | y |\n\n## Unreleased\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h2 id="unreleased">Unreleased <a class="heading-anchor" href="#unreleased" aria-label="Link"><span aria-hidden="true">#</span></a></h2>`,
		`<h3 id="s-2-fast-loose">2 Fast &amp; loose `,
		`<div class="table-wrap" role="group" aria-label="Table: 2 Fast &amp; loose" tabindex="0"><table>`,
		"<th scope=\"col\">A</th>",
		`<td><code><span class="tok">&lt;x&gt;</span></code></td>`,
		`<h2 id="unreleased-2">`, // duplicate ids get a suffix
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s\n%s", want, out)
		}
	}
	if len(ctx.headings) != 3 || ctx.headings[0].Text != "Unreleased" {
		t.Errorf("headings %+v", ctx.headings)
	}

	out, _, err = render("", "```powershell\na < b && c\n```\n\n    indented\n")
	if err != nil || !strings.Contains(out, "<pre tabindex=\"0\"><code class=\"language-powershell\">a &lt; b &amp;&amp; c</code></pre>") ||
		!strings.Contains(out, "<pre tabindex=\"0\"><code>indented</code></pre>") {
		t.Errorf("code blocks: %v\n%s", err, out)
	}

	// Shell comments are dimmed; a # inside quotes or a word is not a comment.
	out, _, err = render("", "```sh\n# list\npail ls a#b \"x # y\"   # <all>\n```\n\n```text\na # b\n```\n")
	if err != nil || !strings.Contains(out, `<code class="language-sh"><span class="c"># list</span>`+"\n"+`pail ls a#b "x # y"   <span class="c"># &lt;all&gt;</span></code>`) ||
		!strings.Contains(out, `<code class="language-text">a # b</code>`) {
		t.Errorf("shell comments: %v\n%s", err, out)
	}

	// Inline code breaks only between space-separated tokens; code blocks stay plain.
	for md, want := range map[string]string{
		"`cp SRC DST [-r]`":        `<code><span class="tok">cp</span> <span class="tok">SRC</span> <span class="tok">DST</span> <span class="tok">[-r]</span></code>`,
		"`--vault-password-stdin`": `<code><span class="tok">--vault-password-stdin</span></code>`,
		"`<a&b> \"q\"`":            `<code><span class="tok">&lt;a&amp;b&gt;</span> <span class="tok">&quot;q&quot;</span></code>`,
		"`a\nb`":                   `<code><span class="tok">a</span> <span class="tok">b</span></code>`,
		"``x `y` z``":              `<code><span class="tok">x</span> <span class="tok">` + "`y`" + `</span> <span class="tok">z</span></code>`,
	} {
		out, _, err = render("", md)
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("code span %q: %v\n%s", md, err, out)
		}
	}

	// After Chinese text a span gets a place to break before it; after a space or
	// an opening bracket it does not.
	for md, want := range map[string]string{
		"`--mode`、`--compare`": `</code>、<wbr><code><span class="tok">--compare</span></code>`,
		"加上`--yes`":            `加上<wbr><code>`,
		"use `--yes` (`-y`)":   `<p>use <code><span class="tok">--yes</span></code> (<code>`,
		"（`gzip`）":             `<p>（<code>`,
	} {
		out, _, err = render("", md)
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("break before code span %q: %v\n%s", md, err, out)
		}
	}

	// A link that names a site page by its file path shows the page title.
	out, _, err = render("", "See [docs/cli.md](docs/cli.md#flags), [docs/status.md](docs/status.md) and [the CLI](docs/cli.md).")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<a href="/docs/cli/#flags">Command line reference</a>`, `<a href="` + githubURL + `/blob/master/docs/status.md">docs/status.md</a>`, `<a href="/docs/cli/">the CLI</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s\n%s", want, out)
		}
	}

	for name, md := range map[string]string{
		"image":         "![x](a.png)",
		"raw html":      "<div>x</div>\n",
		"inline html":   "a <b>x</b>",
		"deep heading":  "#### x\n",
		"title heading": "# x\n",
		"escaping link": "[x](../x.md)",
		"script link":   "[x](javascript:alert(1))",
		"untitled path": "[docs/guide/sync.md](docs/guide/sync.md)",
	} {
		if _, _, err := render("", md); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPairHeadings(t *testing.T) {
	h := func(levels ...int) []heading {
		var out []heading
		for _, l := range levels {
			out = append(out, heading{Level: l})
		}
		return out
	}
	if err := pairHeadings("x.md", h(2, 3, 3, 2), h(2, 3, 3, 2)); err != nil {
		t.Error(err)
	}
	for _, got := range [][]heading{h(2, 3, 3), h(2, 3, 3, 2, 2), h(2, 3, 2, 2), nil} {
		if err := pairHeadings("x.md", h(2, 3, 3, 2), got); err == nil {
			t.Errorf("headings %v accepted", got)
		}
	}
}

func TestSplitTitleAndLede(t *testing.T) {
	title, rest, err := splitTitle("# T\n\nEnglish | [简体中文](README.zh-CN.md)\n\nBody\n")
	if err != nil || title != "T" || strings.TrimSpace(rest) != "Body" {
		t.Errorf("splitTitle = %q, %q, %v", title, rest, err)
	}
	if _, _, err := splitTitle("## T\n"); err == nil {
		t.Error("source without a title accepted")
	}
	lede, rest, err := splitLede("\nOne\nparagraph.\n\n## A\n")
	if err != nil || lede != "One paragraph." || !strings.HasPrefix(rest, "## A") {
		t.Errorf("splitLede = %q, %q, %v", lede, rest, err)
	}
	for _, md := range []string{"## A\n", "- item\n", "Uses `code`.\n", "A [link](x.md).\n", "> quote\n", "1. step\n", ""} {
		if _, _, err := splitLede(md); err == nil {
			t.Errorf("lede %q accepted", md)
		}
	}
}

// copyFixture copies the inputs of a build into a temporary repository root.
func copyFixture(t *testing.T) string {
	t.Helper()
	src, dst := repoRoot(t), t.TempDir()
	files := []string{"LICENSE", "CHANGELOG.md", "CHANGELOG.zh-CN.md", "docs/cli.md", "docs/cli.zh-CN.md",
		"docs/images/main-light-en.webp", "docs/images/main-light-zh.webp", "docs/images/main-dark-en.webp", "docs/images/main-dark-zh.webp", "docs/images/social.webp", "assets/brand/pail-icon.svg", "assets/brand/generated/pail-256.png"}
	for _, dir := range []string{"site", "docs/guide"} {
		err := filepath.WalkDir(filepath.Join(src, filepath.FromSlash(dir)), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(src, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		d := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func buildRoot(t *testing.T, root string) error {
	t.Helper()
	return build(config{root: root, out: filepath.Join(t.TempDir(), "site"), version: "1.2.3", baseURL: "https://pail.download"})
}

func appendTo(t *testing.T, root, file, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(file)), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestBuildFailsOnBadDocs(t *testing.T) {
	if err := buildRoot(t, copyFixture(t)); err != nil {
		t.Fatalf("unmodified fixture does not build: %v", err)
	}
	cases := []struct {
		name, file, text, want string
	}{
		{"zh CLI with an extra heading", "docs/cli.zh-CN.md", "\n## 额外章节\n\n内容\n", "headings do not match"},
		{"zh changelog with an extra level-3 heading", "CHANGELOG.zh-CN.md", "\n### 额外\n\n- x\n", "headings do not match"},
		{"en CLI with an extra heading (zh left behind)", "docs/cli.md", "\n## Extra\n\ntext\n", "headings do not match"},
		{"link escaping the repository", "docs/cli.md", "\nSee [x](../../secret.md).\n", "escapes the repository"},
		{"image", "CHANGELOG.md", "\n![x](docs/images/main-light-en.webp)\n", "image not expected"},
		{"deep heading", "docs/cli.md", "\n#### Too deep\n", "unexpected heading level"},
		{"zh guide with an extra heading", "docs/guide/sync.zh-CN.md", "\n## 额外章节\n\n内容\n", "headings do not match"},
		{"guide with a level-1 heading", "docs/guide/install.md", "\n# Second title\n", "unexpected heading level"},
		{"guide linking a guide by its path", "docs/guide/install.md", "\nSee [sync.md](sync.md).\n", "no locale string"},
	}
	for _, c := range cases {
		root := copyFixture(t)
		appendTo(t, root, c.file, c.text)
		err := buildRoot(t, root)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one containing %q", c.name, err, c.want)
		}
	}

	// A guide page must open with a plain-text paragraph.
	root := copyFixture(t)
	p := filepath.Join(root, "docs", "guide", "versions.md")
	b, _ := os.ReadFile(p)
	title, rest, _ := strings.Cut(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n\n")
	if err := os.WriteFile(p, []byte(title+"\n\n## Straight to a section\n\n"+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := buildRoot(t, root); err == nil || !strings.Contains(err.Error(), "plain-text paragraph") {
		t.Errorf("guide without a lede: %v", err)
	}
}
