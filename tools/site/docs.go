package main

import (
	"encoding/json"
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

// docPage is one documentation page. Its text comes from Markdown in the
// repository: the guide in docs/guide/, the command-line reference, the
// changelog and the license (docs/dev/site.md).
type docPage struct {
	Key      string
	Path     string            // URL path below the language root, with trailing slash
	Group    string            // locale key of the navigation group
	Sources  map[string]string // language code -> repository file
	TitleKey string            // locale key of the title; empty: the Markdown's "# " title
	DescKey  string            // locale key of the description; empty: the Markdown's first paragraph
	// EnglishOnly: the text is English on every language's page.
	EnglishOnly bool
}

func guide(key, urlPath, group string) docPage {
	return docPage{Key: key, Path: urlPath, Group: group, Sources: map[string]string{
		"en": "docs/guide/" + key + ".md", "zh": "docs/guide/" + key + ".zh-CN.md"}}
}

var docPages = []docPage{
	guide("index", "docs/", "navStart"),
	guide("install", "docs/install/", "navStart"),
	guide("connect", "docs/connect/", "navStart"),
	guide("browse", "docs/browse/", "navUse"),
	guide("transfers", "docs/transfers/", "navUse"),
	guide("objects", "docs/objects/", "navUse"),
	guide("versions", "docs/versions/", "navUse"),
	guide("buckets", "docs/buckets/", "navUse"),
	guide("links", "docs/links/", "navUse"),
	guide("sync", "docs/sync/", "navUse"),
	guide("shortcuts", "docs/shortcuts/", "navUse"),
	{Key: "cli", Path: "docs/cli/", Group: "navReference", TitleKey: "pageCli", DescKey: "pageCliDesc",
		Sources: map[string]string{"en": "docs/cli.md", "zh": "docs/cli.zh-CN.md"}},
	guide("privacy", "docs/privacy/", "navReference"),
	{Key: "changelog", Path: "changelog/", Group: "navReference", TitleKey: "pageChangelog", DescKey: "pageChangelogDesc",
		Sources: map[string]string{"en": "CHANGELOG.md", "zh": "CHANGELOG.zh-CN.md"}},
	{Key: "license", Path: "license/", Group: "navReference", TitleKey: "pageLicense", DescKey: "pageLicenseDesc",
		Sources: map[string]string{"en": "LICENSE", "zh": "LICENSE"}, EnglishOnly: true},
}

// navGroups is the order of the groups in the docs navigation.
var navGroups = []string{"navStart", "navUse", "navReference"}

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
	Home, Docs, Install, Connect, Transfers, Versions, Links, Sync, Buckets, Objects, Cli, Privacy, Changelog, License string
}

func linksFor(l language) pageLinks {
	u := func(key string) string { return docURL(docPageByKey(key), l) }
	return pageLinks{
		Home: l.Path, Docs: u("index"), Install: u("install"), Connect: u("connect"), Transfers: u("transfers"),
		Versions: u("versions"), Links: u("links"), Sync: u("sync"), Buckets: u("buckets"), Objects: u("objects"), Cli: u("cli"), Privacy: u("privacy"),
		Changelog: u("changelog"), License: u("license"),
	}
}

