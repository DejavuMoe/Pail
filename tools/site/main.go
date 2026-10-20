// Command site builds the static website https://pail.download from site/:
// one page per language rendered from site/templates with site/locales,
// fingerprinted static files, a 404 page, robots.txt and sitemap.xml.
// See docs/site.md.
//
//	go run ./tools/site -out build/site -version 0.1.0
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const githubURL = "https://github.com/DejavuMoe/Pail"

var versionRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

type language struct {
	Code     string // data-lang value and the stored choice in localStorage
	HTMLLang string // BCP 47 tag for lang and hreflang
	File     string // file under site/locales
	Path     string // URL path of the page
}

var languages = []language{
	{Code: "en", HTMLLang: "en", File: "en.json", Path: "/"},
	{Code: "zh", HTMLLang: "zh-Hans", File: "zh-Hans.json", Path: "/zh/"},
}

// runtimeKeys are the strings app.js builds at runtime (download button, demo
// labels). They are embedded in the landing pages as JSON.
var runtimeKeys = []string{"heroDownloadFor", "miniCopy", "miniCopied"}

// asset is a file served from /assets/ under a content-hashed name.
type asset struct {
	name string // name used by templates
	src  string // path relative to the repository root
	svg  bool   // strip XML comments before publishing
}

var assets = []asset{
	{name: "styles.css", src: "site/static/styles.css"},
	{name: "docs.css", src: "site/static/docs.css"},
	{name: "app.js", src: "site/static/app.js"},
	{name: "docs.js", src: "site/static/docs.js"},
	{name: "pail-icon.svg", src: "assets/brand/pail-icon.svg", svg: true},
	{name: "main-light.png", src: "docs/images/main-light.png"},
	{name: "main-dark.png", src: "docs/images/main-dark.png"},
}

// outputs are the top-level entries a build writes. An existing -out
// directory is replaced only when it holds nothing else.
var outputs = append([]string{"index.html", "zh", "404.html", "assets", "robots.txt", "sitemap.xml"}, docOutputs...)

type config struct {
	root    string // repository root
	out     string
	version string
	baseURL string // scheme and host, no trailing slash
}

func main() {
	var c config
	flag.StringVar(&c.out, "out", "", "output directory (created; a previous build there is replaced)")
	flag.StringVar(&c.version, "version", "", "release version the download links point to, e.g. 0.1.0")
	flag.StringVar(&c.root, "root", "", "repository root (default: found from the working directory)")
	flag.StringVar(&c.baseURL, "base-url", "https://pail.download", "public origin for canonical links and the sitemap")
	flag.Parse()
	if c.root == "" {
		root, err := findRoot()
		if err != nil {
			fail(err)
		}
		c.root = root
	}
	if err := build(c); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "site:", err)
	os.Exit(1)
}

var moduleRE = regexp.MustCompile(`(?m)^module github\.com/DejavuMoe/Pail\r?$`)

// findRoot walks up from the working directory to this module's go.mod.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && moduleRE.Match(b) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found; pass -root")
		}
		dir = parent
	}
}

func build(c config) error {
	if c.out == "" {
		return errors.New("-out is required")
	}
	if !versionRE.MatchString(c.version) {
		return fmt.Errorf("-version %q is not x.y.z or x.y.z-suffix", c.version)
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("-base-url %q must be an https origin", c.baseURL)
	}
	c.baseURL = "https://" + u.Host

	locales, err := loadLocales(c.root)
	if err != nil {
		return err
	}
	year, err := copyrightYear(c.root)
	if err != nil {
		return err
	}
	if err := prepareOut(c.out); err != nil {
		return err
	}
	paths, err := writeAssets(c)
	if err != nil {
		return err
	}

	release := githubURL + "/releases/download/v" + c.version
	funcs := template.FuncMap{
		"asset": func(name string) (string, error) {
			p, ok := paths[name]
			if !ok {
				return "", fmt.Errorf("unknown asset %q", name)
			}
			return p, nil
		},
		"download": func(target string) string {
			return release + "/Pail-v" + c.version + "-" + target
		},
		"tl": func(lang, key string) (string, error) { return lookup(locales, lang, key) },
		// t is bound to the page's language before each render.
		"t": func(string) (string, error) { return "", errors.New("t used outside a page") },
	}

	pageTmpl, err := template.New("index.html.tmpl").Funcs(funcs).ParseFiles(filepath.Join(c.root, "site", "templates", "index.html.tmpl"))
	if err != nil {
		return err
	}
	for i, lang := range languages {
		other := languages[1-i]
		runtime := map[string]string{}
		for _, k := range runtimeKeys {
			runtime[k] = locales[lang.Code][k]
		}
		js, err := json.Marshal(runtime) // escapes <, > and & for use inside <script>
		if err != nil {
			return err
		}
		data := map[string]any{
			"Lang":          lang.Code,
			"HTMLLang":      lang.HTMLLang,
			"OtherLang":     other.Code,
			"OtherHTMLLang": other.HTMLLang,
			"OtherPath":     other.Path,
			"PathZh":        languages[1].Path,
			"Links":         linksFor(lang),
			"Canonical":     c.baseURL + lang.Path,
			"URLEn":         c.baseURL + languages[0].Path,
			"URLZh":         c.baseURL + languages[1].Path,
			"Version":       c.version,
			"GitHub":        githubURL,
			"ReleaseBase":   release,
			"Year":          year,
			"Runtime":       template.JS(js),
		}
		t, err := pageTmpl.Clone()
		if err != nil {
			return err
		}
		code := lang.Code
		t.Funcs(template.FuncMap{"t": func(key string) (string, error) { return lookup(locales, code, key) }})
		if err := render(t, data, filepath.Join(c.out, filepath.FromSlash(strings.TrimPrefix(lang.Path, "/")), "index.html")); err != nil {
			return fmt.Errorf("%s page: %w", lang.Code, err)
		}
	}

	if err := renderDocs(c, locales, funcs, year); err != nil {
		return err
	}

	notFound, err := template.New("404.html.tmpl").Funcs(funcs).ParseFiles(filepath.Join(c.root, "site", "templates", "404.html.tmpl"))
	if err != nil {
		return err
	}
	if err := render(notFound, nil, filepath.Join(c.out, "404.html")); err != nil {
		return fmt.Errorf("404 page: %w", err)
	}

	robots := "User-agent: *\nAllow: /\n\nSitemap: " + c.baseURL + "/sitemap.xml\n"
	if err := os.WriteFile(filepath.Join(c.out, "robots.txt"), []byte(robots), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.out, "sitemap.xml"), sitemap(c.baseURL), 0o644)
}

