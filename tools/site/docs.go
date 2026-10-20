package main

import (
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// docPage is one documentation page. Its content comes from the repository's
// own Markdown (docs/site.md), never from separately written copy.
type docPage struct {
	Key         string
	Path        string            // URL path below the language root, with trailing slash
	NavKey      string            // locale key of the entry in the docs navigation
	TitleKey    string            // locale key of the page title (also the text of links that name the file)
	DescKey     string            // locale key of the meta description
	Sources     map[string]string // language code -> repository file
	EnglishOnly bool              // the text is English on every language's page
}

var docPages = []docPage{
	{Key: "start", Path: "docs/", NavKey: "docGetStarted", TitleKey: "docGetStarted", DescKey: "docStartDesc",
		Sources: map[string]string{"en": "README.md", "zh": "README.zh-CN.md"}},
	{Key: "cli", Path: "docs/cli/", NavKey: "docCli", TitleKey: "cliDocs", DescKey: "docCliDesc",
		Sources: map[string]string{"en": "docs/cli.md", "zh": "docs/cli.zh-CN.md"}},
	{Key: "changelog", Path: "changelog/", NavKey: "changelog", TitleKey: "changelog", DescKey: "docChangelogDesc",
		Sources: map[string]string{"en": "CHANGELOG.md", "zh": "CHANGELOG.zh-CN.md"}},
	{Key: "license", Path: "license/", NavKey: "docLicense", TitleKey: "license", DescKey: "docLicenseDesc",
		Sources: map[string]string{"en": "LICENSE", "zh": "LICENSE"}, EnglishOnly: true},
}

// docOutputs are the top-level directories the docs pages add to a build
// (the Chinese ones live under zh/).
var docOutputs = []string{"docs", "changelog", "license"}

func docPageByKey(key string) docPage {
	for _, p := range docPages {
		if p.Key == key {
			return p
		}
	}
	panic("unknown docs page " + key)
}

// docURL is the site path of a docs page in a language.
func docURL(p docPage, l language) string { return l.Path + p.Path }

func languageByCode(code string) language {
	for _, l := range languages {
		if l.Code == code {
			return l
		}
	}
	panic("unknown language " + code)
}

// sitePageForSource finds the docs page that renders a repository file.
func sitePageForSource(file string) (docPage, bool) {
	for _, p := range docPages {
		for _, src := range p.Sources {
			if src == file {
				return p, true
			}
		}
	}
	return docPage{}, false
}

// pageLinks are the site URLs the templates link to, for one language.
type pageLinks struct {
	Home, Docs, Cli, Changelog, License, Build string
}

func linksFor(l language) pageLinks {
	docs := docURL(docPageByKey("start"), l)
	return pageLinks{
		Home:      l.Path,
		Docs:      docs,
		Cli:       docURL(docPageByKey("cli"), l),
		Changelog: docURL(docPageByKey("changelog"), l),
		License:   docURL(docPageByKey("license"), l),
		Build:     docs + "#build-from-source",
	}
}

// README sections shown on "Get started": by position among the README's "## "
// sections, which must carry the expected title (so a reordered README fails
// the build instead of publishing the wrong text).
var readmeTitles = map[string][]string{
	"en": {"Features", "Install", "Quick start", "Command line", "Build from source", "Testing", "Data and privacy", "Platform status", "Documentation", "License"},
	"zh": {"功能", "安装", "快速开始", "命令行", "从源码构建", "测试", "数据与隐私", "平台状态", "文档", "许可证"},
}

var readmeKeep = []struct {
	Index int
	ID    string
}{{1, "install"}, {2, "quick-start"}, {3, "command-line"}, {4, "build-from-source"}, {6, "data-and-privacy"}, {7, "platform-status"}}

type docContent struct {
	HTML     string
	Headings []heading
}

func readSource(root, file string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

type section struct {
	Title string
	lines []string
}

func (s section) markdown() string { return strings.Join(s.lines, "\n") }

// splitSections splits a README at its "## " headings (outside fenced code).
// The "# " title and any preamble are dropped.
func splitSections(md string) []section {
	var out []section
	fence := byte(0)
	for _, line := range strings.Split(md, "\n") {
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			if fence == 0 {
				fence = m[1][0]
			} else if m[1][0] == fence {
				fence = 0
			}
		}
		switch {
		case fence == 0 && strings.HasPrefix(line, "## "):
			out = append(out, section{Title: strings.TrimSpace(line[3:]), lines: []string{line}})
		case len(out) > 0:
			out[len(out)-1].lines = append(out[len(out)-1].lines, line)
		}
	}
	return out
}

var (
	fenceRE    = regexp.MustCompile("^(`{3,}|~{3,})")
	titleRE    = regexp.MustCompile(`^# .*\n`)
	switcherRE = regexp.MustCompile(`^\s*\[?(English|简体中文)\]?(\([^)]*\))? \| .*\n`)
)

// stripTitle drops the "# " title and the README-style language switcher line
// under it; the page shows its own title and language control.
func stripTitle(md string) (string, error) {
	if !strings.HasPrefix(md, "# ") {
		return "", errors.New(`source does not start with a "# " title`)
	}
	return switcherRE.ReplaceAllString(titleRE.ReplaceAllString(md, ""), ""), nil
}

// pairHeadings checks that a translation has the headings of the English
// document: same count and levels, so the English ids can be reused by position.
func pairHeadings(source string, want, got []heading) error {
	levels := func(hs []heading) []int {
		out := make([]int, len(hs))
		for i, h := range hs {
			out[i] = h.Level
		}
		return out
	}
	if !slices.Equal(levels(want), levels(got)) {
		return fmt.Errorf("%s: headings do not match the English source (levels %v, want %v)", source, levels(got), levels(want))
	}
	return nil
}

// licenseHTML reflows LICENSE into paragraphs. The "MIT License" line is
// the page heading and is not part of the text.
func licenseHTML(text string) (string, error) {
	paras := regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(text), -1)
	reflow := regexp.MustCompile(`\s*\n\s*`)
	for i := range paras {
		paras[i] = reflow.ReplaceAllString(paras[i], " ")
	}
	if len(paras) < 2 || paras[0] != "MIT License" || !strings.HasPrefix(paras[1], "Copyright (c) ") {
		return "", errors.New("LICENSE layout changed")
	}
	var b strings.Builder
	for i, p := range paras[1:] {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == 0 {
			b.WriteString(`<p class="license-copyright">`)
		} else {
			b.WriteString("<p>")
		}
		b.WriteString(escText(p) + "</p>")
	}
	return b.String(), nil
}