type docContent struct {
	Title    string
	Lede     string // first paragraph of a guide page, plain text
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

var (
	titleRE    = regexp.MustCompile(`^# (.*)\n`)
	switcherRE = regexp.MustCompile(`^\s*\[?(English|简体中文)\]?(\([^)]*\))? \| .*\n`)
	inlineMDRE = regexp.MustCompile("[`*_\\[\\]<>]")
)

// splitTitle returns the "# " title and the rest without the README-style
// language switcher under it; the page shows its own title and language control.
func splitTitle(md string) (string, string, error) {
	m := titleRE.FindStringSubmatch(md)
	if m == nil {
		return "", "", errors.New(`source does not start with a "# " title`)
	}
	return strings.TrimSpace(m[1]), switcherRE.ReplaceAllString(md[len(m[0]):], ""), nil
}

// splitLede takes the first paragraph of a guide page. It is plain text: the
// page shows it under the title and uses it as the description.
func splitLede(md string) (string, string, error) {
	md = strings.TrimLeft(md, "\n")
	para, rest, _ := strings.Cut(md, "\n\n")
	lede := strings.Join(strings.Fields(para), " ")
	if lede == "" || strings.ContainsAny(lede[:1], "#>|-*0123456789") || inlineMDRE.MatchString(lede) {
		return "", "", fmt.Errorf("a guide page needs one plain-text paragraph under its title, got %q", lede)
	}
	return lede, rest, nil
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
	tr := locales[lang.Code]
	if p.Key == "license" {
		text, err := readSource(root, p.Sources["en"])
		if err != nil {
			return docContent{}, err
		}
		h, err := licenseHTML(text)
		return docContent{Title: tr[p.TitleKey], HTML: h}, err
	}

	source := p.Sources[lang.Code]
	srcDir := ""
	if d := path.Dir(source); d != "." {
		srcDir = d
	}
	ctx := newMDContext(lang.Code, srcDir, tr, tr)
	text, err := readSource(root, source)
	if err != nil {
		return docContent{}, err
	}
	title, md, err := splitTitle(text)
	if err != nil {
		return docContent{}, fmt.Errorf("%s: %w", source, err)
	}
	lede := ""
	if p.TitleKey == "" {
		if lede, md, err = splitLede(md); err != nil {
			return docContent{}, fmt.Errorf("%s: %w", source, err)
		}
		if strings.ContainsAny(title, "`*[]<>") {
			return docContent{}, fmt.Errorf("%s: the title must be plain text", source)
		}
	} else {
		title = tr[p.TitleKey]
	}

	var en *docContent // the English rendering a translation is paired with
	if lang.Code != "en" {
		e, err := buildDocContent(root, p, languages[0], locales)
		if err != nil {
			return docContent{}, err
		}
		en = &e
		ctx.paired = true
		for _, h := range en.Headings {
			ctx.idQueue = append(ctx.idQueue, h.ID)
		}
	}
	body, err := ctx.convert([]byte(md))
	if err != nil {
		return docContent{}, fmt.Errorf("%s: %w", source, err)
	}
	if en != nil {
		if err := pairHeadings(source, en.Headings, ctx.headings); err != nil {
			return docContent{}, err
		}
	}
	return docContent{Title: title, Lede: lede, HTML: body, Headings: ctx.headings}, nil
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

type navGroup struct {
	Label string
	Items []navItem
}

// docSet is every docs page rendered in every language.
type docSet map[string]map[string]docContent // page key -> language code -> content

func buildDocSet(root string, locales map[string]map[string]string) (docSet, error) {
	set := docSet{}
	for _, p := range docPages {
		set[p.Key] = map[string]docContent{}
		for _, lang := range languages {
			c, err := buildDocContent(root, p, lang, locales)
			if err != nil {
				return nil, fmt.Errorf("%s docs page %q: %w", lang.Code, p.Key, err)
			}
			set[p.Key][lang.Code] = c
		}
	}
	return set, nil
}

// searchEntry is one entry of the search palette: a page or a section of it.
type searchEntry struct {
	Page    string `json:"p"`           // page title
	Section string `json:"s,omitempty"` // heading text; empty for the page itself
	Group   string `json:"g"`           // navigation group
	URL     string `json:"u"`
}

// searchIndex lists every page and every "##" and "###" heading of the docs
// in one language, for the search palette of every page.
func searchIndex(set docSet, lang language, locales map[string]map[string]string) ([]byte, error) {
	var out []searchEntry
	for _, p := range docPages {
		c := set[p.Key][lang.Code]
		u := docURL(p, lang)
		g := locales[lang.Code][p.Group]
		out = append(out, searchEntry{Page: c.Title, Group: g, URL: u})
		if p.Key == "changelog" {
			continue // version numbers and "Added" / "Fixed" are not useful targets
		}
		for _, h := range c.Headings {
			out = append(out, searchEntry{Page: c.Title, Section: h.Text, Group: g, URL: u + "#" + h.ID})
		}
	}
	return json.Marshal(out)
}

// renderDocs writes every docs page in every language.
func renderDocs(c config, set docSet, locales map[string]map[string]string, funcs template.FuncMap, common func(language) map[string]any) error {
	base, err := parseTemplates(c.root, funcs, "docs.html.tmpl", "social.html.tmpl", "chrome.html.tmpl")
	if err != nil {
		return err
	}
	for _, p := range docPages {
		for i, lang := range languages {
			other := languages[1-i]
			content := set[p.Key][lang.Code]
			tr := func(key string) string { return locales[lang.Code][key] }

			var groups []navGroup
			var flat []navItem
			for _, g := range navGroups {
				ng := navGroup{Label: tr(g)}
				for _, q := range docPages {
					if q.Group == g {
						it := navItem{URL: docURL(q, lang), Label: set[q.Key][lang.Code].Title, Current: q.Key == p.Key}
						ng.Items = append(ng.Items, it)
						flat = append(flat, it)
					}
				}
				groups = append(groups, ng)
			}
			// The pages are read in the order of the navigation: each one
			// ends with a way to the one before it and the one after.
			var prev, next *navItem
			for n, it := range flat {
				if it.Current {
					if n > 0 {
						prev = &flat[n-1]
					}
					if n < len(flat)-1 {
						next = &flat[n+1]
					}
				}
			}
			var toc []heading
			if countLevel(content.Headings, 2) >= 2 {
				for _, h := range content.Headings {
					if h.Level == 2 {
						toc = append(toc, h)
					}
				}
			}
			desc := content.Lede
			if p.DescKey != "" {
				desc = tr(p.DescKey)
			}
			ld, err := docStructuredData(c.baseURL, lang, content.Title, c.baseURL+docURL(p, lang))
			if err != nil {
				return err
			}
			data := common(lang)
			for k, v := range map[string]any{
				"StructuredData": ld,
				"OtherPath":      docURL(p, other),
				"Canonical":      c.baseURL + docURL(p, lang),
				"URLEn":          c.baseURL + docURL(p, languages[0]),
				"URLZh":          c.baseURL + docURL(p, languages[1]),
				"Title":          content.Title,
				"Lede":           content.Lede,
				"SocialTitle":    content.Title + " · Pail",
				"Description":    desc,
				"PageKey":        p.Key,
				"Group":          tr(p.Group),
				"Nav":            groups,
				"Prev":           prev,
				"Next":           next,
				"TOC":            toc,
				"EnglishOnly":    p.EnglishOnly && lang.Code != "en",
				"Content":        template.HTML(strings.TrimRight(content.HTML, "\n")),
				"SourceURL":      githubURL + "/blob/master/" + p.Sources[lang.Code],
			} {
				data[k] = v
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
