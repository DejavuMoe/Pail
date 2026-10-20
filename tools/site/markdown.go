package main

import (
	"bytes"
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// heading is a heading of a rendered document.
type heading struct {
	Level int
	ID    string
	Text  string // plain text, without the brackets of "[Unreleased]"
}

// mdContext carries the state of rendering one Markdown document (or several
// README sections that end up on one page). It is not safe for concurrent use.
type mdContext struct {
	pageLang   string            // language of the page showing it: links to site pages resolve to it
	srcDir     string            // directory of the source file, relative to the repository root
	labels     map[string]string // locale of the text's language: anchor and table names
	pageLabels map[string]string // locale of pageLang: titles of the pages links open

	ids      map[string]bool
	paired   bool     // true: ids come from idQueue (a translation of an English document)
	idQueue  []string // heading ids of the English document, in order
	fixedIDs []string // ids for the next level-2 headings (README sections)
	headings []heading
}

func newMDContext(pageLang, srcDir string, labels, pageLabels map[string]string) *mdContext {
	// Ids that the page chrome already uses.
	ids := map[string]bool{"main": true, "mobile-nav": true, "toc-title": true, "top": true,
		"fc-product": true, "fc-res": true, "fc-proj": true}
	return &mdContext{pageLang: pageLang, srcDir: srcDir, labels: labels, pageLabels: pageLabels, ids: ids}
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// slugify makes an ASCII id from heading text; text without ASCII letters or
// digits gives "".
func slugify(text string) string {
	s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "s-" + s
	}
	return s
}

var (
	bracketedRE = regexp.MustCompile(`^\[(.+)\]$`)
	schemeRE    = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// escText escapes the characters that matter in HTML text.
func escText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// textValue is the text a Text node shows: escapes and entities resolved.
func textValue(b []byte) string {
	return string(util.ResolveNumericReferences(util.ResolveEntityNames(util.UnescapePunctuations(b))))
}

// plainText is the text of a node without any markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				b.WriteString(textValue(c.Segment.Value(src)))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.WriteString(string(c.Value))
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// siteExtension adds the site's HTML renderer and the table parser.
type siteExtension struct{ ctx *mdContext }

func (e siteExtension) Extend(m goldmark.Markdown) {
	extension.Table.Extend(m)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(&siteRenderer{ctx: e.ctx}, 100)))
}

// convert renders Markdown to the HTML structure of the approved prototype.
// Raw HTML and images are errors rather than silently dropped.
func (c *mdContext) convert(md []byte) (string, error) {
	m := goldmark.New(goldmark.WithExtensions(siteExtension{ctx: c}))
	var buf bytes.Buffer
	if err := m.Convert(md, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type siteRenderer struct {
	ctx    *mdContext
	curID  string
	curLvl int
}

func (r *siteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindFencedCodeBlock, r.renderCode)
	reg.Register(ast.KindCodeBlock, r.renderCode)
	reg.Register(ast.KindCodeSpan, r.renderCodeSpan)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindRawHTML, r.renderRaw)
	reg.Register(ast.KindHTMLBlock, r.renderRaw)
	reg.Register(extast.KindTable, r.renderTable)
	reg.Register(extast.KindTableHeader, r.renderTableHeader)
	reg.Register(extast.KindTableRow, r.renderTableRow)
	reg.Register(extast.KindTableCell, r.renderTableCell)
}

func (r *siteRenderer) renderRaw(util.BufWriter, []byte, ast.Node, bool) (ast.WalkStatus, error) {
	return ast.WalkStop, fmt.Errorf("raw HTML is not allowed in included Markdown")
}

func (r *siteRenderer) renderImage(_ util.BufWriter, _ []byte, n ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkStop, fmt.Errorf("image not expected in included content: %s", n.(*ast.Image).Destination)
}

