package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/i18n"
)

// The catalogs (locales/en.json, the source, and zh-Hans.json) must say the
// same things: same keys, same arguments, plural cases their language has, and
// the Chinese must follow the style of DESIGN.md. Every key has to be used by
// the code (the generated functions make the compiler check the other way).

func readCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("locales/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("%s.json: %v", lang, err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if strings.HasPrefix(k, "@") {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			t.Fatalf("%s.json: %s is not a string", lang, k)
		}
		out[k] = s
	}
	return out
}

func argNames(t *testing.T, key, msg string) []string {
	t.Helper()
	m, err := i18n.ParseMessage(msg)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	var names []string
	for _, a := range m.Args() {
		names = append(names, a.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func TestCatalogsAgree(t *testing.T) {
	en, zh := readCatalog(t, "en"), readCatalog(t, "zh-Hans")
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("zh-Hans.json lacks %q", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("zh-Hans.json has %q, which en.json lacks", k)
		}
	}
	for k, msg := range en {
		other, ok := zh[k]
		if !ok {
			continue
		}
		if strings.TrimSpace(msg) == "" || strings.TrimSpace(other) == "" {
			t.Errorf("%q is empty in a language", k)
		}
		a, b := argNames(t, k, msg), argNames(t, k, other)
		if !slices.Equal(a, b) {
			t.Errorf("%q: arguments %v in en, %v in zh-Hans", k, a, b)
		}
		pm, _ := i18n.ParseMessage(other)
		for _, arg := range pm.Args() {
			for _, c := range pm.Cases(arg.Name) {
				if c != "other" && !strings.HasPrefix(c, "=") {
					t.Errorf("%q: zh-Hans has plural case %q, Chinese only has other", k, c)
				}
			}
		}
	}
}

var (
	goCall   = regexp.MustCompile(`(?:locales\.|\.)([A-Z][A-Za-z0-9]*)\b`)
	keyRegex = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)
)

func TestEveryCatalogKeyIsUsedByTheCode(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range goCall.FindAllStringSubmatch(string(src), -1) {
			used[m[1]] = true
		}
	}
	for k := range readCatalog(t, "en") {
		if !keyRegex.MatchString(k) {
			t.Errorf("key %q is not camelCase", k)
			continue
		}
		if !used[strings.ToUpper(k[:1])+k[1:]] {
			t.Errorf("key %q is not used by any Go file in cmd/pail-desktop", k)
		}
	}
}

func TestLanguagesOfTheCatalogAreTheOnesTheAppOffers(t *testing.T) {
	got := locales.Catalog.Languages()
	slices.Sort(got)
	want := slices.Clone(appLanguages)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("catalog languages %v, appLanguages %v", got, want)
	}
}

// ---- Chinese style -----------------------------------------------------------

var (
	cjkAscii  = regexp.MustCompile(`[\p{Han}][A-Za-z0-9]|[A-Za-z0-9][\p{Han}]`)
	halfPunct = regexp.MustCompile(`[\p{Han}][,;:?!()]|[,;:?!()][\p{Han}]`)
)

func TestChineseFollowsTheStyleGuide(t *testing.T) {
	for k, msg := range readCatalog(t, "zh-Hans") {
		if strings.Contains(msg, "您") {
			t.Errorf("%s: no 您: %q", k, msg)
		}
		hasHan := false
		for _, r := range msg {
			if unicode.Is(unicode.Han, r) {
				hasHan = true
				break
			}
		}
		if !hasHan {
			continue
		}
		// Look at the text of the cases and outside the ICU braces: replace each
		// ICU expression by its case texts joined, placeholders by N.
		text := flattenICU(msg)
		if m := halfPunct.FindString(text); m != "" {
			t.Errorf("%s: half-width punctuation next to Chinese (%q) in %q", k, m, msg)
		}
		if m := cjkAscii.FindString(text); m != "" {
			t.Errorf("%s: no space between Chinese and Latin or digits (%q) in %q", k, m, msg)
		}
		if strings.HasSuffix(strings.TrimSpace(text), ".") && !strings.HasSuffix(strings.TrimSpace(text), "...") {
			t.Errorf("%s: a Chinese sentence ends with 。, not '.': %q", k, msg)
		}
	}
}

// latinArgs are arguments that hold names or numbers written in Latin letters
// or digits (user data, ids, counts), which Chinese text sets off with a space.
var latinArgs = map[string]bool{"n": true, "count": true, "bucket": true, "key": true, "file": true, "path": true, "url": true,
	"id": true, "version": true, "class": true, "region": true, "prefix": true, "size": true, "total": true}

// flattenICU turns a message into plain text for the style checks: plural and
// select expressions are replaced by each of their cases (separated by a
// newline) and arguments by "N".
func flattenICU(msg string) string {
	var out strings.Builder
	var walk func(s string)
	walk = func(s string) {
		for i := 0; i < len(s); i++ {
			if s[i] == '\'' && i+1 < len(s) && (s[i+1] == '{' || s[i+1] == '}') {
				out.WriteByte(s[i+1])
				i++
				continue
			}
			if s[i] != '{' {
				if s[i] == '#' {
					out.WriteString("N")
				} else {
					out.WriteByte(s[i])
				}
				continue
			}
			depth, j := 1, i+1
			for ; j < len(s) && depth > 0; j++ {
				switch s[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			body := s[i+1 : j-1]
			if strings.Contains(body, ",") && strings.Contains(body, "{") {
				// name, plural|select, case {text} case {text}
				rest := body[strings.Index(body, "{"):]
				for len(rest) > 0 {
					d, k := 0, 0
					for ; k < len(rest); k++ {
						if rest[k] == '{' {
							d++
						} else if rest[k] == '}' {
							d--
							if d == 0 {
								break
							}
						}
					}
					if k >= len(rest) {
						break
					}
					walk(rest[1:k])
					out.WriteByte('\n')
					next := strings.Index(rest[k+1:], "{")
					if next < 0 {
						break
					}
					rest = rest[k+1+next:]
				}
			} else {
				name := strings.TrimSpace(strings.SplitN(body, ",", 2)[0])
				if latinArgs[name] {
					out.WriteString("N")
				} else {
					out.WriteString("▢") // a value that may be Chinese or Latin: no check
				}
			}
			i = j - 1
		}
	}
	walk(msg)
	return out.String()
}