// buildDocContent renders the Markdown of a docs page in a language. Chinese
// documents take their heading ids from the English source by position.
func buildDocContent(root string, p docPage, lang language, locales map[string]map[string]string) (docContent, error) {
	if p.Key == "license" {
		text, err := readSource(root, p.Sources["en"])
		if err != nil {
			return docContent{}, err
		}
		h, err := licenseHTML(text)
		return docContent{HTML: h}, err
	}

	contentLang := lang.Code
	source := p.Sources[contentLang]
	srcDir := ""
	if d := path.Dir(source); d != "." {
		srcDir = d
	}
	ctx := newMDContext(lang.Code, srcDir, locales[contentLang], locales[lang.Code])
	text, err := readSource(root, source)
	if err != nil {
		return docContent{}, err
	}

	var en *docContent // the English rendering a translation is paired with
	if lang.Code != "en" {
		e, err := buildDocContent(root, p, languages[0], locales)
		if err != nil {
			return docContent{}, err
		}
		en = &e
	}

	var body string
	if p.Key == "start" {
		wantTitles := readmeTitles[contentLang]
		sections := splitSections(text)
		if len(sections) != len(wantTitles) {
			return docContent{}, fmt.Errorf("%s: sections changed: got %d, want %d", source, len(sections), len(wantTitles))
		}
		for i, s := range sections {
			if s.Title != wantTitles[i] {
				return docContent{}, fmt.Errorf("%s: section %d is %q, want %q", source, i+1, s.Title, wantTitles[i])
			}
		}
		var parts []string
		for _, k := range readmeKeep {
			ctx.fixedIDs = []string{k.ID}
			part, err := ctx.convert([]byte(sections[k.Index].markdown()))
			if err != nil {
				return docContent{}, fmt.Errorf("%s: %w", source, err)
			}
			parts = append(parts, part)
		}
		body = strings.Join(parts, "")
		if n := countLevel(ctx.headings, 2); n != len(readmeKeep) {
			return docContent{}, fmt.Errorf("%s: %d sections on the Get started page, want %d", source, n, len(readmeKeep))
		}
	} else {
		md, err := stripTitle(text)
		if err != nil {
			return docContent{}, fmt.Errorf("%s: %w", source, err)
		}
		if en != nil {
			ctx.paired = true
			for _, h := range en.Headings {
				ctx.idQueue = append(ctx.idQueue, h.ID)
			}
		}
		if body, err = ctx.convert([]byte(md)); err != nil {
			return docContent{}, fmt.Errorf("%s: %w", source, err)
		}
	}
	if en != nil {
		if err := pairHeadings(source, en.Headings, ctx.headings); err != nil {
			return docContent{}, err
		}
	}
	return docContent{HTML: body, Headings: ctx.headings}, nil
}

