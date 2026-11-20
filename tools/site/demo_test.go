package main

import (
	"encoding/binary"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// What the landing page shows of the product has to be what the product does.
// These tests tie the terminal to the CLI's recorded output (site/cli-demo.json,
// kept current by TestSiteCLIDemo in cmd/pail) and the hero to the captures of
// the desktop app.

func recorded(t *testing.T) cliDemo {
	t.Helper()
	d, err := loadCLIDemo(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTerminalShowsRecordedCLIOutput(t *testing.T) {
	demo := recorded(t)
	out := buildSite(t, "1.2.3")
	for _, page := range []string{"index.html", "zh/index.html"} {
		b := read(t, filepath.Join(out, page))
		// The first example is plain HTML, line by line, in order.
		pos := strings.Index(b, `id="term-out"`)
		if pos < 0 {
			t.Fatalf("%s: no terminal", page)
		}
		for _, l := range demo.Examples[0].Lines {
			i := strings.Index(b[pos:], string(termLine(l)))
			if i < 0 {
				t.Errorf("%s: the terminal does not show %q", page, l.Text)
				continue
			}
			pos += i
		}
		// Every example has a tab and is embedded for home.js, unchanged.
		for _, e := range demo.Examples {
			if !strings.Contains(b, `id="tab-`+e.ID+`"`) {
				t.Errorf("%s: no tab for %q", page, e.ID)
			}
		}
		m := regexp.MustCompile(`(?s)<script type="application/json" id="cli-demo">(.*?)</script>`).FindStringSubmatch(b)
		if m == nil {
			t.Fatalf("%s: the recording is not embedded", page)
		}
		var got cliDemo
		if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &got); err != nil {
			t.Fatalf("%s: embedded recording: %v", page, err)
		}
		a, _ := json.Marshal(got)
		w, _ := json.Marshal(demo)
		if string(a) != string(w) {
			t.Errorf("%s: the embedded recording differs from site/cli-demo.json", page)
		}
	}
}

// home.js picks colours for the examples it renders; the page picks them for
// the first one. Both must choose alike.
func TestTerminalClassesMatchScript(t *testing.T) {
	js := read(t, filepath.Join(repoRoot(t), "site", "static", "home.js"))
	for _, re := range []*regexp.Regexp{reTermUpload, reTermDelete, reTermDryRun, reTermTable} {
		if !strings.Contains(js, "/"+re.String()+"/") {
			t.Errorf("home.js does not test for /%s/ as the generator does", re)
		}
	}
	for _, l := range []cliLine{{"out", "upload   a  (1.0 KiB)  new"}, {"out", "delete   a"}, {"out", "dry run: nothing was changed"}, {"err", "2 objects"}, {"out", "plain"}} {
		if termClass(l) == "" {
			t.Errorf("no class for %+v", l)
		}
	}
}

func TestScriptHasNoHandWrittenExamples(t *testing.T) {
	for _, name := range []string{"home.js", "site.js"} {
		js := read(t, filepath.Join(repoRoot(t), "site", "static", name))
		for _, s := range []string{"pail ls ", "pail sync ", "pail presign ", "s3://", "PRE "} {
			if strings.Contains(js, s) {
				t.Errorf("%s contains %q: the terminal examples come from site/cli-demo.json", name, s)
			}
		}
	}
}

// A recording that is empty, repeats an id or does not start with its own
// command is refused.
func TestLoadCLIDemoRejectsBadRecordings(t *testing.T) {
	for name, body := range map[string]string{
		"empty":     `{"examples":[]}`,
		"bad id":    `{"examples":[{"id":"A b","command":"x","lines":[{"kind":"cmd","text":"x"}]}]}`,
		"repeated":  `{"examples":[{"id":"a","command":"x","lines":[{"kind":"cmd","text":"x"}]},{"id":"a","command":"x","lines":[{"kind":"cmd","text":"x"}]}]}`,
		"no cmd":    `{"examples":[{"id":"a","command":"x","lines":[{"kind":"out","text":"x"}]}]}`,
		"other cmd": `{"examples":[{"id":"a","command":"x","lines":[{"kind":"cmd","text":"y"}]}]}`,
	} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "site"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "site", "cli-demo.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCLIDemo(root); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// pail-desktop --demo exists only in development builds (-tags pail_demo), so
// the site must not send people to it.
func TestNoDemoFlagPromise(t *testing.T) {
	out := buildSite(t, "1.2.3")
	for _, page := range []string{"index.html", "zh/index.html"} {
		if strings.Contains(read(t, filepath.Join(out, page)), "--demo") {
			t.Errorf("%s mentions --demo, which release builds do not have", page)
		}
	}
}

// webpSize reads the canvas size from a WebP header (VP8X, VP8L or VP8).
func webpSize(b []byte) (w, h int, ok bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8X":
		return 1 + int(b[24]) + int(b[25])<<8 + int(b[26])<<16, 1 + int(b[27]) + int(b[28])<<8 + int(b[29])<<16, true
	case "VP8L":
		v := binary.LittleEndian.Uint32(b[21:25])
		return 1 + int(v&0x3FFF), 1 + int(v>>14&0x3FFF), true
	case "VP8 ":
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3FFF), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3FFF), true
	}
	return 0, 0, false
}