func lookup(locales map[string]map[string]string, lang, key string) (string, error) {
	s, ok := locales[lang][key]
	if !ok {
		return "", fmt.Errorf("locale %s has no key %q", lang, key)
	}
	return s, nil
}

// loadLocales reads every language and checks that their key sets match and
// that no string is empty.
func loadLocales(root string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	var first []string
	for _, lang := range languages {
		p := filepath.Join(root, "site", "locales", lang.File)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var keys []string
		for k, v := range m {
			if strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("%s: %q is empty", p, k)
			}
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if first == nil {
			first = keys
		} else if !slices.Equal(first, keys) {
			return nil, fmt.Errorf("%s: keys differ from %s", lang.File, languages[0].File)
		}
		for _, k := range runtimeKeys {
			if _, ok := m[k]; !ok {
				return nil, fmt.Errorf("%s: missing runtime key %q", p, k)
			}
		}
		out[lang.Code] = m
	}
	return out, nil
}

var copyrightRE = regexp.MustCompile(`Copyright \(c\) (\d{4}(?:-\d{4})?) `)

// copyrightYear takes the footer year from LICENSE so builds are reproducible.
func copyrightYear(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return "", err
	}
	m := copyrightRE.FindSubmatch(b)
	if m == nil {
		return "", errors.New("LICENSE has no copyright line")
	}
	return string(m[1]), nil
}

// prepareOut creates the output directory, or empties one that holds only a
// previous build, so a mistyped -out never deletes unrelated files.
func prepareOut(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(out, 0o755)
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !slices.Contains(outputs, e.Name()) {
			return fmt.Errorf("%s holds %q, which is not from a site build; choose an empty directory", out, e.Name())
		}
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(out, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

var svgCommentRE = regexp.MustCompile(`(?s)[ \t]*<!--.*?-->\r?\n?`)

// writeAssets copies each asset to assets/<stem>.<hash>.<ext> and returns the
// URL path for each name.
func writeAssets(c config) (map[string]string, error) {
	dir := filepath.Join(c.out, "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, a := range assets {
		b, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(a.src)))
		if err != nil {
			return nil, err
		}
		if a.svg {
			b = svgCommentRE.ReplaceAll(b, nil)
		}
		sum := sha256.Sum256(b)
		ext := path.Ext(a.name)
		name := strings.TrimSuffix(a.name, ext) + "." + hex.EncodeToString(sum[:5]) + ext
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return nil, err
		}
		paths[a.name] = "/assets/" + name
	}
	return paths, nil
}

func render(t *template.Template, data any, dst string) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

func sitemap(base string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	entry := func(path func(language) string) {
		for _, lang := range languages {
			fmt.Fprintf(&b, "  <url>\n    <loc>%s</loc>\n", html.EscapeString(base+path(lang)))
			for _, alt := range languages {
				fmt.Fprintf(&b, "    <xhtml:link rel=\"alternate\" hreflang=\"%s\" href=\"%s\"/>\n", alt.HTMLLang, html.EscapeString(base+path(alt)))
			}
			b.WriteString("  </url>\n")
		}
	}
	entry(func(l language) string { return l.Path })
	for _, p := range docPages {
		entry(func(l language) string { return docURL(p, l) })
	}
	b.WriteString("</urlset>\n")
	return []byte(b.String())
}