func countLevel(hs []heading, level int) int {
	n := 0
	for _, h := range hs {
		if h.Level == level {
			n++
		}
	}
	return n
}

type navItem struct {
	URL, Label string
	Current    bool
}

// renderDocs writes every docs page in every language.
func renderDocs(c config, locales map[string]map[string]string, funcs template.FuncMap, year string) error {
	base, err := template.New("docs.html.tmpl").Funcs(funcs).ParseFiles(filepath.Join(c.root, "site", "templates", "docs.html.tmpl"))
	if err != nil {
		return err
	}
	for _, p := range docPages {
		for i, lang := range languages {
			other := languages[1-i]
			content, err := buildDocContent(c.root, p, lang, locales)
			if err != nil {
				return fmt.Errorf("%s docs page %q: %w", lang.Code, p.Key, err)
			}
			tr := func(key string) string { return locales[lang.Code][key] }

			var nav []navItem
			for _, q := range docPages {
				nav = append(nav, navItem{URL: docURL(q, lang), Label: tr(q.NavKey), Current: q.Key == p.Key})
			}
			var toc []heading
			if countLevel(content.Headings, 2) >= 3 {
				for _, h := range content.Headings {
					if h.Level == 2 {
						toc = append(toc, h)
					}
				}
			}
			data := map[string]any{
				"Lang":          lang.Code,
				"HTMLLang":      lang.HTMLLang,
				"OtherLang":     other.Code,
				"OtherHTMLLang": other.HTMLLang,
				"OtherPath":     docURL(p, other),
				"Canonical":     c.baseURL + docURL(p, lang),
				"URLEn":         c.baseURL + docURL(p, languages[0]),
				"URLZh":         c.baseURL + docURL(p, languages[1]),
				"Title":         tr(p.TitleKey),
				"Description":   tr(p.DescKey),
				"Links":         linksFor(lang),
				"Nav":           nav,
				"TOC":           toc,
				"EnglishOnly":   p.EnglishOnly && lang.Code != "en",
				"Content":       template.HTML(strings.TrimRight(content.HTML, "\n")),
				"GitHub":        githubURL,
				"Year":          year,
			}
			t, err := base.Clone()
			if err != nil {
				return err
			}
			code := lang.Code
			t.Funcs(template.FuncMap{"t": func(key string) (string, error) { return lookup(locales, code, key) }})
			dst := filepath.Join(c.out, filepath.FromSlash(strings.TrimPrefix(docURL(p, lang), "/")), "index.html")
			if err := render(t, data, dst); err != nil {
				return fmt.Errorf("%s docs page %q: %w", lang.Code, p.Key, err)
			}
		}
	}
	return nil
}
