package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// Rule data of TestCopyRules. The rules are the checkable part of
// docs/dev/writing.md; change the lists there and here together.
var copyRules = struct {
	bannedEN []string // matched case-insensitively at the start of a word
	bannedZH []string // matched as substrings
	internal []string // identifiers that user documents must not mention
	maxToken int      // longest space-free part of an inline code span
}{
	bannedEN: []string{"seamless", "powerful", "effortless", "blazing", "robust", "cutting-edge",
		"state-of-the-art", "game-changing", "supercharge", "unleash", "elevate", "delightful",
		"world-class", "next-generation", "just works"},
	bannedZH: []string{"无缝", "强大的", "轻松", "极致", "一站式", "赋能", "全方位", "丝滑", "完美", "革命性", "颠覆", "神器"},
	internal: []string{"internal/", "s3test", "linux-task", "PAIL_EXT_", "Woodpecker", "CodeQL"},
	maxToken: 40,
}

// copyFiles are the user documents and site locales the rules apply to.
var copyFiles = []struct {
	path string
	zh   bool // Chinese text: the spacing rule applies
}{
	{"README.md", false}, {"README.zh-CN.md", true},
	{"docs/cli.md", false}, {"docs/cli.zh-CN.md", true},
	{"CHANGELOG.md", false}, {"CHANGELOG.zh-CN.md", true},
	{"site/locales/en.json", false}, {"site/locales/zh-Hans.json", true},
}

var (
	bannedENRE = func() *regexp.Regexp {
		parts := make([]string, len(copyRules.bannedEN))
		for i, w := range copyRules.bannedEN {
			parts[i] = strings.ReplaceAll(regexp.QuoteMeta(w), `\ `, `\s+`)
		}
		return regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)\w*`)
	}()

	// copyMarker stands for an inline code span in the text the spacing rule reads.
	copyMarker = "\x00"

	spacingRE   = regexp.MustCompile(`\p{Han}[A-Za-z0-9]|[A-Za-z0-9]\p{Han}`)
	codeEdgeRE  = regexp.MustCompile(`\p{Han}\x00|\x00\p{Han}`)
	htmlCodeRE  = regexp.MustCompile(`(?s)<code[^>]*>.*?</code>`)
	linkDestRE  = regexp.MustCompile(`\]\([^)]*\)`)
	refDefRE    = regexp.MustCompile(`^\s*\[[^\]]+\]:\s*\S+`)
	bareURLRE   = regexp.MustCompile(`<?(?:https?://|mailto:)[^\s)>\]]+>?`)
	htmlTagRE   = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	fenceOpenRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
)

func TestCopyRules(t *testing.T) {
	root := repoRoot(t)
	for _, f := range copyFiles {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
		if err != nil {
			t.Errorf("%s: %v", f.path, err)
			continue
		}
		var report func(rule, where, snippet string)
		report = func(rule, where, snippet string) {
			t.Errorf("%s%s: %s: %s", f.path, where, rule, snippet)
		}
		if strings.HasSuffix(f.path, ".json") {
			var locale map[string]string
			if err := json.Unmarshal(data, &locale); err != nil {
				t.Errorf("%s: %v", f.path, err)
				continue
			}
			keys := make([]string, 0, len(locale))
			for k := range locale {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				where := fmt.Sprintf("[%s]", k)
				checkWords(locale[k], func(rule, snippet string) { report(rule, where, snippet) })
				if f.zh {
					text := htmlCodeRE.ReplaceAllString(locale[k], copyMarker)
					checkSpacing(proseText(text), func(snippet string) { report("spacing", where, snippet) })
				}
			}
			continue
		}
		checkMarkdown(string(data), f.zh, func(rule string, line int, snippet string) {
			report(rule, fmt.Sprintf(":%d", line), snippet)
		})
	}
}

// checkWords applies the banned word and internal identifier rules to text.
func checkWords(text string, report func(rule, snippet string)) {
	for _, m := range bannedENRE.FindAllString(text, -1) {
		report("banned word", m)
	}
	for _, w := range copyRules.bannedZH {
		if strings.Contains(text, w) {
			report("banned word", w)
		}
	}
	low := strings.ToLower(text)
	for _, id := range copyRules.internal {
		if strings.Contains(low, strings.ToLower(id)) {
			report("internal identifier", id)
		}
	}
}

// checkMarkdown applies all rules to one Markdown document.
func checkMarkdown(md string, zh bool, report func(rule string, line int, snippet string)) {
	var fence string // the opening fence while inside a fenced code block
	for i, line := range strings.Split(md, "\n") {
		n := i + 1
		line = strings.TrimSuffix(line, "\r")
		checkWords(line, func(rule, snippet string) { report(rule, n, snippet) })
		if fence != "" {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if m := fenceOpenRE.FindStringSubmatch(line); m != nil {
			fence = m[1]
			continue
		}
		prose, spans := splitCodeSpans(line)
		for _, s := range spans {
			for _, part := range strings.Fields(s) {
				if utf8.RuneCountInString(part) > copyRules.maxToken {
					report("long code token", n, "`"+s+"`")
					break
				}
			}
		}
		if zh {
			checkSpacing(proseText(prose), func(snippet string) { report("spacing", n, snippet) })
		}
	}
}

// splitCodeSpans replaces the inline code spans of a line with copyMarker and
// returns them. A backtick run closes at the next run of the same length; an
// unmatched run is literal text. Spans that continue on the next line are not
// recognised.
func splitCodeSpans(line string) (string, []string) {
	var out strings.Builder
	var spans []string
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			out.WriteString(line[i : i+2])
			i += 2
		case c == '`':
			j := i
			for j < len(line) && line[j] == '`' {
				j++
			}
			run := line[i:j]
			end := -1
			for k := j; k < len(line); {
				if line[k] != '`' {
					k++
					continue
				}
				e := k
				for e < len(line) && line[e] == '`' {
					e++
				}
				if e-k == len(run) {
					end = k
					break
				}
				k = e
			}
			if end < 0 {
				out.WriteString(run)
				i = j
				break
			}
			spans = append(spans, strings.TrimSpace(line[j:end]))
			out.WriteString(copyMarker)
			i = end + len(run)
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String(), spans
}

// proseText removes what the spacing rule must not read: link targets,
// reference definitions, URLs and HTML tags.
func proseText(s string) string {
	s = refDefRE.ReplaceAllString(s, " ")
	s = linkDestRE.ReplaceAllString(s, "]")
	s = bareURLRE.ReplaceAllString(s, " ")
	return htmlTagRE.ReplaceAllString(s, " ")
}

// checkSpacing reports a Han character that touches an ASCII letter, a digit or
// an inline code span without a space. writing.md asks for one space there.
func checkSpacing(prose string, report func(snippet string)) {
	for _, re := range []*regexp.Regexp{spacingRE, codeEdgeRE} {
		for _, loc := range re.FindAllStringIndex(prose, -1) {
			start, end := loc[0], loc[1]
			for k := 0; k < 8 && start > 0; k++ {
				_, w := utf8.DecodeLastRuneInString(prose[:start])
				start -= w
			}
			for k := 0; k < 8 && end < len(prose); k++ {
				_, w := utf8.DecodeRuneInString(prose[end:])
				end += w
			}
			report(strings.ReplaceAll(prose[start:end], copyMarker, "`…`"))
		}
	}
}