// The landing page shows the window in the page's language and theme: four captures,
// the width and height written in the page are the file's, and the READMEs use
// the English and Chinese pair.
func TestHeroScreenshotsFollowLanguageAndTheme(t *testing.T) {
	root := repoRoot(t)
	size := func(name string) (int, int) {
		w, h, ok := webpSize([]byte(read(t, filepath.Join(root, "docs", "images", name))))
		if !ok {
			t.Fatalf("%s: not a WebP", name)
		}
		return w, h
	}
	w0, h0 := size("main-light-en.webp")
	for _, name := range []string{"main-light-zh.webp", "main-dark-en.webp", "main-dark-zh.webp"} {
		if w, h := size(name); w != w0 || h != h0 {
			t.Errorf("%s is %dx%d, the others %dx%d: the layout would jump between themes and languages", name, w, h, w0, h0)
		}
	}
	out := buildSite(t, "1.2.3")
	for _, c := range []struct{ page, lang, other string }{{"index.html", "en", "zh"}, {"zh/index.html", "zh", "en"}} {
		b := read(t, filepath.Join(out, c.page))
		light := regexp.MustCompile(`<img src="(/assets/main-light-` + c.lang + `\.[0-9a-f]{10}\.webp)" width="(\d+)" height="(\d+)"`).FindStringSubmatch(b)
		dark := regexp.MustCompile(`<source media="\(prefers-color-scheme: dark\)" srcset="(/assets/main-dark-` + c.lang + `\.[0-9a-f]{10}\.webp)"`).FindStringSubmatch(b)
		if light == nil || dark == nil {
			t.Errorf("%s: the page does not use the %s light and dark captures", c.page, c.lang)
			continue
		}
		if w, _ := strconv.Atoi(light[2]); w != w0 {
			t.Errorf("%s: width %s, the file is %d", c.page, light[2], w0)
		}
		if h, _ := strconv.Atoi(light[3]); h != h0 {
			t.Errorf("%s: height %s, the file is %d", c.page, light[3], h0)
		}
		for _, p := range []string{light[1], dark[1]} {
			if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(p))); err != nil {
				t.Errorf("%s: %s was not published", c.page, p)
			}
		}
		if strings.Contains(b, "main-light-"+c.other) || strings.Contains(b, "main-dark-"+c.other) {
			t.Errorf("%s refers to the %s captures", c.page, c.other)
		}
	}
	for readme, lang := range map[string]string{"README.md": "en", "README.zh-CN.md": "zh"} {
		b := read(t, filepath.Join(root, readme))
		for _, theme := range []string{"light", "dark"} {
			name := "docs/images/main-" + theme + "-" + lang + ".webp"
			if !strings.Contains(b, name) {
				t.Errorf("%s does not show %s", readme, name)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Errorf("%s: %v", readme, err)
			}
		}
	}
}