// stripBrackets turns the text of "[Unreleased]" into "Unreleased" in the AST
// and returns the plain text.
func stripBrackets(h *ast.Heading, src []byte) string {
	raw := plainText(h, src)
	if !bracketedRE.MatchString(raw) {
		return raw
	}
	var first, last *ast.Text
	_ = ast.Walk(h, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*ast.Text); ok && entering {
			if first == nil {
				first = t
			}
			last = t
		}
		return ast.WalkContinue, nil
	})
	if first != nil && last != nil && bytes.HasPrefix(first.Segment.Value(src), []byte("[")) && bytes.HasSuffix(last.Segment.Value(src), []byte("]")) {
		first.Segment = first.Segment.WithStart(first.Segment.Start + 1)
		last.Segment = last.Segment.WithStop(last.Segment.Stop - 1)
		return bracketedRE.FindStringSubmatch(raw)[1]
	}
	return raw
}

func (r *siteRenderer) renderHeading(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	h := node.(*ast.Heading)
	c := r.ctx
	if !entering {
		anchor := html.EscapeString(c.labels["docAnchor"])
		fmt.Fprintf(w, ` <a class="heading-anchor" href="#%s" aria-label="%s"><span aria-hidden="true">#</span></a></h%d>`+"\n", r.curID, anchor, r.curLvl)
		return ast.WalkContinue, nil
	}
	if h.Level < 2 || h.Level > 3 {
		return ast.WalkStop, fmt.Errorf("unexpected heading level %d: %s", h.Level, plainText(h, src))
	}
	raw := stripBrackets(h, src)
	var id string
	switch {
	case c.paired:
		if len(c.idQueue) > 0 {
			id, c.idQueue = c.idQueue[0], c.idQueue[1:]
		}
	case h.Level == 2 && len(c.fixedIDs) > 0:
		id, c.fixedIDs = c.fixedIDs[0], c.fixedIDs[1:]
	default:
		id = slugify(raw)
	}
	if id == "" {
		id = fmt.Sprintf("s-%d", len(c.headings)+1)
	}
	unique := id
	for n := 2; c.ids[unique]; n++ {
		unique = fmt.Sprintf("%s-%d", id, n)
	}
	c.ids[unique] = true
	c.headings = append(c.headings, heading{Level: h.Level, ID: unique, Text: raw})
	r.curID, r.curLvl = unique, h.Level
	fmt.Fprintf(w, `<h%d id="%s">`, h.Level, unique)
	return ast.WalkContinue, nil
}

func (r *siteRenderer) renderCode(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var code bytes.Buffer
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		code.Write(seg.Value(src))
	}
	lang := ""
	if f, ok := node.(*ast.FencedCodeBlock); ok {
		lang = string(f.Language(src))
	}
	class := ""
	if lang != "" {
		class = ` class="language-` + html.EscapeString(lang) + `"`
	}
	fmt.Fprintf(w, "<pre tabindex=\"0\"><code%s>%s</code></pre>\n", class, escText(strings.TrimSuffix(code.String(), "\n")))
	return ast.WalkSkipChildren, nil
}

// renderCodeSpan wraps every run of non-space characters of an inline code span
// in <span class="tok">. The stylesheet keeps a token on one line, so the span
// breaks only at spaces and never inside a flag such as --vault-password-stdin.
func (r *siteRenderer) renderCodeSpan(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var code bytes.Buffer
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			return ast.WalkStop, fmt.Errorf("unexpected %s in a code span", c.Kind())
		}
		value := t.Segment.Value(src)
		// A line break inside a span reads as a space.
		if bytes.HasSuffix(value, []byte("\n")) {
			code.Write(value[:len(value)-1])
			code.WriteByte(' ')
		} else {
			code.Write(value)
		}
	}
	_, _ = w.WriteString("<code>")
	for i, tok := range strings.Split(code.String(), " ") {
		if i > 0 {
			_ = w.WriteByte(' ')
		}
		if tok == "" {
			continue
		}
		_, _ = w.WriteString(`<span class="tok">`)
		_, _ = w.Write(util.EscapeHTML([]byte(tok)))
		_, _ = w.WriteString("</span>")
	}
	_, _ = w.WriteString("</code>")
	return ast.WalkSkipChildren, nil
}

func (r *siteRenderer) renderTable(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		label := r.ctx.labels["docTable"]
		if n := len(r.ctx.headings); n > 0 {
			label += ": " + r.ctx.headings[n-1].Text
		}
		fmt.Fprintf(w, "<div class=\"table-wrap\" role=\"group\" aria-label=\"%s\" tabindex=\"0\"><table>\n", html.EscapeString(label))
		return ast.WalkContinue, nil
	}
	if node.ChildCount() > 1 {
		_, _ = w.WriteString("</tbody>\n")
	}
	_, _ = w.WriteString("</table></div>\n")
	return ast.WalkContinue, nil
}

func (r *siteRenderer) renderTableHeader(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<thead>\n<tr>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("</tr>\n</thead>\n")
	if node.Parent().ChildCount() > 1 {
		_, _ = w.WriteString("<tbody>\n")
	}
	return ast.WalkContinue, nil
}

func (r *siteRenderer) renderTableRow(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<tr>\n")
	} else {
		_, _ = w.WriteString("</tr>\n")
	}
	return ast.WalkContinue, nil
}

func (r *siteRenderer) renderTableCell(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	_, inHeader := node.Parent().(*extast.TableHeader)
	tag, attr := "td", ""
	if inHeader {
		tag, attr = "th", ` scope="col"`
	}
	if entering {
		fmt.Fprintf(w, "<%s%s>", tag, attr)
	} else {
		fmt.Fprintf(w, "</%s>\n", tag)
	}
	return ast.WalkContinue, nil
}

// mapHref rewrites a link of the Markdown source for the site. Links to files
// that are site pages open the page in the page language, any other file in
// the repository opens on GitHub; absolute http(s) and mailto links are kept.
// It returns the page when the link names a site page.
func (c *mdContext) mapHref(href string) (string, *docPage, error) {
	if href == "" || strings.HasPrefix(href, "#") {
		return href, nil, nil
	}
	if schemeRE.MatchString(href) || strings.HasPrefix(href, "//") {
		low := strings.ToLower(href)
		if strings.HasPrefix(href, "//") || !(strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "mailto:")) {
			return "", nil, fmt.Errorf("link %q: only http, https and mailto links are allowed", href)
		}
		return href, nil, nil
	}
	rawPath, frag := href, ""
	if i := strings.Index(href, "#"); i >= 0 {
		rawPath, frag = href[:i], href[i:]
	}
	if strings.HasPrefix(rawPath, "/") {
		return "", nil, fmt.Errorf("link %q is absolute inside the repository", href)
	}
	resolved := path.Clean(path.Join(c.srcDir, rawPath))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", nil, fmt.Errorf("link escapes the repository: %s", href)
	}
	if p, ok := sitePageForSource(resolved); ok {
		return docURL(p, languageByCode(c.pageLang)) + frag, &p, nil
	}
	return githubURL + "/blob/master/" + resolved + frag, nil, nil
}

func (r *siteRenderer) renderLink(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	href := string(util.UnescapePunctuations(util.ResolveNumericReferences(util.ResolveEntityNames(n.Destination))))
	to, page, err := r.ctx.mapHref(href)
	if err != nil {
		return ast.WalkStop, err
	}
	_, _ = w.WriteString(`<a href="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape([]byte(to), false)))
	_ = w.WriteByte('"')
	if n.Title != nil {
		fmt.Fprintf(w, ` title="%s"`, html.EscapeString(textValue(n.Title)))
	}
	_ = w.WriteByte('>')
	// A link whose text is the repository path it points to ("docs/cli.md")
	// reads as the page it now opens.
	if page != nil && plainText(n, src) == strings.SplitN(href, "#", 2)[0] {
		title, ok := r.ctx.pageLabels[page.TitleKey]
		if !ok {
			return ast.WalkStop, fmt.Errorf("no locale string %q for the link %s", page.TitleKey, href)
		}
		_, _ = w.WriteString(escText(title))
		return ast.WalkSkipChildren, nil
	}
	return ast.WalkContinue, nil
}
